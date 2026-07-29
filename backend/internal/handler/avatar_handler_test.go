package handler

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/huoguojun123/EffChat/internal/middleware"
	"github.com/huoguojun123/EffChat/internal/model"
)

func TestAvatarServeOnlyAcceptsManagedFilename(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	filename := "f739c265-653d-4ada-9820-3f4a57a1b2fb.png"
	if err := os.WriteFile(filepath.Join(dir, filename), []byte("png"), 0644); err != nil {
		t.Fatal(err)
	}
	handler := NewAvatarHandler(nil, dir)
	router := gin.New()
	router.GET("/avatars/:filename", handler.Serve)

	valid := httptest.NewRecorder()
	router.ServeHTTP(valid, httptest.NewRequest(http.MethodGet, "/avatars/"+filename, nil))
	if valid.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", valid.Code)
	}
	if valid.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("unexpected cache header: %s", valid.Header().Get("Cache-Control"))
	}

	for _, path := range []string{
		"/avatars/not-a-uuid.png",
		"/avatars/f739c265-653d-4ada-9820-3f4a57a1b2fb.svg",
		"/avatars/..%2Fsecret.png",
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s: expected 404, got %d", path, response.Code)
		}
	}
}

func TestRemoveManagedAvatarDoesNotDeleteExternalPath(t *testing.T) {
	dir := t.TempDir()
	external := filepath.Join(t.TempDir(), "external.png")
	if err := os.WriteFile(external, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	handler := NewAvatarHandler(nil, dir)
	externalURL := external
	handler.removeManaged(&externalURL)
	if _, err := os.Stat(external); err != nil {
		t.Fatalf("external file was removed: %v", err)
	}
}

func TestAvatarUploadReplaceAndDelete(t *testing.T) {
	env := setupTestEnv(t)
	dir := t.TempDir()
	handler := NewAvatarHandler(env.authService, dir)
	router := gin.New()
	router.GET("/api/v1/avatars/:filename", handler.Serve)
	authenticated := router.Group("/api/v1")
	authenticated.Use(middleware.AuthMiddleware(env.authService))
	authenticated.POST("/users/me/avatar", handler.Upload)
	authenticated.DELETE("/users/me/avatar", handler.Delete)

	oldFilename := uuid.NewString() + ".png"
	oldPath := filepath.Join(dir, oldFilename)
	if err := os.WriteFile(oldPath, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	oldURL := avatarURLPrefix + oldFilename
	if _, err := env.authService.UpdateAvatar(env.userID, &oldURL); err != nil {
		t.Fatal(err)
	}

	input := image.NewRGBA(image.Rect(0, 0, 500, 500))
	for y := 0; y < 500; y++ {
		for x := 0; x < 500; x++ {
			input.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: uint8(x + y), A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, input); err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "avatar.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(encoded.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	upload := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/users/me/avatar", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+env.token)
	router.ServeHTTP(upload, request)
	if upload.Code != http.StatusOK {
		t.Fatalf("upload status=%d body=%s", upload.Code, upload.Body.String())
	}
	var user model.User
	if err := json.Unmarshal(upload.Body.Bytes(), &user); err != nil {
		t.Fatal(err)
	}
	if user.AvatarURL == nil || *user.AvatarURL == oldURL {
		t.Fatalf("unexpected avatar URL: %v", user.AvatarURL)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old avatar was not removed: %v", err)
	}
	newFilename := filepath.Base(*user.AvatarURL)
	newInfo, err := os.Stat(filepath.Join(dir, newFilename))
	if err != nil {
		t.Fatal(err)
	}
	if newInfo.Size() > 100<<10 {
		t.Fatalf("avatar size=%d", newInfo.Size())
	}

	remove := httptest.NewRecorder()
	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/v1/users/me/avatar", nil)
	deleteRequest.Header.Set("Authorization", "Bearer "+env.token)
	router.ServeHTTP(remove, deleteRequest)
	if remove.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", remove.Code, remove.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, newFilename)); !os.IsNotExist(err) {
		t.Fatalf("new avatar was not removed: %v", err)
	}
}
