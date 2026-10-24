package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/modelstream"
	"github.com/huoguojun123/EffChat/internal/service"
)

func TestHistoricalThinkingProjectionPreservesUnrelatedMetadata(t *testing.T) {
	for _, signed := range []bool{false, true} {
		t.Run(fmt.Sprintf("signed=%v", signed), func(t *testing.T) {
			extra := map[string]any{
				"_eino_claude_thinking":   "historical thought",
				"fixture_other_signature": "unrelated-provider-signature",
				"_eino_claude_breakpoint": true,
			}
			if signed {
				extra["_eino_claude_thinking_signature"] = "bound-signature"
			}
			raw, err := json.Marshal(&schema.Message{Role: schema.Assistant, Content: "answer", ReasoningContent: "historical thought", Extra: extra})
			if err != nil {
				t.Fatal(err)
			}
			original := bytes.Clone(raw)
			got, err := convertToEinoMessages([]*model.Message{{ID: 1, MessageData: raw}}, false)
			if err != nil || len(got) != 1 {
				t.Fatalf("projection = %v, %v", got, err)
			}
			m := got[0]
			if m.ReasoningContent != "" || m.Extra["fixture_other_signature"] != "unrelated-provider-signature" || m.Extra["_eino_claude_breakpoint"] != true {
				t.Fatalf("unrelated projection changed: %+v", m)
			}
			_, hasThinking := m.Extra["_eino_claude_thinking"]
			_, hasSignature := m.Extra["_eino_claude_thinking_signature"]
			if hasSignature || hasThinking == signed {
				t.Fatalf("bound thinking must be omitted; unsigned compatible thinking must remain: %v", m.Extra)
			}
			if !bytes.Equal(raw, original) {
				t.Fatal("historical projection changed persisted bytes")
			}
		})
	}
}

func TestClaudeHistoricalBoundThinkingIsNotReplayed(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", streaming), func(t *testing.T) {
			bodies := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				bodies <- body
				if !streaming {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"id":"fixture","type":"message","role":"assistant","model":"claude-sonnet-5-5","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "event: message_start\ndata: "+`{"type":"message_start","message":{"id":"fixture","type":"message","role":"assistant","model":"claude-sonnet-5-5","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`+"\n\n")
				fmt.Fprint(w, "event: content_block_start\ndata: "+`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"ok"}}`+"\n\n")
				fmt.Fprint(w, "event: content_block_stop\ndata: "+`{"type":"content_block_stop","index":0}`+"\n\n")
				fmt.Fprint(w, "event: message_delta\ndata: "+`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`+"\n\n")
				fmt.Fprint(w, "event: message_stop\ndata: "+`{"type":"message_stop"}`+"\n\n")
			}))
			defer server.Close()
			var history []*model.Message
			for _, m := range []*schema.Message{
				schema.UserMessage("first question"),
				{Role: schema.Assistant, Content: "first answer", ReasoningContent: "first thought", Extra: map[string]any{"_eino_claude_thinking": "first thought", "_eino_claude_thinking_signature": "first-bound-signature"}},
				schema.UserMessage("second question"),
				{Role: schema.Assistant, Content: "second answer", Extra: map[string]any{"_eino_claude_thinking": "second thought", "_eino_claude_thinking_signature": "second-bound-signature"}},
				schema.UserMessage("continue"),
			} {
				raw, err := json.Marshal(m)
				if err != nil {
					t.Fatal(err)
				}
				history = append(history, &model.Message{MessageData: raw})
			}
			messages, err := convertToEinoMessages(history, false)
			if err != nil {
				t.Fatal(err)
			}
			messages = append([]*schema.Message{schema.SystemMessage("rebuilt system instruction")}, messages...)
			a := NewEinoAgent(service.NewChannelService(nil), nil, 4096, nil, nil, nil, nil, nil, nil)
			cm, err := a.buildChatModel(t.Context(), &ChatRequest{
				ModelID: "claude-sonnet-5-5", Provider: "anthropic", Reasoning: true,
				RuntimeChannel: &model.AIChannel{Key: "fixture", Adapter: service.AdapterAnthropic, BaseURL: server.URL, APIKey: "fixture-key", Enabled: true},
			}, modelbank.SearchDecision{})
			if err != nil {
				t.Fatal(err)
			}
			var result *schema.Message
			if streaming {
				result, err = modelstream.Collect(t.Context(), cm, messages, time.Second)
			} else {
				result, err = cm.Generate(t.Context(), messages)
			}
			if err != nil || result.Content != "ok" {
				t.Fatalf("response = %v, %v", result, err)
			}
			body := <-bodies
			wireMessages := body["messages"].([]any)
			if len(wireMessages) != 5 {
				t.Fatalf("history length = %d", len(wireMessages))
			}
			for _, value := range wireMessages {
				m := value.(map[string]any)
				for _, value := range m["content"].([]any) {
					block := value.(map[string]any)
					if block["type"] == "thinking" || block["type"] == "redacted_thinking" || block["signature"] != nil {
						t.Errorf("bound historical thinking reached wire: %v", block)
					}
				}
			}
		})
	}
}
