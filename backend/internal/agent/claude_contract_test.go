package agent

import (
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

func TestClaude55WireThinkingContract(t *testing.T) {
	for _, tc := range []struct {
		id, effort, want string
		utility          bool
	}{
		{id: "claude-opus-5-5", want: "medium"},
		{id: "claude-sonnet-5-5", want: "high"},
		{id: "claude-haiku-5-5", want: "medium"},
		{id: "claude-haiku-5-5", effort: "xhigh", want: "xhigh"},
		{id: "claude-haiku-5-5", effort: "max", want: "low", utility: true},
	} {
		t.Run(tc.id+"/"+tc.want, func(t *testing.T) {
			bodies := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/messages" {
					t.Errorf("endpoint = %s", r.URL.Path)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					http.Error(w, "invalid fixture request", 400)
					return
				}
				bodies <- body
				w.Header().Set("Content-Type", "text/event-stream")
				for _, event := range []string{
					fmt.Sprintf(`{"type":"message_start","message":{"id":"msg_fixture","type":"message","role":"assistant","model":%q,"content":[],"usage":{"input_tokens":11,"output_tokens":0}}}`, tc.id),
					`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
					`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
					`{"type":"content_block_stop","index":0}`,
					`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`,
					`{"type":"message_stop"}`,
				} {
					var envelope struct {
						Type string `json:"type"`
					}
					if err := json.Unmarshal([]byte(event), &envelope); err != nil {
						t.Error(err)
						return
					}
					fmt.Fprintf(w, "event: %s\ndata: %s\n\n", envelope.Type, event)
				}
			}))
			defer server.Close()
			a := NewEinoAgent(service.NewChannelService(nil), nil, 4096, nil, nil, nil, nil, nil, nil)
			temperature := 0.7
			cm, err := a.buildChatModel(t.Context(), &ChatRequest{
				ModelID: tc.id, Provider: "anthropic", Reasoning: true, ThinkingEffort: tc.effort,
				SuppressThinking: tc.utility, Temperature: &temperature,
				RuntimeChannel: &model.AIChannel{Key: "fixture", Adapter: service.AdapterAnthropic, BaseURL: server.URL, APIKey: "fixture-key", Enabled: true},
			}, modelbank.SearchDecision{})
			if err != nil {
				t.Fatal(err)
			}
			result, err := modelstream.Collect(t.Context(), cm, []*schema.Message{schema.UserMessage("Hello")}, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if result.Content != "ok" {
				t.Fatalf("content = %q", result.Content)
			}
			body := <-bodies
			thinking, _ := body["thinking"].(map[string]any)
			output, _ := body["output_config"].(map[string]any)
			if body["model"] != tc.id || thinking["type"] != "adaptive" || thinking["budget_tokens"] != nil || output["effort"] != tc.want {
				t.Fatalf("invalid thinking contract: %v / %v", thinking, output)
			}
			if tc.utility && thinking["display"] != nil || !tc.utility && thinking["display"] != "summarized" {
				t.Fatalf("display = %v for utility=%v", thinking["display"], tc.utility)
			}
			for _, field := range []string{"temperature", "top_p", "top_k"} {
				if _, ok := body[field]; ok {
					t.Errorf("unsupported field %s sent", field)
				}
			}
		})
	}
}
