package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

const (
	policyIntPrefix         = "int:"
	policyBoolPrefix        = "bool:"
	policyStringPrefix      = "string:"
	policyStringSlicePrefix = "string_slice:"
)

// GetPolicyIntContext reads a required integer policy value. A successful value
// becomes the last-known-good snapshot for this repository instance. Temporary
// query or parse failures may reuse only that snapshot; a cold process without
// one returns the original error instead of broadening to the caller fallback.
func (r *ConfigRepository) GetPolicyIntContext(ctx context.Context, key string, fallback int) (value int, degraded bool, err error) {
	item, err := r.GetContext(ctx, key)
	if err == nil {
		parsed, ok := parseConfigInt(item.Value)
		if !ok {
			err = fmt.Errorf("config %s is not a valid integer", key)
		} else {
			value = clampToOptions(key, parsed)
			r.policyCache.Store(policyIntPrefix+key, value)
			return value, false, nil
		}
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fallback, false, ctxErr
	}
	if cached, ok := r.policyCache.Load(policyIntPrefix + key); ok {
		return cached.(int), true, nil
	}
	return fallback, false, err
}

// GetPolicyStringContext is the string counterpart used for model identifiers
// that participate in a content-sharing decision.
func (r *ConfigRepository) GetPolicyStringContext(ctx context.Context, key, fallback string) (value string, degraded bool, err error) {
	item, err := r.GetContext(ctx, key)
	if err == nil {
		if unmarshalErr := json.Unmarshal(item.Value, &value); unmarshalErr != nil {
			err = fmt.Errorf("config %s is not a valid string", key)
		} else {
			r.policyCache.Store(policyStringPrefix+key, value)
			return value, false, nil
		}
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fallback, false, ctxErr
	}
	if cached, ok := r.policyCache.Load(policyStringPrefix + key); ok {
		return cached.(string), true, nil
	}
	return fallback, false, err
}

// GetPolicyBoolContext applies the same last-known-good rule to required
// boolean control-plane values such as content-extraction switches.
func (r *ConfigRepository) GetPolicyBoolContext(ctx context.Context, key string, fallback bool) (value bool, degraded bool, err error) {
	item, err := r.GetContext(ctx, key)
	if err == nil {
		if unmarshalErr := json.Unmarshal(item.Value, &value); unmarshalErr != nil {
			var raw string
			if stringErr := json.Unmarshal(item.Value, &raw); stringErr != nil {
				err = fmt.Errorf("config %s is not a valid boolean", key)
			} else if value, err = strconv.ParseBool(raw); err != nil {
				err = fmt.Errorf("config %s is not a valid boolean", key)
			}
		}
		if err == nil {
			r.policyCache.Store(policyBoolPrefix+key, value)
			return value, false, nil
		}
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fallback, false, ctxErr
	}
	if cached, ok := r.policyCache.Load(policyBoolPrefix + key); ok {
		return cached.(bool), true, nil
	}
	return fallback, false, err
}

// GetPolicyStringSliceContext clones cached slices on both store and load so a
// caller cannot mutate the policy snapshot shared by concurrent requests.
func (r *ConfigRepository) GetPolicyStringSliceContext(ctx context.Context, key string, fallback []string) (value []string, degraded bool, err error) {
	item, err := r.GetContext(ctx, key)
	if err == nil {
		if unmarshalErr := json.Unmarshal(item.Value, &value); unmarshalErr != nil {
			err = fmt.Errorf("config %s is not a valid string array", key)
		} else {
			stored := append([]string(nil), value...)
			r.policyCache.Store(policyStringSlicePrefix+key, stored)
			return append([]string(nil), stored...), false, nil
		}
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return append([]string(nil), fallback...), false, ctxErr
	}
	if cached, ok := r.policyCache.Load(policyStringSlicePrefix + key); ok {
		return append([]string(nil), cached.([]string)...), true, nil
	}
	return append([]string(nil), fallback...), false, err
}
