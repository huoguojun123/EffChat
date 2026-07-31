package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/agent"
	"github.com/huoguojun123/EffChat/internal/middleware"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
	modelusage "github.com/huoguojun123/EffChat/internal/usage"
	"github.com/huoguojun123/EffChat/pkg/streaming"
)

func TestSendMessageRequiresCompactionBeforePersistence(t *testing.T) {
	env := setupTestEnv(t)
	einoAgent := newCompactionGateTestAgent(t, env)

	created := env.doRequest(http.MethodPost, "/api/v1/sessions", map[string]interface{}{
		"model_id": "gpt-4o-mini",
		"provider": env.channelKey,
		"title":    "Compaction gate",
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create session: status=%d body=%s", created.Code, created.Body.String())
	}
	var session model.Session
	if err := json.Unmarshal(created.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode session: %v", err)
	}

	runHub := service.NewRunHub(time.Minute, 1<<20)
	router := gin.New()
	auth := router.Group("/api/v1")
	auth.Use(middleware.AuthMiddleware(env.authService))
	auth.POST(
		"/sessions/:id/messages/stream",
		SendMessageStreamHandler(
			env.messageService,
			env.sessionService,
			env.authService,
			service.NewSkillService(nil, nil, nil),
			einoAgent,
			nil,
			runHub,
			nil,
			nil,
			0,
			0,
		),
	)

	body, _ := json.Marshal(map[string]interface{}{
		"content":       "This draft must not be persisted before compaction.",
		"client_run_id": "compaction-gate-run",
	})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/sessions/%d/messages/stream", session.ID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+env.token)
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("send status=%d, want 409; body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Code      string `json:"code"`
		Retryable bool   `json:"retryable"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Code != "compaction_required" || !payload.Retryable {
		t.Fatalf("response=%+v, want retryable compaction_required", payload)
	}

	var messageCount int
	if err := env.db.QueryRow("SELECT COUNT(*) FROM messages WHERE session_id = $1", session.ID).Scan(&messageCount); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if messageCount != 0 {
		t.Fatalf("message count=%d, want 0", messageCount)
	}
	if active := runHub.Active(session.ID, env.userID); active != nil {
		t.Fatalf("run reservation leaked: %+v", active)
	}
}

func TestRetryRequiresProtectedCompactionBeforeReservation(t *testing.T) {
	env := setupTestEnv(t)
	einoAgent := newCompactionGateTestAgent(t, env)
	created := env.doRequest(http.MethodPost, "/api/v1/sessions", map[string]interface{}{
		"model_id": "gpt-4o-mini",
		"provider": env.channelKey,
		"title":    "Retry compaction gate",
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create session: status=%d body=%s", created.Code, created.Body.String())
	}
	var session model.Session
	if err := json.Unmarshal(created.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode session: %v", err)
	}

	firstUser, err := env.messageService.CreateUserMessage(session.ID, env.userID, &service.SendMessageRequest{Content: "Earlier context that may be compacted.", SchemaVersion: "v1"})
	if err != nil {
		t.Fatalf("create first user: %v", err)
	}
	if _, err := env.messageService.CreateAssistantMessage(session.ID, env.userID, map[string]interface{}{"role": "assistant", "content": "Earlier answer."}, "v1"); err != nil {
		t.Fatalf("create first assistant: %v", err)
	}
	retryUser, err := env.messageService.CreateUserMessage(session.ID, env.userID, &service.SendMessageRequest{Content: "Regenerate this final user turn.", SchemaVersion: "v1"})
	if err != nil {
		t.Fatalf("create retry user: %v", err)
	}
	retryAssistant, err := env.messageService.CreateAssistantMessage(session.ID, env.userID, map[string]interface{}{"role": "assistant", "content": "The answer being replaced."}, "v1")
	if err != nil {
		t.Fatalf("create retry assistant: %v", err)
	}
	if firstUser.ID >= retryUser.ID {
		t.Fatalf("invalid test message order: first=%d retry=%d", firstUser.ID, retryUser.ID)
	}

	runHub := service.NewRunHub(time.Minute, 1<<20)
	router := gin.New()
	auth := router.Group("/api/v1")
	auth.Use(middleware.AuthMiddleware(env.authService))
	auth.POST(
		"/sessions/:id/messages/:message_id/retry",
		RetryMessageStreamHandler(
			env.messageService,
			env.sessionService,
			env.authService,
			service.NewSkillService(nil, nil, nil),
			einoAgent,
			nil,
			runHub,
			nil,
			nil,
			0,
			0,
		),
	)

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/sessions/%d/messages/%d/retry?client_run_id=retry-compaction-gate", session.ID, retryAssistant.ID), nil)
	req.Header.Set("Authorization", "Bearer "+env.token)
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("retry status=%d, want 409; body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Code              string `json:"code"`
		Retryable         bool   `json:"retryable"`
		PreserveMessageID int64  `json:"preserve_message_id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode retry response: %v", err)
	}
	if payload.Code != "compaction_required" || !payload.Retryable || payload.PreserveMessageID != retryUser.ID {
		t.Fatalf("retry compaction response=%+v, want protected user id %d", payload, retryUser.ID)
	}
	if active := runHub.Active(session.ID, env.userID); active != nil {
		t.Fatalf("retry compaction gate leaked run reservation: %+v", active)
	}
	var attemptCount int
	if err := env.db.QueryRow("SELECT COUNT(*) FROM answer_attempts WHERE session_id = $1", session.ID).Scan(&attemptCount); err != nil {
		t.Fatalf("count retry attempts: %v", err)
	}
	if attemptCount != 0 {
		t.Fatalf("retry compaction gate created %d attempts", attemptCount)
	}
}

func newCompactionGateTestAgent(t *testing.T, env *testEnv) *agent.EinoAgent {
	t.Helper()
	previous := modelbank.Get("gpt-4o-mini")
	modelbank.Register(&modelbank.ModelInfo{
		ID:             "gpt-4o-mini",
		DisplayName:    "Handler test model",
		Provider:       env.channelKey,
		Enabled:        true,
		ThinkingFormat: "auto",
		Capabilities: modelbank.ModelCapabilities{
			ContextWindow: 4096,
			MaxOutput:     1024,
		},
	})
	if previous != nil {
		t.Cleanup(func() {
			modelbank.Register(previous)
		})
	}
	return agent.NewEinoAgent(
		service.NewChannelService(repository.NewChannelRepository(env.db)),
		nil,
		1,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
}

func TestMessageEndpointsRejectOversizedJSONBeforeServiceWork(t *testing.T) {
	body, err := json.Marshal(map[string]string{"content": strings.Repeat("x", service.MaxMessageRequestBytes)})
	if err != nil {
		t.Fatal(err)
	}
	for name, factory := range map[string]func() gin.HandlerFunc{
		"send": func() gin.HandlerFunc {
			return SendMessageStreamHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, 0, 0)
		},
		"preflight": func() gin.HandlerFunc {
			return MessagePreflightHandler(nil, nil, nil, nil, nil, nil, nil)
		},
	} {
		t.Run(name, func(t *testing.T) {
			router := gin.New()
			router.POST("/api/v1/sessions/:id/messages/"+name, factory())
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/1/messages/"+name, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, req)
			if recorder.Code != http.StatusRequestEntityTooLarge || !strings.Contains(recorder.Body.String(), "message_too_large") {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}

	router := gin.New()
	router.POST(
		"/api/v1/sessions/:id/messages/:message_id/edit-retry",
		EditRetryMessageStreamHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, 0, 0),
	)
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/1/messages/2/edit-retry", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusRequestEntityTooLarge || !strings.Contains(recorder.Body.String(), "message_too_large") {
		t.Fatalf("edit retry status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestSendMessageReplaysExistingRunBeforeCompactionGate(t *testing.T) {
	env := setupTestEnv(t)
	created := env.doRequest(http.MethodPost, "/api/v1/sessions", map[string]interface{}{
		"model_id": "gpt-4o-mini",
		"provider": env.channelKey,
		"title":    "Idempotent replay",
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create session: status=%d body=%s", created.Code, created.Body.String())
	}
	var session model.Session
	if err := json.Unmarshal(created.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode session: %v", err)
	}

	runHub := service.NewRunHub(time.Minute, 1<<20)
	sendRequest := &service.SendMessageRequest{
		Content:     "This draft would require compaction if evaluated.",
		ClientRunID: "existing-run",
	}
	run, err := runHub.StartWithIntent(session.ID, env.userID, 44, "existing-run", service.RunKindChat, service.BuildSendRunIntent(&session, sendRequest))
	if err != nil {
		t.Fatalf("start existing run: %v", err)
	}
	runHub.Record(run.RunID, "content_delta", map[string]interface{}{"delta": "stored answer"})
	runHub.Complete(run.RunID, nil, nil)

	router := gin.New()
	auth := router.Group("/api/v1")
	auth.Use(middleware.AuthMiddleware(env.authService))
	auth.POST(
		"/sessions/:id/messages/stream",
		SendMessageStreamHandler(
			env.messageService,
			env.sessionService,
			env.authService,
			service.NewSkillService(nil, nil, nil),
			agent.NewEinoAgent(nil, nil, 1, nil, nil, nil, nil, nil, nil),
			nil,
			runHub,
			nil,
			nil,
			0,
			0,
		),
	)

	body, _ := json.Marshal(sendRequest)
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/sessions/%d/messages/stream", session.ID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+env.token)
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "stored answer") {
		t.Fatalf("existing run was not replayed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var messageCount int
	if err := env.db.QueryRow("SELECT COUNT(*) FROM messages WHERE session_id = $1", session.ID).Scan(&messageCount); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if messageCount != 0 {
		t.Fatalf("idempotent replay persisted %d new messages", messageCount)
	}
}

func TestSendMessageReplaysDurableTerminalAndRejectsChangedIntent(t *testing.T) {
	env := setupTestEnv(t)
	created := env.doRequest(http.MethodPost, "/api/v1/sessions", map[string]interface{}{
		"model_id": "gpt-4o-mini",
		"provider": env.channelKey,
		"title":    "Durable replay",
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create session: status=%d body=%s", created.Code, created.Body.String())
	}
	var session model.Session
	if err := json.Unmarshal(created.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}

	sendRequest := &service.SendMessageRequest{Content: "persist once", ClientRunID: "durable-terminal"}
	intent := service.BuildSendRunIntent(&session, sendRequest)
	quotaRepo := repository.NewQuotaRepository(env.db)
	message, err := env.messageService.BuildUserMessagePreview(session.ID, env.userID, sendRequest)
	if err != nil {
		t.Fatal(err)
	}
	admission, err := quotaRepo.AdmitChatMessage(context.Background(), repository.ChatRunReservationInput{
		UserID: env.userID, AuthVersion: 1, SessionID: session.ID, RunID: sendRequest.ClientRunID,
		Kind: service.RunKindChat, Operation: intent.Operation, IntentVersion: intent.Version, IntentHash: intent.Hash,
		ReserveMessage: true, ExpiresAt: time.Now().Add(time.Minute),
	}, message)
	if err != nil {
		t.Fatal(err)
	}
	assistantMessage := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"assistant","content":"persisted answer"}`)}
	if err := repository.NewMessageRepository(env.db).Create(assistantMessage); err != nil {
		t.Fatal(err)
	}
	terminalEvent, _ := json.Marshal(map[string]interface{}{
		"event": streaming.EventMessageComplete,
		"data":  map[string]interface{}{"message_id": assistantMessage.ID, "finish_reason": "stop"},
	})
	if _, transitioned, err := quotaRepo.TransitionChatRun(context.Background(), repository.ChatRunTransitionInput{
		RunID: admission.Record.RunID, Status: service.RunStatusCompleted, TerminalMessageID: assistantMessage.ID,
		TerminalEvent: terminalEvent, ExpiresAt: time.Now().Add(time.Minute),
	}); err != nil || !transitioned {
		t.Fatalf("complete durable run: transitioned=%v err=%v", transitioned, err)
	}

	runHub := service.NewRunHub(time.Minute, 1<<20)
	runHub.SetStore(quotaRepo)
	quotaService := service.NewQuotaService(quotaRepo)
	router := gin.New()
	auth := router.Group("/api/v1")
	auth.Use(middleware.AuthMiddleware(env.authService))
	auth.POST("/sessions/:id/messages/stream", SendMessageStreamHandler(
		env.messageService, env.sessionService, env.authService, nil, nil, nil,
		runHub, quotaService, nil, 0, 0,
	))

	requestBody, _ := json.Marshal(sendRequest)
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/sessions/%d/messages/stream", session.ID), bytes.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+env.token)
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), streaming.EventMessageComplete) {
		t.Fatalf("durable replay status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	changed := *sendRequest
	changed.Content = "different payload"
	requestBody, _ = json.Marshal(&changed)
	recorder = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/sessions/%d/messages/stream", session.ID), bytes.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+env.token)
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "run_id_conflict") {
		t.Fatalf("changed intent status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestFailRunWithPublicErrorTerminatesReservationSubscribers(t *testing.T) {
	runHub := service.NewRunHub(time.Minute, 1<<20)
	run, err := runHub.Start(7, 9, 0, "reserved-run", service.RunKindChat)
	if err != nil {
		t.Fatalf("start reservation: %v", err)
	}
	_, ch, cleanup, _, err := runHub.EventsAfter(run.RunID, 7, 9, 0)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer cleanup()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/stream", nil)
	c.Set("request_id", "req-reservation")
	failRunWithPublicError(c, runHub, run.RunID, "message_create_failed", "消息创建失败，请重试", errors.New("sql: secret"))

	event, ok := <-ch
	if !ok || event.Event != "error" {
		t.Fatalf("reservation subscriber did not receive terminal error: ok=%v event=%+v", ok, event)
	}
	payload, _ := event.Data.(gin.H)
	if payload["code"] != "message_create_failed" || payload["request_id"] != "req-reservation" {
		t.Fatalf("terminal payload=%+v", payload)
	}
	if _, stillOpen := <-ch; stillOpen {
		t.Fatal("reservation subscriber channel remained open")
	}
}

func TestRunAgentStreamAcknowledgesDurableTurnBeforeCancellation(t *testing.T) {
	runHub := service.NewRunHub(time.Minute, 1<<20)
	run, err := runHub.Start(7, 9, 44, "accepted-before-bootstrap", service.RunKindChat)
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
	c.Request = httptest.NewRequest(http.MethodPost, "/stream", nil)
	runAgentStream(c, nil, nil, nil, nil, nil, nil, runHub, nil, 0, time.Second, 7, 9, &model.Message{ID: 44}, run, modelusage.KindChat)

	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || !strings.Contains(body, streaming.EventMessageStart) || !strings.Contains(body, "first_output_timeout") {
		t.Fatalf("admitted cancellation stream status=%d body=%s", recorder.Code, body)
	}
}

func TestFailQuotaAdmissionPreservesQuotaTerminal(t *testing.T) {
	runHub := service.NewRunHub(time.Minute, 1<<20)
	run, err := runHub.Start(7, 9, 0, "quota-admission", service.RunKindChat)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/stream", nil)
	c.Set("request_id", "quota-request")

	if !failQuotaAdmission(c, runHub, run.RunID, &service.QuotaError{
		Code: "daily_message_limit_exceeded", Message: "今日消息数已达上限（1）", Limit: 1, Used: 1,
	}) {
		t.Fatal("quota error was not classified")
	}
	if recorder.Code != http.StatusTooManyRequests || !strings.Contains(recorder.Body.String(), "daily_message_limit_exceeded") {
		t.Fatalf("quota response status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	snapshot, ok := runHub.Get(run.RunID, 7, 9)
	if !ok || snapshot.Status != service.RunStatusFailed || snapshot.ErrorCode != "daily_message_limit_exceeded" {
		t.Fatalf("quota terminal snapshot=%+v", snapshot)
	}
}

func TestMessageCreationFailureClassifiesUserInput(t *testing.T) {
	status, code, _, retryable := messageCreationFailure(fmt.Errorf("%w: attachment is invalid", service.ErrInvalidMessageInput))
	if status != http.StatusBadRequest || code != "message_input_invalid" || retryable {
		t.Fatalf("invalid input classification = status=%d code=%s retryable=%v", status, code, retryable)
	}
	status, code, _, retryable = messageCreationFailure(errors.New("database unavailable"))
	if status != http.StatusInternalServerError || code != "message_create_failed" || !retryable {
		t.Fatalf("internal classification = status=%d code=%s retryable=%v", status, code, retryable)
	}
}

func TestWriteRunTerminalMapsCancellationCauses(t *testing.T) {
	tests := []struct {
		name      string
		cause     service.RunCancelCause
		wantEvent string
		wantCode  string
	}{
		{name: "user stop", cause: service.RunCancelUserStop, wantEvent: streaming.EventMessageComplete},
		{name: "first output timeout", cause: service.RunCancelFirstOutputTimeout, wantEvent: streaming.EventError, wantCode: "first_output_timeout"},
		{name: "server drain", cause: service.RunCancelServerDrain, wantEvent: streaming.EventError, wantCode: "server_draining"},
		{name: "account changed", cause: service.RunCancelAccountChanged, wantEvent: streaming.EventError, wantCode: "account_changed"},
		{name: "session deleted", cause: service.RunCancelSessionDeleted, wantEvent: streaming.EventError, wantCode: "session_deleted"},
		{name: "upstream canceled", cause: service.RunCancelUpstream, wantEvent: streaming.EventError, wantCode: "upstream_canceled"},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runHub := service.NewRunHub(time.Minute, 1<<20)
			run, err := runHub.Start(int64(index+1), 9, 0, "cancel-"+strings.ReplaceAll(test.name, " ", "-"), service.RunKindChat)
			if err != nil {
				t.Fatal(err)
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/stream", nil)
			writer, err := streaming.NewSSEWriter(c)
			if err != nil {
				t.Fatal(err)
			}
			if err := writeRunTerminal(writer, runHub, run.RunID, service.RunTerminal{
				Status: service.RunStatusCanceled, CancelCause: test.cause,
			}); err != nil {
				t.Fatal(err)
			}
			snapshot, ok := runHub.Get(run.RunID, int64(index+1), 9)
			if !ok || snapshot.Status != service.RunStatusCanceled || snapshot.CancelCause != string(test.cause) {
				t.Fatalf("terminal snapshot = %+v", snapshot)
			}
			body := recorder.Body.String()
			if !strings.Contains(body, "event: "+test.wantEvent) {
				t.Fatalf("terminal event body = %q", body)
			}
			if test.wantCode != "" && !strings.Contains(body, `"code":"`+test.wantCode+`"`) {
				t.Fatalf("terminal error body = %q", body)
			}
		})
	}
}

func TestWriteRunTerminalPreservesExplicitPartialCompletionAcrossCancellation(t *testing.T) {
	runHub := service.NewRunHub(time.Minute, 1<<20)
	run, err := runHub.Start(7, 9, 44, "partial-cancel", service.RunKindChat)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/stream", nil)
	writer, err := streaming.NewSSEWriter(c)
	if err != nil {
		t.Fatal(err)
	}

	err = writeRunTerminal(writer, runHub, run.RunID, service.RunTerminal{
		Status:      service.RunStatusCanceled,
		CancelCause: service.RunCancelServerDrain,
		Event:       streaming.EventMessageComplete,
		Data: streaming.MessageCompleteEvent{
			MessageID: 44, FinishReason: "canceled", Incomplete: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	snapshot, ok := runHub.Get(run.RunID, 7, 9)
	if !ok || snapshot.Status != service.RunStatusCanceled || snapshot.ErrorCode != "server_draining" {
		t.Fatalf("terminal snapshot = %+v", snapshot)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "event: "+streaming.EventMessageComplete) || !strings.Contains(body, `"incomplete":true`) {
		t.Fatalf("partial completion was not preserved: %q", body)
	}
	if strings.Contains(body, "event: "+streaming.EventError) {
		t.Fatalf("partial completion emitted a second terminal error: %q", body)
	}
}

func TestShouldPersistCanceledPartial(t *testing.T) {
	for _, cause := range []service.RunCancelCause{
		service.RunCancelUserStop,
		service.RunCancelFirstOutputTimeout,
		service.RunCancelServerDrain,
		service.RunCancelAccountChanged,
		service.RunCancelUpstream,
	} {
		if !shouldPersistCanceledPartial(cause) {
			t.Fatalf("cause %q should preserve generated partial messages", cause)
		}
	}
	if shouldPersistCanceledPartial(service.RunCancelSessionDeleted) {
		t.Fatal("deleted sessions must not receive partial message writes")
	}
}

func TestTransitionReservationFailureCancelsStaleAuthenticatedRun(t *testing.T) {
	runHub := service.NewRunHub(time.Minute, 1<<20)
	run, err := runHub.Start(7, 9, 0, "stale-auth-run", service.RunKindChat)
	if err != nil {
		t.Fatal(err)
	}
	runContext, ok := runHub.Context(run.RunID)
	if !ok {
		t.Fatal("run context missing")
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/stream", nil)

	if !transitionReservationFailure(c, runHub, run.RunID, 7, 9, runContext, service.ErrAuthenticationUnavailable) {
		t.Fatal("stale authentication error was not handled")
	}
	if recorder.Code != http.StatusUnauthorized || !strings.Contains(recorder.Body.String(), `"code":"account_changed"`) {
		t.Fatalf("reservation failure response = %d %s", recorder.Code, recorder.Body.String())
	}
	snapshot, ok := runHub.Get(run.RunID, 7, 9)
	if !ok || snapshot.Status != service.RunStatusCanceled || snapshot.CancelCause != string(service.RunCancelAccountChanged) {
		t.Fatalf("stale authentication terminal = %+v", snapshot)
	}
}
