package openairesponses

import (
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"
	openaiSchema "github.com/cloudwego/eino/schema/openai"
)

type responseState struct {
	sawRefusal  bool
	sawToolCall bool
}

func fromAgenticMessage(message *schema.AgenticMessage, state *responseState) (*schema.Message, error) {
	if message == nil {
		return nil, fmt.Errorf("openai responses returned a nil message")
	}
	if state == nil {
		state = &responseState{}
	}
	out := &schema.Message{Role: classicRole(message.Role)}
	for _, block := range message.ContentBlocks {
		if block == nil {
			return nil, fmt.Errorf("openai responses returned a nil content block")
		}
		switch block.Type {
		case schema.ContentBlockTypeAssistantGenText:
			if block.AssistantGenText == nil {
				return nil, fmt.Errorf("assistant text block is missing payload")
			}
			out.Content += block.AssistantGenText.Text
			if extension := block.AssistantGenText.OpenAIExtension; extension != nil && extension.Refusal != nil {
				state.sawRefusal = true
				out.Content += extension.Refusal.Reason
			}
		case schema.ContentBlockTypeReasoning:
			if block.Reasoning == nil {
				return nil, fmt.Errorf("reasoning block is missing payload")
			}
			out.ReasoningContent += reasoningText(block.Reasoning)
		case schema.ContentBlockTypeFunctionToolCall:
			if block.FunctionToolCall == nil {
				return nil, fmt.Errorf("function tool call block is missing payload")
			}
			index := 0
			if block.StreamingMeta != nil {
				index = block.StreamingMeta.Index
			}
			state.sawToolCall = true
			out.ToolCalls = append(out.ToolCalls, schema.ToolCall{
				Index: &index,
				ID:    block.FunctionToolCall.CallID,
				Type:  "function",
				Function: schema.FunctionCall{
					Name:      block.FunctionToolCall.Name,
					Arguments: block.FunctionToolCall.Arguments,
				},
			})
		case schema.ContentBlockTypeAssistantGenImage,
			schema.ContentBlockTypeAssistantGenAudio,
			schema.ContentBlockTypeAssistantGenVideo:
			return nil, fmt.Errorf("unsupported OpenAI Responses assistant media block %q", block.Type)
		case schema.ContentBlockTypeServerToolCall,
			schema.ContentBlockTypeServerToolResult,
			schema.ContentBlockTypeMCPToolCall,
			schema.ContentBlockTypeMCPToolResult,
			schema.ContentBlockTypeMCPListToolsResult,
			schema.ContentBlockTypeMCPToolApprovalRequest,
			schema.ContentBlockTypeMCPToolApprovalResponse,
			schema.ContentBlockTypeToolSearchResult:
			return nil, fmt.Errorf("unsupported hosted OpenAI Responses block %q", block.Type)
		default:
			return nil, fmt.Errorf("unsupported OpenAI Responses block %q", block.Type)
		}
	}

	terminalErr := applyResponseMeta(out, message.ResponseMeta, state)
	if out.Role == "" && len(out.Content) == 0 && len(out.ReasoningContent) == 0 && len(out.ToolCalls) == 0 && out.ResponseMeta == nil {
		return nil, terminalErr
	}
	return out, terminalErr
}

func classicRole(role schema.AgenticRoleType) schema.RoleType {
	switch role {
	case schema.AgenticRoleTypeSystem:
		return schema.System
	case schema.AgenticRoleTypeUser:
		return schema.User
	case schema.AgenticRoleTypeAssistant:
		return schema.Assistant
	default:
		return ""
	}
}

func reasoningText(reasoning *schema.Reasoning) string {
	if reasoning == nil {
		return ""
	}
	if reasoning.Text != "" {
		return reasoning.Text
	}
	if reasoning.OpenAIExtension == nil {
		return ""
	}
	var result strings.Builder
	for _, content := range reasoning.OpenAIExtension.Content {
		if content != nil {
			result.WriteString(content.Text)
		}
	}
	return result.String()
}

func applyResponseMeta(out *schema.Message, meta *schema.AgenticResponseMeta, state *responseState) error {
	if meta == nil {
		return nil
	}
	out.ResponseMeta = &schema.ResponseMeta{}
	if meta.TokenUsage != nil {
		usage := meta.TokenUsage
		out.ResponseMeta.Usage = &schema.TokenUsage{
			PromptTokens:            usage.PromptTokens,
			PromptTokenDetails:      schema.PromptTokenDetails{CachedTokens: usage.PromptTokenDetails.CachedTokens},
			CompletionTokens:        usage.CompletionTokens,
			CompletionTokensDetails: schema.CompletionTokensDetails{ReasoningTokens: usage.CompletionTokensDetails.ReasoningTokens},
			TotalTokens:             usage.TotalTokens,
		}
	}
	extension := meta.OpenAIExtension
	if extension == nil {
		return nil
	}
	if state.sawRefusal {
		out.ResponseMeta.FinishReason = "refusal"
	} else if state.sawToolCall {
		out.ResponseMeta.FinishReason = "tool_calls"
	} else {
		out.ResponseMeta.FinishReason = responsesFinishReason(extension)
	}
	if extension.Error != nil || extension.Status == openaiSchema.ResponseStatusFailed || extension.Status == openaiSchema.ResponseStatusCancelled {
		if extension.Error != nil && extension.Error.Message != "" {
			return fmt.Errorf("openai responses failed: %s", extension.Error.Message)
		}
		return fmt.Errorf("openai responses ended with status %q", extension.Status)
	}
	return nil
}

func responsesFinishReason(extension *openaiSchema.ResponseMetaExtension) string {
	if extension == nil {
		return ""
	}
	if extension.IncompleteDetails != nil && extension.IncompleteDetails.Reason != "" {
		return extension.IncompleteDetails.Reason
	}
	switch extension.Status {
	case openaiSchema.ResponseStatusCompleted:
		return "stop"
	case openaiSchema.ResponseStatusIncomplete:
		return "incomplete"
	default:
		return ""
	}
}
