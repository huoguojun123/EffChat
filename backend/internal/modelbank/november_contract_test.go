package modelbank

import (
	"testing"

	"github.com/huoguojun123/EffChat/internal/model"
)

func TestNovemberVerifiedCandidates(t *testing.T) {
	models := builtinModels()
	for _, id := range []string{"gpt-6.1-terra", "claude-fable-5-2"} {
		info := models[id]
		if info == nil || !info.Capabilities.Vision || !info.Capabilities.ToolUse || info.Capabilities.MaxOutput != 128000 {
			t.Fatalf("missing verified candidate %s: %+v", id, info)
		}
	}
	for _, adapter := range []string{"openai_compatible", "openai_responses"} {
		profile := RuntimeProfileForModelWithAdapter(&model.Model{ID: "gpt-6.1-terra", Provider: "openai", Reasoning: true, ToolUse: true}, adapter)
		if len(profile.ThinkingEffortOptions) != 6 || profile.DefaultThinkingEffort != "medium" || profile.SupportsTools != (adapter == "openai_responses") {
			t.Fatalf("Terra %s profile = %+v", adapter, profile)
		}
	}
}
