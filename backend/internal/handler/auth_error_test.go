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
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
	"github.com/huoguojun123/EffChat/pkg/config"
)

type authErrorResponse struct {
	Code      string `json:"code"`
	Error     string `json:"error"`
	Retryable bool   `json:"retryable"`
	RequestID string `json:"request_id"`
}

func TestWriteAuthErrorContract(t *testing.T) {
	internal := errors.New("postgres://fixture:secret@db.example/effchat /srv/private/auth")
	tests := []struct {
		name      string
		operation string
		err       error
		status    int
		code      string
		retryable bool
		requestID bool
	}{
		{name: "invalid registration", operation: "register", err: fmt.Errorf("%w: invalid email", service.ErrUserRegistrationInvalid), status: http.StatusBadRequest, code: "registration_invalid"},
		{name: "identity conflict", operation: "register", err: repository.ErrUserConflict, status: http.StatusConflict, code: "user_identity_conflict"},
		{name: "inactive account", operation: "login", err: service.ErrAccountInactive, status: http.StatusUnauthorized, code: "account_inactive"},
		{name: "invalid credentials", operation: "login", err: service.ErrInvalidCredentials, status: http.StatusUnauthorized, code: "invalid_credentials"},
		{name: "internal", operation: "login", err: internal, status: http.StatusInternalServerError, code: "authentication_login_failed", retryable: true, requestID: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/"+tt.operation, nil)
			ctx.Set("request_id", "req-auth-contract")

			writeAuthError(ctx, tt.operation, tt.err)
			body := decodeAuthError(t, recorder)
			if recorder.Code != tt.status || body.Code != tt.code || body.Retryable != tt.retryable {
				t.Fatalf("response=%d %+v", recorder.Code, body)
			}
			if tt.requestID && body.RequestID != "req-auth-contract" {
				t.Fatalf("missing request ID: %+v", body)
			}
			if !tt.requestID && body.RequestID != "" {
				t.Fatalf("unexpected request ID: %+v", body)
			}
			for _, secret := range []string{"postgres://", "fixture:secret", "/srv/private/auth"} {
				if strings.Contains(recorder.Body.String(), secret) {
					t.Fatalf("public response leaked %q: %s", secret, recorder.Body.String())
				}
			}
		})
	}
}

func TestRegisterHandlerValidatesBeforeRepositoryAccess(t *testing.T) {
	svc := service.NewAuthService(repository.NewUserRepository(nil), "fixture-secret")
	tests := []struct {
		name string
		body []byte
	}{
		{name: "overlong nickname", body: fmtJSON(map[string]any{"username": "fixture-user", "password": "fixture-pass", "nickname": strings.Repeat("界", 101)})},
		{name: "overlong email", body: fmtJSON(map[string]any{"username": "fixture-user", "password": "fixture-pass", "email": strings.Repeat("a", 244) + "@example.test"})},
		{name: "overlong password", body: fmtJSON(map[string]any{"username": "fixture-user", "password": strings.Repeat("p", 73)})},
		{name: "invalid preferences", body: []byte(`{"username":"fixture-user","password":"fixture-pass","preferences":[]}`)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := serveAuthHandler(http.MethodPost, "/register", tt.body, RegisterHandler(svc))
			assertAuthError(t, recorder, http.StatusBadRequest, "registration_invalid", false, false)
		})
	}
}

func TestAuthHandlersClassifyRepositoryFailures(t *testing.T) {
	db := setupHandlerTestDB(t)
	repo := repository.NewUserRepository(db)
	authService := service.NewAuthService(repo, "fixture-secret")

	firstEmail := "auth-first@example.test"
	first, err := authService.Register(&service.RegisterRequest{Username: "auth_fixture_a", Password: "fixture-pass", Email: firstEmail})
	if err != nil || first.User == nil {
		t.Fatalf("seed first registration: response=%+v err=%v", first, err)
	}
	if _, err := authService.Register(&service.RegisterRequest{Username: "auth_fixture_pending", Password: "fixture-pass"}); err != nil {
		t.Fatalf("seed inactive registration: %v", err)
	}

	t.Run("duplicate username", func(t *testing.T) {
		body := []byte(`{"username":"auth_fixture_a","password":"fixture-pass"}`)
		recorder := serveAuthHandler(http.MethodPost, "/register", body, RegisterHandler(authService))
		assertAuthError(t, recorder, http.StatusConflict, "user_identity_conflict", false, false)
	})

	t.Run("duplicate email constraint", func(t *testing.T) {
		body := fmtJSON(map[string]string{"username": "auth_fixture_b", "password": "fixture-pass", "email": firstEmail})
		recorder := serveAuthHandler(http.MethodPost, "/register", body, RegisterHandler(authService))
		assertAuthError(t, recorder, http.StatusConflict, "user_identity_conflict", false, false)
	})

	t.Run("unknown credentials", func(t *testing.T) {
		body := []byte(`{"username":"missing-user","password":"fixture-pass"}`)
		recorder := serveAuthHandler(http.MethodPost, "/login", body, LoginHandler(authService))
		assertAuthError(t, recorder, http.StatusUnauthorized, "invalid_credentials", false, false)
	})

	t.Run("incorrect password", func(t *testing.T) {
		body := []byte(`{"username":"auth_fixture_a","password":"wrong-pass"}`)
		recorder := serveAuthHandler(http.MethodPost, "/login", body, LoginHandler(authService))
		assertAuthError(t, recorder, http.StatusUnauthorized, "invalid_credentials", false, false)
	})

	t.Run("inactive account", func(t *testing.T) {
		body := []byte(`{"username":"auth_fixture_pending","password":"fixture-pass"}`)
		recorder := serveAuthHandler(http.MethodPost, "/login", body, LoginHandler(authService))
		assertAuthError(t, recorder, http.StatusUnauthorized, "account_inactive", false, false)
	})

	if err := db.Close(); err != nil {
		t.Fatalf("close auth database: %v", err)
	}
	t.Run("closed registration repository", func(t *testing.T) {
		body := []byte(`{"username":"auth_fixture_closed","password":"fixture-pass"}`)
		recorder := serveAuthHandler(http.MethodPost, "/register", body, RegisterHandler(authService))
		assertAuthError(t, recorder, http.StatusInternalServerError, "authentication_register_failed", true, true)
	})
	t.Run("closed login repository", func(t *testing.T) {
		body := []byte(`{"username":"auth_fixture_a","password":"fixture-pass"}`)
		recorder := serveAuthHandler(http.MethodPost, "/login", body, LoginHandler(authService))
		assertAuthError(t, recorder, http.StatusInternalServerError, "authentication_login_failed", true, true)
	})
}

func TestAuthHandlerRateLimitContract(t *testing.T) {
	limiter := NewAuthRateLimiter(config.AuthRateLimitConfig{MaxAttempts: 1, Window: time.Minute, Block: time.Minute})
	limiter.RecordFailure("203.0.113.9", "fixture-user")

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("client_ip", "203.0.113.9")
		c.Next()
	})
	router.POST("/login", LoginHandler(nil, limiter))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/login", bytes.NewReader([]byte(`{"username":"fixture-user","password":"fixture-pass"}`)))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	assertAuthError(t, recorder, http.StatusTooManyRequests, "authentication_rate_limited", true, false)
	if recorder.Header().Get("Retry-After") == "" {
		t.Fatal("missing Retry-After header")
	}
}

func serveAuthHandler(method, path string, body []byte, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("request_id", "req-auth-handler")
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

func assertAuthError(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string, retryable, requestID bool) {
	t.Helper()
	body := decodeAuthError(t, recorder)
	if recorder.Code != status || body.Code != code || body.Retryable != retryable {
		t.Fatalf("response=%d %+v", recorder.Code, body)
	}
	if requestID && body.RequestID != "req-auth-handler" {
		t.Fatalf("missing request ID: %+v", body)
	}
	if !requestID && body.RequestID != "" {
		t.Fatalf("unexpected request ID: %+v", body)
	}
}

func decodeAuthError(t *testing.T, recorder *httptest.ResponseRecorder) authErrorResponse {
	t.Helper()
	var body authErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v body=%s", err, recorder.Body.String())
	}
	return body
}
