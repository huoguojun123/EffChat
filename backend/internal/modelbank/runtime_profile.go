package modelbank

import (
	"strings"

	"github.com/huoguojun123/EffChat/internal/model"
)

const (
	WireProtocolOpenAICompatible = "openai-compatible"
	WireProtocolOpenAIResponses  = "openai-responses"
	WireProtocolAnthropicNative  = "anthropic-native"
	WireProtocolGoogleNative     = "google-native"
)

// RuntimeProfileForModel is the single shape that frontend and runtime callers
// should read when deciding how a model behaves at request time.
//
// It deliberately keeps provider and model family separate: provider describes
// the wire protocol/credential bucket, while family describes the model-specific
// capability rules behind that protocol.
func RuntimeProfileForModel(m *model.Model) model.ModelRuntimeProfile {
	return RuntimeProfileForModelWithAdapter(m, "")
}

func RuntimeProfileForModelWithAdapter(m *model.Model, adapter string) model.ModelRuntimeProfile {
	if m == nil {
		return model.ModelRuntimeProfile{}
	}
	format := ResolveThinkingFormatWithContext(m.Provider, adapter, m.ID, m.DisplayName, m.ThinkingFormat, m.Reasoning)
	options := ThinkingEffortOptionsForModel(format, strings.TrimSpace(m.ID+" "+m.DisplayName))
	profile := model.ModelRuntimeProfile{
		Family:                inferRuntimeFamilyWithContext(m.Provider, adapter, m.ID, m.DisplayName),
		WireProtocol:          inferWireProtocolWithAdapter(m.Provider, adapter),
		ThinkingFormat:        string(format),
		ThinkingEffortOptions: options,
		SupportsVision:        m.Vision,
		SupportsTools:         m.ToolUse,
		SearchImpl:            m.SearchImpl,
		TemperaturePolicy:     model.NormalizeTemperaturePolicy(m.TemperaturePolicy),
		TemperatureValue:      cloneFloat64Pointer(m.TemperatureValue),
		OpenAIRequestProfile:  model.CloneOpenAIRequestProfile(m.OpenAIRequestProfile),
	}
	if len(options) > 0 {
		profile.DefaultThinkingEffort = string(ResolveThinkingEffortForModel(format, m.ID, ""))
	}
	return profile
}

func ApplyRuntimeProfile(m *model.Model) *model.Model {
	return ApplyRuntimeProfileWithAdapter(m, "")
}

func ApplyRuntimeProfileWithAdapter(m *model.Model, adapter string) *model.Model {
	if m == nil {
		return nil
	}
	profile := RuntimeProfileForModelWithAdapter(m, adapter)
	m.RuntimeProfile = &profile
	m.ResolvedThinkingFormat = profile.ThinkingFormat
	m.ThinkingEffortOptions = profile.ThinkingEffortOptions
	m.DefaultThinkingEffort = profile.DefaultThinkingEffort
	return m
}

func inferWireProtocolWithAdapter(provider, adapter string) string {
	switch normalizeAdapter(adapter) {
	case "anthropic":
		return WireProtocolAnthropicNative
	case "google":
		return WireProtocolGoogleNative
	case "openai_compatible":
		return WireProtocolOpenAICompatible
	case "openai_responses":
		return WireProtocolOpenAIResponses
	}
	p := normalizeProvider(provider)
	switch {
	case p == "anthropic":
		return WireProtocolAnthropicNative
	case p == "google":
		return WireProtocolGoogleNative
	case IsOpenAICompatibleProvider(p):
		return WireProtocolOpenAICompatible
	default:
		return p
	}
}

func inferRuntimeFamilyWithContext(provider, adapter, modelID, displayName string) string {
	p := normalizeProvider(provider)
	a := normalizeAdapter(adapter)
	id := normalizeModelID(strings.TrimSpace(modelID + " " + displayName))
	switch {
	case isDeepSeekFamily(id):
		return "deepseek"
	case isQwenFamily(id):
		return "qwen"
	case isGrokReasoningModel(id):
		return "xai"
	case isGLMThinkingModel(id):
		return "glm"
	case isKimiThinkingModel(id):
		return "kimi"
	case isMiniMaxThinkingModel(id):
		return "minimax"
	case isVolcengineThinkingModel(id):
		return "volcengine"
	case isGeminiFamily(id):
		return "gemini"
	case strings.Contains(id, "claude"):
		return "claude"
	case a == "google":
		return "gemini"
	case a == "anthropic":
		return "claude"
	case isOpenAIFamily(p, id):
		return "openai"
	case p != "":
		return p
	default:
		return "unknown"
	}
}

func isDeepSeekFamily(id string) bool {
	return strings.Contains(id, "deepseek")
}

func isQwenFamily(id string) bool {
	return strings.Contains(id, "qwen") || strings.Contains(id, "qwq")
}

func isGeminiFamily(id string) bool {
	return strings.Contains(id, "gemini")
}

func isOpenAIFamily(provider, id string) bool {
	if provider == "openai" && (strings.HasPrefix(id, "gpt-") || strings.HasPrefix(id, "o1") || strings.HasPrefix(id, "o3") || strings.HasPrefix(id, "o4")) {
		return true
	}
	return provider == "openai" && id == ""
}
