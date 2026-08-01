package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/huoguojun123/EffChat/internal/extractor"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
)

func TestOCRRecoveryRunnerDrainWaitsForWorkers(t *testing.T) {
	runner := NewOCRRecoveryRunner(nil, nil, nil, nil, nil)
	workerStarted := make(chan struct{})
	releaseWorker := make(chan struct{})
	runner.startWorker(func() {
		close(workerStarted)
		<-releaseWorker
	})
	<-workerStarted

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if runner.Drain(ctx) {
		t.Fatal("drain returned before the OCR worker exited")
	}

	close(releaseWorker)
	if !runner.Drain(context.Background()) {
		t.Fatal("drain did not complete after the OCR worker exited")
	}
}

func TestOCRRecoveryRunnerDrainWithoutStartCompletes(t *testing.T) {
	runner := NewOCRRecoveryRunner(nil, nil, nil, nil, nil)
	if !runner.Drain(context.Background()) {
		t.Fatal("drain without a started loop should complete")
	}
}

func TestOCRRecoveryRunnerDoesNotSubmitPendingWorkWhenPolicyBlocks(t *testing.T) {
	env := setupTestEnv(t)
	sessionID := createUploadTestSession(t, env)
	file := createOCRRefreshTestFile(t, env, sessionID, "ocr_pending", nil)
	repo := repository.NewFileRepository(env.db)
	runner := NewOCRRecoveryRunner(repo, nil, nil, nil, nil)

	for _, tc := range []struct {
		name      string
		policy    attachmentProcessingPolicy
		policyErr error
	}{
		{name: "disabled", policy: attachmentProcessingPolicy{Enabled: false, TimeoutSeconds: 60, MaxOutputMB: 5}},
		{name: "unavailable", policyErr: context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner.process(context.Background(), service.MinerUOCRConfig{}, tc.policy, tc.policyErr, file)
			got, err := repo.GetByID(file.ID, env.userID)
			if err != nil {
				t.Fatalf("get pending OCR file: %v", err)
			}
			if got.ExtractStatus != "ocr_pending" || got.OCRTaskID != nil || got.OCRAttempts != 0 {
				t.Fatalf("blocked pending task mutated submission state: status=%s task=%v attempts=%d", got.ExtractStatus, got.OCRTaskID, got.OCRAttempts)
			}
			if got.OCRNextRetryAt == nil || !got.OCRNextRetryAt.After(time.Now()) {
				t.Fatalf("blocked pending task did not back off: next_retry=%v", got.OCRNextRetryAt)
			}
		})
	}
}

func TestOCRRecoveryRunnerFinishesSubmittedTaskWhenNewExtractionIsDisabled(t *testing.T) {
	env := setupTestEnv(t)
	sessionID := createUploadTestSession(t, env)
	taskID := "submitted-task"
	file := createOCRRefreshTestFile(t, env, sessionID, "ocr_running", &taskID)
	file.FilePath = fmt.Sprintf("./storage/attachments/extracted/%d/policy_poll_%d.txt", env.userID, time.Now().UnixNano())
	if _, err := env.db.Exec("UPDATE files SET file_path = $1, extracted_text_path = $1 WHERE id = $2", file.FilePath, file.ID); err != nil {
		t.Fatalf("set OCR output path: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(file.FilePath) })

	var polls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		polls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"state":"ready","markdown":"completed text","token_estimate":2}`))
	}))
	defer server.Close()
	runner := NewOCRRecoveryRunner(repository.NewFileRepository(env.db), nil, extractor.NewSidecarClient(server.URL, time.Second), nil, nil)
	runner.process(context.Background(), service.MinerUOCRConfig{}, attachmentProcessingPolicy{Enabled: false, TimeoutSeconds: 60, MaxOutputMB: 1}, nil, file)

	got, err := repository.NewFileRepository(env.db).GetByID(file.ID, env.userID)
	if err != nil {
		t.Fatalf("get completed OCR file: %v", err)
	}
	if polls != 1 || got.ExtractStatus != "ready" {
		t.Fatalf("submitted task was not completed: polls=%d status=%s", polls, got.ExtractStatus)
	}
	content, err := os.ReadFile(filepath.Clean(file.FilePath))
	if err != nil || string(content) != "completed text" {
		t.Fatalf("completed OCR output=%q err=%v", content, err)
	}
}
