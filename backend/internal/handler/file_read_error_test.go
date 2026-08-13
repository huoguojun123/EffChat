package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
)

func TestDownloadFileHandlerUsesServedContentContract(t *testing.T) {
	env := setupTestEnv(t)
	repo := repository.NewFileRepository(env.db)
	router := newFileReadContractRouter(repo, env.userID)

	makeFile := func(name, contentType, body string, extracted bool) *model.File {
		t.Helper()
		path := fmt.Sprintf("./storage/attachments/%s/%d/%d", map[bool]string{true: "extracted", false: "originals"}[extracted], env.userID, time.Now().UnixNano())
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Remove(path) })
		file := &model.File{UserID: env.userID, FileName: name, FilePath: path, FileType: contentType, FileSize: int64(len(body)), ExtractStatus: "ready"}
		if extracted {
			file.ExtractedTextPath = &path
		}
		if err := repo.Create(file); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = env.db.Exec("DELETE FROM files WHERE id = $1", file.ID) })
		return file
	}

	document := makeFile("预算.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "# extracted workbook", true)
	image := makeFile("cover.png", "image/png", "fixture png", false)

	for _, tt := range []struct {
		name            string
		file            *model.File
		wantFilename    string
		wantContentType string
		wantBody        string
	}{
		{name: "document sidecar", file: document, wantFilename: "预算.xlsx.txt", wantContentType: "text/plain; charset=utf-8", wantBody: "# extracted workbook"},
		{name: "image original", file: image, wantFilename: "cover.png", wantContentType: "image/png", wantBody: "fixture png"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/files/%d", tt.file.ID), nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			_, params, err := mime.ParseMediaType(recorder.Header().Get("Content-Disposition"))
			if err != nil || params["filename"] != tt.wantFilename {
				t.Fatalf("content disposition=%q filename=%q err=%v", recorder.Header().Get("Content-Disposition"), params["filename"], err)
			}
			if got := recorder.Header().Get("Content-Type"); got != tt.wantContentType {
				t.Fatalf("content type=%q, want %q", got, tt.wantContentType)
			}
			if got := recorder.Body.String(); got != tt.wantBody {
				t.Fatalf("body=%q, want %q", got, tt.wantBody)
			}
		})
	}
}

func TestFileLookupErrorHidesInternalDetails(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantRetry  bool
	}{
		{name: "missing", err: repository.ErrNotFound, wantStatus: http.StatusNotFound, wantCode: "file_not_found"},
		{name: "repository failure", err: errors.New("postgres://secret@internal/private/file"), wantStatus: http.StatusInternalServerError, wantCode: "file_load_failed", wantRetry: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/files/1", nil)
			ctx.Set("request_id", "req-file-read")
			writeFileLookupError(ctx, tt.err)

			assertFileErrorResponse(t, recorder, tt.wantStatus, tt.wantCode, tt.wantRetry)
			if strings.Contains(recorder.Body.String(), "secret") || strings.Contains(recorder.Body.String(), "/private/") {
				t.Fatalf("response leaked internal details: %s", recorder.Body.String())
			}
		})
	}
}

func TestFileReadEndpointsClassifyInputMissingAndRepositoryFailures(t *testing.T) {
	db := setupHandlerTestDB(t)
	repo := repository.NewFileRepository(db)
	router := newFileReadContractRouter(repo, 9_999_999_999)

	for _, endpoint := range []struct {
		method     string
		path       string
		wantStatus int
		wantCode   string
	}{
		{method: http.MethodGet, path: "/files/not-a-number", wantStatus: http.StatusBadRequest, wantCode: "file_id_invalid"},
		{method: http.MethodGet, path: "/files/not-a-number/preview", wantStatus: http.StatusBadRequest, wantCode: "file_id_invalid"},
		{method: http.MethodPost, path: "/files/not-a-number/ocr-refresh", wantStatus: http.StatusBadRequest, wantCode: "file_id_invalid"},
		{method: http.MethodGet, path: "/files?session_id=not-a-number", wantStatus: http.StatusBadRequest, wantCode: "session_id_invalid"},
		{method: http.MethodGet, path: "/files/9999999999", wantStatus: http.StatusNotFound, wantCode: "file_not_found"},
		{method: http.MethodGet, path: "/files/9999999999/preview", wantStatus: http.StatusNotFound, wantCode: "file_not_found"},
		{method: http.MethodPost, path: "/files/9999999999/ocr-refresh", wantStatus: http.StatusNotFound, wantCode: "file_not_found"},
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(endpoint.method, endpoint.path, nil))
		assertFileErrorResponse(t, recorder, endpoint.wantStatus, endpoint.wantCode, false)
	}

	closedDB := setupHandlerTestDB(t)
	closedRepo := repository.NewFileRepository(closedDB)
	if err := closedDB.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}
	closedRouter := newFileReadContractRouter(closedRepo, 1)
	for _, endpoint := range []struct {
		method   string
		path     string
		wantCode string
	}{
		{method: http.MethodGet, path: "/files", wantCode: "file_list_failed"},
		{method: http.MethodGet, path: "/files/1", wantCode: "file_load_failed"},
		{method: http.MethodGet, path: "/files/1/preview", wantCode: "file_load_failed"},
		{method: http.MethodPost, path: "/files/1/ocr-refresh", wantCode: "file_load_failed"},
	} {
		recorder := httptest.NewRecorder()
		closedRouter.ServeHTTP(recorder, httptest.NewRequest(endpoint.method, endpoint.path, nil))
		assertFileErrorResponse(t, recorder, http.StatusInternalServerError, endpoint.wantCode, true)
	}
}

func TestFileReadEndpointsHideManagedStorageFailures(t *testing.T) {
	env := setupTestEnv(t)
	repo := repository.NewFileRepository(env.db)
	extractedPath := "/private/secret-extracted.txt"
	file := &model.File{
		UserID:            env.userID,
		FileName:          "fictional.txt",
		FilePath:          "/private/secret-original.txt",
		FileType:          "text/plain",
		FileSize:          64,
		ExtractedTextPath: &extractedPath,
		ExtractStatus:     "ready",
	}
	if err := repo.Create(file); err != nil {
		t.Fatalf("create file fixture: %v", err)
	}
	t.Cleanup(func() { _, _ = env.db.Exec("DELETE FROM files WHERE id = $1", file.ID) })
	router := newFileReadContractRouter(repo, env.userID)

	for _, endpoint := range []struct {
		path     string
		wantCode string
	}{
		{path: fmt.Sprintf("/files/%d", file.ID), wantCode: "file_download_unavailable"},
		{path: fmt.Sprintf("/files/%d/preview", file.ID), wantCode: "file_preview_failed"},
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, endpoint.path, nil))
		assertFileErrorResponse(t, recorder, http.StatusInternalServerError, endpoint.wantCode, true)
		if recorder.Header().Get("Content-Disposition") != "" {
			t.Fatalf("%s retained download headers on JSON failure: %q", endpoint.path, recorder.Header().Get("Content-Disposition"))
		}
		if strings.Contains(recorder.Body.String(), "secret") || strings.Contains(recorder.Body.String(), "/private/") {
			t.Fatalf("%s leaked managed path: %s", endpoint.path, recorder.Body.String())
		}
	}
}

func TestFileReadEndpointsExposeStableBusinessCodes(t *testing.T) {
	env := setupTestEnv(t)
	repo := repository.NewFileRepository(env.db)
	pending := &model.File{
		UserID:        env.userID,
		FileName:      "pending.pdf",
		FilePath:      "./storage/attachments/extracted/fictional-pending.txt",
		FileType:      "application/pdf",
		FileSize:      64,
		ExtractStatus: "ocr_pending",
	}
	if err := repo.Create(pending); err != nil {
		t.Fatalf("create pending fixture: %v", err)
	}

	previewPath := fmt.Sprintf("./storage/attachments/extracted/%d/preview_contract_%d.txt", env.userID, time.Now().UnixNano())
	if err := os.MkdirAll(filepath.Dir(previewPath), 0o700); err != nil {
		t.Fatalf("create preview directory: %v", err)
	}
	if err := os.WriteFile(previewPath, []byte("fictional preview"), 0o600); err != nil {
		t.Fatalf("write preview fixture: %v", err)
	}
	ready := &model.File{
		UserID:            env.userID,
		FileName:          "preview.txt",
		FilePath:          previewPath,
		FileType:          "text/plain",
		FileSize:          17,
		ExtractedTextPath: &previewPath,
		ExtractStatus:     "ready",
	}
	if err := repo.Create(ready); err != nil {
		t.Fatalf("create preview fixture: %v", err)
	}
	t.Cleanup(func() {
		_, _ = env.db.Exec("DELETE FROM files WHERE id IN ($1, $2)", pending.ID, ready.ID)
		_ = os.Remove(previewPath)
	})
	router := newFileReadContractRouter(repo, env.userID)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/files/%d", pending.ID), nil))
	assertFileErrorResponse(t, recorder, http.StatusConflict, "file_content_pending", true)

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/files/%d/preview", pending.ID), nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("pending preview status=%d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var pendingPreview map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &pendingPreview); err != nil {
		t.Fatalf("decode pending preview: %v", err)
	}
	if pendingPreview["code"] != "file_text_unavailable" || pendingPreview["retryable"] != true {
		t.Fatalf("pending preview response=%#v", pendingPreview)
	}

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/files/%d/preview?cursor=not-base64", ready.ID), nil))
	assertFileErrorResponse(t, recorder, http.StatusBadRequest, "preview_cursor_invalid", false)
}

func newFileReadContractRouter(repo *repository.FileRepository, userID int64) *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", userID)
		c.Set("request_id", "req-file-read")
		c.Next()
	})
	router.GET("/files", ListFilesHandler(repo))
	router.GET("/files/:id/preview", PreviewFileHandler(repo, nil, nil))
	router.POST("/files/:id/ocr-refresh", RefreshOCRFileHandler(repo, nil, nil))
	router.GET("/files/:id", DownloadFileHandler(repo))
	return router
}

func assertFileErrorResponse(t *testing.T, recorder *httptest.ResponseRecorder, wantStatus int, wantCode string, wantRetry bool) {
	t.Helper()
	if recorder.Code != wantStatus {
		t.Fatalf("status=%d, want %d; body=%s", recorder.Code, wantStatus, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, recorder.Body.String())
	}
	if payload["code"] != wantCode || payload["retryable"] != wantRetry {
		t.Fatalf("response=%#v, want code=%q retryable=%v", payload, wantCode, wantRetry)
	}
	if wantStatus >= http.StatusInternalServerError && payload["request_id"] != "req-file-read" {
		t.Fatalf("server response lacks request id: %#v", payload)
	}
}
