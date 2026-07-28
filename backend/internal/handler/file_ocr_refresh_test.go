package handler

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/huoguojun123/effchat/internal/model"
	"github.com/huoguojun123/effchat/internal/repository"
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
