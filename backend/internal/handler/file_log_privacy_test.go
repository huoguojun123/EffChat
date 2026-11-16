package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/filepolicy"
	"github.com/huoguojun123/EffChat/internal/model"
)

func TestFileLogsExcludeNamesPathsAndUpstreamErrors(t *testing.T) {
	const marker = "private-fixture-do-not-log"
	if os.Getenv("EFFCHAT_FILE_LOG_TEST_CHILD") == "1" {
		// A fresh process captures the real package logger, whose outputs are
		// intentionally initialized once. No production logger reset is needed.
		env := setupTestEnv(t)
		sessionID := createUploadTestSession(t, env)
		t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(filepolicy.AttachmentExtractedRoot, fmt.Sprint(env.userID))) })
		r := httptest.NewRecorder()
		env.router.ServeHTTP(r, uploadMultipart(t, env.token, sessionID, marker+".txt", "text/plain", []byte("synthetic text")))
		if r.Code != http.StatusCreated {
			t.Fatalf("upload code=%d", r.Code)
		}
		var uploaded model.File
		if err := json.Unmarshal(r.Body.Bytes(), &uploaded); err != nil {
			t.Fatal("decode fixture upload")
		}
		if _, err := env.db.Exec("UPDATE files SET file_path=$1 WHERE id=$2", "/private/"+marker, uploaded.ID); err != nil {
			t.Fatal("prepare unsafe path fixture")
		}
		if r := env.doRequest(http.MethodDelete, fmt.Sprintf("/api/v1/files/%d", uploaded.ID), nil); r.Code != http.StatusInternalServerError {
			t.Fatalf("unsafe path code=%d", r.Code)
		}
		r = httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(r)
		ctx.Request = httptest.NewRequest("POST", "/api/v1/files", nil)
		writeAttachmentExtractionError(ctx, errors.New("https://private.example/"+marker+"?key=fixture"))
		if r.Code != http.StatusServiceUnavailable {
			t.Fatalf("failure code=%d", r.Code)
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestFileLogsExcludeNamesPathsAndUpstreamErrors$", "-test.v")
	cmd.Env = append(os.Environ(), "EFFCHAT_FILE_LOG_TEST_CHILD=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("log fixture failed: %v", err)
	}
	if strings.Contains(string(output), "--- SKIP:") {
		t.Skip("isolated PostgreSQL required for log fixture")
	}
	if strings.Contains(string(output), marker) {
		t.Fatal("file identity or raw upstream error appeared in logs")
	}
	if !strings.Contains(string(output), "file extracted:") || !strings.Contains(string(output), "attachment_extractor_unavailable") {
		t.Fatal("required operational events missing")
	}
}
