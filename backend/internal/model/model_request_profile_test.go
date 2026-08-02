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
