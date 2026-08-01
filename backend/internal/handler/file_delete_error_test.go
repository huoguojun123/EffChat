package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/filepolicy"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
)

func TestDeleteFileHandlerErrorContract(t *testing.T) {
	t.Run("invalid id", func(t *testing.T) {
		assertDeleteFileError(t, serveDeleteFile(nil, 42, "invalid"), http.StatusBadRequest, "file_id_invalid", false, false)
	})

	t.Run("missing file", func(t *testing.T) {
		db := setupHandlerTestDB(t)
		assertDeleteFileError(t, serveDeleteFile(repository.NewFileRepository(db), 42, "999999999"), http.StatusNotFound, "file_not_found", false, false)
	})

	t.Run("lookup repository failure", func(t *testing.T) {
		db := setupHandlerTestDB(t)
		if err := db.Close(); err != nil {
			t.Fatalf("close file database: %v", err)
		}
		assertDeleteFileError(t, serveDeleteFile(repository.NewFileRepository(db), 42, "1"), http.StatusInternalServerError, "file_load_failed", true, true)
	})

	t.Run("unsafe managed path", func(t *testing.T) {
		db := setupHandlerTestDB(t)
		repo := repository.NewFileRepository(db)
		file := createDeleteFileFixture(t, db, repo, 42, "/tmp/effchat-delete-fixture.txt")
		assertDeleteFileError(t, serveDeleteFile(repo, 42, fmt.Sprint(file.ID)), http.StatusInternalServerError, "file_path_invalid", true, true)
	})

	t.Run("mutation failure", func(t *testing.T) {
		db := setupHandlerTestDB(t)
		repo := repository.NewFileRepository(db)
		file := createDeleteFileFixture(t, db, repo, 42, filepath.Join(filepolicy.AttachmentOriginalsRoot, "42", "mutation.txt"))
		if _, err := db.Exec(`
			CREATE FUNCTION reject_file_deletion() RETURNS trigger AS $$
			BEGIN
				RAISE EXCEPTION 'fixture deletion failure';
			END;
			$$ LANGUAGE plpgsql;
			CREATE TRIGGER reject_file_deletion
			BEFORE UPDATE ON files
			FOR EACH ROW
			WHEN (NEW.status = 'cleanup_claimed')
			EXECUTE FUNCTION reject_file_deletion()
		`); err != nil {
			t.Fatalf("install deletion failure trigger: %v", err)
		}
		assertDeleteFileError(t, serveDeleteFile(repo, 42, fmt.Sprint(file.ID)), http.StatusInternalServerError, "file_delete_failed", true, true)
	})
}

func TestDeleteFileHandlerMarksFileForDeferredCleanup(t *testing.T) {
	db := setupHandlerTestDB(t)
	repo := repository.NewFileRepository(db)
	file := createDeleteFileFixture(t, db, repo, 42, filepath.Join(filepolicy.AttachmentOriginalsRoot, "42", "success.txt"))
	recorder := serveDeleteFile(repo, 42, fmt.Sprint(file.ID))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200 body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Message      string `json:"message"`
		CleanupAfter string `json:"cleanup_after"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.Message != "file deleted" || body.CleanupAfter == "" {
		t.Fatalf("delete response=%+v err=%v", body, err)
	}
	var status string
	if err := db.QueryRow("SELECT status FROM files WHERE id = $1", file.ID).Scan(&status); err != nil || status != repository.FileStatusCleanupClaimed {
		t.Fatalf("stored status=%q err=%v", status, err)
	}
}

func serveDeleteFile(repo *repository.FileRepository, userID int64, fileID string) *httptest.ResponseRecorder {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", userID)
		c.Set("request_id", "req-file-delete")
		c.Next()
	})
	router.DELETE("/files/:id", DeleteFileHandler(repo))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/files/"+fileID, nil))
	return recorder
}

func assertDeleteFileError(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string, retryable, requestID bool) {
	t.Helper()
	body := decodeUploadError(t, recorder)
	if recorder.Code != status || body.Code != code || body.Retryable != retryable {
		t.Fatalf("delete response=%d %+v", recorder.Code, body)
	}
	if requestID && body.RequestID != "req-file-delete" {
		t.Fatalf("delete response missing request ID: %+v", body)
	}
	if !requestID && body.RequestID != "" {
		t.Fatalf("delete response unexpectedly included request ID: %+v", body)
	}
}

func createDeleteFileFixture(t *testing.T, db *sql.DB, repo *repository.FileRepository, userID int64, filePath string) *model.File {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO users (id, username, password_hash, role, is_active, permissions, preferences)
		 VALUES ($1, $2, 'fixture-hash', 'user', true, '{}', '{}')
		 ON CONFLICT (id) DO NOTHING`,
		userID,
		fmt.Sprintf("file_delete_fixture_%d", userID),
	); err != nil {
		t.Fatalf("create file delete user fixture: %v", err)
	}
	file := &model.File{UserID: userID, FileName: "delete-fixture.txt", FilePath: filePath, FileType: "text/plain", FileSize: 32, ExtractStatus: "ready"}
	if err := repo.Create(file); err != nil {
		t.Fatalf("create file delete fixture: %v", err)
	}
	return file
}
