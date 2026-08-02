package model

import "strings"

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
