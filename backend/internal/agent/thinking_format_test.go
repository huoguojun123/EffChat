package agent

import (
	"testing"

	"github.com/cloudwego/eino-ext/components/model/claude"
	"github.com/cloudwego/eino-ext/components/model/gemini"
	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"google.golang.org/genai"
)

func TestApplyOpenAICompatibleThinkingDeepSeekViaOpenAI(t *testing.T) {
	cfg := &openai.ChatModelConfig{Model: "deepseek-v4-flash"}
	applyOpenAICompatibleThinking(&ChatRequest{
		Provider:  "openai",
		ModelID:   "deepseek-v4-flash",
		Reasoning: true,
	}, cfg)

	thinking, ok := cfg.ExtraFields["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" {
		t.Fatalf("thinking = %#v, want enabled", cfg.ExtraFields["thinking"])
	}
	if got := cfg.ExtraFields["reasoning_effort"]; got != "high" {
		t.Fatalf("reasoning_effort = %#v, want high", got)
	}
}

func TestApplyOpenAICompatibleThinkingDeepSeekMax(t *testing.T) {
	cfg := &openai.ChatModelConfig{Model: "deepseek-v4-flash"}
	applyOpenAICompatibleThinking(&ChatRequest{
		Provider:       "openai",
		ModelID:        "deepseek-v4-flash",
		Reasoning:      true,
		ThinkingEffort: string(modelbank.ThinkingEffortMax),
	}, cfg)

	if got := cfg.ExtraFields["reasoning_effort"]; got != "max" {
		t.Fatalf("reasoning_effort = %#v, want max", got)
	}
}

func TestApplyOpenAICompatibleThinkingDeepSeekLow(t *testing.T) {
	cfg := &openai.ChatModelConfig{Model: "deepseek-v4-flash"}
	applyOpenAICompatibleThinking(&ChatRequest{
		Provider:       "openai",
		ModelID:        "deepseek-v4-flash",
		Reasoning:      true,
		ThinkingEffort: string(modelbank.ThinkingEffortLow),
	}, cfg)

	if got := cfg.ExtraFields["reasoning_effort"]; got != "low" {
		t.Fatalf("reasoning_effort = %#v, want low", got)
	}
}

func TestApplyOpenAICompatibleThinkingOpenAIHigh(t *testing.T) {
	cfg := &openai.ChatModelConfig{Model: "gpt-5.1"}
	applyOpenAICompatibleThinking(&ChatRequest{
		Provider:       "openai",
		ModelID:        "gpt-5.1",
		Reasoning:      true,
		ThinkingEffort: string(modelbank.ThinkingEffortHigh),
	}, cfg)
	if cfg.ReasoningEffort != openai.ReasoningEffortLevelHigh {
		t.Fatalf("reasoning effort = %q, want high", cfg.ReasoningEffort)
	}
}

func TestApplyOpenAICompatibleThinkingSupportsGPT56ExtendedEfforts(t *testing.T) {
	for _, effort := range []string{"none", "xhigh", "max"} {
		cfg := &openai.ChatModelConfig{Model: "gpt-5.6"}
		applyOpenAICompatibleThinking(&ChatRequest{
			Provider:       "openai",
			ModelID:        "gpt-5.6",
			Reasoning:      true,
			ThinkingFormat: string(modelbank.ThinkingFormatOpenAIGPT56),
			ThinkingEffort: effort,
		}, cfg)
		if got := cfg.ExtraFields["reasoning_effort"]; got != effort {
			t.Fatalf("effort %q encoded as %#v", effort, got)
		}
	}
}

func TestOpenAIResponsesReasoningSupportsGPT56Max(t *testing.T) {
	reasoning := openAIResponsesReasoning(&ChatRequest{
		Provider:       "openai",
		ModelID:        "gpt-5.6",
		Reasoning:      true,
		ThinkingFormat: string(modelbank.ThinkingFormatOpenAIGPT56),
		ThinkingEffort: string(modelbank.ThinkingEffortMax),
	})
	if reasoning == nil || reasoning.Effort != "max" {
		t.Fatalf("Responses reasoning = %#v, want max", reasoning)
	}
}

func TestApplyOpenAITokenLimitUsesCompletionTokensForReasoning(t *testing.T) {
	reasoning := &openai.ChatModelConfig{}
	applyOpenAITokenLimit(&ChatRequest{Provider: "openai", ModelID: "gpt-5.6", Reasoning: true, ThinkingFormat: "auto", MaxTokens: 4096}, reasoning)
	if reasoning.MaxCompletionTokens == nil || *reasoning.MaxCompletionTokens != 4096 || reasoning.MaxTokens != nil {
		t.Fatalf("reasoning token fields = max=%v completion=%v", reasoning.MaxTokens, reasoning.MaxCompletionTokens)
	}
	regular := &openai.ChatModelConfig{}
	applyOpenAITokenLimit(&ChatRequest{Provider: "openai", ModelID: "gpt-4o", MaxTokens: 2048}, regular)
	if regular.MaxTokens == nil || *regular.MaxTokens != 2048 || regular.MaxCompletionTokens != nil {
		t.Fatalf("regular token fields = max=%v completion=%v", regular.MaxTokens, regular.MaxCompletionTokens)
	}
}

func TestApplyOpenAICompatibleThinkingManualNone(t *testing.T) {
	cfg := &openai.ChatModelConfig{Model: "deepseek-v4-flash"}
	applyOpenAICompatibleThinking(&ChatRequest{
		Provider:       "openai",
		ModelID:        "deepseek-v4-flash",
		Reasoning:      true,
		ThinkingFormat: string(modelbank.ThinkingFormatNone),
	}, cfg)
	if len(cfg.ExtraFields) != 0 || cfg.ReasoningEffort != "" {
		t.Fatalf("manual none should not set thinking params: extra=%#v effort=%q", cfg.ExtraFields, cfg.ReasoningEffort)
	}
}

func TestApplyOpenAICompatibleThinkingQwen(t *testing.T) {
	cfg := &openai.ChatModelConfig{Model: "qwen3.7-plus"}
	applyOpenAICompatibleThinking(&ChatRequest{Provider: "openai", ModelID: "qwen3.7-plus", Reasoning: true, ThinkingEffort: "high"}, cfg)
	if got := cfg.ExtraFields["enable_thinking"]; got != true {
		t.Fatalf("enable_thinking = %#v, want true", got)
	}
	if got := cfg.ExtraFields["thinking_budget"]; got != 8192 {
		t.Fatalf("thinking_budget = %#v, want 8192", got)
	}
	if got := cfg.ExtraFields["preserve_thinking"]; got != true {
		t.Fatalf("preserve_thinking = %#v, want true", got)
	}
}

func TestApplyOpenAICompatibleThinkingQwenUtilityLifecycle(t *testing.T) {
	t.Run("hybrid disables thinking", func(t *testing.T) {
		cfg := &openai.ChatModelConfig{Model: "qwen3.7-plus"}
		applyOpenAICompatibleThinking(&ChatRequest{Provider: "qwen", ModelID: "qwen3.7-plus", Reasoning: true, SuppressThinking: true}, cfg)
		if got := cfg.ExtraFields["enable_thinking"]; got != false {
			t.Fatalf("enable_thinking = %#v, want false", got)
		}
	})

	t.Run("thinking only uses minimum budget", func(t *testing.T) {
		cfg := &openai.ChatModelConfig{Model: "qwen3.7-max-preview"}
		applyOpenAICompatibleThinking(&ChatRequest{Provider: "qwen", ModelID: "qwen3.7-max-preview", Reasoning: true, SuppressThinking: true}, cfg)
		if _, ok := cfg.ExtraFields["enable_thinking"]; ok {
			t.Fatalf("thinking-only model must not receive enable_thinking: %#v", cfg.ExtraFields)
		}
		if got := cfg.ExtraFields["thinking_budget"]; got != 1024 {
			t.Fatalf("thinking_budget = %#v, want 1024", got)
		}
	})
}

func TestApplyOpenAICompatibleThinkingVendorFormats(t *testing.T) {
	t.Run("grok", func(t *testing.T) {
		cfg := &openai.ChatModelConfig{Model: "grok-4.6"}
		applyOpenAICompatibleThinking(&ChatRequest{
			Provider:       "xai",
			ModelID:        "grok-4.6",
			Reasoning:      true,
			ThinkingEffort: "xhigh",
		}, cfg)
		if got := cfg.ExtraFields["reasoning_effort"]; got != "xhigh" {
			t.Fatalf("reasoning_effort = %#v, want xhigh", got)
		}
	})

	t.Run("grok utility uses minimum effort", func(t *testing.T) {
		cfg := &openai.ChatModelConfig{Model: "grok-4.6"}
		applyOpenAICompatibleThinking(&ChatRequest{
			Provider: "xai", ModelID: "grok-4.6", Reasoning: true, SuppressThinking: true,
		}, cfg)
		if got := cfg.ExtraFields["reasoning_effort"]; got != "low" {
			t.Fatalf("reasoning_effort = %#v, want low", got)
		}
	})

	t.Run("glm", func(t *testing.T) {
		cfg := &openai.ChatModelConfig{Model: "glm-4.5"}
		applyOpenAICompatibleThinking(&ChatRequest{
			Provider:       "zhipu",
			ModelID:        "glm-4.5",
			Reasoning:      true,
			ThinkingEffort: "none",
		}, cfg)
		thinking, ok := cfg.ExtraFields["thinking"].(map[string]any)
		if !ok || thinking["type"] != "disabled" {
			t.Fatalf("thinking = %#v, want disabled", cfg.ExtraFields["thinking"])
		}
	})

	t.Run("minimax m3", func(t *testing.T) {
		cfg := &openai.ChatModelConfig{Model: "MiniMax-M3"}
		applyOpenAICompatibleThinking(&ChatRequest{
			Provider:  "minimax",
			ModelID:   "MiniMax-M3",
			Reasoning: true,
		}, cfg)
		if got := cfg.ExtraFields["reasoning_split"]; got != true {
			t.Fatalf("reasoning_split = %#v, want true", got)
		}
		thinking, ok := cfg.ExtraFields["thinking"].(map[string]any)
		if !ok || thinking["type"] != "adaptive" {
			t.Fatalf("thinking = %#v, want adaptive", cfg.ExtraFields["thinking"])
		}
	})

	t.Run("minimax m2", func(t *testing.T) {
		cfg := &openai.ChatModelConfig{Model: "MiniMax-M2.7"}
		applyOpenAICompatibleThinking(&ChatRequest{
			Provider:       "minimax",
			ModelID:        "MiniMax-M2.7",
			Reasoning:      true,
			ThinkingEffort: "high",
		}, cfg)
		if got := cfg.ExtraFields["reasoning_split"]; got != true {
			t.Fatalf("reasoning_split = %#v, want true", got)
		}
		if _, ok := cfg.ExtraFields["thinking"]; ok {
			t.Fatalf("MiniMax M2 must not receive unsupported thinking field: %#v", cfg.ExtraFields)
		}
	})

	t.Run("doubao", func(t *testing.T) {
		cfg := &openai.ChatModelConfig{Model: "doubao-seed-2-0-pro"}
		applyOpenAICompatibleThinking(&ChatRequest{
			Provider:       "volcengine",
			ModelID:        "doubao-seed-2-0-pro",
			Reasoning:      true,
			ThinkingEffort: "high",
		}, cfg)
		thinking, ok := cfg.ExtraFields["thinking"].(map[string]any)
		if !ok || thinking["type"] != "enabled" {
			t.Fatalf("thinking = %#v, want enabled", cfg.ExtraFields["thinking"])
		}
		if got := cfg.ExtraFields["reasoning_effort"]; got != "high" {
			t.Fatalf("reasoning_effort = %#v, want high", got)
		}
	})
}

func TestApplyClaudeThinkingExplicitBudgetDefaultMedium(t *testing.T) {
	cfg := &claude.Config{Model: "claude-sonnet-4-5", MaxTokens: 0}
	applyClaudeThinking(&ChatRequest{
		Provider:       "anthropic",
		ModelID:        "claude-sonnet-4-5",
		Reasoning:      true,
		ThinkingFormat: string(modelbank.ThinkingFormatAnthropicBudget),
	}, cfg)
	if cfg.Thinking == nil || !cfg.Thinking.Enable || cfg.Thinking.BudgetTokens != 8192 {
		t.Fatalf("thinking = %#v, want enabled budget", cfg.Thinking)
	}
	if cfg.MaxTokens <= cfg.Thinking.BudgetTokens {
		t.Fatalf("max tokens = %d should exceed budget %d", cfg.MaxTokens, cfg.Thinking.BudgetTokens)
	}
}

func TestApplyClaudeThinkingAdaptiveHigh(t *testing.T) {
	cfg := &claude.Config{Model: "claude-sonnet-5", MaxTokens: 0}
	applyClaudeThinking(&ChatRequest{
		Provider:       "anthropic",
		ModelID:        "claude-sonnet-5",
		Reasoning:      true,
		ThinkingFormat: string(modelbank.ThinkingFormatAnthropicAdaptive),
		ThinkingEffort: string(modelbank.ThinkingEffortHigh),
	}, cfg)
	outputConfig, ok := cfg.AdditionalRequestFields["output_config"].(map[string]any)
	if !ok || outputConfig["effort"] != "high" {
		t.Fatalf("output_config = %#v, want effort high", cfg.AdditionalRequestFields["output_config"])
	}
	thinking, ok := cfg.AdditionalRequestFields["thinking"].(map[string]any)
	if !ok || thinking["type"] != "adaptive" || thinking["display"] != "summarized" {
		t.Fatalf("thinking = %#v, want adaptive summarized", cfg.AdditionalRequestFields["thinking"])
	}
}

func TestApplyGeminiThinkingUsesGenerationContract(t *testing.T) {
	t.Run("2.5 budget", func(t *testing.T) {
		cfg := &gemini.Config{Model: "gemini-2.5-pro"}
		applyGeminiThinking(&ChatRequest{Provider: "google", ModelID: "gemini-2.5-pro", Reasoning: true}, cfg)
		if cfg.ThinkingConfig == nil || !cfg.ThinkingConfig.IncludeThoughts {
			t.Fatalf("thinking config = %#v, want include thoughts", cfg.ThinkingConfig)
		}
		if cfg.ThinkingConfig.ThinkingBudget == nil || *cfg.ThinkingConfig.ThinkingBudget != 4096 || cfg.ThinkingConfig.ThinkingLevel != "" {
			t.Fatalf("thinking config = %#v, want budget 4096 only", cfg.ThinkingConfig)
		}
	})

	t.Run("3.7 level", func(t *testing.T) {
		cfg := &gemini.Config{Model: "gemini-3.7-flash"}
		applyGeminiThinking(&ChatRequest{
			Provider:       "google",
			ModelID:        "gemini-3.7-flash",
			Reasoning:      true,
			ThinkingEffort: string(modelbank.ThinkingEffortHigh),
		}, cfg)
		if cfg.ThinkingConfig == nil || !cfg.ThinkingConfig.IncludeThoughts || cfg.ThinkingConfig.ThinkingLevel != genai.ThinkingLevelHigh || cfg.ThinkingConfig.ThinkingBudget != nil {
			t.Fatalf("thinking config = %#v, want HIGH level only", cfg.ThinkingConfig)
		}
	})

	t.Run("unknown version is not guessed", func(t *testing.T) {
		cfg := &gemini.Config{Model: "gemini-3.8-unverified"}
		applyGeminiThinking(&ChatRequest{Provider: "google", ModelID: cfg.Model, Reasoning: true}, cfg)
		if cfg.ThinkingConfig != nil {
			t.Fatalf("unknown Gemini thinking config = %#v", cfg.ThinkingConfig)
		}
	})
}

func TestSuppressThinkingKeepsUtilityAdaptersWithinTheirOutputBudget(t *testing.T) {
	t.Run("openai compatible", func(t *testing.T) {
		cfg := &openai.ChatModelConfig{Model: "qwen3-max"}
		applyOpenAICompatibleThinking(&ChatRequest{
			Provider:         "openai",
			ModelID:          "qwen3-max",
			Reasoning:        true,
			SuppressThinking: true,
		}, cfg)
		if len(cfg.ExtraFields) != 1 || cfg.ExtraFields["enable_thinking"] != false || cfg.ReasoningEffort != "" {
			t.Fatalf("suppressed utility thinking fields = %#v effort=%q", cfg.ExtraFields, cfg.ReasoningEffort)
		}
	})

	t.Run("deepseek v4 is explicitly disabled", func(t *testing.T) {
		cfg := &openai.ChatModelConfig{Model: "deepseek-ai/DeepSeek-V4-Flash"}
		applyOpenAICompatibleThinking(&ChatRequest{
			Provider:         "deepseek",
			ModelID:          "deepseek-ai/DeepSeek-V4-Flash",
			Reasoning:        true,
			SuppressThinking: true,
		}, cfg)
		thinking, ok := cfg.ExtraFields["thinking"].(map[string]any)
		if !ok || thinking["type"] != "disabled" || cfg.ReasoningEffort != "" {
			t.Fatalf("suppressed DeepSeek fields = %#v effort=%q", cfg.ExtraFields, cfg.ReasoningEffort)
		}
	})

	t.Run("openai reasoning token field", func(t *testing.T) {
		req := &ChatRequest{
			Provider:         "openai",
			ModelID:          "gpt-5.6",
			MaxTokens:        4096,
			Reasoning:        true,
			ThinkingFormat:   string(modelbank.ThinkingFormatOpenAIGPT56),
			SuppressThinking: true,
		}
		cfg := &openai.ChatModelConfig{Model: req.ModelID}
		applyOpenAITokenLimit(req, cfg)
		applyOpenAICompatibleThinking(req, cfg)
		if cfg.MaxCompletionTokens == nil || *cfg.MaxCompletionTokens != 4096 || cfg.MaxTokens != nil {
			t.Fatalf("suppressed reasoning token fields = max:%v completion:%v", cfg.MaxTokens, cfg.MaxCompletionTokens)
		}
		if len(cfg.ExtraFields) != 0 || cfg.ReasoningEffort != "" {
			t.Fatalf("suppressed reasoning fields = %#v effort=%q", cfg.ExtraFields, cfg.ReasoningEffort)
		}
	})

	t.Run("anthropic", func(t *testing.T) {
		cfg := &claude.Config{Model: "claude-sonnet-4-5", MaxTokens: 4096}
		applyClaudeThinking(&ChatRequest{
			Provider:         "anthropic",
			ModelID:          "claude-sonnet-4-5",
			Reasoning:        true,
			SuppressThinking: true,
		}, cfg)
		if cfg.Thinking != nil || len(cfg.AdditionalRequestFields) != 0 || cfg.MaxTokens != 4096 {
			t.Fatalf("suppressed Claude config = thinking:%#v additional:%#v max:%d", cfg.Thinking, cfg.AdditionalRequestFields, cfg.MaxTokens)
		}
	})

	t.Run("anthropic adaptive uses low without display", func(t *testing.T) {
		cfg := &claude.Config{Model: "claude-fable-5", MaxTokens: 4096}
		applyClaudeThinking(&ChatRequest{
			Provider:         "anthropic",
			ModelID:          cfg.Model,
			Reasoning:        true,
			SuppressThinking: true,
		}, cfg)
		thinking, ok := cfg.AdditionalRequestFields["thinking"].(map[string]any)
		if !ok || thinking["type"] != "adaptive" {
			t.Fatalf("suppressed adaptive thinking = %#v", cfg.AdditionalRequestFields["thinking"])
		}
		if _, exists := thinking["display"]; exists {
			t.Fatalf("utility thinking must omit display: %#v", thinking)
		}
		outputConfig, _ := cfg.AdditionalRequestFields["output_config"].(map[string]any)
		if outputConfig["effort"] != "low" {
			t.Fatalf("suppressed adaptive output config = %#v", outputConfig)
		}
	})

	t.Run("gemini", func(t *testing.T) {
		cfg := &gemini.Config{Model: "gemini-2.5-pro"}
		applyGeminiThinking(&ChatRequest{
			Provider:         "google",
			ModelID:          "gemini-2.5-pro",
			Reasoning:        true,
			SuppressThinking: true,
		}, cfg)
		if cfg.ThinkingConfig != nil {
			t.Fatalf("suppressed Gemini thinking config = %#v", cfg.ThinkingConfig)
		}
	})

	t.Run("gemini 3.x uses the lowest legal level", func(t *testing.T) {
		cfg := &gemini.Config{Model: "gemini-3.7-flash"}
		applyGeminiThinking(&ChatRequest{
			Provider:         "google",
			ModelID:          cfg.Model,
			Reasoning:        true,
			SuppressThinking: true,
		}, cfg)
		if cfg.ThinkingConfig == nil || cfg.ThinkingConfig.IncludeThoughts || cfg.ThinkingConfig.ThinkingLevel != genai.ThinkingLevelLow || cfg.ThinkingConfig.ThinkingBudget != nil {
			t.Fatalf("suppressed Gemini 3.x config = %#v", cfg.ThinkingConfig)
		}
	})
}
