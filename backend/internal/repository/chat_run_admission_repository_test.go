package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/huoguojun123/effchat/internal/model"
)

func setupChatRunAdmission(t *testing.T, name string) (*QuotaRepository, *MessageRepository, int64, *model.Session) {
	t.Helper()
	db := setupTestDB(t)
	userID := createRepositoryTestUser(t, db, name)
	session := &model.Session{UserID: userID, Title: name, ModelID: "m", Provider: "p", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := NewSessionRepository(db).Create(session); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM messages WHERE session_id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM chat_run_reservations WHERE session_id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", userID)
		_ = db.Close()
	})
	return NewQuotaRepository(db), NewMessageRepository(db), userID, session
}

func admissionInput(userID, sessionID int64, runID, operation, hash string, reserveMessage bool) ChatRunReservationInput {
	return ChatRunReservationInput{
		UserID: userID, AuthVersion: 1, SessionID: sessionID, RunID: runID,
		Kind: "chat", Operation: operation, IntentVersion: 1, IntentHash: hash,
		ReserveMessage: reserveMessage, ExpiresAt: time.Now().Add(time.Minute),
	}
}

func TestAdmitChatMessageIsAtomicAndIdempotent(t *testing.T) {
	quotaRepo, _, userID, session := setupChatRunAdmission(t, "atomic_send_admission")
	input := admissionInput(userID, session.ID, fmt.Sprintf("send-%d", time.Now().UnixNano()), "send", "v1:send", true)
	input.AcceptedAt = time.Date(2026, time.July, 23, 12, 34, 56, 123456000, time.UTC)
	input.RuntimeSnapshot = []byte(`{"version":1,"checksum":"sha256:test"}`)
	message := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(fmt.Sprintf(`{"role":"user","content":"hello","metadata":{"run_id":%q}}`, input.RunID))}

	first, err := quotaRepo.AdmitChatMessage(context.Background(), input, message)
	if err != nil {
		t.Fatalf("admit message: %v", err)
	}
	if first.Existing || first.Message == nil || first.Message.ID == 0 || first.Record.UserMessageID != first.Message.ID {
		t.Fatalf("first admission = %+v", first)
	}
	if first.Record.Operation != "send" || first.Record.IntentVersion != 1 || first.Record.IntentHash != "v1:send" {
		t.Fatalf("stored intent = %+v", first.Record)
	}
	if !first.Record.AcceptedAt.Equal(input.AcceptedAt) {
		t.Fatalf("accepted_at = %s, want %s", first.Record.AcceptedAt, input.AcceptedAt)
	}
	var runtimeSnapshot struct {
		Version  int    `json:"version"`
		Checksum string `json:"checksum"`
	}
	if err := json.Unmarshal(first.Record.RuntimeSnapshot, &runtimeSnapshot); err != nil {
		t.Fatalf("decode runtime snapshot: %v", err)
	}
	if runtimeSnapshot.Version != 1 || runtimeSnapshot.Checksum != "sha256:test" {
		t.Fatalf("runtime snapshot = %+v", runtimeSnapshot)
	}
	var attemptCount, selectedCount int
	if err := quotaRepo.db.QueryRow(`
		SELECT COUNT(*), COUNT(*) FILTER (WHERE selected)
		FROM answer_attempts
		WHERE session_id = $1 AND user_message_id = $2 AND run_id = $3
	`, session.ID, first.Message.ID, input.RunID).Scan(&attemptCount, &selectedCount); err != nil {
		t.Fatalf("read initial answer attempt: %v", err)
	}
	if attemptCount != 1 || selectedCount != 1 {
		t.Fatalf("initial attempts = count:%d selected:%d, want 1/1", attemptCount, selectedCount)
	}
	var reserved bool
	if err := quotaRepo.db.QueryRow(`SELECT message_reserved FROM chat_run_reservations WHERE run_id = $1`, input.RunID).Scan(&reserved); err != nil || reserved {
		t.Fatalf("message reservation = %v err=%v", reserved, err)
	}

	second, err := quotaRepo.AdmitChatMessage(context.Background(), input, message)
	if err != nil {
		t.Fatalf("repeat admission: %v", err)
	}
	if !second.Existing || second.Message == nil || second.Message.ID != first.Message.ID {
		t.Fatalf("repeat admission = %+v", second)
	}
	var count int
	if err := quotaRepo.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE session_id = $1`, session.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("message count = %d err=%v", count, err)
	}

	conflict := input
	conflict.IntentHash = "v1:different"
	if _, err := quotaRepo.AdmitChatMessage(context.Background(), conflict, message); !errors.Is(err, ErrChatRunIntentConflict) {
		t.Fatalf("different intent error = %v", err)
	}
}

func TestNormalizeChatRunReservationRejectsOversizedRuntimeSnapshot(t *testing.T) {
	input := ChatRunReservationInput{
		UserID: 1, AuthVersion: 1, SessionID: 2, RunID: "oversized-runtime",
		Kind: "chat", Operation: "send", IntentVersion: 1, IntentHash: "v1:test",
		RuntimeSnapshot: []byte(`{"payload":"` + strings.Repeat("x", 16384) + `"}`),
		ExpiresAt:       time.Now().Add(time.Minute),
	}
	if _, err := normalizeChatRunReservation(input); err == nil {
		t.Fatal("oversized runtime snapshot was accepted")
	}
}

func TestAdmitChatMessageRollsBackReservationWhenMessageFails(t *testing.T) {
	quotaRepo, _, userID, session := setupChatRunAdmission(t, "rollback_send_admission")
	input := admissionInput(userID, session.ID, fmt.Sprintf("send-rollback-%d", time.Now().UnixNano()), "send", "v1:rollback", true)
	message := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{`)}
	if _, err := quotaRepo.AdmitChatMessage(context.Background(), input, message); err == nil {
		t.Fatal("invalid message unexpectedly admitted")
	}
	var count int
	if err := quotaRepo.db.QueryRow(`SELECT COUNT(*) FROM chat_run_reservations WHERE run_id = $1`, input.RunID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rolled back reservation count = %d err=%v", count, err)
	}
	if err := quotaRepo.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE session_id = $1`, session.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rolled back message count = %d err=%v", count, err)
	}
}

func TestAdmitRetryRunPreservesPriorAnswerAndCreatesUnselectedAttempt(t *testing.T) {
	quotaRepo, messageRepo, userID, session := setupChatRunAdmission(t, "atomic_retry_admission")
	create := func(data string) *model.Message {
		t.Helper()
		message := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(data)}
		if err := messageRepo.Create(message); err != nil {
			t.Fatal(err)
		}
		return message
	}
	firstUser := create(`{"role":"user","content":"first"}`)
	firstAssistant := create(`{"role":"assistant","content":"first answer"}`)
	secondUser := create(`{"role":"user","content":"second"}`)
	secondAssistant := create(`{"role":"assistant","content":"second answer"}`)
	trailingTool := create(`{"role":"tool","content":"tail"}`)
	var legacyAttemptID int64
	if err := quotaRepo.db.QueryRow(`
		INSERT INTO answer_attempts (session_id, user_message_id, attempt_number, status, selected)
		VALUES ($1, $2, 1, 'completed', true)
		RETURNING id
	`, session.ID, secondUser.ID).Scan(&legacyAttemptID); err != nil {
		t.Fatalf("create selected legacy attempt: %v", err)
	}
	if _, err := quotaRepo.db.Exec(`UPDATE messages SET answer_attempt_id = $1 WHERE id IN ($2, $3)`, legacyAttemptID, secondAssistant.ID, trailingTool.ID); err != nil {
		t.Fatalf("bind legacy output: %v", err)
	}

	stale := admissionInput(userID, session.ID, fmt.Sprintf("retry-stale-%d", time.Now().UnixNano()), "retry", "v1:stale", false)
	stale.RetryTargetMessageID = firstAssistant.ID
	if _, err := quotaRepo.AdmitRetryChatRun(context.Background(), stale, firstAssistant.ID); !errors.Is(err, ErrRetryTargetStale) {
		t.Fatalf("stale retry error = %v", err)
	}
	var count int
	if err := quotaRepo.db.QueryRow(`SELECT COUNT(*) FROM chat_run_reservations WHERE run_id = $1`, stale.RunID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("stale reservation count = %d err=%v", count, err)
	}
	if err := quotaRepo.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE session_id = $1 AND deleted_at IS NOT NULL`, session.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("stale retry deleted %d messages err=%v", count, err)
	}

	input := admissionInput(userID, session.ID, fmt.Sprintf("retry-%d", time.Now().UnixNano()), "retry", "v1:retry", false)
	input.RetryTargetMessageID = secondAssistant.ID
	admission, err := quotaRepo.AdmitRetryChatRun(context.Background(), input, secondAssistant.ID)
	if err != nil {
		t.Fatalf("admit retry: %v", err)
	}
	if admission.Message == nil || admission.Message.ID != secondUser.ID || admission.Record.UserMessageID != secondUser.ID {
		t.Fatalf("retry admission = %+v", admission)
	}
	if err := quotaRepo.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE id IN ($1, $2) AND deleted_at IS NULL`, secondAssistant.ID, trailingTool.ID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("prior attempt visibility count = %d err=%v", count, err)
	}
	var attemptCount, selectedCount int
	if err := quotaRepo.db.QueryRow(`
		SELECT COUNT(*), COUNT(*) FILTER (WHERE selected)
		FROM answer_attempts
		WHERE user_message_id = $1
	`, secondUser.ID).Scan(&attemptCount, &selectedCount); err != nil {
		t.Fatalf("read attempts: %v", err)
	}
	if attemptCount != 2 || selectedCount != 1 {
		t.Fatalf("attempts = count:%d selected:%d, want 2/1", attemptCount, selectedCount)
	}
	var retryStatus string
	var retrySelected bool
	if err := quotaRepo.db.QueryRow(`
		SELECT status, selected
		FROM answer_attempts
		WHERE run_id = $1
	`, input.RunID).Scan(&retryStatus, &retrySelected); err != nil {
		t.Fatalf("read retry attempt: %v", err)
	}
	if retryStatus != "running" || retrySelected {
		t.Fatalf("retry attempt = status:%s selected:%t, want running/unselected", retryStatus, retrySelected)
	}
	if firstUser.ID == 0 {
		t.Fatal("first user fixture was not persisted")
	}
}

func TestAdmitEditedRetryReplacesOnlyAnUnansweredTailAtomically(t *testing.T) {
	quotaRepo, messageRepo, userID, session := setupChatRunAdmission(t, "atomic_edit_retry_admission")
	ctx := context.Background()
	source := &model.Message{
		SessionID: session.ID, SchemaVersion: "v1",
		MessageData: []byte(`{"role":"user","content":"before","metadata":{"run_id":"old-run"}}`),
	}
	if err := messageRepo.Create(source); err != nil {
		t.Fatal(err)
	}
	var failedAttemptID int64
	if err := quotaRepo.db.QueryRow(`
		INSERT INTO answer_attempts (session_id, user_message_id, run_id, attempt_number, status, selected, completed_at)
		VALUES ($1, $2, $3, 1, 'failed', true, NOW())
		RETURNING id
	`, session.ID, source.ID, "failed-run").Scan(&failedAttemptID); err != nil {
		t.Fatalf("create failed attempt: %v", err)
	}
	errorMessage := &model.Message{
		SessionID: session.ID, SchemaVersion: "v1", AnswerAttemptID: &failedAttemptID,
		MessageData: []byte(`{"role":"assistant","content":"请求失败","metadata":{"ephemeral_error":true}}`),
	}
	if err := messageRepo.Create(errorMessage); err != nil {
		t.Fatal(err)
	}

	runID := fmt.Sprintf("edit-retry-%d", time.Now().UnixNano())
	input := admissionInput(userID, session.ID, runID, "retry", "v1:edit", true)
	input.RetryTargetMessageID = source.ID
	replacement := &model.Message{
		ID: source.ID, SessionID: session.ID, SchemaVersion: "v1", Role: "user",
		MessageData: []byte(fmt.Sprintf(`{"role":"user","content":"after","metadata":{"run_id":%q}}`, runID)),
	}
	first, err := quotaRepo.AdmitEditedRetryChatRun(ctx, input, source.ID, replacement)
	if err != nil {
		t.Fatalf("admit edited retry: %v", err)
	}
	if first.Existing || first.Message == nil || first.Message.ID == source.ID || first.Record.UserMessageID != first.Message.ID {
		t.Fatalf("edited retry admission = %+v", first)
	}

	var oldDeleted, errorDeleted bool
	if err := quotaRepo.db.QueryRow(`
		SELECT source.deleted_at IS NOT NULL, failure.deleted_at IS NOT NULL
		FROM messages source, messages failure
		WHERE source.id = $1 AND failure.id = $2
	`, source.ID, errorMessage.ID).Scan(&oldDeleted, &errorDeleted); err != nil {
		t.Fatalf("read replaced tail: %v", err)
	}
	if !oldDeleted || !errorDeleted {
		t.Fatalf("replaced tail visibility = source:%t error:%t", oldDeleted, errorDeleted)
	}
	var oldSelected, newSelected int
	if err := quotaRepo.db.QueryRow(`SELECT COUNT(*) FROM answer_attempts WHERE user_message_id = $1 AND selected`, source.ID).Scan(&oldSelected); err != nil {
		t.Fatal(err)
	}
	if err := quotaRepo.db.QueryRow(`SELECT COUNT(*) FROM answer_attempts WHERE user_message_id = $1 AND run_id = $2 AND selected`, first.Message.ID, runID).Scan(&newSelected); err != nil {
		t.Fatal(err)
	}
	if oldSelected != 0 || newSelected != 1 {
		t.Fatalf("answer selections = old:%d new:%d", oldSelected, newSelected)
	}
	visible, err := messageRepo.ListBySessionContext(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(visible) != 1 || visible[0].ID != first.Message.ID {
		t.Fatalf("visible messages = %+v", visible)
	}

	second, err := quotaRepo.AdmitEditedRetryChatRun(ctx, input, source.ID, replacement)
	if err != nil {
		t.Fatalf("repeat edited retry admission: %v", err)
	}
	if !second.Existing || second.Message == nil || second.Message.ID != first.Message.ID {
		t.Fatalf("repeat edited retry = %+v", second)
	}
}

func TestAdmitEditedRetryRejectsAnyPriorAnswerOutput(t *testing.T) {
	quotaRepo, messageRepo, userID, session := setupChatRunAdmission(t, "edit_retry_rejects_output")
	source := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"user","content":"before"}`)}
	if err := messageRepo.Create(source); err != nil {
		t.Fatal(err)
	}
	var attemptID int64
	if err := quotaRepo.db.QueryRow(`
		INSERT INTO answer_attempts (session_id, user_message_id, attempt_number, status, selected)
		VALUES ($1, $2, 1, 'completed', false)
		RETURNING id
	`, session.ID, source.ID).Scan(&attemptID); err != nil {
		t.Fatal(err)
	}
	output := &model.Message{
		SessionID: session.ID, SchemaVersion: "v1", AnswerAttemptID: &attemptID,
		MessageData: []byte(`{"role":"assistant","content":"hidden prior answer"}`),
	}
	if err := messageRepo.Create(output); err != nil {
		t.Fatal(err)
	}
	runID := fmt.Sprintf("edit-output-%d", time.Now().UnixNano())
	input := admissionInput(userID, session.ID, runID, "retry", "v1:edit-output", true)
	input.RetryTargetMessageID = source.ID
	replacement := &model.Message{
		ID: source.ID, SessionID: session.ID, SchemaVersion: "v1", Role: "user",
		MessageData: []byte(fmt.Sprintf(`{"role":"user","content":"after","metadata":{"run_id":%q}}`, runID)),
	}
	if _, err := quotaRepo.AdmitEditedRetryChatRun(context.Background(), input, source.ID, replacement); !errors.Is(err, ErrMessageAlreadyAnswered) {
		t.Fatalf("edited retry output error = %v", err)
	}
	var reservations, deleted int
	if err := quotaRepo.db.QueryRow(`SELECT COUNT(*) FROM chat_run_reservations WHERE run_id = $1`, runID).Scan(&reservations); err != nil {
		t.Fatal(err)
	}
	if err := quotaRepo.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE session_id = $1 AND deleted_at IS NOT NULL`, session.ID).Scan(&deleted); err != nil {
		t.Fatal(err)
	}
	if reservations != 0 || deleted != 0 {
		t.Fatalf("rejected edit mutated state: reservations=%d deleted=%d", reservations, deleted)
	}
}

func TestAdmitEditedRetryWaitsForThePreviousRunToFinish(t *testing.T) {
	quotaRepo, messageRepo, userID, session := setupChatRunAdmission(t, "edit_retry_active_run")
	source := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"user","content":"before"}`)}
	if err := messageRepo.Create(source); err != nil {
		t.Fatal(err)
	}
	active := admissionInput(userID, session.ID, fmt.Sprintf("active-%d", time.Now().UnixNano()), "retry", "v1:active", false)
	active.RetryTargetMessageID = source.ID
	if _, err := quotaRepo.ReserveChatRun(context.Background(), active); err != nil {
		t.Fatal(err)
	}
	runID := fmt.Sprintf("edit-active-%d", time.Now().UnixNano())
	input := admissionInput(userID, session.ID, runID, "retry", "v1:edit-active", true)
	input.RetryTargetMessageID = source.ID
	replacement := &model.Message{
		ID: source.ID, SessionID: session.ID, SchemaVersion: "v1", Role: "user",
		MessageData: []byte(fmt.Sprintf(`{"role":"user","content":"after","metadata":{"run_id":%q}}`, runID)),
	}
	if _, err := quotaRepo.AdmitEditedRetryChatRun(context.Background(), input, source.ID, replacement); !errors.Is(err, ErrChatRunActive) {
		t.Fatalf("active run edit error = %v", err)
	}
	var reservations int
	if err := quotaRepo.db.QueryRow(`SELECT COUNT(*) FROM chat_run_reservations WHERE run_id = $1`, runID).Scan(&reservations); err != nil {
		t.Fatal(err)
	}
	if reservations != 0 {
		t.Fatalf("active-run edit left %d reservations", reservations)
	}
}

func TestCompletedRetrySelectsNewAnswerWithoutDeletingPriorAttempt(t *testing.T) {
	quotaRepo, messageRepo, userID, session := setupChatRunAdmission(t, "retry_selects_new_answer")
	ctx := context.Background()
	firstRun := admissionInput(userID, session.ID, fmt.Sprintf("send-%d", time.Now().UnixNano()), "send", "v1:send", true)
	first, err := quotaRepo.AdmitChatMessage(ctx, firstRun, &model.Message{
		SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"user","content":"question"}`),
	})
	if err != nil {
		t.Fatalf("admit first message: %v", err)
	}
	firstOutput := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"assistant","content":"first answer"}`)}
	if _, transitioned, err := messageRepo.CreateBatchAndTransitionActiveRun(ctx, session.ID, userID, firstRun.RunID, []*model.Message{firstOutput}, ChatRunTransitionInput{
		RunID: firstRun.RunID, Status: "completed", ExpiresAt: time.Now().Add(time.Minute),
	}); err != nil || !transitioned {
		t.Fatalf("finish first attempt = transitioned:%t err:%v", transitioned, err)
	}

	retryRun := admissionInput(userID, session.ID, fmt.Sprintf("retry-%d", time.Now().UnixNano()), "retry", "v1:retry", false)
	retryRun.RetryTargetMessageID = firstOutput.ID
	if _, err := quotaRepo.AdmitRetryChatRun(ctx, retryRun, firstOutput.ID); err != nil {
		t.Fatalf("admit retry: %v", err)
	}
	retryOutput := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"assistant","content":"second answer"}`)}
	if _, transitioned, err := messageRepo.CreateBatchAndTransitionActiveRun(ctx, session.ID, userID, retryRun.RunID, []*model.Message{retryOutput}, ChatRunTransitionInput{
		RunID: retryRun.RunID, Status: "completed", ExpiresAt: time.Now().Add(time.Minute),
	}); err != nil || !transitioned {
		t.Fatalf("finish retry attempt = transitioned:%t err:%v", transitioned, err)
	}

	var selectedContent string
	if err := quotaRepo.db.QueryRow(`
		SELECT message_data->>'content'
		FROM messages m
		JOIN answer_attempts a ON a.id = m.answer_attempt_id
		WHERE a.user_message_id = $1 AND m.role = 'assistant' AND a.selected
	`, first.Message.ID).Scan(&selectedContent); err != nil {
		t.Fatalf("read selected retry output: %v", err)
	}
	if selectedContent != "second answer" {
		t.Fatalf("selected output = %q, want retry answer", selectedContent)
	}
	var allOutputs int
	if err := quotaRepo.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE session_id = $1 AND role = 'assistant'`, session.ID).Scan(&allOutputs); err != nil || allOutputs != 2 {
		t.Fatalf("stored answer count = %d err=%v, want 2", allOutputs, err)
	}
	visible, err := messageRepo.ListBySessionContext(ctx, session.ID)
	if err != nil {
		t.Fatalf("list selected messages: %v", err)
	}
	if len(visible) != 2 || visible[1].ID != retryOutput.ID {
		t.Fatalf("visible messages = %+v, want user + selected retry output", visible)
	}
}

func TestFailedRetryKeepsSelectedAnswer(t *testing.T) {
	quotaRepo, messageRepo, userID, session := setupChatRunAdmission(t, "retry_keeps_answer")
	ctx := context.Background()
	firstRun := admissionInput(userID, session.ID, fmt.Sprintf("send-%d", time.Now().UnixNano()), "send", "v1:send", true)
	first, err := quotaRepo.AdmitChatMessage(ctx, firstRun, &model.Message{
		SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"user","content":"question"}`),
	})
	if err != nil {
		t.Fatalf("admit first message: %v", err)
	}
	firstOutput := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"assistant","content":"stable answer"}`)}
	if _, transitioned, err := messageRepo.CreateBatchAndTransitionActiveRun(ctx, session.ID, userID, firstRun.RunID, []*model.Message{firstOutput}, ChatRunTransitionInput{
		RunID: firstRun.RunID, Status: "completed", ExpiresAt: time.Now().Add(time.Minute),
	}); err != nil || !transitioned {
		t.Fatalf("finish first attempt = transitioned:%t err:%v", transitioned, err)
	}

	retryRun := admissionInput(userID, session.ID, fmt.Sprintf("retry-%d", time.Now().UnixNano()), "retry", "v1:retry", false)
	retryRun.RetryTargetMessageID = firstOutput.ID
	if _, err := quotaRepo.AdmitRetryChatRun(ctx, retryRun, firstOutput.ID); err != nil {
		t.Fatalf("admit retry: %v", err)
	}
	if _, transitioned, err := quotaRepo.TransitionChatRun(ctx, ChatRunTransitionInput{
		RunID: retryRun.RunID, Status: "failed", PublicErrorCode: "upstream_failed", PublicErrorMessage: "retry failed", ExpiresAt: time.Now().Add(time.Minute),
	}); err != nil || !transitioned {
		t.Fatalf("fail retry attempt = transitioned:%t err:%v", transitioned, err)
	}

	var selectedContent, retryStatus string
	if err := quotaRepo.db.QueryRow(`
		SELECT m.message_data->>'content'
		FROM messages m
		JOIN answer_attempts a ON a.id = m.answer_attempt_id
		WHERE a.user_message_id = $1 AND m.role = 'assistant' AND a.selected
	`, first.Message.ID).Scan(&selectedContent); err != nil {
		t.Fatalf("read selected output: %v", err)
	}
	if err := quotaRepo.db.QueryRow(`SELECT status FROM answer_attempts WHERE run_id = $1`, retryRun.RunID).Scan(&retryStatus); err != nil {
		t.Fatalf("read failed retry: %v", err)
	}
	if selectedContent != "stable answer" || retryStatus != AnswerAttemptStatusFailed {
		t.Fatalf("failed retry changed selection: selected=%q status=%q", selectedContent, retryStatus)
	}
}

func TestReconcileRunningChatRunsProducesRetryableTerminalFacts(t *testing.T) {
	quotaRepo, _, userID, session := setupChatRunAdmission(t, "restart_reconcile")
	input := admissionInput(userID, session.ID, fmt.Sprintf("restart-%d", time.Now().UnixNano()), "send", "v1:restart", true)
	if _, err := quotaRepo.ReserveChatRun(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	count, err := quotaRepo.ReconcileRunningChatRuns(context.Background())
	if err != nil || count < 1 {
		t.Fatalf("reconcile count = %d err=%v", count, err)
	}
	record, err := quotaRepo.GetChatRun(context.Background(), input.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != "failed" || record.PublicErrorCode != "server_restarted" || record.TerminalAt == nil || len(record.TerminalEvent) == 0 {
		t.Fatalf("reconciled record = %+v", record)
	}
}
