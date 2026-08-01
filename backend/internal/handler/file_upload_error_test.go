package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/extractor"
	"github.com/huoguojun123/EffChat/internal/filepolicy"
	"github.com/huoguojun123/EffChat/internal/repository"
)

type uploadErrorResponse struct {
	Error     string `json:"error"`
	Code      string `json:"code"`
	Retryable bool   `json:"retryable"`
	RequestID string `json:"request_id"`
}

func decodeUploadError(t *testing.T, recorder *httptest.ResponseRecorder) uploadErrorResponse {
	t.Helper()
	var body uploadErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode upload error: %v (body: %s)", err, recorder.Body.String())
	}
	return body
}

func uploadFailureRouter(fileRepo *repository.FileRepository, configRepo *repository.ConfigRepository, sessionRepo *repository.SessionRepository, userID int64) *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", userID)
		c.Set("request_id", "req-file-upload")
		c.Next()
	})
	options := []UploadFileHandlerOption{}
	if sessionRepo != nil {
		options = append(options, WithUploadSessionRepo(sessionRepo))
	}
	router.POST("/api/v1/files", UploadFileHandler(fileRepo, configRepo, options...))
	return router
}

func TestUploadSessionLookupKeepsMissingAndRepositoryFailuresDistinct(t *testing.T) {
	configDB := setupHandlerTestDB(t)
	defer configDB.Close()
	fileRepo := repository.NewFileRepository(configDB)
	configRepo := repository.NewConfigRepository(configDB)

	t.Run("missing session", func(t *testing.T) {
		router := uploadFailureRouter(fileRepo, configRepo, repository.NewSessionRepository(configDB), 9_999_999_991)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, uploadMultipart(t, "", 9_999_999_992, "fixture.txt", "text/plain", []byte("fixture")))
		body := decodeUploadError(t, recorder)
		if recorder.Code != http.StatusNotFound || body.Code != "session_not_found" || body.Retryable || body.RequestID != "" {
			t.Fatalf("missing session response = %d %+v", recorder.Code, body)
		}
	})

	t.Run("repository failure", func(t *testing.T) {
		closedDB := setupHandlerTestDB(t)
		if err := closedDB.Close(); err != nil {
			t.Fatalf("close session database: %v", err)
		}
		router := uploadFailureRouter(fileRepo, configRepo, repository.NewSessionRepository(closedDB), 9_999_999_993)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, uploadMultipart(t, "", 9_999_999_994, "fixture.txt", "text/plain", []byte("fixture")))
		body := decodeUploadError(t, recorder)
		if recorder.Code != http.StatusInternalServerError || body.Code != "session_load_failed" || !body.Retryable || body.RequestID != "req-file-upload" {
			t.Fatalf("repository failure response = %d %+v", recorder.Code, body)
		}
		if strings.Contains(recorder.Body.String(), "sql") || strings.Contains(recorder.Body.String(), "database") {
			t.Fatalf("repository failure leaked internal detail: %s", recorder.Body.String())
		}
	})
}

func TestUploadRepositoryFailureUsesRetryableRequestIDContract(t *testing.T) {
	configDB := setupHandlerTestDB(t)
	defer configDB.Close()
	closedDB := setupHandlerTestDB(t)
	if err := closedDB.Close(); err != nil {
		t.Fatalf("close file database: %v", err)
	}

	router := uploadFailureRouter(repository.NewFileRepository(closedDB), repository.NewConfigRepository(configDB), nil, 9_999_999_995)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, uploadMultipart(t, "", 9_999_999_996, "fixture.txt", "text/plain", []byte("fixture")))
	body := decodeUploadError(t, recorder)
	if recorder.Code != http.StatusInternalServerError || body.Code != "file_duplicate_check_failed" || !body.Retryable || body.RequestID != "req-file-upload" {
		t.Fatalf("repository failure response = %d %+v", recorder.Code, body)
	}
}

func TestUploadMetadataFailureRemovesExtractedSidecar(t *testing.T) {
	db := setupHandlerTestDB(t)
	defer db.Close()
	const userID int64 = 9_999_999_997
	const sessionID int64 = 9_999_999_998
	extractedDir := filepath.Join(filepolicy.AttachmentExtractedRoot, "9999999997")
	_ = os.RemoveAll(extractedDir)
	t.Cleanup(func() { _ = os.RemoveAll(extractedDir) })

	router := uploadFailureRouter(repository.NewFileRepository(db), repository.NewConfigRepository(db), nil, userID)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, uploadMultipart(t, "", sessionID, "fixture.txt", "text/plain", []byte("fixture text")))
	body := decodeUploadError(t, recorder)
	if recorder.Code != http.StatusInternalServerError || body.Code != "file_metadata_create_failed" || !body.Retryable || body.RequestID != "req-file-upload" {
		t.Fatalf("metadata failure response = %d %+v", recorder.Code, body)
	}
	remainingFiles := 0
	err := filepath.WalkDir(extractedDir, func(_ string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			remainingFiles++
		}
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("walk extracted directory: %v", err)
	}
	if remainingFiles != 0 {
		t.Fatalf("metadata failure left %d extracted sidecar(s)", remainingFiles)
	}
}

func TestOCRQueueFailuresUseStableServerCodes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code string
	}{
		{name: "buffer", err: errOCRUploadBufferWrite, code: "ocr_upload_buffer_write_failed"},
		{name: "metadata", err: errOCRMetadataCreate, code: "ocr_metadata_create_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/files", nil)
			ctx.Set("request_id", "req-file-upload")
			writeOCRQueueError(ctx, test.err)
			body := decodeUploadError(t, recorder)
			if recorder.Code != http.StatusInternalServerError || body.Code != test.code || !body.Retryable || body.RequestID != "req-file-upload" {
				t.Fatalf("queue failure response = %d %+v", recorder.Code, body)
			}
		})
	}
}

func TestAttachmentExtractionErrorContract(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		status    int
		code      string
		retryable bool
	}{
		{name: "unsupported", err: extractor.ErrUnsupported, status: http.StatusUnsupportedMediaType, code: "attachment_type_unsupported"},
		{name: "no text", err: extractor.ErrNoReadableText, status: http.StatusUnprocessableEntity, code: "attachment_no_readable_text"},
		{name: "limit", err: extractor.ErrLimitExceeded, status: http.StatusRequestEntityTooLarge, code: "attachment_extract_too_large"},
		{name: "invalid content", err: extractor.ErrUnprocessable, status: http.StatusUnprocessableEntity, code: "attachment_extract_failed"},
		{name: "dependency", err: errors.New("https://secret.example/private/path"), status: http.StatusServiceUnavailable, code: "attachment_extractor_unavailable", retryable: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/files", nil)
			ctx.Set("request_id", "req-file-upload")
			writeAttachmentExtractionError(ctx, test.err)
			body := decodeUploadError(t, recorder)
			if recorder.Code != test.status || body.Code != test.code || body.Retryable != test.retryable {
				t.Fatalf("extraction response = %d %+v", recorder.Code, body)
			}
			if test.retryable && body.RequestID != "req-file-upload" {
				t.Fatalf("retryable extraction response missing request ID: %+v", body)
			}
			if strings.Contains(recorder.Body.String(), "secret.example") {
				t.Fatalf("extraction response leaked internal error: %s", recorder.Body.String())
			}
		})
	}
}

func TestParseRequiredSessionIDUsesStableCodes(t *testing.T) {
	tests := []struct {
		name string
		body string
		code string
	}{
		{name: "missing", body: "", code: "session_id_required"},
		{name: "invalid", body: "session_id=not-a-number", code: "session_id_invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/files", strings.NewReader(test.body))
			ctx.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if _, ok := parseRequiredSessionID(ctx); ok {
				t.Fatal("parseRequiredSessionID() unexpectedly accepted invalid input")
			}
			body := decodeUploadError(t, recorder)
			if recorder.Code != http.StatusBadRequest || body.Code != test.code || body.Retryable {
				t.Fatalf("session ID response = %d %+v", recorder.Code, body)
			}
		})
	}
}
