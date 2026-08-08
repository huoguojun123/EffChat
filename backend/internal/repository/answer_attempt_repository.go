package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/lib/pq"
)

const (
	AnswerAttemptStatusRunning    = "running"
	AnswerAttemptStatusCompleted  = "completed"
	AnswerAttemptStatusIncomplete = "incomplete"
	AnswerAttemptStatusFailed     = "failed"
)

var (
	ErrAnswerAttemptNotFound      = errors.New("answer attempt not found")
	ErrAnswerAttemptNotSelectable = errors.New("answer attempt has no selectable output")
	ErrAnswerAttemptLastRemaining = errors.New("answer attempt is the last remaining selectable answer")
)

type AnswerAttempt struct {
	ID                int64
	SessionID         int64
	UserMessageID     int64
	RunID             string
	AttemptNumber     int
	Status            string
	Selected          bool
	SelectionChanged  bool
	SelectionRevision int64
}

type AnswerAttemptNavigation struct {
	AnswerAttempt
	AttemptCount int
	PreviousID   *int64
	NextID       *int64
	CanSwitch    bool
}

type AnswerAttemptDeletion struct {
	DeletedAttemptID  int64
	SelectedAttempt   *AnswerAttempt
	SelectionChanged  bool
	SelectionRevision int64
}

type AnswerAttemptRepository struct {
	db *sql.DB
}

func NewAnswerAttemptRepository(db *sql.DB) *AnswerAttemptRepository {
	return &AnswerAttemptRepository{db: db}
}

func createAnswerAttemptForRunTx(ctx context.Context, tx *sql.Tx, sessionID, userMessageID int64, runID string, selected bool) (*AnswerAttempt, error) {
	runID = strings.TrimSpace(runID)
	if sessionID <= 0 || userMessageID <= 0 || runID == "" {
		return nil, fmt.Errorf("invalid answer attempt input")
	}
	var lockedUserMessageID int64
	if err := tx.QueryRowContext(ctx, `
		SELECT id
		FROM messages
		WHERE id = $1 AND session_id = $2 AND role = 'user' AND deleted_at IS NULL
		FOR UPDATE
	`, userMessageID, sessionID).Scan(&lockedUserMessageID); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrRetryTargetStale
		}
		return nil, fmt.Errorf("lock answer attempt user message: %w", err)
	}

	var nextAttemptNumber int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(attempt_number), 0) + 1
		FROM answer_attempts
		WHERE user_message_id = $1
	`, userMessageID).Scan(&nextAttemptNumber); err != nil {
		return nil, fmt.Errorf("next answer attempt number: %w", err)
	}

	attempt := &AnswerAttempt{}
	err := tx.QueryRowContext(ctx, `
		INSERT INTO answer_attempts (session_id, user_message_id, run_id, attempt_number, status, selected)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, session_id, user_message_id, COALESCE(run_id, ''), attempt_number, status, selected
	`, sessionID, userMessageID, runID, nextAttemptNumber, AnswerAttemptStatusRunning, selected).Scan(
		&attempt.ID,
		&attempt.SessionID,
		&attempt.UserMessageID,
		&attempt.RunID,
		&attempt.AttemptNumber,
		&attempt.Status,
		&attempt.Selected,
	)
	if err != nil {
		return nil, fmt.Errorf("create answer attempt: %w", err)
	}
	return attempt, nil
}

func answerAttemptForRunTx(ctx context.Context, tx *sql.Tx, runID string) (*AnswerAttempt, bool, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, false, nil
	}
	attempt := &AnswerAttempt{}
	err := tx.QueryRowContext(ctx, `
		SELECT id, session_id, user_message_id, COALESCE(run_id, ''), attempt_number, status, selected
		FROM answer_attempts
		WHERE run_id = $1
		FOR UPDATE
	`, runID).Scan(
		&attempt.ID,
		&attempt.SessionID,
		&attempt.UserMessageID,
		&attempt.RunID,
		&attempt.AttemptNumber,
		&attempt.Status,
		&attempt.Selected,
	)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("load answer attempt for run: %w", err)
	}
	return attempt, true, nil
}

func bindMessagesToAnswerAttempt(messages []*model.Message, attemptID int64) {
	if attemptID <= 0 {
		return
	}
	for _, message := range messages {
		if message == nil {
			continue
		}
		role := message.Role
		if role == "" {
			data, err := ParseMessageData(message.MessageData)
			if err != nil {
				continue
			}
			role, _ = data["role"].(string)
		}
		if role != "assistant" && role != "tool" {
			continue
		}
		id := attemptID
		message.AnswerAttemptID = &id
	}
}

func messageHasSelectableAnswerOutput(message *model.Message) bool {
	if message == nil || message.Role != "assistant" {
		return false
	}
	data, err := ParseMessageData(message.MessageData)
	if err != nil {
		return false
	}
	if metadata, ok := data["metadata"].(map[string]interface{}); ok {
		if ephemeral, _ := metadata["ephemeral_error"].(bool); ephemeral {
			return false
		}
	}
	content, _ := data["content"].(string)
	return strings.TrimSpace(content) != "" || message.HasToolCalls || message.HasReasoning
}

func messagesHaveSelectableAnswerOutput(messages []*model.Message) bool {
	for _, message := range messages {
		if messageHasSelectableAnswerOutput(message) {
			return true
		}
	}
	return false
}

func answerAttemptTerminalStatus(runStatus string, messages []*model.Message) string {
	if !messagesHaveSelectableAnswerOutput(messages) {
		return AnswerAttemptStatusFailed
	}
	if runStatus == "completed" {
		return AnswerAttemptStatusCompleted
	}
	return AnswerAttemptStatusIncomplete
}

func finishAnswerAttemptTx(ctx context.Context, tx *sql.Tx, attempt *AnswerAttempt, runStatus string, messages []*model.Message) error {
	if attempt == nil {
		return nil
	}
	status := answerAttemptTerminalStatus(runStatus, messages)
	if status == AnswerAttemptStatusCompleted && !attempt.Selected {
		if _, err := tx.ExecContext(ctx, `
			UPDATE answer_attempts
			SET selected = false
			WHERE user_message_id = $1 AND selected
		`, attempt.UserMessageID); err != nil {
			return fmt.Errorf("clear selected answer attempt: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE answer_attempts
			SET status = $2, selected = true, completed_at = NOW()
			WHERE id = $1
		`, attempt.ID, status); err != nil {
			return fmt.Errorf("select completed answer attempt: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE sessions
			SET answer_selection_revision = answer_selection_revision + 1, updated_at = NOW()
			WHERE id = $1
		`, attempt.SessionID); err != nil {
			return fmt.Errorf("advance answer selection revision: %w", err)
		}
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE answer_attempts
		SET status = $2, completed_at = NOW()
		WHERE id = $1
	`, attempt.ID, status); err != nil {
		return fmt.Errorf("finish answer attempt: %w", err)
	}
	return nil
}

func markAnswerAttemptTerminalForRun(ctx context.Context, exec dbExecutor, runID string) error {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil
	}
	status := AnswerAttemptStatusFailed
	if _, err := exec.ExecContext(ctx, `
		UPDATE answer_attempts
		SET status = $2, completed_at = NOW()
		WHERE run_id = $1 AND status = 'running'
	`, runID, status); err != nil {
		return fmt.Errorf("mark answer attempt terminal: %w", err)
	}
	return nil
}

func (r *AnswerAttemptRepository) SelectForActiveSession(ctx context.Context, sessionID, userID, attemptID int64) (*AnswerAttempt, error) {
	if attemptID <= 0 {
		return nil, ErrAnswerAttemptNotFound
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin answer attempt selection: %w", err)
	}
	defer tx.Rollback()
	if err := lockActiveSession(ctx, tx, sessionID, userID); err != nil {
		return nil, err
	}

	attempt := &AnswerAttempt{}
	err = tx.QueryRowContext(ctx, `
		SELECT id, session_id, user_message_id, COALESCE(run_id, ''), attempt_number, status, selected
		FROM answer_attempts
		WHERE id = $1 AND session_id = $2
		FOR UPDATE
	`, attemptID, sessionID).Scan(
		&attempt.ID,
		&attempt.SessionID,
		&attempt.UserMessageID,
		&attempt.RunID,
		&attempt.AttemptNumber,
		&attempt.Status,
		&attempt.Selected,
	)
	if err == sql.ErrNoRows {
		return nil, ErrAnswerAttemptNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load answer attempt for selection: %w", err)
	}
	if attempt.Status != AnswerAttemptStatusCompleted && attempt.Status != AnswerAttemptStatusIncomplete {
		return nil, ErrAnswerAttemptNotSelectable
	}

	var selectable bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM messages
			WHERE answer_attempt_id = $1
			  AND deleted_at IS NULL
			  AND compressed_at IS NULL
			  AND role = 'assistant'
			  AND COALESCE(message_data->'metadata'->>'ephemeral_error', '') <> 'true'
			  AND (
				length(btrim(COALESCE(message_data->>'content', ''))) > 0
				OR has_tool_calls
				OR has_reasoning
			  )
		)
	`, attempt.ID).Scan(&selectable); err != nil {
		return nil, fmt.Errorf("check answer attempt output: %w", err)
	}
	if !selectable {
		return nil, ErrAnswerAttemptNotSelectable
	}
	if !attempt.Selected {
		if _, err := tx.ExecContext(ctx, `UPDATE answer_attempts SET selected = false WHERE user_message_id = $1 AND selected`, attempt.UserMessageID); err != nil {
			return nil, fmt.Errorf("clear current answer attempt selection: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE answer_attempts SET selected = true WHERE id = $1`, attempt.ID); err != nil {
			return nil, fmt.Errorf("select answer attempt: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE sessions
			SET answer_selection_revision = answer_selection_revision + 1, updated_at = NOW()
			WHERE id = $1
		`, sessionID); err != nil {
			return nil, fmt.Errorf("advance answer selection revision: %w", err)
		}
		attempt.Selected = true
		attempt.SelectionChanged = true
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT answer_selection_revision
		FROM sessions
		WHERE id = $1
	`, sessionID).Scan(&attempt.SelectionRevision); err != nil {
		return nil, fmt.Errorf("load answer selection revision: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit answer attempt selection: %w", err)
	}
	return attempt, nil
}

func (r *AnswerAttemptRepository) DeleteForActiveSession(ctx context.Context, sessionID, userID, attemptID int64) (*AnswerAttemptDeletion, error) {
	if attemptID <= 0 {
		return nil, ErrAnswerAttemptNotFound
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin answer attempt deletion: %w", err)
	}
	defer tx.Rollback()
	if err := lockActiveSession(ctx, tx, sessionID, userID); err != nil {
		return nil, err
	}

	target := &AnswerAttempt{}
	err = tx.QueryRowContext(ctx, `
		SELECT id, session_id, user_message_id, COALESCE(run_id, ''), attempt_number, status, selected
		FROM answer_attempts
		WHERE id = $1 AND session_id = $2
		FOR UPDATE
	`, attemptID, sessionID).Scan(
		&target.ID,
		&target.SessionID,
		&target.UserMessageID,
		&target.RunID,
		&target.AttemptNumber,
		&target.Status,
		&target.Selected,
	)
	if err == sql.ErrNoRows {
		return nil, ErrAnswerAttemptNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load answer attempt for deletion: %w", err)
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT a.id, a.session_id, a.user_message_id, COALESCE(a.run_id, ''), a.attempt_number, a.status, a.selected
		FROM answer_attempts a
		WHERE a.user_message_id = $1
		  AND a.status IN ('completed', 'incomplete')
		  AND EXISTS (
			SELECT 1
			FROM messages output
			WHERE output.answer_attempt_id = a.id
			  AND output.deleted_at IS NULL
			  AND output.compressed_at IS NULL
			  AND output.role = 'assistant'
			  AND COALESCE(output.message_data->'metadata'->>'ephemeral_error', '') <> 'true'
			  AND (
				length(btrim(COALESCE(output.message_data->>'content', ''))) > 0
				OR output.has_tool_calls
				OR output.has_reasoning
			  )
		  )
		ORDER BY a.attempt_number
		FOR UPDATE
	`, target.UserMessageID)
	if err != nil {
		return nil, fmt.Errorf("lock selectable answer attempts for deletion: %w", err)
	}
	selectable := make([]*AnswerAttempt, 0, 4)
	for rows.Next() {
		candidate := &AnswerAttempt{}
		if err := rows.Scan(
			&candidate.ID,
			&candidate.SessionID,
			&candidate.UserMessageID,
			&candidate.RunID,
			&candidate.AttemptNumber,
			&candidate.Status,
			&candidate.Selected,
		); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan selectable answer attempt: %w", err)
		}
		selectable = append(selectable, candidate)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate selectable answer attempts: %w", err)
	}
	rows.Close()

	targetIndex := -1
	for index, candidate := range selectable {
		if candidate.ID == target.ID {
			targetIndex = index
			break
		}
	}
	if targetIndex < 0 {
		return nil, ErrAnswerAttemptNotSelectable
	}
	if len(selectable) <= 1 {
		return nil, ErrAnswerAttemptLastRemaining
	}

	var replacement *AnswerAttempt
	if target.Selected {
		if targetIndex > 0 {
			replacement = selectable[targetIndex-1]
		} else {
			replacement = selectable[targetIndex+1]
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE answer_attempt_id = $1`, target.ID); err != nil {
		return nil, fmt.Errorf("delete answer attempt messages: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM answer_attempts WHERE id = $1`, target.ID); err != nil {
		return nil, fmt.Errorf("delete answer attempt: %w", err)
	}

	deletion := &AnswerAttemptDeletion{DeletedAttemptID: target.ID}
	if replacement != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE answer_attempts SET selected = true WHERE id = $1`, replacement.ID); err != nil {
			return nil, fmt.Errorf("select replacement answer attempt: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE sessions
			SET answer_selection_revision = answer_selection_revision + 1, updated_at = NOW()
			WHERE id = $1
		`, sessionID); err != nil {
			return nil, fmt.Errorf("advance answer selection revision after deletion: %w", err)
		}
		replacement.Selected = true
		replacement.SelectionChanged = true
		deletion.SelectedAttempt = replacement
		deletion.SelectionChanged = true
	}
	if err := tx.QueryRowContext(ctx, `SELECT answer_selection_revision FROM sessions WHERE id = $1`, sessionID).Scan(&deletion.SelectionRevision); err != nil {
		return nil, fmt.Errorf("load answer selection revision after deletion: %w", err)
	}
	if deletion.SelectedAttempt != nil {
		deletion.SelectedAttempt.SelectionRevision = deletion.SelectionRevision
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit answer attempt deletion: %w", err)
	}
	return deletion, nil
}

func (r *AnswerAttemptRepository) NavigationForAttemptIDs(ctx context.Context, attemptIDs []int64) (map[int64]AnswerAttemptNavigation, error) {
	if len(attemptIDs) == 0 {
		return map[int64]AnswerAttemptNavigation{}, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT
			a.id,
			a.session_id,
			a.user_message_id,
			COALESCE(a.run_id, ''),
			(
				SELECT COUNT(*)
				FROM answer_attempts candidate
				WHERE candidate.user_message_id = a.user_message_id
				  AND candidate.attempt_number <= a.attempt_number
				  AND candidate.status IN ('completed', 'incomplete')
				  AND EXISTS (
					SELECT 1 FROM messages output
					WHERE output.answer_attempt_id = candidate.id
					  AND output.deleted_at IS NULL
					  AND output.compressed_at IS NULL
					  AND output.role = 'assistant'
					  AND COALESCE(output.message_data->'metadata'->>'ephemeral_error', '') <> 'true'
					  AND (length(btrim(COALESCE(output.message_data->>'content', ''))) > 0 OR output.has_tool_calls OR output.has_reasoning)
				  )
			),
			a.status,
			a.selected,
			(
				SELECT COUNT(*)
				FROM answer_attempts candidate
				WHERE candidate.user_message_id = a.user_message_id
				  AND candidate.status IN ('completed', 'incomplete')
				  AND EXISTS (
					SELECT 1 FROM messages output
					WHERE output.answer_attempt_id = candidate.id
					  AND output.deleted_at IS NULL
					  AND output.compressed_at IS NULL
					  AND output.role = 'assistant'
					  AND COALESCE(output.message_data->'metadata'->>'ephemeral_error', '') <> 'true'
					  AND (length(btrim(COALESCE(output.message_data->>'content', ''))) > 0 OR output.has_tool_calls OR output.has_reasoning)
				  )
			),
			COALESCE((
				SELECT candidate.id
				FROM answer_attempts candidate
				WHERE candidate.user_message_id = a.user_message_id
				  AND candidate.attempt_number < a.attempt_number
				  AND candidate.status IN ('completed', 'incomplete')
				  AND EXISTS (
					SELECT 1 FROM messages output
					WHERE output.answer_attempt_id = candidate.id
					  AND output.deleted_at IS NULL
					  AND output.compressed_at IS NULL
					  AND output.role = 'assistant'
					  AND COALESCE(output.message_data->'metadata'->>'ephemeral_error', '') <> 'true'
					  AND (length(btrim(COALESCE(output.message_data->>'content', ''))) > 0 OR output.has_tool_calls OR output.has_reasoning)
				  )
				ORDER BY candidate.attempt_number DESC
				LIMIT 1
			), 0),
			COALESCE((
				SELECT candidate.id
				FROM answer_attempts candidate
				WHERE candidate.user_message_id = a.user_message_id
				  AND candidate.attempt_number > a.attempt_number
				  AND candidate.status IN ('completed', 'incomplete')
				  AND EXISTS (
					SELECT 1 FROM messages output
					WHERE output.answer_attempt_id = candidate.id
					  AND output.deleted_at IS NULL
					  AND output.compressed_at IS NULL
					  AND output.role = 'assistant'
					  AND COALESCE(output.message_data->'metadata'->>'ephemeral_error', '') <> 'true'
					  AND (length(btrim(COALESCE(output.message_data->>'content', ''))) > 0 OR output.has_tool_calls OR output.has_reasoning)
				  )
				ORDER BY candidate.attempt_number ASC
				LIMIT 1
			), 0),
			TRUE
		FROM answer_attempts a
		WHERE a.id = ANY($1)
	`, pq.Array(attemptIDs))
	if err != nil {
		return nil, fmt.Errorf("list answer attempt navigation: %w", err)
	}
	defer rows.Close()
	result := make(map[int64]AnswerAttemptNavigation, len(attemptIDs))
	for rows.Next() {
		var navigation AnswerAttemptNavigation
		var previousID, nextID int64
		if err := rows.Scan(
			&navigation.ID,
			&navigation.SessionID,
			&navigation.UserMessageID,
			&navigation.RunID,
			&navigation.AttemptNumber,
			&navigation.Status,
			&navigation.Selected,
			&navigation.AttemptCount,
			&previousID,
			&nextID,
			&navigation.CanSwitch,
		); err != nil {
			return nil, fmt.Errorf("scan answer attempt navigation: %w", err)
		}
		if previousID > 0 {
			id := previousID
			navigation.PreviousID = &id
		}
		if nextID > 0 {
			id := nextID
			navigation.NextID = &id
		}
		result[navigation.ID] = navigation
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate answer attempt navigation: %w", err)
	}
	return result, nil
}
