package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/EffChat/internal/model"
)

func TestConvertToEinoMessagesAppendsWebToolContextToLatestUser(t *testing.T) {
	searchContent, _ := json.Marshal(map[string]interface{}{
		"summary": "Search results for \"agent cache\":\n\n[1] Result A\nURL: https://example.com/a\nSnippet A\n",
		"citations": []map[string]string{
			{"title": "Result A", "url": "https://example.com/a", "snippet": "Snippet A"},
			{"title": "Result B", "url": "https://example.com/b", "snippet": "Snippet B"},
		},
	})
	extractContent, _ := json.Marshal(map[string]interface{}{
		"ok":      true,
		"url":     "https://example.com/a",
		"title":   "Result A",
		"content": "提取后的网页摘要",
	})

	dbMessages := []*model.Message{
		messageFromSchema(t, &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{toolCall("search_call", "web_search")}}),
		messageFromSchema(t, &schema.Message{Role: schema.Tool, ToolName: "web_search", ToolCallID: "search_call", Content: string(searchContent)}),
		messageFromSchema(t, &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{toolCall("extract_call", "web_extract")}}),
		messageFromSchema(t, &schema.Message{Role: schema.Tool, ToolName: "web_extract", ToolCallID: "extract_call", Content: string(extractContent)}),
		messageFromSchema(t, schema.UserMessage("继续看第二条")),
	}

	got, err := convertToEinoMessages(dbMessages, true)
	if err != nil {
		t.Fatalf("convertToEinoMessages: %v", err)
	}
	last := got[len(got)-1]
	for _, want := range []string{"继续看第二条", "Session Web Evidence", "https://example.com/a", "https://example.com/b", "Snippet B"} {
		if !strings.Contains(last.Content, want) {
			t.Fatalf("latest user message missing %q:\n%s", want, last.Content)
		}
	}
}

func TestConvertToEinoMessagesSkipsFailedWebSearchContext(t *testing.T) {
	searchContent, _ := json.Marshal(map[string]interface{}{
		"summary":       "failed",
		"search_failed": true,
		"citations":     []map[string]string{{"title": "Bad", "url": "https://example.com/bad"}},
	})
	dbMessages := []*model.Message{
		messageFromSchema(t, &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{toolCall("search_call", "web_search")}}),
		messageFromSchema(t, &schema.Message{Role: schema.Tool, ToolName: "web_search", ToolCallID: "search_call", Content: string(searchContent)}),
		messageFromSchema(t, schema.UserMessage("继续")),
	}

	got, err := convertToEinoMessages(dbMessages, true)
	if err != nil {
		t.Fatalf("convertToEinoMessages: %v", err)
	}
	last := got[len(got)-1]
	if strings.Contains(last.Content, "https://example.com/bad") || strings.Contains(last.Content, "Session Web Evidence") {
		t.Fatalf("failed web search should not be appended:\n%s", last.Content)
	}
}

func TestConvertToEinoMessagesBoundsSourceEvidence(t *testing.T) {
	sourceText := strings.Repeat("原文段落", 800)
	extractContent, _ := json.Marshal(map[string]interface{}{
		"ok": true, "url": "https://example.com/source", "title": "原文", "detail": "source", "content": sourceText,
	})
	dbMessages := []*model.Message{
		messageFromSchema(t, &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{toolCall("extract_call", "web_extract")}}),
		messageFromSchema(t, &schema.Message{Role: schema.Tool, ToolName: "web_extract", ToolCallID: "extract_call", Content: string(extractContent)}),
		messageFromSchema(t, schema.UserMessage("继续核对")),
	}

	got, err := convertToEinoMessages(dbMessages, true)
	if err != nil {
		t.Fatalf("convertToEinoMessages: %v", err)
	}
	if len([]rune(got[len(got)-1].Content)) >= len([]rune(sourceText)) {
		t.Fatalf("source evidence was repeated without a bound: %d", len([]rune(got[len(got)-1].Content)))
	}
}

func TestConvertToEinoMessagesBoundsAndDeduplicatesWebEvidence(t *testing.T) {
	citations := make([]map[string]string, 0, 20)
	for i := 1; i <= 20; i++ {
		citations = append(citations, map[string]string{
			"title":   "Result " + intString(i),
			"url":     "https://example.com/" + intString(i),
			"snippet": strings.Repeat("Snippet "+intString(i)+" ", 120),
		})
	}
	citations = append(citations, map[string]string{
		"title": "Duplicate", "url": "https://example.com/20#fragment", "snippet": "duplicate",
	})
	searchContent, _ := json.Marshal(map[string]interface{}{
		"summary":   "Search results for \"many\":\n\n",
		"citations": citations,
	})
	dbMessages := []*model.Message{
		messageFromSchema(t, &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{toolCall("search_call", "web_search")}}),
		messageFromSchema(t, &schema.Message{Role: schema.Tool, ToolName: "web_search", ToolCallID: "search_call", Content: string(searchContent)}),
		messageFromSchema(t, schema.UserMessage("继续")),
	}

	got, err := convertToEinoMessages(dbMessages, true)
	if err != nil {
		t.Fatalf("convertToEinoMessages: %v", err)
	}
	last := got[len(got)-1]
	if strings.Count(last.Content, "https://example.com/20") != 1 {
		t.Fatalf("duplicate URL should appear once:\n%s", last.Content)
	}
	if !strings.Contains(last.Content, "additional evidence item") {
		t.Fatalf("bounded evidence should disclose omitted items:\n%s", last.Content)
	}
	if got := len([]rune(last.Content)); got > webEvidenceMaxChars+200 {
		t.Fatalf("evidence exceeded character budget: %d", got)
	}
}

func TestWebEvidenceKeepsRecentlyRefreshedURL(t *testing.T) {
	citations := make([]map[string]string, 0, 13)
	for i := 1; i <= 13; i++ {
		citations = append(citations, map[string]string{
			"title": "Result " + intString(i), "url": "https://example.com/" + intString(i), "snippet": "short",
		})
	}
	searchContent, _ := json.Marshal(map[string]interface{}{
		"summary": "Search results for \"many\":\n\n", "citations": citations,
	})
	extractContent, _ := json.Marshal(map[string]interface{}{
		"ok": true, "url": "https://example.com/1", "title": "Refreshed", "content": "latest extract",
	})
	dbMessages := []*model.Message{
		messageFromSchema(t, &schema.Message{Role: schema.Tool, ToolName: "web_search", ToolCallID: "shared", Content: string(searchContent)}),
		messageFromSchema(t, &schema.Message{Role: schema.Tool, ToolName: "web_extract", ToolCallID: "shared", Content: string(extractContent)}),
		messageFromSchema(t, &schema.Message{Role: schema.Tool, ToolName: "web_search", ToolCallID: "shared", Content: string(searchContent)}),
	}
	got := buildRecentWebToolContext(dbMessages)
	if !strings.Contains(got, "https://example.com/1") || !strings.Contains(got, "latest extract") {
		t.Fatalf("recent extraction should refresh URL recency:\n%s", got)
	}
	if strings.Contains(got, "https://example.com/2\n") {
		t.Fatalf("oldest untouched URL should be omitted before refreshed evidence:\n%s", got)
	}
}

func messageFromSchema(t *testing.T, msg *schema.Message) *model.Message {
	t.Helper()
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	return &model.Message{MessageData: raw}
}

func toolCall(id string, name string) schema.ToolCall {
	return schema.ToolCall{
		ID:   id,
		Type: "function",
		Function: schema.FunctionCall{
			Name:      name,
			Arguments: `{}`,
		},
	}
}
