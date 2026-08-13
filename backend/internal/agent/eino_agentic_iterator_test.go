package agent

import (
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

func TestAgenticPreparedIteratorPreservesAssistantAndToolEvents(t *testing.T) {
	inner, generator := adk.NewAsyncIteratorPair[*adk.TypedAgentEvent[*schema.AgenticMessage]]()
	assistantStream := schema.StreamReaderFromArray([]*schema.AgenticMessage{{
		Role:          schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{schema.NewContentBlock(&schema.AssistantGenText{Text: "answer"})},
	}})
	generator.Send(adk.EventFromAgenticMessage(nil, assistantStream, schema.AgenticRoleTypeAssistant))
	generator.Send(adk.EventFromAgenticMessage(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeUser,
		ContentBlocks: []*schema.ContentBlock{schema.NewContentBlock(&schema.FunctionToolResult{
			CallID: "call-1", Name: "file_read",
			Content: []*schema.FunctionToolResultContentBlock{{
				Type: schema.FunctionToolResultContentBlockTypeText,
				Text: &schema.UserInputText{Text: "file content"},
			}},
		})},
	}, nil, schema.AgenticRoleTypeUser))
	generator.Close()

	iterator := &agenticPreparedIterator{inner: inner}
	assistantEvent, ok := iterator.Next()
	if !ok || assistantEvent.Err != nil || assistantEvent.Output == nil {
		t.Fatalf("unexpected assistant event: %#v", assistantEvent)
	}
	assistant, err := assistantEvent.Output.MessageOutput.GetMessage()
	if err != nil {
		t.Fatalf("read assistant event: %v", err)
	}
	if assistant.Role != schema.Assistant || assistant.Content != "answer" {
		t.Fatalf("unexpected assistant message: %#v", assistant)
	}

	toolEvent, ok := iterator.Next()
	if !ok || toolEvent.Err != nil || toolEvent.Output == nil {
		t.Fatalf("unexpected tool event: %#v", toolEvent)
	}
	toolMessage, err := toolEvent.Output.MessageOutput.GetMessage()
	if err != nil {
		t.Fatalf("read tool event: %v", err)
	}
	if toolEvent.Output.MessageOutput.Role != schema.Tool || toolMessage.ToolCallID != "call-1" || toolMessage.ToolName != "file_read" || toolMessage.Content != "file content" {
		t.Fatalf("unexpected tool message: %#v", toolMessage)
	}
}
