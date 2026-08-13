package model

import (
	"fmt"
	"math"
	"strings"
)

const (
	TemperaturePolicyConfigurable = "configurable"
	TemperaturePolicyOmit         = "omit"
	TemperaturePolicyFixed        = "fixed"
)

// NormalizeTemperaturePolicy keeps legacy and incomplete administrator input
// on the backwards-compatible path where a session may choose temperature.
func NormalizeTemperaturePolicy(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case TemperaturePolicyOmit:
		return TemperaturePolicyOmit
	case TemperaturePolicyFixed:
		return TemperaturePolicyFixed
	default:
		return TemperaturePolicyConfigurable
	}
}

// ValidateTemperatureProfile enforces the persisted typed-policy invariant.
// Arbitrary provider parameters stay outside this model-owned contract.
func ValidateTemperatureProfile(policy string, fixed *float64) error {
	switch NormalizeTemperaturePolicy(policy) {
	case TemperaturePolicyFixed:
		if fixed == nil || math.IsNaN(*fixed) || math.IsInf(*fixed, 0) || *fixed < 0 || *fixed > 2 {
			return fmt.Errorf("fixed temperature must be between 0 and 2")
		}
	default:
		if fixed != nil {
			return fmt.Errorf("temperature value is only valid for a fixed policy")
		}
	}
	return nil
}

// ResolveTemperatureForRequest applies the model-owned policy without
// mutating the session preference. nil means the provider field is omitted.
func ResolveTemperatureForRequest(policy string, fixed, requested *float64) (*float64, error) {
	if err := ValidateTemperatureProfile(policy, fixed); err != nil {
		return nil, err
	}
	switch NormalizeTemperaturePolicy(policy) {
	case TemperaturePolicyOmit:
		return nil, nil
	case TemperaturePolicyFixed:
		value := *fixed
		return &value, nil
	default:
		if requested == nil {
			return nil, nil
		}
		value := *requested
		return &value, nil
	}
}

// ValidateOpenAIRequestProfile keeps compatibility overrides typed and
// bounded before they reach either PostgreSQL or an upstream request.
func ValidateOpenAIRequestProfile(profile OpenAIRequestProfile) error {
	if profile.TopP != nil && (math.IsNaN(*profile.TopP) || math.IsInf(*profile.TopP, 0) || *profile.TopP < 0 || *profile.TopP > 1) {
		return fmt.Errorf("openai top_p must be between 0 and 1")
	}
	if profile.N != nil && *profile.N < 1 {
		return fmt.Errorf("openai n must be at least 1")
	}
	if profile.PresencePenalty != nil && (math.IsNaN(*profile.PresencePenalty) || math.IsInf(*profile.PresencePenalty, 0) || *profile.PresencePenalty < -2 || *profile.PresencePenalty > 2) {
		return fmt.Errorf("openai presence_penalty must be between -2 and 2")
	}
	if profile.FrequencyPenalty != nil && (math.IsNaN(*profile.FrequencyPenalty) || math.IsInf(*profile.FrequencyPenalty, 0) || *profile.FrequencyPenalty < -2 || *profile.FrequencyPenalty > 2) {
		return fmt.Errorf("openai frequency_penalty must be between -2 and 2")
	}
	return nil
}

func CloneOpenAIRequestProfile(profile OpenAIRequestProfile) OpenAIRequestProfile {
	return OpenAIRequestProfile{
		TopP:             cloneOptionalFloat64(profile.TopP),
		N:                cloneOptionalInt(profile.N),
		PresencePenalty:  cloneOptionalFloat64(profile.PresencePenalty),
		FrequencyPenalty: cloneOptionalFloat64(profile.FrequencyPenalty),
	}
}

func OpenAIRequestProfileEmpty(profile OpenAIRequestProfile) bool {
	return profile.TopP == nil && profile.N == nil && profile.PresencePenalty == nil && profile.FrequencyPenalty == nil
}

func cloneOptionalFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneOptionalInt(value *int) *int {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
