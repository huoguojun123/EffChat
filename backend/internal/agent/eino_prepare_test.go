package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/modelstream"
	"github.com/huoguojun123/EffChat/internal/service"
	"github.com/huoguojun123/EffChat/pkg/streaming"
)

type preparedChatEventWriter struct {
	mu     sync.Mutex
	events []string
}

func TestBuildChatModelUsesOpenAIResponsesProtocol(t *testing.T) {
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
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"resp_agent\",\"status\":\"in_progress\",\"model\":\"gpt-5.1\",\"output\":[]}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"item_id\":\"msg_1\",\"output_index\":0,\"content_index\":0,\"delta\":\"responses ready\",\"logprobs\":[]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":{\"id\":\"resp_agent\",\"status\":\"completed\",\"model\":\"gpt-5.1\",\"output\":[],\"usage\":{\"input_tokens\":3,\"output_tokens\":2,\"total_tokens\":5}}}\n\n")
	}))
	defer server.Close()

	a := NewEinoAgent(service.NewChannelService(nil), nil, 4096, nil, nil, nil, nil, nil, nil)
	chatModel, err := a.buildChatModel(t.Context(), &ChatRequest{
		ModelID:        "gpt-5.1",
		Provider:       "responses-channel",
		MaxTokens:      321,
		Reasoning:      true,
		ThinkingFormat: string(modelbank.ThinkingFormatOpenAIReasoningEffort),
		ThinkingEffort: string(modelbank.ThinkingEffortHigh),
		RuntimeChannel: &model.AIChannel{
			Key:     "responses-channel",
			Adapter: service.AdapterOpenAIResponses,
			BaseURL: server.URL + "/v1",
			APIKey:  "test-key",
			Enabled: true,
		},
	}, modelbank.SearchDecision{})
	if err != nil {
		t.Fatalf("buildChatModel() error = %v", err)
	}
	result, err := modelstream.Collect(t.Context(), chatModel, []*schema.Message{schema.UserMessage("hello")}, time.Second)
	if err != nil {
		t.Fatalf("collect Responses stream: %v", err)
	}
	if result.Content != "responses ready" {
		t.Fatalf("content = %q", result.Content)
	}
	body := <-requestBodies
	if body["max_output_tokens"] != float64(321) || body["store"] != false {
		t.Fatalf("request limits/state = %#v", body)
	}
	reasoning, _ := body["reasoning"].(map[string]any)
	if reasoning["effort"] != "high" || reasoning["summary"] != "auto" {
		t.Fatalf("reasoning = %#v", reasoning)
	}
	if _, exists := body["previous_response_id"]; exists {
		t.Fatalf("request must remain stateless: %#v", body)
	}
}

func (w *preparedChatEventWriter) WriteEvent(event string, _ interface{}) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.events = append(w.events, event)
	return nil
}

func (w *preparedChatEventWriter) hasEvent(event string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, recorded := range w.events {
		if recorded == event {
			return true
		}
	}
	return false
}

func TestPrepareChatDoesNotRetainCanceledSetupContext(t *testing.T) {
	providerCalled := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		select {
		case providerCalled <- struct{}{}:
		default:
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-setup-boundary\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"setup-boundary-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"durable output\"},\"finish_reason\":null}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-setup-boundary\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"setup-boundary-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	einoAgent := NewEinoAgent(
		service.NewChannelService(nil),
		nil,
		4096,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	req := &ChatRequest{
		UserID:          11,
		SessionID:       22,
		ModelID:         "setup-boundary-model",
		Provider:        "setup-boundary-channel",
		MaxTokens:       128,
		ContextWindow:   4096,
		ModelMaxOutput:  128,
		RuntimeResolved: true,
		RuntimeChannel: &model.AIChannel{
			Key:     "setup-boundary-channel",
			Adapter: service.AdapterOpenAICompatible,
			BaseURL: server.URL + "/v1",
			APIKey:  "test-key",
			Enabled: true,
		},
		RuntimeToolConfig: service.ToolRuntimeConfigSet{},
		Messages: []*model.Message{{
			MessageData: []byte(`{"role":"user","content":"hello"}`),
		}},
	}
	writer := &preparedChatEventWriter{}
	setupCtx, cancelSetup := context.WithCancel(t.Context())
	prepared, err := einoAgent.PrepareChat(setupCtx, req, writer)
	if err != nil {
		t.Fatalf("PrepareChat() error = %v", err)
	}
	cancelSetup()

	resp, err := einoAgent.RunPreparedChat(t.Context(), prepared)
	if err != nil {
		t.Fatalf("RunPreparedChat() error = %v", err)
	}
	select {
	case <-providerCalled:
	default:
		t.Fatal("provider stream was not started with the durable context")
	}
	if resp == nil || len(resp.Messages) != 1 || resp.Messages[0]["content"] != "durable output" {
		t.Fatalf("response = %#v", resp)
	}
	if !writer.hasEvent(streaming.EventContentDelta) {
		t.Fatalf("stream events = %#v", writer.events)
	}
}

func TestStreamChatCompatibilityWrapperUsesSingleContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-wrapper\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"wrapper-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"wrapper output\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	einoAgent := NewEinoAgent(service.NewChannelService(nil), nil, 4096, nil, nil, nil, nil, nil, nil)
	resp, err := einoAgent.StreamChat(t.Context(), &ChatRequest{
		ModelID:         "wrapper-model",
		Provider:        "wrapper-channel",
		MaxTokens:       64,
		ContextWindow:   4096,
		ModelMaxOutput:  64,
		RuntimeResolved: true,
		RuntimeChannel: &model.AIChannel{
			Key:     "wrapper-channel",
			Adapter: service.AdapterOpenAICompatible,
			BaseURL: server.URL + "/v1",
			APIKey:  "test-key",
			Enabled: true,
		},
		RuntimeToolConfig: service.ToolRuntimeConfigSet{},
		Messages: []*model.Message{{
			MessageData: []byte(`{"role":"user","content":"hello"}`),
		}},
	}, &preparedChatEventWriter{})
	if err != nil {
		t.Fatalf("StreamChat() error = %v", err)
	}
	if resp == nil || len(resp.Messages) != 1 || resp.Messages[0]["content"] != "wrapper output" {
		t.Fatalf("response = %#v", resp)
	}
}

func TestPreparedChatRejectsMissingLifecycleInputs(t *testing.T) {
	einoAgent := &EinoAgent{}
	if _, err := einoAgent.PrepareChat(nil, &ChatRequest{}, &preparedChatEventWriter{}); err == nil {
		t.Fatal("PrepareChat accepted a nil setup context")
	}
	if _, err := einoAgent.PrepareChat(t.Context(), &ChatRequest{}, nil); err == nil {
		t.Fatal("PrepareChat accepted a nil event writer")
	}
	if _, err := einoAgent.RunPreparedChat(nil, &PreparedChatRun{}); err == nil {
		t.Fatal("RunPreparedChat accepted a nil durable context")
	}
}

func TestPrepareCompactionDoesNotRetainCanceledSetupContext(t *testing.T) {
	providerCalled := make(chan struct{}, 1)
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
		select {
		case providerCalled <- struct{}{}:
		default:
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-compaction-boundary\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"deepseek-ai/DeepSeek-V4-Flash\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"<think>内部推理\"},\"finish_reason\":null}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-compaction-boundary\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"deepseek-ai/DeepSeek-V4-Flash\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"过程</think>to be written in Chinese</analysis>\\n\\n<summary>继续上下文</summary>\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	einoAgent := NewEinoAgent(service.NewChannelService(nil), nil, 4096, nil, nil, nil, nil, nil, nil)
	req := &ChatRequest{
		ModelID:         "deepseek-ai/DeepSeek-V4-Flash",
		Provider:        "compaction-boundary-channel",
		MaxTokens:       64000,
		ContextWindow:   128000,
		ModelMaxOutput:  64000,
		RuntimeResolved: true,
		RuntimeChannel: &model.AIChannel{
			Key:     "compaction-boundary-channel",
			Adapter: service.AdapterOpenAICompatible,
			BaseURL: server.URL + "/v1",
			APIKey:  "test-key",
			Enabled: true,
		},
		Messages: []*model.Message{{
			ID:          41,
			MessageData: []byte(`{"role":"user","content":"需要压缩的历史"}`),
		}},
	}
	setupCtx, cancelSetup := context.WithCancel(t.Context())
	prepared, err := einoAgent.PrepareCompaction(setupCtx, req)
	if err != nil {
		t.Fatalf("PrepareCompaction() error = %v", err)
	}
	cancelSetup()

	checkpoint, err := einoAgent.RunPreparedCompaction(t.Context(), prepared)
	if err != nil {
		t.Fatalf("RunPreparedCompaction() error = %v", err)
	}
	select {
	case <-providerCalled:
	default:
		t.Fatal("compaction provider stream was not started with the durable context")
	}
	var providerRequest map[string]interface{}
	select {
	case body := <-requestBodies:
		if err := json.Unmarshal(body, &providerRequest); err != nil {
			t.Fatalf("decode provider request: %v", err)
		}
	default:
		t.Fatal("compaction provider request body was not captured")
	}
	if got, _ := providerRequest["max_tokens"].(float64); int(got) != compactionMaxOutputTokens {
		t.Fatalf("provider max_tokens = %v, want %d", providerRequest["max_tokens"], compactionMaxOutputTokens)
	}
	thinking, ok := providerRequest["thinking"].(map[string]interface{})
	if !ok || thinking["type"] != "disabled" {
		t.Fatalf("provider thinking = %#v, want disabled", providerRequest["thinking"])
	}
	if checkpoint == nil || checkpoint.CompressBefore != 42 || checkpoint.Provider != req.Provider || checkpoint.ModelID != req.ModelID {
		t.Fatalf("checkpoint = %#v", checkpoint)
	}
	if !strings.Contains(string(checkpoint.SummaryData), "继续上下文") {
		t.Fatalf("summary data = %s", checkpoint.SummaryData)
	}
	if strings.Contains(string(checkpoint.SummaryData), "内部推理") {
		t.Fatalf("summary data retained inline thinking: %s", checkpoint.SummaryData)
	}
	if strings.Contains(string(checkpoint.SummaryData), "to be written in Chinese") || strings.Contains(string(checkpoint.SummaryData), "analysis>") || strings.Contains(string(checkpoint.SummaryData), "summary>") {
		t.Fatalf("summary data retained provider reasoning envelope: %s", checkpoint.SummaryData)
	}
	if req.MaxTokens != 64000 {
		t.Fatalf("PrepareCompaction mutated the active request MaxTokens = %d", req.MaxTokens)
	}
	if req.SuppressThinking {
		t.Fatal("PrepareCompaction mutated the active request SuppressThinking")
	}
}

func TestPreparedCompactionRejectsMissingLifecycleInputs(t *testing.T) {
	einoAgent := &EinoAgent{}
	if _, err := einoAgent.PrepareCompaction(nil, &ChatRequest{}); err == nil {
		t.Fatal("PrepareCompaction accepted a nil setup context")
	}
	if _, err := einoAgent.PrepareCompaction(t.Context(), nil); err == nil {
		t.Fatal("PrepareCompaction accepted a nil request")
	}
	if _, err := einoAgent.RunPreparedCompaction(nil, &PreparedCompactionRun{}); err == nil {
		t.Fatal("RunPreparedCompaction accepted a nil durable context")
	}
}
