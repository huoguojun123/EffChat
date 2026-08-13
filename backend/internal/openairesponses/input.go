package openairesponses

import (
	"fmt"

	"github.com/cloudwego/eino/schema"
)

func toAgenticMessages(messages []*schema.Message) ([]*schema.AgenticMessage, error) {
	result := make([]*schema.AgenticMessage, 0, len(messages))
	for index, message := range messages {
		converted, err := toAgenticMessage(message)
		if err != nil {
			return nil, fmt.Errorf("convert input message %d: %w", index, err)
		}
		result = append(result, converted)
	}
	return result, nil
}

// ToAgenticMessages converts EffChat's persisted Eino messages at the Agent
// boundary. It is intentionally separate from the wire model: the typed Agent
// and official Responses component remain responsible for Tool/ReAct behavior.
func ToAgenticMessages(messages []*schema.Message) ([]*schema.AgenticMessage, error) {
	return toAgenticMessages(messages)
}

func toAgenticMessage(message *schema.Message) (*schema.AgenticMessage, error) {
	if message == nil {
		return nil, fmt.Errorf("message is nil")
	}
	switch message.Role {
	case schema.System:
		return &schema.AgenticMessage{
			Role:          schema.AgenticRoleTypeSystem,
			ContentBlocks: textInputBlocks(message.Content),
		}, nil
	case schema.User:
		blocks, err := userInputBlocks(message)
		if err != nil {
			return nil, err
		}
		return &schema.AgenticMessage{Role: schema.AgenticRoleTypeUser, ContentBlocks: blocks}, nil
	case schema.Assistant:
		return assistantInputMessage(message)
	case schema.Tool:
		if message.ToolCallID == "" || message.ToolName == "" {
			return nil, fmt.Errorf("tool result requires call ID and name")
		}
		return &schema.AgenticMessage{
			Role: schema.AgenticRoleTypeUser,
			ContentBlocks: []*schema.ContentBlock{schema.NewContentBlock(&schema.FunctionToolResult{
				CallID: message.ToolCallID,
				Name:   message.ToolName,
				Content: []*schema.FunctionToolResultContentBlock{{
					Type: schema.FunctionToolResultContentBlockTypeText,
					Text: &schema.UserInputText{Text: message.Content},
				}},
			})},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported message role %q", message.Role)
	}
}

func textInputBlocks(text string) []*schema.ContentBlock {
	if text == "" {
		return nil
	}
	return []*schema.ContentBlock{schema.NewContentBlock(&schema.UserInputText{Text: text})}
}

func userInputBlocks(message *schema.Message) ([]*schema.ContentBlock, error) {
	if len(message.UserInputMultiContent) == 0 {
		return textInputBlocks(message.Content), nil
	}
	blocks := make([]*schema.ContentBlock, 0, len(message.UserInputMultiContent))
	for _, part := range message.UserInputMultiContent {
		switch part.Type {
		case schema.ChatMessagePartTypeText:
			blocks = append(blocks, schema.NewContentBlock(&schema.UserInputText{Text: part.Text}))
		case schema.ChatMessagePartTypeImageURL:
			if part.Image == nil {
				return nil, fmt.Errorf("image input is missing payload")
			}
			blocks = append(blocks, schema.NewContentBlock(&schema.UserInputImage{
				URL:        stringValue(part.Image.URL),
				Base64Data: stringValue(part.Image.Base64Data),
				MIMEType:   part.Image.MIMEType,
				Detail:     part.Image.Detail,
			}))
		case schema.ChatMessagePartTypeAudioURL:
			if part.Audio == nil {
				return nil, fmt.Errorf("audio input is missing payload")
			}
			blocks = append(blocks, schema.NewContentBlock(&schema.UserInputAudio{
				URL:        stringValue(part.Audio.URL),
				Base64Data: stringValue(part.Audio.Base64Data),
				MIMEType:   part.Audio.MIMEType,
			}))
		case schema.ChatMessagePartTypeVideoURL:
			if part.Video == nil {
				return nil, fmt.Errorf("video input is missing payload")
			}
			blocks = append(blocks, schema.NewContentBlock(&schema.UserInputVideo{
				URL:        stringValue(part.Video.URL),
				Base64Data: stringValue(part.Video.Base64Data),
				MIMEType:   part.Video.MIMEType,
			}))
		case schema.ChatMessagePartTypeFileURL:
			if part.File == nil {
				return nil, fmt.Errorf("file input is missing payload")
			}
			blocks = append(blocks, schema.NewContentBlock(&schema.UserInputFile{
				URL:        stringValue(part.File.URL),
				Name:       part.File.Name,
				Base64Data: stringValue(part.File.Base64Data),
				MIMEType:   part.File.MIMEType,
			}))
		default:
			return nil, fmt.Errorf("unsupported user input part %q", part.Type)
		}
	}
	return blocks, nil
}

func assistantInputMessage(message *schema.Message) (*schema.AgenticMessage, error) {
	blocks := make([]*schema.ContentBlock, 0, 2+len(message.ToolCalls))
	if message.ReasoningContent != "" {
		blocks = append(blocks, schema.NewContentBlock(&schema.Reasoning{Text: message.ReasoningContent}))
	}
	if message.Content != "" {
		blocks = append(blocks, schema.NewContentBlock(&schema.AssistantGenText{Text: message.Content}))
	}
	for _, call := range message.ToolCalls {
		if call.ID == "" || call.Function.Name == "" {
			return nil, fmt.Errorf("assistant tool call requires call ID and name")
		}
		blocks = append(blocks, schema.NewContentBlock(&schema.FunctionToolCall{
			CallID:    call.ID,
			Name:      call.Function.Name,
			Arguments: call.Function.Arguments,
		}))
	}
	return &schema.AgenticMessage{Role: schema.AgenticRoleTypeAssistant, ContentBlocks: blocks}, nil
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
