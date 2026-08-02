package openairesponses

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	openaiSchema "github.com/cloudwego/eino/schema/openai"
)

type captureAgenticModel struct {
	input  []*schema.AgenticMessage
	opts   *einoModel.Options
	out    *schema.AgenticMessage
	stream *schema.StreamReader[*schema.AgenticMessage]
}

func (m *captureAgenticModel) Generate(_ context.Context, input []*schema.AgenticMessage, opts ...einoModel.Option) (*schema.AgenticMessage, error) {
	m.input = input
	m.opts = einoModel.GetCommonOptions(nil, opts...)
	return m.out, nil
}

func (m *captureAgenticModel) Stream(_ context.Context, input []*schema.AgenticMessage, opts ...einoModel.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	m.input = input
	m.opts = einoModel.GetCommonOptions(nil, opts...)
	return m.stream, nil
}

func TestChatModelBridgesMessagesToolsReasoningAndUsage(t *testing.T) {
	base := &captureAgenticModel{out: &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{
			schema.NewContentBlock(&schema.Reasoning{Text: "check inventory"}),
			schema.NewContentBlock(&schema.AssistantGenText{Text: "I will look it up."}),
			schema.NewContentBlock(&schema.FunctionToolCall{CallID: "call_1", Name: "inventory_lookup", Arguments: `{"sku":"demo-1"}`}),
		},
		ResponseMeta: &schema.AgenticResponseMeta{
			TokenUsage:      &schema.TokenUsage{PromptTokens: 8, CompletionTokens: 5, TotalTokens: 13},
			OpenAIExtension: &openaiSchema.ResponseMetaExtension{Status: openaiSchema.ResponseStatusCompleted},
		},
	}}
	model, err := newChatModel(base).WithTools([]*schema.ToolInfo{{Name: "inventory_lookup"}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.Generate(t.Context(), []*schema.Message{
		{Role: schema.System, Content: "Use tools when needed."},
		{Role: schema.User, Content: "Check demo inventory."},
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "call_0", Function: schema.FunctionCall{Name: "catalog_lookup", Arguments: `{}`}}}},
		{Role: schema.Tool, ToolCallID: "call_0", ToolName: "catalog_lookup", Content: "available"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(base.input) != 4 || base.input[3].ContentBlocks[0].FunctionToolResult.CallID != "call_0" {
		t.Fatalf("tool replay was not converted: %#v", base.input)
	}
	if base.opts == nil || len(base.opts.Tools) != 1 || base.opts.Tools[0].Name != "inventory_lookup" {
		t.Fatalf("bound tools were not passed per call: %#v", base.opts)
	}
	if result.Content != "I will look it up." || result.ReasoningContent != "check inventory" {
		t.Fatalf("result content mismatch: %#v", result)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].ID != "call_1" || result.ToolCalls[0].Function.Name != "inventory_lookup" {
		t.Fatalf("tool call mismatch: %#v", result.ToolCalls)
	}
	if result.ResponseMeta == nil || result.ResponseMeta.FinishReason != "tool_calls" || result.ResponseMeta.Usage.TotalTokens != 13 {
		t.Fatalf("response metadata mismatch: %#v", result.ResponseMeta)
	}
}

func TestChatModelStreamPreservesFailedUsageBeforeReturningError(t *testing.T) {
	upstream, sender := schema.Pipe[*schema.AgenticMessage](2)
	sender.Send(&schema.AgenticMessage{
		Role:          schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{schema.NewContentBlock(&schema.AssistantGenText{Text: "partial"})},
	}, nil)
	sender.Send(&schema.AgenticMessage{ResponseMeta: &schema.AgenticResponseMeta{
		TokenUsage: &schema.TokenUsage{PromptTokens: 4, CompletionTokens: 2, TotalTokens: 6},
		OpenAIExtension: &openaiSchema.ResponseMetaExtension{
			Status: openaiSchema.ResponseStatusFailed,
			Error:  &openaiSchema.ResponseError{Message: "upstream failed"},
		},
	}}, nil)
	sender.Close()

	base := &captureAgenticModel{stream: upstream}
	stream, err := newChatModel(base).Stream(t.Context(), []*schema.Message{{Role: schema.User, Content: "hello"}})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	first, err := stream.Recv()
	if err != nil || first.Content != "partial" {
		t.Fatalf("first chunk = %#v, err=%v", first, err)
	}
	usageChunk, err := stream.Recv()
	if err != nil || usageChunk.ResponseMeta == nil || usageChunk.ResponseMeta.Usage.TotalTokens != 6 {
		t.Fatalf("usage chunk = %#v, err=%v", usageChunk, err)
	}
	_, err = stream.Recv()
	if err == nil || errors.Is(err, io.EOF) || !strings.Contains(err.Error(), "upstream failed") {
		t.Fatalf("terminal error = %v", err)
	}
}

func TestChatModelRejectsHostedBlocks(t *testing.T) {
	base := &captureAgenticModel{out: &schema.AgenticMessage{
		Role:          schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{schema.NewContentBlock(&schema.ServerToolCall{Name: "web_search"})},
	}}
	_, err := newChatModel(base).Generate(t.Context(), []*schema.Message{{Role: schema.User, Content: "hello"}})
	if err == nil || !strings.Contains(err.Error(), "unsupported hosted") {
		t.Fatalf("error = %v", err)
	}
}
