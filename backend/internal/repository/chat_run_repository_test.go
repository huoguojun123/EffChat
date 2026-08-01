package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
)

func TestChatRunTransitionIsDurableAndImmutable(t *testing.T) {
	db := setupTestDB(t)
	t.Cleanup(func() { _ = db.Close() })
	userID := createRepositoryTestUser(t, db, "durable_chat_run")
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM users WHERE id = $1", userID) })
	session := &model.Session{
		UserID: userID, Title: "run ledger", ModelID: "m", Provider: "p", MessageFormat: "v1", Metadata: []byte(`{}`),
	}
	if err := NewSessionRepository(db).Create(session); err != nil {
		t.Fatal(err)
	}

	repo := NewQuotaRepository(db)
	runID := fmt.Sprintf("durable-run-%d", time.Now().UnixNano())
	if _, err := repo.ReserveChatRun(context.Background(), ChatRunReservationInput{
		UserID: userID, AuthVersion: 1, SessionID: session.ID, RunID: runID, Kind: "chat",
		ExpiresAt: time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatalf("reserve run: %v", err)
	}
	userMessage := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"user","content":"test"}`)}
	if err := NewMessageRepository(db).Create(userMessage); err != nil {
		t.Fatalf("create user message: %v", err)
	}
	if bound, err := repo.BindChatRunUserMessage(context.Background(), runID, userMessage.ID); err != nil || !bound {
		t.Fatalf("bind user message = %v, %v", bound, err)
	}
	terminalMessage := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"assistant","content":"done"}`)}
	event, _ := json.Marshal(map[string]interface{}{
		"event": "message_complete",
		"data":  map[string]interface{}{"message_id": 0, "finish_reason": "stop"},
	})
	completed, transitioned, err := NewMessageRepository(db).CreateBatchAndTransitionActiveRun(context.Background(), session.ID, userID, runID, []*model.Message{terminalMessage}, ChatRunTransitionInput{
		RunID: runID, Status: "completed",
		TerminalEvent: event, ExpiresAt: time.Now().Add(time.Minute),
	})
	if err != nil || !transitioned {
		t.Fatalf("complete run = transitioned:%v record:%+v err:%v", transitioned, completed, err)
	}
	if completed.Status != "completed" || completed.UserMessageID != userMessage.ID || completed.TerminalMessageID != terminalMessage.ID || completed.TerminalAt == nil {
		t.Fatalf("completed record = %+v", completed)
	}

	existing, transitioned, err := repo.TransitionChatRun(context.Background(), ChatRunTransitionInput{
		RunID: runID, Status: "failed", PublicErrorCode: "late_failure",
		TerminalEvent: json.RawMessage(`{"event":"error","data":{"code":"late_failure"}}`),
		ExpiresAt:     time.Now().Add(2 * time.Minute),
	})
	if err != nil || transitioned {
		t.Fatalf("late transition = transitioned:%v record:%+v err:%v", transitioned, existing, err)
	}
	var gotEvent, wantEvent interface{}
	if err := json.Unmarshal(existing.TerminalEvent, &gotEvent); err != nil {
		t.Fatalf("decode stored terminal event: %v", err)
	}
	event = terminalEventWithMessageID(event, terminalMessage.ID)
	if err := json.Unmarshal(event, &wantEvent); err != nil {
		t.Fatalf("decode expected terminal event: %v", err)
	}
	if existing.Status != "completed" || existing.PublicErrorCode != "" || !reflect.DeepEqual(gotEvent, wantEvent) {
		t.Fatalf("late transition changed terminal fact: %+v", existing)
	}
	if bound, err := repo.BindChatRunUserMessage(context.Background(), runID, userMessage.ID); err != nil || bound {
		t.Fatalf("terminal bind = %v, %v", bound, err)
	}
	lateMessage := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"assistant","content":"late"}`)}
	if err := NewMessageRepository(db).CreateBatchForActiveRun(context.Background(), session.ID, userID, runID, []*model.Message{lateMessage}); !errors.Is(err, ErrChatRunTerminal) {
		t.Fatalf("late run message error = %v", err)
	}
	lateSummary := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"user","content":"late summary"}`)}
	lateRecord, lateTransitioned, err := NewMessageRepository(db).PersistCheckpointAndTransitionActiveRun(context.Background(), session.ID, userID, runID, lateSummary, terminalMessage.ID, ChatRunTransitionInput{
		RunID: runID, Status: "completed", TerminalEvent: json.RawMessage(`{"event":"compaction_complete","data":{"compacted":true}}`), ExpiresAt: time.Now().Add(time.Minute),
	}, nil)
	if err != nil || lateTransitioned || lateRecord.Status != "completed" || lateRecord.TerminalMessageID != terminalMessage.ID {
		t.Fatalf("late compression checkpoint recovery = transitioned:%v record:%+v err:%v", lateTransitioned, lateRecord, err)
	}
	if lateSummary.ID != 0 {
		t.Fatalf("late compression summary was persisted with id %d", lateSummary.ID)
	}
}

func TestChatRunTerminalRecoveryReturnsCanonicalRecordWithoutDuplicates(t *testing.T) {
	quotaRepo, messageRepo, userID, session := setupChatRunAdmission(t, "chat_terminal_recovery")
	runID := fmt.Sprintf("chat-terminal-recovery-%d", time.Now().UnixNano())
	input := admissionInput(userID, session.ID, runID, "send", "v1:terminal-recovery", true)
	userMessage := &model.Message{
		SessionID:     session.ID,
		SchemaVersion: "v1",
		MessageData:   []byte(fmt.Sprintf(`{"role":"user","content":"persist once","metadata":{"run_id":%q}}`, runID)),
	}
	admission, err := quotaRepo.AdmitChatMessage(context.Background(), input, userMessage)
	if err != nil {
		t.Fatalf("admit chat message: %v", err)
	}
	terminalMessage := &model.Message{
		SessionID:     session.ID,
		SchemaVersion: "v1",
		MessageData:   []byte(fmt.Sprintf(`{"role":"assistant","content":"durable answer","metadata":{"run_id":%q,"run_sequence":0}}`, runID)),
	}
	terminalEvent := json.RawMessage(`{"event":"message_complete","data":{"message_id":0,"finish_reason":"stop"}}`)
	completed, transitioned, err := messageRepo.CreateBatchAndTransitionActiveRun(context.Background(), session.ID, userID, runID, []*model.Message{terminalMessage}, ChatRunTransitionInput{
		RunID: runID, Status: "completed", TerminalEvent: terminalEvent, ExpiresAt: time.Now().Add(time.Minute),
	})
	if err != nil || !transitioned || completed.Status != "completed" {
		t.Fatalf("complete chat run = transitioned:%v record:%+v err:%v", transitioned, completed, err)
	}

	var beforeMessageCount, beforeAttemptCount, beforeSelectedCount int
	var beforeAttemptID int64
	var beforeAttemptStatus string
	var beforeAttemptCompletedAt time.Time
	var beforeSelectionRevision int64
	if err := quotaRepo.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE session_id = $1`, session.ID).Scan(&beforeMessageCount); err != nil {
		t.Fatalf("count messages before recovery: %v", err)
	}
	if err := quotaRepo.db.QueryRow(`
		SELECT COUNT(*), COUNT(*) FILTER (WHERE selected)
		FROM answer_attempts
		WHERE run_id = $1
	`, runID).Scan(&beforeAttemptCount, &beforeSelectedCount); err != nil {
		t.Fatalf("count attempts before recovery: %v", err)
	}
	if err := quotaRepo.db.QueryRow(`
		SELECT id, status, completed_at
		FROM answer_attempts
		WHERE run_id = $1
	`, runID).Scan(&beforeAttemptID, &beforeAttemptStatus, &beforeAttemptCompletedAt); err != nil {
		t.Fatalf("read attempt before recovery: %v", err)
	}
	if err := quotaRepo.db.QueryRow(`SELECT answer_selection_revision FROM sessions WHERE id = $1`, session.ID).Scan(&beforeSelectionRevision); err != nil {
		t.Fatalf("read selection revision before recovery: %v", err)
	}

	recoveryMessage := &model.Message{
		SessionID:     session.ID,
		SchemaVersion: "v1",
		MessageData:   []byte(fmt.Sprintf(`{"role":"assistant","content":"must not persist","metadata":{"run_id":%q,"run_sequence":0}}`, runID)),
	}
	recovered, transitioned, err := messageRepo.CreateBatchAndTransitionActiveRun(context.Background(), session.ID, userID, runID, []*model.Message{recoveryMessage}, ChatRunTransitionInput{
		RunID: runID, Status: "failed", PublicErrorCode: "late_failure", PublicErrorMessage: "must not replace completed output",
		TerminalEvent: json.RawMessage(`{"event":"error","data":{"code":"late_failure"}}`), ExpiresAt: time.Now().Add(2 * time.Minute),
	})
	if err != nil || transitioned {
		t.Fatalf("recover terminal chat run = transitioned:%v record:%+v err:%v", transitioned, recovered, err)
	}
	if recovered.Status != completed.Status || recovered.TerminalMessageID != completed.TerminalMessageID || !reflect.DeepEqual(recovered.TerminalEvent, completed.TerminalEvent) {
		t.Fatalf("recovered terminal record = %+v, want canonical %+v", recovered, completed)
	}
	if recoveryMessage.ID != 0 {
		t.Fatalf("recovery message was persisted with id %d", recoveryMessage.ID)
	}

	var afterMessageCount, afterAttemptCount, afterSelectedCount int
	var afterAttemptID int64
	var afterAttemptStatus string
	var afterAttemptCompletedAt time.Time
	var afterSelectionRevision int64
	if err := quotaRepo.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE session_id = $1`, session.ID).Scan(&afterMessageCount); err != nil {
		t.Fatalf("count messages after recovery: %v", err)
	}
	if err := quotaRepo.db.QueryRow(`
		SELECT COUNT(*), COUNT(*) FILTER (WHERE selected)
		FROM answer_attempts
		WHERE run_id = $1
	`, runID).Scan(&afterAttemptCount, &afterSelectedCount); err != nil {
		t.Fatalf("count attempts after recovery: %v", err)
	}
	if err := quotaRepo.db.QueryRow(`
		SELECT id, status, completed_at
		FROM answer_attempts
		WHERE run_id = $1
	`, runID).Scan(&afterAttemptID, &afterAttemptStatus, &afterAttemptCompletedAt); err != nil {
		t.Fatalf("read attempt after recovery: %v", err)
	}
	if err := quotaRepo.db.QueryRow(`SELECT answer_selection_revision FROM sessions WHERE id = $1`, session.ID).Scan(&afterSelectionRevision); err != nil {
		t.Fatalf("read selection revision after recovery: %v", err)
	}
	if afterMessageCount != beforeMessageCount || afterAttemptCount != beforeAttemptCount || afterSelectedCount != beforeSelectedCount || afterAttemptID != beforeAttemptID || afterAttemptStatus != beforeAttemptStatus || !afterAttemptCompletedAt.Equal(beforeAttemptCompletedAt) || afterSelectionRevision != beforeSelectionRevision {
		t.Fatalf("recovery changed persisted chat state: messages %d/%d attempts %d/%d selected %d/%d attempt %d/%d status %q/%q completed %s/%s revision %d/%d", afterMessageCount, beforeMessageCount, afterAttemptCount, beforeAttemptCount, afterSelectedCount, beforeSelectedCount, afterAttemptID, beforeAttemptID, afterAttemptStatus, beforeAttemptStatus, afterAttemptCompletedAt, beforeAttemptCompletedAt, afterSelectionRevision, beforeSelectionRevision)
	}
	if admission.Record.UserMessageID == 0 {
		t.Fatal("admission did not bind the user message")
	}
}

func TestCompressionCheckpointAndRunTerminalCommitAtomically(t *testing.T) {
	db := setupTestDB(t)
	t.Cleanup(func() { _ = db.Close() })
	userID := createRepositoryTestUser(t, db, "atomic_compaction")
	session := &model.Session{UserID: userID, Title: "atomic compaction", ModelID: "m", Provider: "p", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := NewSessionRepository(db).Create(session); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM messages WHERE session_id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM chat_run_reservations WHERE session_id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", userID)
	})
	message := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"user","content":"history"}`)}
	if err := NewMessageRepository(db).Create(message); err != nil {
		t.Fatal(err)
	}
	runID := fmt.Sprintf("atomic-compaction-%d", time.Now().UnixNano())
	quotaRepo := NewQuotaRepository(db)
	if _, err := quotaRepo.ReserveChatRun(context.Background(), ChatRunReservationInput{
		UserID: userID, AuthVersion: 1, SessionID: session.ID, RunID: runID, Kind: "compaction", ExpiresAt: time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	messageRepo := NewMessageRepository(db)
	rolledBackSummary := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"user","content":"rolled back"}`)}
	if _, _, err := messageRepo.PersistCheckpointAndTransitionActiveRun(context.Background(), session.ID, userID, runID, rolledBackSummary, message.ID+1, ChatRunTransitionInput{
		RunID: runID, Status: "invalid", ExpiresAt: time.Now().Add(time.Minute),
	}, nil); err == nil {
		t.Fatal("invalid terminal status committed checkpoint")
	}
	var messageCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM messages WHERE session_id = $1", session.ID).Scan(&messageCount); err != nil || messageCount != 1 {
		t.Fatalf("rolled back message count = %d err=%v", messageCount, err)
	}
	if run, err := quotaRepo.GetChatRun(context.Background(), runID); err != nil || run.Status != "running" {
		t.Fatalf("rolled back run = %+v err=%v", run, err)
	}

	summary := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"user","content":"summary"}`)}
	record, transitioned, err := messageRepo.PersistCheckpointAndTransitionActiveRun(context.Background(), session.ID, userID, runID, summary, message.ID+1, ChatRunTransitionInput{
		RunID: runID, Status: "completed", TerminalEvent: json.RawMessage(`{"event":"compaction_complete","data":{"compacted":true}}`), ExpiresAt: time.Now().Add(time.Minute),
	}, nil)
	if err != nil || !transitioned || record.Status != "completed" {
		t.Fatalf("atomic compaction = transitioned:%v record:%+v err:%v", transitioned, record, err)
	}
	var compressed bool
	if err := db.QueryRow("SELECT compressed_at IS NOT NULL FROM messages WHERE id = $1", message.ID).Scan(&compressed); err != nil || !compressed {
		t.Fatalf("compressed history = %v err=%v", compressed, err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM messages WHERE session_id = $1", session.ID).Scan(&messageCount); err != nil || messageCount != 2 {
		t.Fatalf("committed message count = %d err=%v", messageCount, err)
	}

	recoverySummary := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"user","content":"must not persist"}`)}
	recovered, transitioned, err := messageRepo.PersistCheckpointAndTransitionActiveRun(context.Background(), session.ID, userID, runID, recoverySummary, message.ID+1, ChatRunTransitionInput{
		RunID: runID, Status: "failed", PublicErrorCode: "late_failure", PublicErrorMessage: "must not replace completed checkpoint",
		TerminalEvent: json.RawMessage(`{"event":"error","data":{"code":"late_failure"}}`), ExpiresAt: time.Now().Add(2 * time.Minute),
	}, nil)
	if err != nil || transitioned {
		t.Fatalf("recover terminal compaction = transitioned:%v record:%+v err:%v", transitioned, recovered, err)
	}
	if recovered.Status != record.Status || recovered.TerminalMessageID != record.TerminalMessageID || !reflect.DeepEqual(recovered.TerminalEvent, record.TerminalEvent) {
		t.Fatalf("recovered checkpoint terminal = %+v, want canonical %+v", recovered, record)
	}
	if recoverySummary.ID != 0 {
		t.Fatalf("recovery summary was persisted with id %d", recoverySummary.ID)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM messages WHERE session_id = $1", session.ID).Scan(&messageCount); err != nil || messageCount != 2 {
		t.Fatalf("recovery message count = %d err=%v", messageCount, err)
	}
	var compressionSummaryID int64
	if err := db.QueryRow("SELECT compression_summary_id FROM messages WHERE id = $1", message.ID).Scan(&compressionSummaryID); err != nil || compressionSummaryID != summary.ID {
		t.Fatalf("recovery compression summary id = %d want %d err=%v", compressionSummaryID, summary.ID, err)
	}
	if _, err := db.Exec("UPDATE sessions SET answer_selection_revision = answer_selection_revision + 1 WHERE id = $1", session.ID); err != nil {
		t.Fatalf("advance answer selection revision: %v", err)
	}
	staleRevision := int64(0)
	recovered, transitioned, err = messageRepo.PersistCheckpointAndTransitionActiveRun(context.Background(), session.ID, userID, runID, recoverySummary, message.ID+1, ChatRunTransitionInput{
		RunID: runID, Status: "completed", TerminalEvent: json.RawMessage(`{"event":"compaction_complete","data":{"compacted":true}}`), ExpiresAt: time.Now().Add(3 * time.Minute),
	}, &staleRevision)
	if err != nil || transitioned || recovered.Status != record.Status || !reflect.DeepEqual(recovered.TerminalEvent, record.TerminalEvent) {
		t.Fatalf("recover terminal compaction after selection change = transitioned:%v record:%+v err:%v", transitioned, recovered, err)
	}
	var attemptCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM answer_attempts WHERE run_id = $1", runID).Scan(&attemptCount); err != nil || attemptCount != 0 {
		t.Fatalf("compaction recovery attempts = %d err=%v", attemptCount, err)
	}
}

func TestCompressionCheckpointRejectsStaleAnswerSelectionRevision(t *testing.T) {
	db := setupTestDB(t)
	t.Cleanup(func() { _ = db.Close() })

	userID := createRepositoryTestUser(t, db, "stale_compaction")
	session := &model.Session{UserID: userID, Title: "stale compaction", ModelID: "m", Provider: "p", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := NewSessionRepository(db).Create(session); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", userID)
	})

	history := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"user","content":"history"}`)}
	if err := NewMessageRepository(db).Create(history); err != nil {
		t.Fatal(err)
	}
	runID := fmt.Sprintf("stale-compaction-%d", time.Now().UnixNano())
	quotaRepo := NewQuotaRepository(db)
	if _, err := quotaRepo.ReserveChatRun(context.Background(), ChatRunReservationInput{
		UserID: userID, AuthVersion: 1, SessionID: session.ID, RunID: runID, Kind: "compaction", ExpiresAt: time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE sessions SET answer_selection_revision = 2 WHERE id = $1", session.ID); err != nil {
		t.Fatal(err)
	}

	expectedRevision := int64(1)
	summary := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"user","content":"stale summary"}`)}
	_, transitioned, err := NewMessageRepository(db).PersistCheckpointAndTransitionActiveRun(context.Background(), session.ID, userID, runID, summary, history.ID+1, ChatRunTransitionInput{
		RunID: runID, Status: "completed", TerminalEvent: json.RawMessage(`{"event":"compaction_complete","data":{"compacted":true}}`), ExpiresAt: time.Now().Add(time.Minute),
	}, &expectedRevision)
	if !errors.Is(err, ErrAnswerSelectionRevisionConflict) || transitioned {
		t.Fatalf("stale compaction = transitioned:%v err:%v", transitioned, err)
	}
	if summary.ID != 0 {
		t.Fatalf("stale summary was persisted with id %d", summary.ID)
	}
	var compressed bool
	if err := db.QueryRow("SELECT compressed_at IS NOT NULL FROM messages WHERE id = $1", history.ID).Scan(&compressed); err != nil || compressed {
		t.Fatalf("history was compressed=%v err=%v", compressed, err)
	}
	run, err := quotaRepo.GetChatRun(context.Background(), runID)
	if err != nil || run.Status != "running" {
		t.Fatalf("stale checkpoint terminalized run = %+v err=%v", run, err)
	}
}

func TestSessionDeleteTransitionsRunningRunsAtomically(t *testing.T) {
	db := setupTestDB(t)
	t.Cleanup(func() { _ = db.Close() })
	userID := createRepositoryTestUser(t, db, "delete_running_run")
	session := &model.Session{UserID: userID, Title: "delete running run", ModelID: "m", Provider: "p", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := NewSessionRepository(db).Create(session); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM chat_run_reservations WHERE session_id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", userID)
	})

	runID := fmt.Sprintf("delete-running-%d", time.Now().UnixNano())
	quotaRepo := NewQuotaRepository(db)
	if _, err := quotaRepo.ReserveChatRun(context.Background(), ChatRunReservationInput{
		UserID: userID, AuthVersion: 1, SessionID: session.ID, RunID: runID, Kind: "chat", ReserveMessage: true,
		ExpiresAt: time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	if err := NewSessionRepository(db).Delete(session.ID, userID); err != nil {
		t.Fatal(err)
	}
	record, err := quotaRepo.GetChatRun(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != "canceled" || record.CancelCause != "session_deleted" || record.PublicErrorCode != "session_deleted" || record.TerminalAt == nil {
		t.Fatalf("deleted session run = %+v", record)
	}
	if record.PublicErrorMessage != "会话已删除" || len(record.TerminalEvent) == 0 {
		t.Fatalf("deleted session terminal payload = %+v", record)
	}
	if _, err := quotaRepo.ReserveChatRun(context.Background(), ChatRunReservationInput{
		UserID: userID, AuthVersion: 1, SessionID: session.ID, RunID: fmt.Sprintf("after-delete-%d", time.Now().UnixNano()), Kind: "chat",
		ExpiresAt: time.Now().Add(time.Minute),
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reservation after session deletion error = %v, want ErrNotFound", err)
	}
}

func TestAccountChangesTransitionRunningRunsAtomically(t *testing.T) {
	db := setupTestDB(t)
	t.Cleanup(func() { _ = db.Close() })
	userID := createRepositoryTestUser(t, db, "change_running_run")
	session := &model.Session{UserID: userID, Title: "change running run", ModelID: "m", Provider: "p", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := NewSessionRepository(db).Create(session); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM chat_run_reservations WHERE session_id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", userID)
	})

	quotaRepo := NewQuotaRepository(db)
	reserve := func(runID string, authVersion int) error {
		t.Helper()
		_, err := quotaRepo.ReserveChatRun(context.Background(), ChatRunReservationInput{
			UserID: userID, AuthVersion: authVersion, SessionID: session.ID, RunID: runID, Kind: "chat", ReserveMessage: true,
			ExpiresAt: time.Now().Add(time.Minute),
		})
		return err
	}
	assertCanceled := func(runID string) {
		t.Helper()
		record, err := quotaRepo.GetChatRun(context.Background(), runID)
		if err != nil {
			t.Fatal(err)
		}
		if record.Status != "canceled" || record.CancelCause != "account_changed" || record.PublicErrorCode != "account_changed" || record.TerminalAt == nil {
			t.Fatalf("account-changed run = %+v", record)
		}
	}

	adminRunID := fmt.Sprintf("admin-change-%d", time.Now().UnixNano())
	if err := reserve(adminRunID, 1); err != nil {
		t.Fatal(err)
	}
	user, err := NewUserRepository(db).GetByIDIncludeInactive(userID)
	if err != nil {
		t.Fatal(err)
	}
	user.IsActive = false
	if err := NewUserRepository(db).UpdateAdminFields(user); err != nil {
		t.Fatal(err)
	}
	assertCanceled(adminRunID)
	if err := reserve("stale-after-disable", 1); !errors.Is(err, ErrAccountStateChanged) {
		t.Fatalf("stale reservation after disable error = %v", err)
	}

	user, err = NewUserRepository(db).GetByIDIncludeInactive(userID)
	if err != nil {
		t.Fatal(err)
	}
	user.IsActive = true
	if err := NewUserRepository(db).UpdateAdminFields(user); err != nil {
		t.Fatal(err)
	}
	user, err = NewUserRepository(db).GetByIDIncludeInactive(userID)
	if err != nil {
		t.Fatal(err)
	}

	passwordRunID := fmt.Sprintf("password-change-%d", time.Now().UnixNano())
	passwordVersion := user.AuthVersion
	if err := reserve(passwordRunID, passwordVersion); err != nil {
		t.Fatal(err)
	}
	if err := NewUserRepository(db).UpdatePassword(userID, "updated-password-hash"); err != nil {
		t.Fatal(err)
	}
	assertCanceled(passwordRunID)
	if err := reserve("stale-after-password", passwordVersion); !errors.Is(err, ErrAccountStateChanged) {
		t.Fatalf("stale reservation after password change error = %v", err)
	}

	user, err = NewUserRepository(db).GetByIDIncludeInactive(userID)
	if err != nil {
		t.Fatal(err)
	}
	roleVersion := user.AuthVersion
	roleRunID := fmt.Sprintf("role-change-%d", time.Now().UnixNano())
	if err := reserve(roleRunID, roleVersion); err != nil {
		t.Fatal(err)
	}
	user.Role = "admin"
	if err := NewUserRepository(db).UpdateAdminFields(user); err != nil {
		t.Fatal(err)
	}
	assertCanceled(roleRunID)
	if err := reserve("stale-after-role", roleVersion); !errors.Is(err, ErrAccountStateChanged) {
		t.Fatalf("stale reservation after role change error = %v", err)
	}
}
