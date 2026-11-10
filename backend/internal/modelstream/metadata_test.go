package modelstream

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

// Metadata can arrive after the tool name/arguments. Losing that late frame
// breaks signed tool replay even though the visible answer looks complete.
func TestCollectPreservesLateToolMetadata(t *testing.T) {
	index := 0
	model := &streamOnlyModel{stream: func(context.Context) (*schema.StreamReader[*schema.Message], error) {
		return schema.StreamReaderFromArray([]*schema.Message{
			{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{Index: &index, ID: "call_fixture", Type: "function", Function: schema.FunctionCall{Name: "fixture_lookup", Arguments: `{"q":`}}}},
			{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{Index: &index, Function: schema.FunctionCall{Arguments: `"fixture"}`}, Extra: map[string]any{"gemini_thought_signature": []byte{1, 2, 3}}}}},
		}), nil
	}}
	got, err := Collect(t.Context(), model, nil, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ToolCalls) != 1 || got.ToolCalls[0].Function.Arguments != `{"q":"fixture"}` {
		t.Fatalf("tool fragments = %+v", got.ToolCalls)
	}
	signature, _ := got.ToolCalls[0].Extra["gemini_thought_signature"].([]byte)
	if !bytes.Equal(signature, []byte{1, 2, 3}) {
		t.Fatalf("late tool signature = %v", signature)
	}
}

func TestCollectHandlesNullableStreamMetadata(t *testing.T) {
	for _, values := range [][]any{{nil, "fixture"}, {"fixture", nil}, {nil, nil}} {
		t.Run("nullable", func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("nullable metadata panicked: %v", recovered)
				}
			}()
			model := &streamOnlyModel{stream: func(context.Context) (*schema.StreamReader[*schema.Message], error) {
				return schema.StreamReaderFromArray([]*schema.Message{
					{Role: schema.Assistant, Content: "a", Extra: map[string]any{"provider_metadata": values[0]}},
					{Role: schema.Assistant, Content: "b", Extra: map[string]any{"provider_metadata": values[1]}},
				}), nil
			}}
			got, err := Collect(t.Context(), model, nil, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			want := values[0]
			if want == nil {
				want = values[1]
			}
			value, exists := got.Extra["provider_metadata"]
			if got.Content != "ab" || !exists || value != want {
				t.Fatalf("collected metadata = %#v, want %v", got, want)
			}
		})
	}
}
