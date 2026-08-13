package modelstream

import (
	"context"
	"strings"

	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// ObserveChatModel places first-output observation directly around the raw
// ChatModel stream. ADK retry middleware may read chunks before it exposes an
// assistant event to the outer Agent loop; observing only that outer event can
// therefore let a startup timer fire even though the provider already emitted
// useful output.
func ObserveChatModel(base einoModel.ToolCallingChatModel) einoModel.ToolCallingChatModel {
	if base == nil {
		return nil
	}
	if _, ok := base.(*observingChatModel); ok {
		return base
	}
	return &observingChatModel{base: base}
}

// ObserveAgenticModel applies the same first-output contract to Eino's typed
// AgenticModel path. This keeps the startup timer at the raw provider stream
// boundary even when the outer Agent consumes typed Agentic events.
func ObserveAgenticModel(base einoModel.AgenticModel) einoModel.AgenticModel {
	if base == nil {
		return nil
	}
	if _, ok := base.(*observingAgenticModel); ok {
		return base
	}
	return &observingAgenticModel{base: base}
}

type observingAgenticModel struct {
	base einoModel.AgenticModel
}

func (m *observingAgenticModel) Generate(ctx context.Context, input []*schema.AgenticMessage, options ...einoModel.Option) (*schema.AgenticMessage, error) {
	return m.base.Generate(ctx, input, options...)
}

func (m *observingAgenticModel) Stream(ctx context.Context, input []*schema.AgenticMessage, options ...einoModel.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	ArmFirstOutputTimeout(ctx)
	reader, err := m.base.Stream(ctx, input, options...)
	if err != nil {
		return nil, err
	}
	if reader == nil {
		return nil, ErrNilReader
	}
	return schema.StreamReaderWithConvert(reader, func(chunk *schema.AgenticMessage) (*schema.AgenticMessage, error) {
		if HasMeaningfulAgenticOutput(chunk) {
			MarkOutput(ctx)
		}
		return chunk, nil
	}), nil
}

func HasMeaningfulAgenticOutput(message *schema.AgenticMessage) bool {
	if message == nil {
		return false
	}
	for _, block := range message.ContentBlocks {
		if block == nil {
			continue
		}
		switch block.Type {
		case schema.ContentBlockTypeAssistantGenText:
			if block.AssistantGenText != nil && strings.TrimSpace(block.AssistantGenText.Text) != "" {
				return true
			}
		case schema.ContentBlockTypeReasoning:
			if block.Reasoning != nil && strings.TrimSpace(block.Reasoning.Text) != "" {
				return true
			}
		case schema.ContentBlockTypeFunctionToolCall:
			if block.FunctionToolCall != nil && strings.TrimSpace(block.FunctionToolCall.Name) != "" {
				return true
			}
		}
	}
	return false
}

type observingChatModel struct {
	base einoModel.ToolCallingChatModel
}

// Generate exists only because Eino's ToolCallingChatModel interface includes
// both modes. EffChat runtime call sites use Stream; this wrapper never turns
// a stream failure into a Generate fallback.
func (m *observingChatModel) Generate(ctx context.Context, input []*schema.Message, options ...einoModel.Option) (*schema.Message, error) {
	return m.base.Generate(ctx, input, options...)
}

func (m *observingChatModel) Stream(ctx context.Context, input []*schema.Message, options ...einoModel.Option) (*schema.StreamReader[*schema.Message], error) {
	ArmFirstOutputTimeout(ctx)
	reader, err := m.base.Stream(ctx, input, options...)
	if err != nil {
		return nil, err
	}
	if reader == nil {
		return nil, ErrNilReader
	}
	return schema.StreamReaderWithConvert(reader, func(chunk *schema.Message) (*schema.Message, error) {
		ObserveMessage(ctx, chunk)
		return chunk, nil
	}), nil
}

func (m *observingChatModel) WithTools(tools []*schema.ToolInfo) (einoModel.ToolCallingChatModel, error) {
	next, err := m.base.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return ObserveChatModel(next), nil
}
