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
	"github.com/huoguojun123/EffChat/internal/filepolicy"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
)

type cleanupResponse struct {
	Marked    int                  `json:"marked"`
	Removed   int                  `json:"removed"`
	Failed    int                  `json:"failed"`
	Failures  []fileCleanupFailure `json:"failures"`
	RequestID string               `json:"request_id"`
}

func TestCleanupOrphanFilesHandlerErrorContract(t *testing.T) {
	t.Run("invalid parameter", func(t *testing.T) {
		recorder := serveFileCleanup(nil, "?limit=invalid")
		body := decodeUploadError(t, recorder)
		if recorder.Code != http.StatusBadRequest || body.Code != "file_cleanup_parameter_invalid" || body.Retryable {
			t.Fatalf("cleanup parameter response=%d %+v", recorder.Code, body)
		}
	})

	t.Run("repository failure", func(t *testing.T) {
		db := setupHandlerTestDB(t)
		if err := db.Close(); err != nil {
			t.Fatalf("close cleanup database: %v", err)
		}
		recorder := serveFileCleanup(repository.NewFileRepository(db), "")
		body := decodeUploadError(t, recorder)
		if recorder.Code != http.StatusInternalServerError || body.Code != "file_cleanup_reference_count_failed" || !body.Retryable || body.RequestID != "req-file-cleanup" {
			t.Fatalf("cleanup repository response=%d %+v", recorder.Code, body)
		}
	})
}

func TestCleanupOrphanFilesHandlerReportsStructuredPartialFailures(t *testing.T) {
	t.Run("unsafe path releases claim", func(t *testing.T) {
		db := setupHandlerTestDB(t)
		repo := repository.NewFileRepository(db)
		file := createStaleCleanupFixture(t, db, repo, "/tmp/effchat-cleanup-fixture.txt")
		body := decodeCleanupResponse(t, serveFileCleanup(repo, ""))
		assertCleanupFailure(t, body, file.ID, "file_cleanup_remove_failed")
		var lease *time.Time
		if err := db.QueryRow("SELECT cleanup_lease_until FROM files WHERE id = $1", file.ID).Scan(&lease); err != nil || lease != nil {
			t.Fatalf("released cleanup lease=%v err=%v", lease, err)
		}
	})

	t.Run("finalize failure", func(t *testing.T) {
		db := setupHandlerTestDB(t)
		repo := repository.NewFileRepository(db)
		file := createStaleCleanupFixture(t, db, repo, filepath.Join(filepolicy.AttachmentOriginalsRoot, "42", "missing-finalize.txt"))
		if _, err := db.Exec(`
			CREATE FUNCTION reject_cleanup_finalize() RETURNS trigger AS $$
			BEGIN RAISE EXCEPTION 'fixture finalize failure'; END;
			$$ LANGUAGE plpgsql;
			CREATE TRIGGER reject_cleanup_finalize BEFORE UPDATE ON files
			FOR EACH ROW WHEN (NEW.status = 'storage_removed')
			EXECUTE FUNCTION reject_cleanup_finalize()
		`); err != nil {
			t.Fatalf("install finalize trigger: %v", err)
		}
		body := decodeCleanupResponse(t, serveFileCleanup(repo, ""))
		assertCleanupFailure(t, body, file.ID, "file_cleanup_finalize_failed")
	})

	t.Run("claim release failure", func(t *testing.T) {
		db := setupHandlerTestDB(t)
		repo := repository.NewFileRepository(db)
		file := createStaleCleanupFixture(t, db, repo, "/tmp/effchat-cleanup-release-fixture.txt")
		if _, err := db.Exec(`
			CREATE FUNCTION reject_cleanup_release() RETURNS trigger AS $$
			BEGIN RAISE EXCEPTION 'fixture release failure'; END;
			$$ LANGUAGE plpgsql;
			CREATE TRIGGER reject_cleanup_release BEFORE UPDATE ON files
			FOR EACH ROW WHEN (OLD.cleanup_lease_until IS NOT NULL AND NEW.cleanup_lease_until IS NULL AND NEW.status = 'cleanup_claimed')
			EXECUTE FUNCTION reject_cleanup_release()
		`); err != nil {
			t.Fatalf("install release trigger: %v", err)
		}
		body := decodeCleanupResponse(t, serveFileCleanup(repo, ""))
		assertCleanupFailure(t, body, file.ID, "file_cleanup_claim_release_failed")
	})
}

func TestCleanupOrphanFilesHandlerRemovesAndFinalizesManagedFile(t *testing.T) {
	db := setupHandlerTestDB(t)
	repo := repository.NewFileRepository(db)
	path := filepath.Join(filepolicy.AttachmentOriginalsRoot, "42", fmt.Sprintf("cleanup-%d.txt", time.Now().UnixNano()))
	if err := filepolicy.WriteFile(path, []byte("fictional cleanup fixture"), 0o600); err != nil {
		t.Fatalf("write cleanup fixture: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	file := createStaleCleanupFixture(t, db, repo, path)
	body := decodeCleanupResponse(t, serveFileCleanup(repo, ""))
	if body.Marked != 1 || body.Removed != 1 || body.Failed != 0 || len(body.Failures) != 0 || body.RequestID != "" {
		t.Fatalf("cleanup success response=%+v", body)
	}
	var status string
	if err := db.QueryRow("SELECT status FROM files WHERE id = $1", file.ID).Scan(&status); err != nil || status != repository.FileStatusStorageRemoved {
		t.Fatalf("cleanup status=%q err=%v", status, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("managed file still exists: %v", err)
	}
}

func serveFileCleanup(repo *repository.FileRepository, query string) *httptest.ResponseRecorder {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("request_id", "req-file-cleanup")
		c.Next()
	})
	router.POST("/cleanup", CleanupOrphanFilesHandler(repo))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/cleanup"+query, nil))
	return recorder
}

func decodeCleanupResponse(t *testing.T, recorder *httptest.ResponseRecorder) cleanupResponse {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("cleanup status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var body cleanupResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode cleanup response: %v body=%s", err, recorder.Body.String())
	}
	return body
}

func assertCleanupFailure(t *testing.T, body cleanupResponse, fileID int64, code string) {
	t.Helper()
	if body.Failed != 1 || len(body.Failures) != 1 || body.Failures[0].FileID != fileID || body.Failures[0].Code != code || !body.Failures[0].Retryable || body.RequestID != "req-file-cleanup" {
		t.Fatalf("cleanup partial response=%+v", body)
	}
}

func createStaleCleanupFixture(t *testing.T, db *sql.DB, repo *repository.FileRepository, filePath string) *model.File {
	t.Helper()
	file := createDeleteFileFixture(t, db, repo, 42, filePath)
	if _, err := db.Exec("UPDATE files SET created_at = $1 WHERE id = $2", time.Now().Add(-48*time.Hour), file.ID); err != nil {
		t.Fatalf("age cleanup fixture: %v", err)
	}
	return file
}
