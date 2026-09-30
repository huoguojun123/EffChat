package modelbank

import (
	"testing"

	"github.com/huoguojun123/EffChat/internal/model"
)

func TestLunaVerifiedReasoningAndToolContract(t *testing.T) {
	resetRegistryToBuiltins()
	info := Get("gpt-6-luna")
	if info == nil || !info.Capabilities.Vision || !info.Capabilities.ToolUse || !info.Capabilities.Reasoning || info.Capabilities.ContextWindow != 1050000 || info.Capabilities.MaxOutput != 128000 {
		t.Fatalf("unexpected Luna builtin: %+v", info)
	}
	profile := RuntimeProfileForModelWithAdapter(&model.Model{ID: "gpt-6-luna", Provider: "openai", Reasoning: true, ToolUse: true}, "openai_compatible")
	if profile.ThinkingFormat != string(ThinkingFormatOpenAIGPT56) || profile.DefaultThinkingEffort != "medium" || len(profile.ThinkingEffortOptions) != 6 || !profile.SupportsTools {
		t.Fatalf("Luna profile = %+v", profile)
	}
	if got := ResolveThinkingEffortForModel(ThinkingFormatOpenAIGPT56, "gpt-6-luna", ""); got != ThinkingEffortMedium {
		t.Fatalf("default effort = %q", got)
	}
}

func TestLunaRequestToolGate(t *testing.T) {
	for _, tc := range []struct {
		adapter string
		effort  ThinkingEffort
		want    bool
	}{
		{adapter: "openai_compatible", effort: ThinkingEffortNone, want: true},
		{adapter: "openai_compatible", effort: ThinkingEffortMedium, want: false},
		{adapter: "openai_responses", effort: ThinkingEffortMedium, want: true},
	} {
		if got := ModelSupportsToolsForRequest("gpt-6-luna", tc.adapter, tc.effort, true); got != tc.want {
			t.Fatalf("adapter=%s effort=%s got %v want %v", tc.adapter, tc.effort, got, tc.want)
		}
	}
}
