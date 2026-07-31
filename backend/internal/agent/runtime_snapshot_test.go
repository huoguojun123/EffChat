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
	if _, err := channelService.SaveAIChannel(&service.AIChannelInput{
		Key: channelKey, DisplayName: "Runtime snapshot", Adapter: service.AdapterOpenAICompatible,
		BaseURL: "https://gateway.example.test/v1", APIKey: "secret-key-one", Enabled: &enabled,
	}); err != nil {
		t.Fatalf("save channel: %v", err)
	}
	modelbank.Register(&modelbank.ModelInfo{
		ID: modelID, DisplayName: modelID, Provider: channelKey, Enabled: true, ThinkingFormat: "auto",
		Capabilities: modelbank.ModelCapabilities{
			Vision: true, ToolUse: true, Reasoning: true, SearchImpl: modelbank.SearchImplTool,
			ContextWindow: 128000, MaxOutput: 8192,
		},
	})

	agent := NewEinoAgent(
		channelService,
		service.NewToolConfigService(repository.NewToolConfigRepository(db)),
		64000,
		repository.NewConfigRepository(db),
		nil,
		nil,
		nil,
		nil,
		nil,
	)
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
	if snapshot.ToolConfigState.State == "" || snapshot.SearchConfigState.Search.State == "" || snapshot.MemoryState.State == "" {
		t.Fatalf("runtime dependency states missing: %+v", snapshot)
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
