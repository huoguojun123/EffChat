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
	"github.com/huoguojun123/effchat/internal/middleware"
	"github.com/huoguojun123/effchat/internal/model"
	"github.com/huoguojun123/effchat/internal/repository"
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

func TestMemoryUndoErrorPayloadUsesStablePublicMessage(t *testing.T) {
	payload := memoryUndoErrorPayload(errors.New("sql: internal detail"))
	if payload["code"] != "memory_undo_unavailable" {
		t.Fatalf("code = %v", payload["code"])
	}
	if payload["error"] == "sql: internal detail" {
		t.Fatal("internal error leaked")
	}

	notFound := memoryUndoErrorPayload(repository.ErrMemoryChangeNotFound)
	if notFound["code"] != "memory_change_not_found" {
		t.Fatalf("not found code = %v", notFound["code"])
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
