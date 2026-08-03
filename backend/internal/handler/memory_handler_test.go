package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/agent"
	"github.com/huoguojun123/EffChat/internal/middleware"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
)

func TestLatestMemoryRetryUserTextFromMessages(t *testing.T) {
	messages := []*model.Message{
		{Role: "user", MessageData: []byte(`{"role":"user","content":"first real message"}`)},
		{Role: "assistant", MessageData: []byte(`{"role":"assistant","content":"answer"}`)},
		{Role: "user", MessageData: []byte(`{"role":"user","content":"[summary]","extra":{"_eino_summarization_content_type":"summary"}}`)},
		{Role: "user", MessageData: []byte(`{"role":"user","content":"  latest durable note  "}`)},
	}

	got, err := latestMemoryRetryUserTextFromMessages(messages)
	if err != nil {
		t.Fatalf("latestMemoryRetryUserTextFromMessages: %v", err)
	}
	if got != "latest durable note" {
		t.Fatalf("got %q", got)
	}
}

func TestLatestMemoryRetryUserTextFromMessagesRejectsEmpty(t *testing.T) {
	_, err := latestMemoryRetryUserTextFromMessages([]*model.Message{
		{Role: "assistant", MessageData: []byte(`{"role":"assistant","content":"answer"}`)},
		{Role: "user", MessageData: []byte(`{"role":"user","content":"[summary]","metadata":{"compaction_summary":true}}`)},
	})
	if err == nil {
		t.Fatal("expected no user message error")
	}
}

func TestMemoryUndoErrorClassificationUsesStablePublicMessage(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "not found", err: repository.ErrMemoryChangeNotFound, wantStatus: http.StatusNotFound, wantCode: "memory_change_not_found"},
		{name: "not undoable", err: repository.ErrMemoryChangeNotUndoable, wantStatus: http.StatusConflict, wantCode: "memory_undo_unavailable"},
		{name: "internal", err: errors.New("postgres://secret@internal/private/memory"), wantStatus: http.StatusInternalServerError, wantCode: "memory_undo_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/sessions/1/memory/changes/1/undo", nil)
			ctx.Set("request_id", "req-memory")
			writeMemoryUndoError(ctx, tt.err)
			if recorder.Code != tt.wantStatus || !strings.Contains(recorder.Body.String(), tt.wantCode) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if strings.Contains(recorder.Body.String(), "secret") || strings.Contains(recorder.Body.String(), "/private/memory") {
				t.Fatalf("response leaked internal error: %s", recorder.Body.String())
			}
		})
	}
}

func TestParseSessionIDUsesStablePublicError(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Params = gin.Params{{Key: "id", Value: "not-a-number"}}

	if _, ok := parseSessionID(context); ok {
		t.Fatal("invalid session id was accepted")
	}
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["code"] != "session_id_invalid" || body["retryable"] != false {
		t.Fatalf("response = %#v", body)
	}
}

func TestMemoryMaintenanceFailurePayloadUsesStableCodes(t *testing.T) {
	budget, ok := memoryMaintenanceFailurePayload(fmt.Errorf("capacity detail: %w", agent.ErrMemoryMaintenanceOutputBudgetInsufficient))
	if !ok || budget["code"] != "memory_output_budget_insufficient" || budget["retryable"] != false {
		t.Fatalf("budget payload = %#v", budget)
	}

	outputLimit, ok := memoryMaintenanceFailurePayload(fmt.Errorf("finish detail: %w", agent.ErrMemoryMaintenanceOutputLimit))
	if !ok || outputLimit["code"] != "memory_output_limit" || outputLimit["retryable"] != true {
		t.Fatalf("output-limit payload = %#v", outputLimit)
	}
}

func TestMemoryModelRequestCarriesRegisteredCapabilities(t *testing.T) {
	req := memoryModelRequest(&model.Session{
		ID:            9,
		ModelID:       "gpt-5.6-terra",
		Provider:      "openai",
		MessageFormat: "v1",
		MemoryEnabled: true,
	}, 7, []byte(`{"locale":"zh-CN"}`))
	if req == nil {
		t.Fatal("memoryModelRequest returned nil")
	}
	if req.ModelMaxOutput != 128000 || !req.Reasoning || req.ContextWindow != 1050000 {
		t.Fatalf("manual memory request lost model capabilities: %+v", req)
	}
}

func TestSaveSessionMemoryRejectsStaleEditorContent(t *testing.T) {
	env := setupTestEnv(t)
	created := env.doRequest(http.MethodPost, "/api/v1/sessions", map[string]interface{}{
		"model_id": "gpt-4o-mini",
		"provider": env.channelKey,
		"title":    "Memory conflict",
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create session: status=%d body=%s", created.Code, created.Body.String())
	}
	var session model.Session
	if err := json.Unmarshal(created.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	memoryRepo := repository.NewSessionMemoryRepository(env.db)
	configRepo := repository.NewConfigRepository(env.db)
	oldContent := "## Current Progress\n- Current: old state."
	newContent := "## Current Progress\n- Current: background update."
	if err := memoryRepo.Set(session.ID, oldContent); err != nil {
		t.Fatalf("seed memory: %v", err)
	}
	_, oldUpdatedAt, err := memoryRepo.GetWithUpdatedAt(t.Context(), session.ID)
	if err != nil {
		t.Fatalf("load seeded memory version: %v", err)
	}
	if err := memoryRepo.Set(session.ID, newContent); err != nil {
		t.Fatalf("simulate background update: %v", err)
	}

	router := gin.New()
	auth := router.Group("/api/v1")
	auth.Use(middleware.AuthMiddleware(env.authService))
	auth.PUT("/sessions/:id/memory", SaveSessionMemoryHandler(env.sessionService, memoryRepo, nil, configRepo))
	body, _ := json.Marshal(map[string]interface{}{
		"content":             "## Current Progress\n- Current: stale editor overwrite.",
		"expected_updated_at": oldUpdatedAt,
	})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/sessions/%d/memory", session.ID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+env.token)
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "session_memory_conflict") {
		t.Fatalf("stale save status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	stored, err := memoryRepo.Get(session.ID)
	if err != nil {
		t.Fatalf("get memory: %v", err)
	}
	if !strings.Contains(stored, "background update") || strings.Contains(stored, "stale editor overwrite") {
		t.Fatalf("stale editor overwrote memory: %q", stored)
	}

	if err := memoryRepo.Set(session.ID, "legacy plain text memory"); err != nil {
		t.Fatalf("seed legacy memory: %v", err)
	}
	_, legacyUpdatedAt, err := memoryRepo.GetWithUpdatedAt(t.Context(), session.ID)
	if err != nil {
		t.Fatalf("load legacy memory version: %v", err)
	}
	body, _ = json.Marshal(map[string]interface{}{
		"content":             "## Current Progress\n- Current: normalized legacy memory.",
		"expected_updated_at": legacyUpdatedAt,
	})
	recorder = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/sessions/%d/memory", session.ID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+env.token)
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("legacy memory save status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestSaveSessionMemoryRejectsSecretWithoutDurableTrace(t *testing.T) {
	env := setupTestEnv(t)
	created := env.doRequest(http.MethodPost, "/api/v1/sessions", map[string]interface{}{
		"model_id": "gpt-4o-mini",
		"provider": env.channelKey,
		"title":    "Memory secret guard",
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create session: status=%d body=%s", created.Code, created.Body.String())
	}
	var session model.Session
	if err := json.Unmarshal(created.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode session: %v", err)
	}

	secret := "fixture-password-42"
	saved := env.doRequest(http.MethodPut, fmt.Sprintf("/api/v1/sessions/%d/memory", session.ID), map[string]interface{}{
		"content": "## Decisions\n- password=" + secret,
	})
	if saved.Code != http.StatusBadRequest || !strings.Contains(saved.Body.String(), "invalid_memory_content") {
		t.Fatalf("secret save status=%d body=%s", saved.Code, saved.Body.String())
	}
	if strings.Contains(saved.Body.String(), secret) {
		t.Fatalf("secret save response leaked rejected value: %s", saved.Body.String())
	}

	var memoryCount, changeCount int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM session_memories WHERE session_id = $1 AND content <> ''`, session.ID).Scan(&memoryCount); err != nil {
		t.Fatalf("count session memory: %v", err)
	}
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM session_memory_changes WHERE session_id = $1`, session.ID).Scan(&changeCount); err != nil {
		t.Fatalf("count session memory changes: %v", err)
	}
	if memoryCount != 0 || changeCount != 0 {
		t.Fatalf("rejected secret left durable state: memory=%d changes=%d", memoryCount, changeCount)
	}
}

func TestSessionMemoryToggleDoesNotCommitDocumentOrChangeHistory(t *testing.T) {
	env := setupTestEnv(t)
	created := env.doRequest(http.MethodPost, "/api/v1/sessions", map[string]interface{}{
		"model_id": "gpt-4o-mini",
		"provider": env.channelKey,
		"title":    "Memory toggle boundary",
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create session: status=%d body=%s", created.Code, created.Body.String())
	}
	var session model.Session
	if err := json.Unmarshal(created.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode session: %v", err)
	}

	baseline := "## Project Context\n- Stable synthetic baseline."
	saved := env.doRequest(http.MethodPut, fmt.Sprintf("/api/v1/sessions/%d/memory", session.ID), map[string]interface{}{
		"content": baseline,
	})
	if saved.Code != http.StatusOK {
		t.Fatalf("seed memory: status=%d body=%s", saved.Code, saved.Body.String())
	}

	var beforeContent string
	var beforeChanges int
	if err := env.db.QueryRow(`SELECT content FROM session_memories WHERE session_id = $1`, session.ID).Scan(&beforeContent); err != nil {
		t.Fatalf("load baseline memory: %v", err)
	}
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM session_memory_changes WHERE session_id = $1`, session.ID).Scan(&beforeChanges); err != nil {
		t.Fatalf("count baseline changes: %v", err)
	}

	toggled := env.doRequest(http.MethodPatch, fmt.Sprintf("/api/v1/sessions/%d", session.ID), map[string]interface{}{
		"memory_enabled": false,
	})
	if toggled.Code != http.StatusOK {
		t.Fatalf("toggle memory: status=%d body=%s", toggled.Code, toggled.Body.String())
	}

	var enabled bool
	var afterContent string
	var afterChanges int
	if err := env.db.QueryRow(`SELECT memory_enabled FROM sessions WHERE id = $1`, session.ID).Scan(&enabled); err != nil {
		t.Fatalf("load memory setting: %v", err)
	}
	if err := env.db.QueryRow(`SELECT content FROM session_memories WHERE session_id = $1`, session.ID).Scan(&afterContent); err != nil {
		t.Fatalf("load memory after toggle: %v", err)
	}
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM session_memory_changes WHERE session_id = $1`, session.ID).Scan(&afterChanges); err != nil {
		t.Fatalf("count changes after toggle: %v", err)
	}
	if enabled {
		t.Fatal("memory_enabled remained true after toggle")
	}
	if afterContent != beforeContent {
		t.Fatalf("memory toggle changed document: before=%q after=%q", beforeContent, afterContent)
	}
	if afterChanges != beforeChanges {
		t.Fatalf("memory toggle created change history: before=%d after=%d", beforeChanges, afterChanges)
	}
}
