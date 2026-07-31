package modelstream

import (
	"context"

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
