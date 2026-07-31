package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/EffChat/internal/filepolicy"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/service"
	"github.com/huoguojun123/EffChat/pkg/streaming"
)

// TestMessageToData_FieldNamesAlignWithDBColumns 验证序列化出的 JSON 字段名
// 与数据库生成列依赖的字段一致（tool_calls / reasoning_content / tool_call_id），
// 否则 has_tool_calls / has_reasoning 生成列会失效。
func TestMessageToData_FieldNamesAlignWithDBColumns(t *testing.T) {
	idx := 0
	assistant := &schema.Message{
		Role:    schema.Assistant,
		Content: "let me search",
		ToolCalls: []schema.ToolCall{
			{
				Index: &idx,
				ID:    "call_1",
				Type:  "function",
				Function: schema.FunctionCall{
					Name:      "web_search",
					Arguments: `{"query":"golang"}`,
				},
			},
		},
		ReasoningContent: "I should look this up",
	}

	data := messageToData(assistant)

	if data["role"] != "assistant" {
		t.Errorf("role = %v, want assistant", data["role"])
	}
	if _, ok := data["tool_calls"]; !ok {
		t.Error("missing tool_calls field — DB has_tool_calls generated column would break")
	}
	if data["reasoning_content"] != "I should look this up" {
		t.Errorf("reasoning_content = %v, want preserved — DB has_reasoning would break", data["reasoning_content"])
	}
	// 验证 tool_calls 是数组且 jsonb_array_length > 0（生成列的判定方式）
	tcs, ok := data["tool_calls"].([]interface{})
	if !ok || len(tcs) != 1 {
		t.Fatalf("tool_calls should be a non-empty array, got %#v", data["tool_calls"])
	}
}

// TestMessageToData_PlainAssistantOmitsToolCalls 验证纯文本助手消息不会带出
// tool_calls 键。eino schema.Message 的 ToolCalls 为 nil 时经 omitempty 应被省略，
// 否则若序列化出 tool_calls:null，会触发 v1 has_tool_calls 生成列对标量取数组长度而
// 整条 INSERT 失败（助手回复静默丢失）。
func TestMessageToData_PlainAssistantOmitsToolCalls(t *testing.T) {
	data := messageToData(&schema.Message{Role: schema.Assistant, Content: "hello"})
	if _, ok := data["tool_calls"]; ok {
		t.Errorf("plain assistant message must not carry tool_calls key, got %#v", data["tool_calls"])
	}
}

// TestSanitizeMessageData_DropsNonArrayToolCalls 验证清洗逻辑：任何非数组的
// tool_calls（JSON null / 对象 / 字符串）都会被删除，从而不破坏数据库生成列；
// 非空数组的 tool_calls 必须原样保留。
func TestSanitizeMessageData_DropsNonArrayToolCalls(t *testing.T) {
	cases := []struct {
		name      string
		toolCalls interface{}
		present   bool // 期望清洗后 tool_calls 键是否仍存在
	}{
		{"json null", nil, false},
		{"object", map[string]interface{}{"a": 1}, false},
		{"string", "oops", false},
		{"empty array kept", []interface{}{}, true},
		{"array kept", []interface{}{map[string]interface{}{"id": "c1"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := map[string]interface{}{"role": "assistant", "content": "x", "tool_calls": tc.toolCalls}
			sanitizeMessageData(data)
			if _, ok := data["tool_calls"]; ok != tc.present {
				t.Errorf("tool_calls present = %v, want %v (value was %#v)", ok, tc.present, tc.toolCalls)
			}
		})
	}
}

func TestHasDisplayableAssistantOutput(t *testing.T) {
	cases := []struct {
		name     string
		messages []map[string]interface{}
		want     bool
	}{
		{
			name:     "empty produced",
			messages: nil,
			want:     false,
		},
		{
			name: "empty assistant",
			messages: []map[string]interface{}{
				{"role": "assistant", "content": "  "},
			},
			want: false,
		},
		{
			name: "tool call only assistant",
			messages: []map[string]interface{}{
				{"role": "assistant", "tool_calls": []interface{}{map[string]interface{}{"id": "call_1"}}},
			},
			want: true,
		},
		{
			name: "thinking only assistant",
			messages: []map[string]interface{}{
				{"role": "assistant", "reasoning_content": "thinking without final answer"},
			},
			want: false,
		},
		{
			name: "normal text assistant",
			messages: []map[string]interface{}{
				{"role": "assistant", "content": "hello"},
			},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasDisplayableAssistantOutput(tc.messages); got != tc.want {
				t.Fatalf("hasDisplayableAssistantOutput() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSearchRuntimeConfigSeparatesSearchAndCrawlerProviders(t *testing.T) {
	searchOnly := service.SearchRuntimeConfig{
		SearchProvider:  "searxng",
		SearchProviders: []string{"searxng"},
	}
	if !hasSearchProvider(searchOnly) {
		t.Fatal("search provider should be configured")
	}
	if len(searchOnly.CrawlerProviders) != 0 {
		t.Fatal("crawler providers should stay unconfigured when only search is configured")
	}

	withCrawler := service.SearchRuntimeConfig{
		CrawlerProviders: []string{"jina"},
	}
	if len(withCrawler.CrawlerProviders) != 1 {
		t.Fatal("crawler provider should be configured")
	}
}

func TestBuildSearchInstructionTreatsWebContentAsUntrusted(t *testing.T) {
	instruction := buildSearchInstruction("", modelbank.SearchDecision{UseApplicationTool: true}, map[string]bool{"web_extract": true})
	if !strings.Contains(instruction, "untrusted reference material") {
		t.Fatalf("instruction missing untrusted content policy: %s", instruction)
	}
}

func TestMaterializeReasoningOnlyAssistantOutput(t *testing.T) {
	messages := []map[string]interface{}{
		{"role": "assistant", "reasoning_content": "thinking without final answer"},
	}

	fallback, ok := materializeReasoningOnlyAssistantOutput(messages)
	if !ok {
		t.Fatal("materializeReasoningOnlyAssistantOutput() ok = false, want true")
	}
	if fallback == "" {
		t.Fatal("fallback should not be empty")
	}
	if messages[0]["content"] != fallback {
		t.Fatalf("content = %#v, want fallback %q", messages[0]["content"], fallback)
	}
	if !hasDisplayableAssistantOutput(messages) {
		t.Fatal("materialized assistant should be displayable")
	}
}

func TestMaterializeReasoningOnlyAssistantOutput_DoesNotMaskEmptyOutput(t *testing.T) {
	messages := []map[string]interface{}{
		{"role": "assistant", "content": "  "},
	}

	if _, ok := materializeReasoningOnlyAssistantOutput(messages); ok {
		t.Fatal("materializeReasoningOnlyAssistantOutput() ok = true, want false")
	}
}

func TestNewModelEmptyResponseErrorIsStructured(t *testing.T) {
	err := newModelEmptyResponseError("claude", "claude-sonnet-x", "stop", &Usage{PromptTokens: 10})
	runtimeErr, ok := err.(*RuntimeError)
	if !ok {
		t.Fatalf("error type = %T, want *RuntimeError", err)
	}
	if runtimeErr.Code != "model_empty_response" {
		t.Fatalf("code = %q, want model_empty_response", runtimeErr.Code)
	}
	if runtimeErr.Provider != "claude" || runtimeErr.ModelID != "claude-sonnet-x" || runtimeErr.FinishReason != "stop" {
		t.Fatalf("runtime metadata = provider:%q model:%q finish:%q", runtimeErr.Provider, runtimeErr.ModelID, runtimeErr.FinishReason)
	}
	if runtimeErr.Error() == "agent produced no assistant output" {
		t.Fatal("empty response must not expose the old generic error")
	}
}

func TestConvertToEinoMessages_DropsReasoningOnlyAssistant(t *testing.T) {
	raw, err := json.Marshal(&schema.Message{
		Role:             schema.Assistant,
		ReasoningContent: "thinking without final answer",
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := convertToEinoMessages([]*model.Message{{ID: 1, MessageData: raw}}, true)
	if err != nil {
		t.Fatalf("convertToEinoMessages: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d messages, want 0: %#v", len(got), got)
	}
}

// TestToolResultMessage_FieldNames 验证工具结果消息序列化字段名。
func TestToolResultMessage_FieldNames(t *testing.T) {
	toolMsg := &schema.Message{
		Role:       schema.Tool,
		Content:    `{"results":[]}`,
		ToolCallID: "call_1",
		ToolName:   "web_search",
	}
	data := messageToData(toolMsg)

	if data["role"] != "tool" {
		t.Errorf("role = %v, want tool", data["role"])
	}
	if data["tool_call_id"] != "call_1" {
		t.Errorf("tool_call_id = %v, want call_1", data["tool_call_id"])
	}
	if data["tool_name"] != "web_search" {
		t.Errorf("tool_name = %v, want web_search", data["tool_name"])
	}
}

// TestConvertToEinoMessages_RoundTrip 验证存库消息能忠实还原为 schema.Message，
// 包括 tool_calls 与 reasoning_content，保证工具调用链跨请求可回放。
func TestConvertToEinoMessages_RoundTrip(t *testing.T) {
	idx := 0
	original := &schema.Message{
		Role:    schema.Assistant,
		Content: "answer",
		ToolCalls: []schema.ToolCall{
			{Index: &idx, ID: "c1", Type: "function", Function: schema.FunctionCall{Name: "web_search", Arguments: `{"query":"x"}`}},
		},
		ReasoningContent: "thinking",
	}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	toolRaw, _ := json.Marshal(&schema.Message{
		Role:       schema.Tool,
		Content:    `{"result":"ok"}`,
		ToolCallID: "c1",
		ToolName:   "web_search",
	})

	dbMsgs := []*model.Message{
		{ID: 1, MessageData: raw},
		{ID: 2, MessageData: toolRaw},
	}

	got, err := convertToEinoMessages(dbMsgs, true)
	if err != nil {
		t.Fatalf("convertToEinoMessages: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2", len(got))
	}
	m := got[0]
	if m.Role != schema.Assistant {
		t.Errorf("role = %v, want assistant", m.Role)
	}
	if m.Content != "answer" {
		t.Errorf("content = %q, want answer", m.Content)
	}
	if len(m.ToolCalls) != 1 || m.ToolCalls[0].ID != "c1" {
		t.Errorf("tool_calls not preserved: %#v", m.ToolCalls)
	}
	if m.ToolCalls[0].Function.Name != "web_search" {
		t.Errorf("tool call name = %q, want web_search", m.ToolCalls[0].Function.Name)
	}
	// 历史 assistant 的 reasoning 在喂回模型前被剥离（省 token，避免网关回灌报错）。
	if m.ReasoningContent != "" {
		t.Errorf("reasoning_content = %q, want stripped (empty)", m.ReasoningContent)
	}
}

// TestConvertToEinoMessages_ToolResultRoundTrip 验证工具结果消息回放。
func TestConvertToEinoMessages_ToolResultRoundTrip(t *testing.T) {
	idx := 0
	assistant := &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{
			{Index: &idx, ID: "c1", Type: "function", Function: schema.FunctionCall{Name: "web_search", Arguments: `{"query":"x"}`}},
		},
	}
	tool := &schema.Message{
		Role:       schema.Tool,
		Content:    "search output",
		ToolCallID: "c1",
		ToolName:   "web_search",
	}
	assistantRaw, _ := json.Marshal(assistant)
	toolRaw, _ := json.Marshal(tool)

	got, err := convertToEinoMessages([]*model.Message{
		{ID: 1, MessageData: assistantRaw},
		{ID: 2, MessageData: toolRaw},
	}, true)
	if err != nil {
		t.Fatalf("convertToEinoMessages: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2", len(got))
	}
	m := got[1]
	if m.Role != schema.Tool {
		t.Errorf("role = %v, want tool", m.Role)
	}
	if m.ToolCallID != "c1" {
		t.Errorf("tool_call_id = %q, want c1", m.ToolCallID)
	}
}

func TestConvertToEinoMessages_SplitsParallelToolReplay(t *testing.T) {
	mk := func(msg *schema.Message) *model.Message {
		raw, _ := json.Marshal(msg)
		return &model.Message{MessageData: raw}
	}
	idx0 := 0
	idx1 := 1

	got, err := convertToEinoMessages([]*model.Message{
		mk(schema.UserMessage("what font does Claude use?")),
		mk(&schema.Message{
			Role:    schema.Assistant,
			Content: "I will check.",
			ToolCalls: []schema.ToolCall{
				{Index: &idx0, ID: "call_a", Type: "function", Function: schema.FunctionCall{Name: "web_search", Arguments: `{"query":"Claude font"}`}},
				{Index: &idx1, ID: "call_b", Type: "function", Function: schema.FunctionCall{Name: "web_extract", Arguments: `{"url":"https://example.com"}`}},
			},
		}),
		mk(&schema.Message{Role: schema.Tool, ToolCallID: "call_a", ToolName: "web_search", Content: `{"result":"a"}`}),
		mk(&schema.Message{Role: schema.Tool, ToolCallID: "call_b", ToolName: "web_extract", Content: `{"result":"b"}`}),
		mk(schema.AssistantMessage("done", nil)),
	}, true)
	if err != nil {
		t.Fatalf("convertToEinoMessages: %v", err)
	}

	if len(got) != 6 {
		t.Fatalf("got %d messages, want 6", len(got))
	}
	if got[2].Role != schema.Tool || got[2].ToolCallID != "call_a" {
		t.Fatalf("message 2 = role %s tool_call_id %q, want tool call_a", got[2].Role, got[2].ToolCallID)
	}
	if got[1].Role != schema.Assistant || len(got[1].ToolCalls) != 1 || got[1].ToolCalls[0].ID != "call_a" {
		t.Fatalf("message 1 should be assistant with call_a, got role %s calls %#v", got[1].Role, got[1].ToolCalls)
	}
	if got[4].Role != schema.Tool || got[4].ToolCallID != "call_b" {
		t.Fatalf("message 4 = role %s tool_call_id %q, want tool call_b", got[4].Role, got[4].ToolCallID)
	}
	if got[3].Role != schema.Assistant || len(got[3].ToolCalls) != 1 || got[3].ToolCalls[0].ID != "call_b" {
		t.Fatalf("message 3 should be assistant with call_b, got role %s calls %#v", got[3].Role, got[3].ToolCalls)
	}
	if got[3].Content != "" {
		t.Fatalf("synthetic assistant content = %q, want empty", got[3].Content)
	}
}

func TestConvertToEinoMessages_ReordersRepeatedParallelToolReplay(t *testing.T) {
	mk := func(msg *schema.Message) *model.Message {
		raw, _ := json.Marshal(msg)
		return &model.Message{MessageData: raw}
	}
	idx0 := 0
	idx1 := 1
	callA := schema.ToolCall{Index: &idx0, ID: "call_a", Type: "function", Function: schema.FunctionCall{Name: "web_search", Arguments: `{"query":"a"}`}}
	callB := schema.ToolCall{Index: &idx1, ID: "call_b", Type: "function", Function: schema.FunctionCall{Name: "web_search", Arguments: `{"query":"b"}`}}
	callC := schema.ToolCall{Index: &idx0, ID: "call_c", Type: "function", Function: schema.FunctionCall{Name: "web_extract", Arguments: `{"url":"https://c.test"}`}}
	callD := schema.ToolCall{Index: &idx1, ID: "call_d", Type: "function", Function: schema.FunctionCall{Name: "web_extract", Arguments: `{"url":"https://d.test"}`}}

	got, err := convertToEinoMessages([]*model.Message{
		mk(schema.UserMessage("news")),
		mk(&schema.Message{Role: schema.Assistant, Content: "searching", ToolCalls: []schema.ToolCall{callA, callB}}),
		mk(&schema.Message{Role: schema.Tool, ToolCallID: "call_b", ToolName: "web_search", Content: `{"result":"b"}`}),
		mk(&schema.Message{Role: schema.Tool, ToolCallID: "call_a", ToolName: "web_search", Content: `{"result":"a"}`}),
		mk(&schema.Message{Role: schema.Assistant, Content: "extracting", ToolCalls: []schema.ToolCall{callC, callD}}),
		mk(&schema.Message{Role: schema.Tool, ToolCallID: "call_d", ToolName: "web_extract", Content: `{"result":"d"}`}),
		mk(&schema.Message{Role: schema.Tool, ToolCallID: "call_c", ToolName: "web_extract", Content: `{"result":"c"}`}),
		mk(schema.AssistantMessage("done", nil)),
		mk(schema.UserMessage("next")),
	}, true)
	if err != nil {
		t.Fatalf("convertToEinoMessages: %v", err)
	}

	want := []struct {
		role       schema.RoleType
		toolCallID string
	}{
		{schema.User, ""},
		{schema.Assistant, "call_a"},
		{schema.Tool, "call_a"},
		{schema.Assistant, "call_b"},
		{schema.Tool, "call_b"},
		{schema.Assistant, "call_c"},
		{schema.Tool, "call_c"},
		{schema.Assistant, "call_d"},
		{schema.Tool, "call_d"},
		{schema.Assistant, ""},
		{schema.User, ""},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d messages, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Role != w.role {
			t.Fatalf("message %d role = %s, want %s", i, got[i].Role, w.role)
		}
		if w.role == schema.Assistant && w.toolCallID != "" {
			if len(got[i].ToolCalls) != 1 || got[i].ToolCalls[0].ID != w.toolCallID {
				t.Fatalf("message %d assistant calls = %#v, want %s", i, got[i].ToolCalls, w.toolCallID)
			}
			continue
		}
		if got[i].ToolCallID != w.toolCallID {
			t.Fatalf("message %d tool_call_id = %q, want %q", i, got[i].ToolCallID, w.toolCallID)
		}
	}
}

func TestConvertToEinoMessages_DropsOrphanToolReplay(t *testing.T) {
	rawTool, _ := json.Marshal(&schema.Message{Role: schema.Tool, ToolCallID: "orphan", ToolName: "web_search", Content: `{"result":"x"}`})
	rawUser, _ := json.Marshal(schema.UserMessage("hello"))

	got, err := convertToEinoMessages([]*model.Message{
		{MessageData: rawTool},
		{MessageData: rawUser},
	}, true)
	if err != nil {
		t.Fatalf("convertToEinoMessages: %v", err)
	}
	if len(got) != 1 || got[0].Role != schema.User {
		t.Fatalf("orphan tool should be dropped from model history, got %#v", got)
	}
}

func TestCanonicalizeProducedMessages_ReordersParallelToolResults(t *testing.T) {
	produced := []map[string]interface{}{
		{"role": "assistant", "content": "searching", "tool_calls": []interface{}{
			map[string]interface{}{"id": "call_a", "type": "function"},
			map[string]interface{}{"id": "call_b", "type": "function"},
		}},
		{"role": "tool", "tool_call_id": "call_b", "content": `{"result":"b"}`},
		{"role": "tool", "tool_call_id": "call_a", "content": `{"result":"a"}`},
		{"role": "assistant", "content": "done"},
	}

	got := canonicalizeProducedMessages(produced)
	wantRoles := []string{"assistant", "tool", "assistant", "tool", "assistant"}
	wantToolIDs := []string{"call_a", "call_a", "call_b", "call_b", ""}
	if len(got) != len(wantRoles) {
		t.Fatalf("got %d messages, want %d: %#v", len(got), len(wantRoles), got)
	}
	for i := range wantRoles {
		if got[i]["role"] != wantRoles[i] {
			t.Fatalf("message %d role = %v, want %s", i, got[i]["role"], wantRoles[i])
		}
		if got[i]["role"] == "assistant" && wantToolIDs[i] != "" {
			calls, ok := got[i]["tool_calls"].([]interface{})
			if !ok || len(calls) != 1 {
				t.Fatalf("message %d tool_calls = %#v", i, got[i]["tool_calls"])
			}
			call, ok := calls[0].(map[string]interface{})
			if !ok || call["id"] != wantToolIDs[i] {
				t.Fatalf("message %d tool call = %#v, want %s", i, calls[0], wantToolIDs[i])
			}
		}
		if got[i]["role"] == "tool" && got[i]["tool_call_id"] != wantToolIDs[i] {
			t.Fatalf("message %d tool_call_id = %v, want %s", i, got[i]["tool_call_id"], wantToolIDs[i])
		}
	}
	if got[2]["content"] != "" {
		t.Fatalf("second synthetic assistant content = %q, want empty", got[2]["content"])
	}
}

// TestConvertToEinoMessages_InvalidJSON 验证坏数据返回错误而非 panic。
func TestConvertToEinoMessages_InvalidJSON(t *testing.T) {
	_, err := convertToEinoMessages([]*model.Message{{ID: 3, MessageData: []byte("{not json")}}, true)
	if err == nil {
		t.Error("expected error on invalid JSON, got nil")
	}
}

// TestConvertToEinoMessages_MergesConsecutiveUsers 复现 session 324 的故障：
// 用户连发多条而无助手回复，历史里出现连续 user 消息，deepseek 等网关返回空补全。
// 合并后应当只剩一条 user 消息，内容按空行拼接。
func TestConvertToEinoMessages_MergesConsecutiveUsers(t *testing.T) {
	mk := func(role schema.RoleType, content string) *model.Message {
		raw, _ := json.Marshal(&schema.Message{Role: role, Content: content})
		return &model.Message{MessageData: raw}
	}
	got, err := convertToEinoMessages([]*model.Message{
		mk(schema.User, "你好"),
		mk(schema.User, "？"),
		mk(schema.User, ""), // 空内容不应产生多余空行
		mk(schema.User, "[附件：x.pdf]\n这是啥"),
	}, true)
	if err != nil {
		t.Fatalf("convertToEinoMessages: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1 merged user message", len(got))
	}
	if got[0].Role != schema.User {
		t.Errorf("role = %v, want user", got[0].Role)
	}
	want := "你好\n\n？\n\n[附件：x.pdf]\n这是啥"
	if got[0].Content != want {
		t.Errorf("merged content = %q, want %q", got[0].Content, want)
	}
}

// TestConvertToEinoMessages_PreservesAlternation 确保正常交替对话不被改动。
func TestConvertToEinoMessages_PreservesAlternation(t *testing.T) {
	mk := func(role schema.RoleType, content string) *model.Message {
		raw, _ := json.Marshal(&schema.Message{Role: role, Content: content})
		return &model.Message{MessageData: raw}
	}
	got, err := convertToEinoMessages([]*model.Message{
		mk(schema.User, "a"),
		mk(schema.Assistant, "b"),
		mk(schema.User, "c"),
	}, true)
	if err != nil {
		t.Fatalf("convertToEinoMessages: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d messages, want 3 (no merge)", len(got))
	}
}

// mkImageMsg 构造一条带 _image_parts 的 user 消息，指向一个真实受管图片文件。
func mkImageMsg(t *testing.T, content, filename string) *model.Message {
	t.Helper()
	dir := filepath.Join(filepolicy.AttachmentOriginalsRoot, "test-eino-images", strings.ReplaceAll(t.Name(), "/", "_"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, filename)
	imageData := []byte("\x89PNG\r\n\x1a\nfake-image-bytes")
	if err := os.WriteFile(path, imageData, 0644); err != nil {
		t.Fatal(err)
	}
	data := map[string]interface{}{
		"role":    "user",
		"content": content,
		"_image_parts": []map[string]interface{}{
			{"file_id": 1, "file_type": "image/png", "file_path": path, "filename": filename, "file_size": len(imageData)},
		},
	}
	raw, _ := json.Marshal(data)
	return &model.Message{ID: 1, MessageData: raw}
}

// 模型支持 vision：图片读盘转 base64 进 UserInputMultiContent，文本作为 text 部分一并保留。
func TestConvertToEinoMessages_ImageVisionCapable(t *testing.T) {
	got, err := convertToEinoMessages([]*model.Message{mkImageMsg(t, "看这张图", "pic.png")}, true)
	if err != nil {
		t.Fatalf("convertToEinoMessages: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1", len(got))
	}
	if len(got[0].UserInputMultiContent) != 2 {
		t.Fatalf("UserInputMultiContent len = %d, want 2 (text + image)", len(got[0].UserInputMultiContent))
	}
	if len(got[0].MultiContent) != 0 {
		t.Fatalf("deprecated MultiContent len = %d, want 0", len(got[0].MultiContent))
	}
	// openai/gemini 适配器都不应同时收到 Content 与多模态字段：进入多模态后 Content 必须清空。
	if got[0].Content != "" {
		t.Errorf("Content = %q, want empty when UserInputMultiContent is set", got[0].Content)
	}
	if got[0].UserInputMultiContent[0].Type != schema.ChatMessagePartTypeText || got[0].UserInputMultiContent[0].Text != "看这张图" {
		t.Errorf("first part = %+v, want text 看这张图", got[0].UserInputMultiContent[0])
	}
	img := got[0].UserInputMultiContent[1]
	if img.Type != schema.ChatMessagePartTypeImageURL || img.Image == nil {
		t.Fatalf("second part not image_url: %+v", img)
	}
	if img.Image.MIMEType != "image/png" {
		t.Errorf("image MIMEType = %q, want image/png", img.Image.MIMEType)
	}
	if img.Image.Base64Data == nil || *img.Image.Base64Data == "" {
		t.Fatalf("image Base64Data should be set")
	}
}

func TestConvertToEinoMessages_ImageOnlyAddsDefaultPrompt(t *testing.T) {
	got, err := convertToEinoMessages([]*model.Message{mkImageMsg(t, "", "pic.png")}, true)
	if err != nil {
		t.Fatalf("convertToEinoMessages: %v", err)
	}
	if len(got[0].UserInputMultiContent) != 2 {
		t.Fatalf("UserInputMultiContent len = %d, want 2 (default prompt + image)", len(got[0].UserInputMultiContent))
	}
	if got[0].UserInputMultiContent[0].Text != "Analyze this image and answer in the conversation language. Default to Chinese." {
		t.Errorf("default prompt = %q", got[0].UserInputMultiContent[0].Text)
	}
}

// 模型不支持 vision：不读盘、不产生多模态字段，content 末尾追加不支持提示。
func TestConvertToEinoMessages_ImageVisionIncapable(t *testing.T) {
	got, err := convertToEinoMessages([]*model.Message{mkImageMsg(t, "看这张图", "pic.png")}, false)
	if err != nil {
		t.Fatalf("convertToEinoMessages: %v", err)
	}
	if len(got[0].MultiContent) != 0 {
		t.Errorf("MultiContent should be empty for non-vision model, got %d", len(got[0].MultiContent))
	}
	if len(got[0].UserInputMultiContent) != 0 {
		t.Errorf("UserInputMultiContent should be empty for non-vision model, got %d", len(got[0].UserInputMultiContent))
	}
	if !strings.Contains(got[0].Content, "cannot inspect images") {
		t.Errorf("content = %q, want cannot inspect images note", got[0].Content)
	}
}

func TestConvertToEinoMessages_SendsOnlyLatestUserImages(t *testing.T) {
	messages := make([]*model.Message, 0, filepolicy.MaxVisionImages+1)
	for index := 0; index <= filepolicy.MaxVisionImages; index++ {
		messages = append(messages, mkImageMsg(t, fmt.Sprintf("image-%d", index), fmt.Sprintf("pic-%d.png", index)))
	}
	got, err := convertToEinoMessages(messages, true)
	if err != nil {
		t.Fatalf("convertToEinoMessages: %v", err)
	}
	imageParts := 0
	for _, part := range got[0].UserInputMultiContent {
		if part.Type == schema.ChatMessagePartTypeImageURL {
			imageParts++
		}
	}
	if imageParts != 1 {
		t.Fatalf("vision image parts = %d, want only current 1", imageParts)
	}
	text := ""
	for _, part := range got[0].UserInputMultiContent {
		if part.Type == schema.ChatMessagePartTypeText {
			text += part.Text
		}
	}
	if strings.Contains(text, "was omitted") {
		t.Fatalf("historical images should not add omission notes, text=%q", text)
	}
}

func TestConvertToEinoMessages_DoesNotResendHistoricalImagesForTextFollowUp(t *testing.T) {
	image := mkImageMsg(t, "first image", "first.png")
	followUp := &model.Message{ID: 2, MessageData: []byte(`{"role":"user","content":"继续解释"}`)}
	got, err := convertToEinoMessages([]*model.Message{image, followUp}, true)
	if err != nil {
		t.Fatalf("convertToEinoMessages: %v", err)
	}
	for _, message := range got {
		for _, part := range message.UserInputMultiContent {
			if part.Type == schema.ChatMessagePartTypeImageURL {
				t.Fatal("historical image was resent for text-only follow-up")
			}
		}
	}
}

func TestConvertToEinoMessages_ReadsLegacyImageWithoutStoredSize(t *testing.T) {
	message := mkImageMsg(t, "legacy image", "legacy.png")
	var data map[string]interface{}
	if err := json.Unmarshal(message.MessageData, &data); err != nil {
		t.Fatal(err)
	}
	data["_image_parts"].([]interface{})[0].(map[string]interface{})["file_size"] = nil
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	message.MessageData = raw

	got, err := convertToEinoMessages([]*model.Message{message}, true)
	if err != nil {
		t.Fatalf("convertToEinoMessages: %v", err)
	}
	for _, part := range got[0].UserInputMultiContent {
		if part.Type == schema.ChatMessagePartTypeImageURL {
			return
		}
	}
	t.Fatal("legacy image was omitted")
}

// TestConsumeAssistantEvent_StreamingReassembly 验证生产中的流式路径：
// 逐帧 Recv → 发出 content_delta/thinking_delta → ConcatMessages 还原完整消息，
// tool_calls 跨帧合并、reasoning 拼接、usage 保留。
func TestConsumeAssistantEvent_StreamingReassembly(t *testing.T) {
	idx := 0
	chunks := []*schema.Message{
		{Role: schema.Assistant, ReasoningContent: "let me "},
		{Role: schema.Assistant, ReasoningContent: "think"},
		{Role: schema.Assistant, Content: "Hello"},
		{Role: schema.Assistant, Content: " world"},
		{
			Role: schema.Assistant,
			ToolCalls: []schema.ToolCall{
				{Index: &idx, ID: "c1", Type: "function", Function: schema.FunctionCall{Name: "web_search", Arguments: `{"q":"x"}`}},
			},
			ResponseMeta: &schema.ResponseMeta{
				FinishReason: "tool_calls",
				Usage: &schema.TokenUsage{
					PromptTokens:     10,
					CompletionTokens: 5,
					TotalTokens:      15,
					PromptTokenDetails: schema.PromptTokenDetails{
						CachedTokens: 6,
					},
					CompletionTokensDetails: schema.CompletionTokensDetails{
						ReasoningTokens: 2,
					},
				},
			},
		},
	}

	mv := &adk.MessageVariant{
		IsStreaming:   true,
		MessageStream: schema.StreamReaderFromArray(chunks),
		Role:          schema.Assistant,
	}

	var contentDeltas, thinkingDeltas string
	emit := func(event string, data interface{}) error {
		switch event {
		case streaming.EventContentDelta:
			contentDeltas += data.(streaming.ContentDeltaEvent).Delta
		case streaming.EventThinkingDelta:
			thinkingDeltas += data.(streaming.ThinkingDeltaEvent).Delta
		}
		return nil
	}

	a := &EinoAgent{}
	full, err := a.consumeAssistantEvent(t.Context(), mv, emit)
	if err != nil {
		t.Fatalf("consumeAssistantEvent: %v", err)
	}
	if full == nil {
		t.Fatal("expected reassembled message, got nil")
	}

	// 流式 delta 顺序与内容
	if thinkingDeltas != "let me think" {
		t.Errorf("thinking deltas = %q, want %q", thinkingDeltas, "let me think")
	}
	if contentDeltas != "Hello world" {
		t.Errorf("content deltas = %q, want %q", contentDeltas, "Hello world")
	}

	// 还原后的完整消息
	if full.Content != "Hello world" {
		t.Errorf("full content = %q, want %q", full.Content, "Hello world")
	}
	if full.ReasoningContent != "let me think" {
		t.Errorf("full reasoning = %q, want %q", full.ReasoningContent, "let me think")
	}
	if len(full.ToolCalls) != 1 || full.ToolCalls[0].ID != "c1" {
		t.Errorf("tool_calls not preserved across stream: %#v", full.ToolCalls)
	}
	if full.ResponseMeta == nil || full.ResponseMeta.Usage == nil {
		t.Fatal("response_meta/usage lost in reassembly")
	}
	if full.ResponseMeta.Usage.TotalTokens != 15 {
		t.Errorf("total tokens = %d, want 15", full.ResponseMeta.Usage.TotalTokens)
	}
	if full.ResponseMeta.Usage.PromptTokenDetails.CachedTokens != 6 {
		t.Errorf("cached tokens = %d, want 6", full.ResponseMeta.Usage.PromptTokenDetails.CachedTokens)
	}
	if full.ResponseMeta.Usage.CompletionTokensDetails.ReasoningTokens != 2 {
		t.Errorf("reasoning tokens = %d, want 2", full.ResponseMeta.Usage.CompletionTokensDetails.ReasoningTokens)
	}
	if full.ResponseMeta.FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %q, want tool_calls", full.ResponseMeta.FinishReason)
	}
}

func TestUsageFromTokenUsagePreservesTokenDetails(t *testing.T) {
	got := usageFromTokenUsage(&schema.TokenUsage{
		PromptTokens:     100,
		CompletionTokens: 30,
		TotalTokens:      130,
		PromptTokenDetails: schema.PromptTokenDetails{
			CachedTokens: 80,
		},
		CompletionTokensDetails: schema.CompletionTokensDetails{
			ReasoningTokens: 12,
		},
	})
	if got == nil {
		t.Fatal("usageFromTokenUsage returned nil")
	}
	if got.CachedTokens != 80 {
		t.Errorf("cached tokens = %d, want 80", got.CachedTokens)
	}
	if got.ReasoningTokens != 12 {
		t.Errorf("reasoning tokens = %d, want 12", got.ReasoningTokens)
	}
}

func TestResolveClaudeMaxTokensProvidesPositiveDefault(t *testing.T) {
	if got := resolveClaudeMaxTokens(0, 64000); got != 8192 {
		t.Fatalf("default max tokens = %d, want 8192", got)
	}
	if got := resolveClaudeMaxTokens(0, 4096); got != 4096 {
		t.Fatalf("model-capped max tokens = %d, want 4096", got)
	}
	if got := resolveClaudeMaxTokens(2048, 1024); got != 2048 {
		t.Fatalf("explicit max tokens = %d, want 2048", got)
	}
}

func TestTemperaturePointerPreservesExplicitZero(t *testing.T) {
	zero := 0.0
	if got := ptrFloat32(nil); got != nil {
		t.Fatalf("unset temperature = %v, want nil", *got)
	}
	got := ptrFloat32(&zero)
	if got == nil || *got != 0 {
		t.Fatalf("explicit zero temperature = %v, want 0", got)
	}
	if formatted := formatFloat(&zero); formatted != "0" {
		t.Fatalf("formatted zero temperature = %q, want 0", formatted)
	}
}

// TestConsumeAssistantEvent_InlineThinkSplit 验证老模型把思考写成内联 <think>
// 流时：thinking_delta 收到思考、content_delta 不含 <think>、还原消息的 Content
// 干净且 ReasoningContent 拿到思考。标签跨帧切开以贴近真实流。
func TestConsumeAssistantEvent_InlineThinkSplit(t *testing.T) {
	chunks := []*schema.Message{
		{Role: schema.Assistant, Content: "<thi"},
		{Role: schema.Assistant, Content: "nk>用户在问候</thi"},
		{Role: schema.Assistant, Content: "nk>你好"},
	}
	mv := &adk.MessageVariant{
		IsStreaming:   true,
		MessageStream: schema.StreamReaderFromArray(chunks),
		Role:          schema.Assistant,
	}

	var content, thinking string
	emit := func(event string, data interface{}) error {
		switch event {
		case streaming.EventContentDelta:
			content += data.(streaming.ContentDeltaEvent).Delta
		case streaming.EventThinkingDelta:
			thinking += data.(streaming.ThinkingDeltaEvent).Delta
		}
		return nil
	}

	a := &EinoAgent{}
	full, err := a.consumeAssistantEvent(t.Context(), mv, emit)
	if err != nil {
		t.Fatalf("consumeAssistantEvent: %v", err)
	}
	if content != "你好" {
		t.Errorf("content deltas = %q, want 你好 (no <think>)", content)
	}
	if thinking != "用户在问候" {
		t.Errorf("thinking deltas = %q, want 用户在问候", thinking)
	}
	if full == nil {
		t.Fatal("expected reassembled message")
	}
	if full.Content != "你好" {
		t.Errorf("full content = %q, want 你好 (stripped)", full.Content)
	}
	if full.ReasoningContent != "用户在问候" {
		t.Errorf("full reasoning = %q, want 用户在问候", full.ReasoningContent)
	}
}

// TestConsumeAssistantEvent_NonStreaming 验证非流式路径同样发 delta 并返回消息。
func TestConsumeAssistantEvent_NonStreaming(t *testing.T) {
	mv := &adk.MessageVariant{
		IsStreaming: false,
		Message:     &schema.Message{Role: schema.Assistant, Content: "hi", ReasoningContent: "hmm"},
		Role:        schema.Assistant,
	}
	var content, thinking string
	emit := func(event string, data interface{}) error {
		switch event {
		case streaming.EventContentDelta:
			content += data.(streaming.ContentDeltaEvent).Delta
		case streaming.EventThinkingDelta:
			thinking += data.(streaming.ThinkingDeltaEvent).Delta
		}
		return nil
	}
	a := &EinoAgent{}
	full, err := a.consumeAssistantEvent(t.Context(), mv, emit)
	if err != nil {
		t.Fatal(err)
	}
	if full == nil || full.Content != "hi" {
		t.Errorf("full = %#v, want content hi", full)
	}
	if content != "hi" || thinking != "hmm" {
		t.Errorf("deltas content=%q thinking=%q", content, thinking)
	}
}

func TestConsumeAssistantEvent_EmitCancelStopsStream(t *testing.T) {
	mv := &adk.MessageVariant{
		IsStreaming: false,
		Message:     &schema.Message{Role: schema.Assistant, Content: "hi"},
		Role:        schema.Assistant,
	}
	a := &EinoAgent{}
	full, err := a.consumeAssistantEvent(t.Context(), mv, func(string, interface{}) error {
		return context.Canceled
	})
	if err != context.Canceled {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if full != nil {
		t.Fatalf("full = %#v, want nil", full)
	}
}

func TestConsumeAssistantEvent_CanceledStreamReturnsPartialMessage(t *testing.T) {
	reader, writer := schema.Pipe[*schema.Message](2)
	go func() {
		writer.Send(&schema.Message{Role: schema.Assistant, Content: "partial answer"}, nil)
		writer.Send(nil, context.Canceled)
		writer.Close()
	}()
	mv := &adk.MessageVariant{IsStreaming: true, MessageStream: reader, Role: schema.Assistant}

	var reset bool
	full, err := (&EinoAgent{}).consumeAssistantEvent(t.Context(), mv, func(event string, _ interface{}) error {
		reset = reset || event == streaming.EventAttemptReset
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if full == nil || full.Content != "partial answer" {
		t.Fatalf("partial message = %#v", full)
	}
	if reset {
		t.Fatal("explicit cancellation reset the visible partial attempt")
	}
}

func TestConsumeAssistantEvent_RetryResetsPartialAttempt(t *testing.T) {
	reader, writer := schema.Pipe[*schema.Message](2)
	go func() {
		writer.Send(&schema.Message{Role: schema.Assistant, Content: "坏答案", ReasoningContent: "错误思路"}, nil)
		writer.Send(nil, &adk.WillRetryError{ErrStr: "retrying", RetryAttempt: 1})
		writer.Close()
	}()
	mv := &adk.MessageVariant{IsStreaming: true, MessageStream: reader, Role: schema.Assistant}

	var events []string
	var reset streaming.AttemptResetEvent
	full, err := (&EinoAgent{}).consumeAssistantEvent(t.Context(), mv, func(event string, data interface{}) error {
		events = append(events, event)
		if event == streaming.EventAttemptReset {
			reset = data.(streaming.AttemptResetEvent)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("consumeAssistantEvent: %v", err)
	}
	if full != nil {
		t.Fatalf("retrying attempt should not produce a message: %#v", full)
	}
	if reset.ContentRunes != 3 || reset.ThinkingRunes != 4 {
		t.Fatalf("reset = %+v, want content=3 thinking=4", reset)
	}
	if len(events) != 3 || events[2] != streaming.EventAttemptReset {
		t.Fatalf("events = %v, want deltas followed by reset", events)
	}
}

func TestConsumeAssistantEvent_TerminalErrorPreservesPartialAttempt(t *testing.T) {
	reader, writer := schema.Pipe[*schema.Message](2)
	go func() {
		writer.Send(&schema.Message{Role: schema.Assistant, Content: "partial"}, nil)
		writer.Send(nil, errors.New("upstream disconnected"))
		writer.Close()
	}()
	mv := &adk.MessageVariant{IsStreaming: true, MessageStream: reader, Role: schema.Assistant}

	var reset bool
	full, err := (&EinoAgent{}).consumeAssistantEvent(t.Context(), mv, func(event string, data interface{}) error {
		if event == streaming.EventAttemptReset {
			reset = true
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "upstream disconnected") {
		t.Fatalf("terminal stream error = %v", err)
	}
	if full == nil || full.Content != "partial" {
		t.Fatalf("full=%#v, want preserved partial", full)
	}
	if reset {
		t.Fatal("terminal stream error reset the visible partial attempt")
	}
}

func TestPartialChatResponsePreservesCompletedPrefix(t *testing.T) {
	produced := []map[string]interface{}{
		{"role": "assistant", "content": "", "tool_calls": []interface{}{map[string]interface{}{"id": "call-1"}}},
		{"role": "tool", "tool_call_id": "call-1", "content": "done"},
	}
	resp := partialChatResponse(produced, "tool_calls", &Usage{TotalTokens: 12})
	if resp == nil || len(resp.Messages) != 2 || resp.FinishReason != "tool_calls" || resp.Usage.TotalTokens != 12 {
		t.Fatalf("partial response = %+v", resp)
	}
}

func TestPartialChatResponsePreservesTextBeforeUnfinishedToolCall(t *testing.T) {
	produced := []map[string]interface{}{
		{
			"role":              "assistant",
			"content":           "先说明目前已经确认的事实。",
			"reasoning_content": "正在准备调用工具",
			"tool_calls": []interface{}{
				map[string]interface{}{"id": "unfinished-call", "type": "function"},
			},
		},
	}
	resp := partialChatResponse(produced, "canceled", &Usage{})
	if resp == nil || len(resp.Messages) != 1 {
		t.Fatalf("partial response = %+v", resp)
	}
	message := resp.Messages[0]
	if message["content"] != "先说明目前已经确认的事实。" || message["reasoning_content"] != "正在准备调用工具" {
		t.Fatalf("partial assistant output = %#v", message)
	}
	if _, exists := message["tool_calls"]; exists {
		t.Fatalf("unfinished tool calls must not be persisted: %#v", message["tool_calls"])
	}
}

func TestInterruptedFinishReasonDoesNotReportNaturalStop(t *testing.T) {
	if got := interruptedFinishReason("stop"); got != "error" {
		t.Fatalf("stop interrupted finish reason = %q, want error", got)
	}
	if got := interruptedFinishReason("unknown"); got != "error" {
		t.Fatalf("unknown interrupted finish reason = %q, want error", got)
	}
	if got := interruptedFinishReason(string(FinishReasonToolContinuation)); got != string(FinishReasonToolContinuation) {
		t.Fatalf("tool-call interrupted finish reason = %q", got)
	}
}

func TestStreamChat_SourceEnablesRunnerStreaming(t *testing.T) {
	source := readSourceFile(t, "eino_agent.go")
	if !strings.Contains(source, "EnableStreaming: true") {
		t.Fatal("runner streaming must be explicitly enabled in StreamChat")
	}
}

func readSourceFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read source file %s: %v", name, err)
	}
	return string(data)
}

func TestBuildSearchInstruction(t *testing.T) {
	tests := []struct {
		name     string
		prompt   string
		decision modelbank.SearchDecision
		tools    map[string]bool
		wantIn   []string
		wantOut  []string
	}{
		{
			name:   "无搜索提示时保持原样",
			prompt: "你是助手",
			decision: modelbank.SearchDecision{
				EnabledSearch: false,
			},
			wantIn: []string{"你是助手"},
		},
		{
			name:   "原生优先并保留工具兜底",
			prompt: "请用中文回答",
			decision: modelbank.SearchDecision{
				EnabledSearch:        true,
				UseModelNativeSearch: true,
				UseApplicationTool:   true,
			},
			tools:  map[string]bool{"web_search": true, "web_extract": true},
			wantIn: []string{"请用中文回答", "native search", "Session Web Evidence", "use web_search", "web_extract"},
		},
		{
			name:   "仅网页搜索工具",
			prompt: "",
			decision: modelbank.SearchDecision{
				EnabledSearch:      true,
				UseApplicationTool: true,
			},
			tools:   map[string]bool{"web_search": true},
			wantIn:  []string{"Session Web Evidence", "Use web_search"},
			wantOut: []string{"web_extract"},
		},
		{
			name:   "仅网页提取工具",
			prompt: "",
			decision: modelbank.SearchDecision{
				EnabledSearch:      true,
				UseApplicationTool: true,
			},
			tools:   map[string]bool{"web_extract": true},
			wantIn:  []string{"Session Web Evidence", "Use web_extract"},
			wantOut: []string{"web_search"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildSearchInstruction(tt.prompt, tt.decision, tt.tools)
			for _, want := range tt.wantIn {
				if !strings.Contains(got, want) {
					t.Fatalf("instruction missing %q:\n%s", want, got)
				}
			}
			for _, absent := range tt.wantOut {
				if strings.Contains(got, absent) {
					t.Fatalf("instruction mentions unavailable tool %q:\n%s", absent, got)
				}
			}
		})
	}
}

func TestConvertToEinoMessages_StripsAssistantReasoning(t *testing.T) {
	asst, _ := json.Marshal(&schema.Message{Role: schema.Assistant, Content: "a", ReasoningContent: "secret thinking"})
	got, err := convertToEinoMessages([]*model.Message{{ID: 1, MessageData: asst}}, true)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if got[0].ReasoningContent != "" {
		t.Errorf("assistant reasoning not stripped: %q", got[0].ReasoningContent)
	}
}

func TestExtractSummarySection(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"with tags", "<analysis>scratch</analysis>\n<summary>final body</summary>", "final body"},
		{"summary no close", "<summary>tail only", "tail only"},
		{"analysis only stripped", "<analysis>x</analysis>\nremainder", "remainder"},
		{"no tags passthrough", "plain summary text", "plain summary text"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := extractSummarySection(c.in); got != c.want {
				t.Errorf("extractSummarySection(%q)=%q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestRenderRecentVerbatim(t *testing.T) {
	mk := func(role, content string) *model.Message {
		b, _ := json.Marshal(map[string]string{"role": role, "content": content})
		return &model.Message{MessageData: b}
	}
	msgs := []*model.Message{
		mk("user", "q1"), mk("assistant", "a1"), mk("assistant", ""), mk("user", "q2"),
	}
	got := renderRecentVerbatim(msgs, 2)
	if !strings.Contains(got, "用户：q2") {
		t.Errorf("missing recent user msg: %q", got)
	}
	if strings.Contains(got, "q1") {
		t.Errorf("should only include last 2 messages, got: %q", got)
	}
	// 空内容消息应被跳过
	full := renderRecentVerbatim(msgs, 10)
	if strings.Count(full, "助手：") != 1 {
		t.Errorf("empty assistant msg should be skipped, got: %q", full)
	}
}

func TestUnknownToolHandler(t *testing.T) {
	// 未挂载工具被调用时，应返回非空提示且不报错（优雅降级，不中断流）。
	out, err := unknownToolHandler(context.Background(), "web_search", `{"query":"x"}`)
	if err != nil {
		t.Fatalf("unknownToolHandler 不应返回 error，得到: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("unknownToolHandler 应返回非空提示文本")
	}
	if !strings.Contains(out, "web_search") {
		t.Errorf("提示文本应包含被调用的工具名，得到: %q", out)
	}
}
