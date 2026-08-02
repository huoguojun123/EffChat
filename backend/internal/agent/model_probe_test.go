package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
	"github.com/huoguojun123/EffChat/internal/testutil"
)

func TestPrepareModelProbeDoesNotRetainCanceledSetupContext(t *testing.T) {
	requestBodies := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		select {
		case requestBodies <- body:
		default:
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-probe\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"probe-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"O\"},\"finish_reason\":null}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-probe\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"probe-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"K\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	einoAgent := NewEinoAgent(service.NewChannelService(nil), nil, 4096, nil, nil, nil, nil, nil, nil)
	req := &ChatRequest{
		ModelID:         "probe-model",
		Provider:        "probe-channel",
		MaxTokens:       64000,
		RuntimeResolved: true,
		RuntimeChannel: &model.AIChannel{
			Key:     "probe-channel",
			Adapter: service.AdapterOpenAICompatible,
			BaseURL: server.URL + "/v1",
			APIKey:  "test-key",
			Enabled: true,
		},
	}
	setupCtx, cancelSetup := context.WithCancel(t.Context())
	prepared, err := einoAgent.PrepareModelProbe(setupCtx, req)
	if err != nil {
		t.Fatalf("PrepareModelProbe() error = %v", err)
	}
	cancelSetup()

	result, err := einoAgent.RunPreparedModelProbe(t.Context(), prepared)
	if err != nil {
		t.Fatalf("RunPreparedModelProbe() error = %v", err)
	}
	if result == nil || result.Output != "OK" || !result.Matched {
		t.Fatalf("probe result = %#v", result)
	}
	var providerRequest map[string]interface{}
	select {
	case body := <-requestBodies:
		if err := json.Unmarshal(body, &providerRequest); err != nil {
			t.Fatalf("decode provider request: %v", err)
		}
	default:
		t.Fatal("provider request was not captured")
	}
	if got, _ := providerRequest["max_tokens"].(float64); int(got) != modelProbeMaxOutputTokens {
		t.Fatalf("provider max_tokens = %v, want %d", providerRequest["max_tokens"], modelProbeMaxOutputTokens)
	}
	if req.MaxTokens != 64000 {
		t.Fatalf("PrepareModelProbe mutated active request MaxTokens = %d", req.MaxTokens)
	}
}

func TestModelProbeRejectsUnexpectedCompleteOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-probe\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"probe-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"NOT OK\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	einoAgent := NewEinoAgent(service.NewChannelService(nil), nil, 4096, nil, nil, nil, nil, nil, nil)
	result, err := einoAgent.TestModel(t.Context(), &ChatRequest{
		ModelID:         "probe-model",
		Provider:        "probe-channel",
		RuntimeResolved: true,
		RuntimeChannel: &model.AIChannel{
			Key:     "probe-channel",
			Adapter: service.AdapterOpenAICompatible,
			BaseURL: server.URL + "/v1",
			APIKey:  "test-key",
			Enabled: true,
		},
	})
	if err != nil {
		t.Fatalf("TestModel() error = %v", err)
	}
	if result == nil || result.Output != "NOT OK" || result.Matched {
		t.Fatalf("probe result = %#v", result)
	}
}

func TestPrepareModelProbeUsesOpenAIResponsesAdapter(t *testing.T) {
	requestBodies := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		requestBodies <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"resp_probe\",\"status\":\"in_progress\",\"model\":\"gpt-5.1\",\"output\":[]}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"item_id\":\"msg_1\",\"output_index\":0,\"content_index\":0,\"delta\":\"OK\",\"logprobs\":[]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":{\"id\":\"resp_probe\",\"status\":\"completed\",\"model\":\"gpt-5.1\",\"output\":[],\"usage\":{\"input_tokens\":3,\"output_tokens\":1,\"total_tokens\":4}}}\n\n")
	}))
	defer server.Close()

	einoAgent := NewEinoAgent(service.NewChannelService(nil), nil, 4096, nil, nil, nil, nil, nil, nil)
	result, err := einoAgent.TestModel(t.Context(), &ChatRequest{
		ModelID:         "gpt-5.1",
		Provider:        "responses-probe",
		RuntimeResolved: true,
		RuntimeChannel: &model.AIChannel{
			Key:     "responses-probe",
			Adapter: service.AdapterOpenAIResponses,
			BaseURL: server.URL + "/v1",
			APIKey:  "test-key",
			Enabled: true,
		},
	})
	if err != nil {
		t.Fatalf("TestModel() error = %v", err)
	}
	if result.Output != "OK" || !result.Matched {
		t.Fatalf("probe result = %#v", result)
	}
	body := <-requestBodies
	if body["max_output_tokens"] != float64(modelProbeMaxOutputTokens) || body["store"] != false {
		t.Fatalf("probe request = %#v", body)
	}
	if _, exists := body["reasoning"]; exists {
		t.Fatalf("minimal probe must suppress reasoning: %#v", body)
	}
}

func TestClassifyModelProbeOutputRequiresExactOK(t *testing.T) {
	tests := []struct {
		name        string
		response    *schema.Message
		wantOutput  string
		wantMatched bool
	}{
		{name: "nil response"},
		{name: "empty", response: schema.AssistantMessage("", nil)},
		{name: "whitespace", response: schema.AssistantMessage("  \n\t", nil)},
		{name: "exact", response: schema.AssistantMessage("OK", nil), wantOutput: "OK", wantMatched: true},
		{name: "trimmed exact", response: schema.AssistantMessage(" \nOK\t", nil), wantOutput: "OK", wantMatched: true},
		{name: "lowercase", response: schema.AssistantMessage("ok", nil), wantOutput: "ok"},
		{name: "negative", response: schema.AssistantMessage("NOT OK", nil), wantOutput: "NOT OK"},
		{name: "extra text", response: schema.AssistantMessage("OK - connected", nil), wantOutput: "OK - connected"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, matched := classifyModelProbeOutput(tt.response)
			if output != tt.wantOutput || matched != tt.wantMatched {
				t.Fatalf("classifyModelProbeOutput() = (%q, %t), want (%q, %t)", output, matched, tt.wantOutput, tt.wantMatched)
			}
		})
	}
}

func TestPreparedModelProbeRejectsMissingLifecycleInputs(t *testing.T) {
	einoAgent := &EinoAgent{}
	if _, err := einoAgent.PrepareModelProbe(nil, &ChatRequest{}); err == nil {
		t.Fatal("PrepareModelProbe accepted a nil setup context")
	}
	if _, err := einoAgent.RunPreparedModelProbe(nil, &PreparedModelProbe{}); err == nil {
		t.Fatal("RunPreparedModelProbe accepted a nil run context")
	}
	if _, err := einoAgent.RunPreparedModelProbe(t.Context(), nil); err == nil {
		t.Fatal("RunPreparedModelProbe accepted a nil prepared probe")
	}
}

func TestPrepareModelProbeBoundsChannelResolution(t *testing.T) {
	db := testutil.OpenPostgresTestDB(t)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	held, err := db.Conn(t.Context())
	if err != nil {
		t.Fatalf("hold database connection: %v", err)
	}
	defer held.Close()
	defer db.Close()

	einoAgent := NewEinoAgent(
		service.NewChannelService(repository.NewChannelRepository(db)),
		nil,
		4096,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	setupCtx, cancelSetup := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancelSetup()
	started := time.Now()
	_, err = einoAgent.PrepareModelProbe(setupCtx, &ChatRequest{
		ModelID:  "blocked-model",
		Provider: "blocked-channel",
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("PrepareModelProbe() error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("channel resolution ignored setup deadline: %v", elapsed)
	}
}
