package middleware

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/service"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newTestAuthService(secret string) *service.AuthService {
	return service.NewAuthService(nil, secret)
}

func makeToken(secret string, claims jwt.MapClaims) string {
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, _ := tok.SignedString([]byte(secret))
	return s
}

func runMiddleware(token string) *httptest.ResponseRecorder {
	authHeader := ""
	if token != "" {
		authHeader = "Bearer " + token
	}
	return runMiddlewareWithHeaderAndResolver(authHeader, func(userID int64, authVersion int) (*model.User, error) {
		return &model.User{ID: userID, Username: "alice", Role: "user", IsActive: true, AuthVersion: authVersion}, nil
	})
}

func runMiddlewareWithResolver(token string, resolver authStateResolver) *httptest.ResponseRecorder {
	return runMiddlewareWithHeaderAndResolver("Bearer "+token, resolver)
}

func runMiddlewareWithHeaderAndResolver(authHeader string, resolver authStateResolver) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, r := gin.CreateTestContext(w)

	authSvc := newTestAuthService("testsecret")
	r.GET("/", func(c *gin.Context) {
		c.Set("request_id", "req-auth")
		c.Next()
	}, authMiddleware(authSvc, resolver), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	c.Request = req
	r.ServeHTTP(w, req)
	return w
}

func TestAuthMiddleware_MissingHeader(t *testing.T) {
	w := runMiddleware("")
	assertMiddlewareError(t, w, http.StatusUnauthorized, "auth_header_missing", false, false)
}

func TestAuthMiddleware_InvalidFormat(t *testing.T) {
	w := runMiddlewareWithHeaderAndResolver("notbearer", func(int64, int) (*model.User, error) {
		t.Fatal("resolver must not run for an invalid Authorization header")
		return nil, nil
	})
	assertMiddlewareError(t, w, http.StatusUnauthorized, "auth_header_invalid", false, false)
}

func TestAuthMiddleware_WrongSecret(t *testing.T) {
	token := makeToken("wrongsecret", jwt.MapClaims{
		"user_id":  float64(1),
		"username": "alice",
		"role":     "user",
		"exp":      time.Now().Add(time.Hour).Unix(),
	})
	w := runMiddleware(token)
	assertMiddlewareError(t, w, http.StatusUnauthorized, "auth_token_invalid", false, false)
}

func TestAuthMiddleware_Expired(t *testing.T) {
	token := makeToken("testsecret", jwt.MapClaims{
		"user_id":  float64(1),
		"username": "alice",
		"role":     "user",
		"exp":      time.Now().Add(-time.Hour).Unix(),
	})
	w := runMiddleware(token)
	assertMiddlewareError(t, w, http.StatusUnauthorized, "auth_token_invalid", false, false)
}

func TestAuthMiddleware_MissingClaims(t *testing.T) {
	// token missing user_id field
	token := makeToken("testsecret", jwt.MapClaims{
		"username": "alice",
		"role":     "user",
		"exp":      time.Now().Add(time.Hour).Unix(),
	})
	w := runMiddleware(token)
	assertMiddlewareError(t, w, http.StatusUnauthorized, "auth_claims_invalid", false, false)
}

func TestAuthMiddleware_WrongClaimType(t *testing.T) {
	// user_id as string instead of number — triggers ok2 failure path
	token := makeToken("testsecret", jwt.MapClaims{
		"user_id":  "not-a-number",
		"username": "alice",
		"role":     "user",
		"exp":      time.Now().Add(time.Hour).Unix(),
	})
	w := runMiddleware(token)
	assertMiddlewareError(t, w, http.StatusUnauthorized, "auth_claims_invalid", false, false)
}

func TestAuthMiddleware_ValidToken(t *testing.T) {
	token := makeToken("testsecret", jwt.MapClaims{
		"user_id":      float64(42),
		"iat":          time.Now().Unix(),
		"auth_version": float64(1),
		"exp":          time.Now().Add(time.Hour).Unix(),
	})
	w := runMiddleware(token)
	if w.Code != http.StatusOK {
		t.Errorf("want 200 for valid token, got %d", w.Code)
	}
}

func TestAuthMiddleware_AcceptsLegacyTokenWithoutAuthVersion(t *testing.T) {
	token := makeToken("testsecret", jwt.MapClaims{
		"user_id": float64(42),
		"iat":     time.Now().Unix(),
		"exp":     time.Now().Add(time.Hour).Unix(),
	})
	w := runMiddlewareWithResolver(token, func(userID int64, authVersion int) (*model.User, error) {
		if userID != 42 || authVersion != 1 {
			t.Fatalf("resolver received %d/%d, want 42/1", userID, authVersion)
		}
		return &model.User{ID: userID, Username: "alice", Role: "user", IsActive: true, AuthVersion: authVersion}, nil
	})
	if w.Code != http.StatusOK {
		t.Fatalf("want 200 for a legacy token, got %d", w.Code)
	}
}

func TestAuthMiddleware_UsesCurrentAccountStateInsteadOfTokenProfile(t *testing.T) {
	token := makeToken("testsecret", jwt.MapClaims{
		"user_id":      float64(42),
		"username":     "stale-admin-name",
		"role":         "admin",
		"iat":          time.Now().Unix(),
		"auth_version": float64(1),
		"exp":          time.Now().Add(time.Hour).Unix(),
	})
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	authSvc := newTestAuthService("testsecret")
	r.GET("/", authMiddleware(authSvc, func(userID int64, authVersion int) (*model.User, error) {
		if userID != 42 || authVersion != 1 {
			t.Fatalf("resolver received %d/%d, want 42/1", userID, authVersion)
		}
		return &model.User{ID: 42, Username: "current-user", Role: "user", IsActive: true, AuthVersion: 1}, nil
	}), func(c *gin.Context) {
		if GetUsername(c) != "current-user" || GetRole(c) != "user" || GetAuthVersion(c) != 1 {
			t.Fatalf("context profile = %q/%q/%d, want current-user/user/1", GetUsername(c), GetRole(c), GetAuthVersion(c))
		}
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

func TestAuthMiddleware_RejectsUnavailableCurrentAccount(t *testing.T) {
	token := makeToken("testsecret", jwt.MapClaims{
		"user_id":      float64(42),
		"iat":          time.Now().Unix(),
		"auth_version": float64(1),
		"exp":          time.Now().Add(time.Hour).Unix(),
	})
	w := runMiddlewareWithResolver(token, func(int64, int) (*model.User, error) {
		return nil, service.ErrAuthenticationUnavailable
	})
	assertMiddlewareError(t, w, http.StatusUnauthorized, "auth_session_invalid", false, false)
}

func TestAuthMiddleware_ReturnsServerErrorWhenAccountLookupFails(t *testing.T) {
	token := makeToken("testsecret", jwt.MapClaims{
		"user_id":      float64(42),
		"iat":          time.Now().Unix(),
		"auth_version": float64(1),
		"exp":          time.Now().Add(time.Hour).Unix(),
	})
	w := runMiddlewareWithResolver(token, func(int64, int) (*model.User, error) {
		return nil, errors.New("postgres://fixture:secret@db.example/effchat /srv/private/auth")
	})
	assertMiddlewareError(t, w, http.StatusInternalServerError, "auth_state_load_failed", true, true)
	if strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "/srv/private") {
		t.Fatalf("response leaked internal cause: %s", w.Body.String())
	}
}

func TestAuthMiddleware_PreservesLargeJSONNumberUserID(t *testing.T) {
	const wantID int64 = 9007199254740993
	token := makeToken("testsecret", jwt.MapClaims{
		"user_id":  json.Number("9007199254740993"),
		"username": "alice",
		"role":     "user",
		"exp":      time.Now().Add(time.Hour).Unix(),
	})
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	authSvc := newTestAuthService("testsecret")
	var gotID int64
	r.GET("/", authMiddleware(authSvc, func(userID int64, authVersion int) (*model.User, error) {
		return &model.User{ID: userID, Username: "alice", Role: "user", IsActive: true, AuthVersion: authVersion}, nil
	}), func(c *gin.Context) {
		gotID = GetUserID(c)
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200 for valid token, got %d", w.Code)
	}
	if gotID != wantID {
		t.Fatalf("user_id = %d, want %d", gotID, wantID)
	}
}

func TestGetUserID_Missing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	if id := GetUserID(c); id != 0 {
		t.Errorf("want 0 for missing user_id, got %d", id)
	}
}

func TestAdminMiddleware_Forbidden(t *testing.T) {
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.GET("/admin", func(c *gin.Context) {
		c.Set("role", "user")
		c.Next()
	}, AdminMiddleware(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	r.ServeHTTP(w, req)
	assertMiddlewareError(t, w, http.StatusForbidden, "admin_access_required", false, false)
}

func assertMiddlewareError(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string, retryable, requestID bool) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status = %d body=%s, want %d", recorder.Code, recorder.Body.String(), status)
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["code"] != code || body["retryable"] != retryable {
		t.Fatalf("response = %#v", body)
	}
	if requestID && body["request_id"] == "" {
		t.Fatalf("request_id = %#v", body["request_id"])
	}
	if !requestID {
		if _, ok := body["request_id"]; ok {
			t.Fatalf("unexpected request_id: %#v", body)
		}
	}
}
