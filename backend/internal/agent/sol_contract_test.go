package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/modelstream"
	"github.com/huoguojun123/EffChat/internal/service"
)

func TestSol61UtilityUsesLowestLegalEffort(t *testing.T) {
	req := &ChatRequest{ModelID: "gpt-6.1-sol", Provider: "openai", Reasoning: true, SuppressThinking: true}
	cfg := &openai.ChatModelConfig{Model: req.ModelID}
	applyOpenAICompatibleThinking(req, cfg)
	if cfg.ExtraFields["reasoning_effort"] != "low" {
		t.Fatalf("utility effort = %#v", cfg.ExtraFields)
	}
	got := openAIResponsesReasoning(req)
	if got == nil || got.Effort != "low" {
		t.Fatalf("Responses utility effort = %+v", got)
	}
}

func TestSol61WireReasoningContract(t *testing.T) {
	for _, adapter := range []string{service.AdapterOpenAICompatible, service.AdapterOpenAIResponses} {
		t.Run(adapter, func(t *testing.T) {
			bodies := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					http.Error(w, "invalid request", 400)
					return
				}
				bodies <- body
				w.Header().Set("Content-Type", "text/event-stream")
				if adapter == service.AdapterOpenAIResponses {
					if r.URL.Path != "/v1/responses" {
						t.Errorf("endpoint = %s", r.URL.Path)
					}
					fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"sequence_number\":0,\"item_id\":\"msg_demo\",\"output_index\":0,\"content_index\":0,\"delta\":\"ok\",\"logprobs\":[]}\n\n")
					fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"sequence_number\":1,\"response\":{\"id\":\"resp_demo\",\"status\":\"completed\",\"model\":\"gpt-6.1-sol\",\"output\":[]}}\n\n")
				} else {
					if r.URL.Path != "/v1/chat/completions" {
						t.Errorf("endpoint = %s", r.URL.Path)
					}
					fmt.Fprint(w, "data: {\"id\":\"chat_demo\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-6.1-sol\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\ndata: [DONE]\n\n")
				}
			}))
			defer server.Close()
			a := NewEinoAgent(service.NewChannelService(nil), nil, 4096, nil, nil, nil, nil, nil, nil)
			cm, err := a.buildChatModel(t.Context(), &ChatRequest{
				ModelID: "gpt-6.1-sol", Provider: "openai", Reasoning: true, ThinkingEffort: "max",
				RuntimeChannel: &model.AIChannel{Key: "fixture", Adapter: adapter, BaseURL: server.URL + "/v1", APIKey: "test-key", Enabled: true},
			}, modelbank.SearchDecision{})
			if err != nil {
				t.Fatal(err)
			}
			result, err := modelstream.Collect(t.Context(), cm, []*schema.Message{schema.UserMessage("hello")}, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if result.Content != "ok" {
				t.Fatalf("content = %q", result.Content)
			}
			body := <-bodies
			if body["model"] != "gpt-6.1-sol" {
				t.Fatalf("model = %#v", body["model"])
			}
			if adapter == service.AdapterOpenAIResponses {
				thinking, _ := body["reasoning"].(map[string]any)
				if thinking["effort"] != "max" || body["store"] != false {
					t.Fatalf("Responses request = %#v", body)
				}
			} else if body["reasoning_effort"] != "max" {
				t.Fatalf("Chat request = %#v", body)
			}
		})
	}
}
