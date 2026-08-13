package model

import "strings"

const (
	CatalogSourceManual    = "manual"
	CatalogSourceChannel   = "channel"
	CatalogSourceModelsDev = "models_dev"
	CatalogSourceBuiltin   = "builtin"
	CatalogSourceUnknown   = "unknown"

	ModelLifecycleActive     = "active"
	ModelLifecyclePreview    = "preview"
	ModelLifecycleDeprecated = "deprecated"
	ModelLifecycleRetired    = "retired"
	ModelLifecycleUnknown    = "unknown"
)

var validCatalogSources = map[string]struct{}{
	CatalogSourceManual: {}, CatalogSourceChannel: {}, CatalogSourceModelsDev: {},
	CatalogSourceBuiltin: {}, CatalogSourceUnknown: {},
}

var validModelLifecycleStatuses = map[string]struct{}{
	ModelLifecycleActive: {}, ModelLifecyclePreview: {}, ModelLifecycleDeprecated: {},
	ModelLifecycleRetired: {}, ModelLifecycleUnknown: {},
}

// NormalizeCatalogSource keeps legacy rows and direct repository callers on the
// explicit administrator-owned default instead of persisting an empty source.
func NormalizeCatalogSource(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return CatalogSourceManual
	}
	return value
}

func IsValidCatalogSource(value string) bool {
	_, ok := validCatalogSources[NormalizeCatalogSource(value)]
	return ok
}

func NormalizeModelLifecycleStatus(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ModelLifecycleUnknown
	}
	return value
}

func IsValidModelLifecycleStatus(value string) bool {
	_, ok := validModelLifecycleStatuses[NormalizeModelLifecycleStatus(value)]
	return ok
}

// InferModelLifecycleStatus only recognizes lifecycle markers carried by the
// model identifier itself. Absence of such a marker is unknown, not active:
// directory presence alone is not proof that an account can still run it.
func InferModelLifecycleStatus(modelID string) string {
	normalized := strings.ToLower(strings.TrimSpace(modelID))
	if strings.Contains(normalized, "preview") || strings.Contains(normalized, "experimental") {
		return ModelLifecyclePreview
	}
	return ModelLifecycleUnknown
}
