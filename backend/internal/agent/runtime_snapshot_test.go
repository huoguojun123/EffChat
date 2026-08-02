package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
	"github.com/huoguojun123/EffChat/internal/testutil"
)

func TestAcceptedRuntimeSnapshotRejectsChangedDependenciesWithoutPersistingSecrets(t *testing.T) {
	db := testutil.OpenPostgresTestDB(t)
	channelKey := fmt.Sprintf("runtime-snapshot-%d", time.Now().UnixNano())
	modelID := fmt.Sprintf("runtime-snapshot-model-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM ai_channels WHERE channel_key = $1", channelKey)
		_ = db.Close()
	})

	channelService := service.NewChannelService(repository.NewChannelRepository(db))
	enabled := true
	fixedTemperature := 1.0
	topP, presencePenalty, frequencyPenalty := 1.0, 0.0, 0.0
	n := 1
	if _, err := channelService.SaveAIChannel(&service.AIChannelInput{
		Key: channelKey, DisplayName: "Runtime snapshot", Adapter: service.AdapterOpenAICompatible,
		BaseURL: "https://gateway.example.test/v1", APIKey: "secret-key-one", Enabled: &enabled,
	}); err != nil {
		t.Fatalf("save channel: %v", err)
	}
	modelbank.Register(&modelbank.ModelInfo{
		ID: modelID, DisplayName: modelID, Provider: channelKey, Enabled: true, ThinkingFormat: "auto",
		TemperaturePolicy: model.TemperaturePolicyFixed, TemperatureValue: &fixedTemperature,
		OpenAIRequestProfile: model.OpenAIRequestProfile{
			TopP: &topP, N: &n, PresencePenalty: &presencePenalty, FrequencyPenalty: &frequencyPenalty,
		},
		Capabilities: modelbank.ModelCapabilities{
			Vision: true, ToolUse: true, Reasoning: true, SearchImpl: modelbank.SearchImplTool,
			ContextWindow: 128000, MaxOutput: 8192,
		},
	})
	configRepo := repository.NewConfigRepository(db)
	if err := configRepo.Update("extract_summary_enabled", json.RawMessage(`false`)); err != nil {
		t.Fatalf("disable extract summary: %v", err)
	}
	if err := configRepo.Update("extract_summary_model", json.RawMessage(`"accepted-refiner"`)); err != nil {
		t.Fatalf("set extract summary model: %v", err)
	}

	agent := NewEinoAgent(
		channelService,
		service.NewToolConfigService(repository.NewToolConfigRepository(db)),
		64000,
		configRepo,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	requestedTemperature := 0.25
	req := &ChatRequest{
		UserID: 7, SessionID: 9, ModelID: modelID, Provider: channelKey,
		SystemName: "EffChat", MessageFormat: "v2", SchemaVersion: "v2",
		UserName: "tester", UserRole: "user", UserPreferences: []byte(`{"language":"zh-CN"}`),
		SessionMetadata: []byte(`{"skills_enabled":["runtime-skill"]}`),
		EnabledSkills: []SkillInstruction{{
			ID: "runtime-skill", Name: "Runtime Skill", Description: "private skill description",
			Files: []model.SkillFile{{
				RelativePath: "SKILL.md", StoragePath: "/private/skill/body.md",
				Kind: "entry", Size: 2048, Checksum: "sha256:skill-file",
			}},
		}},
		ThinkingEffort: "high", SearchMode: modelbank.SearchModeAuto,
		PreferModelNativeSearch: true, MemoryEnabled: false,
		Temperature: &requestedTemperature,
	}

	raw, err := agent.CaptureAcceptedRuntimeSnapshot(context.Background(), req)
	if err != nil {
		t.Fatalf("capture runtime snapshot: %v", err)
	}
	for _, secret := range []string{"secret-key-one", "/private/skill/body.md", "private skill description"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("runtime snapshot leaked %q: %s", secret, raw)
		}
	}
	snapshot, err := ParseAcceptedRuntimeSnapshot(raw)
	if err != nil {
		t.Fatalf("parse runtime snapshot: %v", err)
	}
	if snapshot.ModelID != modelID || snapshot.ChannelKey != channelKey || snapshot.ContextWindow != 128000 || snapshot.ModelMaxOutput != 8192 {
		t.Fatalf("runtime snapshot identity = %+v", snapshot)
	}
	if req.TemperaturePolicy != model.TemperaturePolicyFixed || req.Temperature == nil || *req.Temperature != fixedTemperature {
		t.Fatalf("accepted temperature profile = %q/%v", req.TemperaturePolicy, req.Temperature)
	}
	if req.OpenAIRequestProfile.N == nil || *req.OpenAIRequestProfile.N != 1 || req.OpenAIRequestProfile.TopP == nil || *req.OpenAIRequestProfile.TopP != 1 {
		t.Fatalf("accepted OpenAI request profile = %#v", req.OpenAIRequestProfile)
	}
	if snapshot.ToolConfigState.State == "" || snapshot.SearchConfigState.Search.State == "" || snapshot.MemoryState.State == "" {
		t.Fatalf("runtime dependency states missing: %+v", snapshot)
	}
	if snapshot.ExtractSummaryChecksum == "" {
		t.Fatal("extract summary dependency checksum is missing")
	}
	if req.RuntimeExtractSummaryEnabled || req.RuntimeExtractSummaryModel != "accepted-refiner" {
		t.Fatalf("captured extract summary runtime = enabled:%t model:%q", req.RuntimeExtractSummaryEnabled, req.RuntimeExtractSummaryModel)
	}
	if req.RuntimeExtractSummaryModelInfo != nil || req.RuntimeExtractSummaryChannel != nil {
		t.Fatalf("disabled extract summary resolved dependencies: info=%#v channel=%#v", req.RuntimeExtractSummaryModelInfo, req.RuntimeExtractSummaryChannel)
	}
	if len(snapshot.Skills) != 1 || snapshot.Skills[0].ID != "runtime-skill" || snapshot.Skills[0].Checksum == "" {
		t.Fatalf("runtime skill refs = %+v", snapshot.Skills)
	}

	current := *req
	current.RuntimeResolved = false
	current.RuntimeChannel = nil
	current.RuntimePromptTemplate = ""
	current.RuntimeMemory = nil
	current.RuntimeToolConfig = nil
	current.RuntimeSearchConfig = service.SearchRuntimeConfig{}
	if err := agent.ValidateAcceptedRuntimeSnapshot(context.Background(), &current, raw); err != nil {
		t.Fatalf("validate unchanged snapshot: %v", err)
	}
	if !current.RuntimeResolved || current.RuntimeChannel == nil || current.RuntimeChannel.APIKey != "secret-key-one" {
		t.Fatal("validated request did not receive its in-memory runtime dependencies")
	}
	if current.TemperaturePolicy != model.TemperaturePolicyFixed || current.Temperature == nil || *current.Temperature != fixedTemperature {
		t.Fatalf("restored temperature profile = %q/%v", current.TemperaturePolicy, current.Temperature)
	}
	if current.OpenAIRequestProfile.N == nil || *current.OpenAIRequestProfile.N != 1 || current.OpenAIRequestProfile.TopP == nil || *current.OpenAIRequestProfile.TopP != 1 {
		t.Fatalf("restored OpenAI request profile = %#v", current.OpenAIRequestProfile)
	}
	if current.RuntimeExtractSummaryEnabled || current.RuntimeExtractSummaryModel != "accepted-refiner" {
		t.Fatalf("validated extract summary runtime = enabled:%t model:%q", current.RuntimeExtractSummaryEnabled, current.RuntimeExtractSummaryModel)
	}

	// A policy edit after validation belongs to the next run. The already
	// accepted request must not reopen live config during PrepareChat and start
	// sharing crawler content with a utility model that was disabled at
	// admission.
	if err := configRepo.Update("extract_summary_enabled", json.RawMessage(`true`)); err != nil {
		t.Fatalf("enable live extract summary: %v", err)
	}
	if err := configRepo.Update("extract_summary_model", json.RawMessage(`"live-refiner"`)); err != nil {
		t.Fatalf("change live extract summary model: %v", err)
	}
	summarizer, enabledSummary, err := agent.buildExtractSummarizer(t.Context(), &current)
	if err != nil {
		t.Fatalf("build accepted extract summarizer: %v", err)
	}
	if summarizer != nil || enabledSummary {
		t.Fatalf("accepted disabled refinement reopened live config: summarizer=%T enabled=%t", summarizer, enabledSummary)
	}
	staleConfig := *req
	staleConfig.RuntimeResolved = false
	err = agent.ValidateAcceptedRuntimeSnapshot(context.Background(), &staleConfig, raw)
	var configRuntimeErr *RuntimeError
	if !errors.As(err, &configRuntimeErr) || configRuntimeErr.Code != "runtime_dependency_changed" {
		t.Fatalf("changed extract config error = %T %v", err, err)
	}
	if err := configRepo.Update("extract_summary_enabled", json.RawMessage(`false`)); err != nil {
		t.Fatalf("restore extract summary state: %v", err)
	}
	if err := configRepo.Update("extract_summary_model", json.RawMessage(`"accepted-refiner"`)); err != nil {
		t.Fatalf("restore extract summary model: %v", err)
	}

	if _, err := channelService.SaveAIChannel(&service.AIChannelInput{
		Key: channelKey, DisplayName: "Runtime snapshot", Adapter: service.AdapterOpenAICompatible,
		BaseURL: "https://gateway.example.test/v1", APIKey: "secret-key-two", Enabled: &enabled,
	}); err != nil {
		t.Fatalf("rotate channel key: %v", err)
	}
	changed := *req
	changed.RuntimeResolved = false
	changed.RuntimeChannel = nil
	changed.RuntimePromptTemplate = ""
	changed.RuntimeMemory = nil
	changed.RuntimeToolConfig = nil
	changed.RuntimeSearchConfig = service.SearchRuntimeConfig{}
	err = agent.ValidateAcceptedRuntimeSnapshot(context.Background(), &changed, raw)
	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) || runtimeErr.Code != "runtime_dependency_changed" {
		t.Fatalf("changed dependency error = %T %v", err, err)
	}

	var stored map[string]interface{}
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("decode runtime snapshot JSON: %v", err)
	}
	if _, exists := stored["api_key"]; exists {
		t.Fatalf("runtime snapshot contains an api_key field: %+v", stored)
	}
}

func TestRuntimeModelChecksumKeepsConfigurableTemperatureBackwardCompatible(t *testing.T) {
	info := &modelbank.ModelInfo{
		ID: "fixture-model", Provider: "fixture-channel", ThinkingFormat: "auto",
		TemperaturePolicy: model.TemperaturePolicyConfigurable,
		Capabilities:      modelbank.ModelCapabilities{ToolUse: true, ContextWindow: 4096, MaxOutput: 512},
	}
	legacy := struct {
		ID            string
		Provider      string
		Vision        bool
		ToolUse       bool
		Reasoning     bool
		Thinking      string
		SearchImpl    modelbank.SearchImpl
		ContextWindow int
		MaxOutput     int
	}{
		ID: info.ID, Provider: info.Provider, ToolUse: true, Thinking: "auto", ContextWindow: 4096, MaxOutput: 512,
	}
	if got, want := checksumValue("model", runtimeModelInfoMaterial(info)), checksumValue("model", legacy); got != want {
		t.Fatalf("configurable default changed legacy checksum: got %s want %s", got, want)
	}

	fixed := 1.0
	info.TemperaturePolicy = model.TemperaturePolicyFixed
	info.TemperatureValue = &fixed
	if got, legacyChecksum := checksumValue("model", runtimeModelInfoMaterial(info)), checksumValue("model", legacy); got == legacyChecksum {
		t.Fatal("fixed temperature profile did not change the model checksum")
	}
}

func TestAcceptedRuntimeSnapshotPinsExtractSummaryModelAndChannel(t *testing.T) {
	db := testutil.OpenPostgresTestDB(t)
	defer db.Close()

	mainChannelKey := fmt.Sprintf("snapshot-main-channel-%d", time.Now().UnixNano())
	mainModelID := fmt.Sprintf("snapshot-main-model-%d", time.Now().UnixNano())
	refinerChannelKey := fmt.Sprintf("snapshot-refiner-channel-%d", time.Now().UnixNano())
	refinerModelID := fmt.Sprintf("snapshot-refiner-model-%d", time.Now().UnixNano())
	enabled := true
	channelService := service.NewChannelService(repository.NewChannelRepository(db))
	for _, input := range []*service.AIChannelInput{
		{
			Key: mainChannelKey, DisplayName: "Snapshot main", Adapter: service.AdapterOpenAICompatible,
			BaseURL: "https://main.example.test/v1", APIKey: "main-snapshot-secret", Enabled: &enabled,
		},
		{
			Key: refinerChannelKey, DisplayName: "Snapshot refiner", Adapter: service.AdapterOpenAICompatible,
			BaseURL: "https://refiner-old.example.test/v1", APIKey: "refiner-snapshot-secret-old", Enabled: &enabled,
		},
	} {
		if _, err := channelService.SaveAIChannel(input); err != nil {
			t.Fatalf("save channel %q: %v", input.Key, err)
		}
	}
	modelbank.Register(&modelbank.ModelInfo{
		ID: mainModelID, DisplayName: mainModelID, Provider: mainChannelKey, Enabled: true,
		Capabilities: modelbank.ModelCapabilities{ContextWindow: 128000, MaxOutput: 8192, ToolUse: true},
	})
	refinerInfo := &modelbank.ModelInfo{
		ID: refinerModelID, DisplayName: refinerModelID, Provider: refinerChannelKey, Enabled: true,
		ThinkingFormat: "auto",
		Capabilities:   modelbank.ModelCapabilities{ContextWindow: 32000, MaxOutput: 4096},
	}
	modelbank.Register(refinerInfo)
	t.Cleanup(func() { modelbank.Register(refinerInfo) })

	configRepo := repository.NewConfigRepository(db)
	if err := configRepo.Update("extract_summary_enabled", json.RawMessage(`true`)); err != nil {
		t.Fatalf("enable extract summary: %v", err)
	}
	if err := configRepo.Update("extract_summary_model", json.RawMessage(fmt.Sprintf("%q", refinerModelID))); err != nil {
		t.Fatalf("set extract summary model: %v", err)
	}
	agent := NewEinoAgent(
		channelService,
		service.NewToolConfigService(repository.NewToolConfigRepository(db)),
		64000,
		configRepo,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	req := &ChatRequest{
		ModelID: mainModelID, Provider: mainChannelKey, MessageFormat: "v2", SchemaVersion: "v2",
	}
	raw, err := agent.CaptureAcceptedRuntimeSnapshot(t.Context(), req)
	if err != nil {
		t.Fatalf("capture runtime snapshot: %v", err)
	}
	if strings.Contains(string(raw), "refiner-snapshot-secret-old") {
		t.Fatalf("runtime snapshot leaked refiner secret: %s", raw)
	}
	snapshot, err := ParseAcceptedRuntimeSnapshot(raw)
	if err != nil {
		t.Fatalf("parse runtime snapshot: %v", err)
	}
	if snapshot.ExtractSummaryChecksum == "" {
		t.Fatal("extract summary dependency checksum is missing")
	}
	if req.RuntimeExtractSummaryModelInfo == nil ||
		req.RuntimeExtractSummaryModelInfo.ID != refinerModelID ||
		req.RuntimeExtractSummaryModelInfo == refinerInfo ||
		req.RuntimeExtractSummaryChannel == nil ||
		req.RuntimeExtractSummaryChannel.Key != refinerChannelKey ||
		req.RuntimeExtractSummaryChannel.APIKey != "refiner-snapshot-secret-old" {
		t.Fatalf("captured extract summary dependencies: info=%#v channel=%#v", req.RuntimeExtractSummaryModelInfo, req.RuntimeExtractSummaryChannel)
	}

	if _, err := channelService.SaveAIChannel(&service.AIChannelInput{
		Key: refinerChannelKey, DisplayName: "Snapshot refiner", Adapter: service.AdapterOpenAICompatible,
		BaseURL: "https://refiner-new.example.test/v1", APIKey: "refiner-snapshot-secret-new", Enabled: &enabled,
	}); err != nil {
		t.Fatalf("rotate refiner channel: %v", err)
	}
	changedChannel := *req
	changedChannel.RuntimeResolved = false
	err = agent.ValidateAcceptedRuntimeSnapshot(t.Context(), &changedChannel, raw)
	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) || runtimeErr.Code != "runtime_dependency_changed" {
		t.Fatalf("changed refiner channel error = %T %v", err, err)
	}

	if _, err := channelService.SaveAIChannel(&service.AIChannelInput{
		Key: refinerChannelKey, DisplayName: "Snapshot refiner", Adapter: service.AdapterOpenAICompatible,
		BaseURL: "https://refiner-old.example.test/v1", APIKey: "refiner-snapshot-secret-old", Enabled: &enabled,
	}); err != nil {
		t.Fatalf("restore refiner channel: %v", err)
	}
	changedInfo := *refinerInfo
	changedInfo.Capabilities.MaxOutput = 8192
	modelbank.Register(&changedInfo)
	changedModel := *req
	changedModel.RuntimeResolved = false
	err = agent.ValidateAcceptedRuntimeSnapshot(t.Context(), &changedModel, raw)
	runtimeErr = nil
	if !errors.As(err, &runtimeErr) || runtimeErr.Code != "runtime_dependency_changed" {
		t.Fatalf("changed refiner model error = %T %v", err, err)
	}

	modelbank.Register(refinerInfo)
	validated := *req
	validated.RuntimeResolved = false
	if err := agent.ValidateAcceptedRuntimeSnapshot(t.Context(), &validated, raw); err != nil {
		t.Fatalf("validate restored extract summary dependencies: %v", err)
	}
	if validated.RuntimeExtractSummaryModelInfo == nil ||
		validated.RuntimeExtractSummaryModelInfo.ID != refinerModelID ||
		validated.RuntimeExtractSummaryChannel == nil ||
		validated.RuntimeExtractSummaryChannel.APIKey != "refiner-snapshot-secret-old" {
		t.Fatalf("validated extract summary dependencies: info=%#v channel=%#v", validated.RuntimeExtractSummaryModelInfo, validated.RuntimeExtractSummaryChannel)
	}
	acceptedVersion := agent.extractSummarizerRuntimeVersion(
		validated.RuntimeExtractSummaryModel,
		validated.RuntimeExtractSummaryModelInfo,
		validated.RuntimeExtractSummaryChannel,
	)

	// Changes after validation belong to the next run. PrepareChat must build
	// from the copied dependency material without reopening the live registry or
	// channel table in the small validate-to-execution window.
	modelbank.Register(&changedInfo)
	if _, err := channelService.SaveAIChannel(&service.AIChannelInput{
		Key: refinerChannelKey, DisplayName: "Snapshot refiner", Adapter: service.AdapterOpenAICompatible,
		BaseURL: "https://refiner-new.example.test/v1", APIKey: "refiner-snapshot-secret-new", Enabled: &enabled,
	}); err != nil {
		t.Fatalf("rotate live refiner after validation: %v", err)
	}
	built, summaryEnabled, err := agent.buildExtractSummarizer(t.Context(), &validated)
	if err != nil {
		t.Fatalf("build validated extract summarizer: %v", err)
	}
	summarizer, ok := built.(*extractSummarizer)
	if !summaryEnabled || !ok || summarizer.runtimeVersion != acceptedVersion {
		t.Fatalf("validated summarizer = %#v enabled=%t, want runtime %q", built, summaryEnabled, acceptedVersion)
	}
}

func TestReadRuntimeMemoryReportsRepositoryUnavailable(t *testing.T) {
	agent := &EinoAgent{}
	memory, state := agent.readRuntimeMemory(context.Background(), 1, true)
	if memory != "" || state.State != service.RuntimeStateUnavailable || state.Cause != "repository_unavailable" {
		t.Fatalf("memory=%q state=%+v", memory, state)
	}
	if _, _, err := agent.readRuntimeMemoryContext(context.Background(), 1, true); err != nil {
		t.Fatalf("ordinary unavailable repository error = %v", err)
	}
}

func TestRuntimeSnapshotContextDependenciesSurfaceDeadline(t *testing.T) {
	db := testutil.OpenPostgresTestDB(t)
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	blocker, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("hold database connection: %v", err)
	}
	defer blocker.Close()
	releaseFallback := time.AfterFunc(2*time.Second, func() { _ = blocker.Close() })
	defer releaseFallback.Stop()

	configRepo := repository.NewConfigRepository(db)
	agent := NewEinoAgent(
		service.NewChannelService(repository.NewChannelRepository(db)),
		service.NewToolConfigService(repository.NewToolConfigRepository(db)),
		64000,
		configRepo,
		repository.NewSessionMemoryRepository(db),
		nil,
		nil,
		nil,
		nil,
	)
	tests := []struct {
		name string
		run  func(context.Context) error
	}{
		{
			name: "prompt",
			run: func(ctx context.Context) error {
				_, err := loadPromptTemplateContext(ctx, configRepo)
				return err
			},
		},
		{
			name: "memory",
			run: func(ctx context.Context) error {
				_, _, err := agent.readRuntimeMemoryContext(ctx, 1, true)
				return err
			},
		},
		{
			name: "tool runtime",
			run: func(ctx context.Context) error {
				_, _, err := agent.resolveToolRuntimeConfigWithStateContext(ctx)
				return err
			},
		},
		{
			name: "search runtime",
			run: func(ctx context.Context) error {
				_, _, err := agent.resolveSearchRuntimeConfigWithStateContext(ctx)
				return err
			},
		},
		{
			name: "config material",
			run: func(ctx context.Context) error {
				_, err := agent.runtimeConfigMaterialContext(ctx)
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			if err := test.run(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("error = %v, want context.DeadlineExceeded", err)
			}
		})
	}
}

func TestValidateAcceptedRuntimeSnapshotPreservesContextCancellation(t *testing.T) {
	raw, err := json.Marshal(AcceptedRuntimeSnapshot{
		Version:  acceptedRuntimeSnapshotVersion,
		ModelID:  "context-model",
		Provider: "context-provider",
		Checksum: "sha256:accepted",
	})
	if err != nil {
		t.Fatalf("marshal accepted snapshot: %v", err)
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := fmt.Errorf("accepted snapshot validation canceled: %w", context.Canceled)
	cancel(cause)
	err = (&EinoAgent{}).ValidateAcceptedRuntimeSnapshot(ctx, &ChatRequest{
		ModelID:  "context-model",
		Provider: "context-provider",
	}, raw)
	if err != cause {
		t.Fatalf("validate error = %v, want original cause %v", err, cause)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("validate error = %v, want context.Canceled", err)
	}
	var runtimeErr *RuntimeError
	if errors.As(err, &runtimeErr) {
		t.Fatalf("context cancellation was rewritten as runtime error: %v", runtimeErr)
	}
}

func TestCaptureAcceptedRuntimeSnapshotPreservesContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := fmt.Errorf("accepted snapshot capture canceled: %w", context.Canceled)
	cancel(cause)

	_, err := (&EinoAgent{}).CaptureAcceptedRuntimeSnapshot(ctx, &ChatRequest{})
	if err != cause {
		t.Fatalf("capture error = %v, want original cause %v", err, cause)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("capture error = %v, want context.Canceled", err)
	}
}

func TestRuntimeContextPlanDoesNotMountUnavailableMemory(t *testing.T) {
	agent := &EinoAgent{memoryRepo: repository.NewSessionMemoryRepository(nil)}
	plan := agent.buildRuntimeContextPlan(&ChatRequest{
		SessionID:     1,
		ModelID:       "gpt-5.6",
		MemoryEnabled: true,
		RuntimeMemoryState: service.RuntimeConfigState{
			State: service.RuntimeStateUnavailable,
			Cause: "repository_unavailable",
		},
	})
	if plan.mountedTools["memory"] {
		t.Fatal("memory tool should not mount when the memory repository is unavailable")
	}
}

func TestAcceptedRuntimeSnapshotRejectsChangedSkillReference(t *testing.T) {
	first := []SkillInstruction{{
		ID: "skill-a", Name: "Skill A",
		Files: []model.SkillFile{{RelativePath: "SKILL.md", Kind: "entry", Size: 10, Checksum: "sha256:first"}},
	}}
	second := []SkillInstruction{{
		ID: "skill-a", Name: "Skill A",
		Files: []model.SkillFile{{RelativePath: "SKILL.md", Kind: "entry", Size: 10, Checksum: "sha256:second"}},
	}}
	firstRefs, firstChecksum := runtimeSkillRefs(first)
	secondRefs, secondChecksum := runtimeSkillRefs(second)
	if firstChecksum == secondChecksum || firstRefs[0].Checksum == secondRefs[0].Checksum {
		t.Fatalf("skill checksum did not change: first=%s second=%s", firstChecksum, secondChecksum)
	}
}
