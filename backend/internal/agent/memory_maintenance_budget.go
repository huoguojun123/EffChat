package agent

import (
	"errors"
	"fmt"
	"strings"

	sessionmemory "github.com/huoguojun123/EffChat/internal/memory"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	modelusage "github.com/huoguojun123/EffChat/internal/usage"
)

const (
	// A memory update returns the complete fixed-section Markdown document
	// inside JSON, not only the newly added bullets. Reserve one token for each
	// allowed document character plus fixed headroom for JSON escaping, section
	// headers, and the change summary. This is deliberately a conservative
	// cross-provider capacity contract rather than a tokenizer-specific estimate.
	memoryMaintenanceJSONHeadroomTokens = 4096
	memoryMaintenanceOutputTokenStep    = 4096
)

var (
	// ErrMemoryMaintenanceOutputBudgetInsufficient means the model declares a
	// smaller output capability than the selected memory capacity requires.
	ErrMemoryMaintenanceOutputBudgetInsufficient = errors.New("memory maintenance output budget is insufficient")
	// ErrMemoryMaintenanceOutputLimit means the provider ended an otherwise
	// readable stream at its output ceiling, so the rewrite must not be saved.
	ErrMemoryMaintenanceOutputLimit = errors.New("memory maintenance reached the model output limit")
)

type memoryOutputBudgetError struct {
	ModelID         string
	MemoryMaxChars  int
	RequiredTokens  int
	AvailableTokens int
}

func (e *memoryOutputBudgetError) Error() string {
	if e == nil {
		return ErrMemoryMaintenanceOutputBudgetInsufficient.Error()
	}
	modelID := strings.TrimSpace(e.ModelID)
	if modelID == "" {
		modelID = "当前模型"
	}
	return fmt.Sprintf(
		"模型 %s 的输出能力不足：%d 字符会话记忆至少需要 %d 输出 token，模型声明上限为 %d；请降低 memory_max_chars 或切换到更高输出模型",
		modelID,
		e.MemoryMaxChars,
		e.RequiredTokens,
		e.AvailableTokens,
	)
}

func (e *memoryOutputBudgetError) Unwrap() error {
	return ErrMemoryMaintenanceOutputBudgetInsufficient
}

type memoryOutputLimitError struct {
	Raw string
}

func (e *memoryOutputLimitError) Error() string {
	if e == nil || strings.TrimSpace(e.Raw) == "" {
		return "模型达到输出上限，会话记忆未修改"
	}
	return fmt.Sprintf("模型达到输出上限（finish_reason=%s），会话记忆未修改", boundedFinishReason(e.Raw))
}

func (e *memoryOutputLimitError) Unwrap() error {
	return ErrMemoryMaintenanceOutputLimit
}

// memoryMaintenanceOutputTokenBudget maps the configured character ceiling to
// a stable output allowance. Storage still enforces MaxChars exactly; this
// budget is the minimum capacity requested for a complete JSON-wrapped rewrite.
func memoryMaintenanceOutputTokenBudget(limits sessionmemory.Limits) int {
	limits = sessionmemory.NormalizeLimits(limits.MaxChars, limits.SoftMaxChars)
	raw := limits.MaxChars + memoryMaintenanceJSONHeadroomTokens
	return ((raw + memoryMaintenanceOutputTokenStep - 1) / memoryMaintenanceOutputTokenStep) * memoryMaintenanceOutputTokenStep
}

func prepareMemoryMaintenanceModelRequest(req *ChatRequest, limits sessionmemory.Limits) (*ChatRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("memory maintenance requires the active chat model configuration")
	}

	limits = sessionmemory.NormalizeLimits(limits.MaxChars, limits.SoftMaxChars)
	required := memoryMaintenanceOutputTokenBudget(limits)
	available := req.ModelMaxOutput
	// Manual compact/retry requests are rebuilt from the session instead of an
	// accepted runtime snapshot. For those live requests, use the current model
	// registry when it has a declared capability. A zero capability remains
	// "unknown" and must not be guessed or silently reduced.
	if available <= 0 && !req.RuntimeResolved {
		available = modelbank.GetOrDefault(req.ModelID, req.Provider).Capabilities.MaxOutput
	}
	if available > 0 && available < required {
		return nil, &memoryOutputBudgetError{
			ModelID:         req.ModelID,
			MemoryMaxChars:  limits.MaxChars,
			RequiredTokens:  required,
			AvailableTokens: available,
		}
	}

	modelReq := taskModelRequest(req, required)
	if modelReq.ModelMaxOutput <= 0 && available > 0 {
		modelReq.ModelMaxOutput = available
	}
	// Memory maintenance is a bounded structured-output task. Optional thinking
	// would consume the same completion allowance and, for Anthropic manual
	// thinking, can even expand max_tokens beyond the task-owned budget.
	modelReq.SuppressThinking = true
	return modelReq, nil
}

func memoryMaintenanceTaskErrorType(err error) string {
	switch {
	case errors.Is(err, ErrMemoryMaintenanceOutputBudgetInsufficient):
		return "memory_output_budget_insufficient"
	case errors.Is(err, ErrMemoryMaintenanceOutputLimit):
		return "memory_output_limit"
	default:
		return modelusage.ErrorType(err)
	}
}

func memoryMaintenanceOutputLimitError(raw string) error {
	return &memoryOutputLimitError{Raw: boundedFinishReason(raw)}
}
