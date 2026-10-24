package agent

import (
	"context"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func (m *toolBudgetMiddleware) WrapModel(_ context.Context, base model.BaseChatModel, _ *adk.ModelContext) (model.BaseChatModel, error) {
	if !m.forceFinal.Load() {
		return base, nil
	}
	return &budgetFinalModel[*schema.Message]{base: base, choice: model.WithToolChoice(schema.ToolChoiceForbidden)}, nil
}

func (m *agenticToolBudgetMiddleware) WrapModel(_ context.Context, base model.AgenticModel, _ *adk.TypedModelContext[*schema.AgenticMessage]) (model.AgenticModel, error) {
	if m == nil || m.budget == nil || !m.budget.forceFinal.Load() {
		return base, nil
	}
	// Responses rejects classic ToolChoice options, even though both contracts
	// serialize to "none". Keep this distinction at the model boundary.
	return &budgetFinalModel[*schema.AgenticMessage]{base: base, choice: model.WithAgenticToolChoice(&schema.AgenticToolChoice{Type: schema.ToolChoiceForbidden})}, nil
}

type budgetFinalModel[M adk.MessageType] struct {
	base   model.BaseModel[M]
	choice model.Option
}

func (m *budgetFinalModel[M]) Generate(ctx context.Context, input []M, opts ...model.Option) (M, error) {
	return m.base.Generate(ctx, input, append(append([]model.Option(nil), opts...), m.choice)...)
}

func (m *budgetFinalModel[M]) Stream(ctx context.Context, input []M, opts ...model.Option) (*schema.StreamReader[M], error) {
	return m.base.Stream(ctx, input, append(append([]model.Option(nil), opts...), m.choice)...)
}
