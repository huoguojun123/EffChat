package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
)

func TestBuildSessionMarkdownFiltersPrivateRuntimeData(t *testing.T) {
	createdAt := time.Date(2026, 7, 20, 9, 30, 0, 0, time.FixedZone("CST", 8*60*60))
	exportedAt := time.Date(2026, 7, 27, 14, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	session := &model.Session{ID: 42, Title: "研究 / 记录", CreatedAt: createdAt}
	messages := []*model.Message{
		exportTestMessage(1, "user", map[string]interface{}{
			"role": "user", "content": "内部摘要", "metadata": map[string]interface{}{"compaction_summary": true},
		}),
		exportTestMessage(2, "user", map[string]interface{}{
			"role": "user", "content": "请核对来源", "attachments": []map[string]interface{}{{
				"file_id": 7, "filename": "report.pdf", "file_type": "application/pdf",
			}},
		}),
		exportTestMessage(3, "assistant", map[string]interface{}{
			"role": "assistant", "content": "我先检索。", "thinking": "private reasoning",
			"tool_calls": []map[string]interface{}{
				{"id": "search-1", "function": map[string]interface{}{"name": "web_search", "arguments": `{"query":"secret prompt"}`}},
				{"id": "file-1", "function": map[string]interface{}{"name": "file_read", "arguments": `{"file_id":7,"path":"/private/data/report.pdf"}`}},
			},
		}),
		exportTestMessage(4, "tool", map[string]interface{}{
			"role": "tool", "tool_call_id": "search-1", "tool_name": "web_search",
			"content": `{"source":"basic","attempted_sources":["firecrawl","basic"],"citations":[{"url":"https://example.com/source","snippet":"raw result"}]}`,
		}),
		exportTestMessage(5, "tool", map[string]interface{}{
			"role": "tool", "tool_call_id": "file-1", "tool_name": "file_read",
			"content": `API_KEY=do-not-export /private/data/report.pdf full tool result`,
		}),
		exportTestMessage(6, "assistant", map[string]interface{}{
			"role": "assistant", "content": "这是最终回答。",
		}),
		exportTestMessage(7, "assistant", map[string]interface{}{
			"role": "assistant", "content": "请求失败：upstream", "metadata": map[string]interface{}{"error": true, "error_detail": "stack trace"},
		}),
	}

	content, err := buildSessionMarkdown(session, messages, map[int64]struct{}{}, true, true, exportedAt)
	if err != nil {
		t.Fatalf("buildSessionMarkdown: %v", err)
	}
	for _, expected := range []string{
		"# 研究 / 记录",
		"请核对来源",
		"`report.pdf` · `application/pdf` · 已删除",
		"我先检索。",
		"这是最终回答。",
		"`web_search` · 降级完成",
		"来源：<https://example.com/source>",
		"`file_read` · 成功 · 附件：`report.pdf`",
	} {
		if !strings.Contains(content, expected) {
			t.Errorf("export missing %q:\n%s", expected, content)
		}
	}
	for _, forbidden := range []string{
		"内部摘要",
		"private reasoning",
		"secret prompt",
		"API_KEY",
		"/private/data",
		"raw result",
		"stack trace",
		"请求失败：upstream",
	} {
		if strings.Contains(content, forbidden) {
			t.Errorf("export leaked %q:\n%s", forbidden, content)
		}
	}
}

func TestBuildSessionMarkdownOmitsToolsByDefault(t *testing.T) {
	session := &model.Session{ID: 3, Title: "Simple", CreatedAt: time.Unix(0, 0)}
	messages := []*model.Message{
		exportTestMessage(1, "user", map[string]interface{}{"role": "user", "content": "Question"}),
		exportTestMessage(2, "assistant", map[string]interface{}{
			"role": "assistant", "content": "Answer", "tool_calls": []map[string]interface{}{{"id": "call-1", "name": "web_search"}},
		}),
	}
	content, err := buildSessionMarkdown(session, messages, nil, false, false, time.Unix(1, 0))
	if err != nil {
		t.Fatalf("buildSessionMarkdown: %v", err)
	}
	if strings.Contains(content, "工具摘要") || strings.Contains(content, "web_search") {
		t.Fatalf("tool summary should be omitted:\n%s", content)
	}
	if !strings.Contains(content, "Question") || !strings.Contains(content, "Answer") {
		t.Fatalf("conversation content missing:\n%s", content)
	}
}

func TestSafeSessionExportFilename(t *testing.T) {
	got := safeSessionExportFilename(`  <>:"/\\|?*  `, 9, time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC))
	if got != "conversation-20260727-9.md" {
		t.Fatalf("safe filename = %q", got)
	}
	got = safeSessionExportFilename("A/B\nC", 9, time.Time{})
	if got != "A B C.md" {
		t.Fatalf("normalized filename = %q", got)
	}
}

func TestExportSourceURLRejectsCredentialsAndInvalidValues(t *testing.T) {
	for _, value := range []string{
		"https://user:secret@example.com",
		"https://",
		"javascript:alert(1)",
		"https://example.com\nmalicious",
		"https://example.com/download?X-Amz-Signature=secret",
		"https://example.com/download?token=secret",
	} {
		if _, ok := exportSourceURL(value); ok {
			t.Errorf("exportSourceURL(%q) accepted a private or invalid URL", value)
		}
	}
	got, ok := exportSourceURL("https://example.com/source?q=1#section")
	if !ok || got != "https://example.com/source?q=1" {
		t.Fatal("public HTTPS URL was rejected")
	}
}

func exportTestMessage(id int64, role string, data map[string]interface{}) *model.Message {
	raw, err := json.Marshal(data)
	if err != nil {
		panic(err)
	}
	return &model.Message{ID: id, Role: role, MessageData: raw}
}
