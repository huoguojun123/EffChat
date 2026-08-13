package handler

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/repository"
)

func TestWritePromptGroupErrorContract(t *testing.T) {
	internal := errors.New("postgres://fixture:secret@db.example/effchat /srv/private/prompts")
	tests := []struct {
		name      string
		err       error
		status    int
		code      string
		retryable bool
		requestID bool
	}{
		{name: "invalid", err: fmt.Errorf("%w: group name is required", repository.ErrPromptGroupInvalid), status: http.StatusBadRequest, code: "prompt_group_invalid"},
		{name: "missing", err: repository.ErrNotFound, status: http.StatusNotFound, code: "prompt_group_not_found"},
		{name: "conflict", err: repository.ErrPromptGroupConflict, status: http.StatusConflict, code: "prompt_group_conflict"},
		{name: "internal", err: internal, status: http.StatusInternalServerError, code: "prompt_group_update_failed", retryable: true, requestID: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPatch, "/api/v1/prompt-groups/7", nil)
			ctx.Set("request_id", "req-prompt-group")

			writePromptGroupError(ctx, "update", tt.err)
			body := decodeUploadError(t, recorder)
			if recorder.Code != tt.status || body.Code != tt.code || body.Retryable != tt.retryable {
				t.Fatalf("response=%d %+v", recorder.Code, body)
			}
			if tt.requestID && body.RequestID != "req-prompt-group" {
				t.Fatalf("missing request ID: %+v", body)
			}
			if !tt.requestID && body.RequestID != "" {
				t.Fatalf("unexpected request ID: %+v", body)
			}
			for _, secret := range []string{"postgres://", "fixture:secret", "/srv/private/prompts"} {
				if strings.Contains(recorder.Body.String(), secret) {
					t.Fatalf("public response leaked %q: %s", secret, recorder.Body.String())
				}
			}
		})
	}
}

func TestPromptGroupHandlersClassifyFailures(t *testing.T) {
	t.Run("invalid name", func(t *testing.T) {
		repo := repository.NewPromptGroupRepository(nil)
		recorder := servePromptGroupHandler(http.MethodPost, "/prompt-groups", "/prompt-groups", []byte(`{"name":"   "}`), 82001, CreatePromptGroupHandler(repo))
		assertPromptGroupError(t, recorder, http.StatusBadRequest, "prompt_group_invalid", false, false)
	})

	t.Run("missing group", func(t *testing.T) {
		db := setupHandlerTestDB(t)
		repo := repository.NewPromptGroupRepository(db)
		recorder := servePromptGroupHandler(http.MethodPatch, "/prompt-groups/:id", "/prompt-groups/999999999", []byte(`{"name":"fixture"}`), 82002, UpdatePromptGroupHandler(repo))
		assertPromptGroupError(t, recorder, http.StatusNotFound, "prompt_group_not_found", false, false)
	})

	t.Run("duplicate name", func(t *testing.T) {
		db := setupHandlerTestDB(t)
		const userID = int64(82003)
		seedPromptGroupUser(t, db, userID)
		repo := repository.NewPromptGroupRepository(db)
		if _, err := repo.Create(userID, "Existing"); err != nil {
			t.Fatalf("seed prompt group: %v", err)
		}
		recorder := servePromptGroupHandler(http.MethodPost, "/prompt-groups", "/prompt-groups", []byte(`{"name":"existing"}`), userID, CreatePromptGroupHandler(repo))
		assertPromptGroupError(t, recorder, http.StatusConflict, "prompt_group_conflict", false, false)
	})

	t.Run("closed repository", func(t *testing.T) {
		db := setupHandlerTestDB(t)
		repo := repository.NewPromptGroupRepository(db)
		if err := db.Close(); err != nil {
			t.Fatalf("close prompt group database: %v", err)
		}
		recorder := servePromptGroupHandler(http.MethodGet, "/prompt-groups", "/prompt-groups", nil, 82004, ListPromptGroupsHandler(repo))
		assertPromptGroupError(t, recorder, http.StatusInternalServerError, "prompt_group_list_failed", true, true)
	})
}

func servePromptGroupHandler(method, route, path string, body []byte, userID int64, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", userID)
		c.Set("request_id", "req-prompt-group-handler")
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

func assertPromptGroupError(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string, retryable, requestID bool) {
	t.Helper()
	body := decodeUploadError(t, recorder)
	if recorder.Code != status || body.Code != code || body.Retryable != retryable {
		t.Fatalf("response=%d %+v", recorder.Code, body)
	}
	if requestID && body.RequestID != "req-prompt-group-handler" {
		t.Fatalf("missing request ID: %+v", body)
	}
	if !requestID && body.RequestID != "" {
		t.Fatalf("unexpected request ID: %+v", body)
	}
}

func seedPromptGroupUser(t *testing.T, db *sql.DB, userID int64) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO users (id, username, password_hash, role, is_active, permissions, preferences)
		 VALUES ($1, $2, 'fixture-hash', 'user', true, '{}', '{}')`,
		userID,
		fmt.Sprintf("prompt_group_contract_%d", userID),
	); err != nil {
		t.Fatalf("seed prompt group user: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM users WHERE id = $1", userID) })
}
