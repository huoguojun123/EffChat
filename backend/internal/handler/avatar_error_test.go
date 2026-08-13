package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
)

func TestAvatarErrorContractHidesInternalDetails(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/users/me/avatar", nil)
	ctx.Set("request_id", "req-avatar-contract")

	writeAvatarProcessError(ctx, errors.New("postgres://fixture:secret@db.example/effchat /srv/private/avatars"))
	body := decodeAvatarError(t, recorder)
	if recorder.Code != http.StatusInternalServerError || body.Code != "avatar_process_failed" || !body.Retryable || body.RequestID != "req-avatar-contract" {
		t.Fatalf("response=%d %+v", recorder.Code, body)
	}
	for _, secret := range []string{"postgres://", "fixture:secret", "/srv/private/avatars"} {
		if bytes.Contains(recorder.Body.Bytes(), []byte(secret)) {
			t.Fatalf("public response leaked %q: %s", secret, recorder.Body.String())
		}
	}
}

func TestAvatarUploadClassifiesAccountFailuresAndCleansNewFile(t *testing.T) {
	tests := []struct {
		name       string
		userID     int64
		buildAuth  func(*testing.T) *service.AuthService
		wantStatus int
		wantCode   string
	}{
		{
			name:   "missing user",
			userID: 999999999,
			buildAuth: func(t *testing.T) *service.AuthService {
				db := setupHandlerTestDB(t)
				return service.NewAuthService(repository.NewUserRepository(db), "fixture-secret")
			},
			wantStatus: http.StatusNotFound,
			wantCode:   "user_not_found",
		},
		{
			name:   "closed repository",
			userID: 7,
			buildAuth: func(t *testing.T) *service.AuthService {
				db := setupHandlerTestDB(t)
				if err := db.Close(); err != nil {
					t.Fatalf("close avatar database: %v", err)
				}
				return service.NewAuthService(repository.NewUserRepository(db), "fixture-secret")
			},
			wantStatus: http.StatusInternalServerError,
			wantCode:   "avatar_update_failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			handler := NewAvatarHandler(tt.buildAuth(t), dir)
			recorder := serveAvatarUpload(t, handler, tt.userID)
			body := decodeAvatarError(t, recorder)
			if recorder.Code != tt.wantStatus || body.Code != tt.wantCode || body.Retryable != (tt.wantStatus >= 500) {
				t.Fatalf("response=%d %+v", recorder.Code, body)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatalf("read avatar directory: %v", err)
			}
			if len(entries) != 0 {
				t.Fatalf("failed upload left managed files: %v", entries)
			}
		})
	}
}

func TestAvatarUploadCleansNewFileWhenProfileUpdateFails(t *testing.T) {
	env := setupTestEnv(t)
	if _, err := env.db.Exec(`
		CREATE OR REPLACE FUNCTION reject_avatar_update() RETURNS trigger AS $$
		BEGIN
			RAISE EXCEPTION 'fixture avatar update failure';
		END;
		$$ LANGUAGE plpgsql;
		CREATE TRIGGER reject_avatar_update
		BEFORE UPDATE ON users
		FOR EACH ROW EXECUTE FUNCTION reject_avatar_update();
	`); err != nil {
		t.Fatalf("install avatar update trigger: %v", err)
	}

	dir := t.TempDir()
	handler := NewAvatarHandler(env.authService, dir)
	recorder := serveAvatarUpload(t, handler, env.userID)
	body := decodeAvatarError(t, recorder)
	if recorder.Code != http.StatusInternalServerError || body.Code != "avatar_update_failed" || !body.Retryable || body.RequestID != "req-avatar-handler" {
		t.Fatalf("response=%d %+v", recorder.Code, body)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read avatar directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed avatar update left managed files: %v", entries)
	}
}

type avatarErrorResponse struct {
	Code      string `json:"code"`
	Error     string `json:"error"`
	Retryable bool   `json:"retryable"`
	RequestID string `json:"request_id"`
}

func serveAvatarUpload(t *testing.T, handler *AvatarHandler, userID int64) *httptest.ResponseRecorder {
	t.Helper()
	var encoded bytes.Buffer
	input := image.NewRGBA(image.Rect(0, 0, 2, 2))
	input.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&encoded, input); err != nil {
		t.Fatalf("encode avatar fixture: %v", err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "fixture.png")
	if err != nil {
		t.Fatalf("create avatar form: %v", err)
	}
	if _, err := part.Write(encoded.Bytes()); err != nil {
		t.Fatalf("write avatar form: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close avatar form: %v", err)
	}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", userID)
		c.Set("request_id", "req-avatar-handler")
		c.Next()
	})
	router.POST("/users/me/avatar", handler.Upload)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/users/me/avatar", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(recorder, request)
	return recorder
}

func decodeAvatarError(t *testing.T, recorder *httptest.ResponseRecorder) avatarErrorResponse {
	t.Helper()
	var body avatarErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v body=%s", err, recorder.Body.String())
	}
	return body
}
