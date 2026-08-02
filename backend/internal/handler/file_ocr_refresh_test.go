package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/extractor"
	"github.com/huoguojun123/EffChat/internal/filepolicy"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
)

func TestRefreshOCRFileHandler_PendingWithoutTaskKeepsWaiting(t *testing.T) {
	env := setupTestEnv(t)
	sessionID := createUploadTestSession(t, env)
	file := createOCRRefreshTestFile(t, env, sessionID, "ocr_pending", nil)

	w := env.doRequest(http.MethodPost, fmt.Sprintf("/api/v1/files/%d/ocr-refresh", file.ID), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200 body=%s", w.Code, w.Body.String())
	}

	got, err := repository.NewFileRepository(env.db).GetByID(file.ID, env.userID)
	if err != nil {
		t.Fatalf("get refreshed file: %v", err)
	}
	if got.ExtractStatus != "ocr_pending" {
		t.Fatalf("extract_status=%q, want ocr_pending", got.ExtractStatus)
	}
	if got.ExtractError != nil || got.OCRErrorType != nil {
		t.Fatalf("pending file should not be failed: error=%v type=%v", got.ExtractError, got.OCRErrorType)
	}
}

func TestRetryOCRFileHandlerRejectsDisabledPolicyBeforeRestart(t *testing.T) {
	env := setupTestEnv(t)
	sessionID := createUploadTestSession(t, env)
	provider := "mineru"
	sourcePath := "./storage/attachments/ocr/mock/source.pdf"
	file := &model.File{
		UserID:        env.userID,
		SessionID:     &sessionID,
		FileName:      "retry.pdf",
		FilePath:      "./storage/attachments/extracted/mock/retry.txt",
		FileType:      "application/pdf",
		FileSize:      128,
		ExtractStatus: "failed",
		OCRProvider:   &provider,
		OCRSourcePath: &sourcePath,
	}
	repo := repository.NewFileRepository(env.db)
	if err := repo.Create(file); err != nil {
		t.Fatalf("create failed OCR file: %v", err)
	}
	t.Cleanup(func() { _, _ = env.db.Exec("DELETE FROM files WHERE id = $1", file.ID) })
	if err := repository.NewConfigRepository(env.db).Update("attachment_extract_enabled", json.RawMessage("false")); err != nil {
		t.Fatalf("disable attachment extraction: %v", err)
	}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", env.userID)
		c.Next()
	})
	router.POST("/files/:id/ocr-retry", RetryOCRFileHandler(repo, repository.NewConfigRepository(env.db), nil, nil, nil))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, fmt.Sprintf("/files/%d/ocr-retry", file.ID), nil))
	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d, want %d body=%s", w.Code, http.StatusConflict, w.Body.String())
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Code != "attachment_extract_disabled" {
		t.Fatalf("response code=%q err=%v body=%s", body.Code, err, w.Body.String())
	}
	got, err := repo.GetByID(file.ID, env.userID)
	if err != nil || got.ExtractStatus != "failed" {
		t.Fatalf("disabled retry changed file state: status=%v err=%v", got, err)
	}
}

func TestRetryOCRFileHandlerErrorContract(t *testing.T) {
	t.Run("invalid id", func(t *testing.T) {
		recorder := serveOCRRetry(nil, nil, nil, nil, nil, 42, "invalid")
		assertOCRRetryError(t, recorder, http.StatusBadRequest, "file_id_invalid", false, false)
	})

	t.Run("policy repository failure", func(t *testing.T) {
		closedDB := setupHandlerTestDB(t)
		if err := closedDB.Close(); err != nil {
			t.Fatalf("close policy database: %v", err)
		}
		recorder := serveOCRRetry(nil, repository.NewConfigRepository(closedDB), nil, nil, nil, 42, "1")
		assertOCRRetryError(t, recorder, http.StatusServiceUnavailable, "attachment_policy_unavailable", true, true)
	})

	t.Run("runtime unavailable", func(t *testing.T) {
		db := setupHandlerTestDB(t)
		recorder := serveOCRRetry(repository.NewFileRepository(db), repository.NewConfigRepository(db), nil, nil, nil, 42, "1")
		assertOCRRetryError(t, recorder, http.StatusServiceUnavailable, "ocr_runtime_unavailable", true, true)
	})

	t.Run("channel repository failure", func(t *testing.T) {
		db := setupHandlerTestDB(t)
		closedDB := setupHandlerTestDB(t)
		if err := closedDB.Close(); err != nil {
			t.Fatalf("close channel database: %v", err)
		}
		repo := repository.NewFileRepository(db)
		runner := NewOCRRecoveryRunner(repo, nil, nil, nil, nil)
		recorder := serveOCRRetry(repo, repository.NewConfigRepository(db), service.NewChannelService(repository.NewChannelRepository(closedDB)), enabledExtractorClient(), runner, 42, "1")
		assertOCRRetryError(t, recorder, http.StatusServiceUnavailable, "ocr_config_unavailable", true, true)
	})

	t.Run("missing file", func(t *testing.T) {
		db := setupHandlerTestDB(t)
		repo := repository.NewFileRepository(db)
		channelService := enabledMinerUService(t, db)
		runner := NewOCRRecoveryRunner(repo, channelService, enabledExtractorClient(), nil, repository.NewConfigRepository(db))
		recorder := serveOCRRetry(repo, repository.NewConfigRepository(db), channelService, enabledExtractorClient(), runner, 42, "999999999")
		assertOCRRetryError(t, recorder, http.StatusNotFound, "file_not_found", false, false)
	})

	t.Run("file repository failure", func(t *testing.T) {
		db := setupHandlerTestDB(t)
		closedDB := setupHandlerTestDB(t)
		if err := closedDB.Close(); err != nil {
			t.Fatalf("close file database: %v", err)
		}
		channelService := enabledMinerUService(t, db)
		repo := repository.NewFileRepository(closedDB)
		runner := NewOCRRecoveryRunner(repo, channelService, enabledExtractorClient(), nil, repository.NewConfigRepository(db))
		recorder := serveOCRRetry(repo, repository.NewConfigRepository(db), channelService, enabledExtractorClient(), runner, 42, "1")
		assertOCRRetryError(t, recorder, http.StatusInternalServerError, "ocr_retry_failed", true, true)
	})
}

func TestRetryOCRFileHandlerReconcilesMissingSource(t *testing.T) {
	db := setupHandlerTestDB(t)
	repo := repository.NewFileRepository(db)
	channelService := enabledMinerUService(t, db)
	extractorClient := enabledExtractorClient()
	runner := NewOCRRecoveryRunner(repo, channelService, extractorClient, nil, repository.NewConfigRepository(db))
	const userID int64 = 42
	sourcePath := filepath.Join(filepolicy.AttachmentOCRRoot, "42", "missing.pdf")
	file := createRetryOCRTestFile(t, db, repo, userID, sourcePath)

	recorder := serveOCRRetry(repo, repository.NewConfigRepository(db), channelService, extractorClient, runner, userID, fmt.Sprint(file.ID))
	assertOCRRetryError(t, recorder, http.StatusConflict, "ocr_source_unavailable", false, false)
	stored, err := repo.GetByID(file.ID, userID)
	if err != nil || stored.ExtractStatus != "failed" || valueOrEmpty(stored.OCRErrorType) != "ocr_source_missing" {
		t.Fatalf("missing source state = %+v err=%v", stored, err)
	}
}

func TestRetryOCRFileHandlerRejectsExpiredAndUnsafeSources(t *testing.T) {
	t.Run("expired source", func(t *testing.T) {
		db := setupHandlerTestDB(t)
		repo := repository.NewFileRepository(db)
		channelService := enabledMinerUService(t, db)
		extractorClient := enabledExtractorClient()
		runner := NewOCRRecoveryRunner(repo, channelService, extractorClient, nil, repository.NewConfigRepository(db))
		file := createRetryOCRTestFile(t, db, repo, 42, filepath.Join(filepolicy.AttachmentOCRRoot, "42", "expired.pdf"))
		if _, err := db.Exec("UPDATE files SET created_at = $1 WHERE id = $2", time.Now().Add(-2*ocrSourceRetention), file.ID); err != nil {
			t.Fatalf("age OCR source: %v", err)
		}

		recorder := serveOCRRetry(repo, repository.NewConfigRepository(db), channelService, extractorClient, runner, 42, fmt.Sprint(file.ID))
		assertOCRRetryError(t, recorder, http.StatusConflict, "ocr_source_unavailable", false, false)
		stored, err := repo.GetByID(file.ID, 42)
		if err != nil || stored.ExtractStatus != "failed" {
			t.Fatalf("expired source state = %+v err=%v", stored, err)
		}
	})

	t.Run("outside managed storage", func(t *testing.T) {
		db := setupHandlerTestDB(t)
		repo := repository.NewFileRepository(db)
		channelService := enabledMinerUService(t, db)
		extractorClient := enabledExtractorClient()
		runner := NewOCRRecoveryRunner(repo, channelService, extractorClient, nil, repository.NewConfigRepository(db))
		file := createRetryOCRTestFile(t, db, repo, 42, "/tmp/effchat-ocr-fixture.pdf")

		recorder := serveOCRRetry(repo, repository.NewConfigRepository(db), channelService, extractorClient, runner, 42, fmt.Sprint(file.ID))
		assertOCRRetryError(t, recorder, http.StatusInternalServerError, "ocr_source_path_invalid", true, true)
		stored, err := repo.GetByID(file.ID, 42)
		if err != nil || stored.ExtractStatus != "failed" {
			t.Fatalf("unsafe source state = %+v err=%v", stored, err)
		}
	})

	t.Run("source stat failure", func(t *testing.T) {
		db := setupHandlerTestDB(t)
		repo := repository.NewFileRepository(db)
		channelService := enabledMinerUService(t, db)
		extractorClient := enabledExtractorClient()
		runner := NewOCRRecoveryRunner(repo, channelService, extractorClient, nil, repository.NewConfigRepository(db))
		blockedDir := filepath.Join(filepolicy.AttachmentOCRRoot, "42", fmt.Sprintf("blocked-%d", time.Now().UnixNano()))
		if err := os.MkdirAll(blockedDir, 0o700); err != nil {
			t.Fatalf("create blocked OCR directory: %v", err)
		}
		if err := os.Chmod(blockedDir, 0); err != nil {
			t.Fatalf("block OCR directory: %v", err)
		}
		t.Cleanup(func() {
			_ = os.Chmod(blockedDir, 0o700)
			_ = os.RemoveAll(blockedDir)
		})
		file := createRetryOCRTestFile(t, db, repo, 42, filepath.Join(blockedDir, "source.pdf"))

		recorder := serveOCRRetry(repo, repository.NewConfigRepository(db), channelService, extractorClient, runner, 42, fmt.Sprint(file.ID))
		assertOCRRetryError(t, recorder, http.StatusInternalServerError, "ocr_source_check_failed", true, true)
	})
}

func TestRetryOCRFileHandlerReportsCompensationFailure(t *testing.T) {
	db := setupHandlerTestDB(t)
	repo := repository.NewFileRepository(db)
	channelService := enabledMinerUService(t, db)
	extractorClient := enabledExtractorClient()
	runner := NewOCRRecoveryRunner(repo, channelService, extractorClient, nil, repository.NewConfigRepository(db))
	file := createRetryOCRTestFile(t, db, repo, 42, filepath.Join(filepolicy.AttachmentOCRRoot, "42", "missing-with-trigger.pdf"))
	if _, err := db.Exec(`
		CREATE FUNCTION reject_ocr_retry_compensation() RETURNS trigger AS $$
		BEGIN
			RAISE EXCEPTION 'fixture compensation failure';
		END;
		$$ LANGUAGE plpgsql;
		CREATE TRIGGER reject_ocr_retry_compensation
		BEFORE UPDATE ON files
		FOR EACH ROW
		WHEN (OLD.extract_status = 'ocr_pending' AND NEW.extract_status = 'failed')
		EXECUTE FUNCTION reject_ocr_retry_compensation()
	`); err != nil {
		t.Fatalf("install compensation failure trigger: %v", err)
	}

	recorder := serveOCRRetry(repo, repository.NewConfigRepository(db), channelService, extractorClient, runner, 42, fmt.Sprint(file.ID))
	assertOCRRetryError(t, recorder, http.StatusInternalServerError, "ocr_source_state_update_failed", true, true)
}

func TestRetryOCRFileHandlerRestartsAndWakesRunner(t *testing.T) {
	db := setupHandlerTestDB(t)
	repo := repository.NewFileRepository(db)
	channelService := enabledMinerUService(t, db)
	extractorClient := enabledExtractorClient()
	runner := NewOCRRecoveryRunner(repo, channelService, extractorClient, nil, repository.NewConfigRepository(db))
	sourcePath := filepath.Join(filepolicy.AttachmentOCRRoot, "42", fmt.Sprintf("success-%d.pdf", time.Now().UnixNano()))
	if err := filepolicy.WriteFile(sourcePath, []byte("fictional PDF fixture"), 0o600); err != nil {
		t.Fatalf("write OCR source fixture: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(sourcePath) })
	file := createRetryOCRTestFile(t, db, repo, 42, sourcePath)
	previousGeneration := file.OCRLeaseGeneration

	recorder := serveOCRRetry(repo, repository.NewConfigRepository(db), channelService, extractorClient, runner, 42, fmt.Sprint(file.ID))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200 body=%s", recorder.Code, recorder.Body.String())
	}
	stored, err := repo.GetByID(file.ID, 42)
	if err != nil || stored.ExtractStatus != "ocr_pending" || stored.OCRLeaseGeneration != previousGeneration+1 {
		t.Fatalf("restarted OCR state = %+v err=%v", stored, err)
	}
	if len(runner.wake) != 1 {
		t.Fatalf("runner wake signals=%d, want 1", len(runner.wake))
	}
}

func serveOCRRetry(fileRepo *repository.FileRepository, configRepo *repository.ConfigRepository, channelService *service.ChannelService, extractorClient *extractor.SidecarClient, runner *OCRRecoveryRunner, userID int64, fileID string) *httptest.ResponseRecorder {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", userID)
		c.Set("request_id", "req-ocr-retry")
		c.Next()
	})
	router.POST("/files/:id/ocr-retry", RetryOCRFileHandler(fileRepo, configRepo, channelService, extractorClient, runner))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/files/"+fileID+"/ocr-retry", nil))
	return recorder
}

func assertOCRRetryError(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string, retryable, requestID bool) {
	t.Helper()
	body := decodeUploadError(t, recorder)
	if recorder.Code != status || body.Code != code || body.Retryable != retryable {
		t.Fatalf("OCR retry response = %d %+v", recorder.Code, body)
	}
	if requestID && body.RequestID != "req-ocr-retry" {
		t.Fatalf("OCR retry response missing request ID: %+v", body)
	}
	if !requestID && body.RequestID != "" {
		t.Fatalf("OCR retry response unexpectedly included request ID: %+v", body)
	}
}

func enabledMinerUService(t *testing.T, db *sql.DB) *service.ChannelService {
	t.Helper()
	channelService := service.NewChannelService(repository.NewChannelRepository(db))
	enabled := true
	if _, err := channelService.SaveExternalService(&service.ExternalServiceInput{
		Key:            "mineru",
		DisplayName:    "MinerU fixture",
		Kind:           service.ServiceKindOCR,
		BaseURL:        "https://ocr.example.test",
		APIKey:         "fixture-key",
		Enabled:        &enabled,
		MaxConcurrency: 1,
	}); err != nil {
		t.Fatalf("enable MinerU fixture: %v", err)
	}
	return channelService
}

func enabledExtractorClient() *extractor.SidecarClient {
	return extractor.NewSidecarClient("http://extractor.example.test", time.Minute)
}

func createRetryOCRTestFile(t *testing.T, db *sql.DB, repo *repository.FileRepository, userID int64, sourcePath string) *model.File {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO users (id, username, password_hash, role, is_active, permissions, preferences)
		 VALUES ($1, $2, 'fixture-hash', 'user', true, '{}', '{}')
		 ON CONFLICT (id) DO NOTHING`,
		userID,
		fmt.Sprintf("ocr_retry_fixture_%d", userID),
	); err != nil {
		t.Fatalf("create OCR retry user fixture: %v", err)
	}
	provider := "mineru"
	file := &model.File{
		UserID:        userID,
		FileName:      "retry-fixture.pdf",
		FilePath:      filepath.Join(filepolicy.AttachmentExtractedRoot, fmt.Sprint(userID), "retry-fixture.txt"),
		FileType:      "application/pdf",
		FileSize:      128,
		ExtractStatus: "failed",
		OCRProvider:   &provider,
		OCRSourcePath: &sourcePath,
	}
	if err := repo.Create(file); err != nil {
		t.Fatalf("create OCR retry fixture: %v", err)
	}
	return file
}

func TestRefreshOCRFileHandler_RunningTaskOnlyReturnsPersistedState(t *testing.T) {
	env := setupTestEnv(t)
	sessionID := createUploadTestSession(t, env)
	taskID := "task-123"
	file := createOCRRefreshTestFile(t, env, sessionID, "ocr_running", &taskID)

	w := env.doRequest(http.MethodPost, fmt.Sprintf("/api/v1/files/%d/ocr-refresh", file.ID), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200 body=%s", w.Code, w.Body.String())
	}

	got, err := repository.NewFileRepository(env.db).GetByID(file.ID, env.userID)
	if err != nil {
		t.Fatalf("get refreshed file: %v", err)
	}
	if got.ExtractStatus != "ocr_running" {
		t.Fatalf("extract_status=%q, want ocr_running", got.ExtractStatus)
	}
	if got.OCRErrorType != nil {
		t.Fatalf("ocr_error_type=%v, want nil", got.OCRErrorType)
	}
	if got.ExtractError != nil {
		t.Fatalf("extract_error=%v, want nil", got.ExtractError)
	}
}

func createOCRRefreshTestFile(t *testing.T, env *testEnv, sessionID int64, status string, taskID *string) *model.File {
	t.Helper()
	provider := "mineru"
	now := time.Now()
	file := &model.File{
		UserID:           env.userID,
		SessionID:        &sessionID,
		FileName:         "scan.pdf",
		FilePath:         fmt.Sprintf("./storage/attachments/extracted/%d/ocr_refresh_%d.txt", env.userID, time.Now().UnixNano()),
		FileType:         "application/pdf",
		FileSize:         128,
		ExtractStatus:    status,
		OCRProvider:      &provider,
		OCRTaskID:        taskID,
		OCRStartedAt:     &now,
		OCRPageCount:     3,
		OCRProgressPages: 0,
	}
	repo := repository.NewFileRepository(env.db)
	if err := repo.Create(file); err != nil {
		t.Fatalf("create ocr refresh test file: %v", err)
	}
	t.Cleanup(func() {
		env.db.Exec("DELETE FROM files WHERE id = $1", file.ID)
	})
	return file
}
