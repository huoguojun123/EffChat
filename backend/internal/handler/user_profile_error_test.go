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
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
)

type userProfileErrorResponse struct {
	Code      string `json:"code"`
	Error     string `json:"error"`
	Retryable bool   `json:"retryable"`
	RequestID string `json:"request_id"`
}

func TestWriteUserProfileErrorContract(t *testing.T) {
	internal := errors.New("postgres://fixture:secret@db.example/effchat /srv/private/profile")
	tests := []struct {
		name      string
		operation string
		err       error
		status    int
		code      string
		retryable bool
		requestID bool
	}{
		{name: "invalid", operation: "update", err: fmt.Errorf("%w: invalid email", service.ErrUserProfileInvalid), status: http.StatusBadRequest, code: "user_profile_invalid"},
		{name: "incorrect old password", operation: "change_password", err: service.ErrIncorrectOldPassword, status: http.StatusBadRequest, code: "old_password_incorrect"},
		{name: "missing", operation: "load", err: repository.ErrNotFound, status: http.StatusNotFound, code: "user_profile_not_found"},
		{name: "email conflict", operation: "update", err: repository.ErrUserConflict, status: http.StatusConflict, code: "user_profile_conflict"},
		{name: "internal", operation: "update", err: internal, status: http.StatusInternalServerError, code: "user_profile_update_failed", retryable: true, requestID: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPatch, "/api/v1/me", nil)
			ctx.Set("request_id", "req-user-profile")

			writeUserProfileError(ctx, tt.operation, tt.err)
			body := decodeUserProfileError(t, recorder)
			if recorder.Code != tt.status || body.Code != tt.code || body.Retryable != tt.retryable {
				t.Fatalf("response=%d %+v", recorder.Code, body)
			}
			if tt.requestID && body.RequestID != "req-user-profile" {
				t.Fatalf("missing request ID: %+v", body)
			}
			if !tt.requestID && body.RequestID != "" {
				t.Fatalf("unexpected request ID: %+v", body)
			}
			for _, secret := range []string{"postgres://", "fixture:secret", "/srv/private/profile"} {
				if strings.Contains(recorder.Body.String(), secret) {
					t.Fatalf("public response leaked %q: %s", secret, recorder.Body.String())
				}
			}
		})
	}
}

func TestUserProfileHandlersValidateBeforeRepositoryAccess(t *testing.T) {
	svc := service.NewAuthService(repository.NewUserRepository(nil), "fixture-secret")
	tests := []struct {
		name    string
		path    string
		body    []byte
		handler gin.HandlerFunc
		code    string
	}{
		{name: "overlong nickname", path: "/me", body: fmtJSON(map[string]string{"nickname": strings.Repeat("界", 101)}), handler: UpdateMeHandler(svc), code: "user_profile_invalid"},
		{name: "invalid email", path: "/me", body: []byte(`{"email":"not-an-email"}`), handler: UpdateMeHandler(svc), code: "invalid_request_body"},
		{name: "overlong email", path: "/me", body: fmtJSON(map[string]string{"email": strings.Repeat("a", 244) + "@example.test"}), handler: UpdateMeHandler(svc), code: "user_profile_invalid"},
		{name: "overlong password", path: "/me/password", body: fmtJSON(map[string]string{"old_password": "fixture-pass", "new_password": strings.Repeat("p", 73)}), handler: ChangePasswordHandler(svc), code: "user_profile_invalid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := serveUserProfileHandler(http.MethodPatch, tt.path, tt.body, 7, tt.handler)
			assertUserProfileError(t, recorder, http.StatusBadRequest, tt.code, false, false)
		})
	}
}

func TestUserProfileHandlersClassifyRepositoryFailures(t *testing.T) {
	db := setupHandlerTestDB(t)
	repo := repository.NewUserRepository(db)
	authService := service.NewAuthService(repo, "fixture-secret")
	adminService := service.NewUserAdminService(repo)

	firstEmail := "first@example.test"
	first, err := adminService.Create(&service.CreateUserRequest{Username: "profile_fixture_a", Password: "fixture-pass", Email: &firstEmail})
	if err != nil {
		t.Fatalf("seed first user: %v", err)
	}
	secondEmail := "second@example.test"
	second, err := adminService.Create(&service.CreateUserRequest{Username: "profile_fixture_b", Password: "fixture-pass", Email: &secondEmail})
	if err != nil {
		t.Fatalf("seed second user: %v", err)
	}

	t.Run("missing profile", func(t *testing.T) {
		recorder := serveUserProfileHandler(http.MethodGet, "/me", nil, 999999999, GetMeHandler(authService))
		assertUserProfileError(t, recorder, http.StatusNotFound, "user_profile_not_found", false, false)
	})

	t.Run("missing password owner", func(t *testing.T) {
		body := []byte(`{"old_password":"fixture-pass","new_password":"updated-pass"}`)
		recorder := serveUserProfileHandler(http.MethodPatch, "/me/password", body, 999999999, ChangePasswordHandler(authService))
		assertUserProfileError(t, recorder, http.StatusNotFound, "user_profile_not_found", false, false)
	})

	t.Run("duplicate email", func(t *testing.T) {
		body := fmtJSON(map[string]string{"email": firstEmail})
		recorder := serveUserProfileHandler(http.MethodPatch, "/me", body, second.ID, UpdateMeHandler(authService))
		assertUserProfileError(t, recorder, http.StatusConflict, "user_profile_conflict", false, false)
	})

	t.Run("incorrect old password", func(t *testing.T) {
		body := []byte(`{"old_password":"wrong-pass","new_password":"updated-pass"}`)
		recorder := serveUserProfileHandler(http.MethodPatch, "/me/password", body, first.ID, ChangePasswordHandler(authService))
		assertUserProfileError(t, recorder, http.StatusBadRequest, "old_password_incorrect", false, false)
	})

	if err := db.Close(); err != nil {
		t.Fatalf("close profile database: %v", err)
	}
	t.Run("closed repository", func(t *testing.T) {
		recorder := serveUserProfileHandler(http.MethodGet, "/me", nil, first.ID, GetMeHandler(authService))
		assertUserProfileError(t, recorder, http.StatusInternalServerError, "user_profile_load_failed", true, true)
	})
}

func serveUserProfileHandler(method, path string, body []byte, userID int64, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", userID)
		c.Set("request_id", "req-user-profile-handler")
		c.Next()
	})
	router.Handle(method, path, handler)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	router.ServeHTTP(recorder, request)
	return recorder
}

func assertUserProfileError(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string, retryable, requestID bool) {
	t.Helper()
	body := decodeUserProfileError(t, recorder)
	if recorder.Code != status || body.Code != code || body.Retryable != retryable {
		t.Fatalf("response=%d %+v", recorder.Code, body)
	}
	if requestID && body.RequestID != "req-user-profile-handler" {
		t.Fatalf("missing request ID: %+v", body)
	}
	if !requestID && body.RequestID != "" {
		t.Fatalf("unexpected request ID: %+v", body)
	}
}

func decodeUserProfileError(t *testing.T, recorder *httptest.ResponseRecorder) userProfileErrorResponse {
	t.Helper()
	var body userProfileErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v body=%s", err, recorder.Body.String())
	}
	return body
}

func fmtJSON(value any) []byte {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return payload
}
