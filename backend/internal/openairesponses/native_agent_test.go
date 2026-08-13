package openairesponses

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/cloudwego/eino/adk"
	einoModel "github.com/cloudwego/eino/components/model"
	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// scriptedAgenticModel proves the stable Eino typed Agent path owns the local
// ReAct loop: the first model turn requests a function, and the second turn is
// accepted only after Eino has executed the local tool and appended its result.
type scriptedAgenticModel struct {
	mu    sync.Mutex
	calls int
}

func (m *scriptedAgenticModel) Generate(_ context.Context, input []*schema.AgenticMessage, opts ...einoModel.Option) (*schema.AgenticMessage, error) {
	return nil, fmt.Errorf("unexpected non-streaming call")
}

func (m *scriptedAgenticModel) Stream(_ context.Context, input []*schema.AgenticMessage, opts ...einoModel.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++

	options := einoModel.GetCommonOptions(nil, opts...)
	if len(options.Tools) != 1 || options.Tools[0].Name != "lookup" {
		return nil, fmt.Errorf("typed agent did not pass the local tool schema to the model")
	}

	if m.calls == 1 {
		return schema.StreamReaderFromArray([]*schema.AgenticMessage{{
			Role: schema.AgenticRoleTypeAssistant,
			ContentBlocks: []*schema.ContentBlock{schema.NewContentBlock(&schema.FunctionToolCall{
				CallID:    "call-1",
				Name:      "lookup",
				Arguments: `{"query":"EffChat"}`,
			})},
		}}), nil
	}

	if !containsFunctionResult(input, "call-1", "EffChat result") {
		return nil, fmt.Errorf("typed agent did not append the local tool result")
	}
	return schema.StreamReaderFromArray([]*schema.AgenticMessage{{
		Role:          schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{schema.NewContentBlock(&schema.AssistantGenText{Text: "done"})},
	}}), nil
}

type lookupTool struct {
	mu    sync.Mutex
	calls int
}

var _ einoTool.InvokableTool = (*lookupTool)(nil)

func (t *lookupTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "lookup", Desc: "Return a deterministic test result."}, nil
}

func (t *lookupTool) InvokableRun(_ context.Context, arguments string, _ ...einoTool.Option) (string, error) {
	if arguments != `{"query":"EffChat"}` {
		return "", fmt.Errorf("unexpected arguments: %s", arguments)
	}
	t.mu.Lock()
	t.calls++
	t.mu.Unlock()
	return "EffChat result", nil
}

func TestStableTypedAgentExecutesLocalFunctionTool(t *testing.T) {
	model := &scriptedAgenticModel{}
	tool := &lookupTool{}
	agent, err := adk.NewTypedChatModelAgent[*schema.AgenticMessage](t.Context(), &adk.TypedChatModelAgentConfig[*schema.AgenticMessage]{
		Model:         model,
		MaxIterations: 3,
		ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{
			Tools: []einoTool.BaseTool{tool},
		}},
	})
	if err != nil {
		t.Fatalf("create typed agent: %v", err)
	}

	iter := agent.Run(t.Context(), &adk.TypedAgentInput[*schema.AgenticMessage]{
		Messages:        []*schema.AgenticMessage{schema.UserAgenticMessage("look it up")},
		EnableStreaming: true,
	})
	var finalText string
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			t.Fatalf("run typed agent: %v", event.Err)
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}
		message, readErr := event.Output.MessageOutput.GetMessage()
		if readErr != nil {
			t.Fatalf("read typed agent event: %v", readErr)
		}
		for _, block := range message.ContentBlocks {
			if block != nil && block.AssistantGenText != nil {
				finalText += block.AssistantGenText.Text
			}
		}
	}

	model.mu.Lock()
	modelCalls := model.calls
	model.mu.Unlock()
	tool.mu.Lock()
	toolCalls := tool.calls
	tool.mu.Unlock()
	if modelCalls != 2 || toolCalls != 1 {
		t.Fatalf("unexpected ReAct lifecycle: model calls=%d tool calls=%d", modelCalls, toolCalls)
	}
	if finalText != "done" {
		t.Fatalf("unexpected final assistant text: %q", finalText)
	}
}

func containsFunctionResult(messages []*schema.AgenticMessage, callID, expected string) bool {
	for _, message := range messages {
		if message == nil {
			continue
		}
		for _, block := range message.ContentBlocks {
			if block == nil || block.FunctionToolResult == nil || block.FunctionToolResult.CallID != callID {
				continue
			}
			for _, content := range block.FunctionToolResult.Content {
				if content != nil && content.Text != nil && content.Text.Text == expected {
					return true
				}
			}
		}
	}
	return false
}
