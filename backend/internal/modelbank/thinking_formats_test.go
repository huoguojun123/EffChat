package modelbank

import (
	"strings"
	"testing"

	"github.com/huoguojun123/EffChat/internal/model"
)

func TestResolveThinkingFormatPrefersModelIDOverProvider(t *testing.T) {
	got := ResolveThinkingFormat("openai", "deepseek-ai/deepseek-v4-flash", "auto", true)
	if got != ThinkingFormatDeepSeekV4 {
		t.Fatalf("format = %q, want %q", got, ThinkingFormatDeepSeekV4)
	}
}

func TestResolveThinkingFormatAllowsManualOverride(t *testing.T) {
	got := ResolveThinkingFormat("openai", "deepseek-v4-flash", "none", true)
	if got != ThinkingFormatNone {
		t.Fatalf("format = %q, want none", got)
	}
}

func TestResolveThinkingFormatFallsBackWhenManualFormatDoesNotMatch(t *testing.T) {
	cases := []struct {
		name       string
		provider   string
		modelID    string
		configured string
		reasoning  bool
		want       ThinkingFormat
	}{
		{name: "wrong model family falls back to none", provider: "openai", modelID: "gpt-4o", configured: "deepseek_v4", reasoning: true, want: ThinkingFormatNone},
		{name: "wrong provider falls back to auto", provider: "openai", modelID: "deepseek-v4-flash", configured: "gemini_thinking", reasoning: true, want: ThinkingFormatDeepSeekV4},
		{name: "anthropic manual budget is accepted by provider", provider: "anthropic", modelID: "claude-sonnet-4-5", configured: "anthropic_budget", reasoning: false, want: ThinkingFormatAnthropicBudget},
		{name: "claude channel key accepts manual thinking by fuzzy provider", provider: "claude", modelID: "sonnet-4-5", configured: "anthropic_budget", reasoning: false, want: ThinkingFormatAnthropicBudget},
		{name: "claude model id accepts manual thinking by fuzzy model id", provider: "my-gateway", modelID: "claude-sonnet-4-5", configured: "anthropic_budget", reasoning: false, want: ThinkingFormatAnthropicBudget},
		{name: "gemini manual thinking is accepted by provider and model", provider: "google", modelID: "gemini-2.5-pro", configured: "gemini_thinking", reasoning: false, want: ThinkingFormatGeminiThinking},
		{name: "gemini channel key accepts manual thinking by fuzzy model id", provider: "gemini", modelID: "Gemini-3.5-Flash", configured: "gemini_thinking", reasoning: false, want: ThinkingFormatGeminiThinking},
		{name: "custom gateway accepts qwen thinking by fuzzy model id", provider: "my-gateway", modelID: "Qwen3-Max", configured: "dashscope_qwen", reasoning: false, want: ThinkingFormatDashScopeQwen},
		{name: "custom gateway accepts openai reasoning by fuzzy model id", provider: "my-gateway", modelID: "gpt-5.1", configured: "openai_reasoning_effort", reasoning: false, want: ThinkingFormatOpenAIReasoningEffort},
		{name: "gpt 5.6 legacy format falls back to dedicated format", provider: "openai", modelID: "gpt-5.6", configured: "openai_reasoning_effort", reasoning: true, want: ThinkingFormatOpenAIGPT56},
		{name: "gpt 5.5 does not accept dedicated format", provider: "openai", modelID: "gpt-5.5", configured: "openai_gpt_5_6", reasoning: true, want: ThinkingFormatOpenAIReasoningEffort},
		{name: "google adapter accepts gemini manual thinking by display name", provider: "my-gateway", modelID: "flash-latest", configured: "gemini_thinking", reasoning: false, want: ThinkingFormatGeminiThinking},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			displayName := ""
			adapter := ""
			if strings.Contains(tc.name, "display name") {
				displayName = "Gemini-3.5-Flash"
				adapter = "google"
			}
			got := ResolveThinkingFormatWithContext(tc.provider, adapter, tc.modelID, displayName, tc.configured, tc.reasoning)
			if got != tc.want {
				t.Fatalf("format = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveThinkingFormatDisablesDeepSeekV4WhenReasoningOff(t *testing.T) {
	got := ResolveThinkingFormat("openai", "deepseek-v4-flash", "auto", false)
	if got != ThinkingFormatDeepSeekV4Disabled {
		t.Fatalf("format = %q, want %q", got, ThinkingFormatDeepSeekV4Disabled)
	}
}

func TestApplyThinkingRuntimeMetadata(t *testing.T) {
	m := ApplyThinkingRuntimeMetadata(&model.Model{
		ID:             "deepseek-v4-flash",
		DisplayName:    "DeepSeek V4 Flash",
		Provider:       "openai",
		Reasoning:      true,
		ThinkingFormat: "gemini_thinking",
	})
	if m.ResolvedThinkingFormat != string(ThinkingFormatDeepSeekV4) {
		t.Fatalf("resolved format = %q, want deepseek_v4", m.ResolvedThinkingFormat)
	}
	if m.DefaultThinkingEffort != string(ThinkingEffortHigh) {
		t.Fatalf("default effort = %q, want high", m.DefaultThinkingEffort)
	}
	if got := len(m.ThinkingEffortOptions); got != 3 {
		t.Fatalf("options len = %d, want 3", got)
	}
}

func TestResolveThinkingFormatKnownFamilies(t *testing.T) {
	cases := []struct {
		name      string
		provider  string
		modelID   string
		reasoning bool
		want      ThinkingFormat
	}{
		{name: "openai reasoning", provider: "openai", modelID: "gpt-5.1", reasoning: true, want: ThinkingFormatOpenAIReasoningEffort},
		{name: "gpt 5.6 reasoning", provider: "openai", modelID: "gpt-5.6", reasoning: true, want: ThinkingFormatOpenAIGPT56},
		{name: "qwen thinking", provider: "openai", modelID: "qwen3-max", reasoning: true, want: ThinkingFormatDashScopeQwen},
		{name: "qwen non-reasoning does not auto-enable thinking", provider: "openai", modelID: "Qwen/Qwen3-VL-32B-Instruct", reasoning: false, want: ThinkingFormatNone},
		{name: "gemini thinking", provider: "google", modelID: "gemini-2.5-pro", reasoning: true, want: ThinkingFormatGeminiThinking},
		{name: "gemini uppercase fuzzy thinking", provider: "gemini", modelID: "Gemini-3.5-Flash", reasoning: false, want: ThinkingFormatGeminiThinking},
		{name: "claude fuzzy thinking", provider: "claude", modelID: "Claude-Sonnet-4.5", reasoning: false, want: ThinkingFormatAnthropicBudget},
		{name: "claude adaptive fuzzy thinking", provider: "anthropic", modelID: "claude-5-sonnet", reasoning: false, want: ThinkingFormatAnthropicAdaptive},
		{name: "claude canonical sonnet 5", provider: "anthropic", modelID: "claude-sonnet-5", reasoning: false, want: ThinkingFormatAnthropicAdaptive},
		{name: "claude canonical opus 4.8", provider: "anthropic", modelID: "claude-opus-4-8", reasoning: false, want: ThinkingFormatAnthropicAdaptive},
		{name: "claude 3.5 remains manual budget", provider: "anthropic", modelID: "claude-3-5-haiku-latest", reasoning: false, want: ThinkingFormatAnthropicBudget},
		{name: "openai reasoning through custom gateway", provider: "my-gateway", modelID: "gpt-5.1", reasoning: true, want: ThinkingFormatOpenAIReasoningEffort},
		{name: "grok reasoning", provider: "xai", modelID: "grok-4.5", reasoning: true, want: ThinkingFormatXAIGrok},
		{name: "grok 4.6 reasoning", provider: "xai", modelID: "grok-4.6", reasoning: true, want: ThinkingFormatXAIGrok},
		{name: "grok multi agent remains unsupported", provider: "xai", modelID: "grok-4.20-multi-agent", reasoning: true, want: ThinkingFormatNone},
		{name: "grok explicit non reasoning remains unsupported", provider: "xai", modelID: "grok-4-fast-non-reasoning", reasoning: true, want: ThinkingFormatNone},
		{name: "glm thinking", provider: "zhipu", modelID: "glm-4.5", reasoning: true, want: ThinkingFormatGLMThinking},
		{name: "minimax m3 thinking", provider: "minimax", modelID: "MiniMax-M3", reasoning: true, want: ThinkingFormatMiniMaxThinking},
		{name: "minimax m2 thinking", provider: "minimax", modelID: "MiniMax-M2.7", reasoning: true, want: ThinkingFormatMiniMaxThinking},
		{name: "doubao thinking", provider: "volcengine", modelID: "doubao-seed-2-0-pro", reasoning: true, want: ThinkingFormatVolcengineThinking},
		{name: "unsupported", provider: "openai", modelID: "gpt-4o", reasoning: true, want: ThinkingFormatNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveThinkingFormat(tc.provider, tc.modelID, "auto", tc.reasoning)
			if got != tc.want {
				t.Fatalf("format = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestVendorThinkingEffortSemantics(t *testing.T) {
	cases := []struct {
		name      string
		format    ThinkingFormat
		modelID   string
		requested string
		want      ThinkingEffort
	}{
		{name: "grok cannot disable ordinary reasoning", format: ThinkingFormatXAIGrok, modelID: "grok-4.5", requested: "none", want: ThinkingEffortHigh},
		{name: "grok keeps medium", format: ThinkingFormatXAIGrok, modelID: "grok-4.5", requested: "medium", want: ThinkingEffortMedium},
		{name: "grok 4.5 rejects xhigh", format: ThinkingFormatXAIGrok, modelID: "grok-4.5", requested: "xhigh", want: ThinkingEffortHigh},
		{name: "grok 4.6 keeps xhigh", format: ThinkingFormatXAIGrok, modelID: "grok-4.6", requested: "xhigh", want: ThinkingEffortXHigh},
		{name: "glm accepts disabled", format: ThinkingFormatGLMThinking, modelID: "glm-4.5", requested: "none", want: ThinkingEffortNone},
		{name: "glm normalizes enabled", format: ThinkingFormatGLMThinking, modelID: "glm-4.5", requested: "low", want: ThinkingEffortHigh},
		{name: "minimax m3 accepts disabled", format: ThinkingFormatMiniMaxThinking, modelID: "MiniMax-M3", requested: "none", want: ThinkingEffortNone},
		{name: "minimax m3 defaults adaptive", format: ThinkingFormatMiniMaxThinking, modelID: "MiniMax-M3", requested: "", want: ThinkingEffortMedium},
		{name: "minimax m2 has no selectable effort", format: ThinkingFormatMiniMaxThinking, modelID: "MiniMax-M2.7", requested: "high", want: ThinkingEffortAuto},
		{name: "doubao accepts disabled", format: ThinkingFormatVolcengineThinking, modelID: "doubao-seed-2-0-pro", requested: "none", want: ThinkingEffortNone},
		{name: "doubao keeps high", format: ThinkingFormatVolcengineThinking, modelID: "doubao-seed-2-0-pro", requested: "high", want: ThinkingEffortHigh},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveThinkingEffortForModel(tc.format, tc.modelID, tc.requested); got != tc.want {
				t.Fatalf("ResolveThinkingEffortForModel(%q, %q, %q) = %q, want %q", tc.format, tc.modelID, tc.requested, got, tc.want)
			}
		})
	}

	if got := ThinkingEffortOptionsForModel(ThinkingFormatMiniMaxThinking, "MiniMax-M2.7"); len(got) != 0 {
		t.Fatalf("MiniMax M2 options = %#v, want no selector", got)
	}
	if got := ThinkingEffortOptionsForModel(ThinkingFormatGLMThinking, "glm-4.5"); len(got) != 2 || got[0].Value != "none" || got[1].Value != "high" {
		t.Fatalf("GLM options = %#v", got)
	}
	if got := ThinkingEffortOptionsForModel(ThinkingFormatXAIGrok, "grok-4.6"); len(got) != 4 || got[3].Value != "xhigh" {
		t.Fatalf("Grok 4.6 options = %#v", got)
	}
	if got := ThinkingEffortOptionsForModel(ThinkingFormatXAIGrok, "grok-4.5"); len(got) != 3 {
		t.Fatalf("Grok 4.5 options = %#v", got)
	}
}

func TestKnownThinkingModelDetection(t *testing.T) {
	for _, tc := range []struct {
		provider string
		modelID  string
	}{
		{provider: "xai", modelID: "grok-4.5"},
		{provider: "zhipu", modelID: "glm-4.5"},
		{provider: "minimax", modelID: "MiniMax-M3"},
		{provider: "volcengine", modelID: "doubao-seed-2-0-pro"},
	} {
		if !IsKnownThinkingModel(tc.provider, tc.modelID, "") {
			t.Fatalf("%s should be recognized as a thinking model", tc.modelID)
		}
	}
	if IsKnownThinkingModel("xai", "grok-4.20-multi-agent", "") {
		t.Fatal("Grok multi-agent must not receive the ordinary reasoning format")
	}
	if IsKnownThinkingModel("xai", "grok-4-fast-non-reasoning", "") {
		t.Fatal("Grok non-reasoning variants must not receive the reasoning format")
	}
}

func TestQwenThinkingLifecycleCapabilities(t *testing.T) {
	for _, id := range []string{"qwen3.7-plus", "qwen3.6-plus"} {
		if !QwenThinkingCanDisable(id) || !QwenPreservesThinkingHistory(id) {
			t.Fatalf("%s should support toggle and preserve_thinking", id)
		}
	}
	for _, id := range []string{"qwq-plus", "qwen3.7-max-preview", "qwen3-next-80b-a3b-thinking"} {
		if QwenThinkingCanDisable(id) {
			t.Fatalf("thinking-only model %s must not expose disable", id)
		}
	}
	if QwenPreservesThinkingHistory("qwen3.5-plus") {
		t.Fatal("Qwen 3.5 must not receive the newer preserve_thinking field")
	}
}

func TestResolveThinkingEffortPerFormat(t *testing.T) {
	cases := []struct {
		name      string
		format    ThinkingFormat
		requested string
		want      ThinkingEffort
	}{
		{name: "openai high", format: ThinkingFormatOpenAIReasoningEffort, requested: "high", want: ThinkingEffortHigh},
		{name: "openai default", format: ThinkingFormatOpenAIReasoningEffort, requested: "", want: ThinkingEffortMedium},
		{name: "deepseek max", format: ThinkingFormatDeepSeekV4, requested: "max", want: ThinkingEffortMax},
		{name: "deepseek low", format: ThinkingFormatDeepSeekV4, requested: "low", want: ThinkingEffortLow},
		{name: "deepseek medium coerces high", format: ThinkingFormatDeepSeekV4, requested: "medium", want: ThinkingEffortHigh},
		{name: "budget format high", format: ThinkingFormatDashScopeQwen, requested: "high", want: ThinkingEffortHigh},
		{name: "adaptive effort low", format: ThinkingFormatAnthropicAdaptive, requested: "low", want: ThinkingEffortLow},
		{name: "none ignores effort", format: ThinkingFormatNone, requested: "high", want: ThinkingEffortAuto},
		{name: "invalid uses default", format: ThinkingFormatGeminiThinking, requested: "turbo", want: ThinkingEffortMedium},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveThinkingEffort(tc.format, tc.requested)
			if got != tc.want {
				t.Fatalf("effort = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGPT56ThinkingEfforts(t *testing.T) {
	wants := []ThinkingEffort{
		ThinkingEffortNone,
		ThinkingEffortLow,
		ThinkingEffortMedium,
		ThinkingEffortHigh,
		ThinkingEffortXHigh,
		ThinkingEffortMax,
	}
	options := ThinkingEffortOptionsForModel(ThinkingFormatOpenAIGPT56, "gpt-5.6-terra")
	if len(options) != len(wants) {
		t.Fatalf("options = %d, want %d", len(options), len(wants))
	}
	for i, want := range wants {
		if options[i].Value != string(want) {
			t.Fatalf("option[%d] = %q, want %q", i, options[i].Value, want)
		}
		if got := ResolveThinkingEffortForModel(ThinkingFormatOpenAIGPT56, "gpt-5.6", string(want)); got != want {
			t.Fatalf("resolved effort = %q, want %q", got, want)
		}
	}
	if got := ResolveThinkingEffortForModel(ThinkingFormatOpenAIReasoningEffort, "gpt-5.5", "max"); got != ThinkingEffortMedium {
		t.Fatalf("legacy GPT effort = %q, want medium", got)
	}
}

func TestThinkingEffortOptionsDescribeModelFamilies(t *testing.T) {
	cases := []struct {
		name     string
		format   ThinkingFormat
		modelID  string
		contains []string
	}{
		{
			name:     "openai",
			format:   ThinkingFormatOpenAIReasoningEffort,
			contains: []string{"gpt-5", "gpt-oss", "o 系列", "reasoning_effort"},
		},
		{
			name:     "deepseek",
			format:   ThinkingFormatDeepSeekV4,
			contains: []string{"deepseek-v4", "reasoning_effort=low", "thinking.type=enabled", "reasoning_effort=max"},
		},
		{
			name:     "qwen",
			format:   ThinkingFormatDashScopeQwen,
			contains: []string{"QwQ", "Qwen3/3.5/3.6/3.7", "enable_thinking", "thinking_budget"},
		},
		{
			name:     "gemini 2.5",
			format:   ThinkingFormatGeminiThinking,
			modelID:  "gemini-2.5-pro",
			contains: []string{"Gemini 2.5", "ThinkingConfig.thinkingBudget"},
		},
		{
			name:     "gemini 3.7",
			format:   ThinkingFormatGeminiThinking,
			modelID:  "gemini-3.7-flash",
			contains: []string{"Gemini 3.x", "ThinkingConfig.thinkingLevel"},
		},
		{
			name:     "anthropic manual budget",
			format:   ThinkingFormatAnthropicBudget,
			contains: []string{"Claude 4.5", "manual budget_tokens", "thinking.budget_tokens"},
		},
		{
			name:     "anthropic adaptive",
			format:   ThinkingFormatAnthropicAdaptive,
			contains: []string{"Claude 4.6+", "Fable/Mythos", "output_config.effort"},
		},
		{
			name:     "grok",
			format:   ThinkingFormatXAIGrok,
			modelID:  "grok-4.6",
			contains: []string{"Grok 标准推理模型", "reasoning_effort", "Grok 4.6"},
		},
		{
			name:     "glm",
			format:   ThinkingFormatGLMThinking,
			contains: []string{"GLM 4.5+", "thinking.type=enabled", "thinking.type=disabled"},
		},
		{
			name:     "minimax",
			format:   ThinkingFormatMiniMaxThinking,
			contains: []string{"MiniMax M3", "thinking.type=adaptive", "reasoning_content"},
		},
		{
			name:     "doubao",
			format:   ThinkingFormatVolcengineThinking,
			contains: []string{"豆包 Seed", "thinking.type=enabled", "reasoning_effort"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var joined strings.Builder
			for _, opt := range ThinkingEffortOptionsForModel(tc.format, tc.modelID) {
				joined.WriteString(opt.Description)
				joined.WriteString("\n")
			}
			got := joined.String()
			for _, want := range tc.contains {
				if !strings.Contains(got, want) {
					t.Fatalf("description for %s does not contain %q:\n%s", tc.format, want, got)
				}
			}
		})
	}
}

func TestResolveGeminiThinkingContract(t *testing.T) {
	cases := []struct {
		modelID string
		want    GeminiThinkingContract
		omit    bool
	}{
		{modelID: "gemini-2.5-pro", want: GeminiThinkingBudget},
		{modelID: "models/gemini-3-flash-preview", want: GeminiThinkingLevel, omit: true},
		{modelID: "gemini-3.5-flash", want: GeminiThinkingLevel, omit: true},
		{modelID: "gemini-3.6-flash", want: GeminiThinkingLevel, omit: true},
		{modelID: "gemini-3.7-flash", want: GeminiThinkingLevel, omit: true},
		{modelID: "gemini-3.8-unverified", want: GeminiThinkingUnknown},
		{modelID: "custom-google-model", want: GeminiThinkingUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.modelID, func(t *testing.T) {
			if got := ResolveGeminiThinkingContract(tc.modelID); got != tc.want {
				t.Fatalf("contract = %v, want %v", got, tc.want)
			}
			if got := GeminiOmitsSamplingParameters(tc.modelID); got != tc.omit {
				t.Fatalf("omit sampling = %t, want %t", got, tc.omit)
			}
		})
	}
}

func TestAnthropicAdaptiveEffortsFollowModelCapabilities(t *testing.T) {
	cases := []struct {
		modelID string
		want    []ThinkingEffort
	}{
		{modelID: "claude-sonnet-4-6", want: []ThinkingEffort{ThinkingEffortLow, ThinkingEffortMedium, ThinkingEffortHigh, ThinkingEffortMax}},
		{modelID: "claude-opus-4-8", want: []ThinkingEffort{ThinkingEffortLow, ThinkingEffortMedium, ThinkingEffortHigh, ThinkingEffortXHigh, ThinkingEffortMax}},
		{modelID: "claude-sonnet-5", want: []ThinkingEffort{ThinkingEffortLow, ThinkingEffortMedium, ThinkingEffortHigh, ThinkingEffortXHigh, ThinkingEffortMax}},
		{modelID: "claude-fable-5", want: []ThinkingEffort{ThinkingEffortLow, ThinkingEffortMedium, ThinkingEffortHigh, ThinkingEffortXHigh, ThinkingEffortMax}},
	}
	for _, tc := range cases {
		t.Run(tc.modelID, func(t *testing.T) {
			options := ThinkingEffortOptionsForModel(ThinkingFormatAnthropicAdaptive, tc.modelID)
			if len(options) != len(tc.want) {
				t.Fatalf("options = %#v, want %v entries", options, len(tc.want))
			}
			for i, want := range tc.want {
				if options[i].Value != string(want) {
					t.Fatalf("option[%d] = %q, want %q", i, options[i].Value, want)
				}
				if got := ResolveThinkingEffortForModel(ThinkingFormatAnthropicAdaptive, tc.modelID, string(want)); got != want {
					t.Fatalf("resolved %q = %q, want %q", want, got, want)
				}
			}
		})
	}
	if got := ResolveThinkingEffortForModel(ThinkingFormatAnthropicAdaptive, "claude-sonnet-4-6", "xhigh"); got != ThinkingEffortMedium {
		t.Fatalf("Claude 4.6 xhigh = %q, want medium fallback", got)
	}
}
