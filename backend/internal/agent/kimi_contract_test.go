package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/modelstream"
	"github.com/huoguojun123/EffChat/internal/service"
)

func TestKimi31WireRoundTrip(t *testing.T) {
	for _, id := range []string{"kimi-k3.1-pro", "kimi-k3.1-pro-highspeed"} {
		for _, tc := range []struct {
			effort, want string
			utility      bool
		}{{want: "max"}, {effort: "high", want: "high"}, {effort: "none", want: "low"}, {utility: true, want: "low"}} {
			t.Run(id+"/"+tc.want+fmt.Sprint(tc.utility), func(t *testing.T) {
				requests := make(chan map[string]any, 2)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/chat/completions" {
						t.Errorf("path=%s", r.URL.Path)
					}
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						return
					}
					requests <- body
					raw, _ := json.Marshal(body["messages"])
					delta := `{"role":"assistant","reasoning_content":"fixture thinking","tool_calls":[{"index":0,"id":"call_fixture","type":"function","function":{"name":"fixture_lookup","arguments":"{}"}}]}`
					if strings.Contains(string(raw), "fixture result") {
						delta = `{"role":"assistant","content":"ok"}`
					}
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "data: {\"id\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":%s}]}\n\n", delta)
					fmt.Fprint(w, "data: {\"id\":\"fixture\",\"choices\":[],\"usage\":{\"prompt_tokens\":17,\"completion_tokens\":5,\"total_tokens\":22,\"prompt_tokens_details\":{\"cached_tokens\":11}}}\n\ndata: [DONE]\n\n")
				}))
				defer server.Close()
				temperature, topP, penalty, n := 0.5, 0.8, 0.2, 2
				a := NewEinoAgent(service.NewChannelService(nil), nil, 4096, nil, nil, nil, nil, nil, nil)
				cm, err := a.buildChatModel(t.Context(), &ChatRequest{
					ModelID: id, Provider: "moonshot", Reasoning: true, ThinkingEffort: tc.effort, SuppressThinking: tc.utility, MaxTokens: 8192,
					TemperaturePolicy: model.TemperaturePolicyFixed, TemperatureValue: &temperature,
					OpenAIRequestProfile: model.OpenAIRequestProfile{TopP: &topP, N: &n, PresencePenalty: &penalty, FrequencyPenalty: &penalty},
					RuntimeChannel:       &model.AIChannel{Adapter: service.AdapterOpenAICompatible, BaseURL: server.URL, APIKey: "fixture-key"},
				}, modelbank.SearchDecision{})
				if err != nil {
					t.Fatal(err)
				}
				cm, err = cm.WithTools([]*schema.ToolInfo{{Name: "fixture_lookup", ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{})}})
				if err != nil {
					t.Fatal(err)
				}
				image := "AQID"
				messages := []*schema.Message{{Role: schema.User, UserInputMultiContent: []schema.MessageInputPart{
					{Type: schema.ChatMessagePartTypeText, Text: "describe fixture"},
					{Type: schema.ChatMessagePartTypeImageURL, Image: &schema.MessageInputImage{MessagePartCommon: schema.MessagePartCommon{Base64Data: &image, MIMEType: "image/png"}}},
				}}}
				first, err := modelstream.Collect(t.Context(), cm, messages, time.Second)
				if err != nil {
					t.Fatal(err)
				}
				if first.ReasoningContent != "fixture thinking" || len(first.ToolCalls) != 1 {
					t.Fatalf("first=%+v", first)
				}
				messages = append(messages, first, &schema.Message{Role: schema.Tool, ToolCallID: "call_fixture", Content: "fixture result"})
				last, err := modelstream.Collect(t.Context(), cm, messages, time.Second)
				if err != nil {
					t.Fatal(err)
				}
				if last.Content != "ok" || last.ResponseMeta.Usage.TotalTokens != 22 || last.ResponseMeta.Usage.PromptTokenDetails.CachedTokens != 11 {
					t.Fatalf("last=%+v", last)
				}
				for i := 0; i < 2; i++ {
					body := <-requests
					if body["model"] != id || body["reasoning_effort"] != tc.want || body["max_completion_tokens"] != float64(8192) {
						t.Errorf("invalid contract: %v", body)
					}
					for _, key := range []string{"thinking", "temperature", "top_p", "n", "presence_penalty", "frequency_penalty", "max_tokens"} {
						if body[key] != nil {
							t.Errorf("unsupported field %s: %v", key, body)
						}
					}
					raw, _ := json.Marshal(body["messages"])
					if !strings.Contains(string(raw), "data:image/png;base64,AQID") || body["tools"] == nil || i == 1 && (!strings.Contains(string(raw), "fixture thinking") || !strings.Contains(string(raw), "call_fixture")) {
						t.Errorf("lost image/tool continuation: %s", raw)
					}
				}
			})
		}
	}
}
