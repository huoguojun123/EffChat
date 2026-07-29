package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
)

func TestFileRepository_GetReadableFileForAgentRequiresSessionBoundFile(t *testing.T) {
	db := setupTestDB(t)
	t.Cleanup(func() { _ = db.Close() })

	userRepo := NewUserRepository(db)
	user := &model.User{
		Username:     fmt.Sprintf("file_read_%d", time.Now().UnixNano()),
		PasswordHash: "x",
		Role:         "user",
		IsActive:     true,
		Permissions:  []byte(`{}`),
		Preferences:  []byte(`{}`),
	}
	if err := userRepo.Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	other := &model.User{
		Username:     fmt.Sprintf("file_read_other_%d", time.Now().UnixNano()),
		PasswordHash: "x",
		Role:         "user",
		IsActive:     true,
		Permissions:  []byte(`{}`),
		Preferences:  []byte(`{}`),
	}
	if err := userRepo.Create(other); err != nil {
		t.Fatalf("create other user: %v", err)
	}
	t.Cleanup(func() {
		db.Exec("DELETE FROM users WHERE id IN ($1, $2)", user.ID, other.ID)
	})

	sessionRepo := NewSessionRepository(db)
	session := &model.Session{
		UserID: user.ID, Title: "文件读取授权", ModelID: "gpt-4o",
		Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`),
	}
	if err := sessionRepo.Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	otherSession := &model.Session{
		UserID: user.ID, Title: "另一个会话", ModelID: "gpt-4o",
		Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`),
	}
	if err := sessionRepo.Create(otherSession); err != nil {
		t.Fatalf("create other session: %v", err)
	}
	t.Cleanup(func() {
		db.Exec("DELETE FROM messages WHERE session_id IN ($1, $2)", session.ID, otherSession.ID)
		db.Exec("DELETE FROM files WHERE user_id = $1", user.ID)
		db.Exec("DELETE FROM sessions WHERE id IN ($1, $2)", session.ID, otherSession.ID)
	})

	fileRepo := NewFileRepository(db)
	sessionID := session.ID
	file := &model.File{
		UserID:        user.ID,
		SessionID:     &sessionID,
		FileName:      "brief.md",
		FilePath:      fmt.Sprintf("/tmp/brief_%d.md", time.Now().UnixNano()),
		FileType:      "text/markdown",
		FileSize:      32,
		ExtractStatus: "ready",
		TokenEstimate: 3,
	}
	extractedPath := file.FilePath + ".txt"
	file.ExtractedTextPath = &extractedPath
	if err := fileRepo.Create(file); err != nil {
		t.Fatalf("create file: %v", err)
	}

	if _, err := fileRepo.GetReadableFileForAgent(user.ID, session.ID, file.ID); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("staged file should not be readable before message commitment, err=%v", err)
	}
	messageData := []byte(fmt.Sprintf(`{"role":"user","attachments":[{"file_id":%d}]}`, file.ID))
	if err := NewMessageRepository(db).CreateForActiveSession(context.Background(), session.ID, user.ID, &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: messageData}); err != nil {
		t.Fatalf("commit user attachment message: %v", err)
	}

	got, err := fileRepo.GetReadableFileForAgent(user.ID, session.ID, file.ID)
	if err != nil {
		t.Fatalf("read formal session-bound file: %v", err)
	}
	if got.ExtractedTextPath == nil || *got.ExtractedTextPath != extractedPath {
		t.Fatalf("session-bound extracted_text_path = %v, want %s", got.ExtractedTextPath, extractedPath)
	}

	if _, err := fileRepo.GetReadableFileForAgent(user.ID, otherSession.ID, file.ID); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("other session should not read file, err=%v", err)
	}
	if _, err := fileRepo.GetReadableFileForAgent(other.ID, session.ID, file.ID); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("other user should not read file, err=%v", err)
	}
}

func TestFileRepository_HistoricalCrossSessionReferenceDoesNotGrantAccess(t *testing.T) {
	db := setupTestDB(t)
	t.Cleanup(func() { _ = db.Close() })

	user := &model.User{Username: fmt.Sprintf("file_history_%d", time.Now().UnixNano()), PasswordHash: "x", Role: "user", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`)}
	if err := NewUserRepository(db).Create(user); err != nil {
		t.Fatal(err)
	}
	filesSession := &model.Session{UserID: user.ID, Title: "files", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`)}
	historySession := &model.Session{UserID: user.ID, Title: "history", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`)}
	sessions := NewSessionRepository(db)
	if err := sessions.Create(filesSession); err != nil {
		t.Fatal(err)
	}
	if err := sessions.Create(historySession); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM messages WHERE session_id IN ($1, $2)", filesSession.ID, historySession.ID)
		_, _ = db.Exec("DELETE FROM files WHERE user_id = $1", user.ID)
		_, _ = db.Exec("DELETE FROM sessions WHERE user_id = $1", user.ID)
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", user.ID)
	})

	file := &model.File{UserID: user.ID, SessionID: &filesSession.ID, FileName: "private.txt", FilePath: "/tmp/private.txt", FileType: "text/plain", FileSize: 1, ExtractStatus: "ready"}
	files := NewFileRepository(db)
	if err := files.Create(file); err != nil {
		t.Fatal(err)
	}
	messageData := []byte(fmt.Sprintf(`{"role":"user","attachments":[{"file_id":%d}]}`, file.ID))
	if err := NewMessageRepository(db).Create(&model.Message{SessionID: historySession.ID, SchemaVersion: "v1", MessageData: messageData}); err != nil {
		t.Fatal(err)
	}
	if _, err := files.GetReadableFileForAgent(user.ID, historySession.ID, file.ID); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("historical cross-session reference should not grant access, err=%v", err)
	}
	listed, err := files.ListReadableFilesForAgent(user.ID, historySession.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 0 {
		t.Fatalf("historical cross-session reference leaked files: %+v", listed)
	}
}

func TestFileRepository_ClaimFilesForStorageCleanupRespectsAttachmentOwnership(t *testing.T) {
	db := setupTestDB(t)
	t.Cleanup(func() { _ = db.Close() })

	userRepo := NewUserRepository(db)
	user := &model.User{
		Username:     fmt.Sprintf("file_orphan_%d", time.Now().UnixNano()),
		PasswordHash: "x",
		Role:         "user",
		IsActive:     true,
		Permissions:  []byte(`{}`),
		Preferences:  []byte(`{}`),
	}
	if err := userRepo.Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	sessionRepo := NewSessionRepository(db)
	session := &model.Session{
		UserID: user.ID, Title: "孤儿文件清理", ModelID: "gpt-4o-mini",
		Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`),
	}
	if err := sessionRepo.Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	deletedSession := &model.Session{
		UserID: user.ID, Title: "已删除会话", ModelID: "gpt-4o-mini",
		Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`),
	}
	if err := sessionRepo.Create(deletedSession); err != nil {
		t.Fatalf("create deleted session: %v", err)
	}
	t.Cleanup(func() {
		db.Exec("DELETE FROM messages WHERE session_id = $1", session.ID)
		db.Exec("DELETE FROM files WHERE user_id = $1", user.ID)
		db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
		db.Exec("DELETE FROM sessions WHERE id = $1", deletedSession.ID)
		db.Exec("DELETE FROM users WHERE id = $1", user.ID)
	})

	fileRepo := NewFileRepository(db)
	sessionID := session.ID
	referenced := &model.File{
		UserID:        user.ID,
		SessionID:     &sessionID,
		FileName:      "referenced.txt",
		FilePath:      fmt.Sprintf("/tmp/referenced_%d.txt", time.Now().UnixNano()),
		FileType:      "text/plain",
		FileSize:      10,
		ExtractStatus: "ready",
	}
	sessionBound := &model.File{
		UserID:        user.ID,
		SessionID:     &sessionID,
		FileName:      "session-bound.txt",
		FilePath:      fmt.Sprintf("/tmp/session_bound_%d.txt", time.Now().UnixNano()),
		FileType:      "text/plain",
		FileSize:      10,
		ExtractStatus: "ready",
	}
	deletedSessionID := deletedSession.ID
	deletedSessionFile := &model.File{
		UserID:        user.ID,
		SessionID:     &deletedSessionID,
		FileName:      "deleted-session.txt",
		FilePath:      fmt.Sprintf("/tmp/deleted_session_%d.txt", time.Now().UnixNano()),
		FileType:      "text/plain",
		FileSize:      10,
		ExtractStatus: "ready",
	}
	pendingOCR := &model.File{
		UserID: user.ID, SessionID: &sessionID, FileName: "pending.pdf", FilePath: fmt.Sprintf("/tmp/pending_%d.pdf", time.Now().UnixNano()),
		FileType: "application/pdf", FileSize: 10, ExtractStatus: "ocr_running",
	}
	deletedSessionOCR := &model.File{
		UserID: user.ID, SessionID: &deletedSessionID, FileName: "deleted-session-pending.pdf", FilePath: fmt.Sprintf("/tmp/deleted_session_pending_%d.pdf", time.Now().UnixNano()),
		FileType: "application/pdf", FileSize: 10, ExtractStatus: "ocr_running",
	}
	ocrSourcePath := deletedSessionOCR.FilePath + ".source"
	deletedSessionOCR.OCRSourcePath = &ocrSourcePath
	if err := fileRepo.Create(referenced); err != nil {
		t.Fatalf("create referenced file: %v", err)
	}
	if err := fileRepo.Create(sessionBound); err != nil {
		t.Fatalf("create session-bound file: %v", err)
	}
	if err := fileRepo.Create(deletedSessionFile); err != nil {
		t.Fatalf("create deleted-session file: %v", err)
	}
	if err := fileRepo.Create(pendingOCR); err != nil {
		t.Fatalf("create pending OCR file: %v", err)
	}
	if err := fileRepo.Create(deletedSessionOCR); err != nil {
		t.Fatalf("create deleted-session OCR file: %v", err)
	}
	if _, err := db.Exec("UPDATE files SET created_at = NOW() - INTERVAL '48 hours' WHERE id IN ($1, $2, $3, $4, $5)", referenced.ID, sessionBound.ID, deletedSessionFile.ID, pendingOCR.ID, deletedSessionOCR.ID); err != nil {
		t.Fatalf("age files: %v", err)
	}

	messageData, _ := json.Marshal(map[string]interface{}{
		"role":    "user",
		"content": "引用文件",
		"attachments": []map[string]interface{}{
			{"file_id": referenced.ID, "filename": referenced.FileName, "file_type": referenced.FileType},
		},
	})
	if err := NewMessageRepository(db).Create(&model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: messageData}); err != nil {
		t.Fatalf("create message: %v", err)
	}
	deletedSessionMessageData, _ := json.Marshal(map[string]interface{}{
		"role":    "user",
		"content": "删除前的正式附件",
		"attachments": []map[string]interface{}{
			{"file_id": deletedSessionFile.ID, "filename": deletedSessionFile.FileName, "file_type": deletedSessionFile.FileType},
			{"file_id": deletedSessionOCR.ID, "filename": deletedSessionOCR.FileName, "file_type": deletedSessionOCR.FileType},
		},
	})
	if err := NewMessageRepository(db).Create(&model.Message{SessionID: deletedSession.ID, SchemaVersion: "v1", MessageData: deletedSessionMessageData}); err != nil {
		t.Fatalf("create deleted-session message: %v", err)
	}
	if _, err := db.Exec("UPDATE files SET status = 'formal' WHERE id IN ($1, $2, $3)", referenced.ID, deletedSessionFile.ID, deletedSessionOCR.ID); err != nil {
		t.Fatalf("mark formal attachments: %v", err)
	}
	if err := sessionRepo.Delete(deletedSession.ID, user.ID); err != nil {
		t.Fatalf("delete session: %v", err)
	}

	now := time.Now()
	claims, err := fileRepo.ClaimFilesForStorageCleanup(context.Background(), now.Add(-24*time.Hour), now, time.Minute, 10)
	if err != nil {
		t.Fatalf("claim stale unreferenced files: %v", err)
	}
	claimedIDs := map[int64]bool{}
	for _, claim := range claims {
		claimedIDs[claim.File.ID] = true
	}
	if len(claims) != 1 || !claimedIDs[sessionBound.ID] {
		t.Fatalf("claims = %+v, want only staged file %d before deleted-session retention expires", claims, sessionBound.ID)
	}
	expiredOCR, err := fileRepo.ExpireStaleOCROriginals(now.Add(-24*time.Hour), now, 10)
	if err != nil {
		t.Fatalf("expire fresh deleted-session OCR: %v", err)
	}
	if len(expiredOCR) != 0 {
		t.Fatalf("freshly deleted session must retain OCR source, expired=%+v", expiredOCR)
	}

	if _, err := db.Exec("UPDATE sessions SET deleted_at = NOW() - INTERVAL '25 hours' WHERE id = $1", deletedSession.ID); err != nil {
		t.Fatalf("age deleted session: %v", err)
	}
	claims, err = fileRepo.ClaimFilesForStorageCleanup(context.Background(), now.Add(-24*time.Hour), now, time.Minute, 10)
	if err != nil {
		t.Fatalf("claim expired deleted-session files: %v", err)
	}
	claimedIDs = map[int64]bool{}
	claimTokens := map[int64]string{}
	for _, claim := range claims {
		claimedIDs[claim.File.ID] = true
		claimTokens[claim.File.ID] = claim.Token
	}
	if len(claims) != 1 || !claimedIDs[deletedSessionFile.ID] || claimedIDs[deletedSessionOCR.ID] {
		t.Fatalf("claims = %+v, want completed deleted-session attachment only", claims)
	}
	expiredOCR, err = fileRepo.ExpireStaleOCROriginals(now.Add(-24*time.Hour), now, 10)
	if err != nil {
		t.Fatalf("expire stale deleted-session OCR: %v", err)
	}
	if len(expiredOCR) != 1 || expiredOCR[0].ID != deletedSessionOCR.ID {
		t.Fatalf("expired OCR = %+v, want deleted-session OCR %d", expiredOCR, deletedSessionOCR.ID)
	}

	if _, err := fileRepo.GetByID(referenced.ID, user.ID); err != nil {
		t.Fatalf("referenced file should stay formal: %v", err)
	}
	if _, err := fileRepo.GetByID(pendingOCR.ID, user.ID); err != nil {
		t.Fatalf("staged OCR file should remain available: %v", err)
	}
	if _, err := fileRepo.GetByID(deletedSessionOCR.ID, user.ID); err != nil {
		t.Fatalf("deleted-session OCR should remain formal until cleanup claim: %v", err)
	}
	claims, err = fileRepo.ClaimFilesForStorageCleanup(context.Background(), now.Add(-24*time.Hour), now, time.Minute, 10)
	if err != nil {
		t.Fatalf("claim completed deleted-session OCR: %v", err)
	}
	claimedIDs = map[int64]bool{}
	for _, claim := range claims {
		claimedIDs[claim.File.ID] = true
	}
	if len(claims) != 1 || !claimedIDs[deletedSessionOCR.ID] {
		t.Fatalf("expired deleted-session OCR should be reclaimable, claims=%+v", claims)
	}
	if err := fileRepo.FinalizeFileStorageRemoval(context.Background(), deletedSessionFile.ID, claimTokens[deletedSessionFile.ID]); err != nil {
		t.Fatalf("finalize candidate: %v", err)
	}
	if _, err := fileRepo.GetByID(deletedSessionFile.ID, user.ID); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("cleaned file should no longer be active, err=%v", err)
	}
	skipped, err := fileRepo.CountStaleReferencedFiles(time.Now().Add(-24 * time.Hour))
	if err != nil {
		t.Fatalf("count session-bound files: %v", err)
	}
	if skipped != 1 {
		t.Fatalf("skipped referenced files = %d, want 1", skipped)
	}
}

func TestFileRepository_ListReferencedBySession(t *testing.T) {
	db := setupTestDB(t)
	t.Cleanup(func() { _ = db.Close() })
	user := &model.User{Username: fmt.Sprintf("file_referenced_%d", time.Now().UnixNano()), PasswordHash: "x", Role: "user", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`)}
	if err := NewUserRepository(db).Create(user); err != nil {
		t.Fatal(err)
	}
	session := &model.Session{UserID: user.ID, Title: "referenced", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := NewSessionRepository(db).Create(session); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM messages WHERE session_id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM files WHERE user_id = $1", user.ID)
		_, _ = db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", user.ID)
	})
	repo := NewFileRepository(db)
	sessionID := session.ID
	referenced := &model.File{UserID: user.ID, SessionID: &sessionID, FileName: "sent.txt", FilePath: fmt.Sprintf("/tmp/sent_%d.txt", time.Now().UnixNano()), FileType: "text/plain", FileSize: 1, ExtractStatus: "ready"}
	staged := &model.File{UserID: user.ID, SessionID: &sessionID, FileName: "staged.txt", FilePath: fmt.Sprintf("/tmp/staged_%d.txt", time.Now().UnixNano()), FileType: "text/plain", FileSize: 1, ExtractStatus: "ready"}
	if err := repo.Create(referenced); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(staged); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]interface{}{"role": "user", "attachments": []map[string]int64{{"file_id": referenced.ID}}})
	if err := NewMessageRepository(db).Create(&model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: data}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE files SET status = 'formal' WHERE id = $1", referenced.ID); err != nil {
		t.Fatal(err)
	}
	files, err := repo.ListReferencedBySession(user.ID, session.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].ID != referenced.ID {
		t.Fatalf("referenced files = %+v, want %d", files, referenced.ID)
	}
}

func TestFileRepository_CleanupClaimRetriesRequestedDeletion(t *testing.T) {
	db := setupTestDB(t)
	t.Cleanup(func() { _ = db.Close() })
	user := &model.User{Username: fmt.Sprintf("file_delete_retry_%d", time.Now().UnixNano()), PasswordHash: "x", Role: "user", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`)}
	if err := NewUserRepository(db).Create(user); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM files WHERE user_id = $1", user.ID)
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", user.ID)
	})
	file := &model.File{UserID: user.ID, FileName: "retry.txt", FilePath: fmt.Sprintf("./storage/attachments/extracted/%d/retry.txt", user.ID), FileType: "text/plain", FileSize: 1, ExtractStatus: "ready"}
	repo := NewFileRepository(db)
	if err := repo.Create(file); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := repo.RequestDeletion(context.Background(), file.ID, user.ID, now, now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	claims, err := repo.ClaimFilesForStorageCleanup(context.Background(), now.Add(-24*time.Hour), now.Add(23*time.Hour), time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 0 {
		t.Fatalf("deletion claim should wait for retention, claims=%+v", claims)
	}
	claims, err = repo.ClaimFilesForStorageCleanup(context.Background(), now.Add(-24*time.Hour), now.Add(24*time.Hour), time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || claims[0].File.ID != file.ID {
		t.Fatalf("requested deletion was not reclaimable: %#v", claims)
	}
}

func TestFileRepository_RestartAndExpireOCRSource(t *testing.T) {
	db := setupTestDB(t)
	t.Cleanup(func() { _ = db.Close() })
	user := &model.User{Username: fmt.Sprintf("ocr_lifecycle_%d", time.Now().UnixNano()), PasswordHash: "x", Role: "user", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`)}
	if err := NewUserRepository(db).Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	session := &model.Session{UserID: user.ID, Title: "OCR lifecycle", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := NewSessionRepository(db).Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() {
		db.Exec("DELETE FROM files WHERE user_id = $1", user.ID)
		db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
		db.Exec("DELETE FROM users WHERE id = $1", user.ID)
	})
	provider, source := "mineru", fmt.Sprintf("./storage/attachments/ocr-staging/%d/source.pdf", time.Now().UnixNano())
	sessionID := session.ID
	file := &model.File{UserID: user.ID, SessionID: &sessionID, FileName: "scan.pdf", FilePath: source + ".txt", FileType: "application/pdf", FileSize: 64, ExtractStatus: "failed", OCRProvider: &provider, OCRSourcePath: &source, OCRAttempts: 5}
	repo := NewFileRepository(db)
	if err := repo.Create(file); err != nil {
		t.Fatalf("create OCR file: %v", err)
	}
	restarted, err := repo.RestartOCR(file.ID, user.ID, time.Now(), time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("restart OCR: %v", err)
	}
	if restarted.ExtractStatus != "ocr_pending" || restarted.OCRTaskID != nil || restarted.OCRAttempts != 0 {
		t.Fatalf("restart state = %+v", restarted)
	}
	if _, err := db.Exec("UPDATE files SET created_at = NOW() - INTERVAL '25 hours' WHERE id = $1", file.ID); err != nil {
		t.Fatalf("age OCR file: %v", err)
	}
	expired, err := repo.ExpireStaleOCROriginals(time.Now().Add(-24*time.Hour), time.Now(), 10)
	if err != nil {
		t.Fatalf("expire OCR source: %v", err)
	}
	if len(expired) != 1 || expired[0].OCRSourcePath == nil || *expired[0].OCRSourcePath != source {
		t.Fatalf("expired = %+v", expired)
	}
	got, err := repo.GetByID(file.ID, user.ID)
	if err != nil {
		t.Fatalf("get expired OCR file: %v", err)
	}
	if got.ExtractStatus != "failed" || got.OCRSourcePath == nil || *got.OCRSourcePath != source || got.OCRErrorType == nil || *got.OCRErrorType != "ocr_source_expired" {
		t.Fatalf("expired state = %+v", got)
	}
	if err := repo.ClearOCRSourcePath(file.ID, user.ID, source); err != nil {
		t.Fatalf("clear expired OCR source: %v", err)
	}
	got, err = repo.GetByID(file.ID, user.ID)
	if err != nil {
		t.Fatalf("get cleared expired OCR file: %v", err)
	}
	if got.OCRSourcePath != nil {
		t.Fatalf("cleared OCR source path = %v, want nil", got.OCRSourcePath)
	}
	completed, err := repo.CompleteOCR(file.ID, user.ID, file.FilePath, 12)
	if err != nil {
		t.Fatalf("complete expired OCR: %v", err)
	}
	if completed {
		t.Fatal("late OCR result must not revive an expired file")
	}
	got, err = repo.GetByID(file.ID, user.ID)
	if err != nil {
		t.Fatalf("get file after late OCR result: %v", err)
	}
	if got.ExtractStatus != "failed" || got.OCRSourcePath != nil {
		t.Fatalf("late OCR result changed expired state = %+v", got)
	}
}

func TestFileRepository_ExpireStaleOCROriginalsKeepsReferencedFiles(t *testing.T) {
	db := setupTestDB(t)
	t.Cleanup(func() { _ = db.Close() })
	user := &model.User{Username: fmt.Sprintf("ocr_referenced_%d", time.Now().UnixNano()), PasswordHash: "x", Role: "user", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`)}
	if err := NewUserRepository(db).Create(user); err != nil {
		t.Fatal(err)
	}
	session := &model.Session{UserID: user.ID, Title: "Referenced OCR", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := NewSessionRepository(db).Create(session); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM messages WHERE session_id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM files WHERE user_id = $1", user.ID)
		_, _ = db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", user.ID)
	})
	sessionID := session.ID
	source := fmt.Sprintf("./storage/attachments/ocr-staging/%d/referenced.pdf", user.ID)
	file := &model.File{UserID: user.ID, SessionID: &sessionID, FileName: "referenced.pdf", FilePath: source + ".txt", FileType: "application/pdf", FileSize: 10, ExtractStatus: "ready", OCRSourcePath: &source}
	repo := NewFileRepository(db)
	if err := repo.Create(file); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]interface{}{"role": "user", "attachments": []map[string]int64{{"file_id": file.ID}}})
	if err := NewMessageRepository(db).CreateForActiveSession(context.Background(), session.ID, user.ID, &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: data}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE files SET created_at = NOW() - INTERVAL '25 hours' WHERE id = $1", file.ID); err != nil {
		t.Fatal(err)
	}
	expired, err := repo.ExpireStaleOCROriginals(time.Now().Add(-24*time.Hour), time.Now(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 0 {
		t.Fatalf("referenced OCR source should remain, expired=%+v", expired)
	}
}

func TestFileRepository_OCRSubmissionReceiptPreventsAutomaticResubmit(t *testing.T) {
	db := setupTestDB(t)
	t.Cleanup(func() { _ = db.Close() })
	user := &model.User{Username: fmt.Sprintf("ocr_receipt_%d", time.Now().UnixNano()), PasswordHash: "x", Role: "user", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`)}
	if err := NewUserRepository(db).Create(user); err != nil {
		t.Fatal(err)
	}
	session := &model.Session{UserID: user.ID, Title: "OCR receipt", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := NewSessionRepository(db).Create(session); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM files WHERE user_id = $1", user.ID)
		_, _ = db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", user.ID)
	})
	provider, source := "mineru", "./storage/attachments/ocr-staging/receipt.pdf"
	file := &model.File{UserID: user.ID, SessionID: &session.ID, FileName: "receipt.pdf", FilePath: "./storage/attachments/extracted/receipt.txt", FileType: "application/pdf", FileSize: 1, ExtractStatus: "ocr_pending", OCRProvider: &provider, OCRSourcePath: &source}
	repo := NewFileRepository(db)
	if err := repo.Create(file); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE files SET ocr_lease_until = NOW() + INTERVAL '1 minute' WHERE id = $1", file.ID); err != nil {
		t.Fatal(err)
	}
	marked, err := repo.MarkOCRSubmissionStarted(file.ID, user.ID)
	if err != nil || !marked {
		t.Fatalf("mark submission started = %v, %v", marked, err)
	}
	claimed, err := repo.ClaimRecoverableOCRTasks("mineru", time.Now(), time.Minute, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 0 {
		t.Fatalf("submission receipt was claimed again: %+v", claimed)
	}
	if _, err := db.Exec("UPDATE files SET ocr_lease_until = NOW() - INTERVAL '1 minute' WHERE id = $1", file.ID); err != nil {
		t.Fatal(err)
	}
	failed, err := repo.FailStaleOCRSubmissions(time.Now())
	if err != nil || failed != 1 {
		t.Fatalf("fail stale submissions = %d, %v", failed, err)
	}
	got, err := repo.GetByID(file.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ExtractStatus != "failed" || got.OCRErrorType == nil || *got.OCRErrorType != "ocr_submission_unknown" {
		t.Fatalf("submission state = %+v", got)
	}
}
