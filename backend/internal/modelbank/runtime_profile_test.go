package modelbank

import (
	"testing"

	"github.com/huoguojun123/EffChat/internal/model"
)

func TestRuntimeProfileSeparatesProviderAndFamily(t *testing.T) {
	m := &model.Model{
		ID:             "deepseek-v4-flash",
		Provider:       "openai",
		ToolUse:        true,
		Reasoning:      true,
		ThinkingFormat: "auto",
		SearchImpl:     string(SearchImplNone),
	}

	profile := RuntimeProfileForModel(m)
	if profile.WireProtocol != WireProtocolOpenAICompatible {
		t.Fatalf("wire_protocol = %q, want %q", profile.WireProtocol, WireProtocolOpenAICompatible)
	}
	if profile.Family != "deepseek" {
		t.Fatalf("family = %q, want deepseek", profile.Family)
	}
	if profile.ThinkingFormat != string(ThinkingFormatDeepSeekV4) {
		t.Fatalf("thinking_format = %q, want %q", profile.ThinkingFormat, ThinkingFormatDeepSeekV4)
	}
	if len(profile.ThinkingEffortOptions) == 0 || profile.DefaultThinkingEffort == "" {
		t.Fatalf("thinking effort metadata missing: %+v", profile)
	}
}

func TestApplyRuntimeProfileKeepsLegacyThinkingFields(t *testing.T) {
	m := ApplyRuntimeProfile(&model.Model{
		ID:             "gemini-2.5-pro",
		Provider:       "google",
		Vision:         true,
		ToolUse:        true,
		Reasoning:      true,
		ThinkingFormat: "auto",
		SearchImpl:     string(SearchImplParams),
	})

	if m.RuntimeProfile == nil {
		t.Fatalf("runtime_profile is nil")
	}
	if m.RuntimeProfile.Family != "gemini" || m.RuntimeProfile.WireProtocol != WireProtocolGoogleNative {
		t.Fatalf("bad runtime profile: %+v", m.RuntimeProfile)
	}
	if m.ResolvedThinkingFormat != m.RuntimeProfile.ThinkingFormat {
		t.Fatalf("legacy resolved_thinking_format = %q, profile = %q", m.ResolvedThinkingFormat, m.RuntimeProfile.ThinkingFormat)
	}
	if len(m.ThinkingEffortOptions) != len(m.RuntimeProfile.ThinkingEffortOptions) {
		t.Fatalf("legacy options and profile options differ")
	}
}

func TestRuntimeProfileUsesChannelAdapterAndDisplayName(t *testing.T) {
	m := &model.Model{
		ID:             "flash-latest",
		DisplayName:    "Gemini-3.5-Flash",
		Provider:       "my-gateway",
		ToolUse:        true,
		Reasoning:      true,
		ThinkingFormat: "gemini_thinking",
		SearchImpl:     string(SearchImplNone),
	}

	profile := RuntimeProfileForModelWithAdapter(m, "google")
	if profile.WireProtocol != WireProtocolGoogleNative {
		t.Fatalf("wire_protocol = %q, want %q", profile.WireProtocol, WireProtocolGoogleNative)
	}
	if profile.Family != "gemini" {
		t.Fatalf("family = %q, want gemini", profile.Family)
	}
	if profile.ThinkingFormat != string(ThinkingFormatGeminiThinking) {
		t.Fatalf("thinking_format = %q, want %q", profile.ThinkingFormat, ThinkingFormatGeminiThinking)
	}
}

func TestRuntimeProfileExposesOpenAIResponsesWireProtocol(t *testing.T) {
	profile := RuntimeProfileForModelWithAdapter(&model.Model{
		ID:             "gpt-5.1",
		Provider:       "openai",
		Reasoning:      true,
		ThinkingFormat: "openai_reasoning_effort",
	}, "openai_responses")
	if profile.WireProtocol != WireProtocolOpenAIResponses {
		t.Fatalf("wire_protocol = %q, want %q", profile.WireProtocol, WireProtocolOpenAIResponses)
	}
	if profile.Family != "openai" {
		t.Fatalf("family = %q, want openai", profile.Family)
	}
}

func TestRuntimeProfileExposesGPT56Efforts(t *testing.T) {
	profile := RuntimeProfileForModel(&model.Model{
		ID:             "gpt-5.6-sol",
		DisplayName:    "GPT-5.6 Sol",
		Provider:       "openai",
		Reasoning:      true,
		ThinkingFormat: "auto",
	})
	if profile.DefaultThinkingEffort != "medium" {
		t.Fatalf("default effort = %q, want medium", profile.DefaultThinkingEffort)
	}
	if profile.ThinkingFormat != string(ThinkingFormatOpenAIGPT56) {
		t.Fatalf("thinking format = %q, want %q", profile.ThinkingFormat, ThinkingFormatOpenAIGPT56)
	}
	if len(profile.ThinkingEffortOptions) != 6 {
		t.Fatalf("effort options = %d, want 6", len(profile.ThinkingEffortOptions))
	}
}

func TestRuntimeProfileExposesVendorSpecificThinkingControls(t *testing.T) {
	cases := []struct {
		name          string
		provider      string
		modelID       string
		family        string
		format        ThinkingFormat
		optionCount   int
		defaultEffort string
	}{
		{name: "grok", provider: "xai", modelID: "grok-4.5", family: "xai", format: ThinkingFormatXAIGrok, optionCount: 3, defaultEffort: "high"},
		{name: "glm", provider: "zhipu", modelID: "glm-4.5", family: "glm", format: ThinkingFormatGLMThinking, optionCount: 2, defaultEffort: "high"},
		{name: "minimax m3", provider: "minimax", modelID: "MiniMax-M3", family: "minimax", format: ThinkingFormatMiniMaxThinking, optionCount: 2, defaultEffort: "medium"},
		{name: "minimax m2", provider: "minimax", modelID: "MiniMax-M2.7", family: "minimax", format: ThinkingFormatMiniMaxThinking, optionCount: 0, defaultEffort: ""},
		{name: "doubao", provider: "volcengine", modelID: "doubao-seed-2-0-pro", family: "volcengine", format: ThinkingFormatVolcengineThinking, optionCount: 4, defaultEffort: "medium"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			profile := RuntimeProfileForModel(&model.Model{
				ID:             tc.modelID,
				Provider:       tc.provider,
				Reasoning:      true,
				ThinkingFormat: "auto",
			})
			if profile.Family != tc.family || profile.ThinkingFormat != string(tc.format) {
				t.Fatalf("profile = %+v, want family=%q format=%q", profile, tc.family, tc.format)
			}
			if len(profile.ThinkingEffortOptions) != tc.optionCount {
				t.Fatalf("options = %#v, want %d", profile.ThinkingEffortOptions, tc.optionCount)
			}
			if profile.DefaultThinkingEffort != tc.defaultEffort {
				t.Fatalf("default = %q, want %q", profile.DefaultThinkingEffort, tc.defaultEffort)
			}
		})
	}
}
