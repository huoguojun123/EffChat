package usage

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/EffChat/internal/modelstream"
)

type eventChannelStore struct {
	fakeStore
	events chan Event
}

func (s *eventChannelStore) Create(_ context.Context, event *Event) error {
	s.events <- *event
	return nil
}

type recordingChatStub struct {
	einoModel.ToolCallingChatModel
	reader *schema.StreamReader[*schema.Message]
}

func (m recordingChatStub) Stream(context.Context, []*schema.Message, ...einoModel.Option) (*schema.StreamReader[*schema.Message], error) {
	return m.reader, nil
}

type recordingAgenticStub struct {
	einoModel.AgenticModel
	reader *schema.StreamReader[*schema.AgenticMessage]
}

func (m recordingAgenticStub) Stream(context.Context, []*schema.AgenticMessage, ...einoModel.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	return m.reader, nil
}

func TestRecordingStreamsPreserveAuditContract(t *testing.T) {
	defaults := Meta{UserID: 7, SessionID: 11, ModelID: "fixture-model", Provider: "fixture"}
	ctx := WithMeta(context.Background(), Meta{RunID: "fixture-run", Kind: KindRetry})
	t.Run("classic", func(t *testing.T) {
		checkRecordingStream(t, func(reader *schema.StreamReader[*schema.Message], recorder *Service) (*schema.StreamReader[*schema.Message], error) {
			return WrapChatModel(recordingChatStub{reader: reader}, recorder, defaults).Stream(ctx, nil)
		}, func(usage *schema.TokenUsage) *schema.Message {
			return &schema.Message{ResponseMeta: &schema.ResponseMeta{Usage: usage}}
		})
	})
	t.Run("typed", func(t *testing.T) {
		checkRecordingStream(t, func(reader *schema.StreamReader[*schema.AgenticMessage], recorder *Service) (*schema.StreamReader[*schema.AgenticMessage], error) {
			return WrapAgenticModel(recordingAgenticStub{reader: reader}, recorder, defaults).Stream(ctx, nil)
		}, func(usage *schema.TokenUsage) *schema.AgenticMessage {
			return &schema.AgenticMessage{ResponseMeta: &schema.AgenticResponseMeta{TokenUsage: usage}}
		})
	})
}

func checkRecordingStream[T comparable](t *testing.T, wrap func(*schema.StreamReader[T], *Service) (*schema.StreamReader[T], error), message func(*schema.TokenUsage) T) {
	t.Helper()
	for _, mode := range []string{"eof", "upstream_error", "consumer_close", "nil_reader"} {
		t.Run(mode, func(t *testing.T) {
			store := &eventChannelStore{events: make(chan Event, 2)}
			recorder := NewService(store)
			upstream, writer := schema.Pipe[T](4)
			defer writer.Close()
			if mode == "nil_reader" {
				upstream.Close()
				upstream = nil
			}
			out, err := wrap(upstream, recorder)
			if mode == "nil_reader" {
				if !errors.Is(err, modelstream.ErrNilReader) || out != nil {
					t.Fatalf("nil reader contract: out=%v err=%v", out, err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				first := message(&schema.TokenUsage{PromptTokens: 3, CompletionTokens: 1, TotalTokens: 4})
				lastUsage := &schema.TokenUsage{PromptTokens: 13, CompletionTokens: 5, TotalTokens: 18}
				lastUsage.PromptTokenDetails.CachedTokens = 7
				lastUsage.CompletionTokensDetails.ReasoningTokens = 2
				last := message(lastUsage)
				writer.Send(first, nil)
				writer.Send(last, nil)
				for _, want := range []T{first, last} {
					got, recvErr := out.Recv()
					if recvErr != nil || got != want {
						t.Fatalf("changed chunk: %v, %v", got, recvErr)
					}
				}
				if mode == "consumer_close" {
					out.Close()
					// The provider still owns a blocked Recv. Supply the next
					// chunk so the wrapper observes its consumer's closed pipe.
					writer.Send(message(nil), nil)
				} else {
					defer out.Close()
					var zero T
					writer.Send(zero, nil)
					if got, recvErr := out.Recv(); recvErr != nil || got != zero {
						t.Fatalf("nil chunk: %v, %v", got, recvErr)
					}
					if mode == "upstream_error" {
						failure := errors.New("fixture upstream failed")
						writer.Send(zero, failure)
						if _, recvErr := out.Recv(); !errors.Is(recvErr, failure) {
							t.Fatalf("lost upstream error: %v", recvErr)
						}
					} else {
						writer.Send(zero, io.EOF)
						if _, recvErr := out.Recv(); !errors.Is(recvErr, io.EOF) {
							t.Fatalf("EOF: %v", recvErr)
						}
					}
				}
			}
			select {
			case event := <-store.events:
				if event.UserID != 7 || event.SessionID != 11 || event.RunID != "fixture-run" || event.Kind != KindRetry || event.Provider != "fixture" || event.ModelID != "fixture-model" {
					t.Fatalf("lost audit identity: %+v", event)
				}
				if event.Success != (mode == "eof") {
					t.Fatalf("wrong success: %+v", event)
				}
				if mode != "nil_reader" && (event.PromptTokens != 13 || event.CompletionTokens != 5 || event.TotalTokens != 18 || event.CachedTokens != 7 || event.ReasoningTokens != 2) {
					t.Fatalf("expected last cumulative usage, not sum: %+v", event)
				}
				if mode == "consumer_close" && event.ErrorType != "canceled" {
					t.Fatalf("close classification: %+v", event)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("usage recording did not finish")
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if !recorder.Drain(ctx) {
				t.Fatal("recorder did not drain")
			}
			if len(store.events) != 0 {
				t.Fatal("model call recorded more than once")
			}
		})
	}
}
