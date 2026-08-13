package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type ChatRunRecord struct {
	RunID                string
	UserID               int64
	SessionID            int64
	Kind                 string
	Operation            string
	IntentVersion        int
	IntentHash           string
	RetryTargetMessageID int64
	RuntimeSnapshot      json.RawMessage
	Status               string
	CancelCause          string
	PublicErrorCode      string
	PublicErrorMessage   string
	UserMessageID        int64
	TerminalMessageID    int64
	TerminalEvent        json.RawMessage
	AcceptedAt           time.Time
	TerminalAt           *time.Time
	ExpiresAt            time.Time
}

type ChatRunTransitionInput struct {
	RunID              string
	Status             string
	CancelCause        string
	PublicErrorCode    string
	PublicErrorMessage string
	TerminalMessageID  int64
	TerminalEvent      json.RawMessage
	ExpiresAt          time.Time
}

func (r *QuotaRepository) GetChatRun(ctx context.Context, runID string) (ChatRunRecord, error) {
	record, err := scanChatRun(r.db.QueryRowContext(ctx, chatRunSelect+` WHERE run_id = $1`, runID))
	if err == sql.ErrNoRows {
		return ChatRunRecord{}, ErrNotFound
	}
	return record, err
}

// loadTerminalChatRunForScope returns a durable terminal record only when the
// caller still owns the same run/session/user scope. Content-plus-terminal
// transactions use it after their active-run lock misses: a previous commit
// may have succeeded even if its client observed a transient commit error.
// A missing, foreign, or still-running row deliberately remains indistinct to
// the caller, which must preserve its original active-lock error.
func loadTerminalChatRunForScope(ctx context.Context, exec dbExecutor, runID string, sessionID, userID int64) (ChatRunRecord, bool, error) {
	record, err := scanChatRun(exec.QueryRowContext(ctx, chatRunSelect+`
		WHERE run_id = $1 AND session_id = $2 AND user_id = $3`, runID, sessionID, userID))
	if err == sql.ErrNoRows {
		return ChatRunRecord{}, false, nil
	}
	if err != nil {
		return ChatRunRecord{}, false, fmt.Errorf("load terminal chat run for scope: %w", err)
	}
	return record, record.Status != "running", nil
}

func (r *QuotaRepository) BindChatRunUserMessage(ctx context.Context, runID string, userMessageID int64) (bool, error) {
	if runID == "" || userMessageID <= 0 {
		return false, nil
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE chat_run_reservations
		SET user_message_id = $2, updated_at = NOW()
		WHERE run_id = $1
		  AND status = 'running'
		  AND (user_message_id IS NULL OR user_message_id = $2)
	`, runID, userMessageID)
	if err != nil {
		return false, fmt.Errorf("bind chat run user message: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read bound chat run rows: %w", err)
	}
	return updated == 1, nil
}

func (r *QuotaRepository) TransitionChatRun(ctx context.Context, input ChatRunTransitionInput) (ChatRunRecord, bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return ChatRunRecord{}, false, fmt.Errorf("begin chat run transition: %w", err)
	}
	defer tx.Rollback()
	record, transitioned, err := transitionChatRun(ctx, tx, input)
	if err != nil || !transitioned {
		return record, transitioned, err
	}
	if err := markAnswerAttemptTerminalForRun(ctx, tx, input.RunID); err != nil {
		return ChatRunRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return ChatRunRecord{}, false, fmt.Errorf("commit chat run transition: %w", err)
	}
	return record, true, nil
}

func (r *QuotaRepository) ReconcileRunningChatRuns(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.QueryRowContext(ctx, `
		WITH reconciled AS (
			UPDATE chat_run_reservations
			SET status = 'failed',
			    cancel_cause = '',
			    public_error_code = 'server_restarted',
			    public_error_message = '服务已重启，请重试',
			    terminal_event = jsonb_build_object(
			        'event', 'error',
			        'data', jsonb_build_object(
			            'error', '服务已重启，请重试',
			            'code', 'server_restarted',
			            'retryable', true
			        )
			    ),
			    message_reserved = false,
			    released_at = COALESCE(released_at, NOW()),
			    terminal_at = NOW(),
			    updated_at = NOW(),
			    expires_at = GREATEST(expires_at, NOW() + INTERVAL '10 minutes')
			WHERE status = 'running'
			RETURNING run_id
		), terminal_attempts AS (
			UPDATE answer_attempts attempt
			SET status = 'failed', completed_at = NOW()
			FROM reconciled
			WHERE attempt.run_id = reconciled.run_id AND attempt.status = 'running'
		)
		SELECT COUNT(*) FROM reconciled
	`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("reconcile running chat runs: %w", err)
	}
	return count, nil
}

func transitionChatRun(ctx context.Context, exec dbExecutor, input ChatRunTransitionInput) (ChatRunRecord, bool, error) {
	if input.RunID == "" || input.ExpiresAt.IsZero() {
		return ChatRunRecord{}, false, fmt.Errorf("invalid chat run transition")
	}
	if input.Status != "completed" && input.Status != "failed" && input.Status != "canceled" {
		return ChatRunRecord{}, false, fmt.Errorf("invalid chat run terminal status")
	}
	var event interface{}
	if len(input.TerminalEvent) > 0 {
		event = string(input.TerminalEvent)
	}
	record, err := scanChatRun(exec.QueryRowContext(ctx, `
		UPDATE chat_run_reservations
		SET status = $2,
		    cancel_cause = $3,
		    public_error_code = $4,
		    public_error_message = $5,
			    terminal_message_id = COALESCE(NULLIF($6, 0), terminal_message_id),
		    terminal_event = $7::jsonb,
		    message_reserved = false,
		    released_at = COALESCE(released_at, NOW()),
		    terminal_at = NOW(),
		    updated_at = NOW(),
		    expires_at = $8
		WHERE run_id = $1 AND status = 'running'
		RETURNING run_id, user_id, session_id, kind, operation, intent_version, intent_hash,
		          COALESCE(retry_target_message_id, 0), runtime_snapshot, status, cancel_cause,
		          public_error_code, public_error_message,
		          COALESCE(user_message_id, 0), COALESCE(terminal_message_id, 0),
		          terminal_event, accepted_at, terminal_at, expires_at
	`, input.RunID, input.Status, input.CancelCause, input.PublicErrorCode, input.PublicErrorMessage, input.TerminalMessageID, event, input.ExpiresAt))
	if err == nil {
		return record, true, nil
	}
	if err != sql.ErrNoRows {
		return ChatRunRecord{}, false, fmt.Errorf("transition chat run: %w", err)
	}
	record, err = scanChatRun(exec.QueryRowContext(ctx, chatRunSelect+` WHERE run_id = $1`, input.RunID))
	if err == sql.ErrNoRows {
		return ChatRunRecord{}, false, ErrNotFound
	}
	if err != nil {
		return ChatRunRecord{}, false, err
	}
	return record, false, nil
}

func cancelRunningChatRuns(ctx context.Context, exec dbExecutor, userID int64, sessionID *int64, cause, code, message string, retryable bool) error {
	args := []interface{}{cause, code, message, retryable, userID}
	scope := "user_id = $5"
	if sessionID != nil {
		args = append(args, *sessionID)
		scope += " AND session_id = $6"
	}
	_, err := exec.ExecContext(ctx, fmt.Sprintf(`
		WITH canceled AS (
			UPDATE chat_run_reservations
			SET status = 'canceled',
			    cancel_cause = $1,
			    public_error_code = $2,
			    public_error_message = $3,
			    terminal_event = jsonb_build_object(
			        'event', 'error',
			        'data', jsonb_build_object(
			            'error', $3::text,
			            'code', $2::text,
			            'retryable', $4::boolean
			        )
			    ),
			    message_reserved = false,
			    released_at = COALESCE(released_at, NOW()),
			    terminal_at = NOW(),
			    updated_at = NOW()
			WHERE %s AND status = 'running'
			RETURNING run_id
		)
		UPDATE answer_attempts attempt
		SET status = 'failed', completed_at = NOW()
		FROM canceled
		WHERE attempt.run_id = canceled.run_id AND attempt.status = 'running'
	`, scope), args...)
	return err
}

const chatRunSelect = `
	SELECT run_id, user_id, session_id, kind, operation, intent_version, intent_hash,
	       COALESCE(retry_target_message_id, 0), runtime_snapshot, status, cancel_cause,
	       public_error_code, public_error_message,
	       COALESCE(user_message_id, 0), COALESCE(terminal_message_id, 0),
	       terminal_event, accepted_at, terminal_at, expires_at
	FROM chat_run_reservations`

type rowScanner interface {
	Scan(dest ...interface{}) error
}

func scanChatRun(row rowScanner) (ChatRunRecord, error) {
	var record ChatRunRecord
	var terminalEvent []byte
	var terminalAt sql.NullTime
	if err := row.Scan(
		&record.RunID,
		&record.UserID,
		&record.SessionID,
		&record.Kind,
		&record.Operation,
		&record.IntentVersion,
		&record.IntentHash,
		&record.RetryTargetMessageID,
		&record.RuntimeSnapshot,
		&record.Status,
		&record.CancelCause,
		&record.PublicErrorCode,
		&record.PublicErrorMessage,
		&record.UserMessageID,
		&record.TerminalMessageID,
		&terminalEvent,
		&record.AcceptedAt,
		&terminalAt,
		&record.ExpiresAt,
	); err != nil {
		return ChatRunRecord{}, err
	}
	if len(terminalEvent) > 0 {
		record.TerminalEvent = append(json.RawMessage(nil), terminalEvent...)
	}
	record.RuntimeSnapshot = append(json.RawMessage(nil), record.RuntimeSnapshot...)
	if terminalAt.Valid {
		value := terminalAt.Time
		record.TerminalAt = &value
	}
	return record, nil
}
