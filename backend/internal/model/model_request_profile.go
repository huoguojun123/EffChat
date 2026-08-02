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
