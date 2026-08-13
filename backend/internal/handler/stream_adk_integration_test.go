package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

// scriptedOpenAIProvider exercises the real OpenAI-compatible adapter and ADK
// runner without depending on an external model. Each request consumes exactly
// one script entry, making retry assertions observable at the provider boundary.
type scriptedOpenAIProvider struct {
	calls  atomic.Int32
	steps  []func(http.ResponseWriter, *http.Request)
	server *httptest.Server
}

func newScriptedOpenAIProvider(t *testing.T, steps ...func(http.ResponseWriter, *http.Request)) *scriptedOpenAIProvider {
	t.Helper()
	provider := &scriptedOpenAIProvider{steps: steps}
	provider.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		call := int(provider.calls.Add(1))
		if call > len(provider.steps) {
			http.Error(w, "unexpected provider request", http.StatusInternalServerError)
			return
		}
		provider.steps[call-1](w, r)
	}))
	t.Cleanup(provider.server.Close)
	return provider
}

func writeOpenAIStreamingCompletion(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "text/event-stream")
	chunks := []map[string]interface{}{
		{
			"id": "chatcmpl-handler-regression", "object": "chat.completion.chunk", "created": 1, "model": "gpt-4o-mini",
			"choices": []map[string]interface{}{{"index": 0, "delta": map[string]interface{}{"role": "assistant", "content": content}, "finish_reason": nil}},
		},
		{
			"id": "chatcmpl-handler-regression", "object": "chat.completion.chunk", "created": 1, "model": "gpt-4o-mini",
			"choices": []map[string]interface{}{{"index": 0, "delta": map[string]interface{}{}, "finish_reason": "stop"}},
		},
	}
	for _, chunk := range chunks {
		payload, err := json.Marshal(chunk)
		if err != nil {
			panic(err)
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
}

// writeOpenAIPartialTransportFailure advertises a much larger body than it
// sends, then closes the connection after one valid SSE delta. The HTTP client
// therefore receives an unexpected EOF rather than a normal SSE EOF, while ADK
// has already observed visible output and must not retry the model call.
func writeOpenAIPartialTransportFailure(w http.ResponseWriter, _ *http.Request) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "test server cannot hijack connection", http.StatusInternalServerError)
		return
	}
	conn, buffered, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	payload := `{"id":"chatcmpl-partial","object":"chat.completion.chunk","created":1,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"role":"assistant","content":"partial durable text"},"finish_reason":null}]}`
	_, _ = fmt.Fprint(buffered, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nContent-Length: 1048576\r\nConnection: close\r\n\r\n")
	_, _ = fmt.Fprintf(buffered, "data: %s\n\n", payload)
	_ = buffered.Flush()
}

type adkRunRegressionHarness struct {
	env            *testEnv
	router         *gin.Engine
	runHub         *service.RunHub
	usage          *modelusage.Service
	modelID        string
	provider       string
	thinkingEffort string
}

func newADKRunRegressionHarness(t *testing.T, env *testEnv, provider *scriptedOpenAIProvider) *adkRunRegressionHarness {
	t.Helper()
	return newADKRunRegressionHarnessForProvider(t, env, adkProviderHarnessConfig{
		Adapter:     service.AdapterOpenAICompatible,
		BaseURL:     provider.server.URL + "/v1",
		ModelID:     "gpt-4o-mini",
		DisplayName: "ADK handler regression",
	})
}

type adkProviderHarnessConfig struct {
	Adapter            string
	BaseURL            string
	ModelID            string
	DisplayName        string
	Reasoning          bool
	ThinkingFormat     string
	ThinkingEffort     string
	FirstOutputTimeout time.Duration
}

func newADKRunRegressionHarnessForProvider(t *testing.T, env *testEnv, cfg adkProviderHarnessConfig) *adkRunRegressionHarness {
	t.Helper()
	channelService := service.NewChannelService(repository.NewChannelRepository(env.db))
	enabled := true
	if _, err := channelService.SaveAIChannel(&service.AIChannelInput{
		Key: env.channelKey, DisplayName: cfg.DisplayName, Adapter: cfg.Adapter,
		BaseURL: cfg.BaseURL, APIKey: "test-key", Enabled: &enabled,
	}); err != nil {
		t.Fatalf("configure scripted model channel: %v", err)
	}
	if err := repository.NewModelRepository(env.db).Upsert(&model.Model{
		ID: cfg.ModelID, DisplayName: cfg.DisplayName, Provider: env.channelKey,
		ContextWindow: 32768, MaxOutput: 4096, Enabled: true, Reasoning: cfg.Reasoning, ThinkingFormat: cfg.ThinkingFormat,
	}); err != nil {
		t.Fatalf("configure scripted model capacity: %v", err)
	}

	previous := modelbank.Get(cfg.ModelID)
	modelbank.Register(&modelbank.ModelInfo{
		ID: cfg.ModelID, DisplayName: cfg.DisplayName, Provider: env.channelKey, Enabled: true,
		ThinkingFormat: cfg.ThinkingFormat,
		Capabilities:   modelbank.ModelCapabilities{ContextWindow: 32768, MaxOutput: 4096, Reasoning: cfg.Reasoning},
	})
	if previous != nil {
		t.Cleanup(func() { modelbank.Register(previous) })
	}

	quotaRepo := repository.NewQuotaRepository(env.db)
	quotaService := service.NewQuotaService(quotaRepo)
	usageService := modelusage.NewService(modelusage.NewRepository(env.db))
	runHub := service.NewRunHub(time.Minute, 1<<20)
	runHub.SetStore(quotaRepo)
	einoAgent := agent.NewEinoAgent(channelService, nil, 32768, nil, nil, nil, nil, usageService, quotaService)

	router := gin.New()
	auth := router.Group("/api/v1")
	auth.Use(middleware.AuthMiddleware(env.authService))
	auth.POST("/sessions/:id/messages/stream", SendMessageStreamHandler(
		env.messageService,
		env.sessionService,
		env.authService,
		service.NewSkillService(nil, nil, nil),
		einoAgent,
		nil,
		runHub,
		quotaService,
		nil,
		0,
		cfg.FirstOutputTimeout,
	))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = usageService.Drain(ctx)
	})
	return &adkRunRegressionHarness{
		env: env, router: router, runHub: runHub, usage: usageService,
		modelID: cfg.ModelID, provider: env.channelKey, thinkingEffort: cfg.ThinkingEffort,
	}
}

func (h *adkRunRegressionHarness) createSession(t *testing.T, title string) *model.Session {
	t.Helper()
	created := h.env.doRequest(http.MethodPost, "/api/v1/sessions", map[string]interface{}{
		"model_id": h.modelID, "provider": h.provider, "title": title,
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create regression session: status=%d body=%s", created.Code, created.Body.String())
	}
	var session model.Session
	if err := json.Unmarshal(created.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode regression session: %v", err)
	}
	return &session
}

func (h *adkRunRegressionHarness) send(t *testing.T, sessionID int64, runID, content string) *httptest.ResponseRecorder {
	t.Helper()
	payload := map[string]interface{}{"content": content, "client_run_id": runID}
	if h.thinkingEffort != "" {
		payload["thinking_effort"] = h.thinkingEffort
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/sessions/%d/messages/stream", sessionID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.env.token)
	h.router.ServeHTTP(recorder, req)
	return recorder
}

func (h *adkRunRegressionHarness) drainUsage(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if !h.usage.Drain(ctx) {
		t.Fatal("timed out draining model usage recorder")
	}
}

type adkRunRegressionRows struct {
	messages      int
	attempts      int
	usage         int
	attemptStatus string
	selected      bool
}

func readADKRunRegressionRows(t *testing.T, db interface {
	QueryRow(string, ...interface{}) *sql.Row
}, sessionID int64, runID string) adkRunRegressionRows {
	t.Helper()
	var rows adkRunRegressionRows
	if err := db.QueryRow("SELECT COUNT(*) FROM messages WHERE session_id = $1 AND deleted_at IS NULL", sessionID).Scan(&rows.messages); err != nil {
		t.Fatalf("count session messages: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM answer_attempts WHERE run_id = $1", runID).Scan(&rows.attempts); err != nil {
		t.Fatalf("count answer attempts: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM model_usage_events WHERE run_id = $1", runID).Scan(&rows.usage); err != nil {
		t.Fatalf("count model usage events: %v", err)
	}
	if err := db.QueryRow("SELECT status, selected FROM answer_attempts WHERE run_id = $1", runID).Scan(&rows.attemptStatus, &rows.selected); err != nil {
		t.Fatalf("read answer attempt: %v", err)
	}
	return rows
}

func runEventsForRegression(t *testing.T, hub *service.RunHub, runID string, sessionID, userID int64) []service.RunEvent {
	t.Helper()
	events, _, err := hub.EventsSince(runID, sessionID, userID, 0)
	if err != nil {
		t.Fatalf("read run events: %v", err)
	}
	return events
}

func countRunEvents(events []service.RunEvent, event string) int {
	count := 0
	for _, item := range events {
		if item.Event == event {
			count++
		}
	}
	return count
}

func TestADKRunCompletionReplayDoesNotDuplicateDurableState(t *testing.T) {
	provider := newScriptedOpenAIProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		writeOpenAIStreamingCompletion(w, "exactly once answer")
	})
	env := setupTestEnv(t)
	harness := newADKRunRegressionHarness(t, env, provider)
	session := harness.createSession(t, "ADK completion replay")
	runID := "adk-completion-replay"

	first := harness.send(t, session.ID, runID, "answer once")
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), "event: "+streaming.EventMessageComplete) {
		t.Fatalf("initial stream status=%d body=%s", first.Code, first.Body.String())
	}
	harness.drainUsage(t)
	if got := provider.calls.Load(); got != 1 {
		t.Fatalf("provider calls after initial run = %d, want 1", got)
	}
	snapshot, ok := harness.runHub.Get(runID, session.ID, env.userID)
	if !ok || snapshot.Status != service.RunStatusCompleted {
		t.Fatalf("initial terminal snapshot = %+v", snapshot)
	}
	events := runEventsForRegression(t, harness.runHub, runID, session.ID, env.userID)
	if countRunEvents(events, streaming.EventMessageStart) != 1 || countRunEvents(events, streaming.EventMessageComplete) != 1 {
		t.Fatalf("initial event ledger = %+v", events)
	}
	initialRows := readADKRunRegressionRows(t, env.db, session.ID, runID)
	if initialRows.messages != 2 || initialRows.attempts != 1 || initialRows.usage != 1 || initialRows.attemptStatus != repository.AnswerAttemptStatusCompleted || !initialRows.selected {
		t.Fatalf("initial durable rows = %+v", initialRows)
	}

	replay := harness.send(t, session.ID, runID, "answer once")
	if replay.Code != http.StatusOK || strings.Count(replay.Body.String(), "event: "+streaming.EventMessageComplete) != 1 {
		t.Fatalf("replay stream status=%d body=%s", replay.Code, replay.Body.String())
	}
	if got := provider.calls.Load(); got != 1 {
		t.Fatalf("replay invoked provider %d times, want 1 total", got)
	}
	if rows := readADKRunRegressionRows(t, env.db, session.ID, runID); rows != initialRows {
		t.Fatalf("replay changed durable rows: got=%+v want=%+v", rows, initialRows)
	}
	if events := runEventsForRegression(t, harness.runHub, runID, session.ID, env.userID); countRunEvents(events, streaming.EventMessageStart) != 1 || countRunEvents(events, streaming.EventMessageComplete) != 1 {
		t.Fatalf("replay duplicated run event ledger: %+v", events)
	}
}

func TestADKRunRetriesOnlyZeroOutputTransientFailure(t *testing.T) {
	provider := newScriptedOpenAIProvider(t,
		func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
		},
		func(w http.ResponseWriter, _ *http.Request) { writeOpenAIStreamingCompletion(w, "second attempt wins") },
	)
	env := setupTestEnv(t)
	harness := newADKRunRegressionHarness(t, env, provider)
	session := harness.createSession(t, "ADK zero-output retry")
	runID := "adk-zero-output-retry"

	stream := harness.send(t, session.ID, runID, "retry transient provider failure")
	if stream.Code != http.StatusOK || !strings.Contains(stream.Body.String(), "event: "+streaming.EventMessageComplete) {
		t.Fatalf("retry stream status=%d body=%s", stream.Code, stream.Body.String())
	}
	harness.drainUsage(t)
	if got := provider.calls.Load(); got != 2 {
		t.Fatalf("provider calls = %d, want one failed attempt plus one retry", got)
	}
	snapshot, ok := harness.runHub.Get(runID, session.ID, env.userID)
	if !ok || snapshot.Status != service.RunStatusCompleted {
		t.Fatalf("retry terminal snapshot = %+v", snapshot)
	}
	events := runEventsForRegression(t, harness.runHub, runID, session.ID, env.userID)
	if countRunEvents(events, streaming.EventModelRetry) != 1 || countRunEvents(events, streaming.EventMessageStart) != 1 || countRunEvents(events, streaming.EventMessageComplete) != 1 || countRunEvents(events, streaming.EventAttemptReset) != 0 {
		t.Fatalf("zero-output retry event ledger = %+v", events)
	}
	rows := readADKRunRegressionRows(t, env.db, session.ID, runID)
	// Usage records actual upstream calls, not user-visible answers: the failed
	// attempt and successful retry are both auditable, while the durable answer
	// remains exactly one user/assistant turn and one selected attempt.
	if rows.messages != 2 || rows.attempts != 1 || rows.usage != 2 || rows.attemptStatus != repository.AnswerAttemptStatusCompleted || !rows.selected {
		t.Fatalf("zero-output retry durable rows = %+v", rows)
	}
}

func TestADKRunPartialOutputTransportFailureDoesNotRetry(t *testing.T) {
	provider := newScriptedOpenAIProvider(t, writeOpenAIPartialTransportFailure)
	env := setupTestEnv(t)
	harness := newADKRunRegressionHarness(t, env, provider)
	session := harness.createSession(t, "ADK partial transport failure")
	runID := "adk-partial-no-retry"

	stream := harness.send(t, session.ID, runID, "keep partial answer")
	if stream.Code != http.StatusOK || !strings.Contains(stream.Body.String(), "event: "+streaming.EventMessageComplete) || !strings.Contains(stream.Body.String(), `"incomplete":true`) {
		t.Fatalf("partial stream status=%d body=%s", stream.Code, stream.Body.String())
	}
	harness.drainUsage(t)
	if got := provider.calls.Load(); got != 1 {
		t.Fatalf("partial-output failure retried provider %d times, want 1", got)
	}
	snapshot, ok := harness.runHub.Get(runID, session.ID, env.userID)
	if !ok || snapshot.Status != service.RunStatusFailed {
		t.Fatalf("partial terminal snapshot = %+v", snapshot)
	}
	events := runEventsForRegression(t, harness.runHub, runID, session.ID, env.userID)
	if countRunEvents(events, streaming.EventContentDelta) != 1 || countRunEvents(events, streaming.EventModelRetry) != 0 || countRunEvents(events, streaming.EventAttemptReset) != 0 || countRunEvents(events, streaming.EventMessageComplete) != 1 || countRunEvents(events, streaming.EventError) != 0 {
		t.Fatalf("partial-output event ledger = %+v", events)
	}
	rows := readADKRunRegressionRows(t, env.db, session.ID, runID)
	if rows.messages != 2 || rows.attempts != 1 || rows.usage != 1 || rows.attemptStatus != repository.AnswerAttemptStatusIncomplete || !rows.selected {
		t.Fatalf("partial-output durable rows = %+v", rows)
	}
	var incomplete bool
	if err := env.db.QueryRow(`
		SELECT COALESCE((message_data->'metadata'->>'incomplete')::boolean, false)
		FROM messages
		WHERE session_id = $1 AND role = 'assistant'
	`, session.ID).Scan(&incomplete); err != nil || !incomplete {
		t.Fatalf("partial assistant incomplete marker = %t err=%v", incomplete, err)
	}
}
