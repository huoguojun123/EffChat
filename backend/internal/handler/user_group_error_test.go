package handler

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
)

func TestWriteUserGroupErrorContract(t *testing.T) {
	internal := errors.New("postgres://fixture:secret@db.example/effchat /srv/private/groups")
	tests := []struct {
		name      string
		err       error
		status    int
		code      string
		retryable bool
		requestID bool
	}{
		{name: "invalid", err: fmt.Errorf("%w: level must be >= 0", service.ErrUserGroupInvalid), status: http.StatusBadRequest, code: "user_group_invalid"},
		{name: "missing", err: repository.ErrNotFound, status: http.StatusNotFound, code: "user_group_not_found"},
		{name: "conflict", err: repository.ErrUserGroupConflict, status: http.StatusConflict, code: "user_group_conflict"},
		{name: "default invariant", err: repository.ErrDefaultUserGroupRequired, status: http.StatusConflict, code: "user_group_default_required"},
		{name: "internal", err: internal, status: http.StatusInternalServerError, code: "user_group_update_failed", retryable: true, requestID: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPatch, "/api/v1/admin/groups/7", nil)
			ctx.Set("request_id", "req-user-group")

			writeUserGroupError(ctx, "update", tt.err)
			body := decodeUploadError(t, recorder)
			if recorder.Code != tt.status || body.Code != tt.code || body.Retryable != tt.retryable {
				t.Fatalf("response=%d %+v", recorder.Code, body)
			}
			if tt.requestID && body.RequestID != "req-user-group" {
				t.Fatalf("missing request ID: %+v", body)
			}
			if !tt.requestID && body.RequestID != "" {
				t.Fatalf("unexpected request ID: %+v", body)
			}
			for _, secret := range []string{"postgres://", "fixture:secret", "/srv/private/groups"} {
				if strings.Contains(recorder.Body.String(), secret) {
					t.Fatalf("public response leaked %q: %s", secret, recorder.Body.String())
				}
			}
		})
	}
}

func TestUserGroupHandlersClassifyFailures(t *testing.T) {
	t.Run("invalid id", func(t *testing.T) {
		repo := repository.NewUserGroupRepository(nil)
		svc := service.NewUserGroupService(repo)
		recorder := serveUserGroupHandler(http.MethodPatch, "/groups/:id", "/groups/0", []byte(`{}`), UpdateUserGroupHandler(svc))
		assertUserGroupError(t, recorder, http.StatusBadRequest, "user_group_id_invalid", false, false)
	})

	t.Run("overlong description", func(t *testing.T) {
		repo := repository.NewUserGroupRepository(nil)
		svc := service.NewUserGroupService(repo)
		payload := fmt.Appendf(nil, `{"name":"fixture","description":%q}`, strings.Repeat("界", 201))
		recorder := serveUserGroupHandler(http.MethodPost, "/groups", "/groups", payload, CreateUserGroupHandler(svc))
		assertUserGroupError(t, recorder, http.StatusBadRequest, "user_group_invalid", false, false)
	})

	t.Run("duplicate name", func(t *testing.T) {
		db := setupHandlerTestDB(t)
		repo := repository.NewUserGroupRepository(db)
		svc := service.NewUserGroupService(repo)
		name := fmt.Sprintf("contract_group_%d", time.Now().UnixNano())
		if _, err := svc.Create(&service.CreateGroupRequest{Name: name}); err != nil {
			t.Fatalf("seed user group: %v", err)
		}
		t.Cleanup(func() { _, _ = db.Exec("DELETE FROM user_groups WHERE name = $1", name) })
		recorder := serveUserGroupHandler(http.MethodPost, "/groups", "/groups", fmt.Appendf(nil, `{"name":%q}`, name), CreateUserGroupHandler(svc))
		assertUserGroupError(t, recorder, http.StatusConflict, "user_group_conflict", false, false)
	})

	t.Run("missing group", func(t *testing.T) {
		db := setupHandlerTestDB(t)
		repo := repository.NewUserGroupRepository(db)
		svc := service.NewUserGroupService(repo)
		recorder := serveUserGroupHandler(http.MethodPatch, "/groups/:id", "/groups/999999999", []byte(`{}`), UpdateUserGroupHandler(svc))
		assertUserGroupError(t, recorder, http.StatusNotFound, "user_group_not_found", false, false)
	})

	t.Run("closed repository", func(t *testing.T) {
		db := setupHandlerTestDB(t)
		repo := repository.NewUserGroupRepository(db)
		svc := service.NewUserGroupService(repo)
		if err := db.Close(); err != nil {
			t.Fatalf("close user group database: %v", err)
		}
		recorder := serveUserGroupHandler(http.MethodGet, "/groups", "/groups", nil, ListUserGroupsHandler(svc))
		assertUserGroupError(t, recorder, http.StatusInternalServerError, "user_group_list_failed", true, true)
	})
}

func serveUserGroupHandler(method, route, path string, body []byte, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("request_id", "req-user-group-handler")
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

func assertUserGroupError(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string, retryable, requestID bool) {
	t.Helper()
	body := decodeUploadError(t, recorder)
	if recorder.Code != status || body.Code != code || body.Retryable != retryable {
		t.Fatalf("response=%d %+v", recorder.Code, body)
	}
	if requestID && body.RequestID != "req-user-group-handler" {
		t.Fatalf("missing request ID: %+v", body)
	}
	if !requestID && body.RequestID != "" {
		t.Fatalf("unexpected request ID: %+v", body)
	}
}
