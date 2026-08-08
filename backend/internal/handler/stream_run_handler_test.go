package handler

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/agent"
	"github.com/huoguojun123/EffChat/internal/middleware"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/modelstream"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
	modelusage "github.com/huoguojun123/EffChat/internal/usage"
	"github.com/huoguojun123/EffChat/pkg/streaming"
)

type failingHandlerChatRunStore struct {
	err error
}

func (s *failingHandlerChatRunStore) BindChatRunUserMessage(context.Context, string, int64) (bool, error) {
	return false, s.err
}

func (s *failingHandlerChatRunStore) TransitionChatRun(context.Context, repository.ChatRunTransitionInput) (repository.ChatRunRecord, bool, error) {
	return repository.ChatRunRecord{}, false, s.err
}

func TestSlowTCPConsumerDoesNotBlockRunCompletion(t *testing.T) {
	if os.Getenv("EFFCHAT_LONG_STREAM_TEST") != "1" {
		t.Skip("set EFFCHAT_LONG_STREAM_TEST=1 to run the 60-second TCP acceptance test")
	}
	pause := 60 * time.Second
	if raw := os.Getenv("EFFCHAT_SLOW_CONSUMER_DURATION"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			t.Fatalf("invalid EFFCHAT_SLOW_CONSUMER_DURATION %q", raw)
		}
		pause = parsed
	}

	gin.SetMode(gin.TestMode)
	runHub := service.NewRunHub(2*time.Minute, 1<<20)
	run, err := runHub.Start(1, 2, 0, "slow-tcp-consumer", service.RunKindChat)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	ready := make(chan struct{})
	router := gin.New()
	router.GET("/events", func(c *gin.Context) {
		events, ch, cleanup, _, subscribeErr := runHub.EventsAfter(run.RunID, 1, 2, 0)
		if subscribeErr != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		defer cleanup()
		writer, writerErr := streaming.NewSSEWriter(c)
		if writerErr != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		close(ready)
		forwardRunEvents(c, writer, runHub, 0, 1, 2, run.RunID, events, ch, 0)
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	address := strings.TrimPrefix(server.URL, "http://")
	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatalf("dial SSE server: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := fmt.Fprintf(conn, "GET /events HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", address); err != nil {
		t.Fatalf("send SSE request: %v", err)
	}
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("SSE subscriber was not ready")
	}
	if !runHub.Record(run.RunID, streaming.EventMessageStart, map[string]interface{}{"run_id": run.RunID}) {
		t.Fatal("record initial event")
	}
	reader := bufio.NewReader(conn)
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatalf("read SSE response headers: %v", readErr)
		}
		if line == "\r\n" {
			break
		}
	}

	producerDone := make(chan struct{})
	go func() {
		defer close(producerDone)
		payload := strings.Repeat("x", 8*1024)
		for i := 0; i < 2000; i++ {
			runHub.Record(run.RunID, streaming.EventContentDelta, map[string]interface{}{"content": payload})
		}
		runHub.Complete(run.RunID, nil, nil)
	}()
	select {
	case <-producerDone:
	case <-time.After(10 * time.Second):
		t.Fatal("run producer was blocked by the unread TCP consumer")
	}

	time.Sleep(pause)
	snapshot, ok := runHub.Get(run.RunID, 1, 2)
	if !ok || snapshot.Status != service.RunStatusCompleted || snapshot.Cursor < 2001 {
		t.Fatalf("run snapshot after slow consumer = %#v", snapshot)
	}
}

type snapshotNotifyRecorder struct {
	*httptest.ResponseRecorder
	snapshotWritten chan struct{}
	once            sync.Once
}

func (r *snapshotNotifyRecorder) Write(data []byte) (int, error) {
	n, err := r.ResponseRecorder.Write(data)
	if strings.Contains(string(data), "event: run_snapshot") {
		r.once.Do(func() { close(r.snapshotWritten) })
	}
	return n, err
}

func createResumeTestSession(t *testing.T, env *testEnv) model.Session {
	t.Helper()
	created := env.doRequest(http.MethodPost, "/api/v1/sessions", map[string]interface{}{
		"model_id": "gpt-4o-mini",
		"provider": env.channelKey,
		"title":    "Resume run",
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create session: status=%d body=%s", created.Code, created.Body.String())
	}
	var session model.Session
	if err := json.Unmarshal(created.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	return session
}

func newResumeRunRouter(env *testEnv, runHub *service.RunHub) *gin.Engine {
	router := gin.New()
	auth := router.Group("/api/v1")
	auth.Use(middleware.AuthMiddleware(env.authService))
	auth.GET("/sessions/:id/runs/:run_id/resume", ResumeRunHandler(runHub, env.sessionService, 0))
	return router
}

func TestChatRunStatusPayloadExposesOnlyPublicTerminalFacts(t *testing.T) {
	payload := chatRunStatusPayload(repository.ChatRunRecord{
		RunID:              "run-1",
		SessionID:          7,
		Kind:               service.RunKindChat,
		Status:             service.RunStatusFailed,
		UserMessageID:      42,
		TerminalMessageID:  0,
		PublicErrorCode:    "message_persist_failed",
		PublicErrorMessage: "回复已生成但保存失败，请重试最后一条消息",
		IntentHash:         "secret-intent-hash",
		TerminalEvent:      json.RawMessage(`{"event":"error","data":{"internal":"hidden"}}`),
	})
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	body := string(encoded)
	if !strings.Contains(body, `"error_code":"message_persist_failed"`) || !strings.Contains(body, `"user_message_id":42`) {
		t.Fatalf("public terminal payload = %s", body)
	}
	if strings.Contains(body, "secret-intent-hash") || strings.Contains(body, "internal") {
		t.Fatalf("status payload leaked private run data: %s", body)
	}
}

func TestReplayGapPayloadOnlyReportsMissingOutput(t *testing.T) {
	payload, ok := replayGapPayload(&service.RunSnapshot{
		ReplayFrom:      12,
		OutputTruncated: true,
	}, 4)
	if !ok {
		t.Fatal("missing output should produce a replay gap")
	}
	if payload["requested_cursor"] != int64(4) || payload["replay_from"] != int64(12) {
		t.Fatalf("replay gap payload = %#v", payload)
	}

	if _, ok := replayGapPayload(&service.RunSnapshot{ReplayFrom: 12, OutputTruncated: true}, 12); ok {
		t.Fatal("a cursor at the retained boundary must not report a gap")
	}
	if _, ok := replayGapPayload(&service.RunSnapshot{ReplayFrom: 12}, 4); ok {
		t.Fatal("trimmed non-output events must not report an output gap")
	}
}

func TestResumeRunHandlerReplaysNormalEventsWithoutSnapshot(t *testing.T) {
	env := setupTestEnv(t)
	session := createResumeTestSession(t, env)
	runHub := service.NewRunHub(time.Minute, 1<<20)
	run, err := runHub.Start(session.ID, env.userID, 0, "normal-resume", service.RunKindChat)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if !runHub.Record(run.RunID, streaming.EventContentDelta, streaming.ContentDeltaEvent{Delta: "first"}) ||
		!runHub.Record(run.RunID, streaming.EventContentDelta, streaming.ContentDeltaEvent{Delta: "second"}) {
		t.Fatal("record replay events")
	}
	runHub.Complete(run.RunID, nil, nil)

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%d/runs/%s/resume?cursor=0", session.ID, run.RunID), nil)
	req.Header.Set("Authorization", "Bearer "+env.token)
	newResumeRunRouter(env, runHub).ServeHTTP(recorder, req)

	body := recorder.Body.String()
	first := strings.Index(body, `"delta":"first"`)
	second := strings.Index(body, `"delta":"second"`)
	if recorder.Code != http.StatusOK || first < 0 || second < 0 || first >= second {
		t.Fatalf("normal resume status=%d body=%q", recorder.Code, body)
	}
	if strings.Contains(body, "event: run_snapshot") || strings.Contains(body, "event: replay_gap") {
		t.Fatalf("normal resume must only replay events: %q", body)
	}
}

func TestResumeRunHandlerEmitsGapSnapshotBeforeLiveEvents(t *testing.T) {
	env := setupTestEnv(t)
	session := createResumeTestSession(t, env)
	runHub := service.NewRunHub(time.Minute, 1024)
	run, err := runHub.Start(session.ID, env.userID, 0, "gap-resume", service.RunKindChat)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	for i := 0; i < 3; i++ {
		if !runHub.Record(run.RunID, streaming.EventContentDelta, streaming.ContentDeltaEvent{
			Delta: fmt.Sprintf("old-%d-%s", i, strings.Repeat("x", 1024)),
		}) {
			t.Fatal("record output to trim")
		}
	}
	snapshot, ok := runHub.Get(run.RunID, session.ID, env.userID)
	if !ok || !snapshot.OutputTruncated || snapshot.ReplayFrom == 0 {
		t.Fatalf("expected truncated replay snapshot, got %+v", snapshot)
	}

	recorder := &snapshotNotifyRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		snapshotWritten:  make(chan struct{}),
	}
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%d/runs/%s/resume?cursor=0", session.ID, run.RunID), nil)
	req.Header.Set("Authorization", "Bearer "+env.token)
	done := make(chan struct{})
	go func() {
		newResumeRunRouter(env, runHub).ServeHTTP(recorder, req)
		close(done)
	}()

	select {
	case <-recorder.snapshotWritten:
	case <-time.After(time.Second):
		t.Fatal("resume handler did not emit snapshot after replay gap")
	}
	if !runHub.Record(run.RunID, streaming.EventContentDelta, streaming.ContentDeltaEvent{Delta: "after-snapshot"}) {
		t.Fatal("record live output")
	}
	runHub.Complete(run.RunID, nil, nil)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("resume handler did not finish after the run completed")
	}

	body := recorder.Body.String()
	gap := strings.Index(body, "event: replay_gap")
	gapSnapshot := strings.Index(body, "event: run_snapshot")
	live := strings.Index(body, `"delta":"after-snapshot"`)
	if recorder.Code != http.StatusOK || gap < 0 || gapSnapshot < gap || live < gapSnapshot {
		t.Fatalf("gap resume event order status=%d body=%q", recorder.Code, body)
	}
	if strings.Count(body, "event: content_delta") != 1 {
		t.Fatalf("events at or before the snapshot cursor were replayed: %q", body)
	}
}

func TestAgentErrorPayload_RuntimeError(t *testing.T) {
	err := &agent.RuntimeError{
		Code:         "model_empty_response",
		Message:      "模型这次没有返回可展示内容",
		Diagnostic:   "HTTP 503 · 上游服务异常 · 请求 ID req-runtime",
		Category:     agent.RuntimeErrorTransient,
		Retryable:    true,
		Provider:     "claude",
		ModelID:      "claude-sonnet-4",
		FinishReason: "stop",
		Usage:        &agent.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12},
	}

	payload := agentErrorPayload(err, "req-runtime")
	if payload["error"] != err.Message {
		t.Fatalf("error = %v, want %q", payload["error"], err.Message)
	}
	if payload["code"] != "model_empty_response" {
		t.Fatalf("code = %v, want model_empty_response", payload["code"])
	}
	if payload["category"] != string(agent.RuntimeErrorTransient) || payload["retryable"] != true {
		t.Fatalf("recovery metadata mismatch: %#v", payload)
	}
	if payload["diagnostic"] != err.Diagnostic {
		t.Fatalf("diagnostic = %v, want %q", payload["diagnostic"], err.Diagnostic)
	}
	if _, exists := payload["provider"]; exists {
		t.Fatalf("provider must stay private: %#v", payload)
	}
	if _, exists := payload["model_id"]; exists {
		t.Fatalf("model ID must stay private: %#v", payload)
	}
	if payload["finish_reason"] != "stop" {
		t.Fatalf("finish_reason = %v, want stop", payload["finish_reason"])
	}
	if payload["usage"] == nil {
		t.Fatal("usage should be preserved")
	}
	if payload["request_id"] != "req-runtime" {
		t.Fatalf("request_id = %v", payload["request_id"])
	}
}

func TestEffectiveFirstOutputTimeoutUsesConfiguredValueOrExistingDefault(t *testing.T) {
	if got := effectiveFirstOutputTimeout(20*time.Second, defaultChatFirstOutputTimeout); got != 20*time.Second {
		t.Fatalf("configured timeout = %s, want 20s", got)
	}
	if got := effectiveFirstOutputTimeout(0, defaultCompactionFirstOutputTimeout); got != defaultCompactionFirstOutputTimeout {
		t.Fatalf("default timeout = %s, want %s", got, defaultCompactionFirstOutputTimeout)
	}
}

func TestEffectiveRunSetupTimeoutPrecedesFirstOutputGuard(t *testing.T) {
	tests := []struct {
		name        string
		firstOutput time.Duration
		want        time.Duration
	}{
		{name: "non-positive falls back to setup cap", firstOutput: 0, want: maxRunSetupTimeout},
		{name: "negative falls back to setup cap", firstOutput: -time.Second, want: maxRunSetupTimeout},
		{name: "one nanosecond becomes immediate", firstOutput: time.Nanosecond, want: 0},
		{name: "tiny positive is halved", firstOutput: 2 * time.Nanosecond, want: time.Nanosecond},
		{name: "short configured guard is halved", firstOutput: 20 * time.Second, want: 10 * time.Second},
		{name: "compaction default uses setup cap", firstOutput: defaultCompactionFirstOutputTimeout, want: maxRunSetupTimeout},
		{name: "chat default uses setup cap", firstOutput: defaultChatFirstOutputTimeout, want: maxRunSetupTimeout},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := effectiveRunSetupTimeout(test.firstOutput)
			if got != test.want {
				t.Fatalf("effectiveRunSetupTimeout(%s) = %s, want %s", test.firstOutput, got, test.want)
			}
			if test.firstOutput > 0 && got >= test.firstOutput {
				t.Fatalf("setup timeout %s must precede first-output guard %s", got, test.firstOutput)
			}
		})
	}
}

func TestRunSetupDeadlineTransitionsChatAndCompactionAsSetupTimeout(t *testing.T) {
	for index, kind := range []string{service.RunKindChat, service.RunKindCompaction} {
		t.Run(kind, func(t *testing.T) {
			runHub := service.NewRunHub(time.Minute, 1<<20)
			run, err := runHub.StartWithFirstOutputTimeout(int64(index+1), 9, 0, "setup-timeout-"+kind, kind, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if err := runHub.PersistDurable(t.Context(), run.RunID, func(context.Context) error { return nil }); err != nil {
				t.Fatal(err)
			}
			runContext, err := runHub.BeginExecution(run.RunID)
			if err != nil {
				t.Fatal(err)
			}
			setupContext, setupCancel := newRunSetupContext(runContext, 5*time.Millisecond)
			defer setupCancel()
			select {
			case <-setupContext.Done():
			case <-time.After(time.Second):
				t.Fatal("setup context did not reach its deadline")
			}

			if !transitionRunSetupInterruption(nil, nil, runHub, run.RunID, "setup-request", runContext, setupContext) {
				t.Fatal("setup deadline was not classified")
			}
			snapshot, ok := runHub.Get(run.RunID, int64(index+1), 9)
			if !ok {
				t.Fatal("setup-timeout run disappeared")
			}
			if snapshot.Status != service.RunStatusFailed || snapshot.ErrorCode != "run_setup_timeout" {
				t.Fatalf("setup terminal = status:%q code:%q error:%q", snapshot.Status, snapshot.ErrorCode, snapshot.Error)
			}
			if snapshot.CancelCause != "" || snapshot.ErrorCode == "first_output_timeout" {
				t.Fatalf("setup timeout was misclassified as cancellation: %+v", snapshot)
			}
			events, _, cleanup, _, err := runHub.EventsAfter(run.RunID, int64(index+1), 9, 0)
			if err != nil {
				t.Fatal(err)
			}
			if cleanup != nil {
				cleanup()
			}
			if len(events) != 1 || events[0].Event != streaming.EventError {
				t.Fatalf("setup timeout events = %+v", events)
			}
			payload, ok := events[0].Data.(gin.H)
			if !ok || payload["code"] != "run_setup_timeout" || payload["retryable"] != true || payload["request_id"] != "setup-request" {
				t.Fatalf("setup timeout payload = %#v", events[0].Data)
			}
		})
	}
}

func TestRunTerminalFallbackResponsesKeepRequestCorrelation(t *testing.T) {
	tests := []struct {
		name       string
		transition func(*gin.Context, *service.RunHub, *service.RunSnapshot, context.Context) bool
	}{
		{
			name: "cancellation",
			transition: func(c *gin.Context, runHub *service.RunHub, run *service.RunSnapshot, runContext context.Context) bool {
				runHub.CancelWithCause(run.RunID, run.SessionID, run.UserID, service.RunCancelUserStop)
				return transitionCanceledRun(c, nil, runHub, run.RunID, runContext)
			},
		},
		{
			name: "setup timeout",
			transition: func(c *gin.Context, runHub *service.RunHub, run *service.RunSnapshot, runContext context.Context) bool {
				setupContext, setupCancel := context.WithCancelCause(runContext)
				setupCancel(context.DeadlineExceeded)
				return transitionRunSetupInterruption(c, nil, runHub, run.RunID, "req-terminal", runContext, setupContext)
			},
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runHub := service.NewRunHub(time.Minute, 1<<20)
			run, err := runHub.Start(int64(index+1), 9, 0, "terminal-fallback-"+strings.ReplaceAll(test.name, " ", "-"), service.RunKindChat)
			if err != nil {
				t.Fatal(err)
			}
			if err := runHub.PersistDurable(t.Context(), run.RunID, func(context.Context) error { return nil }); err != nil {
				t.Fatal(err)
			}
			runHub.SetStore(&failingHandlerChatRunStore{err: errors.New("postgres://fixture:secret@db.example/effchat /srv/private/terminal")})
			runContext, err := runHub.BeginExecution(run.RunID)
			if err != nil {
				t.Fatal(err)
			}
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/sessions/1/messages/stream", nil)
			ctx.Set("request_id", "req-terminal")

			if !test.transition(ctx, runHub, run, runContext) {
				t.Fatal("terminal failure was not classified")
			}

			assertRunFallbackError(t, recorder, "run_terminal_failed", "req-terminal")
		})
	}
}

func TestAcceptedRunStreamUnavailableKeepsRecoveryMetadata(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/sessions/1/messages/stream", nil)
	ctx.Set("request_id", "req-stream")

	writeAcceptedRunStreamUnavailable(ctx, "run-fixture", errors.New("transport secret /srv/private/stream"))

	assertRunFallbackError(t, recorder, "stream_unavailable", "req-stream")
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["run_id"] != "run-fixture" {
		t.Fatalf("run_id = %#v", body["run_id"])
	}
}

func TestRunConflictFallbacksKeepRecoveryMetadata(t *testing.T) {
	tests := []struct {
		name  string
		code  string
		runID string
		write func(*gin.Context)
	}{
		{name: "terminal", code: "run_terminal", write: writeRunTerminalConflict},
		{name: "execution owned", code: "run_execution_owned", runID: "run-owned", write: func(c *gin.Context) { writeRunExecutionOwned(c, "run-owned") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/sessions/1/messages/stream", nil)
			ctx.Set("request_id", "req-conflict")

			test.write(ctx)

			if recorder.Code != http.StatusConflict {
				t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body["code"] != test.code || body["retryable"] != false || body["request_id"] != "req-conflict" {
				t.Fatalf("response = %#v", body)
			}
			if test.runID != "" && body["run_id"] != test.runID {
				t.Fatalf("run_id = %#v, want %q", body["run_id"], test.runID)
			}
		})
	}
}

func assertRunFallbackError(t *testing.T, recorder *httptest.ResponseRecorder, code, requestID string) {
	t.Helper()
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["code"] != code || body["retryable"] != true || body["request_id"] != requestID {
		t.Fatalf("response = %#v", body)
	}
	if strings.Contains(recorder.Body.String(), "secret") || strings.Contains(recorder.Body.String(), "/srv/private") {
		t.Fatalf("response leaked internal cause: %s", recorder.Body.String())
	}
}

func TestRunSetupCancelLeavesRunContextAvailableForModelStream(t *testing.T) {
	const firstOutputTimeout = 40 * time.Millisecond
	runHub := service.NewRunHub(time.Minute, 1<<20)
	run, err := runHub.StartWithFirstOutputTimeout(7, 9, 0, "setup-child-cancel", service.RunKindChat, firstOutputTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if err := runHub.PersistDurable(t.Context(), run.RunID, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	runContext, err := runHub.BeginExecution(run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	setupContext, setupCancel := newRunSetupContext(runContext, effectiveRunSetupTimeout(firstOutputTimeout))
	setupCancel()
	if !errors.Is(context.Cause(setupContext), context.Canceled) {
		t.Fatalf("setup cause = %v, want context.Canceled", context.Cause(setupContext))
	}
	if cause := context.Cause(runContext); cause != nil {
		t.Fatalf("canceling setup child canceled durable parent: %v", cause)
	}

	// Simulate the raw model observer seeing its first meaningful chunk. The
	// original runContext must still carry and disarm the RunHub output gate.
	modelstream.MarkOutput(runContext)
	select {
	case <-runContext.Done():
		t.Fatalf("durable parent canceled after setup completed: %v", context.Cause(runContext))
	case <-time.After(3 * firstOutputTimeout):
	}
	runHub.Complete(run.RunID, nil, nil)
}

func TestFinishRunSetupDoesNotReclassifySuccessfulSetupAsTimeout(t *testing.T) {
	runHub := service.NewRunHub(time.Minute, 1<<20)
	run, err := runHub.StartWithFirstOutputTimeout(7, 9, 0, "setup-success-boundary", service.RunKindChat, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := runHub.PersistDurable(t.Context(), run.RunID, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	runContext, err := runHub.BeginExecution(run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	setupContext, setupCancel := newRunSetupContext(runContext, time.Millisecond)
	select {
	case <-setupContext.Done():
	case <-time.After(time.Second):
		t.Fatal("setup context did not reach its deadline")
	}
	if !errors.Is(context.Cause(setupContext), context.DeadlineExceeded) {
		t.Fatalf("setup cause = %v, want context.DeadlineExceeded", context.Cause(setupContext))
	}

	// The final setup operation is modeled as having returned success. That
	// success owns the child-deadline race; only the durable parent is checked.
	if finishRunSetup(nil, nil, runHub, run.RunID, runContext, setupCancel) {
		t.Fatal("successful setup was reclassified as an interruption")
	}
	snapshot, ok := runHub.Get(run.RunID, 7, 9)
	if !ok || snapshot.Status != service.RunStatusRunning || snapshot.ErrorCode != "" {
		t.Fatalf("successful setup terminal = %+v", snapshot)
	}
	modelstream.MarkOutput(runContext)
	runHub.Complete(run.RunID, nil, nil)
}

func TestFinishRunSetupPreservesParentCancellationCause(t *testing.T) {
	runHub := service.NewRunHub(time.Minute, 1<<20)
	run, err := runHub.StartWithFirstOutputTimeout(7, 9, 0, "setup-success-parent-cancel", service.RunKindChat, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := runHub.PersistDurable(t.Context(), run.RunID, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	runContext, err := runHub.BeginExecution(run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	_, setupCancel := newRunSetupContext(runContext, 500*time.Millisecond)
	if !runHub.CancelWithCause(run.RunID, 7, 9, service.RunCancelServerDrain) {
		t.Fatal("failed to cancel durable parent")
	}
	if !finishRunSetup(nil, nil, runHub, run.RunID, runContext, setupCancel) {
		t.Fatal("successful setup boundary ignored durable parent cancellation")
	}
	snapshot, ok := runHub.Get(run.RunID, 7, 9)
	if !ok || snapshot.Status != service.RunStatusCanceled || snapshot.CancelCause != string(service.RunCancelServerDrain) || snapshot.ErrorCode != "server_draining" {
		t.Fatalf("successful setup parent terminal = %+v", snapshot)
	}
}

func TestRunSetupInterruptionPreservesParentCancellationCause(t *testing.T) {
	tests := []struct {
		name      string
		cause     service.RunCancelCause
		wantCode  string
		wantEvent string
	}{
		{name: "user stop", cause: service.RunCancelUserStop, wantEvent: streaming.EventMessageComplete},
		{name: "first output", cause: service.RunCancelFirstOutputTimeout, wantCode: "first_output_timeout", wantEvent: streaming.EventError},
		{name: "server drain", cause: service.RunCancelServerDrain, wantCode: "server_draining", wantEvent: streaming.EventError},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runHub := service.NewRunHub(time.Minute, 1<<20)
			run, err := runHub.StartWithFirstOutputTimeout(int64(index+1), 9, 0, "setup-parent-"+strings.ReplaceAll(test.name, " ", "-"), service.RunKindChat, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if err := runHub.PersistDurable(t.Context(), run.RunID, func(context.Context) error { return nil }); err != nil {
				t.Fatal(err)
			}
			runContext, err := runHub.BeginExecution(run.RunID)
			if err != nil {
				t.Fatal(err)
			}
			setupContext, setupCancel := newRunSetupContext(runContext, 500*time.Millisecond)
			defer setupCancel()
			if !runHub.CancelWithCause(run.RunID, int64(index+1), 9, test.cause) {
				t.Fatal("failed to cancel parent run")
			}

			if !transitionRunSetupInterruption(nil, nil, runHub, run.RunID, "", runContext, setupContext) {
				t.Fatal("parent cancellation was not classified")
			}
			snapshot, ok := runHub.Get(run.RunID, int64(index+1), 9)
			if !ok || snapshot.Status != service.RunStatusCanceled || snapshot.CancelCause != string(test.cause) {
				t.Fatalf("parent cancellation terminal = %+v", snapshot)
			}
			if snapshot.ErrorCode != test.wantCode || snapshot.ErrorCode == "run_setup_timeout" {
				t.Fatalf("parent cancellation code = %q, want %q", snapshot.ErrorCode, test.wantCode)
			}
			events, _, cleanup, _, err := runHub.EventsAfter(run.RunID, int64(index+1), 9, 0)
			if err != nil {
				t.Fatal(err)
			}
			if cleanup != nil {
				cleanup()
			}
			if len(events) != 1 || events[0].Event != test.wantEvent {
				t.Fatalf("parent cancellation events = %+v, want %q", events, test.wantEvent)
			}
		})
	}
}

func TestLoadRunConversationMessagesUsesOriginalRetryTarget(t *testing.T) {
	env := setupTestEnv(t)
	session := createResumeTestSession(t, env)
	userMessage, err := env.messageService.CreateUserMessage(session.ID, env.userID, &service.SendMessageRequest{
		Content:       "retry this turn",
		SchemaVersion: "v1",
	})
	if err != nil {
		t.Fatalf("create user message: %v", err)
	}
	assistantMessage, err := env.messageService.CreateAssistantMessage(session.ID, env.userID, map[string]interface{}{
		"role": "assistant", "content": "replace this answer",
	}, "v1")
	if err != nil {
		t.Fatalf("create assistant message: %v", err)
	}

	messages, err := loadRunConversationMessages(
		context.Background(),
		env.messageService,
		session.ID,
		env.userID,
		&service.RunSnapshot{RetryTargetMessageID: assistantMessage.ID},
		modelusage.KindRetry,
	)
	if err != nil {
		t.Fatalf("load retry conversation: %v", err)
	}
	if len(messages) != 1 || messages[0].ID != userMessage.ID {
		t.Fatalf("retry context = %+v, want only user message %d", messages, userMessage.ID)
	}
}

func TestLoadPostRunMemorySessionUsesDetachedContext(t *testing.T) {
	env := setupTestEnv(t)
	session := createResumeTestSession(t, env)
	runHub := service.NewRunHub(time.Minute, 1<<20)
	run, err := runHub.StartWithFirstOutputTimeout(session.ID, env.userID, 0, "post-run-memory", service.RunKindChat, time.Minute)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	runContext, ok := runHub.Context(run.RunID)
	if !ok {
		t.Fatal("missing run context")
	}
	runHub.Complete(run.RunID, nil, nil)
	if runContext.Err() == nil {
		t.Fatal("completed run context must be canceled")
	}

	loaded, err := loadPostRunMemorySession(env.sessionService, session.ID, env.userID)
	if err != nil {
		t.Fatalf("load session after run completion: %v", err)
	}
	if loaded.ID != session.ID {
		t.Fatalf("loaded session = %d, want %d", loaded.ID, session.ID)
	}
}

func TestRunCompactionTasksKeepsCompactionAfterMemoryFailure(t *testing.T) {
	compactionCanceled := make(chan struct{}, 1)
	checkpoint := &agent.CompressionCheckpoint{SummaryData: []byte("summary")}
	got, err := runCompactionTasks(context.Background(), func(context.Context) error {
		return errors.New("memory failed")
	}, func(ctx context.Context) (*agent.CompressionCheckpoint, error) {
		select {
		case <-ctx.Done():
			compactionCanceled <- struct{}{}
			return nil, ctx.Err()
		default:
			return checkpoint, nil
		}
	})
	if err != nil || got != checkpoint {
		t.Fatalf("checkpoint=%#v err=%v, want memory failure to be non-fatal", got, err)
	}
	select {
	case <-compactionCanceled:
		t.Fatal("compaction was canceled after memory failure")
	default:
	}
}

func TestRunCompactionTasksCancelsMemoryAfterCompactionFailure(t *testing.T) {
	memoryCanceled := make(chan struct{}, 1)
	_, err := runCompactionTasks(context.Background(), func(ctx context.Context) error {
		<-ctx.Done()
		memoryCanceled <- struct{}{}
		return ctx.Err()
	}, func(context.Context) (*agent.CompressionCheckpoint, error) {
		return nil, errors.New("compaction failed")
	})
	if err == nil || err.Error() != "compaction failed" {
		t.Fatalf("error = %v, want compaction failure", err)
	}
	select {
	case <-memoryCanceled:
	case <-time.After(time.Second):
		t.Fatal("memory maintenance was not canceled after compaction failure")
	}
}

func TestRunCompactionTasksMemoryOutputDoesNotDisarmCompressionGuard(t *testing.T) {
	timeoutCause := errors.New("compression first output timeout")
	parent, parentCancel := context.WithTimeout(t.Context(), time.Second)
	defer parentCancel()
	runCtx, cancelRun, stopGuard := modelstream.WithDeferredFirstOutputTimeout(parent, 20*time.Millisecond, timeoutCause)
	defer func() {
		stopGuard()
		cancelRun(nil)
	}()
	modelstream.ArmFirstOutputTimeout(runCtx)

	_, err := runCompactionTasks(runCtx, func(ctx context.Context) error {
		modelstream.MarkOutput(ctx)
		return nil
	}, func(ctx context.Context) (*agent.CompressionCheckpoint, error) {
		<-ctx.Done()
		return nil, context.Cause(ctx)
	})
	if !errors.Is(err, timeoutCause) {
		t.Fatalf("error = %v, want compression-owned first output timeout", err)
	}
}

func TestShouldRetryMemoryMaintenanceBeforeCompaction(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "success", err: nil, want: false},
		{name: "answer selection changed", err: repository.ErrAnswerSelectionRevisionConflict, want: false},
		{name: "declared output capacity", err: fmt.Errorf("wrapped: %w", agent.ErrMemoryMaintenanceOutputBudgetInsufficient), want: false},
		{name: "provider output limit", err: fmt.Errorf("wrapped: %w", agent.ErrMemoryMaintenanceOutputLimit), want: true},
		{name: "transient model failure", err: errors.New("temporary model failure"), want: true},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if got := shouldRetryMemoryMaintenanceBeforeCompaction(testCase.err); got != testCase.want {
				t.Fatalf("shouldRetryMemoryMaintenanceBeforeCompaction(%v) = %t, want %t", testCase.err, got, testCase.want)
			}
		})
	}
}

func TestAgentErrorPayload_GenericError(t *testing.T) {
	payload := agentErrorPayload(errors.New("sql: password=secret plain failure"), "req-generic")
	if payload["error"] != "模型请求失败，请稍后重试" {
		t.Fatalf("error = %v", payload["error"])
	}
	if payload["code"] != "agent_run_failed" || payload["request_id"] != "req-generic" {
		t.Fatalf("generic payload = %#v", payload)
	}
	if strings.Contains(payload["error"].(string), "secret") {
		t.Fatalf("generic error leaked internal detail: %#v", payload)
	}
}

func TestResolveSessionSearchMode(t *testing.T) {
	cases := []struct {
		name string
		mode string
		want modelbank.SearchMode
	}{
		{name: "empty defaults to auto", mode: "", want: modelbank.SearchModeAuto},
		{name: "invalid defaults to auto", mode: "native", want: modelbank.SearchModeAuto},
		{name: "off", mode: "off", want: modelbank.SearchModeOff},
		{name: "on", mode: "on", want: modelbank.SearchModeOn},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveSessionSearchMode(tc.mode); got != tc.want {
				t.Fatalf("resolveSessionSearchMode(%q) = %q, want %q", tc.mode, got, tc.want)
			}
		})
	}
}

func TestThinkingEffortFromMessage(t *testing.T) {
	msg := &model.Message{MessageData: []byte(`{"metadata":{"thinking_effort":"HIGH"}}`)}
	if got := thinkingEffortFromMessage(msg); got != "high" {
		t.Fatalf("thinkingEffortFromMessage = %q, want high", got)
	}

	invalid := &model.Message{MessageData: []byte(`{"metadata":{"thinking_effort":"turbo"}}`)}
	if got := thinkingEffortFromMessage(invalid); got != "" {
		t.Fatalf("invalid thinking effort = %q, want empty", got)
	}

	if got := thinkingEffortFromMessage(&model.Message{MessageData: []byte(`not json`)}); got != "" {
		t.Fatalf("invalid json thinking effort = %q, want empty", got)
	}
}

func TestDurableMessageCompleteEventPreservesRuntimeAndUsage(t *testing.T) {
	event := durableMessageCompleteEvent(&agent.ChatResponse{
		FinishReason:    "stop",
		Incomplete:      true,
		Usage:           &agent.Usage{PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14, CachedTokens: 3, ReasoningTokens: 2},
		DurationMs:      1250,
		TokensPerSecond: 3.2,
	}, 42)

	if event.MessageID != 42 || event.FinishReason != "stop" || !event.Incomplete || event.DurationMs != 1250 || event.TokensPerSecond != 3.2 {
		t.Fatalf("completion event runtime mismatch: %+v", event)
	}
	if event.Usage == nil || event.Usage.TotalTokens != 14 || event.Usage.CachedTokens != 3 || event.Usage.ReasoningTokens != 2 {
		t.Fatalf("completion event usage mismatch: %+v", event.Usage)
	}
}

func TestMarkIncompleteAgentMessagesMarksOnlyTheCurrentAssistant(t *testing.T) {
	messages := []map[string]interface{}{
		{"role": "assistant", "content": "first"},
		{"role": "tool", "tool_call_id": "call-1", "content": "done"},
		{"role": "assistant", "content": "partial"},
	}

	if !markIncompleteAgentMessages(messages) {
		t.Fatal("partial assistant message was not marked incomplete")
	}
	if _, exists := messages[0]["metadata"]; exists {
		t.Fatalf("completed assistant message was modified: %#v", messages[0])
	}
	metadata, ok := messages[2]["metadata"].(map[string]interface{})
	if !ok || metadata["incomplete"] != true {
		t.Fatalf("partial assistant metadata = %#v", messages[2]["metadata"])
	}
}

func TestLastAssistantMessageID(t *testing.T) {
	messages := []*model.Message{
		{ID: 11, Role: "assistant"},
		{ID: 12, Role: "tool"},
		{ID: 13, Role: "assistant"},
	}
	if got := lastAssistantMessageID(messages); got != 13 {
		t.Fatalf("last assistant id = %d, want 13", got)
	}
}

func TestShouldRunBackgroundMemoryMaintenanceSkipsSuccessfulToolWrite(t *testing.T) {
	shouldRun, err := shouldRunBackgroundMemoryMaintenance(context.Background(), nil, nil, 1, 1, "请记住这个偏好", true, true)
	if err != nil {
		t.Fatalf("shouldRunBackgroundMemoryMaintenance: %v", err)
	}
	if shouldRun {
		t.Fatal("successful memory tool write must skip background maintenance")
	}
}

func TestAgentProducedSuccessfulMemoryWrite(t *testing.T) {
	messages := []map[string]interface{}{
		{
			"role": "assistant",
			"tool_calls": []interface{}{
				map[string]interface{}{
					"id": "memory-1",
					"function": map[string]interface{}{
						"name":      "memory",
						"arguments": `{"action":"replace","line_number":1,"content":"updated"}`,
					},
				},
			},
		},
		{
			"role":         "tool",
			"tool_call_id": "memory-1",
			"content":      `{"ok":true,"action":"replace","line_number":1}`,
		},
	}
	if !agentProducedSuccessfulMemoryWrite(messages) {
		t.Fatal("successful memory tool result was not detected")
	}
	messages[1]["content"] = `{"ok":false,"error":"memory changed while editing"}`
	if agentProducedSuccessfulMemoryWrite(messages) {
		t.Fatal("failed memory tool result must not suppress background maintenance")
	}
}

func TestReplayExistingRunWritesStoredEvents(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("GET", "/stream", nil)
	writer, err := streaming.NewSSEWriter(c)
	if err != nil {
		t.Fatalf("new sse writer: %v", err)
	}

	runHub := service.NewRunHub(time.Minute, 1<<20)
	run, err := runHub.Start(10, 20, 30, "run-replay", service.RunKindChat)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	runHub.Record(run.RunID, streaming.EventContentDelta, streaming.ContentDeltaEvent{Delta: "hello"})
	runHub.Complete(run.RunID, nil, nil)

	replayExistingRun(c, writer, runHub, 0, 10, 20, run.RunID, 0)

	body := rec.Body.String()
	if !strings.Contains(body, "event: content_delta") || !strings.Contains(body, `"delta":"hello"`) {
		t.Fatalf("replayed SSE body missing stored content event: %q", body)
	}
}

func TestRunHubEventWriterDoesNotWaitForSlowSubscriber(t *testing.T) {
	runHub := service.NewRunHub(time.Minute, 1<<20)
	run, err := runHub.Start(10, 20, 30, "slow-subscriber-producer", service.RunKindChat)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	_, _, cleanup, _, err := runHub.EventsAfter(run.RunID, 10, 20, 0)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer cleanup()

	done := make(chan struct{})
	go func() {
		writer := runHubEventWriter{runHub: runHub, runID: run.RunID}
		for i := 0; i < 100; i++ {
			_ = writer.WriteEvent(streaming.EventContentDelta, streaming.ContentDeltaEvent{Delta: "x"})
		}
		runHub.Complete(run.RunID, nil, nil)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("run producer waited for an unread live subscriber")
	}
	snapshot, ok := runHub.Get(run.RunID, 10, 20)
	if !ok || snapshot.Status != service.RunStatusCompleted || snapshot.Cursor != 101 || len(snapshot.Content) != 100 {
		t.Fatalf("completed snapshot = %+v", snapshot)
	}
}

func TestRunAgentStreamContinuesAfterRequestDisconnect(t *testing.T) {
	runHub := service.NewRunHub(time.Minute, 1<<20)
	run, err := runHub.Start(7, 9, 44, "request-disconnected", service.RunKindChat)
	if err != nil {
		t.Fatal(err)
	}
	if err := runHub.PersistDurable(context.Background(), run.RunID, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !runHub.CancelWithCause(run.RunID, 7, 9, service.RunCancelFirstOutputTimeout) {
		t.Fatal("failed to cancel admitted run")
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	cancelRequest()
	c.Request = httptest.NewRequest(http.MethodPost, "/stream", nil).WithContext(requestCtx)

	runAgentStream(c, nil, nil, nil, nil, nil, nil, runHub, nil, 0, time.Second, 7, 9, &model.Message{ID: 44}, run, modelusage.KindChat)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snapshot, ok := runHub.Get(run.RunID, 7, 9)
		if ok && snapshot.Status == service.RunStatusCanceled {
			return
		}
		time.Sleep(time.Millisecond)
	}
	snapshot, _ := runHub.Get(run.RunID, 7, 9)
	t.Fatalf("disconnected request left run unfinished: %+v", snapshot)
}

func TestExecuteAgentRunRecoversPanicAndIsolatesCurrentRun(t *testing.T) {
	runHub := service.NewRunHub(time.Minute, 1<<20)
	run, err := runHub.Start(7, 9, 44, "panic-run", service.RunKindChat)
	if err != nil {
		t.Fatalf("start panicking run: %v", err)
	}

	executeAgentRun(agentRunExecution{
		requestID:   "panic-request",
		runHub:      runHub,
		runSnapshot: run,
	})

	snapshot, ok := runHub.Get(run.RunID, 7, 9)
	if !ok {
		t.Fatal("panicking run disappeared from run hub")
	}
	if snapshot.Status != service.RunStatusFailed {
		t.Fatalf("panicking run status = %q, want failed", snapshot.Status)
	}
	if snapshot.ErrorCode != "agent_run_panic" || snapshot.Error != "处理过程中发生异常，请重试" {
		t.Fatalf("panicking run public failure = code:%q error:%q", snapshot.ErrorCode, snapshot.Error)
	}

	otherRun, err := runHub.Start(8, 10, 45, "healthy-run", service.RunKindChat)
	if err != nil {
		t.Fatalf("start independent run after panic: %v", err)
	}
	runHub.Complete(otherRun.RunID, nil, nil)
	otherSnapshot, ok := runHub.Get(otherRun.RunID, 8, 10)
	if !ok || otherSnapshot.Status != service.RunStatusCompleted {
		t.Fatalf("independent run after panic = %#v", otherSnapshot)
	}
}

func TestReplayExistingRunWritesErrorForWrongScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("GET", "/stream", nil)
	c.Set("request_id", "req-replay")
	writer, err := streaming.NewSSEWriter(c)
	if err != nil {
		t.Fatalf("new sse writer: %v", err)
	}

	runHub := service.NewRunHub(time.Minute, 1<<20)
	run, err := runHub.Start(10, 20, 30, "run-replay", service.RunKindChat)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	runHub.Complete(run.RunID, nil, nil)

	replayExistingRun(c, writer, runHub, 0, 99, 20, run.RunID, 0)

	body := rec.Body.String()
	if !strings.Contains(body, "event: error") || !strings.Contains(body, "run_not_found") || !strings.Contains(body, `"retryable":false`) || !strings.Contains(body, `"request_id":"req-replay"`) || !strings.Contains(body, `"run_id":"`+run.RunID+`"`) || strings.Contains(body, "run not found") {
		t.Fatalf("wrong-scope replay should write error event: %q", body)
	}
}

func TestRunSubscriptionFailureKeepsRecoveryMetadata(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/stream", nil)
	ctx.Set("request_id", "req-subscribe")
	writer, err := streaming.NewSSEWriter(ctx)
	if err != nil {
		t.Fatalf("new sse writer: %v", err)
	}

	writeRunSubscriptionFailed(ctx, writer, "run-subscribe", errors.New("internal subscription failure"))

	body := recorder.Body.String()
	if !strings.Contains(body, "event: error") || !strings.Contains(body, "stream_subscription_failed") || !strings.Contains(body, `"retryable":true`) || !strings.Contains(body, `"request_id":"req-subscribe"`) || !strings.Contains(body, `"run_id":"run-subscribe"`) || strings.Contains(body, "internal subscription failure") {
		t.Fatalf("subscription failure payload = %q", body)
	}
}

func TestWriteRunEventsSkipsAlreadyWrittenCursors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("GET", "/stream", nil)
	writer, err := streaming.NewSSEWriter(c)
	if err != nil {
		t.Fatalf("new sse writer: %v", err)
	}
	cursor := int64(0)
	events := []service.RunEvent{
		{Cursor: 1, Event: streaming.EventContentDelta, Data: streaming.ContentDeltaEvent{Delta: "a"}},
		{Cursor: 2, Event: streaming.EventContentDelta, Data: streaming.ContentDeltaEvent{Delta: "b"}},
	}
	if err := writeRunEvents(writer, events, &cursor); err != nil {
		t.Fatalf("write first events: %v", err)
	}
	if err := writeRunEvents(writer, []service.RunEvent{
		events[1],
		{Cursor: 3, Event: streaming.EventContentDelta, Data: streaming.ContentDeltaEvent{Delta: "c"}},
	}, &cursor); err != nil {
		t.Fatalf("write replayed events: %v", err)
	}
	if cursor != 3 || strings.Count(rec.Body.String(), "event: content_delta") != 3 {
		t.Fatalf("cursor=%d body=%q", cursor, rec.Body.String())
	}
}

func TestForwardRunEventsRecoversSubscriberOverflowOnClose(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("GET", "/stream", nil)
	writer, err := streaming.NewSSEWriter(c)
	if err != nil {
		t.Fatalf("new sse writer: %v", err)
	}

	runHub := service.NewRunHub(time.Minute, 1<<20)
	run, err := runHub.Start(10, 20, 30, "run-overflow-replay", service.RunKindChat)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	events, ch, cleanup, _, err := runHub.EventsAfter(run.RunID, 10, 20, 0)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer cleanup()
	for i := 0; i < 80; i++ {
		runHub.Record(run.RunID, streaming.EventContentDelta, streaming.ContentDeltaEvent{Delta: "x"})
	}
	runHub.Complete(run.RunID, nil, nil)

	forwardRunEvents(c, writer, runHub, 0, 10, 20, run.RunID, events, ch, 0)

	if got := strings.Count(rec.Body.String(), "event: content_delta"); got != 80 {
		t.Fatalf("replayed content events=%d, want 80", got)
	}
}

func TestForwardRunEventsSignalsReplayGapAfterTrimmedSubscriberOverflow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("GET", "/stream", nil)
	writer, err := streaming.NewSSEWriter(c)
	if err != nil {
		t.Fatalf("new sse writer: %v", err)
	}

	runHub := service.NewRunHub(time.Minute, 1024)
	run, err := runHub.Start(10, 20, 30, "run-overflow-trimmed-replay", service.RunKindChat)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	events, ch, cleanup, _, err := runHub.EventsAfter(run.RunID, 10, 20, 0)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer cleanup()
	for i := 0; i < 80; i++ {
		runHub.Record(run.RunID, streaming.EventContentDelta, streaming.ContentDeltaEvent{Delta: strings.Repeat("x", 256)})
	}
	runHub.Complete(run.RunID, nil, nil)

	forwardRunEvents(c, writer, runHub, 0, 10, 20, run.RunID, events, ch, 0)

	body := rec.Body.String()
	gapIndex := strings.Index(body, "event: replay_gap")
	snapshotIndex := strings.Index(body, "event: run_snapshot")
	if gapIndex < 0 || snapshotIndex <= gapIndex {
		t.Fatalf("trimmed overflow should signal replay gap before snapshot: %q", body)
	}
}
