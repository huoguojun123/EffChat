package handler

import (
	"context"
	"errors"
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
)

func TestOCRQueueRejectsFinalCapacityWithoutLeavingSource(t *testing.T) {
	env := setupTestEnv(t)
	sessionID := createUploadTestSession(t, env)
	repo := repository.NewFileRepository(env.db)
	channelService := enabledMinerUService(t, env.db)
	client := enabledExtractorClient()
	opts := uploadFileHandlerOptions{channelService: channelService, extractorClient: client,
		ocrRecovery: NewOCRRecoveryRunner(repo, channelService, client, nil, repository.NewConfigRepository(env.db))}
	newFile := func() *model.File {
		return &model.File{UserID: env.userID, SessionID: &sessionID, FileName: "synthetic.pdf", FileType: "application/pdf", FileSize: 20}
	}
	sourceDir := filepath.Join(filepolicy.AttachmentOCRRoot, fmt.Sprint(env.userID), "capacity-fixture")
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(sourceDir)) })
	if queued, err := queueMinerUOCR(context.Background(), repo, opts, newFile(), env.userID, sessionID,
		[]byte("synthetic PDF"), "application/pdf", "synthetic.pdf", "winner.pdf", sourceDir, 1); err != nil || queued == nil {
		t.Fatalf("queue first: file=%v err=%v", queued, err)
	}
	if queued, err := queueMinerUOCR(context.Background(), repo, opts, newFile(), env.userID, sessionID,
		[]byte("synthetic PDF"), "application/pdf", "synthetic.pdf", "loser.pdf", sourceDir, 1); !errors.Is(err, repository.ErrSessionFileLimitReached) || queued != nil {
		t.Fatalf("queue over capacity: file=%v err=%v", queued, err)
	}
	if _, err := os.Stat(filepath.Join(sourceDir, "loser.pdf")); !os.IsNotExist(err) {
		t.Fatalf("rejected OCR source remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sourceDir, "winner.pdf")); err != nil {
		t.Fatalf("accepted OCR source missing: %v", err)
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	writeOCRQueueError(ctx, repository.ErrSessionFileLimitReached)
	if recorder.Code != http.StatusConflict || decodeUploadError(t, recorder).Retryable {
		t.Fatalf("invalid OCR limit response: %d", recorder.Code)
	}
}

func TestConcurrentUploadsRespectFinalSessionCapacity(t *testing.T) {
	env := setupTestEnv(t)
	sessionID := createUploadTestSession(t, env)
	if _, err := env.db.Exec(`INSERT INTO system_config (key,value,config_type)
		VALUES ('file_upload_max_session_files','1','number')
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, root := range []string{filepolicy.AttachmentOriginalsRoot, filepolicy.AttachmentExtractedRoot} {
			_ = os.RemoveAll(filepath.Join(root, fmt.Sprint(env.userID)))
		}
	})
	// Both requests must finish the early capacity check before either parser
	// returns. This exercises the real upload seam, not a guessed SQL race.
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		select {
		case <-release:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"text":"synthetic parsed document","parser":"text","token_estimate":4}`))
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("user_id", env.userID) })
	router.POST("/api/v1/files", UploadFileHandler(repository.NewFileRepository(env.db), repository.NewConfigRepository(env.db),
		WithUploadSessionRepo(repository.NewSessionRepository(env.db)),
		WithUploadExtractorClient(extractor.NewSidecarClient(server.URL, time.Second))))
	responses := make(chan *httptest.ResponseRecorder, 2)
	for i := 0; i < 2; i++ {
		req := uploadMultipart(t, "", sessionID, fmt.Sprintf("capacity-%d.docx", i), "application/vnd.openxmlformats-officedocument.wordprocessingml.document", []byte(fmt.Sprintf("synthetic document %d", i)))
		go func() { recorder := httptest.NewRecorder(); router.ServeHTTP(recorder, req); responses <- recorder }()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-arrived:
		case <-time.After(10 * time.Second):
			t.Fatal("both uploads did not reach the parser")
		}
	}
	// Release without closing so deferred cleanup can also release on failure.
	release <- struct{}{}
	release <- struct{}{}
	created, rejected := 0, 0
	for i := 0; i < 2; i++ {
		r := <-responses
		switch r.Code {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			body := decodeUploadError(t, r)
			if body.Code != "session_file_limit_reached" || body.Retryable {
				t.Fatalf("unexpected limit error: %+v", body)
			}
			rejected++
		default:
			t.Fatalf("unexpected upload response: %d %s", r.Code, r.Body.String())
		}
	}
	if created != 1 || rejected != 1 {
		t.Fatalf("created=%d rejected=%d, want one each", created, rejected)
	}
	count, err := repository.NewFileRepository(env.db).CountActiveBySession(env.userID, sessionID)
	if err != nil || count != 1 {
		t.Fatalf("active count=%d err=%v", count, err)
	}
	var paths []string
	_ = filepath.Walk(filepath.Join(filepolicy.AttachmentExtractedRoot, fmt.Sprint(env.userID)), func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			paths = append(paths, path)
		}
		return err
	})
	if len(paths) != 1 {
		t.Fatalf("stored sidecars=%d, rejected upload must remove its bytes", len(paths))
	}
}
