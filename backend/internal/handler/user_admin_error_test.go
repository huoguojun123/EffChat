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

func TestWriteAdminUserErrorContract(t *testing.T) {
	internal := errors.New("postgres://fixture:secret@db.example/effchat /srv/private/users")
	tests := []struct {
		name      string
		err       error
		status    int
		code      string
		retryable bool
		requestID bool
	}{
		{name: "invalid", err: fmt.Errorf("%w: invalid email", service.ErrUserAdminInvalid), status: http.StatusBadRequest, code: "admin_user_invalid"},
		{name: "missing user", err: repository.ErrNotFound, status: http.StatusNotFound, code: "admin_user_not_found"},
		{name: "missing group", err: repository.ErrUserGroupMissing, status: http.StatusNotFound, code: "user_group_not_found"},
		{name: "identity conflict", err: repository.ErrUserConflict, status: http.StatusConflict, code: "admin_user_conflict"},
		{name: "last administrator", err: repository.ErrLastActiveAdmin, status: http.StatusConflict, code: "last_active_admin_required"},
		{name: "internal", err: internal, status: http.StatusInternalServerError, code: "admin_user_update_failed", retryable: true, requestID: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPatch, "/api/v1/admin/users/7", nil)
			ctx.Set("request_id", "req-admin-user")

			writeAdminUserError(ctx, "update", tt.err)
			body := decodeUploadError(t, recorder)
			if recorder.Code != tt.status || body.Code != tt.code || body.Retryable != tt.retryable {
				t.Fatalf("response=%d %+v", recorder.Code, body)
			}
			if tt.requestID && body.RequestID != "req-admin-user" {
				t.Fatalf("missing request ID: %+v", body)
			}
			if !tt.requestID && body.RequestID != "" {
				t.Fatalf("unexpected request ID: %+v", body)
			}
			for _, secret := range []string{"postgres://", "fixture:secret", "/srv/private/users"} {
				if strings.Contains(recorder.Body.String(), secret) {
					t.Fatalf("public response leaked %q: %s", secret, recorder.Body.String())
				}
			}
		})
	}
}

func TestAdminUserHandlersClassifyFailures(t *testing.T) {
	t.Run("invalid pagination", func(t *testing.T) {
		recorder := serveAdminUserHandler(http.MethodGet, "/users", "/users?limit=all", nil, ListUsersHandler(nil))
		assertAdminUserError(t, recorder, http.StatusBadRequest, "admin_user_pagination_invalid", false, false)
	})

	t.Run("invalid id", func(t *testing.T) {
		recorder := serveAdminUserHandler(http.MethodPatch, "/users/:id", "/users/0", []byte(`{}`), UpdateUserHandler(nil))
		assertAdminUserError(t, recorder, http.StatusBadRequest, "admin_user_id_invalid", false, false)
	})

	t.Run("invalid group id", func(t *testing.T) {
		svc := service.NewUserAdminService(repository.NewUserRepository(nil))
		recorder := serveAdminUserHandler(http.MethodPut, "/users/:id/group", "/users/7/group", []byte(`{"group_id":0}`), SetUserGroupHandler(svc))
		assertAdminUserError(t, recorder, http.StatusBadRequest, "admin_user_invalid", false, false)
	})

	t.Run("overlong nickname", func(t *testing.T) {
		svc := service.NewUserAdminService(repository.NewUserRepository(nil))
		payload := fmt.Appendf(nil, `{"username":"fixture-user","password":"fixture-pass","nickname":%q}`, strings.Repeat("界", 101))
		recorder := serveAdminUserHandler(http.MethodPost, "/users", "/users", payload, CreateUserHandler(svc))
		assertAdminUserError(t, recorder, http.StatusBadRequest, "admin_user_invalid", false, false)
	})

	t.Run("overlong password", func(t *testing.T) {
		svc := service.NewUserAdminService(repository.NewUserRepository(nil))
		payload := fmt.Appendf(nil, `{"username":"fixture-user","password":%q}`, strings.Repeat("p", 73))
		recorder := serveAdminUserHandler(http.MethodPost, "/users", "/users", payload, CreateUserHandler(svc))
		assertAdminUserError(t, recorder, http.StatusBadRequest, "admin_user_invalid", false, false)
	})

	t.Run("duplicate username", func(t *testing.T) {
		db := setupHandlerTestDB(t)
		svc := service.NewUserAdminService(repository.NewUserRepository(db))
		username := fmt.Sprintf("contract_user_%d", time.Now().UnixNano())
		request := &service.CreateUserRequest{Username: username, Password: "fixture-pass"}
		if _, err := svc.Create(request); err != nil {
			t.Fatalf("seed administrator-created user: %v", err)
		}
		recorder := serveAdminUserHandler(http.MethodPost, "/users", "/users", fmt.Appendf(nil, `{"username":%q,"password":"fixture-pass"}`, username), CreateUserHandler(svc))
		assertAdminUserError(t, recorder, http.StatusConflict, "admin_user_conflict", false, false)
	})

	t.Run("duplicate email constraint", func(t *testing.T) {
		db := setupHandlerTestDB(t)
		svc := service.NewUserAdminService(repository.NewUserRepository(db))
		suffix := time.Now().UnixNano()
		email := fmt.Sprintf("contract_%d@example.test", suffix)
		if _, err := svc.Create(&service.CreateUserRequest{Username: fmt.Sprintf("contract_email_a_%d", suffix), Password: "fixture-pass", Email: &email}); err != nil {
			t.Fatalf("seed administrator-created email: %v", err)
		}
		payload := fmt.Appendf(nil, `{"username":"contract_email_b_%d","password":"fixture-pass","email":%q}`, suffix, email)
		recorder := serveAdminUserHandler(http.MethodPost, "/users", "/users", payload, CreateUserHandler(svc))
		assertAdminUserError(t, recorder, http.StatusConflict, "admin_user_conflict", false, false)
	})

	t.Run("missing user", func(t *testing.T) {
		db := setupHandlerTestDB(t)
		svc := service.NewUserAdminService(repository.NewUserRepository(db))
		recorder := serveAdminUserHandler(http.MethodPatch, "/users/:id", "/users/999999999", []byte(`{"is_active":false}`), UpdateUserHandler(svc))
		assertAdminUserError(t, recorder, http.StatusNotFound, "admin_user_not_found", false, false)
	})

	t.Run("last active administrator", func(t *testing.T) {
		db := setupHandlerTestDB(t)
		svc := service.NewUserAdminService(repository.NewUserRepository(db))
		active := true
		user, err := svc.Create(&service.CreateUserRequest{Username: fmt.Sprintf("contract_admin_%d", time.Now().UnixNano()), Password: "fixture-pass", Role: "admin", IsActive: &active})
		if err != nil {
			t.Fatalf("seed active administrator: %v", err)
		}
		recorder := serveAdminUserHandler(http.MethodPatch, "/users/:id", fmt.Sprintf("/users/%d", user.ID), []byte(`{"role":"user"}`), UpdateUserHandler(svc))
		assertAdminUserError(t, recorder, http.StatusConflict, "last_active_admin_required", false, false)
	})

	t.Run("missing group", func(t *testing.T) {
		db := setupHandlerTestDB(t)
		svc := service.NewUserAdminService(repository.NewUserRepository(db))
		user, err := svc.Create(&service.CreateUserRequest{Username: fmt.Sprintf("contract_member_%d", time.Now().UnixNano()), Password: "fixture-pass"})
		if err != nil {
			t.Fatalf("seed user: %v", err)
		}
		recorder := serveAdminUserHandler(http.MethodPut, "/users/:id/group", fmt.Sprintf("/users/%d/group", user.ID), []byte(`{"group_id":999999999}`), SetUserGroupHandler(svc))
		assertAdminUserError(t, recorder, http.StatusNotFound, "user_group_not_found", false, false)
	})

	t.Run("closed repository", func(t *testing.T) {
		db := setupHandlerTestDB(t)
		svc := service.NewUserAdminService(repository.NewUserRepository(db))
		if err := db.Close(); err != nil {
			t.Fatalf("close administrator user database: %v", err)
		}
		recorder := serveAdminUserHandler(http.MethodGet, "/users", "/users", nil, ListUsersHandler(svc))
		assertAdminUserError(t, recorder, http.StatusInternalServerError, "admin_user_list_failed", true, true)
	})
}

func serveAdminUserHandler(method, route, path string, body []byte, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("request_id", "req-admin-user-handler")
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

func assertAdminUserError(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string, retryable, requestID bool) {
	t.Helper()
	body := decodeUploadError(t, recorder)
	if recorder.Code != status || body.Code != code || body.Retryable != retryable {
		t.Fatalf("response=%d %+v", recorder.Code, body)
	}
	if requestID && body.RequestID != "req-admin-user-handler" {
		t.Fatalf("missing request ID: %+v", body)
	}
	if !requestID && body.RequestID != "" {
		t.Fatalf("unexpected request ID: %+v", body)
	}
}
