package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/repository"
)

// ModelService writes deliberately replace the process-wide runtime registry.
// Restore the package fixture after each such test so later service tests do
// not inherit a database-specific model catalog.
func preserveModelBank(t *testing.T) {
	t.Helper()
	previous := modelbank.List()
	t.Cleanup(func() {
		models := make([]*model.Model, 0, len(previous))
		for _, info := range previous {
			models = append(models, &model.Model{
				ID:                   info.ID,
				DisplayName:          info.DisplayName,
				Provider:             info.Provider,
				Vision:               info.Capabilities.Vision,
				ToolUse:              info.Capabilities.ToolUse,
				Reasoning:            info.Capabilities.Reasoning,
				ThinkingFormat:       info.ThinkingFormat,
				SearchImpl:           string(info.Capabilities.SearchImpl),
				ContextWindow:        info.Capabilities.ContextWindow,
				MaxOutput:            info.Capabilities.MaxOutput,
				Enabled:              info.Enabled,
				TemperaturePolicy:    info.TemperaturePolicy,
				TemperatureValue:     info.TemperatureValue,
				OpenAIRequestProfile: info.OpenAIRequestProfile,
			})
		}
		modelbank.LoadModels(models)
	})
}

func TestValidateModelInput(t *testing.T) {
	valid := func() *model.Model {
		return &model.Model{
			ID: "gpt-x", DisplayName: "GPT-X", Provider: "openai",
			SearchImpl: "tool", ContextWindow: 1000, MaxOutput: 100,
		}
	}

	tests := []struct {
		name    string
		mutate  func(*model.Model)
		wantErr bool
	}{
		{"合法输入", func(m *model.Model) {}, false},
		{"空 search_impl 合法", func(m *model.Model) { m.SearchImpl = "" }, false},
		{"search_impl=internal 合法", func(m *model.Model) { m.SearchImpl = "internal" }, false},
		{"search_impl=params 合法", func(m *model.Model) { m.SearchImpl = "params" }, false},
		{"非法 search_impl", func(m *model.Model) { m.SearchImpl = "bogus" }, true},
		{"thinking_format=auto 合法", func(m *model.Model) { m.ThinkingFormat = "auto" }, false},
		{"thinking_format=deepseek_v4 合法", func(m *model.Model) { m.ThinkingFormat = "deepseek_v4" }, false},
		{"thinking_format=volcengine_thinking 合法", func(m *model.Model) { m.ThinkingFormat = "volcengine_thinking" }, false},
		{"非法 thinking_format", func(m *model.Model) { m.ThinkingFormat = "guess_by_provider" }, true},
		{"空 id", func(m *model.Model) { m.ID = "" }, true},
		{"空 display_name", func(m *model.Model) { m.DisplayName = "" }, true},
		{"空 provider", func(m *model.Model) { m.Provider = "" }, true},
		{"负 context_window", func(m *model.Model) { m.ContextWindow = -1 }, true},
		{"负 max_output", func(m *model.Model) { m.MaxOutput = -1 }, true},
		{"零 token 合法", func(m *model.Model) { m.ContextWindow = 0; m.MaxOutput = 0 }, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := valid()
			tt.mutate(m)
			err := validateModelInput(m)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateModelInput() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestModelService_DeleteHardRemovesAndDefaultValidationRequiresRunnablePublicModel(t *testing.T) {
	preserveModelBank(t)
	db := setupMessageTestDB(t)
	defer db.Close()
	suffix := time.Now().UnixNano()
	channelKey := fmt.Sprintf("model-default-channel-%d", suffix)
	modelID := fmt.Sprintf("model-default-%d", suffix)

	channelService := NewChannelService(repository.NewChannelRepository(db))
	enabled := true
	if _, err := channelService.SaveAIChannel(&AIChannelInput{Key: channelKey, DisplayName: "Default Channel", Adapter: AdapterOpenAICompatible, BaseURL: "https://example.test/v1", APIKey: "test-key", Enabled: &enabled}); err != nil {
		t.Fatalf("save channel: %v", err)
	}
	modelRepo := repository.NewModelRepository(db)
	if err := modelRepo.Upsert(&model.Model{ID: modelID, DisplayName: "Default", Provider: channelKey, ContextWindow: 1024, MaxOutput: 256, Enabled: true, MinGroupLevel: 0, ThinkingFormat: "auto"}); err != nil {
		t.Fatalf("save model: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM models WHERE id = $1", modelID)
		_, _ = db.Exec("DELETE FROM ai_channels WHERE channel_key = $1", channelKey)
	})

	svc := NewModelService(modelRepo, channelService)
	if err := svc.ValidateDefaultModel(""); err == nil || !errors.Is(err, ErrModelInvalid) {
		t.Fatalf("empty default with runnable public model error = %v, want invalid model configuration", err)
	}
	if err := svc.ValidateDefaultModel(modelID); err != nil {
		t.Fatalf("validate runnable public default: %v", err)
	}
	if err := svc.Delete(context.Background(), modelID); err != nil {
		t.Fatalf("delete model: %v", err)
	}
	stored, err := modelRepo.Get(modelID)
	if err != nil || stored != nil {
		t.Fatalf("deleted model = %#v, err=%v; want hard deletion", stored, err)
	}
	if err := svc.ValidateDefaultModel(modelID); err == nil {
		t.Fatal("disabled model accepted as default")
	} else if !errors.Is(err, ErrModelInvalid) {
		t.Fatalf("disabled model error = %v, want invalid model configuration", err)
	}
	if err := svc.ValidateDefaultModel(""); err != nil {
		t.Fatalf("empty default without runnable public model: %v", err)
	}
}

func TestValidateDefaultModelPreservesRepositoryFailure(t *testing.T) {
	db := setupMessageTestDB(t)
	modelRepo := repository.NewModelRepository(db)
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	err := NewModelService(modelRepo, nil).ValidateDefaultModel("example-model")
	if err == nil || errors.Is(err, ErrModelInvalid) {
		t.Fatalf("repository error = %v, want internal failure", err)
	}
}

func TestModelServiceManualCatalogOverrideClearsDirectoryCheckTime(t *testing.T) {
	preserveModelBank(t)
	db := setupMessageTestDB(t)
	defer db.Close()

	modelID := fmt.Sprintf("model-catalog-override-%d", time.Now().UnixNano())
	checkedAt := time.Date(2026, time.August, 2, 10, 0, 0, 0, time.UTC)
	repo := repository.NewModelRepository(db)
	if err := repo.Upsert(&model.Model{
		ID: modelID, DisplayName: "Catalog Override", Provider: "fixture-channel", ThinkingFormat: "auto",
		CatalogSource: model.CatalogSourceModelsDev, CatalogCheckedAt: &checkedAt,
		LifecycleStatus: model.ModelLifecyclePreview,
	}); err != nil {
		t.Fatalf("seed catalog model: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM models WHERE id = $1", modelID) })

	source := model.CatalogSourceManual
	lifecycle := model.ModelLifecycleUnknown
	updated, err := NewModelService(repo).Update(context.Background(), modelID, &UpdateModelRequest{
		CatalogSource: &source, LifecycleStatus: &lifecycle,
	})
	if err != nil {
		t.Fatalf("apply manual catalog override: %v", err)
	}
	if updated.CatalogSource != model.CatalogSourceManual || updated.LifecycleStatus != model.ModelLifecycleUnknown || updated.CatalogCheckedAt != nil {
		t.Fatalf("manual override metadata = %#v", updated)
	}
}

func TestValidateModelInputRequiresConsistentTemperatureProfile(t *testing.T) {
	fixed := 1.0
	base := model.Model{ID: "fixture-model", DisplayName: "Fixture", Provider: "fixture", ThinkingFormat: "auto"}

	validFixed := base
	validFixed.TemperaturePolicy = model.TemperaturePolicyFixed
	validFixed.TemperatureValue = &fixed
	if err := validateModelInput(&validFixed); err != nil {
		t.Fatalf("valid fixed profile: %v", err)
	}

	missingFixed := base
	missingFixed.TemperaturePolicy = model.TemperaturePolicyFixed
	if err := validateModelInput(&missingFixed); err == nil {
		t.Fatal("fixed profile without a value was accepted")
	}

	omitWithValue := base
	omitWithValue.TemperaturePolicy = model.TemperaturePolicyOmit
	omitWithValue.TemperatureValue = &fixed
	if err := validateModelInput(&omitWithValue); err == nil {
		t.Fatal("omit profile with a fixed value was accepted")
	}
}
