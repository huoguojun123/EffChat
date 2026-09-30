package modelbank

import (
	"testing"

	"github.com/huoguojun123/EffChat/internal/model"
)

func TestSol61VerifiedReasoningContract(t *testing.T) {
	resetRegistryToBuiltins()
	info := Get("gpt-6.1-sol")
	if info == nil || !info.Capabilities.ToolUse || !info.Capabilities.Reasoning {
		t.Fatal("missing verified Sol candidate capabilities")
	}
	for _, adapter := range []string{"openai_compatible", "openai_responses"} {
		profile := RuntimeProfileForModelWithAdapter(&model.Model{
			ID: "gpt-6.1-sol", Provider: "openai", Reasoning: true, ToolUse: true,
		}, adapter)
		if profile.ThinkingFormat != string(ThinkingFormatOpenAIGPT6Astra) || len(profile.ThinkingEffortOptions) != 5 || profile.DefaultThinkingEffort != "medium" {
			t.Fatalf("%s profile = %+v", adapter, profile)
		}
		if profile.SupportsTools != (adapter == "openai_responses") {
			t.Fatalf("%s tool gate = %v", adapter, profile.SupportsTools)
		}
	}
	for _, invalid := range []string{"none", "minimal"} {
		if got := ResolveThinkingEffortForModel(ThinkingFormatOpenAIGPT6Astra, "gpt-6.1-sol", invalid); got != ThinkingEffortMedium {
			t.Fatalf("invalid effort %q became %q", invalid, got)
		}
	}
	if OpenAIOmitsSamplingParameters("gpt-6.1-sol") {
		t.Fatal("Sol must not inherit an unverified Astra sampling rule")
	}
}
