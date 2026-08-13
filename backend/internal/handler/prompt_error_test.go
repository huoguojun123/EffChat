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
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
)

type promptErrorResponse struct {
	Code      string `json:"code"`
	Error     string `json:"error"`
	Retryable bool   `json:"retryable"`
	RequestID string `json:"request_id"`
}

func TestWritePromptErrorContract(t *testing.T) {
	internal := errors.New("postgres://fixture:secret@db.example/effchat /srv/private/prompts")
	tests := []struct {
		name      string
		operation string
		err       error
		status    int
		code      string
		retryable bool
		requestID bool
	}{
		{name: "missing", operation: "update", err: repository.ErrNotFound, status: http.StatusNotFound, code: "prompt_not_found"},
		{name: "internal", operation: "update", err: internal, status: http.StatusInternalServerError, code: "prompt_update_failed", retryable: true, requestID: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPatch, "/api/v1/prompts/7", nil)
			ctx.Set("request_id", "req-prompt-contract")

			writePromptError(ctx, tt.operation, tt.err)
			body := decodePromptError(t, recorder)
			if recorder.Code != tt.status || body.Code != tt.code || body.Retryable != tt.retryable {
				t.Fatalf("response=%d %+v", recorder.Code, body)
			}
			if tt.requestID && body.RequestID != "req-prompt-contract" {
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

func TestPromptHandlersValidateBeforeRepositoryAccess(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		route   string
		path    string
		body    []byte
		handler gin.HandlerFunc
		code    string
	}{
		{name: "invalid pagination", method: http.MethodGet, route: "/prompts", path: "/prompts?limit=all", handler: ListPromptsHandler(nil), code: "prompt_pagination_invalid"},
		{name: "invalid id", method: http.MethodGet, route: "/prompts/:id", path: "/prompts/0", handler: GetPromptHandler(nil), code: "prompt_id_invalid"},
		{name: "blank title", method: http.MethodPost, route: "/prompts", path: "/prompts", body: []byte(`{"title":"   ","content":"fixture"}`), handler: CreatePromptHandler(nil), code: "prompt_invalid"},
		{name: "blank content", method: http.MethodPost, route: "/prompts", path: "/prompts", body: []byte(`{"title":"fixture","content":"   "}`), handler: CreatePromptHandler(nil), code: "prompt_invalid"},
		{name: "overlong title", method: http.MethodPost, route: "/prompts", path: "/prompts", body: fmtJSON(map[string]any{"title": strings.Repeat("题", 201), "content": "fixture"}), handler: CreatePromptHandler(nil), code: "prompt_invalid"},
		{name: "overlong group", method: http.MethodPost, route: "/prompts", path: "/prompts", body: fmtJSON(map[string]any{"title": "fixture", "content": "fixture", "group_name": strings.Repeat("组", 101)}), handler: CreatePromptHandler(nil), code: "prompt_invalid"},
		{name: "invalid group id", method: http.MethodPost, route: "/prompts", path: "/prompts", body: []byte(`{"title":"fixture","content":"fixture","group_id":0}`), handler: CreatePromptHandler(nil), code: "prompt_invalid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := servePromptContractHandler(tt.method, tt.route, tt.path, tt.body, 7, tt.handler)
			assertPromptError(t, recorder, http.StatusBadRequest, tt.code, false, false)
		})
	}
}

func TestPromptHandlersClassifyRepositoryAndOwnershipFailures(t *testing.T) {
	db := setupHandlerTestDB(t)
	userRepo := repository.NewUserRepository(db)
	owner := &model.User{Username: "prompt_contract_owner", PasswordHash: "fixture-hash", Role: "user", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`)}
	admin := &model.User{Username: "prompt_contract_admin", PasswordHash: "fixture-hash", Role: "admin", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`)}
	for _, user := range []*model.User{owner, admin} {
		if err := userRepo.Create(user); err != nil {
			t.Fatalf("create user: %v", err)
		}
	}
	promptRepo := repository.NewPromptRepository(db)
	private := &model.Prompt{UserID: owner.ID, Title: "Private fixture", Content: "private fixture", GroupName: "Fixture", Tags: []string{}, IsPublic: false}
	shared := &model.Prompt{UserID: admin.ID, Title: "Shared fixture", Content: "shared fixture", GroupName: "默认分组", Tags: []string{}, IsPublic: true}
	for _, prompt := range []*model.Prompt{private, shared} {
		if err := promptRepo.Create(prompt); err != nil {
			t.Fatalf("create prompt: %v", err)
		}
	}

	t.Run("missing group", func(t *testing.T) {
		body := []byte(`{"title":"fixture","content":"fixture","group_id":999999999}`)
		recorder := servePromptContractHandler(http.MethodPost, "/prompts", "/prompts", body, owner.ID, CreatePromptHandler(promptRepo))
		assertPromptError(t, recorder, http.StatusNotFound, "prompt_not_found", false, false)
	})

	t.Run("missing prompt", func(t *testing.T) {
		recorder := servePromptContractHandler(http.MethodDelete, "/prompts/:id", "/prompts/999999999", nil, owner.ID, DeletePromptHandler(promptRepo))
		assertPromptError(t, recorder, http.StatusNotFound, "prompt_not_found", false, false)
	})

	t.Run("shared prompt update is read only", func(t *testing.T) {
		body := []byte(`{"title":"mutated"}`)
		recorder := servePromptContractHandler(http.MethodPatch, "/prompts/:id", fmt.Sprintf("/prompts/%d", shared.ID), body, owner.ID, UpdatePromptHandler(promptRepo))
		assertPromptError(t, recorder, http.StatusForbidden, "prompt_read_only", false, false)
	})

	t.Run("shared prompt delete is read only", func(t *testing.T) {
		recorder := servePromptContractHandler(http.MethodDelete, "/prompts/:id", fmt.Sprintf("/prompts/%d", shared.ID), nil, owner.ID, DeletePromptHandler(promptRepo))
		assertPromptError(t, recorder, http.StatusForbidden, "prompt_read_only", false, false)
	})

	t.Run("invalid update after lookup", func(t *testing.T) {
		body := []byte(`{"title":"   "}`)
		recorder := servePromptContractHandler(http.MethodPatch, "/prompts/:id", fmt.Sprintf("/prompts/%d", private.ID), body, owner.ID, UpdatePromptHandler(promptRepo))
		assertPromptError(t, recorder, http.StatusBadRequest, "prompt_invalid", false, false)
	})

	if err := db.Close(); err != nil {
		t.Fatalf("close prompt database: %v", err)
	}
	t.Run("closed repository", func(t *testing.T) {
		recorder := servePromptContractHandler(http.MethodGet, "/prompts", "/prompts", nil, owner.ID, ListPromptsHandler(promptRepo))
		assertPromptError(t, recorder, http.StatusInternalServerError, "prompt_list_failed", true, true)
	})
}

func servePromptContractHandler(method, route, path string, body []byte, userID int64, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", userID)
		c.Set("request_id", "req-prompt-handler")
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

func assertPromptError(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string, retryable, requestID bool) {
	t.Helper()
	body := decodePromptError(t, recorder)
	if recorder.Code != status || body.Code != code || body.Retryable != retryable {
		t.Fatalf("response=%d %+v", recorder.Code, body)
	}
	if requestID && body.RequestID != "req-prompt-handler" {
		t.Fatalf("missing request ID: %+v", body)
	}
	if !requestID && body.RequestID != "" {
		t.Fatalf("unexpected request ID: %+v", body)
	}
}

func decodePromptError(t *testing.T, recorder *httptest.ResponseRecorder) promptErrorResponse {
	t.Helper()
	var body promptErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v body=%s", err, recorder.Body.String())
	}
	return body
}
