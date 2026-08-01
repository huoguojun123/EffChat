package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
)

type ChatRunAdmission struct {
	Record   ChatRunRecord
	Message  *model.Message
	Existing bool
}

func reserveChatRunInTx(ctx context.Context, tx *sql.Tx, input ChatRunReservationInput) (ChatRunReservation, error) {
	input, err := normalizeChatRunReservation(input)
	if err != nil {
		return ChatRunReservation{}, err
	}
	if err := lockChatRunUser(ctx, tx, input.UserID, input.AuthVersion); err != nil {
		return ChatRunReservation{}, err
	}
	if err := lockActiveSession(ctx, tx, input.SessionID, input.UserID); err != nil {
		return ChatRunReservation{}, err
	}

	existing, err := scanChatRun(tx.QueryRowContext(ctx, chatRunSelect+` WHERE run_id = $1 FOR UPDATE`, input.RunID))
	if err == nil {
		if !chatRunReservationMatches(existing, input) {
			return ChatRunReservation{}, ErrChatRunIntentConflict
		}
		return ChatRunReservation{RunID: input.RunID, Existing: true, Record: existing}, nil
	}
	if err != sql.ErrNoRows {
		return ChatRunReservation{}, fmt.Errorf("load chat run reservation: %w", err)
	}

	limits, err := limitsForUserInTx(ctx, tx, input.UserID)
	if err != nil {
		return ChatRunReservation{}, err
	}
	var activeRuns, pendingMessages int64
	if err := tx.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE message_reserved)
		FROM chat_run_reservations
		WHERE user_id = $1
		  AND status = 'running'
		  AND released_at IS NULL
	`, input.UserID).Scan(&activeRuns, &pendingMessages); err != nil {
		return ChatRunReservation{}, fmt.Errorf("load active chat reservations: %w", err)
	}
	if limits.ConcurrentRunLimit > 0 && activeRuns >= int64(limits.ConcurrentRunLimit) {
		return ChatRunReservation{}, quotaExceeded("concurrent_run_limit_exceeded", int64(limits.ConcurrentRunLimit), activeRuns, time.Time{})
	}
	if input.ReserveMessage && limits.DailyMessageLimit > 0 {
		usage, err := usageForTodayInTx(ctx, tx, input.UserID)
		if err != nil {
			return ChatRunReservation{}, err
		}
		if usage.DailyMessages+pendingMessages >= int64(limits.DailyMessageLimit) {
			return ChatRunReservation{}, quotaExceeded("daily_message_limit_exceeded", int64(limits.DailyMessageLimit), usage.DailyMessages+pendingMessages, usage.ResetAt)
		}
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO chat_run_reservations (
			run_id, user_id, session_id, kind, operation, intent_version, intent_hash,
			retry_target_message_id, runtime_snapshot, status, message_reserved, expires_at, accepted_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, 0), $9, 'running', $10, $11, $12, NOW())
		ON CONFLICT (run_id) DO NOTHING
	`, input.RunID, input.UserID, input.SessionID, input.Kind, input.Operation, input.IntentVersion, input.IntentHash, input.RetryTargetMessageID, input.RuntimeSnapshot, input.ReserveMessage, input.ExpiresAt, input.AcceptedAt)
	if err != nil {
		return ChatRunReservation{}, fmt.Errorf("create chat run reservation: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return ChatRunReservation{}, fmt.Errorf("read created chat run reservation rows: %w", err)
	}
	if inserted != 1 {
		return ChatRunReservation{}, ErrChatRunIntentConflict
	}
	record, err := scanChatRun(tx.QueryRowContext(ctx, chatRunSelect+` WHERE run_id = $1`, input.RunID))
	if err != nil {
		return ChatRunReservation{}, fmt.Errorf("load created chat run reservation: %w", err)
	}
	return ChatRunReservation{RunID: input.RunID, Record: record}, nil
}

func normalizeChatRunReservation(input ChatRunReservationInput) (ChatRunReservationInput, error) {
	input.RunID = strings.TrimSpace(input.RunID)
	if input.UserID <= 0 || input.AuthVersion <= 0 || input.SessionID <= 0 || input.RunID == "" || input.ExpiresAt.IsZero() {
		return input, fmt.Errorf("invalid chat run reservation")
	}
	if input.Kind == "" {
		input.Kind = "chat"
	}
	if input.Operation == "" {
		input.Operation = "send"
		if input.Kind == "compaction" {
			input.Operation = "compaction"
		} else if input.Kind == "memory_maintenance" {
			input.Operation = "memory_compact"
		}
	}
	validOperation := input.Kind == "chat" && (input.Operation == "send" || input.Operation == "retry")
	validOperation = validOperation || input.Kind == "compaction" && input.Operation == "compaction"
	validOperation = validOperation || input.Kind == "memory_maintenance" && (input.Operation == "memory_compact" || input.Operation == "memory_retry")
	if !validOperation || input.IntentVersion < 0 || (input.IntentVersion > 0 && input.IntentHash == "") {
		return input, fmt.Errorf("invalid chat run intent")
	}
	if input.Operation == "retry" && input.RetryTargetMessageID <= 0 {
		return input, fmt.Errorf("invalid retry target")
	}
	if input.Operation != "retry" && input.RetryTargetMessageID != 0 {
		return input, fmt.Errorf("unexpected retry target")
	}
	if input.AcceptedAt.IsZero() {
		input.AcceptedAt = time.Now()
	}
	if len(input.RuntimeSnapshot) == 0 {
		input.RuntimeSnapshot = []byte(`{}`)
	}
	if !json.Valid(input.RuntimeSnapshot) || len(input.RuntimeSnapshot) > 16384 {
		return input, fmt.Errorf("invalid runtime snapshot")
	}
	return input, nil
}

func chatRunReservationMatches(record ChatRunRecord, input ChatRunReservationInput) bool {
	return record.UserID == input.UserID &&
		record.SessionID == input.SessionID &&
		record.Kind == input.Kind &&
		record.Operation == input.Operation &&
		record.IntentVersion == input.IntentVersion &&
		record.IntentHash == input.IntentHash &&
		record.RetryTargetMessageID == input.RetryTargetMessageID
}

func (r *QuotaRepository) AdmitChatMessage(ctx context.Context, input ChatRunReservationInput, message *model.Message) (ChatRunAdmission, error) {
	if message == nil || message.SessionID != input.SessionID {
		return ChatRunAdmission{}, fmt.Errorf("message does not match chat run")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return ChatRunAdmission{}, fmt.Errorf("begin chat message admission: %w", err)
	}
	defer tx.Rollback()
	reservation, err := reserveChatRunInTx(ctx, tx, input)
	if err != nil {
		return ChatRunAdmission{}, err
	}
	if reservation.Existing {
		existing, err := loadAdmissionMessage(ctx, tx, reservation.Record.UserMessageID)
		if err != nil {
			return ChatRunAdmission{}, err
		}
		if err := tx.Commit(); err != nil {
			return ChatRunAdmission{}, fmt.Errorf("commit existing chat message admission: %w", err)
		}
		return ChatRunAdmission{Record: reservation.Record, Message: existing, Existing: true}, nil
	}

	persisted := *message
	persisted.MessageData = append([]byte(nil), message.MessageData...)
	if err := claimStagedAttachmentsForMessages(ctx, tx, input.SessionID, input.UserID, []*model.Message{&persisted}); err != nil {
		return ChatRunAdmission{}, err
	}
	if err := createMessage(ctx, tx, &persisted); err != nil {
		return ChatRunAdmission{}, err
	}
	if persisted.Role != "user" {
		return ChatRunAdmission{}, fmt.Errorf("admitted chat message must be a user message")
	}
	record, err := bindAdmissionMessage(ctx, tx, reservation.Record, persisted.ID)
	if err != nil {
		return ChatRunAdmission{}, err
	}
	if _, err := createAnswerAttemptForRunTx(ctx, tx, input.SessionID, persisted.ID, input.RunID, true); err != nil {
		return ChatRunAdmission{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET updated_at = NOW() WHERE id = $1`, input.SessionID); err != nil {
		return ChatRunAdmission{}, fmt.Errorf("touch admitted session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ChatRunAdmission{}, fmt.Errorf("commit chat message admission: %w", err)
	}
	return ChatRunAdmission{Record: record, Message: &persisted}, nil
}

func (r *QuotaRepository) AdmitRetryChatRun(ctx context.Context, input ChatRunReservationInput, targetMessageID int64) (ChatRunAdmission, error) {
	if targetMessageID <= 0 || input.RetryTargetMessageID != targetMessageID {
		return ChatRunAdmission{}, fmt.Errorf("retry target does not match chat run")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return ChatRunAdmission{}, fmt.Errorf("begin retry admission: %w", err)
	}
	defer tx.Rollback()
	reservation, err := reserveChatRunInTx(ctx, tx, input)
	if err != nil {
		return ChatRunAdmission{}, err
	}
	if reservation.Existing {
		existing, err := loadAdmissionMessage(ctx, tx, reservation.Record.UserMessageID)
		if err != nil {
			return ChatRunAdmission{}, err
		}
		if err := tx.Commit(); err != nil {
			return ChatRunAdmission{}, fmt.Errorf("commit existing retry admission: %w", err)
		}
		return ChatRunAdmission{Record: reservation.Record, Message: existing, Existing: true}, nil
	}
	retryUser, err := prepareRetryForActiveSessionTx(ctx, tx, input.SessionID, input.UserID, targetMessageID)
	if err != nil {
		return ChatRunAdmission{}, err
	}
	record, err := bindAdmissionMessage(ctx, tx, reservation.Record, retryUser.ID)
	if err != nil {
		return ChatRunAdmission{}, err
	}
	if _, err := createAnswerAttemptForRunTx(ctx, tx, input.SessionID, retryUser.ID, input.RunID, false); err != nil {
		return ChatRunAdmission{}, err
	}
	if err := tx.Commit(); err != nil {
		return ChatRunAdmission{}, fmt.Errorf("commit retry admission: %w", err)
	}
	return ChatRunAdmission{Record: record, Message: retryUser}, nil
}

func (r *QuotaRepository) AdmitEditedRetryChatRun(ctx context.Context, input ChatRunReservationInput, targetMessageID int64, replacement *model.Message) (ChatRunAdmission, error) {
	if targetMessageID <= 0 || input.RetryTargetMessageID != targetMessageID {
		return ChatRunAdmission{}, fmt.Errorf("edit retry target does not match chat run")
	}
	if !input.ReserveMessage || replacement == nil || replacement.SessionID != input.SessionID {
		return ChatRunAdmission{}, fmt.Errorf("invalid edited retry message")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return ChatRunAdmission{}, fmt.Errorf("begin edited retry admission: %w", err)
	}
	defer tx.Rollback()
	reservation, err := reserveChatRunInTx(ctx, tx, input)
	if err != nil {
		return ChatRunAdmission{}, err
	}
	if reservation.Existing {
		existing, err := loadAdmissionMessage(ctx, tx, reservation.Record.UserMessageID)
		if err != nil {
			return ChatRunAdmission{}, err
		}
		if err := tx.Commit(); err != nil {
			return ChatRunAdmission{}, fmt.Errorf("commit existing edited retry admission: %w", err)
		}
		return ChatRunAdmission{Record: reservation.Record, Message: existing, Existing: true}, nil
	}

	var anotherRunActive bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM chat_run_reservations
			WHERE session_id = $1
			  AND run_id <> $2
			  AND status = 'running'
			  AND released_at IS NULL
		)
	`, input.SessionID, input.RunID).Scan(&anotherRunActive); err != nil {
		return ChatRunAdmission{}, fmt.Errorf("inspect active edited retry runs: %w", err)
	}
	if anotherRunActive {
		return ChatRunAdmission{}, ErrChatRunActive
	}

	source, err := prepareEditRetryForActiveSessionTx(ctx, tx, input.SessionID, input.UserID, targetMessageID)
	if err != nil {
		return ChatRunAdmission{}, err
	}
	sourceData, err := ParseMessageData(source.MessageData)
	if err != nil {
		return ChatRunAdmission{}, err
	}
	replacementData, err := ParseMessageData(replacement.MessageData)
	if err != nil {
		return ChatRunAdmission{}, err
	}
	sourceContent, _ := sourceData["content"].(string)
	replacementContent, _ := replacementData["content"].(string)
	if sourceContent == replacementContent {
		return ChatRunAdmission{}, ErrMessageUnchanged
	}
	if role, _ := replacementData["role"].(string); role != "user" || replacement.SchemaVersion != source.SchemaVersion {
		return ChatRunAdmission{}, fmt.Errorf("edited retry message does not preserve source format")
	}
	sourceAttachments, err := attachmentIDsFromMessageData(source.MessageData)
	if err != nil {
		return ChatRunAdmission{}, err
	}
	replacementAttachments, err := attachmentIDsFromMessageData(replacement.MessageData)
	if err != nil {
		return ChatRunAdmission{}, err
	}
	if !slices.Equal(sourceAttachments, replacementAttachments) {
		return ChatRunAdmission{}, fmt.Errorf("edited retry message does not preserve attachments")
	}
	if strings.TrimSpace(replacementContent) == "" && len(replacementAttachments) == 0 {
		return ChatRunAdmission{}, fmt.Errorf("edited retry message has no content")
	}
	metadata, _ := replacementData["metadata"].(map[string]interface{})
	if runID, _ := metadata["run_id"].(string); strings.TrimSpace(runID) != input.RunID {
		return ChatRunAdmission{}, fmt.Errorf("edited retry message run id does not match")
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE messages
		SET deleted_at = NOW(), updated_at = NOW()
		WHERE session_id = $1 AND id >= $2 AND deleted_at IS NULL
	`, input.SessionID, source.ID); err != nil {
		return ChatRunAdmission{}, fmt.Errorf("hide replaced conversation tail: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE answer_attempts
		SET selected = false
		WHERE user_message_id = $1 AND selected
	`, source.ID); err != nil {
		return ChatRunAdmission{}, fmt.Errorf("hide replaced answer attempts: %w", err)
	}

	persisted := *replacement
	persisted.ID = 0
	persisted.MessageData = append([]byte(nil), replacement.MessageData...)
	persisted.Role = ""
	persisted.HasToolCalls = false
	persisted.HasReasoning = false
	persisted.HasMultimodal = false
	persisted.AnswerAttemptID = nil
	persisted.CompressedAt = nil
	persisted.CompressionSummaryID = nil
	persisted.CreatedAt = time.Time{}
	persisted.UpdatedAt = time.Time{}
	persisted.DeletedAt = nil
	if err := createMessage(ctx, tx, &persisted); err != nil {
		return ChatRunAdmission{}, err
	}
	record, err := bindAdmissionMessage(ctx, tx, reservation.Record, persisted.ID)
	if err != nil {
		return ChatRunAdmission{}, err
	}
	if _, err := createAnswerAttemptForRunTx(ctx, tx, input.SessionID, persisted.ID, input.RunID, true); err != nil {
		return ChatRunAdmission{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE sessions
		SET answer_selection_revision = answer_selection_revision + 1, updated_at = NOW()
		WHERE id = $1
	`, input.SessionID); err != nil {
		return ChatRunAdmission{}, fmt.Errorf("touch edited retry session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ChatRunAdmission{}, fmt.Errorf("commit edited retry admission: %w", err)
	}
	return ChatRunAdmission{Record: record, Message: &persisted}, nil
}

func bindAdmissionMessage(ctx context.Context, tx *sql.Tx, record ChatRunRecord, userMessageID int64) (ChatRunRecord, error) {
	updated, err := scanChatRun(tx.QueryRowContext(ctx, `
		UPDATE chat_run_reservations
		SET user_message_id = $2, message_reserved = false, updated_at = NOW()
		WHERE run_id = $1 AND status = 'running' AND user_message_id IS NULL
		RETURNING run_id, user_id, session_id, kind, operation, intent_version, intent_hash,
		          COALESCE(retry_target_message_id, 0), runtime_snapshot, status, cancel_cause,
		          public_error_code, public_error_message,
		          COALESCE(user_message_id, 0), COALESCE(terminal_message_id, 0),
		          terminal_event, accepted_at, terminal_at, expires_at
	`, record.RunID, userMessageID))
	if err == sql.ErrNoRows {
		return ChatRunRecord{}, ErrChatRunTerminal
	}
	if err != nil {
		return ChatRunRecord{}, fmt.Errorf("bind admitted chat message: %w", err)
	}
	return updated, nil
}

func loadAdmissionMessage(ctx context.Context, tx *sql.Tx, messageID int64) (*model.Message, error) {
	if messageID <= 0 {
		return nil, fmt.Errorf("existing chat run has no user message")
	}
	message := &model.Message{}
	err := tx.QueryRowContext(ctx, `
		SELECT id, session_id, schema_version, message_data, role,
		       has_tool_calls, has_reasoning, has_multimodal, answer_attempt_id,
		       compressed_at, compression_summary_id, created_at, updated_at
		FROM messages WHERE id = $1
	`, messageID).Scan(
		&message.ID, &message.SessionID, &message.SchemaVersion, &message.MessageData, &message.Role,
		&message.HasToolCalls, &message.HasReasoning, &message.HasMultimodal, &message.AnswerAttemptID,
		&message.CompressedAt, &message.CompressionSummaryID, &message.CreatedAt, &message.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load admitted chat message: %w", err)
	}
	return message, nil
}
