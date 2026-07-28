package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/effchat/internal/model"
)

func TestCompactionSummaryPromptSharedShape(t *testing.T) {
	if !strings.Contains(compactionInstruction, "<analysis>") || !strings.Contains(compactionInstruction, "<summary>") {
		t.Fatal("压缩指令必须保留 analysis/summary 标签约束")
	}
	for _, want := range []string{"Create a detailed continuation summary", "用户的主要诉求与意图", "当前进展", "待办事项"} {
		if !strings.Contains(compactionInstruction, want) {
			t.Fatalf("压缩指令缺少关键章节 %q", want)
		}
	}
}

func TestBuildCompactionSummaryMessage_SharedPostprocess(t *testing.T) {
	msgs := []*model.Message{
		{MessageData: []byte(`{"role":"user","content":"最近用户原文"}`)},
		{MessageData: []byte(`{"role":"assistant","content":"最近助手原文"}`)},
	}
	raw := `<analysis>这里是草稿，不应落库</analysis><summary>1. 用户的主要诉求与意图：继续实现压缩。
6. 当前进展：正在写测试。</summary>`

	got := buildCompactionSummaryMessage(raw, msgs)
	if got.Role != schema.User {
		t.Fatalf("summary role = %v, want user", got.Role)
	}
	if got.Extra["_eino_summarization_content_type"] != "summary" {
		t.Fatalf("summary marker missing: %#v", got.Extra)
	}
	if strings.Contains(got.Content, "这里是草稿") || strings.Contains(got.Content, "<analysis>") {
		t.Fatalf("analysis should be stripped, got %q", got.Content)
	}
	for _, want := range []string{compactionPreamble, "用户的主要诉求与意图", "最近用户原文", "最近助手原文"} {
		if !strings.Contains(got.Content, want) {
			t.Fatalf("summary missing %q: %q", want, got.Content)
		}
	}

	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	if !strings.Contains(string(data), "_eino_summarization_content_type") {
		t.Fatalf("marshaled summary should keep marker: %s", string(data))
	}
}

func TestBuildCompactionSummaryBody_TruncatesOversizedSummary(t *testing.T) {
	raw := "<summary>" + strings.Repeat("长", compactionSummaryMaxChars+100) + "</summary>"
	body := buildCompactionSummaryBody(raw, nil)
	if len([]rune(body)) > compactionSummaryMaxChars+40 {
		t.Fatalf("summary not truncated enough: chars=%d", len([]rune(body)))
	}
	if !strings.Contains(body, "已截断") {
		t.Fatalf("truncated body should explain truncation: %q", body[len(body)-120:])
	}
}

// TestCountContextTokens 验证两条判定口径：上游 usage 基线与本地估算取较大值。
func TestCountContextTokens(t *testing.T) {
	withUsage := func(total int) *schema.Message {
		m := schema.AssistantMessage("ok", nil)
		m.ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{TotalTokens: total}}
		return m
	}

	t.Run("usage 基线含系统提示，远超本地可见文本", func(t *testing.T) {
		// 历史可见文本很短，但上一轮 usage=2000（含大系统提示）。
		msgs := []*schema.Message{
			schema.UserMessage("hi"),
			withUsage(2000),
			schema.UserMessage("再问一句"),
		}
		got := countContextTokens(msgs, 0)
		if got < 2000 {
			t.Errorf("应以 usage 基线 2000 起算，got=%d", got)
		}
	})

	t.Run("usage 缺失时用系统提示+历史本地估算兜底", func(t *testing.T) {
		// 无 usage：1500 字节系统提示按 ~3 字节/token ≈ 500 token。
		instruction := string(make([]byte, 1500))
		msgs := []*schema.Message{schema.UserMessage("hi")}
		got := countContextTokens(msgs, estimateTextTokens(instruction))
		if got < 400 {
			t.Errorf("应反映系统提示规模(≈500)，got=%d", got)
		}
	})

	t.Run("多模态 user 输入计入文本与图片成本", func(t *testing.T) {
		b64 := strings.Repeat("a", 4096)
		msgs := []*schema.Message{{
			Role: schema.User,
			UserInputMultiContent: []schema.MessageInputPart{
				{Type: schema.ChatMessagePartTypeText, Text: "请分析这张图片中的表格"},
				{
					Type: schema.ChatMessagePartTypeImageURL,
					Image: &schema.MessageInputImage{
						MessagePartCommon: schema.MessagePartCommon{
							Base64Data: &b64,
							MIMEType:   "image/png",
						},
						Detail: schema.ImageURLDetailAuto,
					},
				},
			},
		}}
		got := countContextTokens(msgs, 0)
		if got < multimodalInputBaseTokens {
			t.Errorf("多模态图片应计入固定上下文成本，got=%d", got)
		}
		if got > multimodalInputBaseTokens+400 {
			t.Errorf("不应按完整 base64 字符串估算导致过早压缩，got=%d", got)
		}
	})

	t.Run("空输入为 0", func(t *testing.T) {
		if got := countContextTokens(nil, 0); got != 0 {
			t.Errorf("空输入应为 0，got=%d", got)
		}
	})
}

func TestCompressionThresholdUsesAcceptedModelBudget(t *testing.T) {
	einoAgent := &EinoAgent{compressMaxTokens: 128000}
	tests := []struct {
		name string
		req  *ChatRequest
		want int
	}{
		{
			name: "global ceiling remains stricter for a large model",
			req:  &ChatRequest{ContextWindow: 1050000, ModelMaxOutput: 128000},
			want: 128000,
		},
		{
			name: "model window reserves maximum output and five percent safety",
			req:  &ChatRequest{ContextWindow: 128000, ModelMaxOutput: 32768},
			want: 88832,
		},
		{
			name: "session output limit replaces model maximum reserve",
			req:  &ChatRequest{ContextWindow: 128000, ModelMaxOutput: 32768, MaxTokens: 4096},
			want: 117504,
		},
		{
			name: "unknown model window keeps the configured ceiling",
			req:  &ChatRequest{ModelMaxOutput: 8192},
			want: 128000,
		},
		{
			name: "impossible output budget forces preflight before any history",
			req:  &ChatRequest{ContextWindow: 4096, MaxTokens: 4096},
			want: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := einoAgent.compressionThresholdForRequest(tt.req); got != tt.want {
				t.Fatalf("compression threshold = %d, want %d", got, tt.want)
			}
		})
	}

	einoAgent.compressMaxTokens = 0
	if got := einoAgent.compressionThresholdForRequest(&ChatRequest{ContextWindow: 128000, ModelMaxOutput: 8192}); got != 0 {
		t.Fatalf("disabled compression threshold = %d, want 0", got)
	}
}

func TestEstimateMountedToolSchemaTokensTracksRuntimeSurface(t *testing.T) {
	base := estimateMountedToolSchemaTokens(nil)
	withMemory := estimateMountedToolSchemaTokens(map[string]bool{"memory": true})
	withWorkspace := estimateMountedToolSchemaTokens(map[string]bool{
		"memory": true, "file_list": true, "file_search": true, "file_read": true,
		"skill_list": true, "skill_search": true, "skill_read": true, "web_search": true, "web_extract": true,
	})
	if base != 0 || withMemory <= base || withWorkspace <= withMemory {
		t.Fatalf("schema budgets must grow with mounted tools: base=%d memory=%d workspace=%d", base, withMemory, withWorkspace)
	}
}
