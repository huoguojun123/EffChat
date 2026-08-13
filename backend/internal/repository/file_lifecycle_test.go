package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
)

func setupFileLifecycle(t *testing.T) (*sql.DB, *FileRepository, *MessageRepository, int64, *model.Session) {
	t.Helper()
	db := setupTestDB(t)
	userID := createRepositoryTestUser(t, db, "file_lifecycle")
	session := &model.Session{
		UserID: userID, Title: "file lifecycle", ModelID: "m", Provider: "p", MessageFormat: "v1", Metadata: []byte(`{}`),
	}
	if err := NewSessionRepository(db).Create(session); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM messages WHERE session_id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM files WHERE session_id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", userID)
		_ = db.Close()
	})
	return db, NewFileRepository(db), NewMessageRepository(db), userID, session
}

func createLifecycleFile(t *testing.T, files *FileRepository, userID, sessionID int64, name string) *model.File {
	t.Helper()
	file := &model.File{
		UserID: userID, SessionID: &sessionID, FileName: name,
		FilePath: fmt.Sprintf("/tmp/%s_%d.txt", name, time.Now().UnixNano()),
		FileType: "text/plain", FileSize: 1, ExtractStatus: "ready",
	}
	if err := files.Create(file); err != nil {
		t.Fatal(err)
	}
	return file
}

func attachmentMessage(sessionID, fileID int64) *model.Message {
	return &model.Message{
		SessionID:     sessionID,
		SchemaVersion: "v1",
		MessageData:   []byte(fmt.Sprintf(`{"role":"user","content":"check this","attachments":[{"file_id":%d,"filename":"note.txt","file_type":"text/plain","size":1}]}`, fileID)),
	}
}

func TestAttachmentIDsFromMessageDataSupportsLegacyStringIDs(t *testing.T) {
	ids, err := attachmentIDsFromMessageData([]byte(`{"attachments":[{"file_id":"42"},{"file_id":42},{"file_id":"007"}]}`))
	if err != nil {
		t.Fatalf("parse legacy attachment ids: %v", err)
	}
	want := []int64{42, 7}
	if len(ids) != len(want) {
		t.Fatalf("attachment ids = %v, want %v", ids, want)
	}
	for index, id := range ids {
		if id != want[index] {
			t.Fatalf("attachment ids = %v, want %v", ids, want)
		}
	}
}

func TestMessageCommitClaimsStagedAttachmentsAsFormal(t *testing.T) {
	db, files, messages, userID, session := setupFileLifecycle(t)
	file := createLifecycleFile(t, files, userID, session.ID, "formal")

	if err := messages.CreateForActiveSession(context.Background(), session.ID, userID, attachmentMessage(session.ID, file.ID)); err != nil {
		t.Fatalf("commit message with staged attachment: %v", err)
	}

	var status string
	if err := db.QueryRow("SELECT status FROM files WHERE id = $1", file.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != FileStatusFormal {
		t.Fatalf("file status = %q, want %q", status, FileStatusFormal)
	}
}

func TestAssistantAttachmentMetadataDoesNotClaimStagedFile(t *testing.T) {
	db, files, messages, userID, session := setupFileLifecycle(t)
	file := createLifecycleFile(t, files, userID, session.ID, "assistant_metadata")
	assistant := &model.Message{
		SessionID:     session.ID,
		SchemaVersion: "v1",
		MessageData:   []byte(fmt.Sprintf(`{"role":"assistant","content":"artifact","attachments":[{"file_id":%d}]}`, file.ID)),
	}

	if err := messages.CreateForActiveSession(context.Background(), session.ID, userID, assistant); err != nil {
		t.Fatalf("commit assistant metadata: %v", err)
	}

	var status string
	if err := db.QueryRow("SELECT status FROM files WHERE id = $1", file.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != FileStatusStaged {
		t.Fatalf("assistant metadata claimed attachment status = %q, want %q", status, FileStatusStaged)
	}
}

func TestMessageCommitRejectsCleanupClaimedAttachmentWithoutWritingTurn(t *testing.T) {
	db, files, messages, userID, session := setupFileLifecycle(t)
	file := createLifecycleFile(t, files, userID, session.ID, "claimed")
	now := time.Now()
	if err := files.RequestDeletion(context.Background(), file.ID, userID, now, now.Add(24*time.Hour)); err != nil {
		t.Fatalf("request deletion: %v", err)
	}

	err := messages.CreateForActiveSession(context.Background(), session.ID, userID, attachmentMessage(session.ID, file.ID))
	if !errors.Is(err, ErrAttachmentUnavailable) {
		t.Fatalf("commit error = %v, want attachment unavailable", err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM messages WHERE session_id = $1", session.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("message count = %d, want 0 after failed attachment claim", count)
	}
}

func TestCleanupClaimCannotRaceIntoFormalMessage(t *testing.T) {
	db, files, messages, userID, session := setupFileLifecycle(t)
	file := createLifecycleFile(t, files, userID, session.ID, "cleanup")
	now := time.Now()
	if _, err := db.Exec("UPDATE files SET created_at = $1 WHERE id = $2", now.Add(-25*time.Hour), file.ID); err != nil {
		t.Fatal(err)
	}

	claims, err := files.ClaimFilesForStorageCleanup(context.Background(), now.Add(-24*time.Hour), now, time.Minute, 10)
	if err != nil {
		t.Fatalf("claim stale file: %v", err)
	}
	if len(claims) != 1 || claims[0].File.ID != file.ID {
		t.Fatalf("cleanup claims = %+v, want file %d", claims, file.ID)
	}
	if err := messages.CreateForActiveSession(context.Background(), session.ID, userID, attachmentMessage(session.ID, file.ID)); !errors.Is(err, ErrAttachmentUnavailable) {
		t.Fatalf("message after cleanup claim = %v, want attachment unavailable", err)
	}
	if err := files.FinalizeFileStorageRemoval(context.Background(), file.ID, claims[0].Token); err != nil {
		t.Fatalf("finalize storage removal: %v", err)
	}

	var status string
	if err := db.QueryRow("SELECT status FROM files WHERE id = $1", file.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != FileStatusStorageRemoved {
		t.Fatalf("file status = %q, want %q", status, FileStatusStorageRemoved)
	}
}

func TestCleanupClaimAndMessageCommitAreMutuallyExclusive(t *testing.T) {
	db, files, messages, userID, session := setupFileLifecycle(t)
	file := createLifecycleFile(t, files, userID, session.ID, "concurrent_cleanup")
	now := time.Now()
	if _, err := db.Exec("UPDATE files SET created_at = $1 WHERE id = $2", now.Add(-25*time.Hour), file.ID); err != nil {
		t.Fatal(err)
	}

	type claimResult struct {
		claims []FileCleanupClaim
		err    error
	}
	start := make(chan struct{})
	claimsDone := make(chan claimResult, 1)
	messageDone := make(chan error, 1)
	go func() {
		<-start
		claims, err := files.ClaimFilesForStorageCleanup(context.Background(), now.Add(-24*time.Hour), now, time.Minute, 10)
		claimsDone <- claimResult{claims: claims, err: err}
	}()
	go func() {
		<-start
		messageDone <- messages.CreateForActiveSession(context.Background(), session.ID, userID, attachmentMessage(session.ID, file.ID))
	}()
	close(start)

	claim := <-claimsDone
	messageErr := <-messageDone
	if claim.err != nil {
		t.Fatalf("claim stale attachment: %v", claim.err)
	}

	var status string
	var messageCount int
	if err := db.QueryRow("SELECT status FROM files WHERE id = $1", file.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM messages WHERE session_id = $1", session.ID).Scan(&messageCount); err != nil {
		t.Fatal(err)
	}

	switch len(claim.claims) {
	case 0:
		if messageErr != nil {
			t.Fatalf("message commit should win when cleanup has no claim: %v", messageErr)
		}
		if status != FileStatusFormal || messageCount != 1 {
			t.Fatalf("message winner left status=%q messages=%d, want formal/1", status, messageCount)
		}
	case 1:
		if claim.claims[0].File.ID != file.ID {
			t.Fatalf("cleanup claimed file %d, want %d", claim.claims[0].File.ID, file.ID)
		}
		if !errors.Is(messageErr, ErrAttachmentUnavailable) {
			t.Fatalf("message should reject claimed attachment, got %v", messageErr)
		}
		if status != FileStatusCleanupClaimed || messageCount != 0 {
			t.Fatalf("cleanup winner left status=%q messages=%d, want cleanup_claimed/0", status, messageCount)
		}
	default:
		t.Fatalf("cleanup claimed %d files, want at most one", len(claim.claims))
	}
}

func TestMessageCommitDoesNotPartiallyClaimAttachments(t *testing.T) {
	db, files, messages, userID, session := setupFileLifecycle(t)
	available := createLifecycleFile(t, files, userID, session.ID, "available")
	unavailable := createLifecycleFile(t, files, userID, session.ID, "unavailable")
	now := time.Now()
	if err := files.RequestDeletion(context.Background(), unavailable.ID, userID, now, now.Add(24*time.Hour)); err != nil {
		t.Fatalf("request deletion: %v", err)
	}

	message := attachmentMessage(session.ID, available.ID)
	message.MessageData = []byte(fmt.Sprintf(`{"role":"user","content":"check both","attachments":[{"file_id":%d},{"file_id":%d}]}`, available.ID, unavailable.ID))
	if err := messages.CreateForActiveSession(context.Background(), session.ID, userID, message); !errors.Is(err, ErrAttachmentUnavailable) {
		t.Fatalf("commit with unavailable attachment = %v, want attachment unavailable", err)
	}

	var status string
	if err := db.QueryRow("SELECT status FROM files WHERE id = $1", available.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != FileStatusStaged {
		t.Fatalf("available attachment status = %q, want %q after rollback", status, FileStatusStaged)
	}
}

func TestDeletingFormalAttachmentTombstonesHistoryAndRejectsRetry(t *testing.T) {
	db, files, messages, userID, session := setupFileLifecycle(t)
	file := createLifecycleFile(t, files, userID, session.ID, "retry")
	userMessage := attachmentMessage(session.ID, file.ID)
	if err := messages.CreateForActiveSession(context.Background(), session.ID, userID, userMessage); err != nil {
		t.Fatalf("commit user message: %v", err)
	}
	assistant := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"assistant","content":"answer"}`)}
	if err := messages.CreateForActiveSession(context.Background(), session.ID, userID, assistant); err != nil {
		t.Fatalf("commit assistant message: %v", err)
	}

	now := time.Now()
	if err := files.RequestDeletion(context.Background(), file.ID, userID, now, now.Add(24*time.Hour)); err != nil {
		t.Fatalf("request deletion: %v", err)
	}

	var messageData []byte
	if err := db.QueryRow("SELECT message_data FROM messages WHERE id = $1", userMessage.ID).Scan(&messageData); err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Attachments []struct {
			Unavailable bool `json:"unavailable"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal(messageData, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Attachments) != 1 || !decoded.Attachments[0].Unavailable {
		t.Fatalf("attachment tombstone = %+v, want unavailable", decoded.Attachments)
	}

	if _, err := messages.PrepareRetryForActiveSession(context.Background(), session.ID, userID, assistant.ID); !errors.Is(err, ErrAttachmentUnavailable) {
		t.Fatalf("retry deleted attachment = %v, want attachment unavailable", err)
	}
	var activeMessages int
	if err := db.QueryRow("SELECT COUNT(*) FROM messages WHERE session_id = $1 AND deleted_at IS NULL", session.ID).Scan(&activeMessages); err != nil {
		t.Fatal(err)
	}
	if activeMessages != 2 {
		t.Fatalf("active messages = %d, want 2 after rejected retry", activeMessages)
	}
}

func TestDeletingFormalAttachmentDoesNotDeadlockWithRetry(t *testing.T) {
	db, files, messages, userID, session := setupFileLifecycle(t)
	file := createLifecycleFile(t, files, userID, session.ID, "delete_retry_lock_order")
	userMessage := attachmentMessage(session.ID, file.ID)
	if err := messages.CreateForActiveSession(context.Background(), session.ID, userID, userMessage); err != nil {
		t.Fatalf("commit user message: %v", err)
	}
	assistant := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"assistant","content":"answer"}`)}
	if err := messages.CreateForActiveSession(context.Background(), session.ID, userID, assistant); err != nil {
		t.Fatalf("commit assistant message: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	retryTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer retryTx.Rollback()
	rows, err := retryTx.QueryContext(ctx, `
		SELECT id
		FROM messages
		WHERE session_id = $1 AND deleted_at IS NULL
		ORDER BY id ASC
		FOR UPDATE
	`, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatal(err)
		}
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}

	deleteDone := make(chan error, 1)
	go func() {
		deleteDone <- files.RequestDeletion(ctx, file.ID, userID, time.Now(), time.Now().Add(24*time.Hour))
	}()
	waitForBlockedAttachmentTombstone(t, db, ctx)

	if err := ensureFormalMessageAttachmentsAvailableTx(ctx, retryTx, session.ID, userID, userMessage); err != nil {
		_ = retryTx.Rollback()
		deleteErr := <-deleteDone
		t.Fatalf("retry attachment check deadlocked: retry=%v delete=%v", err, deleteErr)
	}
	if err := retryTx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-deleteDone; err != nil {
		t.Fatalf("delete after retry: %v", err)
	}
}

func waitForBlockedAttachmentTombstone(t *testing.T, db *sql.DB, ctx context.Context) {
	t.Helper()
	for {
		var waiting bool
		err := db.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM pg_stat_activity
				WHERE datname = current_database()
				  AND wait_event_type = 'Lock'
				  AND query LIKE '%messages%'
			)
		`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("attachment tombstone did not block: %v", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}
