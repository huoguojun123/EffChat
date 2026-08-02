package model

import "testing"

func TestCatalogMetadataNormalization(t *testing.T) {
	if got := NormalizeCatalogSource(""); got != CatalogSourceManual {
		t.Fatalf("empty source = %q, want %q", got, CatalogSourceManual)
	}
	if !IsValidCatalogSource(" MODELS_DEV ") || IsValidCatalogSource("internet") {
		t.Fatal("catalog source validation did not preserve the closed set")
	}
	if got := NormalizeModelLifecycleStatus(""); got != ModelLifecycleUnknown {
		t.Fatalf("empty lifecycle = %q, want %q", got, ModelLifecycleUnknown)
	}
	if !IsValidModelLifecycleStatus("deprecated") || IsValidModelLifecycleStatus("available") {
		t.Fatal("lifecycle validation did not preserve the closed set")
	}
	if got := InferModelLifecycleStatus("example-preview-2026"); got != ModelLifecyclePreview {
		t.Fatalf("preview lifecycle = %q", got)
	}
	if got := InferModelLifecycleStatus("example-stable"); got != ModelLifecycleUnknown {
		t.Fatalf("stable-looking identifier lifecycle = %q, want unknown", got)
	}
}
