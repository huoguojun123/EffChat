package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cloudwego/eino/callbacks"
	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/modelstream"
	"github.com/huoguojun123/EffChat/internal/service"
)

func TestClaudeCacheUsageSurvivesWireAndCallbacks(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", streaming), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Stream bool `json:"stream"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				if body.Stream != streaming {
					t.Errorf("stream = %v", body.Stream)
				}
				if !streaming {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"id":"msg_fixture","type":"message","role":"assistant","model":"claude-sonnet-5-5","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":11,"cache_read_input_tokens":23,"cache_creation_input_tokens":37,"output_tokens":5}}`)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				// Input/cache usage arrives before text; completion usage arrives
				// later. Both must survive metadata buffering without double counting.
				fmt.Fprint(w, "event: message_start\ndata: "+`{"type":"message_start","message":{"id":"msg_fixture","type":"message","role":"assistant","model":"claude-sonnet-5-5","content":[],"usage":{"input_tokens":11,"cache_read_input_tokens":23,"cache_creation_input_tokens":37,"output_tokens":0}}}`+"\n\n")
				fmt.Fprint(w, "event: content_block_start\ndata: "+`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"ok"}}`+"\n\n")
				fmt.Fprint(w, "event: content_block_stop\ndata: "+`{"type":"content_block_stop","index":0}`+"\n\n")
				fmt.Fprint(w, "event: message_delta\ndata: "+`{"type":"message_delta","delta":{},"usage":{"output_tokens":3}}`+"\n\n")
				fmt.Fprint(w, "event: message_delta\ndata: "+`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`+"\n\n")
				fmt.Fprint(w, "event: message_stop\ndata: "+`{"type":"message_stop"}`+"\n\n")
			}))
			defer server.Close()
			a := NewEinoAgent(service.NewChannelService(nil), nil, 4096, nil, nil, nil, nil, nil, nil)
			cm, err := a.buildChatModel(t.Context(), &ChatRequest{
				ModelID: "claude-sonnet-5-5", Provider: "anthropic",
				RuntimeChannel: &model.AIChannel{Key: "fixture", Adapter: service.AdapterAnthropic, BaseURL: server.URL, APIKey: "fixture-key", Enabled: true},
			}, modelbank.SearchDecision{})
			if err != nil {
				t.Fatal(err)
			}
			observed := make(chan einoModel.TokenUsage, 1)
			handler := callbacks.NewHandlerBuilder().
				OnEndFn(func(ctx context.Context, _ *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
					observed <- *einoModel.ConvCallbackOutput(output).TokenUsage
					return ctx
				}).
				OnEndWithStreamOutputFn(func(ctx context.Context, _ *callbacks.RunInfo, reader *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
					defer reader.Close()
					var total einoModel.TokenUsage
					for {
						out, err := reader.Recv()
						if err == io.EOF {
							break
						}
						if err != nil {
							t.Error(err)
							break
						}
						u := einoModel.ConvCallbackOutput(out).TokenUsage
						if u == nil {
							continue
						}
						// The product recorder consumes the last usage snapshot.
						total = *u
					}
					observed <- total
					return ctx
				}).Build()
			ctx := callbacks.InitCallbacks(t.Context(), nil, handler)
			input := []*schema.Message{schema.UserMessage("fixture")}
			var result *schema.Message
			if streaming {
				result, err = modelstream.Collect(ctx, cm, input, time.Second)
			} else {
				result, err = cm.Generate(ctx, input)
			}
			if err != nil {
				t.Fatal(err)
			}
			u := result.ResponseMeta.Usage
			if u.PromptTokens != 71 || u.CompletionTokens != 5 || u.TotalTokens != 76 || u.PromptTokenDetails.CachedTokens != 23 || u.PromptTokenDetails.CacheWriteTokens != 37 {
				t.Errorf("message usage = %+v", u)
			}
			select {
			case u := <-observed:
				if u.PromptTokens != 71 || u.CompletionTokens != 5 || u.TotalTokens != 76 || u.PromptTokenDetails.CachedTokens != 23 || u.PromptTokenDetails.CacheWriteTokens != 37 {
					t.Errorf("callback usage = %+v", u)
				}
			case <-time.After(time.Second):
				t.Fatal("callback did not finish")
			}
		})
	}
}
