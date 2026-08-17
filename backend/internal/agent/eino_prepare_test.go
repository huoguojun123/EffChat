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

func TestBuildChatModelAppliesTemperatureRequestProfile(t *testing.T) {
	requestBodies := make(chan map[string]any, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		requestBodies <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-temperature\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":null}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	requested := 0.25
	fixed := 1.0
	cases := []struct {
		name        string
		policy      string
		fixed       *float64
		wantValue   float64
		wantPresent bool
	}{
		{name: "configurable", policy: model.TemperaturePolicyConfigurable, wantValue: requested, wantPresent: true},
		{name: "omit", policy: model.TemperaturePolicyOmit},
		{name: "fixed", policy: model.TemperaturePolicyFixed, fixed: &fixed, wantValue: fixed, wantPresent: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := NewEinoAgent(service.NewChannelService(nil), nil, 4096, nil, nil, nil, nil, nil, nil)
			chatModel, err := a.buildChatModel(t.Context(), &ChatRequest{
				ModelID: "fixture", Provider: "fixture-channel", Temperature: &requested,
				TemperaturePolicy: tc.policy, TemperatureValue: tc.fixed,
				RuntimeChannel: &model.AIChannel{Key: "fixture-channel", Adapter: service.AdapterOpenAICompatible, BaseURL: server.URL + "/v1", APIKey: "test-key", Enabled: true},
			}, modelbank.SearchDecision{})
			if err != nil {
				t.Fatalf("build model: %v", err)
			}
			if _, err := modelstream.Collect(t.Context(), chatModel, []*schema.Message{schema.UserMessage("hello")}, time.Second); err != nil {
				t.Fatalf("collect stream: %v", err)
			}
			body := <-requestBodies
			value, present := body["temperature"].(float64)
			if present != tc.wantPresent || (present && value != tc.wantValue) {
				t.Fatalf("temperature = %v (present=%t), want %v (present=%t); body=%#v", value, present, tc.wantValue, tc.wantPresent, body)
			}
		})
	}
}

func TestBuildChatModelAppliesTypedOpenAIRequestProfile(t *testing.T) {
	requestBodies := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		requestBodies <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-profile\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":null}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	temperature, topP, presence, frequency := 1.0, 1.0, 0.0, 0.0
	n := 1
	a := NewEinoAgent(service.NewChannelService(nil), nil, 4096, nil, nil, nil, nil, nil, nil)
	chatModel, err := a.buildChatModel(t.Context(), &ChatRequest{
		ModelID: "fixture", Provider: "fixture-channel",
		TemperaturePolicy: model.TemperaturePolicyFixed, TemperatureValue: &temperature,
		OpenAIRequestProfile: model.OpenAIRequestProfile{
			TopP: &topP, N: &n, PresencePenalty: &presence, FrequencyPenalty: &frequency,
		},
		RuntimeChannel: &model.AIChannel{Key: "fixture-channel", Adapter: service.AdapterOpenAICompatible, BaseURL: server.URL + "/v1", APIKey: "test-key", Enabled: true},
	}, modelbank.SearchDecision{})
	if err != nil {
		t.Fatalf("build model: %v", err)
	}
	if _, err := modelstream.Collect(t.Context(), chatModel, []*schema.Message{schema.UserMessage("hello")}, time.Second); err != nil {
		t.Fatalf("collect stream: %v", err)
	}
	body := <-requestBodies
	for key, want := range map[string]float64{
		"temperature": 1, "top_p": 1, "n": 1, "presence_penalty": 0, "frequency_penalty": 0,
	} {
		if got, ok := body[key].(float64); !ok || got != want {
			t.Fatalf("%s = %#v, want %v; body=%#v", key, body[key], want, body)
		}
	}
}

func TestBuildChatModelOmitsUnsupportedGrokReasoningPenalties(t *testing.T) {
	requestBodies := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		requestBodies <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-grok\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"grok-4.6\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":null}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	presence, frequency := 0.5, -0.5
	a := NewEinoAgent(service.NewChannelService(nil), nil, 4096, nil, nil, nil, nil, nil, nil)
	chatModel, err := a.buildChatModel(t.Context(), &ChatRequest{
		ModelID: "grok-4.6", Provider: "xai", Reasoning: true, ThinkingEffort: "xhigh",
		OpenAIRequestProfile: model.OpenAIRequestProfile{
			PresencePenalty: &presence, FrequencyPenalty: &frequency,
		},
		RuntimeChannel: &model.AIChannel{Key: "xai", Adapter: service.AdapterOpenAICompatible, BaseURL: server.URL + "/v1", APIKey: "test-key", Enabled: true},
	}, modelbank.SearchDecision{})
	if err != nil {
		t.Fatalf("build model: %v", err)
	}
	if _, err := modelstream.Collect(t.Context(), chatModel, []*schema.Message{schema.UserMessage("hello")}, time.Second); err != nil {
		t.Fatalf("collect stream: %v", err)
	}
	body := <-requestBodies
	if body["reasoning_effort"] != "xhigh" {
		t.Fatalf("reasoning_effort = %#v, want xhigh; body=%#v", body["reasoning_effort"], body)
	}
	for _, key := range []string{"presence_penalty", "frequency_penalty", "stop"} {
		if _, ok := body[key]; ok {
			t.Fatalf("unsupported Grok reasoning field %q leaked: %#v", key, body)
		}
	}
}

func TestBuildChatModelPreservesDeepSeekReasoningForToolContinuation(t *testing.T) {
	requestBodies := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		requestBodies <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-deepseek\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"deepseek-v4-flash\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"done\"},\"finish_reason\":null}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	a := NewEinoAgent(service.NewChannelService(nil), nil, 4096, nil, nil, nil, nil, nil, nil)
	chatModel, err := a.buildChatModel(t.Context(), &ChatRequest{
		ModelID: "deepseek-v4-flash", Provider: "deepseek", Reasoning: true, ThinkingEffort: "low",
		RuntimeChannel: &model.AIChannel{Key: "deepseek", Adapter: service.AdapterOpenAICompatible, BaseURL: server.URL + "/v1", APIKey: "test-key", Enabled: true},
	}, modelbank.SearchDecision{})
	if err != nil {
		t.Fatalf("build model: %v", err)
	}
	idx := 0
	messages := []*schema.Message{
		schema.UserMessage("look it up"),
		{
			Role: schema.Assistant, ReasoningContent: "I should use the tool",
			ToolCalls: []schema.ToolCall{{
				Index: &idx, ID: "call_1", Type: "function",
				Function: schema.FunctionCall{Name: "web_search", Arguments: `{"query":"fixture"}`},
			}},
		},
		{Role: schema.Tool, Content: `{"result":"ok"}`, ToolCallID: "call_1", ToolName: "web_search"},
	}
	if _, err := modelstream.Collect(t.Context(), chatModel, messages, time.Second); err != nil {
		t.Fatalf("collect stream: %v", err)
	}
	body := <-requestBodies
	wireMessages, ok := body["messages"].([]any)
	if !ok || len(wireMessages) != 3 {
		t.Fatalf("messages = %#v, want three wire messages", body["messages"])
	}
	assistant, ok := wireMessages[1].(map[string]any)
	if !ok || assistant["reasoning_content"] != "I should use the tool" {
		t.Fatalf("assistant tool continuation lost reasoning_content: %#v", wireMessages[1])
	}
	tool, ok := wireMessages[2].(map[string]any)
	if !ok || tool["tool_call_id"] != "call_1" {
		t.Fatalf("tool result lost call id: %#v", wireMessages[2])
	}
}

func TestBuildChatModelAppliesQwenThinkingAndNativeSearch(t *testing.T) {
	requestBodies := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		requestBodies <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-qwen\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"qwen3.7-plus\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":null}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	a := NewEinoAgent(service.NewChannelService(nil), nil, 4096, nil, nil, nil, nil, nil, nil)
	chatModel, err := a.buildChatModel(t.Context(), &ChatRequest{
		ModelID: "qwen3.7-plus", Provider: "qwen", Reasoning: true, ThinkingEffort: "high",
		RuntimeChannel: &model.AIChannel{Key: "qwen", Adapter: service.AdapterOpenAICompatible, BaseURL: server.URL + "/v1", APIKey: "test-key", Enabled: true},
	}, modelbank.SearchDecision{UseModelNativeSearch: true, SearchImpl: modelbank.SearchImplParams})
	if err != nil {
		t.Fatalf("build model: %v", err)
	}
	if _, err := modelstream.Collect(t.Context(), chatModel, []*schema.Message{schema.UserMessage("hello")}, time.Second); err != nil {
		t.Fatalf("collect stream: %v", err)
	}
	body := <-requestBodies
	for key, want := range map[string]any{
		"enable_thinking":   true,
		"thinking_budget":   float64(8192),
		"preserve_thinking": true,
		"enable_search":     true,
	} {
		if body[key] != want {
			t.Fatalf("%s = %#v, want %#v; body=%#v", key, body[key], want, body)
		}
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
	//lint:ignore SA1012 nil is the lifecycle boundary under test.
	if _, err := einoAgent.PrepareChat(nil, &ChatRequest{}, &preparedChatEventWriter{}); err == nil {
		t.Fatal("PrepareChat accepted a nil setup context")
	}
	if _, err := einoAgent.PrepareChat(t.Context(), &ChatRequest{}, nil); err == nil {
		t.Fatal("PrepareChat accepted a nil event writer")
	}
	//lint:ignore SA1012 nil is the lifecycle boundary under test.
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
		var request struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(body, &request)
		response := "<summary>用户要求继续处理虚构历史。</summary>"
		systemExcludesMarker := false
		for _, message := range request.Messages {
			if message.Role == "system" && strings.Contains(message.Content, "Do not mention, quote, reproduce, explain, or otherwise include the final control marker") {
				systemExcludesMarker = true
			}
			if message.Role == "user" && strings.Contains(message.Content, "Create a detailed continuation summary") {
				response = "<summary>用户要求生成详细延续总结并使用七节格式。</summary>"
			}
		}
		if !systemExcludesMarker {
			response = "<summary>用户要求继续处理虚构历史。最新的 <effchat_compaction_request /> 是应用控制标记。</summary>"
		}
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
		encodedResponse, _ := json.Marshal("过程</think>to be written in Chinese</analysis>\n\n" + response)
		_, _ = fmt.Fprintf(w, "data: {\"id\":\"chatcmpl-compaction-boundary\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"deepseek-ai/DeepSeek-V4-Flash\",\"choices\":[{\"index\":0,\"delta\":{\"content\":%s},\"finish_reason\":\"stop\"}]}\n\n", encodedResponse)
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
	messages, ok := providerRequest["messages"].([]interface{})
	if !ok || len(messages) != 3 {
		t.Fatalf("provider messages = %#v, want system + source history + control marker", providerRequest["messages"])
	}
	messageAt := func(index int) map[string]interface{} {
		message, _ := messages[index].(map[string]interface{})
		return message
	}
	if first := messageAt(0); first["role"] != "system" || !strings.Contains(fmt.Sprint(first["content"]), compactionInstruction) {
		t.Fatalf("compaction contract must be a system instruction: %#v", first)
	}
	if source := messageAt(1); source["role"] != "user" || source["content"] != "需要压缩的历史" {
		t.Fatalf("source conversation changed: %#v", source)
	}
	if marker := messageAt(2); marker["role"] != "user" || marker["content"] != compactionRequestMarker {
		t.Fatalf("final compaction marker = %#v", marker)
	}
	for _, raw := range messages[1:] {
		message, _ := raw.(map[string]interface{})
		if strings.Contains(fmt.Sprint(message["content"]), "Create a detailed continuation summary") {
			t.Fatalf("compaction contract leaked into source/user messages: %#v", message)
		}
	}
	if checkpoint == nil || checkpoint.CompressBefore != 42 || checkpoint.Provider != req.Provider || checkpoint.ModelID != req.ModelID {
		t.Fatalf("checkpoint = %#v", checkpoint)
	}
	if !strings.Contains(string(checkpoint.SummaryData), "继续处理虚构历史") {
		t.Fatalf("summary data = %s", checkpoint.SummaryData)
	}
	if strings.Contains(string(checkpoint.SummaryData), "用户要求生成详细延续总结") || strings.Contains(string(checkpoint.SummaryData), "七节格式") {
		t.Fatalf("summary attributed application control instructions to the user: %s", checkpoint.SummaryData)
	}
	if strings.Contains(string(checkpoint.SummaryData), compactionRequestMarker) {
		t.Fatalf("summary retained the application control marker: %s", checkpoint.SummaryData)
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
	//lint:ignore SA1012 nil is the lifecycle boundary under test.
	if _, err := einoAgent.PrepareCompaction(nil, &ChatRequest{}); err == nil {
		t.Fatal("PrepareCompaction accepted a nil setup context")
	}
	if _, err := einoAgent.PrepareCompaction(t.Context(), nil); err == nil {
		t.Fatal("PrepareCompaction accepted a nil request")
	}
	//lint:ignore SA1012 nil is the lifecycle boundary under test.
	if _, err := einoAgent.RunPreparedCompaction(nil, &PreparedCompactionRun{}); err == nil {
		t.Fatal("RunPreparedCompaction accepted a nil durable context")
	}
}
