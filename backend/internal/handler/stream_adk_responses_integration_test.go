package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
	"github.com/huoguojun123/EffChat/pkg/streaming"
)

// scriptedResponsesProvider drives the real official Responses component. It
// intentionally shares the existing ADK/RunHub/PostgreSQL harness so the new
// wire protocol cannot acquire a parallel retry or persistence lifecycle.
type scriptedResponsesProvider struct {
	calls  atomic.Int32
	steps  []func(http.ResponseWriter, *http.Request)
	server *httptest.Server
}

func newScriptedResponsesProvider(t *testing.T, steps ...func(http.ResponseWriter, *http.Request)) *scriptedResponsesProvider {
	t.Helper()
	provider := &scriptedResponsesProvider{steps: steps}
	provider.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
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

func writeResponsesStreamingCompletion(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprint(w, "data: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"resp_adk\",\"status\":\"in_progress\",\"model\":\"gpt-5.1\",\"output\":[]}}\n\n")
	_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"item_id\":\"msg_1\",\"output_index\":0,\"content_index\":0,\"delta\":%q,\"logprobs\":[]}\n\n", content)
	_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":{\"id\":\"resp_adk\",\"status\":\"completed\",\"model\":\"gpt-5.1\",\"output\":[],\"usage\":{\"input_tokens\":9,\"output_tokens\":6,\"total_tokens\":15}}}\n\n")
}

func writeResponsesPartialTransportFailure(w http.ResponseWriter, _ *http.Request) {
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
	_, _ = fmt.Fprint(buffered, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nContent-Length: 1048576\r\nConnection: close\r\n\r\n")
	_, _ = fmt.Fprint(buffered, "data: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"resp_partial\",\"status\":\"in_progress\",\"model\":\"gpt-5.1\",\"output\":[]}}\n\n")
	_, _ = fmt.Fprint(buffered, "data: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"item_id\":\"msg_1\",\"output_index\":0,\"content_index\":0,\"delta\":\"partial durable text\",\"logprobs\":[]}\n\n")
	_ = buffered.Flush()
}

func newResponsesADKHarness(t *testing.T, env *testEnv, provider *scriptedResponsesProvider) *adkRunRegressionHarness {
	t.Helper()
	return newADKRunRegressionHarnessForProvider(t, env, adkProviderHarnessConfig{
		Adapter:     service.AdapterOpenAIResponses,
		BaseURL:     provider.server.URL + "/v1",
		ModelID:     "gpt-5.1",
		DisplayName: "Responses ADK regression",
	})
}

func TestResponsesADKCompletionReplayDoesNotDuplicateDurableState(t *testing.T) {
	provider := newScriptedResponsesProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		writeResponsesStreamingCompletion(w, "exactly once response")
	})
	env := setupTestEnv(t)
	harness := newResponsesADKHarness(t, env, provider)
	session := harness.createSession(t, "Responses completion replay")
	runID := "responses-completion-replay"

	first := harness.send(t, session.ID, runID, "answer once")
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), "event: "+streaming.EventMessageComplete) {
		t.Fatalf("initial stream status=%d body=%s", first.Code, first.Body.String())
	}
	harness.drainUsage(t)
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
}

func TestResponsesADKRetriesOnlyZeroOutputTransientFailure(t *testing.T) {
	provider := newScriptedResponsesProvider(t,
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "0.01")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprint(w, `{"error":{"message":"temporarily unavailable","type":"server_error","code":"server_error"}}`)
		},
		func(w http.ResponseWriter, _ *http.Request) {
			writeResponsesStreamingCompletion(w, "second attempt wins")
		},
	)
	env := setupTestEnv(t)
	harness := newResponsesADKHarness(t, env, provider)
	session := harness.createSession(t, "Responses zero-output retry")
	runID := "responses-zero-output-retry"

	stream := harness.send(t, session.ID, runID, "retry transient provider failure")
	if stream.Code != http.StatusOK || !strings.Contains(stream.Body.String(), "event: "+streaming.EventMessageComplete) {
		t.Fatalf("retry stream status=%d body=%s", stream.Code, stream.Body.String())
	}
	harness.drainUsage(t)
	if got := provider.calls.Load(); got != 2 {
		t.Fatalf("provider calls = %d, want one failed attempt plus one retry", got)
	}
	events := runEventsForRegression(t, harness.runHub, runID, session.ID, env.userID)
	if countRunEvents(events, streaming.EventModelRetry) != 1 || countRunEvents(events, streaming.EventMessageComplete) != 1 {
		t.Fatalf("retry event ledger = %+v", events)
	}
	rows := readADKRunRegressionRows(t, env.db, session.ID, runID)
	if rows.messages != 2 || rows.attempts != 1 || rows.usage != 2 || rows.attemptStatus != repository.AnswerAttemptStatusCompleted || !rows.selected {
		t.Fatalf("retry durable rows = %+v", rows)
	}
}

func TestResponsesADKPartialOutputDoesNotRetry(t *testing.T) {
	provider := newScriptedResponsesProvider(t, writeResponsesPartialTransportFailure)
	env := setupTestEnv(t)
	harness := newResponsesADKHarness(t, env, provider)
	session := harness.createSession(t, "Responses partial output")
	runID := "responses-partial-no-retry"

	stream := harness.send(t, session.ID, runID, "preserve partial output")
	if stream.Code != http.StatusOK || !strings.Contains(stream.Body.String(), "partial durable text") {
		t.Fatalf("partial stream status=%d body=%s", stream.Code, stream.Body.String())
	}
	harness.drainUsage(t)
	if got := provider.calls.Load(); got != 1 {
		t.Fatalf("provider calls = %d, want no replay after output", got)
	}
	events := runEventsForRegression(t, harness.runHub, runID, session.ID, env.userID)
	if countRunEvents(events, streaming.EventModelRetry) != 0 || countRunEvents(events, streaming.EventMessageComplete) != 1 {
		t.Fatalf("partial event ledger = %+v", events)
	}
	rows := readADKRunRegressionRows(t, env.db, session.ID, runID)
	if rows.messages != 2 || rows.attempts != 1 || rows.usage != 1 || rows.attemptStatus != repository.AnswerAttemptStatusIncomplete || !rows.selected {
		t.Fatalf("partial durable rows = %+v", rows)
	}
}
