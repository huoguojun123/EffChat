package model

import "testing"

func TestResolveTemperatureForRequest(t *testing.T) {
	requested := 0.25
	fixed := 1.0

	configurable, err := ResolveTemperatureForRequest(TemperaturePolicyConfigurable, nil, &requested)
	if err != nil || configurable == nil || *configurable != requested {
		t.Fatalf("configurable = %v, err=%v", configurable, err)
	}
	omitted, err := ResolveTemperatureForRequest(TemperaturePolicyOmit, nil, &requested)
	if err != nil || omitted != nil {
		t.Fatalf("omit = %v, err=%v", omitted, err)
	}
	resolvedFixed, err := ResolveTemperatureForRequest(TemperaturePolicyFixed, &fixed, &requested)
	if err != nil || resolvedFixed == nil || *resolvedFixed != fixed {
		t.Fatalf("fixed = %v, err=%v", resolvedFixed, err)
	}
	if _, err := ResolveTemperatureForRequest(TemperaturePolicyFixed, nil, &requested); err == nil {
		t.Fatal("fixed policy without a value was accepted")
	}
}

func TestValidateOpenAIRequestProfile(t *testing.T) {
	topP, presence, frequency := 1.0, 0.0, 0.0
	n := 1
	valid := OpenAIRequestProfile{TopP: &topP, N: &n, PresencePenalty: &presence, FrequencyPenalty: &frequency}
	if err := ValidateOpenAIRequestProfile(valid); err != nil {
		t.Fatalf("valid profile: %v", err)
	}

	invalidTopP := 1.1
	if err := ValidateOpenAIRequestProfile(OpenAIRequestProfile{TopP: &invalidTopP}); err == nil {
		t.Fatal("out-of-range top_p was accepted")
	}
	invalidN := 0
	if err := ValidateOpenAIRequestProfile(OpenAIRequestProfile{N: &invalidN}); err == nil {
		t.Fatal("non-positive n was accepted")
	}
}
