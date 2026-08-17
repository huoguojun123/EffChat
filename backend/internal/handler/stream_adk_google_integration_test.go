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

// scriptedGoogleProvider keeps the Google native regression at the real
// Gemini Developer API HTTP/SSE boundary. This catches path, credential,
// request-shape and adapter conversion drift that a fake ChatModel would hide.
type scriptedGoogleProvider struct {
	calls  atomic.Int32
	steps  []func(http.ResponseWriter, *http.Request)
	server *httptest.Server
}

func newScriptedGoogleProvider(t *testing.T, steps ...func(http.ResponseWriter, *http.Request)) *scriptedGoogleProvider {
	return newScriptedGoogleProviderForModel(t, "gemini-2.5-pro", steps...)
}

func newScriptedGoogleProviderForModel(t *testing.T, modelID string, steps ...func(http.ResponseWriter, *http.Request)) *scriptedGoogleProvider {
	t.Helper()
	provider := &scriptedGoogleProvider{steps: steps}
	provider.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/"+modelID+":streamGenerateContent" || r.URL.Query().Get("alt") != "sse" {
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

func assertGoogleThinkingRequest(t *testing.T, r *http.Request) {
	t.Helper()
	if got := r.Header.Get("x-goog-api-key"); got != "test-key" {
		t.Errorf("Google x-goog-api-key = %q, want test-key", got)
	}
	var payload map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		t.Errorf("decode Google request: %v", err)
		return
	}
	generation, _ := payload["generationConfig"].(map[string]interface{})
	thinking, _ := generation["thinkingConfig"].(map[string]interface{})
	if thinking["includeThoughts"] != true || thinking["thinkingBudget"] != float64(1024) {
		t.Errorf("Google thinking request = %#v", thinking)
	}
	contents, _ := payload["contents"].([]interface{})
	if len(contents) == 0 {
		t.Errorf("Google contents request = %#v", payload["contents"])
	}
}

func writeGoogleEvent(w http.ResponseWriter, payload interface{}) {
	data, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func writeGoogleMetadata(w http.ResponseWriter) {
	writeGoogleEvent(w, map[string]interface{}{
		"candidates": []interface{}{map[string]interface{}{
			"content": map[string]interface{}{"role": "model", "parts": []interface{}{}},
			"index":   0,
		}},
	})
}

func writeGoogleThinking(w http.ResponseWriter, thought string) {
	writeGoogleEvent(w, map[string]interface{}{
		"candidates": []interface{}{map[string]interface{}{
			"content": map[string]interface{}{"role": "model", "parts": []interface{}{map[string]interface{}{"text": thought, "thought": true}}},
			"index":   0,
		}},
	})
}

func writeGoogleTextCompletion(w http.ResponseWriter, content string) {
	writeGoogleEvent(w, map[string]interface{}{
		"candidates": []interface{}{map[string]interface{}{
			"content":      map[string]interface{}{"role": "model", "parts": []interface{}{map[string]interface{}{"text": content}}},
			"finishReason": "STOP",
			"index":        0,
		}},
		"usageMetadata": map[string]interface{}{
			"promptTokenCount": 11, "candidatesTokenCount": 7, "thoughtsTokenCount": 3, "totalTokenCount": 21,
		},
	})
}

func writeGoogleStreamingCompletion(w http.ResponseWriter, thought, content string) {
	w.Header().Set("Content-Type", "text/event-stream")
	writeGoogleMetadata(w)
	writeGoogleThinking(w, thought)
	writeGoogleTextCompletion(w, content)
}

func writeGooglePartialTransportFailure(w http.ResponseWriter, _ *http.Request) {
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
	_, _ = fmt.Fprint(buffered, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"partial google text\"}]},\"index\":0}]}\n\n")
	_ = buffered.Flush()
}

func newGoogleADKHarness(t *testing.T, provider *scriptedGoogleProvider) (*adkRunRegressionHarness, *testEnv) {
	return newGoogleADKHarnessForModel(t, provider, "gemini-2.5-pro", nil)
}

func newGoogleADKHarnessForModel(t *testing.T, provider *scriptedGoogleProvider, modelID string, temperature *float64) (*adkRunRegressionHarness, *testEnv) {
	t.Helper()
	env := setupTestEnv(t)
	temperaturePolicy := ""
	if temperature != nil {
		temperaturePolicy = "fixed"
	}
	harness := newADKRunRegressionHarnessForProvider(t, env, adkProviderHarnessConfig{
		Adapter: service.AdapterGoogle, BaseURL: provider.server.URL,
		ModelID: modelID, DisplayName: "Google native ADK regression",
		Reasoning: true, ThinkingFormat: string(modelbank.ThinkingFormatGeminiThinking),
		ThinkingEffort: string(modelbank.ThinkingEffortLow), TemperaturePolicy: temperaturePolicy, TemperatureValue: temperature,
	})
	return harness, env
}

func TestGoogleADKRunUsesGemini37RequestContract(t *testing.T) {
	provider := newScriptedGoogleProviderForModel(t, "gemini-3.7-flash", func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode Google request: %v", err)
		}
		generation, _ := payload["generationConfig"].(map[string]interface{})
		if _, ok := generation["temperature"]; ok {
			t.Fatalf("Gemini 3.7 request must omit temperature: %#v", generation)
		}
		thinking, _ := generation["thinkingConfig"].(map[string]interface{})
		if thinking["includeThoughts"] != true || thinking["thinkingLevel"] != "LOW" {
			t.Fatalf("Gemini 3.7 thinking request = %#v", thinking)
		}
		if _, ok := thinking["thinkingBudget"]; ok {
			t.Fatalf("Gemini 3.7 request must omit thinkingBudget: %#v", thinking)
		}
		writeGoogleStreamingCompletion(w, "native thought", "native answer")
	})
	temperature := 0.7
	harness, _ := newGoogleADKHarnessForModel(t, provider, "gemini-3.7-flash", &temperature)
	session := harness.createSession(t, "Gemini 3.7 request contract")
	stream := harness.send(t, session.ID, "google-37-contract", "exercise Gemini 3.7 request fields")
	if stream.Code != http.StatusOK || !strings.Contains(stream.Body.String(), "event: "+streaming.EventMessageComplete) {
		t.Fatalf("Google stream status=%d body=%s", stream.Code, stream.Body.String())
	}
}

func TestGoogleADKRunStreamsThinkingAndCompletesOnce(t *testing.T) {
	provider := newScriptedGoogleProvider(t, func(w http.ResponseWriter, r *http.Request) {
		assertGoogleThinkingRequest(t, r)
		writeGoogleStreamingCompletion(w, "native thought", "native answer")
	})
	harness, env := newGoogleADKHarness(t, provider)
	session := harness.createSession(t, "Google native completion")
	runID := "google-native-completion"

	stream := harness.send(t, session.ID, runID, "exercise native Google streaming")
	if stream.Code != http.StatusOK || !strings.Contains(stream.Body.String(), "event: "+streaming.EventThinkingDelta) || !strings.Contains(stream.Body.String(), "event: "+streaming.EventContentDelta) || !strings.Contains(stream.Body.String(), "event: "+streaming.EventMessageComplete) {
		t.Fatalf("Google stream status=%d body=%s", stream.Code, stream.Body.String())
	}
	harness.drainUsage(t)
	if got := provider.calls.Load(); got != 1 {
		t.Fatalf("Google provider calls = %d, want 1", got)
	}
	rows := readADKRunRegressionRows(t, env.db, session.ID, runID)
	if rows.messages != 2 || rows.attempts != 1 || rows.usage != 1 || rows.attemptStatus != repository.AnswerAttemptStatusCompleted || !rows.selected {
		t.Fatalf("Google durable rows = %+v", rows)
	}
}

func TestGoogleADKRunRetriesOnlyZeroOutputTransientFailure(t *testing.T) {
	provider := newScriptedGoogleProvider(t,
		func(w http.ResponseWriter, r *http.Request) {
			assertGoogleThinkingRequest(t, r)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprint(w, `{"error":{"code":503,"message":"temporarily unavailable","status":"UNAVAILABLE"}}`)
		},
		func(w http.ResponseWriter, r *http.Request) {
			assertGoogleThinkingRequest(t, r)
			writeGoogleStreamingCompletion(w, "retry thought", "retry answer")
		},
	)
	harness, env := newGoogleADKHarness(t, provider)
	session := harness.createSession(t, "Google zero-output retry")
	runID := "google-zero-output-retry"

	stream := harness.send(t, session.ID, runID, "retry native transient failure")
	if stream.Code != http.StatusOK || !strings.Contains(stream.Body.String(), "event: "+streaming.EventMessageComplete) {
		t.Fatalf("Google retry status=%d body=%s", stream.Code, stream.Body.String())
	}
	harness.drainUsage(t)
	if got := provider.calls.Load(); got != 2 {
		t.Fatalf("Google provider calls = %d, want 2", got)
	}
	events := runEventsForRegression(t, harness.runHub, runID, session.ID, env.userID)
	if countRunEvents(events, streaming.EventModelRetry) != 1 || countRunEvents(events, streaming.EventAttemptReset) != 0 || countRunEvents(events, streaming.EventMessageComplete) != 1 {
		t.Fatalf("Google retry event ledger = %+v", events)
	}
	rows := readADKRunRegressionRows(t, env.db, session.ID, runID)
	if rows.messages != 2 || rows.attempts != 1 || rows.usage != 2 || rows.attemptStatus != repository.AnswerAttemptStatusCompleted || !rows.selected {
		t.Fatalf("Google retry durable rows = %+v", rows)
	}
}

func TestGoogleADKRunPartialOutputTransportFailureDoesNotRetry(t *testing.T) {
	provider := newScriptedGoogleProvider(t, func(w http.ResponseWriter, r *http.Request) {
		assertGoogleThinkingRequest(t, r)
		writeGooglePartialTransportFailure(w, r)
	})
	harness, env := newGoogleADKHarness(t, provider)
	session := harness.createSession(t, "Google partial transport failure")
	runID := "google-partial-no-retry"

	stream := harness.send(t, session.ID, runID, "preserve native partial output")
	if stream.Code != http.StatusOK || !strings.Contains(stream.Body.String(), "event: "+streaming.EventMessageComplete) || !strings.Contains(stream.Body.String(), `"incomplete":true`) {
		t.Fatalf("Google partial status=%d body=%s", stream.Code, stream.Body.String())
	}
	harness.drainUsage(t)
	if got := provider.calls.Load(); got != 1 {
		t.Fatalf("Google partial provider calls = %d, want 1", got)
	}
	events := runEventsForRegression(t, harness.runHub, runID, session.ID, env.userID)
	if countRunEvents(events, streaming.EventContentDelta) != 1 || countRunEvents(events, streaming.EventModelRetry) != 0 || countRunEvents(events, streaming.EventAttemptReset) != 0 || countRunEvents(events, streaming.EventMessageComplete) != 1 {
		t.Fatalf("Google partial event ledger = %+v", events)
	}
	rows := readADKRunRegressionRows(t, env.db, session.ID, runID)
	if rows.messages != 2 || rows.attempts != 1 || rows.usage != 1 || rows.attemptStatus != repository.AnswerAttemptStatusIncomplete || !rows.selected {
		t.Fatalf("Google partial durable rows = %+v", rows)
	}
}

func TestGoogleADKRunThinkingDisarmsOnlyFirstOutputTimeout(t *testing.T) {
	provider := newScriptedGoogleProvider(t, func(w http.ResponseWriter, r *http.Request) {
		assertGoogleThinkingRequest(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		writeGoogleMetadata(w)
		time.Sleep(20 * time.Millisecond)
		writeGoogleThinking(w, "first meaningful thought")
		time.Sleep(100 * time.Millisecond)
		writeGoogleTextCompletion(w, "answer after the startup budget")
	})
	env := setupTestEnv(t)
	harness := newADKRunRegressionHarnessForProvider(t, env, adkProviderHarnessConfig{
		Adapter: service.AdapterGoogle, BaseURL: provider.server.URL,
		ModelID: "gemini-2.5-pro", DisplayName: "Google first-output regression",
		Reasoning: true, ThinkingFormat: string(modelbank.ThinkingFormatGeminiThinking),
		ThinkingEffort: string(modelbank.ThinkingEffortLow), FirstOutputTimeout: 50 * time.Millisecond,
	})
	session := harness.createSession(t, "Google first-output ownership")

	stream := harness.send(t, session.ID, "google-thinking-disarms-timeout", "continue after thinking starts")
	if stream.Code != http.StatusOK || !strings.Contains(stream.Body.String(), "event: "+streaming.EventThinkingDelta) || !strings.Contains(stream.Body.String(), "event: "+streaming.EventMessageComplete) {
		t.Fatalf("Google delayed stream status=%d body=%s", stream.Code, stream.Body.String())
	}
	if strings.Contains(stream.Body.String(), "first_output_timeout") {
		t.Fatalf("first-output timeout remained armed after thinking: %s", stream.Body.String())
	}
}

func TestGoogleADKRunEmptyMetadataDoesNotDisarmFirstOutputTimeout(t *testing.T) {
	provider := newScriptedGoogleProvider(t, func(w http.ResponseWriter, r *http.Request) {
		assertGoogleThinkingRequest(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		writeGoogleMetadata(w)
		<-r.Context().Done()
	})
	env := setupTestEnv(t)
	harness := newADKRunRegressionHarnessForProvider(t, env, adkProviderHarnessConfig{
		Adapter: service.AdapterGoogle, BaseURL: provider.server.URL,
		ModelID: "gemini-2.5-pro", DisplayName: "Google empty-metadata regression",
		Reasoning: true, ThinkingFormat: string(modelbank.ThinkingFormatGeminiThinking),
		ThinkingEffort: string(modelbank.ThinkingEffortLow), FirstOutputTimeout: 50 * time.Millisecond,
	})
	session := harness.createSession(t, "Google empty metadata")
	runID := "google-empty-metadata-timeout"

	stream := harness.send(t, session.ID, runID, "metadata is not output")
	if stream.Code != http.StatusOK || !strings.Contains(stream.Body.String(), `"code":"first_output_timeout"`) || strings.Contains(stream.Body.String(), "event: "+streaming.EventModelRetry) {
		t.Fatalf("Google empty-metadata status=%d body=%s", stream.Code, stream.Body.String())
	}
	if got := provider.calls.Load(); got != 1 {
		t.Fatalf("first-output timeout retried provider %d times, want 1", got)
	}
}
