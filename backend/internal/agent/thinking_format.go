package agent

import (
	"log"

	"github.com/cloudwego/eino-ext/components/model/claude"
	"github.com/cloudwego/eino-ext/components/model/gemini"
	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/huoguojun123/effchat/internal/modelbank"
	"google.golang.org/genai"
)

const (
	defaultClaudeThinkingBudget = 16000
	defaultClaudeMaxTokens      = 20000
)

// applyOpenAICompatibleThinking 给 OpenAI-compatible 请求挂载各模型家族的官方思考字段。
//
// 关键点：这里不能只看 provider。用户可能把 DeepSeek V4 接在 provider=openai
// 或 NewAPI 的 OpenAI 兼容通道下；真正决定请求体形状的是 model_id + 管理员覆盖项。
func applyOpenAICompatibleThinking(req *ChatRequest, cfg *openai.ChatModelConfig) {
	format := modelbank.ResolveThinkingFormat(req.Provider, req.ModelID, req.ThinkingFormat, req.Reasoning)
	effort := modelbank.ResolveThinkingEffortForModel(format, req.ModelID, req.ThinkingEffort)
	switch format {
	case modelbank.ThinkingFormatOpenAIGPT56:
		setOpenAIExtraField(cfg, "reasoning_effort", string(effort))
	case modelbank.ThinkingFormatOpenAIReasoningEffort:
		cfg.ReasoningEffort = openAIReasoningEffort(effort)
	case modelbank.ThinkingFormatDeepSeekV4:
		setOpenAIExtraField(cfg, "thinking", map[string]any{"type": "enabled"})
		setOpenAIExtraField(cfg, "reasoning_effort", deepSeekReasoningEffort(effort))
	case modelbank.ThinkingFormatDeepSeekV4Disabled:
		setOpenAIExtraField(cfg, "thinking", map[string]any{"type": "disabled"})
	case modelbank.ThinkingFormatDashScopeQwen:
		setOpenAIExtraField(cfg, "enable_thinking", true)
		setOpenAIExtraField(cfg, "thinking_budget", budgetForEffort(effort, 1024, 4096, 8192))
	case modelbank.ThinkingFormatXAIGrok:
		// xAI's standard Grok reasoning models use the Chat Completions field
		// directly. Do not use the OpenAI SDK enum here: it deliberately only
		// knows OpenAI's three values and would hide vendor-specific behavior.
		setOpenAIExtraField(cfg, "reasoning_effort", string(effort))
	case modelbank.ThinkingFormatGLMThinking:
		setOpenAIExtraField(cfg, "thinking", map[string]any{"type": thinkingTypeForToggle(effort, "enabled")})
	case modelbank.ThinkingFormatMiniMaxThinking:
		// MiniMax exposes reasoning in a separate field only with this switch.
		// The OpenAI-compatible Eino adapter already maps reasoning_content into
		// schema.Message.ReasoningContent, so no parallel response path is needed.
		setOpenAIExtraField(cfg, "reasoning_split", true)
		if effort != modelbank.ThinkingEffortAuto {
			setOpenAIExtraField(cfg, "thinking", map[string]any{"type": thinkingTypeForToggle(effort, "adaptive")})
		}
	case modelbank.ThinkingFormatVolcengineThinking:
		if effort == modelbank.ThinkingEffortNone {
			setOpenAIExtraField(cfg, "thinking", map[string]any{"type": "disabled"})
		} else {
			setOpenAIExtraField(cfg, "thinking", map[string]any{"type": "enabled"})
			setOpenAIExtraField(cfg, "reasoning_effort", string(effort))
		}
	}
	logThinkingFormat(req, format, effort)
}

func applyClaudeThinking(req *ChatRequest, cfg *claude.Config) {
	format := modelbank.ResolveThinkingFormat(req.Provider, req.ModelID, req.ThinkingFormat, req.Reasoning)
	effort := modelbank.ResolveThinkingEffortForModel(format, req.ModelID, req.ThinkingEffort)
	switch format {
	case modelbank.ThinkingFormatAnthropicBudget:
		budget := budgetForEffort(effort, 4096, 8192, defaultClaudeThinkingBudget)
		if cfg.MaxTokens <= budget {
			cfg.MaxTokens = max(defaultClaudeMaxTokens, budget+4096)
		}
		cfg.Thinking = &claude.Thinking{Enable: true, BudgetTokens: budget}
	case modelbank.ThinkingFormatAnthropicAdaptive:
		if cfg.MaxTokens <= 0 {
			cfg.MaxTokens = 8192
		}
		if cfg.AdditionalRequestFields == nil {
			cfg.AdditionalRequestFields = map[string]any{}
		}
		cfg.AdditionalRequestFields["thinking"] = map[string]any{"type": "adaptive"}
		cfg.AdditionalRequestFields["output_config"] = map[string]any{"effort": string(effort)}
	}
	logThinkingFormat(req, format, effort)
}

func applyGeminiThinking(req *ChatRequest, cfg *gemini.Config) {
	format := modelbank.ResolveThinkingFormat(req.Provider, req.ModelID, req.ThinkingFormat, req.Reasoning)
	effort := modelbank.ResolveThinkingEffortForModel(format, req.ModelID, req.ThinkingEffort)
	if format == modelbank.ThinkingFormatGeminiThinking {
		budget := int32(budgetForEffort(effort, 1024, 4096, 8192))
		cfg.ThinkingConfig = &genai.ThinkingConfig{IncludeThoughts: true, ThinkingBudget: &budget}
	}
	logThinkingFormat(req, format, effort)
}

func setOpenAIExtraField(cfg *openai.ChatModelConfig, key string, value any) {
	if cfg.ExtraFields == nil {
		cfg.ExtraFields = map[string]any{}
	}
	cfg.ExtraFields[key] = value
}

func openAIReasoningEffort(effort modelbank.ThinkingEffort) openai.ReasoningEffortLevel {
	switch effort {
	case modelbank.ThinkingEffortLow:
		return openai.ReasoningEffortLevelLow
	case modelbank.ThinkingEffortHigh:
		return openai.ReasoningEffortLevelHigh
	default:
		return openai.ReasoningEffortLevelMedium
	}
}

func deepSeekReasoningEffort(effort modelbank.ThinkingEffort) string {
	if effort == modelbank.ThinkingEffortMax {
		return "max"
	}
	return "high"
}

func thinkingTypeForToggle(effort modelbank.ThinkingEffort, enabled string) string {
	if effort == modelbank.ThinkingEffortNone {
		return "disabled"
	}
	return enabled
}

func budgetForEffort(effort modelbank.ThinkingEffort, low, medium, high int) int {
	switch effort {
	case modelbank.ThinkingEffortLow:
		return low
	case modelbank.ThinkingEffortHigh:
		return high
	default:
		return medium
	}
}

func logThinkingFormat(req *ChatRequest, format modelbank.ThinkingFormat, effort modelbank.ThinkingEffort) {
	if format == modelbank.ThinkingFormatAuto || format == modelbank.ThinkingFormatNone {
		return
	}
	log.Printf("[eino] thinking_format applied: provider=%s model=%s configured=%s resolved=%s effort=%s",
		req.Provider, req.ModelID, modelbank.NormalizeThinkingFormat(req.ThinkingFormat), format, effort)
}
