package handler

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
)

func TestWriteSkillErrorContract(t *testing.T) {
	internal := errors.New("postgres://fixture:secret@db.example/effchat /srv/private/skill")
	tests := []struct {
		name      string
		err       error
		status    int
		code      string
		retryable bool
		requestID bool
	}{
		{name: "invalid", err: &service.SkillError{Kind: service.SkillErrorInvalid, Message: "invalid Skill package", Err: internal}, status: http.StatusBadRequest, code: "skill_invalid"},
		{name: "not found", err: &service.SkillError{Kind: service.SkillErrorNotFound, Message: "Skill not found", Err: internal}, status: http.StatusNotFound, code: "skill_not_found"},
		{name: "not authorized", err: &service.SkillError{Kind: service.SkillErrorNotAuthorized, Message: "Skill is not authorized", Err: internal}, status: http.StatusForbidden, code: "skill_not_authorized"},
		{name: "conflict", err: &service.SkillError{Kind: service.SkillErrorConflict, Message: "selected Skill candidate is no longer available", Err: internal}, status: http.StatusConflict, code: "skill_conflict"},
		{name: "session missing", err: &service.SkillError{Kind: service.SkillErrorSessionNotFound, Message: "session not found", Err: internal}, status: http.StatusNotFound, code: "session_not_found"},
		{name: "source unavailable", err: &service.SkillError{Kind: service.SkillErrorSourceUnavailable, Message: "Skill Git source is unavailable", Err: internal}, status: http.StatusBadGateway, code: "skill_source_unavailable", retryable: true, requestID: true},
		{name: "internal", err: internal, status: http.StatusInternalServerError, code: "skill_update_failed", retryable: true, requestID: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPatch, "/api/v1/admin/skills/fixture", nil)
			ctx.Set("request_id", "req-skill-error")

			writeSkillError(ctx, "update", tt.err)
			body := decodeUploadError(t, recorder)
			if recorder.Code != tt.status || body.Code != tt.code || body.Retryable != tt.retryable {
				t.Fatalf("response=%d %+v", recorder.Code, body)
			}
			if tt.requestID && body.RequestID != "req-skill-error" {
				t.Fatalf("missing request ID: %+v", body)
			}
			if !tt.requestID && body.RequestID != "" {
				t.Fatalf("unexpected request ID: %+v", body)
			}
			for _, secret := range []string{"postgres://", "fixture:secret", "/srv/private/skill"} {
				if strings.Contains(recorder.Body.String(), secret) {
					t.Fatalf("public response leaked %q: %s", secret, recorder.Body.String())
				}
			}
		})
	}
}

func TestSkillHandlersClassifyRepositoryFailures(t *testing.T) {
	t.Run("missing Skill", func(t *testing.T) {
		db := setupHandlerTestDB(t)
		const adminUserID = int64(91008)
		if _, err := db.Exec(
			`INSERT INTO users (id, username, password_hash, role, is_active, permissions, preferences)
			 VALUES ($1, $2, 'fixture-hash', 'admin', true, '{}', '{}')`,
			adminUserID,
			fmt.Sprintf("skill_contract_%d", adminUserID),
		); err != nil {
			t.Fatalf("seed Skill admin actor: %v", err)
		}
		t.Cleanup(func() { _, _ = db.Exec("DELETE FROM users WHERE id = $1", adminUserID) })
		svc := service.NewSkillService(repository.NewSkillRepository(db), repository.NewUserRepository(db), repository.NewSessionRepository(db))
		recorder := serveSkillHandler(http.MethodPatch, "/admin/skills/:id", "/admin/skills/missing", []byte(`{"name":"fixture"}`), adminUserID, UpdateSkillHandler(svc))
		assertSkillHandlerError(t, recorder, http.StatusNotFound, "skill_not_found", false, false)
	})

	t.Run("closed repository", func(t *testing.T) {
		db := setupHandlerTestDB(t)
		svc := service.NewSkillService(repository.NewSkillRepository(db), repository.NewUserRepository(db), repository.NewSessionRepository(db))
		if err := db.Close(); err != nil {
			t.Fatalf("close Skill database: %v", err)
		}
		recorder := serveSkillHandler(http.MethodGet, "/admin/skills", "/admin/skills", nil, 0, ListAdminSkillsHandler(svc))
		assertSkillHandlerError(t, recorder, http.StatusInternalServerError, "skill_list_failed", true, true)
	})

	t.Run("missing session", func(t *testing.T) {
		db := setupHandlerTestDB(t)
		const userID = int64(91007)
		if _, err := db.Exec(
			`INSERT INTO users (id, username, password_hash, role, is_active, permissions, preferences)
			 VALUES ($1, $2, 'fixture-hash', 'user', true, '{}', '{}')`,
			userID,
			fmt.Sprintf("skill_contract_%d", userID),
		); err != nil {
			t.Fatalf("seed Skill user: %v", err)
		}
		t.Cleanup(func() { _, _ = db.Exec("DELETE FROM users WHERE id = $1", userID) })
		svc := service.NewSkillService(repository.NewSkillRepository(db), repository.NewUserRepository(db), repository.NewSessionRepository(db))
		recorder := serveSkillHandler(http.MethodPut, "/sessions/:id/skills", "/sessions/999999999/skills", []byte(`{"skills":[]}`), userID, UpdateSessionSkillsHandler(svc))
		assertSkillHandlerError(t, recorder, http.StatusNotFound, "session_not_found", false, false)
	})
}

func serveSkillHandler(method, route, path string, body []byte, userID int64, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("request_id", "req-skill-handler")
		if userID != 0 {
			c.Set("user_id", userID)
		}
		c.Next()
	})
	router.Handle(method, route, handler)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	router.ServeHTTP(recorder, request)
	return recorder
}

func assertSkillHandlerError(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string, retryable, requestID bool) {
	t.Helper()
	body := decodeUploadError(t, recorder)
	if recorder.Code != status || body.Code != code || body.Retryable != retryable {
		t.Fatalf("response=%d %+v", recorder.Code, body)
	}
	if requestID && body.RequestID != "req-skill-handler" {
		t.Fatalf("missing request ID: %+v", body)
	}
	if !requestID && body.RequestID != "" {
		t.Fatalf("unexpected request ID: %+v", body)
	}
	for _, secret := range []string{"postgres://", "fixture-hash", "/srv/private"} {
		if strings.Contains(recorder.Body.String(), secret) {
			t.Fatalf("public response leaked %q: %s", secret, recorder.Body.String())
		}
	}
}
