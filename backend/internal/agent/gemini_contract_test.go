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

func TestGeminiCurrentModelsWireRoundTrip(t *testing.T) {
	for _, id := range []string{"gemini-4-argon", "gemini-3.5-flash-lite"} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", id, streaming), func(t *testing.T) {
				requests := make(chan map[string]any, 2)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					operation := ":generateContent"
					if streaming {
						operation = ":streamGenerateContent"
					}
					if !strings.HasSuffix(r.URL.Path, "/models/"+id+operation) {
						t.Errorf("endpoint = %s", r.URL.Path)
					}
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						return
					}
					requests <- body
					raw, _ := json.Marshal(body["contents"])
					part := `{"functionCall":{"name":"fixture_lookup","args":{}},"thoughtSignature":"AQID"}`
					if strings.Contains(string(raw), "functionResponse") {
						if !strings.Contains(string(raw), `"thoughtSignature":"AQID"`) {
							t.Error("tool replay lost its thought signature")
						}
						part = `{"text":"ok"}`
					}
					response := fmt.Sprintf(`{"candidates":[{"content":{"role":"model","parts":[%s]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":77,"candidatesTokenCount":5,"thoughtsTokenCount":2,"cachedContentTokenCount":11,"totalTokenCount":84}}`, part)
					if streaming {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprintf(w, "data: %s\n\n", response)
					} else {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprint(w, response)
					}
				}))
				defer server.Close()
				a := NewEinoAgent(service.NewChannelService(nil), nil, 4096, nil, nil, nil, nil, nil, nil)
				temp := 0.7
				cm, err := a.buildChatModel(t.Context(), &ChatRequest{
					ModelID: id, Provider: "google", Reasoning: true, ThinkingEffort: "high", MaxTokens: 32768, Temperature: &temp,
					RuntimeChannel: &model.AIChannel{Key: "fixture", Adapter: service.AdapterGoogle, BaseURL: server.URL, APIKey: "fixture-key", Enabled: true},
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
					{Type: schema.ChatMessagePartTypeText, Text: "Describe this fixture."},
					{Type: schema.ChatMessagePartTypeImageURL, Image: &schema.MessageInputImage{MessagePartCommon: schema.MessagePartCommon{Base64Data: &imageData, MIMEType: "image/png"}}},
				}}}
				for turn := 0; turn < 2; turn++ {
					var result *schema.Message
					if streaming {
						result, err = modelstream.Collect(t.Context(), cm, input, time.Second)
					} else {
						result, err = cm.Generate(t.Context(), input)
					}
					if err != nil {
						t.Fatal(err)
					}
					body := <-requests
					cfg, _ := body["generationConfig"].(map[string]any)
					thinking, _ := cfg["thinkingConfig"].(map[string]any)
					if thinking["thinkingLevel"] != "HIGH" || thinking["thinkingBudget"] != nil || cfg["maxOutputTokens"] != float64(32768) {
						t.Errorf("generation config = %v", cfg)
					}
					for _, key := range []string{"temperature", "topP", "topK"} {
						if cfg[key] != nil {
							t.Errorf("unsupported sampling field %s = %v", key, cfg[key])
						}
					}
					contents, _ := json.Marshal(body["contents"])
					if !strings.Contains(string(contents), "Describe this fixture.") || !strings.Contains(string(contents), `"mimeType":"image/png"`) || body["tools"] == nil {
						t.Errorf("image/text/tools not serialized: %s", contents)
					}
					u := result.ResponseMeta.Usage
					if u.PromptTokens != 77 || u.PromptTokenDetails.CachedTokens != 11 || u.TotalTokens != 84 || u.CompletionTokensDetails.ReasoningTokens != 2 {
						t.Errorf("usage = %+v", u)
					}
					if turn == 0 {
						if len(result.ToolCalls) != 1 || result.ToolCalls[0].Function.Name != "fixture_lookup" {
							t.Fatalf("tool call = %+v", result.ToolCalls)
						}
						input = append(input, result, &schema.Message{Role: schema.Tool, ToolCallID: result.ToolCalls[0].ID, Content: `{"value":"fixture"}`})
					} else if result.Content != "ok" {
						t.Errorf("final answer = %q", result.Content)
					}
				}
			})
		}
	}
}

func TestGeminiArgonRejectsUnsupportedLongDecode(t *testing.T) {
	a := NewEinoAgent(service.NewChannelService(nil), nil, 4096, nil, nil, nil, nil, nil, nil)
	_, err := a.buildChatModel(t.Context(), &ChatRequest{
		Provider: "google", ModelID: "gemini-4-argon", MaxTokens: 65536,
		RuntimeChannel: &model.AIChannel{Adapter: service.AdapterGoogle, APIKey: "fixture-key"},
	}, modelbank.SearchDecision{})
	if err == nil || !strings.Contains(err.Error(), "32768") {
		t.Fatalf("unsupported long decode should fail before a request, got %v", err)
	}
}

func TestGeminiArgonUnspecifiedLimitStaysSingleRequest(t *testing.T) {
	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		requests <- body
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`)
	}))
	defer server.Close()
	a := NewEinoAgent(service.NewChannelService(nil), nil, 4096, nil, nil, nil, nil, nil, nil)
	cm, err := a.buildChatModel(t.Context(), &ChatRequest{
		Provider: "google", ModelID: "gemini-4-argon",
		RuntimeChannel: &model.AIChannel{Adapter: service.AdapterGoogle, APIKey: "fixture-key", BaseURL: server.URL},
	}, modelbank.SearchDecision{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = cm.Generate(t.Context(), []*schema.Message{schema.UserMessage("fixture")}); err != nil {
		t.Fatal(err)
	}
	body := <-requests
	cfg, _ := body["generationConfig"].(map[string]any)
	if cfg["maxOutputTokens"] != float64(32768) {
		t.Fatalf("unspecified output limit = %v", cfg["maxOutputTokens"])
	}
}
