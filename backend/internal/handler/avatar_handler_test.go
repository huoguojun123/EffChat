package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/huoguojun123/EffChat/internal/middleware"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
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
	handler.removeManagedIfUnreferenced(&externalURL)
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

func TestConcurrentAvatarUploadsLeaveOnlyTheCurrentFile(t *testing.T) {
	env := setupTestEnv(t)
	dir := t.TempDir()
	handler := NewAvatarHandler(env.authService, dir)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", env.userID)
		c.Next()
	})
	router.POST("/users/me/avatar", handler.Upload)

	blocker, err := env.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin avatar blocker: %v", err)
	}
	if _, err := blocker.ExecContext(context.Background(), `SELECT id FROM users WHERE id = $1 FOR UPDATE`, env.userID); err != nil {
		_ = blocker.Rollback()
		t.Fatalf("lock avatar owner: %v", err)
	}

	requests := []*http.Request{
		newAvatarUploadRequest(t, color.RGBA{R: 255, A: 255}),
		newAvatarUploadRequest(t, color.RGBA{B: 255, A: 255}),
	}
	recorders := []*httptest.ResponseRecorder{httptest.NewRecorder(), httptest.NewRecorder()}
	var wg sync.WaitGroup
	for i := range requests {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			router.ServeHTTP(recorders[i], requests[i])
		}()
	}

	waitForAvatarPatchWaiters(t, env.db, 2)
	if err := blocker.Commit(); err != nil {
		t.Fatalf("release avatar blocker: %v", err)
	}
	wg.Wait()
	for _, recorder := range recorders {
		if recorder.Code != http.StatusOK {
			t.Fatalf("concurrent upload status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}

	user, err := env.authService.GetProfile(env.userID)
	if err != nil {
		t.Fatalf("reload avatar owner: %v", err)
	}
	if user.AvatarURL == nil {
		t.Fatal("concurrent uploads left no current avatar")
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.Base(*user.AvatarURL))); err != nil {
		t.Fatalf("current avatar file is missing: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read avatar directory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("concurrent uploads left orphan files: %v", entries)
	}
}

func TestCanceledAvatarSwapReclaimsItsNewFile(t *testing.T) {
	env := setupTestEnv(t)
	dir := t.TempDir()
	handler := NewAvatarHandler(env.authService, dir)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", env.userID)
		c.Next()
	})
	router.POST("/users/me/avatar", handler.Upload)

	blocker, err := env.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin avatar blocker: %v", err)
	}
	defer blocker.Rollback()
	if _, err := blocker.ExecContext(context.Background(), `SELECT id FROM users WHERE id = $1 FOR UPDATE`, env.userID); err != nil {
		t.Fatalf("lock avatar owner: %v", err)
	}

	request := newAvatarUploadRequest(t, color.RGBA{G: 255, A: 255})
	ctx, cancel := context.WithCancel(request.Context())
	request = request.WithContext(ctx)
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		router.ServeHTTP(recorder, request)
	}()
	waitForAvatarPatchWaiters(t, env.db, 1)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("canceled avatar upload did not return")
	}
	if err := blocker.Commit(); err != nil {
		t.Fatalf("release avatar blocker: %v", err)
	}
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("canceled upload status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read avatar directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("canceled upload left managed files: %v", entries)
	}
}

func TestAvatarCleanupWaitsForAllDatabaseReferences(t *testing.T) {
	env := setupTestEnv(t)
	dir := t.TempDir()
	handler := NewAvatarHandler(env.authService, dir)
	filename := uuid.NewString() + ".png"
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte("shared"), 0644); err != nil {
		t.Fatalf("write shared avatar: %v", err)
	}
	avatarURL := avatarURLPrefix + filename

	second := &model.User{
		Username:     "avatar-reference-" + fmt.Sprint(time.Now().UnixNano()),
		PasswordHash: "fixture-hash",
		Role:         "user",
		Permissions:  []byte(`{}`),
		Preferences:  []byte(`{}`),
		IsActive:     true,
	}
	repo := repository.NewUserRepository(env.db)
	if err := repo.Create(second); err != nil {
		t.Fatalf("create second avatar owner: %v", err)
	}
	t.Cleanup(func() { _, _ = env.db.Exec("DELETE FROM users WHERE id = $1", second.ID) })
	if _, err := env.authService.UpdateAvatar(second.ID, &avatarURL); err != nil {
		t.Fatalf("assign shared avatar: %v", err)
	}

	handler.removeManagedIfUnreferenced(&avatarURL)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("referenced avatar was removed: %v", err)
	}
	if _, err := env.authService.UpdateAvatar(second.ID, nil); err != nil {
		t.Fatalf("release shared avatar: %v", err)
	}
	handler.removeManagedIfUnreferenced(&avatarURL)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unreferenced avatar was not removed: %v", err)
	}
}

func newAvatarUploadRequest(t *testing.T, fill color.RGBA) *http.Request {
	t.Helper()
	input := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			input.SetRGBA(x, y, fill)
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, input); err != nil {
		t.Fatalf("encode avatar fixture: %v", err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "avatar.png")
	if err != nil {
		t.Fatalf("create avatar form: %v", err)
	}
	if _, err := part.Write(encoded.Bytes()); err != nil {
		t.Fatalf("write avatar form: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close avatar form: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/users/me/avatar", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

func waitForAvatarPatchWaiters(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		if err := db.QueryRow(`
			SELECT count(*)
			FROM pg_stat_activity
			WHERE pid <> pg_backend_pid()
			  AND wait_event_type = 'Lock'
			  AND query LIKE '%FROM users WHERE id = $1 FOR UPDATE%'
		`).Scan(&waiting); err == nil && waiting >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("avatar swaps did not reach the row lock: want %d waiters", want)
}
