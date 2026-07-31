package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/EffChat/internal/modelstream"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/pkg/streaming"
)

type blockingChatRunStore struct {
	mu                sync.Mutex
	userMessageID     int64
	bindStarted       chan struct{}
	releaseBind       chan struct{}
	transitionStarted chan struct{}
}

type blockingJSONPayload struct {
	started chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (p *blockingJSONPayload) MarshalJSON() ([]byte, error) {
	p.once.Do(func() { close(p.started) })
	<-p.release
	return []byte(`{"status":"blocked"}`), nil
}

type selectiveBlockingChatRunStore struct {
	blockedRunID string
}

func (s *selectiveBlockingChatRunStore) BindChatRunUserMessage(context.Context, string, int64) (bool, error) {
	return true, nil
}

func (s *selectiveBlockingChatRunStore) TransitionChatRun(ctx context.Context, input repository.ChatRunTransitionInput) (repository.ChatRunRecord, bool, error) {
	if input.RunID == s.blockedRunID {
		<-ctx.Done()
		return repository.ChatRunRecord{}, false, ctx.Err()
	}
	now := time.Now()
	return repository.ChatRunRecord{
		RunID: input.RunID, Status: input.Status, CancelCause: input.CancelCause,
		PublicErrorCode: input.PublicErrorCode, PublicErrorMessage: input.PublicErrorMessage,
		TerminalMessageID: input.TerminalMessageID, TerminalEvent: input.TerminalEvent,
		AcceptedAt: now, TerminalAt: &now, ExpiresAt: input.ExpiresAt,
	}, true, nil
}

func (s *blockingChatRunStore) BindChatRunUserMessage(ctx context.Context, _ string, userMessageID int64) (bool, error) {
	select {
	case s.bindStarted <- struct{}{}:
	default:
	}
	select {
	case <-s.releaseBind:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	s.mu.Lock()
	s.userMessageID = userMessageID
	s.mu.Unlock()
	return true, nil
}

func (s *blockingChatRunStore) TransitionChatRun(_ context.Context, input repository.ChatRunTransitionInput) (repository.ChatRunRecord, bool, error) {
	select {
	case s.transitionStarted <- struct{}{}:
	default:
	}
	s.mu.Lock()
	userMessageID := s.userMessageID
	s.mu.Unlock()
	now := time.Now()
	return repository.ChatRunRecord{
		RunID: input.RunID, Status: input.Status, CancelCause: input.CancelCause,
		PublicErrorCode: input.PublicErrorCode, PublicErrorMessage: input.PublicErrorMessage,
		UserMessageID: userMessageID, TerminalMessageID: input.TerminalMessageID,
		TerminalEvent: input.TerminalEvent, AcceptedAt: now, TerminalAt: &now, ExpiresAt: input.ExpiresAt,
	}, true, nil
}

func terminalRecord(input repository.ChatRunTransitionInput) repository.ChatRunRecord {
	now := time.Now()
	return repository.ChatRunRecord{
		RunID: input.RunID, Status: input.Status, CancelCause: input.CancelCause,
		PublicErrorCode: input.PublicErrorCode, PublicErrorMessage: input.PublicErrorMessage,
		TerminalMessageID: input.TerminalMessageID, TerminalEvent: input.TerminalEvent,
		AcceptedAt: now, TerminalAt: &now, ExpiresAt: input.ExpiresAt,
	}
}

func TestRunHubStart_ReusesExistingRunWithoutResettingSnapshot(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)

	first, err := hub.Start(1, 2, 10, "run-1", RunKindChat)
	if err != nil {
		t.Fatalf("first start: %v", err)
	}
	if first.Reused {
		t.Fatal("first start should not be marked reused")
	}
	hub.Record("run-1", "content_delta", map[string]interface{}{"delta": "hello"})
	hub.Record("run-1", "thinking_delta", map[string]interface{}{"delta": "thinking"})

	second, err := hub.Start(1, 2, 99, "run-1", RunKindChat)
	if err != nil {
		t.Fatalf("second start: %v", err)
	}
	if !second.Reused {
		t.Fatal("second start should be marked reused")
	}
	if second.UserMessageID != 10 {
		t.Fatalf("user_message_id = %d, want original 10", second.UserMessageID)
	}
	if second.Content != "hello" {
		t.Fatalf("content = %q, want preserved snapshot", second.Content)
	}
	if second.Thinking != "thinking" {
		t.Fatalf("thinking = %q, want preserved snapshot", second.Thinking)
	}
}

func TestRunHubStart_RejectsRunIDScopeConflict(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)

	if _, err := hub.Start(1, 2, 10, "run-1", RunKindChat); err != nil {
		t.Fatalf("first start: %v", err)
	}

	conflicts := []struct {
		name      string
		sessionID int64
		userID    int64
		kind      string
	}{
		{name: "different session", sessionID: 9, userID: 2, kind: RunKindChat},
		{name: "different user", sessionID: 1, userID: 9, kind: RunKindChat},
		{name: "different kind", sessionID: 1, userID: 2, kind: RunKindCompaction},
	}
	for _, tt := range conflicts {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := hub.Start(tt.sessionID, tt.userID, 11, "run-1", tt.kind); !errors.Is(err, ErrRunIDConflict) {
				t.Fatalf("Start error = %v, want ErrRunIDConflict", err)
			}
		})
	}
}

func TestRunHubStart_RejectsAnotherMutatingRunInSameSession(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	if _, err := hub.Start(1, 2, 10, "chat-1", RunKindChat); err != nil {
		t.Fatalf("start chat: %v", err)
	}

	for _, kind := range []string{RunKindChat, RunKindCompaction} {
		if _, err := hub.Start(1, 2, 0, "next-"+kind, kind); !errors.Is(err, ErrSessionRunActive) {
			t.Fatalf("Start kind=%s error = %v, want ErrSessionRunActive", kind, err)
		}
	}

	if _, err := hub.Start(9, 2, 0, "other-session", RunKindChat); err != nil {
		t.Fatalf("different session should run concurrently: %v", err)
	}
}

func TestRunHubStart_AllowsNextRunAfterTerminalState(t *testing.T) {
	for _, finish := range []struct {
		name string
		fn   func(*RunHub)
	}{
		{name: "completed", fn: func(h *RunHub) { h.Complete("run-1", nil, nil) }},
		{name: "failed", fn: func(h *RunHub) { h.Fail("run-1", "failed") }},
		{name: "canceled", fn: func(h *RunHub) { h.Canceled("run-1", nil, nil) }},
	} {
		t.Run(finish.name, func(t *testing.T) {
			hub := NewRunHub(time.Minute, 1<<20)
			if _, err := hub.Start(1, 2, 10, "run-1", RunKindChat); err != nil {
				t.Fatalf("first start: %v", err)
			}
			finish.fn(hub)
			if _, err := hub.Start(1, 2, 11, "run-2", RunKindChat); err != nil {
				t.Fatalf("next start after %s: %v", finish.name, err)
			}
		})
	}
}

func TestRunHubStart_AtomicallyReservesSession(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, runID := range []string{"run-1", "run-2"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			<-start
			_, err := hub.Start(1, 2, 0, id, RunKindChat)
			errs <- err
		}(runID)
	}
	close(start)
	wg.Wait()
	close(errs)

	var started, rejected int
	for err := range errs {
		switch {
		case err == nil:
			started++
		case errors.Is(err, ErrSessionRunActive):
			rejected++
		default:
			t.Fatalf("unexpected start error: %v", err)
		}
	}
	if started != 1 || rejected != 1 {
		t.Fatalf("started=%d rejected=%d, want 1 and 1", started, rejected)
	}
}

func TestRunHubSetUserMessageID_PreservesOriginalAssignment(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	if _, err := hub.Start(1, 2, 0, "run-1", RunKindChat); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !hub.SetUserMessageID("run-1", 10) {
		t.Fatal("expected first assignment to succeed")
	}
	if hub.SetUserMessageID("run-1", 99) {
		t.Fatal("second assignment should not overwrite the original message")
	}
	snapshot, ok := hub.Get("run-1", 1, 2)
	if !ok || snapshot.UserMessageID != 10 {
		t.Fatalf("snapshot = %#v, want user_message_id=10", snapshot)
	}
}

func TestRunHubAttemptResetRollsBackSnapshotSuffix(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	run, err := hub.Start(41, 51, 61, "run-attempt-reset", RunKindChat)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	hub.Record(run.RunID, "content_delta", map[string]interface{}{"delta": "stable坏答案"})
	hub.Record(run.RunID, "thinking_delta", map[string]interface{}{"delta": "plan错误思路"})
	hub.Record(run.RunID, "assistant_attempt_reset", map[string]interface{}{"content_runes": 3, "thinking_runes": 4})

	snapshot, ok := hub.Get(run.RunID, 41, 51)
	if !ok {
		t.Fatal("run snapshot not found")
	}
	if snapshot.Content != "stable" || snapshot.Thinking != "plan" {
		t.Fatalf("snapshot content=%q thinking=%q, want stable/plan", snapshot.Content, snapshot.Thinking)
	}
}

func TestRunHubSnapshotPreservesInterleavedOutputOrder(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	run, err := hub.Start(1, 2, 3, "interleaved-output", RunKindChat)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	hub.Record(run.RunID, "thinking_delta", map[string]interface{}{"delta": "先检索"})
	hub.Record(run.RunID, "tool_call_start", map[string]interface{}{"tool_call_id": "tool-1", "tool_name": "web_search"})
	hub.Record(run.RunID, "tool_call_result", map[string]interface{}{"tool_call_id": "tool-1", "result": "完成"})
	hub.Record(run.RunID, "thinking_delta", map[string]interface{}{"delta": "再核对"})
	hub.Record(run.RunID, "content_delta", map[string]interface{}{"delta": "最终答案"})

	snapshot, ok := hub.Get(run.RunID, 1, 2)
	if !ok {
		t.Fatal("run snapshot not found")
	}
	if len(snapshot.Segments) != 4 {
		t.Fatalf("segments = %#v, want four ordered segments", snapshot.Segments)
	}
	if snapshot.Segments[0].Thinking != "先检索" || snapshot.Segments[1].Type != "tool" || snapshot.Segments[2].Thinking != "再核对" || snapshot.Segments[3].Content != "最终答案" {
		t.Fatalf("segments = %#v, want thinking -> tool -> thinking -> content", snapshot.Segments)
	}
	if got := snapshot.Segments[1].ToolCalls[0]["status"]; got != "done" {
		t.Fatalf("tool status = %v, want done", got)
	}
}

func TestRunHubBoundsSnapshotAlongsideReplayEvents(t *testing.T) {
	const maxBytes = 1024
	hub := NewRunHub(time.Minute, maxBytes)
	run, err := hub.Start(1, 2, 0, "bounded-cache", RunKindChat)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	hub.Record(run.RunID, "content_delta", map[string]interface{}{"delta": strings.Repeat("content", 300)})
	hub.Record(run.RunID, "thinking_delta", map[string]interface{}{"delta": strings.Repeat("thinking", 300)})
	hub.Record(run.RunID, "tool_call_start", map[string]interface{}{"tool_call_id": "tool-1", "tool_name": "web_extract"})
	hub.Record(run.RunID, "tool_call_result", map[string]interface{}{"tool_call_id": "tool-1", "result": strings.Repeat("result", 300)})

	hub.mu.RLock()
	state := hub.runs[run.RunID]
	if state == nil {
		hub.mu.RUnlock()
		t.Fatal("run state missing")
	}
	cacheBytes := state.cacheBytes()
	replayFrom := state.ReplayFrom
	hub.mu.RUnlock()
	if cacheBytes > maxBytes {
		t.Fatalf("cache bytes = %d, want <= %d", cacheBytes, maxBytes)
	}
	if replayFrom == 0 {
		t.Fatal("large replay data should advance replay_from")
	}

	snapshot, ok := hub.Get(run.RunID, 1, 2)
	if !ok {
		t.Fatal("snapshot missing")
	}
	if snapshot.ReplayFrom != replayFrom {
		t.Fatalf("snapshot replay_from = %d, want %d", snapshot.ReplayFrom, replayFrom)
	}
	if !snapshot.OutputTruncated {
		t.Fatal("snapshot should mark incomplete cached output")
	}
	if len(snapshot.Content)+len(snapshot.Thinking) >= 3000 {
		t.Fatalf("snapshot retained oversized text: content=%d thinking=%d", len(snapshot.Content), len(snapshot.Thinking))
	}
}

func BenchmarkRunHubRecordDeltas(b *testing.B) {
	for _, deltaCount := range []int{1_000, 5_000, 10_000} {
		b.Run(fmt.Sprintf("%d", deltaCount), func(b *testing.B) {
			hub := NewRunHub(time.Minute, 1<<20)
			run, err := hub.Start(1, 2, 0, fmt.Sprintf("benchmark-%d", deltaCount), RunKindChat)
			if err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for j := 0; j < deltaCount; j++ {
					if !hub.Record(run.RunID, "content_delta", map[string]interface{}{"delta": "x"}) {
						b.Fatal("record delta")
					}
				}
			}
		})
	}
}

func TestRunHubFailIfRunning_DoesNotOverwriteTerminalState(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	if _, err := hub.Start(1, 2, 10, "run-1", RunKindChat); err != nil {
		t.Fatalf("start: %v", err)
	}
	hub.Complete("run-1", nil, nil)
	if hub.FailIfRunning("run-1", "late failure") {
		t.Fatal("completed run should not be changed")
	}
	snapshot, ok := hub.Get("run-1", 1, 2)
	if !ok || snapshot.Status != RunStatusCompleted {
		t.Fatalf("snapshot = %#v, want completed", snapshot)
	}
}

func TestRunHubFailIfRunning_ClosesActiveRun(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	if _, err := hub.Start(1, 2, 10, "run-1", RunKindChat); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !hub.FailIfRunning("run-1", "setup failed") {
		t.Fatal("running run should be marked failed")
	}
	snapshot, ok := hub.Get("run-1", 1, 2)
	if !ok || snapshot.Status != RunStatusFailed || snapshot.Error != "setup failed" {
		t.Fatalf("snapshot = %#v, want failed setup error", snapshot)
	}
}

func TestRunHubEventsAfterCleanupAfterCompleteDoesNotPanic(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	if _, err := hub.Start(1, 2, 10, "run-1", RunKindChat); err != nil {
		t.Fatalf("start: %v", err)
	}
	_, ch, cleanup, _, err := hub.EventsAfter("run-1", 1, 2, 0)
	if err != nil {
		t.Fatalf("events after: %v", err)
	}
	if ch == nil {
		t.Fatal("expected live subscriber")
	}
	hub.Complete("run-1", nil, nil)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("cleanup after complete panicked: %v", r)
		}
	}()
	cleanup()
	cleanup()
}

func TestRunHubEventsSinceRecoversSubscriberOverflow(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	run, err := hub.Start(1, 2, 0, "overflow", RunKindChat)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	_, ch, cleanup, _, err := hub.EventsAfter(run.RunID, 1, 2, 0)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer cleanup()
	for i := 0; i < 80; i++ {
		hub.Record(run.RunID, "content_delta", map[string]interface{}{"delta": "x"})
	}
	hub.Complete(run.RunID, nil, nil)

	lastCursor := int64(0)
	for event := range ch {
		lastCursor = event.Cursor
	}
	if lastCursor >= 80 {
		t.Fatalf("test did not overflow subscriber channel: cursor=%d", lastCursor)
	}
	missed, _, err := hub.EventsSince(run.RunID, 1, 2, lastCursor)
	if err != nil {
		t.Fatalf("load missed events: %v", err)
	}
	if len(missed) == 0 || missed[len(missed)-1].Cursor != 81 || missed[len(missed)-1].Event != "message_complete" {
		t.Fatalf("missed events do not reach final cursor: len=%d", len(missed))
	}
}

func TestRunHubFailureKeepsPublicErrorMetadata(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	run, err := hub.Start(1, 2, 0, "failed-run", RunKindChat)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	hub.Record(run.RunID, "error", map[string]interface{}{
		"error":      "消息创建失败，请重试",
		"code":       "message_create_failed",
		"request_id": "req-failed-run",
	})
	hub.Fail(run.RunID, "sql: password=secret")

	snapshot, ok := hub.Get(run.RunID, 1, 2)
	if !ok {
		t.Fatal("failed run missing")
	}
	if snapshot.Error != "消息创建失败，请重试" || snapshot.ErrorCode != "message_create_failed" || snapshot.RequestID != "req-failed-run" {
		t.Fatalf("snapshot leaked or lost public metadata: %+v", snapshot)
	}
}

func TestRunHubCancelSessionCancelsOnlyMatchingSession(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	bound, err := hub.Start(1, 42, 0, "bound", RunKindChat)
	if err != nil {
		t.Fatalf("start bound: %v", err)
	}
	pending, err := hub.Start(2, 42, 0, "pending", RunKindCompaction)
	if err != nil {
		t.Fatalf("start pending: %v", err)
	}
	boundCanceled := make(chan struct{}, 1)
	hub.Bind(bound.RunID, func() { boundCanceled <- struct{}{} })

	if got := hub.CancelSession(1, 42); got != 1 {
		t.Fatalf("canceled = %d, want 1", got)
	}
	select {
	case <-boundCanceled:
	default:
		t.Fatal("bound run was not canceled")
	}
	if hub.IsCanceled(bound.RunID) {
		t.Fatal("cancellation request became terminal before the worker finalized")
	}
	if cause := hub.CancelCause(bound.RunID); cause != RunCancelSessionDeleted {
		t.Fatalf("bound cancel cause = %q", cause)
	}
	if hub.IsCanceled(pending.RunID) {
		t.Fatal("other session run should remain active")
	}
	hub.Canceled(bound.RunID, nil, nil)
	if snapshot, ok := hub.Get(bound.RunID, 1, 42); !ok || snapshot.Status != RunStatusCanceled {
		t.Fatalf("bound terminal snapshot = %+v", snapshot)
	}
}

func TestRunHubDrainRejectsNewRunsAndCancelsRemainingRuns(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	run, err := hub.Start(1, 2, 0, "drain-run", RunKindChat)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	hub.BeginDrain()
	if _, err := hub.Start(2, 2, 0, "new-run", RunKindChat); !errors.Is(err, ErrServerDraining) {
		t.Fatalf("new run error = %v, want ErrServerDraining", err)
	}
	if reused, err := hub.Start(1, 2, 0, run.RunID, RunKindChat); err != nil || !reused.Reused {
		t.Fatalf("existing run should remain resumable: snapshot=%+v err=%v", reused, err)
	}
	canceled := make(chan struct{}, 1)
	hub.Bind(run.RunID, func() { canceled <- struct{}{} })
	if got := hub.CancelForDrain(); got != 1 {
		t.Fatalf("canceled = %d, want 1", got)
	}
	select {
	case <-canceled:
	default:
		t.Fatal("draining did not cancel run context")
	}
	snapshot, ok := hub.Get(run.RunID, 1, 2)
	if !ok || snapshot.Status != RunStatusRunning || !hub.IsServerDraining(run.RunID) {
		t.Fatalf("drain cancellation request = %+v", snapshot)
	}
	hub.Canceled(run.RunID, nil, nil)
	snapshot, ok = hub.Get(run.RunID, 1, 2)
	if !ok || snapshot.Status != RunStatusCanceled || snapshot.CancelCause != string(RunCancelServerDrain) {
		t.Fatalf("drained snapshot = %+v", snapshot)
	}
}

func TestRunHubRejectsMutationsAfterTerminalTransition(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	run, err := hub.Start(1, 2, 0, "terminal-run", RunKindChat)
	if err != nil {
		t.Fatal(err)
	}
	hub.Record(run.RunID, "content_delta", map[string]interface{}{"delta": "stable"})
	hub.Complete(run.RunID, nil, nil)
	before, _ := hub.Get(run.RunID, 1, 2)

	if hub.Record(run.RunID, "content_delta", map[string]interface{}{"delta": "late"}) {
		t.Fatal("late record was accepted")
	}
	if hub.Bind(run.RunID, func() {}) {
		t.Fatal("late bind was accepted")
	}
	if hub.SetUserMessageID(run.RunID, 99) {
		t.Fatal("late user message binding was accepted")
	}
	called := false
	if err := hub.PersistDurable(context.Background(), run.RunID, func(context.Context) error {
		called = true
		return nil
	}); !errors.Is(err, ErrRunTerminal) || called {
		t.Fatalf("late durable persistence = err:%v called:%v", err, called)
	}
	if hub.Cancel(run.RunID, 1, 2) {
		t.Fatal("late cancellation was accepted")
	}
	after, _ := hub.Get(run.RunID, 1, 2)
	if after.Cursor != before.Cursor || after.Content != before.Content || !after.ExpiresAt.Equal(before.ExpiresAt) {
		t.Fatalf("terminal snapshot changed: before=%+v after=%+v", before, after)
	}
}

func TestRunHubCancellationRequestOverridesConcurrentFailure(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	run, err := hub.Start(1, 2, 0, "cancel-over-failure", RunKindCompaction)
	if err != nil {
		t.Fatal(err)
	}
	if !hub.CancelWithCause(run.RunID, 1, 2, RunCancelUserStop) {
		t.Fatal("cancel request failed")
	}
	_, transitioned, event, err := hub.Transition(context.Background(), run.RunID, RunTerminal{
		Status: RunStatusFailed,
		Event:  "compaction_complete",
		Data:   map[string]interface{}{"compacted": true},
	})
	if err != nil || !transitioned {
		t.Fatalf("terminal transition = transitioned:%v err:%v", transitioned, err)
	}
	snapshot, ok := hub.Get(run.RunID, 1, 2)
	if !ok || snapshot.Status != RunStatusCanceled || snapshot.CancelCause != string(RunCancelUserStop) {
		t.Fatalf("terminal snapshot = %+v", snapshot)
	}
	if event == nil || event.Event != "compaction_skip" {
		t.Fatalf("terminal event = %+v", event)
	}
	payload := toMap(event.Data)
	if payload["reason"] != "canceled" {
		t.Fatalf("terminal payload = %+v", payload)
	}
}

func TestRunHubCancellationPreservesExplicitIncompleteMessageCompletion(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	run, err := hub.Start(1, 2, 0, "cancel-over-partial", RunKindChat)
	if err != nil {
		t.Fatal(err)
	}
	if err := hub.PersistDurable(context.Background(), run.RunID, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !hub.CancelWithCause(run.RunID, 1, 2, RunCancelServerDrain) {
		t.Fatal("cancel request failed")
	}

	snapshot, transitioned, event, err := hub.TransitionWithCommit(context.Background(), run.RunID, func(_ context.Context, input repository.ChatRunTransitionInput) (repository.ChatRunRecord, bool, error) {
		if input.Status != RunStatusCanceled {
			t.Fatalf("terminal status = %q, want canceled", input.Status)
		}
		return terminalRecord(input), true, nil
	}, RunTerminal{
		Status: RunStatusFailed,
		Event:  streaming.EventMessageComplete,
		Data: streaming.MessageCompleteEvent{
			FinishReason: "error",
			Incomplete:   true,
		},
	})
	if err != nil || !transitioned {
		t.Fatalf("terminal transition = snapshot:%+v transitioned:%v err:%v", snapshot, transitioned, err)
	}
	if snapshot.Status != RunStatusCanceled || event == nil || event.Event != streaming.EventMessageComplete {
		t.Fatalf("terminal result = snapshot:%+v event:%+v", snapshot, event)
	}
	payload := toMap(event.Data)
	if payload["incomplete"] != true || payload["finish_reason"] != "error" {
		t.Fatalf("incomplete terminal payload = %+v", payload)
	}
}

func TestRunHubTerminalPersistenceRespectsCancellationOrdering(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	hub.SetStore(&blockingChatRunStore{})
	canceled, err := hub.Start(1, 2, 0, "cancel-before-persist", RunKindCompaction)
	if err != nil {
		t.Fatal(err)
	}
	if err := hub.PersistDurable(context.Background(), canceled.RunID, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !hub.Cancel(canceled.RunID, 1, 2) {
		t.Fatal("cancel request failed")
	}
	persisted := false
	snapshot, transitioned, event, err := hub.TransitionWithCommit(context.Background(), canceled.RunID, func(_ context.Context, input repository.ChatRunTransitionInput) (repository.ChatRunRecord, bool, error) {
		persisted = input.Status == RunStatusCompleted
		return terminalRecord(input), true, nil
	}, RunTerminal{Status: RunStatusCompleted})
	if err != nil || !transitioned || persisted {
		t.Fatalf("canceled persistence = snapshot:%+v transitioned:%v persisted:%v err:%v", snapshot, transitioned, persisted, err)
	}
	if snapshot.Status != RunStatusCanceled || event == nil || event.Event != "compaction_skip" {
		t.Fatalf("canceled terminal = snapshot:%+v event:%+v", snapshot, event)
	}

	completing, err := hub.Start(2, 2, 0, "persist-before-cancel", RunKindCompaction)
	if err != nil {
		t.Fatal(err)
	}
	if err := hub.PersistDurable(context.Background(), completing.RunID, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	persistStarted := make(chan struct{})
	releasePersist := make(chan struct{})
	done := make(chan *RunSnapshot, 1)
	go func() {
		result, _, _, transitionErr := hub.TransitionWithCommit(context.Background(), completing.RunID, func(_ context.Context, input repository.ChatRunTransitionInput) (repository.ChatRunRecord, bool, error) {
			close(persistStarted)
			<-releasePersist
			return terminalRecord(input), true, nil
		}, RunTerminal{Status: RunStatusCompleted})
		if transitionErr != nil {
			done <- nil
			return
		}
		done <- result
	}()
	<-persistStarted
	if hub.Cancel(completing.RunID, 2, 2) {
		t.Fatal("cancel request was accepted after terminal persistence started")
	}
	close(releasePersist)
	if result := <-done; result == nil || result.Status != RunStatusCompleted {
		t.Fatalf("completed terminal = %+v", result)
	}

	failing, err := hub.Start(3, 2, 0, "cancel-during-failed-persist", RunKindCompaction)
	if err != nil {
		t.Fatal(err)
	}
	if err := hub.PersistDurable(context.Background(), failing.RunID, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	failingStarted := make(chan struct{})
	releaseFailing := make(chan struct{})
	failingDone := make(chan error, 1)
	go func() {
		_, _, _, transitionErr := hub.TransitionWithCommit(context.Background(), failing.RunID, func(context.Context, repository.ChatRunTransitionInput) (repository.ChatRunRecord, bool, error) {
			close(failingStarted)
			<-releaseFailing
			return repository.ChatRunRecord{}, false, errors.New("checkpoint unavailable")
		}, RunTerminal{Status: RunStatusCompleted})
		failingDone <- transitionErr
	}()
	<-failingStarted
	if hub.Cancel(failing.RunID, 3, 2) {
		t.Fatal("cancel request was accepted while terminal persistence was in flight")
	}
	close(releaseFailing)
	if err := <-failingDone; err == nil {
		t.Fatal("terminal persistence failure was lost")
	}
	snapshot, transitioned, _, err = hub.Transition(context.Background(), failing.RunID, RunTerminal{Status: RunStatusFailed, FinalizationFailure: true})
	if err != nil || !transitioned || snapshot.Status != RunStatusFailed {
		t.Fatalf("failed persistence terminal = snapshot:%+v transitioned:%v err:%v", snapshot, transitioned, err)
	}
}

func TestRunHubCancellationCauseReachesRunContext(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	run, err := hub.Start(1, 2, 0, "caused-run", RunKindChat)
	if err != nil {
		t.Fatal(err)
	}
	runContext, ok := hub.Context(run.RunID)
	if !ok {
		t.Fatal("run context missing")
	}
	if !hub.CancelWithCause(run.RunID, 1, 2, RunCancelServerDrain) {
		t.Fatal("cancel request failed")
	}
	<-runContext.Done()
	if cause := RunCancelCauseFromContext(runContext); cause != RunCancelServerDrain {
		t.Fatalf("context cause = %q", cause)
	}
}

func TestRunHubFirstOutputTimeoutStartsWhenDurableExecutionIsOwned(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	run, err := hub.StartWithFirstOutputTimeout(1, 2, 0, "first-output-timeout-run", RunKindChat, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	admissionContext, ok := hub.Context(run.RunID)
	if !ok {
		t.Fatal("run context missing")
	}
	time.Sleep(4 * 20 * time.Millisecond)
	select {
	case <-admissionContext.Done():
		t.Fatalf("first-output timeout started during admission: %v", context.Cause(admissionContext))
	default:
	}

	if _, err := hub.BeginExecution(run.RunID); !errors.Is(err, ErrRunNotDurable) {
		t.Fatalf("BeginExecution() before durable admission = %v, want ErrRunNotDurable", err)
	}
	if err := hub.PersistDurable(t.Context(), run.RunID, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("PersistDurable() error = %v", err)
	}
	runContext, err := hub.BeginExecution(run.RunID)
	if err != nil {
		t.Fatalf("BeginExecution() error = %v", err)
	}
	if runContext != admissionContext {
		t.Fatal("execution owner did not receive the admitted run context")
	}
	if _, err := hub.BeginExecution(run.RunID); !errors.Is(err, ErrRunExecutionOwned) {
		t.Fatalf("duplicate BeginExecution() = %v, want ErrRunExecutionOwned", err)
	}
	select {
	case <-runContext.Done():
	case <-time.After(time.Second):
		t.Fatal("armed first-output timeout did not cancel run context")
	}
	if cause := RunCancelCauseFromContext(runContext); cause != RunCancelFirstOutputTimeout {
		t.Fatalf("context cause = %q, want %q", cause, RunCancelFirstOutputTimeout)
	}
}

func TestRunHubFirstOutputTimeoutStopsAfterMeaningfulModelOutput(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	run, err := hub.StartWithFirstOutputTimeout(1, 2, 0, "first-output-run", RunKindChat, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	runContext, ok := hub.Context(run.RunID)
	if !ok {
		t.Fatal("run context missing")
	}
	if err := hub.PersistDurable(t.Context(), run.RunID, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("PersistDurable() error = %v", err)
	}
	runContext, err = hub.BeginExecution(run.RunID)
	if err != nil {
		t.Fatalf("BeginExecution() error = %v", err)
	}

	modelstream.ObserveMessage(runContext, &schema.Message{Role: schema.Assistant, Content: "started"})
	time.Sleep(4 * 20 * time.Millisecond)
	select {
	case <-runContext.Done():
		t.Fatalf("run canceled after output started: %v", context.Cause(runContext))
	default:
	}

	if !hub.CancelWithCause(run.RunID, 1, 2, RunCancelUserStop) {
		t.Fatal("user stop failed after first output")
	}
	select {
	case <-runContext.Done():
	case <-time.After(time.Second):
		t.Fatal("user stop did not cancel disarmed run")
	}
	if cause := RunCancelCauseFromContext(runContext); cause != RunCancelUserStop {
		t.Fatalf("context cause = %q, want %q", cause, RunCancelUserStop)
	}
}

func TestRunHubTransitionHonorsFirstOutputTimeout(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	run, err := hub.StartWithFirstOutputTimeout(1, 2, 0, "first-output-terminal", RunKindChat, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	runContext, ok := hub.Context(run.RunID)
	if !ok {
		t.Fatal("run context missing")
	}
	if err := hub.PersistDurable(t.Context(), run.RunID, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("PersistDurable() error = %v", err)
	}
	runContext, err = hub.BeginExecution(run.RunID)
	if err != nil {
		t.Fatalf("BeginExecution() error = %v", err)
	}
	<-runContext.Done()

	snapshot, transitioned, event, err := hub.Transition(context.Background(), run.RunID, RunTerminal{Status: RunStatusCompleted})
	if err != nil {
		t.Fatal(err)
	}
	if !transitioned || snapshot.Status != RunStatusCanceled || snapshot.CancelCause != string(RunCancelFirstOutputTimeout) {
		t.Fatalf("first-output terminal = snapshot:%+v transitioned:%v", snapshot, transitioned)
	}
	if event == nil || event.Event != "error" {
		t.Fatalf("first-output terminal event = %+v", event)
	}
}

func TestRunHubSerializesUserMessageBindingWithTerminalTransition(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	store := &blockingChatRunStore{
		bindStarted:       make(chan struct{}, 1),
		releaseBind:       make(chan struct{}),
		transitionStarted: make(chan struct{}, 1),
	}
	hub.SetStore(store)
	run, err := hub.Start(1, 2, 0, "serialized-bind", RunKindChat)
	if err != nil {
		t.Fatal(err)
	}
	if err := hub.PersistDurable(context.Background(), run.RunID, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("mark run durable: %v", err)
	}

	bindResult := make(chan bool, 1)
	go func() {
		bound, bindErr := hub.SetUserMessageIDContext(context.Background(), run.RunID, 77)
		bindResult <- bindErr == nil && bound
	}()
	select {
	case <-store.bindStarted:
	case <-time.After(time.Second):
		t.Fatal("durable bind did not start")
	}

	transitionDone := make(chan error, 1)
	go func() {
		_, _, _, transitionErr := hub.Transition(context.Background(), run.RunID, RunTerminal{Status: RunStatusCompleted})
		transitionDone <- transitionErr
	}()
	transitionStartedBeforeBind := false
	select {
	case <-store.transitionStarted:
		transitionStartedBeforeBind = true
	case <-time.After(50 * time.Millisecond):
	}
	close(store.releaseBind)

	if !<-bindResult {
		t.Fatal("user message binding did not complete")
	}
	if err := <-transitionDone; err != nil {
		t.Fatalf("terminal transition: %v", err)
	}
	if transitionStartedBeforeBind {
		t.Fatal("terminal transition reached durable store before user message binding completed")
	}
	snapshot, ok := hub.Get(run.RunID, 1, 2)
	if !ok || snapshot.Status != RunStatusCompleted || snapshot.UserMessageID != 77 {
		t.Fatalf("terminal snapshot = %+v", snapshot)
	}
}

func TestRunHubSerializesDurablePersistenceWithTerminalTransition(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	store := &blockingChatRunStore{transitionStarted: make(chan struct{}, 1)}
	hub.SetStore(store)
	run, err := hub.Start(1, 2, 0, "serialized-durable", RunKindChat)
	if err != nil {
		t.Fatal(err)
	}

	persistStarted := make(chan struct{})
	releasePersist := make(chan struct{})
	persistDone := make(chan error, 1)
	go func() {
		persistDone <- hub.PersistDurable(context.Background(), run.RunID, func(context.Context) error {
			close(persistStarted)
			<-releasePersist
			return nil
		})
	}()
	<-persistStarted

	transitionDone := make(chan error, 1)
	go func() {
		_, _, _, transitionErr := hub.Transition(context.Background(), run.RunID, RunTerminal{Status: RunStatusCompleted})
		transitionDone <- transitionErr
	}()
	select {
	case <-store.transitionStarted:
		t.Fatal("terminal transition reached the durable store before persistence completed")
	case <-time.After(50 * time.Millisecond):
	}
	close(releasePersist)
	if err := <-persistDone; err != nil {
		t.Fatalf("persist durable run: %v", err)
	}
	if err := <-transitionDone; err != nil {
		t.Fatalf("terminal transition: %v", err)
	}
	if snapshot, ok := hub.Get(run.RunID, 1, 2); !ok || snapshot.Status != RunStatusCompleted {
		t.Fatalf("terminal snapshot = %+v", snapshot)
	}
}

func TestRunHubSerializesAtomicAdmissionWithTerminalTransition(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	intent := BuildSendRunIntent(nil, &SendMessageRequest{Content: "hello"})
	run, err := hub.StartWithIntent(1, 2, 0, "atomic-admission", RunKindChat, intent)
	if err != nil {
		t.Fatal(err)
	}
	persistStarted := make(chan struct{})
	releasePersist := make(chan struct{})
	persistDone := make(chan error, 1)
	go func() {
		_, persistErr := hub.PersistAdmission(context.Background(), run.RunID, func(context.Context) (repository.ChatRunRecord, error) {
			close(persistStarted)
			<-releasePersist
			now := time.Now()
			return repository.ChatRunRecord{
				RunID: run.RunID, UserID: 2, SessionID: 1, Kind: RunKindChat,
				Operation: intent.Operation, IntentVersion: intent.Version, IntentHash: intent.Hash,
				Status: RunStatusRunning, UserMessageID: 77, AcceptedAt: now, ExpiresAt: now.Add(time.Minute),
			}, nil
		})
		persistDone <- persistErr
	}()
	<-persistStarted

	transitionDone := make(chan error, 1)
	go func() {
		_, _, _, transitionErr := hub.Transition(context.Background(), run.RunID, RunTerminal{Status: RunStatusCompleted})
		transitionDone <- transitionErr
	}()
	select {
	case err := <-transitionDone:
		t.Fatalf("terminal transition completed before admission: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(releasePersist)
	if err := <-persistDone; err != nil {
		t.Fatalf("persist admission: %v", err)
	}
	if err := <-transitionDone; err != nil {
		t.Fatalf("terminal transition: %v", err)
	}
	snapshot, ok := hub.Get(run.RunID, 1, 2)
	if !ok || snapshot.Status != RunStatusCompleted || snapshot.UserMessageID != 77 {
		t.Fatalf("terminal snapshot = %+v", snapshot)
	}
}

func TestRunHubForceDrainFinalizesPreviouslyCanceledRuns(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	run, err := hub.Start(1, 2, 0, "user-stopped-before-drain", RunKindChat)
	if err != nil {
		t.Fatal(err)
	}
	if !hub.Cancel(run.RunID, 1, 2) {
		t.Fatal("user cancellation was not requested")
	}
	hub.BeginDrain()
	if got := hub.CancelForDrain(); got != 0 {
		t.Fatalf("drain replaced an existing cancel cause: %d", got)
	}
	finalized, err := hub.FinalizeForDrain(context.Background())
	if err != nil || finalized != 1 {
		t.Fatalf("force drain finalized=%d err=%v", finalized, err)
	}
	snapshot, ok := hub.Get(run.RunID, 1, 2)
	if !ok || snapshot.Status != RunStatusCanceled || snapshot.CancelCause != string(RunCancelUserStop) {
		t.Fatalf("forced terminal snapshot = %+v", snapshot)
	}
	events, _, err := hub.EventsSince(run.RunID, 1, 2, 0)
	if err != nil || len(events) != 1 || events[0].Event != "message_complete" {
		t.Fatalf("forced terminal events = %+v err=%v", events, err)
	}
}

func TestRunHubSessionDeletionOverridesEarlierCancellation(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	run, err := hub.Start(1, 2, 0, "deleted-after-stop", RunKindChat)
	if err != nil {
		t.Fatal(err)
	}
	if !hub.Cancel(run.RunID, 1, 2) {
		t.Fatal("user cancellation was not requested")
	}
	if got := hub.CancelSession(1, 2); got != 1 {
		t.Fatalf("session deletion updated %d runs, want 1", got)
	}

	snapshot, transitioned, _, err := hub.Transition(context.Background(), run.RunID, RunTerminal{
		Status: RunStatusCanceled, CancelCause: RunCancelUserStop,
	})
	if err != nil || !transitioned {
		t.Fatalf("session deletion terminal = snapshot:%+v transitioned:%v err:%v", snapshot, transitioned, err)
	}
	if snapshot.CancelCause != string(RunCancelSessionDeleted) || snapshot.ErrorCode != "session_deleted" {
		t.Fatalf("session deletion terminal = %+v", snapshot)
	}
}

func TestRunHubAccountChangeOverridesEarlierUserStop(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	run, err := hub.Start(1, 2, 0, "account-change-after-stop", RunKindChat)
	if err != nil {
		t.Fatal(err)
	}
	if !hub.Cancel(run.RunID, 1, 2) {
		t.Fatal("user cancellation was not requested")
	}
	if got := hub.CancelByUser(2); got != 1 {
		t.Fatalf("account change updated %d runs, want 1", got)
	}

	snapshot, transitioned, _, err := hub.Transition(context.Background(), run.RunID, RunTerminal{
		Status: RunStatusCanceled, CancelCause: RunCancelUserStop,
	})
	if err != nil || !transitioned {
		t.Fatalf("account-change terminal = snapshot:%+v transitioned:%v err:%v", snapshot, transitioned, err)
	}
	if snapshot.CancelCause != string(RunCancelAccountChanged) || snapshot.ErrorCode != "account_changed" {
		t.Fatalf("account-change terminal = %+v", snapshot)
	}
}

func TestRunHubRecordDoesNotSerializeIndependentRuns(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	first, err := hub.Start(1, 1, 0, "event-lock-first", RunKindChat)
	if err != nil {
		t.Fatal(err)
	}
	second, err := hub.Start(2, 1, 0, "event-lock-second", RunKindChat)
	if err != nil {
		t.Fatal(err)
	}

	release := make(chan struct{})
	payload := &blockingJSONPayload{started: make(chan struct{}), release: release}
	firstDone := make(chan bool, 1)
	go func() {
		firstDone <- hub.Record(first.RunID, "diagnostic", payload)
	}()
	select {
	case <-payload.started:
	case <-time.After(time.Second):
		t.Fatal("first run did not enter event serialization")
	}

	secondDone := make(chan bool, 1)
	go func() {
		secondDone <- hub.Record(second.RunID, streaming.EventContentDelta, streaming.ContentDeltaEvent{Delta: "ready"})
	}()
	select {
	case recorded := <-secondDone:
		if !recorded {
			t.Fatal("second run rejected an independent event")
		}
	case <-time.After(time.Second):
		t.Fatal("one run's event serialization blocked another run")
	}

	close(release)
	select {
	case recorded := <-firstDone:
		if !recorded {
			t.Fatal("first run rejected its event")
		}
	case <-time.After(time.Second):
		t.Fatal("first run did not finish after serialization resumed")
	}
}

func TestRunHubForceDrainDoesNotSerializeIndependentTransitions(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	store := &selectiveBlockingChatRunStore{blockedRunID: "blocked-drain"}
	hub.SetStore(store)
	blocked, err := hub.Start(1, 2, 0, store.blockedRunID, RunKindChat)
	if err != nil {
		t.Fatal(err)
	}
	fast, err := hub.Start(2, 2, 0, "fast-drain", RunKindChat)
	if err != nil {
		t.Fatal(err)
	}
	for _, runID := range []string{blocked.RunID, fast.RunID} {
		if err := hub.PersistDurable(context.Background(), runID, func(context.Context) error { return nil }); err != nil {
			t.Fatalf("persist durable run %s: %v", runID, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	finalized, err := hub.FinalizeForDrain(ctx)
	if err == nil || finalized != 1 {
		t.Fatalf("force drain finalized=%d err=%v", finalized, err)
	}
	if snapshot, ok := hub.Get(fast.RunID, 2, 2); !ok || snapshot.Status != RunStatusCanceled {
		t.Fatalf("fast run snapshot = %+v", snapshot)
	}
}

func TestRunHubForceDrainReturnsWhenTerminalLockIsBusy(t *testing.T) {
	hub := NewRunHub(time.Minute, 1<<20)
	run, err := hub.Start(1, 2, 0, "locked-drain", RunKindChat)
	if err != nil {
		t.Fatal(err)
	}

	hub.mu.RLock()
	state := hub.runs[run.RunID]
	hub.mu.RUnlock()
	state.transitionMu.Lock()

	type result struct {
		finalized int
		err       error
	}
	done := make(chan result, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	go func() {
		finalized, finalizeErr := hub.FinalizeForDrain(ctx)
		done <- result{finalized: finalized, err: finalizeErr}
	}()

	select {
	case got := <-done:
		state.transitionMu.Unlock()
		if got.finalized != 0 || !errors.Is(got.err, context.DeadlineExceeded) {
			t.Fatalf("force drain result = %+v", got)
		}
	case <-time.After(200 * time.Millisecond):
		state.transitionMu.Unlock()
		<-done
		t.Fatal("force drain ignored its context while waiting for a terminal lock")
	}
}
