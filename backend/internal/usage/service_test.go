package usage

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/EffChat/internal/modelstream"
)

type fakeStore struct {
	event     Event
	toolEvent ToolEvent
}

func (s *fakeStore) Create(_ context.Context, event *Event) error {
	s.event = *event
	return nil
}

func (s *fakeStore) Aggregate(_ context.Context, _, _ time.Time) (*Summary, error) {
	return &Summary{}, nil
}

func (s *fakeStore) CreateToolEvent(_ context.Context, event *ToolEvent) error {
	s.toolEvent = *event
	return nil
}

func (s *fakeStore) UpdateToolEvent(_ context.Context, event *ToolEvent) error {
	s.toolEvent = *event
	return nil
}

func (s *fakeStore) AggregateToolUsage(_ context.Context, _, _ time.Time) (ToolTotals, []ByTool, error) {
	return ToolTotals{}, nil, nil
}

func (s *fakeStore) QuotaUsersForToday(_ context.Context) ([]QuotaUserUsage, error) {
	return nil, nil
}

func TestServiceRecordNormalizesEvent(t *testing.T) {
	store := &fakeStore{}
	svc := NewService(store)

	event := Event{
		Provider:         "  ",
		ModelID:          "",
		Kind:             "weird",
		PromptTokens:     -1,
		CompletionTokens: -1,
		TotalTokens:      -1,
		ErrorMessage:     string(make([]rune, maxErrorMessageRunes+20)),
	}
	normalizeEvent(&event)
	svc.recordSync(event)

	if store.event.Kind != KindChat {
		t.Fatalf("kind = %q, want %q", store.event.Kind, KindChat)
	}
	if store.event.Provider != "unknown" || store.event.ModelID != "unknown" {
		t.Fatalf("provider/model = %q/%q, want unknown/unknown", store.event.Provider, store.event.ModelID)
	}
	if store.event.TotalTokens != 0 || store.event.PromptTokens != 0 || store.event.CompletionTokens != 0 {
		t.Fatalf("negative tokens should normalize to 0: %#v", store.event)
	}
	if len([]rune(store.event.ErrorMessage)) > maxErrorMessageRunes {
		t.Fatalf("error message was not truncated")
	}
}

func TestWithMetaMergesContext(t *testing.T) {
	ctx := WithMeta(context.Background(), Meta{UserID: 1, Kind: KindChat, Provider: "openai"})
	ctx = WithMeta(ctx, Meta{Kind: KindRetry, ModelID: "gpt-4o"})

	meta := MetaFromContext(ctx)
	if meta.UserID != 1 || meta.Kind != KindRetry || meta.Provider != "openai" || meta.ModelID != "gpt-4o" {
		t.Fatalf("merged meta mismatch: %#v", meta)
	}
}

func TestErrorTypeDistinguishesFirstOutputTimeout(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "first output timeout", err: modelstream.ErrFirstOutputTimeout, want: "first_output_timeout"},
		{name: "wrapped first output timeout", err: fmt.Errorf("stream failed: %w", modelstream.ErrFirstOutputTimeout), want: "first_output_timeout"},
		{name: "semantic cancellation", err: context.Canceled, want: "canceled"},
		{name: "ordinary timeout", err: context.DeadlineExceeded, want: "timeout"},
		{name: "model failure", err: errors.New("provider failed"), want: "model_error"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if got := ErrorType(testCase.err); got != testCase.want {
				t.Fatalf("ErrorType(%v) = %q, want %q", testCase.err, got, testCase.want)
			}
		})
	}
}

func TestServiceRecordToolNormalizesEvent(t *testing.T) {
	store := &fakeStore{}
	svc := NewService(store)

	event := ToolEvent{
		ToolKey:       "  ",
		ContextTokens: -1,
		DurationMs:    -1,
		ErrorMessage:  string(make([]rune, maxErrorMessageRunes+20)),
	}
	normalizeToolEvent(&event)
	svc.recordToolSync(event)

	if store.toolEvent.ToolKey != "unknown" {
		t.Fatalf("tool key = %q, want unknown", store.toolEvent.ToolKey)
	}
	if store.toolEvent.ContextTokens != 0 || store.toolEvent.DurationMs != 0 {
		t.Fatalf("negative values should normalize to zero: %#v", store.toolEvent)
	}
	if len([]rune(store.toolEvent.ErrorMessage)) > maxErrorMessageRunes {
		t.Fatal("tool error message was not truncated")
	}
}

func TestServiceFinishToolUpdatesReservedEvent(t *testing.T) {
	store := &fakeStore{}
	svc := NewService(store)

	svc.FinishToolSync(ToolEvent{
		ID:            42,
		ToolKey:       "web_search",
		ContextTokens: 12,
		DurationMs:    34,
		Success:       true,
	})

	if store.toolEvent.ID != 42 || store.toolEvent.ToolKey != "web_search" || !store.toolEvent.Success {
		t.Fatalf("tool event update mismatch: %#v", store.toolEvent)
	}
}

func TestServiceDrainRejectsNewAsyncRecords(t *testing.T) {
	store := &fakeStore{}
	svc := NewService(store)
	if !svc.startTask() {
		t.Fatal("usage task should start before drain")
	}
	drained := make(chan bool, 1)
	go func() {
		drained <- svc.Drain(context.Background())
	}()
	select {
	case <-drained:
		t.Fatal("usage drain returned before the active task completed")
	case <-time.After(10 * time.Millisecond):
	}
	svc.tasks.Done()
	if !<-drained {
		t.Fatal("usage service should drain after the active task completes")
	}
	if !svc.Drain(context.Background()) {
		t.Fatal("empty usage service should drain immediately")
	}
	svc.Record(Event{Provider: "openai", ModelID: "gpt-5.6"})
	svc.RecordTool(ToolEvent{ToolKey: "web_search"})
	if store.event.ModelID != "" || store.toolEvent.ToolKey != "" {
		t.Fatal("async usage records should be rejected after drain begins")
	}
}

func TestEstimateTextTokensUsesConservativeUTF8Approximation(t *testing.T) {
	if got := EstimateTextTokens("abcdefg"); got != 3 {
		t.Fatalf("EstimateTextTokens = %d, want 3", got)
	}
	if got := EstimateTextTokens("中文内容"); got != 4 {
		t.Fatalf("EstimateTextTokens Chinese = %d, want 4", got)
	}
}

func TestInferKindDetectsCompressionInstruction(t *testing.T) {
	kind := inferKind(KindChat, []*schema.Message{{
		Content: "Create a detailed continuation summary for this conversation and put the final result in <summary>.",
	}})

	if kind != KindCompression {
		t.Fatalf("kind = %q, want %q", kind, KindCompression)
	}
}
