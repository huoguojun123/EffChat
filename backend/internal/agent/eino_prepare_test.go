package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/service"
	"github.com/huoguojun123/EffChat/pkg/streaming"
)

type preparedChatEventWriter struct {
	mu     sync.Mutex
	events []string
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
