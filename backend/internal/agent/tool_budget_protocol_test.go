package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/adk"
	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/service"
)

type budgetFixtureTool struct{ calls atomic.Int64 }

func (*budgetFixtureTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "file_list", Desc: "Synthetic budget fixture", ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{})}, nil
}

func (t *budgetFixtureTool) InvokableRun(context.Context, string, ...einoTool.Option) (string, error) {
	t.calls.Add(1)
	return `{"content":"fixture result"}`, nil
}

// Run the actual classic/typed ADK loops through the real HTTP adapters. A
// deliberately non-compliant provider calls a tool again despite choice=none;
// governance must still block execution, without changing the tool prefix.
func TestToolBudgetPreservesWireDefinitionsAndBlocksExecution(t *testing.T) {
	for _, adapter := range []string{service.AdapterAnthropic, service.AdapterOpenAIResponses} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", adapter, streaming), func(t *testing.T) {
				bodies := make(chan map[string]any, 4)
				var calls atomic.Int64
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						http.Error(w, "invalid fixture", 400)
						return
					}
					bodies <- body
					n := calls.Add(1)
					if n > 3 {
						http.Error(w, "unexpected loop", 400)
						return
					}
					var output string
					if adapter == service.AdapterAnthropic {
						output = `[{"type":"text","text":"done"}]`
						if n < 3 {
							output = fmt.Sprintf(`[{"type":"thinking","thinking":"fixture thought","signature":"fixture-signature"},{"type":"tool_use","id":"call_%d","name":"file_list","input":{}}]`, n)
						}
						w.Header().Set("Content-Type", "application/json")
						if streaming {
							w.Header().Set("Content-Type", "text/event-stream")
							fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"fixture\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n")
							var blocks []json.RawMessage
							_ = json.Unmarshal([]byte(output), &blocks)
							for i, block := range blocks {
								fmt.Fprintf(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":%d,\"content_block\":%s}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":%d}\n\n", i, block, i)
							}
							fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
						} else {
							fmt.Fprintf(w, `{"id":"fixture","type":"message","role":"assistant","content":%s,"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`, output)
						}
					} else {
						output = `[{"type":"message","id":"msg_fixture","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done","annotations":[]}]}]`
						if n < 3 {
							output = fmt.Sprintf(`[{"type":"function_call","id":"fc_%d","call_id":"call_%d","name":"file_list","arguments":"{}","status":"completed"}]`, n, n)
						}
						response := fmt.Sprintf(`{"id":"resp_fixture","status":"completed","model":"gpt-6.1-sol","output":%s,"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}`, output)
						w.Header().Set("Content-Type", "application/json")
						if streaming {
							w.Header().Set("Content-Type", "text/event-stream")
							if n < 3 {
								fmt.Fprintf(w, "data: {\"type\":\"response.output_item.added\",\"sequence_number\":0,\"output_index\":0,\"item\":{\"id\":\"fc_%d\",\"type\":\"function_call\",\"call_id\":\"call_%d\",\"name\":\"file_list\",\"arguments\":\"\",\"status\":\"in_progress\"}}\n\n", n, n)
								fmt.Fprintf(w, "data: {\"type\":\"response.function_call_arguments.delta\",\"sequence_number\":1,\"item_id\":\"fc_%d\",\"output_index\":0,\"delta\":\"{}\"}\n\n", n)
							} else {
								fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"sequence_number\":0,\"item_id\":\"msg_fixture\",\"output_index\":0,\"content_index\":0,\"delta\":\"done\",\"logprobs\":[]}\n\n")
							}
							fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":%s}\n\n", response)
						} else {
							fmt.Fprint(w, response)
						}
					}
				}))
				defer server.Close()
				budget := newToolBudgetMiddleware(&ChatRequest{})
				budget.maxRounds = 1
				tool := &budgetFixtureTool{}
				config := adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: []einoTool.BaseTool{tool}}}
				config.ToolCallMiddlewares = []compose.ToolMiddleware{toolGovernanceMiddleware(service.ToolRuntimeConfigSet{"file_list": {Enabled: true, TimeoutSeconds: 5}}, nil, nil, budget)}
				a := NewEinoAgent(service.NewChannelService(nil), nil, 4096, nil, nil, nil, nil, nil, nil)
				req := &ChatRequest{ModelID: "claude-haiku-5-5", Reasoning: true, RuntimeChannel: &model.AIChannel{Adapter: adapter, BaseURL: server.URL, APIKey: "fixture-key"}}
				var finalText string
				if adapter == service.AdapterAnthropic {
					cm, err := a.buildChatModel(t.Context(), req, modelbank.SearchDecision{})
					if err != nil {
						t.Fatal(err)
					}
					agent, err := adk.NewChatModelAgent(t.Context(), &adk.ChatModelAgentConfig{Model: cm, Instruction: "Stable fixture instruction", MaxIterations: 4, ToolsConfig: config, Handlers: []adk.ChatModelAgentMiddleware{budget}})
					if err != nil {
						t.Fatal(err)
					}
					iter := agent.Run(t.Context(), &adk.AgentInput{Messages: []*schema.Message{schema.UserMessage("fixture request")}, EnableStreaming: streaming})
					for event, ok := iter.Next(); ok; event, ok = iter.Next() {
						if event.Err != nil {
							t.Fatal(event.Err)
						}
						if event.Output != nil && event.Output.MessageOutput != nil {
							message, err := event.Output.MessageOutput.GetMessage()
							if err != nil {
								t.Fatal(err)
							}
							if message.Role == schema.Assistant {
								finalText += message.Content
							}
						}
					}
				} else {
					req.ModelID = "gpt-6.1-sol"
					cm, err := a.buildResponsesAgenticModel(t.Context(), req)
					if err != nil {
						t.Fatal(err)
					}
					agent, err := adk.NewTypedChatModelAgent(t.Context(), &adk.TypedChatModelAgentConfig[*schema.AgenticMessage]{Model: cm, Instruction: "Stable fixture instruction", MaxIterations: 4, ToolsConfig: config, Handlers: []adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage]{newAgenticToolBudgetMiddleware(budget)}})
					if err != nil {
						t.Fatal(err)
					}
					iter := agent.Run(t.Context(), &adk.TypedAgentInput[*schema.AgenticMessage]{Messages: []*schema.AgenticMessage{schema.UserAgenticMessage("fixture request")}, EnableStreaming: streaming})
					for event, ok := iter.Next(); ok; event, ok = iter.Next() {
						if event.Err != nil {
							t.Fatal(event.Err)
						}
						if event.Output != nil && event.Output.MessageOutput != nil {
							message, err := event.Output.MessageOutput.GetMessage()
							if err != nil {
								t.Fatal(err)
							}
							for _, block := range message.ContentBlocks {
								if block != nil && block.AssistantGenText != nil {
									finalText += block.AssistantGenText.Text
								}
							}
						}
					}
				}
				if calls.Load() != 3 || tool.calls.Load() != 1 {
					t.Fatalf("model=%d real tool=%d", calls.Load(), tool.calls.Load())
				}
				if finalText != "done" {
					t.Fatalf("final answer=%q", finalText)
				}
				first := <-bodies
				for i := 1; i < 3; i++ {
					body := <-bodies
					if !reflect.DeepEqual(first["tools"], body["tools"]) || body["tools"] == nil {
						t.Errorf("tool prefix changed: %v", body["tools"])
					}
					choice := body["tool_choice"]
					if native, ok := choice.(map[string]any); ok {
						choice = native["type"]
					}
					if choice != "none" {
						t.Errorf("budget tool_choice=%v", body["tool_choice"])
					}
					raw, _ := json.Marshal(body)
					if strings.Count(string(raw), "Do not call more tools") != 1 {
						t.Error("final notice must be appended exactly once")
					}
					if adapter == service.AdapterAnthropic && !strings.Contains(string(raw), "fixture-signature") {
						t.Error("same-run signed thinking was lost")
					}
				}
			})
		}
	}
}
