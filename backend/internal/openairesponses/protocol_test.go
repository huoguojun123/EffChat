package openairesponses

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloudwego/eino/schema"
)

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
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("function tools = %#v", body["tools"])
	}
}
