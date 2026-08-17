package openairesponses

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

func collectProtocolStream(t *testing.T, model einoModel.ToolCallingChatModel, messages []*schema.Message) *schema.Message {
	t.Helper()
	stream, err := model.Stream(t.Context(), messages)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var chunks []*schema.Message
	for {
		chunk, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			t.Fatalf("receive stream: %v", recvErr)
		}
		chunks = append(chunks, chunk)
	}
	result, err := schema.ConcatMessages(chunks)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestResponsesProtocolStreamsThroughOfficialAdapter(t *testing.T) {
	requestSeen := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected endpoint", http.StatusNotFound)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		requestSeen <- body

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"resp_demo\",\"status\":\"in_progress\",\"model\":\"gpt-demo\",\"output\":[]}}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.reasoning_summary_text.delta\",\"sequence_number\":1,\"item_id\":\"reason_1\",\"output_index\":0,\"summary_index\":0,\"delta\":\"verify first\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_item.added\",\"sequence_number\":2,\"output_index\":1,\"item\":{\"id\":\"fc_1\",\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"inventory_lookup\",\"arguments\":\"\",\"status\":\"in_progress\"}}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.function_call_arguments.delta\",\"sequence_number\":3,\"item_id\":\"fc_1\",\"output_index\":1,\"delta\":\"{\\\"sku\\\":\\\"demo-1\\\"}\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"sequence_number\":4,\"item_id\":\"msg_1\",\"output_index\":2,\"content_index\":0,\"delta\":\"Checking inventory.\",\"logprobs\":[]}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"sequence_number\":5,\"response\":{\"id\":\"resp_demo\",\"status\":\"completed\",\"model\":\"gpt-demo\",\"output\":[],\"usage\":{\"input_tokens\":9,\"output_tokens\":6,\"total_tokens\":15,\"input_tokens_details\":{\"cached_tokens\":2},\"output_tokens_details\":{\"reasoning_tokens\":3}}}}\n\n")
	}))
	defer server.Close()

	model, err := NewChatModel(t.Context(), &Config{
		BaseURL: server.URL + "/v1",
		APIKey:  "test-key",
		Model:   "gpt-demo",
		Reasoning: &responses.ReasoningParam{
			Effort:  shared.ReasoningEffortMax,
			Summary: shared.ReasoningSummaryAuto,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	model, err = model.WithTools([]*schema.ToolInfo{{
		Name:        "inventory_lookup",
		Desc:        "Look up a fictional inventory item.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{}),
	}})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := model.Stream(t.Context(), []*schema.Message{{Role: schema.User, Content: "Check demo-1."}})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var chunks []*schema.Message
	for {
		chunk, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			t.Fatalf("receive stream: %v", recvErr)
		}
		chunks = append(chunks, chunk)
	}
	result, err := schema.ConcatMessages(chunks)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "Checking inventory." || result.ReasoningContent != "verify first" {
		t.Fatalf("stream content mismatch: %#v", result)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].ID != "call_1" || result.ToolCalls[0].Function.Arguments != `{"sku":"demo-1"}` {
		t.Fatalf("stream tool call mismatch: %#v", result.ToolCalls)
	}
	if result.ResponseMeta == nil || result.ResponseMeta.FinishReason != "tool_calls" || result.ResponseMeta.Usage.TotalTokens != 15 {
		t.Fatalf("stream metadata mismatch: %#v", result.ResponseMeta)
	}

	body := <-requestSeen
	if body["store"] != false || body["stream"] != true || body["model"] != "gpt-demo" {
		t.Fatalf("request ownership fields = %#v", body)
	}
	if _, exists := body["previous_response_id"]; exists {
		t.Fatalf("request must not use previous_response_id: %#v", body)
	}
	reasoning, _ := body["reasoning"].(map[string]any)
	if reasoning["effort"] != "max" || reasoning["summary"] != "auto" {
		t.Fatalf("reasoning request = %#v", reasoning)
	}
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("function tools = %#v", body["tools"])
	}
}

func TestResponsesProtocolRoundTripsFunctionToolResults(t *testing.T) {
	var calls atomic.Int32
	secondRequest := make(chan map[string]any, 1)
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
		call := calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		if call == 1 {
			_, _ = io.WriteString(w, "data: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"resp_tool_1\",\"status\":\"in_progress\",\"model\":\"gpt-demo\",\"output\":[]}}\n\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"response.output_item.added\",\"sequence_number\":1,\"output_index\":0,\"item\":{\"id\":\"fc_1\",\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"inventory_lookup\",\"arguments\":\"\",\"status\":\"in_progress\"}}\n\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"response.function_call_arguments.delta\",\"sequence_number\":2,\"item_id\":\"fc_1\",\"output_index\":0,\"delta\":\"{\\\"sku\\\":\\\"demo-1\\\"}\"}\n\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"sequence_number\":3,\"response\":{\"id\":\"resp_tool_1\",\"status\":\"completed\",\"model\":\"gpt-demo\",\"output\":[],\"usage\":{\"input_tokens\":5,\"output_tokens\":2,\"total_tokens\":7}}}\n\n")
			return
		}
		secondRequest <- body
		_, _ = io.WriteString(w, "data: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"resp_tool_2\",\"status\":\"in_progress\",\"model\":\"gpt-demo\",\"output\":[]}}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"item_id\":\"msg_1\",\"output_index\":0,\"content_index\":0,\"delta\":\"Inventory is available.\",\"logprobs\":[]}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":{\"id\":\"resp_tool_2\",\"status\":\"completed\",\"model\":\"gpt-demo\",\"output\":[],\"usage\":{\"input_tokens\":8,\"output_tokens\":3,\"total_tokens\":11}}}\n\n")
	}))
	defer server.Close()

	chatModel, err := NewChatModel(t.Context(), &Config{BaseURL: server.URL + "/v1", APIKey: "test-key", Model: "gpt-demo"})
	if err != nil {
		t.Fatal(err)
	}
	chatModel, err = chatModel.WithTools([]*schema.ToolInfo{{
		Name:        "inventory_lookup",
		Desc:        "Look up fictional inventory.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{}),
	}})
	if err != nil {
		t.Fatal(err)
	}
	user := schema.UserMessage("Check demo-1.")
	toolCall := collectProtocolStream(t, chatModel, []*schema.Message{user})
	if len(toolCall.ToolCalls) != 1 || toolCall.ToolCalls[0].ID != "call_1" {
		t.Fatalf("tool call = %#v", toolCall.ToolCalls)
	}
	toolResult := &schema.Message{
		Role:       schema.Tool,
		ToolCallID: "call_1",
		ToolName:   "inventory_lookup",
		Content:    `{"available":true}`,
	}
	answer := collectProtocolStream(t, chatModel, []*schema.Message{user, toolCall, toolResult})
	if answer.Content != "Inventory is available." {
		t.Fatalf("answer = %#v", answer)
	}
	body := <-secondRequest
	encoded, err := json.Marshal(body["input"])
	if err != nil {
		t.Fatal(err)
	}
	requestInput := string(encoded)
	for _, want := range []string{`"type":"function_call"`, `"call_id":"call_1"`, `"type":"function_call_output"`, `"type":"input_text"`, `"text":"{\"available\":true}"`} {
		if !strings.Contains(requestInput, want) {
			t.Fatalf("second request input %s does not contain %s", requestInput, want)
		}
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("provider calls = %d, want 2", got)
	}
}

func TestResponsesProtocolPreservesPartialOutputBeforeTransportFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijacking unavailable", http.StatusInternalServerError)
			return
		}
		conn, buffered, err := hijacker.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = fmt.Fprint(buffered, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nContent-Length: 1048576\r\nConnection: close\r\n\r\n")
		_, _ = fmt.Fprint(buffered, "data: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"resp_partial\",\"status\":\"in_progress\",\"model\":\"gpt-demo\",\"output\":[]}}\n\n")
		_, _ = fmt.Fprint(buffered, "data: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"item_id\":\"msg_1\",\"output_index\":0,\"content_index\":0,\"delta\":\"partial durable text\",\"logprobs\":[]}\n\n")
		_ = buffered.Flush()
	}))
	defer server.Close()

	chatModel, err := NewChatModel(t.Context(), &Config{BaseURL: server.URL + "/v1", APIKey: "test-key", Model: "gpt-demo"})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := chatModel.Stream(t.Context(), []*schema.Message{schema.UserMessage("hello")})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var content strings.Builder
	var terminalErr error
	for {
		chunk, recvErr := stream.Recv()
		if chunk != nil {
			content.WriteString(chunk.Content)
		}
		if recvErr != nil {
			terminalErr = recvErr
			break
		}
	}
	if content.String() != "partial durable text" {
		t.Fatalf("partial content = %q", content.String())
	}
	// A deliberately truncated response can surface as io.ErrUnexpectedEOF or
	// a connection reset depending on the runner's TCP stack. The protocol
	// contract is that partial output survives and the truncation is not
	// mistaken for a clean end-of-stream.
	if terminalErr == nil || errors.Is(terminalErr, io.EOF) {
		t.Fatalf("terminal error = %v", terminalErr)
	}
}
