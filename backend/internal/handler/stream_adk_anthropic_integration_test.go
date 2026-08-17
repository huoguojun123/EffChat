package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
	"github.com/huoguojun123/EffChat/pkg/streaming"
)

// scriptedAnthropicProvider exercises the native Anthropic Messages API
// through Eino. Keeping this server at the HTTP/SSE boundary verifies the
// actual adapter request and stream conversion instead of a project-local fake
// ChatModel that could silently diverge from the provider protocol.
type scriptedAnthropicProvider struct {
	calls  atomic.Int32
	steps  []func(http.ResponseWriter, *http.Request)
	server *httptest.Server
}

func newScriptedAnthropicProvider(t *testing.T, steps ...func(http.ResponseWriter, *http.Request)) *scriptedAnthropicProvider {
	t.Helper()
	provider := &scriptedAnthropicProvider{steps: steps}
	provider.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
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

func assertAnthropicThinkingRequest(t *testing.T, r *http.Request) {
	t.Helper()
	if got := r.Header.Get("x-api-key"); got != "test-key" {
		t.Errorf("Anthropic x-api-key = %q, want test-key", got)
	}
	if got := r.Header.Get("X-Stainless-Retry-Count"); got != "0" {
		t.Errorf("Anthropic SDK retry count = %q, want each EffChat-owned attempt to start at 0", got)
	}
	var payload map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		t.Errorf("decode Anthropic request: %v", err)
		return
	}
	if payload["model"] != "claude-sonnet-4-6" || payload["stream"] != true {
		t.Errorf("Anthropic model/stream request = %#v", payload)
	}
	for _, field := range []string{"temperature", "top_p", "top_k"} {
		if _, exists := payload[field]; exists {
			t.Errorf("Anthropic adaptive request must omit %s: %#v", field, payload)
		}
	}
	thinking, _ := payload["thinking"].(map[string]interface{})
	if thinking["type"] != "adaptive" || thinking["display"] != "summarized" {
		t.Errorf("Anthropic thinking request = %#v", thinking)
	}
	outputConfig, _ := payload["output_config"].(map[string]interface{})
	if outputConfig["effort"] != "low" {
		t.Errorf("Anthropic output_config = %#v", outputConfig)
	}
	if payload["max_tokens"] != float64(4096) {
		t.Errorf("Anthropic max_tokens = %#v, want 4096", payload["max_tokens"])
	}
}

func writeAnthropicEvent(w http.ResponseWriter, event string, payload interface{}) {
	data, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func writeAnthropicStreamingCompletion(w http.ResponseWriter, thinking, content string) {
	w.Header().Set("Content-Type", "text/event-stream")
	writeAnthropicStreamStart(w)
	writeAnthropicThinkingDelta(w, thinking)
	writeAnthropicTextCompletion(w, content)
}

func writeAnthropicStreamStart(w http.ResponseWriter) {
	writeAnthropicEvent(w, "message_start", map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id": "msg_effchat_test", "type": "message", "role": "assistant", "model": "claude-sonnet-4-6",
			"content": []interface{}{}, "stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]interface{}{"input_tokens": 11, "output_tokens": 0},
		},
	})
}

func writeAnthropicThinkingDelta(w http.ResponseWriter, thinking string) {
	writeAnthropicEvent(w, "content_block_start", map[string]interface{}{
		"type": "content_block_start", "index": 0,
		"content_block": map[string]interface{}{"type": "thinking", "thinking": "", "signature": ""},
	})
	writeAnthropicEvent(w, "content_block_delta", map[string]interface{}{
		"type": "content_block_delta", "index": 0,
		"delta": map[string]interface{}{"type": "thinking_delta", "thinking": thinking},
	})
	writeAnthropicEvent(w, "content_block_stop", map[string]interface{}{"type": "content_block_stop", "index": 0})
}

func writeAnthropicTextCompletion(w http.ResponseWriter, content string) {
	writeAnthropicEvent(w, "content_block_start", map[string]interface{}{
		"type": "content_block_start", "index": 1,
		"content_block": map[string]interface{}{"type": "text", "text": "", "citations": nil},
	})
	writeAnthropicEvent(w, "content_block_delta", map[string]interface{}{
		"type": "content_block_delta", "index": 1,
		"delta": map[string]interface{}{"type": "text_delta", "text": content},
	})
	writeAnthropicEvent(w, "content_block_stop", map[string]interface{}{"type": "content_block_stop", "index": 1})
	writeAnthropicEvent(w, "message_delta", map[string]interface{}{
		"type":  "message_delta",
		"delta": map[string]interface{}{"stop_reason": "end_turn", "stop_sequence": nil},
		"usage": map[string]interface{}{"output_tokens": 7},
	})
	writeAnthropicEvent(w, "message_stop", map[string]interface{}{"type": "message_stop"})
}

func writeAnthropicPartialTransportFailure(w http.ResponseWriter, _ *http.Request) {
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
	_, _ = fmt.Fprint(buffered, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_partial\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-6\",\"content\":[],\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":5,\"output_tokens\":0}}}\n\n")
	_, _ = fmt.Fprint(buffered, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\",\"citations\":null}}\n\n")
	_, _ = fmt.Fprint(buffered, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"partial anthropic text\"}}\n\n")
	_ = buffered.Flush()
}

func newAnthropicADKHarness(t *testing.T, provider *scriptedAnthropicProvider) (*adkRunRegressionHarness, *testEnv) {
	t.Helper()
	env := setupTestEnv(t)
	harness := newADKRunRegressionHarnessForProvider(t, env, adkProviderHarnessConfig{
		Adapter: service.AdapterAnthropic, BaseURL: provider.server.URL,
		ModelID: "claude-sonnet-4-6", DisplayName: "Anthropic native ADK regression",
		Reasoning: true, ThinkingFormat: string(modelbank.ThinkingFormatAnthropicAdaptive),
		ThinkingEffort: string(modelbank.ThinkingEffortLow),
	})
	return harness, env
}

func TestAnthropicADKRunStreamsThinkingAndCompletesOnce(t *testing.T) {
	provider := newScriptedAnthropicProvider(t, func(w http.ResponseWriter, r *http.Request) {
		assertAnthropicThinkingRequest(t, r)
		writeAnthropicStreamingCompletion(w, "native thought", "native answer")
	})
	harness, env := newAnthropicADKHarness(t, provider)
	session := harness.createSession(t, "Anthropic native completion")
	runID := "anthropic-native-completion"

	stream := harness.send(t, session.ID, runID, "exercise native Anthropic streaming")
	if stream.Code != http.StatusOK || !strings.Contains(stream.Body.String(), "event: "+streaming.EventThinkingDelta) || !strings.Contains(stream.Body.String(), "event: "+streaming.EventContentDelta) || !strings.Contains(stream.Body.String(), "event: "+streaming.EventMessageComplete) {
		t.Fatalf("Anthropic stream status=%d body=%s", stream.Code, stream.Body.String())
	}
	harness.drainUsage(t)
	if got := provider.calls.Load(); got != 1 {
		t.Fatalf("Anthropic provider calls = %d, want 1", got)
	}
	rows := readADKRunRegressionRows(t, env.db, session.ID, runID)
	if rows.messages != 2 || rows.attempts != 1 || rows.usage != 1 || rows.attemptStatus != repository.AnswerAttemptStatusCompleted || !rows.selected {
		t.Fatalf("Anthropic durable rows = %+v", rows)
	}
}

func TestAnthropicADKRunRetriesOnlyZeroOutputTransientFailure(t *testing.T) {
	provider := newScriptedAnthropicProvider(t,
		func(w http.ResponseWriter, r *http.Request) {
			assertAnthropicThinkingRequest(t, r)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprint(w, `{"type":"error","error":{"type":"api_error","message":"temporarily unavailable"}}`)
		},
		func(w http.ResponseWriter, r *http.Request) {
			assertAnthropicThinkingRequest(t, r)
			writeAnthropicStreamingCompletion(w, "retry thought", "retry answer")
		},
	)
	harness, env := newAnthropicADKHarness(t, provider)
	session := harness.createSession(t, "Anthropic zero-output retry")
	runID := "anthropic-zero-output-retry"

	stream := harness.send(t, session.ID, runID, "retry native transient failure")
	if stream.Code != http.StatusOK || !strings.Contains(stream.Body.String(), "event: "+streaming.EventMessageComplete) {
		t.Fatalf("Anthropic retry status=%d body=%s", stream.Code, stream.Body.String())
	}
	harness.drainUsage(t)
	if got := provider.calls.Load(); got != 2 {
		t.Fatalf("Anthropic provider calls = %d, want 2", got)
	}
	events := runEventsForRegression(t, harness.runHub, runID, session.ID, env.userID)
	if countRunEvents(events, streaming.EventModelRetry) != 1 || countRunEvents(events, streaming.EventAttemptReset) != 0 || countRunEvents(events, streaming.EventMessageComplete) != 1 {
		t.Fatalf("Anthropic retry event ledger = %+v", events)
	}
	rows := readADKRunRegressionRows(t, env.db, session.ID, runID)
	if rows.messages != 2 || rows.attempts != 1 || rows.usage != 2 || rows.attemptStatus != repository.AnswerAttemptStatusCompleted || !rows.selected {
		t.Fatalf("Anthropic retry durable rows = %+v", rows)
	}
}

func TestAnthropicADKRunPartialOutputTransportFailureDoesNotRetry(t *testing.T) {
	provider := newScriptedAnthropicProvider(t, func(w http.ResponseWriter, r *http.Request) {
		assertAnthropicThinkingRequest(t, r)
		writeAnthropicPartialTransportFailure(w, r)
	})
	harness, env := newAnthropicADKHarness(t, provider)
	session := harness.createSession(t, "Anthropic partial transport failure")
	runID := "anthropic-partial-no-retry"

	stream := harness.send(t, session.ID, runID, "preserve native partial output")
	if stream.Code != http.StatusOK || !strings.Contains(stream.Body.String(), "event: "+streaming.EventMessageComplete) || !strings.Contains(stream.Body.String(), `"incomplete":true`) {
		t.Fatalf("Anthropic partial status=%d body=%s", stream.Code, stream.Body.String())
	}
	harness.drainUsage(t)
	if got := provider.calls.Load(); got != 1 {
		t.Fatalf("Anthropic partial provider calls = %d, want 1", got)
	}
	events := runEventsForRegression(t, harness.runHub, runID, session.ID, env.userID)
	if countRunEvents(events, streaming.EventContentDelta) != 1 || countRunEvents(events, streaming.EventModelRetry) != 0 || countRunEvents(events, streaming.EventAttemptReset) != 0 || countRunEvents(events, streaming.EventMessageComplete) != 1 {
		t.Fatalf("Anthropic partial event ledger = %+v", events)
	}
	rows := readADKRunRegressionRows(t, env.db, session.ID, runID)
	if rows.messages != 2 || rows.attempts != 1 || rows.usage != 1 || rows.attemptStatus != repository.AnswerAttemptStatusIncomplete || !rows.selected {
		t.Fatalf("Anthropic partial durable rows = %+v", rows)
	}
}

func TestAnthropicADKRunThinkingDisarmsOnlyFirstOutputTimeout(t *testing.T) {
	provider := newScriptedAnthropicProvider(t, func(w http.ResponseWriter, r *http.Request) {
		assertAnthropicThinkingRequest(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		writeAnthropicStreamStart(w)
		time.Sleep(20 * time.Millisecond)
		writeAnthropicThinkingDelta(w, "first meaningful thought")
		time.Sleep(100 * time.Millisecond)
		writeAnthropicTextCompletion(w, "answer after the startup budget")
	})
	env := setupTestEnv(t)
	harness := newADKRunRegressionHarnessForProvider(t, env, adkProviderHarnessConfig{
		Adapter: service.AdapterAnthropic, BaseURL: provider.server.URL,
		ModelID: "claude-sonnet-4-6", DisplayName: "Anthropic first-output regression",
		Reasoning: true, ThinkingFormat: string(modelbank.ThinkingFormatAnthropicBudget),
		ThinkingEffort: string(modelbank.ThinkingEffortLow), FirstOutputTimeout: 50 * time.Millisecond,
	})
	session := harness.createSession(t, "Anthropic first-output ownership")

	stream := harness.send(t, session.ID, "anthropic-thinking-disarms-timeout", "continue after thinking starts")
	if stream.Code != http.StatusOK || !strings.Contains(stream.Body.String(), "event: "+streaming.EventThinkingDelta) || !strings.Contains(stream.Body.String(), "event: "+streaming.EventMessageComplete) {
		t.Fatalf("Anthropic delayed stream status=%d body=%s", stream.Code, stream.Body.String())
	}
	if strings.Contains(stream.Body.String(), "first_output_timeout") {
		t.Fatalf("first-output timeout remained armed after thinking: %s", stream.Body.String())
	}
}

func TestAnthropicADKRunEmptyMetadataDoesNotDisarmFirstOutputTimeout(t *testing.T) {
	provider := newScriptedAnthropicProvider(t, func(w http.ResponseWriter, r *http.Request) {
		assertAnthropicThinkingRequest(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		writeAnthropicStreamStart(w)
		<-r.Context().Done()
	})
	env := setupTestEnv(t)
	harness := newADKRunRegressionHarnessForProvider(t, env, adkProviderHarnessConfig{
		Adapter: service.AdapterAnthropic, BaseURL: provider.server.URL,
		ModelID: "claude-sonnet-4-6", DisplayName: "Anthropic empty-metadata regression",
		Reasoning: true, ThinkingFormat: string(modelbank.ThinkingFormatAnthropicBudget),
		ThinkingEffort: string(modelbank.ThinkingEffortLow), FirstOutputTimeout: 50 * time.Millisecond,
	})
	session := harness.createSession(t, "Anthropic empty metadata")
	runID := "anthropic-empty-metadata-timeout"

	stream := harness.send(t, session.ID, runID, "metadata is not output")
	if stream.Code != http.StatusOK || !strings.Contains(stream.Body.String(), `"code":"first_output_timeout"`) || strings.Contains(stream.Body.String(), "event: "+streaming.EventModelRetry) {
		t.Fatalf("Anthropic empty-metadata status=%d body=%s", stream.Code, stream.Body.String())
	}
	if got := provider.calls.Load(); got != 1 {
		t.Fatalf("first-output timeout retried provider %d times, want 1", got)
	}
}
