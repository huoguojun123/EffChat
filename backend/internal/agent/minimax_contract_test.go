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

func TestCompatibleThinkingToolRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		adapter, model, signature string
		wantThinking              bool
	}{
		{service.AdapterOpenAICompatible, "MiniMax-M3.1-Flash", "", true},
		{service.AdapterAnthropic, "MiniMax-M3.1-Flash", "", true},
		{service.AdapterAnthropic, "MiniMax-M3", "", true},
		{service.AdapterAnthropic, "claude-haiku-5-5", "", false},
		{service.AdapterAnthropic, "claude-haiku-5-5", "fixture-signature", true},
	} {
		t.Run(tc.adapter+"/"+tc.model+"/"+tc.signature, func(t *testing.T) {
			requests := make(chan map[string]any, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				requests <- body
				raw, _ := json.Marshal(body["messages"])
				second := strings.Contains(string(raw), "fixture result")
				w.Header().Set("Content-Type", "application/json")
				if tc.adapter == service.AdapterOpenAICompatible {
					message := `{"role":"assistant","content":"","reasoning_content":"consider","tool_calls":[{"id":"call_fixture","type":"function","function":{"name":"fixture_lookup","arguments":"{}"}}]}`
					if second {
						message = `{"role":"assistant","content":"ok"}`
					}
					fmt.Fprintf(w, `{"id":"fixture","choices":[{"index":0,"message":%s,"finish_reason":"stop"}]}`, message)
				} else {
					content := `[{"type":"thinking","thinking":"consider"},{"type":"tool_use","id":"call_fixture","name":"fixture_lookup","input":{}}]`
					if tc.signature != "" {
						content = strings.Replace(content, `"thinking":"consider"`, `"thinking":"consider","signature":"`+tc.signature+`"`, 1)
					}
					if second {
						content = `[{"type":"text","text":"ok"}]`
					}
					fmt.Fprintf(w, `{"id":"fixture","type":"message","role":"assistant","content":%s,"stop_reason":"end_turn","usage":{"input_tokens":17,"output_tokens":5}}`, content)
				}
			}))
			defer server.Close()
			a := NewEinoAgent(service.NewChannelService(nil), nil, 4096, nil, nil, nil, nil, nil, nil)
			cm, err := a.buildChatModel(t.Context(), &ChatRequest{
				ModelID: tc.model, Reasoning: true,
				RuntimeChannel: &model.AIChannel{Adapter: tc.adapter, BaseURL: server.URL, APIKey: "fixture-key"},
			}, modelbank.SearchDecision{})
			if err != nil {
				t.Fatal(err)
			}
			cm, err = cm.WithTools([]*schema.ToolInfo{{Name: "fixture_lookup", ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{})}})
			if err != nil {
				t.Fatal(err)
			}
			imageData := "AQID"
			input := []*schema.Message{{Role: schema.User, UserInputMultiContent: []schema.MessageInputPart{
				{Type: schema.ChatMessagePartTypeText, Text: "Describe fixture"},
				{Type: schema.ChatMessagePartTypeImageURL, Image: &schema.MessageInputImage{MessagePartCommon: schema.MessagePartCommon{Base64Data: &imageData, MIMEType: "image/png"}}},
			}}}
			first, err := cm.Generate(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			if len(first.ToolCalls) != 1 || first.ToolCalls[0].ID != "call_fixture" || first.ReasoningContent != "consider" {
				t.Fatalf("first=%+v", first)
			}
			body := <-requests
			raw, _ := json.Marshal(body)
			if !strings.Contains(string(raw), "AQID") || body["tools"] == nil {
				t.Errorf("missing image/tools: %s", raw)
			}
			input = append(input, first, &schema.Message{Role: schema.Tool, ToolCallID: "call_fixture", Content: "fixture result"})
			second, err := cm.Generate(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			if second.Content != "ok" {
				t.Errorf("answer=%q", second.Content)
			}
			body = <-requests
			raw, _ = json.Marshal(body["messages"])
			if strings.Contains(string(raw), "consider") != tc.wantThinking || !strings.Contains(string(raw), "call_fixture") {
				t.Errorf("incorrect reasoning/tool replay: %s", raw)
			}
			if tc.signature != "" && !strings.Contains(string(raw), tc.signature) {
				t.Errorf("lost original signature: %s", raw)
			}
			if tc.adapter == service.AdapterAnthropic && tc.signature == "" && strings.Contains(string(raw), `"signature"`) {
				t.Errorf("fabricated signature: %s", raw)
			}
		})
	}
}

func TestMiniMax31WireThinkingContract(t *testing.T) {
	for _, adapter := range []string{service.AdapterOpenAICompatible, service.AdapterAnthropic} {
		for _, tc := range []struct {
			effort, want string
			utility      bool
		}{
			{want: "max"}, {effort: "xhigh", want: "xhigh"},
			{effort: "none", want: "low"}, {effort: "max", utility: true, want: "low"},
		} {
			t.Run(adapter+"/"+tc.effort+fmt.Sprint(tc.utility), func(t *testing.T) {
				requests := make(chan map[string]any, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						return
					}
					requests <- body
					w.Header().Set("Content-Type", "text/event-stream")
					if adapter == service.AdapterOpenAICompatible {
						if r.URL.Path != "/chat/completions" {
							t.Errorf("path=%s", r.URL.Path)
						}
						fmt.Fprint(w, "data: {\"id\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"reasoning_content\":\"consider\",\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n")
						fmt.Fprint(w, "data: {\"id\":\"fixture\",\"choices\":[],\"usage\":{\"prompt_tokens\":17,\"completion_tokens\":5,\"total_tokens\":22,\"prompt_tokens_details\":{\"cached_tokens\":11}}}\n\ndata: [DONE]\n\n")
					} else {
						if r.URL.Path != "/v1/messages" {
							t.Errorf("path=%s", r.URL.Path)
						}
						for _, event := range []string{
							`{"type":"message_start","message":{"id":"fixture","type":"message","role":"assistant","content":[],"usage":{"input_tokens":6,"cache_read_input_tokens":11,"output_tokens":0}}}`,
							`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
							`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"consider"}}`,
							`{"type":"content_block_stop","index":0}`,
							`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
							`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"ok"}}`,
							`{"type":"content_block_stop","index":1}`,
							`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
							`{"type":"message_stop"}`,
						} {
							var e struct {
								Type string `json:"type"`
							}
							_ = json.Unmarshal([]byte(event), &e)
							fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, event)
						}
					}
				}))
				defer server.Close()
				a := NewEinoAgent(service.NewChannelService(nil), nil, 4096, nil, nil, nil, nil, nil, nil)
				v := 0.7
				n := 2
				cm, err := a.buildChatModel(t.Context(), &ChatRequest{
					ModelID: "MiniMax-M3.1-Flash", Provider: "minimax", Reasoning: true, ThinkingEffort: tc.effort,
					SuppressThinking: tc.utility, MaxTokens: 8192, Temperature: &v,
					OpenAIRequestProfile: model.OpenAIRequestProfile{TopP: &v, N: &n, PresencePenalty: &v, FrequencyPenalty: &v},
					RuntimeChannel:       &model.AIChannel{Key: "fixture", Adapter: adapter, BaseURL: server.URL, APIKey: "fixture-key"},
				}, modelbank.SearchDecision{})
				if err != nil {
					t.Fatal(err)
				}
				result, err := modelstream.Collect(t.Context(), cm, []*schema.Message{schema.UserMessage("fixture")}, time.Second)
				if err != nil {
					t.Fatal(err)
				}
				if result.Content != "ok" || result.ReasoningContent != "consider" {
					t.Fatalf("result=%+v", result)
				}
				if u := result.ResponseMeta.Usage; u.PromptTokens != 17 || u.PromptTokenDetails.CachedTokens != 11 || u.TotalTokens != 22 {
					t.Errorf("usage=%+v", u)
				}
				body := <-requests
				thinking, _ := body["thinking"].(map[string]any)
				if thinking["type"] != "adaptive" || thinking["budget_tokens"] != nil {
					t.Errorf("thinking=%v", thinking)
				}
				if adapter == service.AdapterOpenAICompatible {
					if body["reasoning_effort"] != tc.want || body["reasoning_split"] != true || body["max_completion_tokens"] != float64(8192) || body["max_tokens"] != nil {
						t.Errorf("invalid OpenAI contract: %v", body)
					}
					options, _ := body["stream_options"].(map[string]any)
					if options["include_usage"] != true {
						t.Errorf("stream options=%v", options)
					}
				} else {
					output, _ := body["output_config"].(map[string]any)
					if output["effort"] != tc.want || body["max_tokens"] != float64(8192) {
						t.Errorf("invalid Anthropic contract: %v", body)
					}
				}
				for _, field := range []string{"temperature", "top_p", "n", "presence_penalty", "frequency_penalty"} {
					if body[field] != nil {
						t.Errorf("unsupported field %s", field)
					}
				}
			})
		}
	}
}
