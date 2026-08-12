package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"github.com/cloudwego/eino/schema"
	sessionmemory "github.com/huoguojun123/EffChat/internal/memory"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
	"github.com/huoguojun123/EffChat/internal/testutil"
)

func TestMemoryMaintenanceOutputTokenBudget(t *testing.T) {
	tests := []struct {
		maxChars int
		want     int
	}{
		{maxChars: 4000, want: 8192},
		{maxChars: 8000, want: 12288},
		{maxChars: 12000, want: 16384},
		{maxChars: 16000, want: 20480},
	}
	for _, testCase := range tests {
		t.Run(fmt.Sprintf("%d_chars", testCase.maxChars), func(t *testing.T) {
			limits := sessionmemory.NormalizeLimits(testCase.maxChars, 0)
			if got := memoryMaintenanceOutputTokenBudget(limits); got != testCase.want {
				t.Fatalf("memoryMaintenanceOutputTokenBudget(%d) = %d, want %d", testCase.maxChars, got, testCase.want)
			}
		})
	}
}

func TestPrepareMemoryMaintenanceModelRequestOwnsBudgetAndThinking(t *testing.T) {
	original := &ChatRequest{
		ModelID:         "gpt-5.6-terra",
		Provider:        "openai",
		MaxTokens:       1024,
		ModelMaxOutput:  128000,
		RuntimeResolved: true,
		Reasoning:       true,
		ThinkingFormat:  string(modelbank.ThinkingFormatOpenAIGPT56),
		ThinkingEffort:  string(modelbank.ThinkingEffortHigh),
	}

	got, err := prepareMemoryMaintenanceModelRequest(original, sessionmemory.NormalizeLimits(16000, 0))
	if err != nil {
		t.Fatalf("prepareMemoryMaintenanceModelRequest() error = %v", err)
	}
	if got == original {
		t.Fatal("memory maintenance must clone the active request")
	}
	if got.MaxTokens != 20480 || !got.SuppressThinking {
		t.Fatalf("prepared request budget/thinking = %d/%t, want 20480/true", got.MaxTokens, got.SuppressThinking)
	}
	if original.MaxTokens != 1024 || original.SuppressThinking {
		t.Fatalf("active request was mutated: %+v", original)
	}
}

func TestPrepareMemoryMaintenanceModelRequestRejectsKnownInsufficientCapability(t *testing.T) {
	limits := sessionmemory.NormalizeLimits(16000, 0)
	required := memoryMaintenanceOutputTokenBudget(limits)
	allowed, err := prepareMemoryMaintenanceModelRequest(&ChatRequest{
		ModelID:         "accepted-exact-model",
		Provider:        "openai",
		ModelMaxOutput:  required,
		RuntimeResolved: true,
	}, limits)
	if err != nil || allowed.MaxTokens != required {
		t.Fatalf("exact model capability should be allowed: request=%+v err=%v", allowed, err)
	}

	_, err = prepareMemoryMaintenanceModelRequest(&ChatRequest{
		ModelID:         "accepted-small-model",
		Provider:        "openai",
		ModelMaxOutput:  required - 1,
		RuntimeResolved: true,
	}, limits)
	if !errors.Is(err, ErrMemoryMaintenanceOutputBudgetInsufficient) {
		t.Fatalf("known insufficient model error = %v", err)
	}
	var budgetErr *memoryOutputBudgetError
	if !errors.As(err, &budgetErr) || budgetErr.RequiredTokens != required || budgetErr.AvailableTokens != required-1 {
		t.Fatalf("budget error details = %+v", budgetErr)
	}

	// A manual request has no accepted runtime snapshot, so current registry
	// capabilities still protect compact/retry before a provider call.
	_, err = prepareMemoryMaintenanceModelRequest(&ChatRequest{
		ModelID:  "gpt-4o",
		Provider: "openai",
	}, limits)
	if !errors.Is(err, ErrMemoryMaintenanceOutputBudgetInsufficient) {
		t.Fatalf("registry capability error = %v", err)
	}
}

func TestPrepareMemoryMaintenanceModelRequestPreservesUnknownCapability(t *testing.T) {
	limits := sessionmemory.NormalizeLimits(16000, 0)
	got, err := prepareMemoryMaintenanceModelRequest(&ChatRequest{
		ModelID:         "custom-model-with-unknown-output",
		Provider:        "custom-provider",
		RuntimeResolved: true,
	}, limits)
	if err != nil {
		t.Fatalf("unknown capability should remain callable: %v", err)
	}
	if got.MaxTokens != 20480 || got.ModelMaxOutput != 0 {
		t.Fatalf("unknown capability request = max:%d model_cap:%d", got.MaxTokens, got.ModelMaxOutput)
	}
}

func TestGenerateMemoryMaintenanceTextRejectsOutputLimit(t *testing.T) {
	for _, finishReason := range []string{"length", "max_tokens", "MAX_TOKENS", "max_output_tokens"} {
		t.Run(finishReason, func(t *testing.T) {
			model := &memoryMaintenanceStreamModel{chunks: []*schema.Message{{
				Role:         schema.Assistant,
				Content:      `{"action":"none"}`,
				ResponseMeta: &schema.ResponseMeta{FinishReason: finishReason},
			}}}

			if _, err := generateMemoryMaintenanceText(t.Context(), model, nil); !errors.Is(err, ErrMemoryMaintenanceOutputLimit) {
				t.Fatalf("output-limit stream error = %v", err)
			}
			if model.generateCalled {
				t.Fatal("memory maintenance must remain stream-only")
			}
		})
	}
}

func TestMemoryMaintenanceInsufficientBudgetSkipsProviderAndPreservesLargeMemory(t *testing.T) {
	db := testutil.OpenPostgresTestDB(t)
	userID, sessionID := createMemoryMaintenanceTestSession(t, db)
	configRepo := repository.NewConfigRepository(db)
	if err := configRepo.UpdateAdminEditable("memory_max_chars", json.RawMessage(`16000`)); err != nil {
		t.Fatalf("set memory limit: %v", err)
	}
	memoryRepo := repository.NewSessionMemoryRepository(db)
	current := largeFictionalMemory(t)
	if _, err := memoryRepo.SaveWithChange(t.Context(), repository.SaveSessionMemoryInput{
		SessionID: sessionID,
		UserID:    userID,
		Content:   current,
		Source:    "manual",
		Action:    "update",
		Summary:   "初始化虚构容量测试记忆",
		MaxChars:  16000,
	}); err != nil {
		t.Fatalf("seed large memory: %v", err)
	}

	var providerCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerCalls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	taskRuns := repository.NewModelTaskRunRepository(db)
	agent := NewEinoAgent(service.NewChannelService(nil), nil, 4096, configRepo, memoryRepo, taskRuns, nil, nil, nil)
	err := agent.MaintainSessionMemory(t.Context(), MemoryMaintenanceRequest{
		SessionID:      sessionID,
		UserID:         userID,
		RunID:          "memory-budget-insufficient",
		UserText:       "请记住这个虚构项目的新阶段",
		MemoryEnabled:  true,
		Force:          true,
		IgnoreCooldown: true,
		ModelRequest: &ChatRequest{
			ModelID:         "small-memory-model",
			Provider:        "small-memory-provider",
			ModelMaxOutput:  16384,
			RuntimeResolved: true,
			RuntimeChannel: &model.AIChannel{
				Key:     "small-memory-provider",
				Adapter: service.AdapterOpenAICompatible,
				BaseURL: server.URL + "/v1",
				APIKey:  "test-key",
				Enabled: true,
			},
		},
	})
	if !errors.Is(err, ErrMemoryMaintenanceOutputBudgetInsufficient) {
		t.Fatalf("maintenance error = %v", err)
	}
	if providerCalls.Load() != 0 {
		t.Fatalf("provider was called %d times despite insufficient declared capability", providerCalls.Load())
	}
	stored, readErr := memoryRepo.Get(sessionID)
	if readErr != nil {
		t.Fatalf("read preserved memory: %v", readErr)
	}
	if stored != current {
		t.Fatal("insufficient-capability failure changed existing memory")
	}
	latest, readErr := taskRuns.LatestForSession(t.Context(), sessionID, userID, repository.ModelTaskMemoryMaintenance)
	if readErr != nil {
		t.Fatalf("read task run: %v", readErr)
	}
	if latest == nil || latest.Status != repository.ModelTaskStatusFailed || latest.ErrorType != "memory_output_budget_insufficient" || latest.Provider != "small-memory-provider" || latest.ModelID != "small-memory-model" || latest.RetryAfter != nil {
		t.Fatalf("budget task run = %+v", latest)
	}
}

func TestMemoryMaintenanceOutputLimitDoesNotPersist(t *testing.T) {
	db := testutil.OpenPostgresTestDB(t)
	userID, sessionID := createMemoryMaintenanceTestSession(t, db)
	memoryRepo := repository.NewSessionMemoryRepository(db)
	current := "## User Preferences\n- 默认使用简洁中文回答。"
	if _, err := memoryRepo.SaveWithChange(t.Context(), repository.SaveSessionMemoryInput{
		SessionID: sessionID,
		UserID:    userID,
		Content:   current,
		Source:    "manual",
		Action:    "update",
		Summary:   "初始化测试记忆",
		MaxChars:  4000,
	}); err != nil {
		t.Fatalf("seed memory: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-memory-limit\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"memory-output-limit\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"{\\\"action\\\":\\\"update\\\",\\\"content\\\":\\\"## User Preferences\\\\n- 默认使用简洁中文回答。\\\\n- 保留新的虚构偏好。\\\",\\\"summary\\\":\\\"更新测试记忆\\\"}\"},\"finish_reason\":\"length\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	taskRuns := repository.NewModelTaskRunRepository(db)
	agent := NewEinoAgent(service.NewChannelService(nil), nil, 4096, repository.NewConfigRepository(db), memoryRepo, taskRuns, nil, nil, nil)
	err := agent.MaintainSessionMemory(t.Context(), MemoryMaintenanceRequest{
		SessionID:      sessionID,
		UserID:         userID,
		RunID:          "memory-output-limit",
		UserText:       "请记住新的虚构偏好",
		MemoryEnabled:  true,
		Force:          true,
		IgnoreCooldown: true,
		ModelRequest: &ChatRequest{
			ModelID:         "memory-output-limit",
			Provider:        "memory-output-provider",
			ModelMaxOutput:  16384,
			RuntimeResolved: true,
			RuntimeChannel: &model.AIChannel{
				Key:     "memory-output-provider",
				Adapter: service.AdapterOpenAICompatible,
				BaseURL: server.URL + "/v1",
				APIKey:  "test-key",
				Enabled: true,
			},
		},
	})
	if !errors.Is(err, ErrMemoryMaintenanceOutputLimit) {
		t.Fatalf("maintenance error = %v", err)
	}
	stored, readErr := memoryRepo.Get(sessionID)
	if readErr != nil {
		t.Fatalf("read preserved memory: %v", readErr)
	}
	if stored != current {
		t.Fatalf("output-limit result changed memory: %q", stored)
	}
	latest, readErr := taskRuns.LatestForSession(t.Context(), sessionID, userID, repository.ModelTaskMemoryMaintenance)
	if readErr != nil {
		t.Fatalf("read task run: %v", readErr)
	}
	if latest == nil || latest.Status != repository.ModelTaskStatusFailed || latest.ErrorType != "memory_output_limit" {
		t.Fatalf("output-limit task run = %+v", latest)
	}
}

func largeFictionalMemory(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("## Project Context\n")
	for i := 0; i < 56; i++ {
		fmt.Fprintf(&b, "- 虚构项目容量条目 %02d：%s\n", i+1, strings.Repeat("用于验证完整重写预算的模拟状态。", 14))
	}
	content := strings.TrimSpace(b.String())
	chars := utf8.RuneCountInString(content)
	const maxChars = 16000
	if chars < maxChars*4/5 || chars > maxChars {
		t.Fatalf("large fictional memory has %d characters, want %d..%d", chars, maxChars*4/5, maxChars)
	}
	return content
}
