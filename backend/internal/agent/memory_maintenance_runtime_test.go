package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	sessionmemory "github.com/huoguojun123/EffChat/internal/memory"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
	"github.com/huoguojun123/EffChat/internal/testutil"
)

type memoryLifecycleStreamModel struct {
	stream func(context.Context) (*schema.StreamReader[*schema.Message], error)
}

func (m *memoryLifecycleStreamModel) Generate(context.Context, []*schema.Message, ...einoModel.Option) (*schema.Message, error) {
	return nil, errors.New("memory maintenance must not use Generate")
}

func (m *memoryLifecycleStreamModel) Stream(ctx context.Context, _ []*schema.Message, _ ...einoModel.Option) (*schema.StreamReader[*schema.Message], error) {
	return m.stream(ctx)
}

func (m *memoryLifecycleStreamModel) WithTools(_ []*schema.ToolInfo) (einoModel.ToolCallingChatModel, error) {
	return m, nil
}

func TestMemoryMaintenanceModelStreamsAfterSetupCancellationAndClonesRequest(t *testing.T) {
	requestBody := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		select {
		case requestBody <- body:
		default:
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-memory\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"memory-stream-test\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"{\\\"action\\\":\\\"\"},\"finish_reason\":null}]}\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-memory\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"memory-stream-test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"none\\\"}\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	temperature := 0.25
	chatReq := &ChatRequest{
		UserID:          11,
		SessionID:       22,
		ModelID:         "memory-stream-test",
		Provider:        "memory-stream-provider",
		MaxTokens:       777,
		Temperature:     &temperature,
		RuntimeResolved: true,
		RuntimeChannel: &model.AIChannel{
			Key:     "memory-stream-provider",
			Adapter: service.AdapterOpenAICompatible,
			BaseURL: server.URL + "/v1",
			APIKey:  "test-key",
			Enabled: true,
		},
	}
	agent := NewEinoAgent(service.NewChannelService(nil), nil, 4096, nil, nil, nil, nil, nil, nil)

	setupCtx, cancelSetup := context.WithCancel(t.Context())
	chatModel, provider, modelID, err := agent.buildMemoryMaintenanceModel(setupCtx, MemoryMaintenanceRequest{
		SessionID:    chatReq.SessionID,
		UserID:       chatReq.UserID,
		ModelRequest: chatReq,
	}, sessionmemory.DefaultLimits())
	if err != nil {
		cancelSetup()
		t.Fatalf("buildMemoryMaintenanceModel() error = %v", err)
	}
	cancelSetup()

	got, err := generateMemoryMaintenanceText(t.Context(), chatModel, nil)
	if err != nil {
		t.Fatalf("generateMemoryMaintenanceText() error = %v", err)
	}
	if got != `{"action":"none"}` {
		t.Fatalf("streamed memory output = %q", got)
	}
	if provider != chatReq.Provider || modelID != chatReq.ModelID {
		t.Fatalf("model identity = %q/%q, want %q/%q", provider, modelID, chatReq.Provider, chatReq.ModelID)
	}
	if chatReq.MaxTokens != 777 || chatReq.UserID != 11 || chatReq.SessionID != 22 {
		t.Fatalf("memory setup mutated the caller request: %+v", chatReq)
	}

	var providerRequest map[string]interface{}
	select {
	case body := <-requestBody:
		if err := json.Unmarshal(body, &providerRequest); err != nil {
			t.Fatalf("decode provider request: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("memory provider request was not captured")
	}
	maxTokens, _ := providerRequest["max_tokens"].(float64)
	if maxTokens == 0 {
		maxTokens, _ = providerRequest["max_completion_tokens"].(float64)
	}
	wantOutputBudget := memoryMaintenanceOutputTokenBudget(sessionmemory.DefaultLimits())
	if int(maxTokens) != wantOutputBudget {
		t.Fatalf("memory output limit = %v, want %d", maxTokens, wantOutputBudget)
	}
}

func TestMemoryMaintenanceGPT56KeepsCompletionFieldWhileSuppressingThinking(t *testing.T) {
	requestBody := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requestBody <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-memory-gpt56\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-5.6-terra\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"{\\\"action\\\":\\\"none\\\"}\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	chatReq := &ChatRequest{
		ModelID:         "gpt-5.6-terra",
		Provider:        "openai",
		ModelMaxOutput:  128000,
		RuntimeResolved: true,
		Reasoning:       true,
		ThinkingFormat:  string(modelbank.ThinkingFormatOpenAIGPT56),
		ThinkingEffort:  string(modelbank.ThinkingEffortHigh),
		RuntimeChannel: &model.AIChannel{
			Key:     "openai",
			Adapter: service.AdapterOpenAICompatible,
			BaseURL: server.URL + "/v1",
			APIKey:  "test-key",
			Enabled: true,
		},
	}
	agent := NewEinoAgent(service.NewChannelService(nil), nil, 4096, nil, nil, nil, nil, nil, nil)
	limits := sessionmemory.NormalizeLimits(8000, 0)
	chatModel, _, _, err := agent.buildMemoryMaintenanceModel(t.Context(), MemoryMaintenanceRequest{
		SessionID:    22,
		UserID:       11,
		ModelRequest: chatReq,
	}, limits)
	if err != nil {
		t.Fatalf("build memory model: %v", err)
	}
	if _, err := generateMemoryMaintenanceText(t.Context(), chatModel, nil); err != nil {
		t.Fatalf("generate memory output: %v", err)
	}

	var providerRequest map[string]interface{}
	select {
	case body := <-requestBody:
		if err := json.Unmarshal(body, &providerRequest); err != nil {
			t.Fatalf("decode provider request: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("memory provider request was not captured")
	}
	wantBudget := memoryMaintenanceOutputTokenBudget(limits)
	if got, _ := providerRequest["max_completion_tokens"].(float64); int(got) != wantBudget {
		t.Fatalf("max_completion_tokens = %v, want %d", got, wantBudget)
	}
	if _, ok := providerRequest["max_tokens"]; ok {
		t.Fatalf("GPT-5.6 utility request used max_tokens: %v", providerRequest)
	}
	if _, ok := providerRequest["reasoning_effort"]; ok {
		t.Fatalf("memory request leaked reasoning_effort: %v", providerRequest)
	}
}

func TestMemoryMaintenanceDrainCancelsStreamAfterFirstOutput(t *testing.T) {
	firstOutput := make(chan struct{})
	model := &memoryLifecycleStreamModel{
		stream: func(ctx context.Context) (*schema.StreamReader[*schema.Message], error) {
			reader, writer := schema.Pipe[*schema.Message](1)
			go func() {
				defer writer.Close()
				writer.Send(&schema.Message{Role: schema.Assistant, Content: `{"action":"`}, nil)
				close(firstOutput)
				<-ctx.Done()
				writer.Send(nil, context.Cause(ctx))
			}()
			return reader, nil
		},
	}
	agent := &EinoAgent{}
	if !agent.startMemoryBackgroundTask() {
		t.Fatal("memory task should start before drain")
	}

	runErr := make(chan error, 1)
	go func() {
		defer agent.memoryTasks.Done()
		_, err := generateMemoryMaintenanceText(agent.backgroundTaskContext(), model, nil)
		runErr <- err
	}()
	select {
	case <-firstOutput:
	case <-time.After(time.Second):
		t.Fatal("memory stream did not produce its first output")
	}

	drainCtx, cancelDrain := context.WithTimeout(t.Context(), time.Second)
	defer cancelDrain()
	if !agent.DrainMemoryTasks(drainCtx) {
		t.Fatal("memory tasks did not drain after canceling the active stream")
	}
	if err := <-runErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("memory stream error = %v, want context canceled", err)
	}
}

func TestMemoryMaintenanceControlStagesHonorShorterParentDeadline(t *testing.T) {
	db := testutil.OpenPostgresTestDB(t)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	held, err := db.Conn(t.Context())
	if err != nil {
		t.Fatalf("hold database connection: %v", err)
	}
	defer held.Close()

	agent := &EinoAgent{
		channelService: service.NewChannelService(repository.NewChannelRepository(db)),
		configRepo:     repository.NewConfigRepository(db),
		memoryRepo:     repository.NewSessionMemoryRepository(db),
		taskRunRepo:    repository.NewModelTaskRunRepository(db),
	}
	assertDeadline := func(name string, run func(context.Context) error) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
			defer cancel()
			started := time.Now()
			if err := run(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("error = %v, want deadline exceeded", err)
			}
			if elapsed := time.Since(started); elapsed > time.Second {
				t.Fatalf("stage ignored the shorter parent deadline: %v", elapsed)
			}
		})
	}

	assertDeadline("memory read", func(ctx context.Context) error {
		return agent.CompactSessionMemory(ctx, MemoryMaintenanceRequest{
			SessionID: 1,
			UserID:    1,
		})
	})
	assertDeadline("cooldown query", func(ctx context.Context) error {
		return agent.MaintainSessionMemory(ctx, MemoryMaintenanceRequest{
			SessionID:     1,
			UserID:        1,
			UserText:      "记住这个长期偏好",
			MemoryEnabled: true,
			Force:         true,
		})
	})
	assertDeadline("memory limits", func(ctx context.Context) error {
		_, err := agent.memoryLimitsContext(ctx)
		return err
	})
	assertDeadline("model channel setup", func(ctx context.Context) error {
		_, _, _, err := agent.buildMemoryMaintenanceModel(ctx, MemoryMaintenanceRequest{
			SessionID: 1,
			UserID:    1,
			ModelRequest: &ChatRequest{
				ModelID:  "memory-blocked-test",
				Provider: "blocked-provider",
			},
		}, sessionmemory.DefaultLimits())
		return err
	})
}

func TestMemoryMaintenanceStageContextsPreserveShorterParentDeadline(t *testing.T) {
	tests := []struct {
		name string
		open func(context.Context) (context.Context, context.CancelFunc)
	}{
		{name: "control", open: memoryMaintenanceControlContext},
		{name: "persistence", open: memoryMaintenancePersistenceContext},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			parent, cancelParent := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancelParent()
			stage, cancelStage := testCase.open(parent)
			defer cancelStage()

			select {
			case <-stage.Done():
				if !errors.Is(stage.Err(), context.DeadlineExceeded) {
					t.Fatalf("stage error = %v, want deadline exceeded", stage.Err())
				}
			case <-time.After(time.Second):
				t.Fatal("stage ignored the shorter parent deadline")
			}
		})
	}
}

func TestAutomaticMemoryContentConflictIsSkippedWithoutCooldown(t *testing.T) {
	db := testutil.OpenPostgresTestDB(t)
	userID, sessionID := createMemoryMaintenanceTestSession(t, db)
	memoryRepo := repository.NewSessionMemoryRepository(db)
	taskRuns := repository.NewModelTaskRunRepository(db)
	if _, err := memoryRepo.SaveWithChange(t.Context(), repository.SaveSessionMemoryInput{
		SessionID: sessionID,
		UserID:    userID,
		Content:   "## User Preferences\n- 默认使用简洁中文回答。",
		Source:    "manual",
		Action:    "update",
		Summary:   "初始化测试记忆",
		MaxChars:  12000,
	}); err != nil {
		t.Fatalf("seed session memory: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := db.Exec(`
			UPDATE session_memories
			SET content = $1, updated_at = NOW()
			WHERE session_id = $2
		`, "## User Preferences\n- 并发编辑后的新内容。", sessionID); err != nil {
			t.Errorf("write concurrent memory change: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-memory-conflict\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"memory-conflict-test\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"{\\\"action\\\":\\\"update\\\",\\\"content\\\":\\\"## User Preferences\\\\n- 模型生成的新内容。\\\",\\\"summary\\\":\\\"更新会话记忆\\\"}\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	agent := NewEinoAgent(
		service.NewChannelService(nil),
		nil,
		4096,
		repository.NewConfigRepository(db),
		memoryRepo,
		taskRuns,
		nil,
		nil,
		nil,
	)
	err := agent.MaintainSessionMemory(t.Context(), MemoryMaintenanceRequest{
		SessionID:      sessionID,
		UserID:         userID,
		RunID:          "memory-content-conflict",
		UserText:       "以后都使用中文回答",
		MemoryEnabled:  true,
		Force:          true,
		IgnoreCooldown: true,
		ModelRequest: &ChatRequest{
			UserID:          userID,
			SessionID:       sessionID,
			ModelID:         "memory-conflict-test",
			Provider:        "memory-conflict-provider",
			RuntimeResolved: true,
			RuntimeChannel: &model.AIChannel{
				Key:     "memory-conflict-provider",
				Adapter: service.AdapterOpenAICompatible,
				BaseURL: server.URL + "/v1",
				APIKey:  "test-key",
				Enabled: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("automatic content conflict should be a stale skip, got %v", err)
	}

	stored, err := memoryRepo.Get(sessionID)
	if err != nil {
		t.Fatalf("read session memory: %v", err)
	}
	if stored != "## User Preferences\n- 并发编辑后的新内容。" {
		t.Fatalf("stale model output overwrote concurrent memory: %q", stored)
	}
	latest, err := taskRuns.LatestForSession(t.Context(), sessionID, userID, repository.ModelTaskMemoryMaintenance)
	if err != nil {
		t.Fatalf("read memory task run: %v", err)
	}
	if latest == nil || latest.Status != repository.ModelTaskStatusSkipped || latest.RetryAfter != nil {
		t.Fatalf("content conflict task run = %+v, want skipped without cooldown", latest)
	}
	var metadata map[string]string
	if err := json.Unmarshal(latest.Metadata, &metadata); err != nil {
		t.Fatalf("decode conflict metadata: %v", err)
	}
	if metadata["reason"] != "memory_changed" {
		t.Fatalf("conflict reason = %q, want memory_changed", metadata["reason"])
	}
}

func TestRecordMemoryTaskRunClearsCooldownOnlyForLifecycleCancellation(t *testing.T) {
	db := testutil.OpenPostgresTestDB(t)
	taskRuns := repository.NewModelTaskRunRepository(db)
	agent := &EinoAgent{taskRunRepo: taskRuns}
	started := time.Now().Add(-time.Second)

	record := func(ctx context.Context, runID string) {
		t.Helper()
		retryAfter := time.Now().Add(30 * time.Minute)
		agent.recordUtilityTaskRun(ctx, repository.RecordModelTaskRunInput{
			TaskKey:      repository.ModelTaskMemoryMaintenance,
			RunID:        runID,
			Source:       repository.ModelTaskSourceAuto,
			Status:       repository.ModelTaskStatusFailed,
			TargetType:   "memory",
			ErrorType:    "canceled",
			ErrorMessage: "lifecycle canceled",
			RetryAfter:   &retryAfter,
			StartedAt:    started,
			FinishedAt:   time.Now(),
		})
	}
	retryAfterForRun := func(runID string) sql.NullTime {
		t.Helper()
		var retryAfter sql.NullTime
		if err := db.QueryRow(`SELECT retry_after FROM model_task_runs WHERE run_id = $1`, runID).Scan(&retryAfter); err != nil {
			t.Fatalf("read task run %q: %v", runID, err)
		}
		return retryAfter
	}

	canceledCtx, cancel := context.WithCancel(t.Context())
	cancel()
	record(canceledCtx, "memory-canceled")
	if retryAfter := retryAfterForRun("memory-canceled"); retryAfter.Valid {
		t.Fatalf("lifecycle cancellation created cooldown: %v", retryAfter.Time)
	}

	record(t.Context(), "memory-provider-failure")
	if retryAfter := retryAfterForRun("memory-provider-failure"); !retryAfter.Valid {
		t.Fatal("provider failure lost its automatic retry cooldown")
	}
}

func createMemoryMaintenanceTestSession(t *testing.T, db *sql.DB) (userID, sessionID int64) {
	t.Helper()
	user := &model.User{
		Username:     fmt.Sprintf("memory_runtime_%d", time.Now().UnixNano()),
		PasswordHash: "test",
		Role:         "user",
		Permissions:  []byte(`{}`),
		Preferences:  []byte(`{}`),
		IsActive:     true,
	}
	if err := repository.NewUserRepository(db).Create(user); err != nil {
		t.Fatalf("create memory test user: %v", err)
	}
	session := &model.Session{
		UserID:        user.ID,
		Title:         "Memory runtime test",
		ModelID:       "memory-runtime-test",
		Provider:      "memory-runtime-provider",
		MessageFormat: "v1",
		MemoryEnabled: true,
		Metadata:      []byte(`{}`),
	}
	if err := repository.NewSessionRepository(db).Create(session); err != nil {
		t.Fatalf("create memory test session: %v", err)
	}
	return user.ID, session.ID
}
