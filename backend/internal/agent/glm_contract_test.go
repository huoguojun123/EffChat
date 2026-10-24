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

func TestGLMFlashModelsWireRoundTrip(t *testing.T) {
	for _, id := range []string{"glm-5.3-flash", "glm-5.3-flashx"} {
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
					delta := `{"role":"assistant","reasoning_content":"consider","tool_calls":[{"index":0,"id":"call_fixture","type":"function","function":{"name":"fixture_lookup","arguments":"{}"}}]}`
					if strings.Contains(string(raw), "fixture result") {
						delta = `{"role":"assistant","content":"ok"}`
					}
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "data: {\"id\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":%s}]}\n\n", delta)
					fmt.Fprint(w, "data: {\"id\":\"fixture\",\"choices\":[],\"usage\":{\"prompt_tokens\":17,\"completion_tokens\":5,\"total_tokens\":22,\"prompt_tokens_details\":{\"cached_tokens\":11}}}\n\ndata: [DONE]\n\n")
				}))
				defer server.Close()
				a := NewEinoAgent(service.NewChannelService(nil), nil, 4096, nil, nil, nil, nil, nil, nil)
				cm, err := a.buildChatModel(t.Context(), &ChatRequest{
					ModelID: id, Provider: "zhipu", Reasoning: true, ThinkingEffort: tc.effort, SuppressThinking: tc.utility,
					RuntimeChannel: &model.AIChannel{Adapter: service.AdapterOpenAICompatible, BaseURL: server.URL, APIKey: "fixture-key"},
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
				if first.ReasoningContent != "consider" || len(first.ToolCalls) != 1 {
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
					thinking, _ := body["thinking"].(map[string]any)
					if body["model"] != id || thinking["type"] != "enabled" || body["reasoning_effort"] != tc.want || thinking["clear_thinking"] != nil || body["tool_stream"] != nil {
						t.Errorf("invalid contract: %v", body)
					}
					raw, _ := json.Marshal(body["messages"])
					if !strings.Contains(string(raw), "AQID") || body["tools"] == nil || i == 1 && (!strings.Contains(string(raw), "consider") || !strings.Contains(string(raw), "call_fixture")) {
						t.Errorf("lost image/tool continuation: %s", raw)
					}
				}
			})
		}
	}
}
