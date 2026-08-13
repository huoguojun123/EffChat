package repository

import (
	"fmt"
	"testing"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
)

func TestModelRepositoryPersistsCatalogMetadata(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	modelID := fmt.Sprintf("catalog-metadata-%d", time.Now().UnixNano())
	checkedAt := time.Date(2026, time.August, 2, 8, 30, 0, 0, time.UTC)
	fixedTemperature := 1.0
	topP, presencePenalty, frequencyPenalty := 1.0, 0.0, 0.0
	n := 1
	repo := NewModelRepository(db)
	item := &model.Model{
		ID: modelID, DisplayName: "Catalog Fixture", Provider: "fixture-channel",
		ThinkingFormat: "auto", Enabled: false,
		CatalogSource: model.CatalogSourceModelsDev, CatalogCheckedAt: &checkedAt,
		LifecycleStatus:   model.ModelLifecyclePreview,
		TemperaturePolicy: model.TemperaturePolicyFixed, TemperatureValue: &fixedTemperature,
		OpenAIRequestProfile: model.OpenAIRequestProfile{
			TopP: &topP, N: &n, PresencePenalty: &presencePenalty, FrequencyPenalty: &frequencyPenalty,
		},
	}
	if err := repo.Upsert(item); err != nil {
		t.Fatalf("upsert model metadata: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM models WHERE id = $1", modelID) })

	stored, err := repo.Get(modelID)
	if err != nil {
		t.Fatalf("get model metadata: %v", err)
	}
	if stored == nil || stored.CatalogSource != model.CatalogSourceModelsDev || stored.LifecycleStatus != model.ModelLifecyclePreview {
		t.Fatalf("stored model metadata = %#v", stored)
	}
	if stored.CatalogCheckedAt == nil || !stored.CatalogCheckedAt.Equal(checkedAt) {
		t.Fatalf("catalog_checked_at = %v, want %v", stored.CatalogCheckedAt, checkedAt)
	}
	if stored.TemperaturePolicy != model.TemperaturePolicyFixed || stored.TemperatureValue == nil || *stored.TemperatureValue != fixedTemperature {
		t.Fatalf("temperature profile = %q/%v", stored.TemperaturePolicy, stored.TemperatureValue)
	}
	profile := stored.OpenAIRequestProfile
	if profile.TopP == nil || *profile.TopP != 1 || profile.N == nil || *profile.N != 1 ||
		profile.PresencePenalty == nil || *profile.PresencePenalty != 0 || profile.FrequencyPenalty == nil || *profile.FrequencyPenalty != 0 {
		t.Fatalf("OpenAI request profile = %#v", profile)
	}
}

func TestModelRepositoryDefaultsLegacyCatalogMetadata(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	modelID := fmt.Sprintf("catalog-default-%d", time.Now().UnixNano())
	repo := NewModelRepository(db)
	if err := repo.Upsert(&model.Model{ID: modelID, DisplayName: "Manual Fixture", Provider: "fixture-channel", ThinkingFormat: "auto"}); err != nil {
		t.Fatalf("upsert legacy-shaped model: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM models WHERE id = $1", modelID) })

	stored, err := repo.Get(modelID)
	if err != nil || stored == nil {
		t.Fatalf("get legacy-shaped model = %#v, err=%v", stored, err)
	}
	if stored.CatalogSource != model.CatalogSourceManual || stored.LifecycleStatus != model.ModelLifecycleUnknown || stored.CatalogCheckedAt != nil {
		t.Fatalf("default catalog metadata = %#v", stored)
	}
	if stored.TemperaturePolicy != model.TemperaturePolicyConfigurable || stored.TemperatureValue != nil {
		t.Fatalf("default temperature profile = %q/%v", stored.TemperaturePolicy, stored.TemperatureValue)
	}
}
