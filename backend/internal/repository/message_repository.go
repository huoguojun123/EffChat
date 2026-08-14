package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/lib/pq"
)

type MessageRepository struct {
	db *sql.DB
}

type ConversationTurn struct {
	ID        int64
	Sequence  int64
	Content   string
	CreatedAt time.Time
}

type MessageWindowMode string

const (
	MessageWindowLatest MessageWindowMode = "latest"
	MessageWindowBefore MessageWindowMode = "before"
	MessageWindowAfter  MessageWindowMode = "after"
	MessageWindowAround MessageWindowMode = "around"
)

type MessageWindow struct {
	Messages    []*model.Message
	FirstTurnID int64
	LastTurnID  int64
	HasOlder    bool
	HasNewer    bool
}

const messageLogicalIDSQL = `
	CASE
		WHEN COALESCE(m.message_data->'metadata'->>'compaction_summary', '') = 'true'
		 AND COALESCE(m.message_data->'metadata'->>'compaction_before_message_id', '') ~ '^[0-9]+$'
		THEN (m.message_data->'metadata'->>'compaction_before_message_id')::BIGINT
		ELSE m.id
	END`

const messageLogicalRankSQL = `
	CASE
		WHEN COALESCE(m.message_data->'metadata'->>'compaction_summary', '') = 'true'
		 AND COALESCE(m.message_data->'metadata'->>'compaction_before_message_id', '') ~ '^[0-9]+$'
		THEN 0
		ELSE 1
	END`

func IsUniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505"
}

var (
	ErrRetryTargetStale       = errors.New("retry target stale")
	ErrMessageAlreadyAnswered = errors.New("message already has answer output")
	ErrMessageUnchanged       = errors.New("message content is unchanged")
	ErrChatRunActive          = errors.New("chat run is still active")
	ErrChatRunTerminal        = errors.New("chat run is no longer running")
	ErrCompactionNotFound     = errors.New("compaction checkpoint not found")
	ErrCompactionUndoDenied   = errors.New("compaction checkpoint cannot be undone")
	ErrCompactionUndoStale    = errors.New("compaction checkpoint has newer messages")
)

func NewMessageRepository(db *sql.DB) *MessageRepository {
	return &MessageRepository{db: db}
}

// dbExecutor 抽象 *sql.DB 与 *sql.Tx 的公共方法，使写入逻辑可在事务内外复用。
type dbExecutor interface {
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
}

func lockActiveSession(ctx context.Context, tx *sql.Tx, sessionID, userID int64) error {
	var found int
	err := tx.QueryRowContext(ctx, `
		SELECT 1 FROM sessions
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
		FOR UPDATE
	`, sessionID, userID).Scan(&found)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	return err
}

func lockActiveChatRun(ctx context.Context, tx *sql.Tx, runID string, sessionID, userID int64) error {
	var found int
	err := tx.QueryRowContext(ctx, `
		SELECT 1 FROM chat_run_reservations
		WHERE run_id = $1 AND session_id = $2 AND user_id = $3 AND status = 'running'
		FOR UPDATE
	`, runID, sessionID, userID).Scan(&found)
	if err == sql.ErrNoRows {
		return ErrChatRunTerminal
	}
	return err
}

// Create 创建消息
func (r *MessageRepository) Create(message *model.Message) error {
	return createMessage(context.Background(), r.db, message)
}

// createMessage 在给定执行器（DB 或事务）上插入一条消息并回填生成列。
func createMessage(ctx context.Context, exec dbExecutor, message *model.Message) error {
	query := `
		INSERT INTO messages (session_id, schema_version, message_data, answer_attempt_id)
		VALUES ($1, $2, $3, $4)
		RETURNING id, role, has_tool_calls, has_reasoning, has_multimodal, answer_attempt_id, created_at, updated_at
	`

	err := exec.QueryRowContext(
		ctx,
		query,
		message.SessionID,
		message.SchemaVersion,
		message.MessageData,
		message.AnswerAttemptID,
	).Scan(
		&message.ID,
		&message.Role,
		&message.HasToolCalls,
		&message.HasReasoning,
		&message.HasMultimodal,
		&message.AnswerAttemptID,
		&message.CreatedAt,
		&message.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to create message: %w", err)
	}

	return nil
}

// CreateBatch 在单个事务内按序写入多条消息：任一条失败整体回滚，
// 避免半截对话落库。成功时回填每条的生成列并返回。
func (r *MessageRepository) CreateBatch(messages []*model.Message) error {
	if len(messages) == 0 {
		return nil
	}
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin batch tx: %w", err)
	}
	defer tx.Rollback()

	for i, message := range messages {
		if err := createMessage(context.Background(), tx, message); err != nil {
			return fmt.Errorf("failed to persist message %d: %w", i, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit batch tx: %w", err)
	}
	return nil
}

func (r *MessageRepository) CreateForActiveSession(ctx context.Context, sessionID, userID int64, message *model.Message) error {
	return r.createBatchForActiveSession(ctx, sessionID, userID, "", []*model.Message{message})
}

func (r *MessageRepository) CreateBatchForActiveRun(ctx context.Context, sessionID, userID int64, runID string, messages []*model.Message) error {
	return r.createBatchForActiveSession(ctx, sessionID, userID, strings.TrimSpace(runID), messages)
}

func (r *MessageRepository) createBatchForActiveSession(ctx context.Context, sessionID, userID int64, runID string, messages []*model.Message) error {
	if len(messages) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin message tx: %w", err)
	}
	defer tx.Rollback()
	if err := lockActiveSession(ctx, tx, sessionID, userID); err != nil {
		return err
	}
	if runID != "" {
		if err := lockActiveChatRun(ctx, tx, runID, sessionID, userID); err != nil {
			return err
		}
	}
	if runID != "" {
		attempt, hasAttempt, err := answerAttemptForRunTx(ctx, tx, runID)
		if err != nil {
			return err
		}
		if hasAttempt {
			bindMessagesToAnswerAttempt(messages, attempt.ID)
		}
	}
	if err := claimStagedAttachmentsForMessages(ctx, tx, sessionID, userID, messages); err != nil {
		return err
	}
	terminalMessageID, err := createMessages(ctx, tx, sessionID, messages)
	if err != nil {
		return err
	}
	if runID != "" {
		result, err := tx.ExecContext(ctx, `
			UPDATE chat_run_reservations
			SET terminal_message_id = COALESCE(NULLIF($2, 0), terminal_message_id), updated_at = NOW()
			WHERE run_id = $1 AND status = 'running'
		`, runID, terminalMessageID)
		if err != nil {
			return fmt.Errorf("attach messages to chat run: %w", err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read attached chat run rows: %w", err)
		}
		if updated != 1 {
			return ErrChatRunTerminal
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET updated_at = NOW() WHERE id = $1`, sessionID); err != nil {
		return fmt.Errorf("failed to touch session activity: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit message tx: %w", err)
	}
	return nil
}

// CreateBatchAndTransitionActiveRun locks the session before the run and
// commits attempt state, attachment claims, terminal messages/event and the
// durable run transition as one fact. Any error rolls all of them back. The
// RunHub caller may publish terminal SSE only after this method returns a
// committed canonical record; a repeated terminal commit reloads that record.
func (r *MessageRepository) CreateBatchAndTransitionActiveRun(ctx context.Context, sessionID, userID int64, runID string, messages []*model.Message, input ChatRunTransitionInput) (ChatRunRecord, bool, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" || input.RunID != runID {
		return ChatRunRecord{}, false, fmt.Errorf("run transition does not match message batch")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return ChatRunRecord{}, false, fmt.Errorf("failed to begin terminal message tx: %w", err)
	}
	defer tx.Rollback()
	if err := lockActiveSession(ctx, tx, sessionID, userID); err != nil {
		return ChatRunRecord{}, false, err
	}
	if err := lockActiveChatRun(ctx, tx, runID, sessionID, userID); err != nil {
		if errors.Is(err, ErrChatRunTerminal) {
			record, terminal, loadErr := loadTerminalChatRunForScope(ctx, tx, runID, sessionID, userID)
			if loadErr != nil {
				return ChatRunRecord{}, false, loadErr
			}
			if terminal {
				return record, false, nil
			}
		}
		return ChatRunRecord{}, false, err
	}
	attempt, hasAttempt, err := answerAttemptForRunTx(ctx, tx, runID)
	if err != nil {
		return ChatRunRecord{}, false, err
	}
	if hasAttempt {
		bindMessagesToAnswerAttempt(messages, attempt.ID)
	}
	if err := claimStagedAttachmentsForMessages(ctx, tx, sessionID, userID, messages); err != nil {
		return ChatRunRecord{}, false, err
	}
	terminalMessageID, err := createMessages(ctx, tx, sessionID, messages)
	if err != nil {
		return ChatRunRecord{}, false, err
	}
	if input.TerminalMessageID == 0 {
		input.TerminalMessageID = terminalMessageID
	}
	input.TerminalEvent = terminalEventWithMessageID(input.TerminalEvent, input.TerminalMessageID)
	record, transitioned, err := transitionChatRun(ctx, tx, input)
	if err != nil {
		return ChatRunRecord{}, false, err
	}
	if !transitioned {
		return ChatRunRecord{}, false, ErrChatRunTerminal
	}
	if hasAttempt {
		if err := finishAnswerAttemptTx(ctx, tx, attempt, input.Status, messages); err != nil {
			return ChatRunRecord{}, false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET updated_at = NOW() WHERE id = $1`, sessionID); err != nil {
		return ChatRunRecord{}, false, fmt.Errorf("failed to touch session activity: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ChatRunRecord{}, false, fmt.Errorf("failed to commit terminal message tx: %w", err)
	}
	return record, true, nil
}

func createMessages(ctx context.Context, tx *sql.Tx, sessionID int64, messages []*model.Message) (int64, error) {
	var terminalMessageID int64
	for index, message := range messages {
		if message.SessionID != sessionID {
			return 0, fmt.Errorf("message %d belongs to another session", index)
		}
		if err := createMessage(ctx, tx, message); err != nil {
			return 0, fmt.Errorf("failed to persist message %d: %w", index, err)
		}
		if message.Role == "assistant" {
			terminalMessageID = message.ID
		}
	}
	return terminalMessageID, nil
}

func terminalEventWithMessageID(raw json.RawMessage, messageID int64) json.RawMessage {
	if len(raw) == 0 || messageID <= 0 {
		return raw
	}
	var payload map[string]interface{}
	if json.Unmarshal(raw, &payload) != nil {
		return raw
	}
	data, ok := payload["data"].(map[string]interface{})
	if !ok {
		data = map[string]interface{}{}
		payload["data"] = data
	}
	data["message_id"] = messageID
	updated, err := json.Marshal(payload)
	if err != nil {
		return raw
	}
	return updated
}

// ListBySession 获取会话的所有有效消息（过滤已压缩消息）
func (r *MessageRepository) ListBySession(sessionID int64) ([]*model.Message, error) {
	return r.ListBySessionContext(context.Background(), sessionID)
}

func (r *MessageRepository) ListBySessionContext(ctx context.Context, sessionID int64) ([]*model.Message, error) {
	query := fmt.Sprintf(`
		SELECT m.id, m.session_id, m.schema_version, m.message_data, m.role,
		       m.has_tool_calls, m.has_reasoning, m.has_multimodal, m.answer_attempt_id,
		       m.compressed_at, m.compression_summary_id, m.created_at, m.updated_at
		FROM messages m
		LEFT JOIN answer_attempts a ON a.id = m.answer_attempt_id
		WHERE m.session_id = $1
		  AND m.deleted_at IS NULL
		  AND (m.compressed_at IS NULL OR m.id = m.compression_summary_id)
		  AND (m.answer_attempt_id IS NULL OR a.selected)
		ORDER BY %s ASC, %s ASC, m.id ASC
	`, messageLogicalIDSQL, messageLogicalRankSQL)

	rows, err := r.db.QueryContext(ctx, query, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to list messages: %w", err)
	}
	defer rows.Close()

	return scanMessages(rows)
}

// GetByID 根据 ID 获取消息
func (r *MessageRepository) GetByID(id int64) (*model.Message, error) {
	message := &model.Message{}
	query := `
		SELECT id, session_id, schema_version, message_data, role,
		       has_tool_calls, has_reasoning, has_multimodal, answer_attempt_id,
		       compressed_at, compression_summary_id, created_at, updated_at
		FROM messages
		WHERE id = $1 AND deleted_at IS NULL
	`

	err := r.db.QueryRow(query, id).Scan(
		&message.ID,
		&message.SessionID,
		&message.SchemaVersion,
		&message.MessageData,
		&message.Role,
		&message.HasToolCalls,
		&message.HasReasoning,
		&message.HasMultimodal,
		&message.AnswerAttemptID,
		&message.CompressedAt,
		&message.CompressionSummaryID,
		&message.CreatedAt,
		&message.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("message not found: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get message: %w", err)
	}

	return message, nil
}

// FindByRunID returns messages persisted for a client run in insertion order.
func (r *MessageRepository) FindByRunID(sessionID int64, runID string, roles []string) ([]*model.Message, error) {
	return r.FindByRunIDContext(context.Background(), sessionID, runID, roles)
}

func (r *MessageRepository) FindByRunIDContext(ctx context.Context, sessionID int64, runID string, roles []string) ([]*model.Message, error) {
	if runID == "" {
		return nil, nil
	}
	args := []interface{}{sessionID, runID}
	roleFilter := ""
	if len(roles) > 0 {
		args = append(args, pq.Array(roles))
		roleFilter = "AND role = ANY($3)"
	}
	query := fmt.Sprintf(`
		SELECT id, session_id, schema_version, message_data, role,
		       has_tool_calls, has_reasoning, has_multimodal, answer_attempt_id,
		       compressed_at, compression_summary_id, created_at, updated_at
		FROM messages
		WHERE session_id = $1
		  AND message_data->'metadata'->>'run_id' = $2
		  AND deleted_at IS NULL
		  %s
		ORDER BY id ASC
	`, roleFilter)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to find messages by run id: %w", err)
	}
	defer rows.Close()

	return scanMessages(rows)
}

// PrepareRetryForActiveSession validates the current visible tail without mutating it.
func (r *MessageRepository) PrepareRetryForActiveSession(ctx context.Context, sessionID, userID, targetMessageID int64) (*model.Message, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin retry tx: %w", err)
	}
	defer tx.Rollback()
	if err := lockActiveSession(ctx, tx, sessionID, userID); err != nil {
		return nil, err
	}
	retryUser, err := prepareRetryForActiveSessionTx(ctx, tx, sessionID, userID, targetMessageID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit retry tx: %w", err)
	}
	return retryUser, nil
}

func (r *MessageRepository) PrepareEditRetryForActiveSession(ctx context.Context, sessionID, userID, targetMessageID int64) (*model.Message, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin edit retry tx: %w", err)
	}
	defer tx.Rollback()
	if err := lockActiveSession(ctx, tx, sessionID, userID); err != nil {
		return nil, err
	}
	editUser, err := prepareEditRetryForActiveSessionTx(ctx, tx, sessionID, userID, targetMessageID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit edit retry tx: %w", err)
	}
	return editUser, nil
}

func prepareRetryForActiveSessionTx(ctx context.Context, tx *sql.Tx, sessionID, userID, targetMessageID int64) (*model.Message, error) {
	messages, err := listRetryMessagesForUpdate(ctx, tx, sessionID)
	if err != nil {
		return nil, err
	}

	targetIndex := -1
	lastRetryable := -1
	for index, message := range messages {
		if isCompactionSummaryMessageData(message.MessageData) {
			continue
		}
		if message.ID == targetMessageID {
			targetIndex = index
		}
		if message.Role == "user" || message.Role == "assistant" {
			lastRetryable = index
		}
	}
	if targetIndex < 0 || targetIndex != lastRetryable {
		return nil, ErrRetryTargetStale
	}

	target := messages[targetIndex]
	retryUser := target
	if target.Role == "assistant" {
		for index := targetIndex - 1; index >= 0; index-- {
			if messages[index].Role == "user" {
				retryUser = messages[index]
				break
			}
		}
		if retryUser == target {
			return nil, ErrRetryTargetStale
		}
	} else if target.Role != "user" {
		return nil, ErrRetryTargetStale
	}
	if err := ensureFormalMessageAttachmentsAvailableTx(ctx, tx, sessionID, userID, retryUser); err != nil {
		return nil, err
	}
	return retryUser, nil
}

func prepareEditRetryForActiveSessionTx(ctx context.Context, tx *sql.Tx, sessionID, userID, targetMessageID int64) (*model.Message, error) {
	messages, err := listRetryMessagesForUpdate(ctx, tx, sessionID)
	if err != nil {
		return nil, err
	}

	targetIndex := -1
	lastUserIndex := -1
	for index, message := range messages {
		if isCompactionSummaryMessageData(message.MessageData) {
			continue
		}
		if message.ID == targetMessageID {
			targetIndex = index
		}
		if message.Role == "user" {
			lastUserIndex = index
		}
	}
	if targetIndex < 0 || targetIndex != lastUserIndex || messages[targetIndex].Role != "user" {
		return nil, ErrRetryTargetStale
	}
	for _, message := range messages[targetIndex+1:] {
		if editRetryMessageHasOutput(message) {
			return nil, ErrMessageAlreadyAnswered
		}
	}

	var hasAttemptOutput bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM messages output
			JOIN answer_attempts attempt ON attempt.id = output.answer_attempt_id
			WHERE attempt.user_message_id = $1
			  AND output.deleted_at IS NULL
			  AND (
			    output.role = 'tool'
			    OR (
			      output.role = 'assistant'
			      AND COALESCE(output.message_data->'metadata'->>'ephemeral_error', 'false') <> 'true'
			      AND (
			        length(BTRIM(COALESCE(output.message_data->>'content', ''))) > 0
			        OR output.has_tool_calls
			        OR output.has_reasoning
			        OR output.has_multimodal
			      )
			    )
			  )
		)
	`, targetMessageID).Scan(&hasAttemptOutput); err != nil {
		return nil, fmt.Errorf("failed to inspect edit retry output: %w", err)
	}
	if hasAttemptOutput {
		return nil, ErrMessageAlreadyAnswered
	}

	target := messages[targetIndex]
	if err := ensureFormalMessageAttachmentsAvailableTx(ctx, tx, sessionID, userID, target); err != nil {
		return nil, err
	}
	return target, nil
}

func listRetryMessagesForUpdate(ctx context.Context, tx *sql.Tx, sessionID int64) ([]*model.Message, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT m.id, m.session_id, m.schema_version, m.message_data, m.role,
		       m.has_tool_calls, m.has_reasoning, m.has_multimodal, m.answer_attempt_id,
		       m.compressed_at, m.compression_summary_id, m.created_at, m.updated_at
		FROM messages m
		LEFT JOIN answer_attempts a ON a.id = m.answer_attempt_id
		WHERE m.session_id = $1
		  AND m.deleted_at IS NULL
		  AND (m.compressed_at IS NULL OR m.id = m.compression_summary_id)
		  AND (m.answer_attempt_id IS NULL OR a.selected)
		ORDER BY m.id ASC
		FOR UPDATE OF m
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to list retry messages: %w", err)
	}
	defer rows.Close()
	messages, err := scanMessages(rows)
	closeErr := rows.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, fmt.Errorf("failed to close retry messages: %w", closeErr)
	}
	return messages, nil
}

func editRetryMessageHasOutput(message *model.Message) bool {
	if message == nil {
		return false
	}
	if message.Role == "tool" {
		return true
	}
	if message.Role != "assistant" {
		return false
	}
	data, err := ParseMessageData(message.MessageData)
	if err != nil {
		return true
	}
	if metadata, ok := data["metadata"].(map[string]interface{}); ok {
		if ephemeral, _ := metadata["ephemeral_error"].(bool); ephemeral {
			return false
		}
	}
	content, _ := data["content"].(string)
	return strings.TrimSpace(content) != "" || message.HasToolCalls || message.HasReasoning || message.HasMultimodal
}

type messageRows interface {
	Next() bool
	Scan(dest ...interface{}) error
	Err() error
}

func scanMessages(rows messageRows) ([]*model.Message, error) {
	messages := []*model.Message{}
	for rows.Next() {
		message := &model.Message{}
		if err := rows.Scan(
			&message.ID,
			&message.SessionID,
			&message.SchemaVersion,
			&message.MessageData,
			&message.Role,
			&message.HasToolCalls,
			&message.HasReasoning,
			&message.HasMultimodal,
			&message.AnswerAttemptID,
			&message.CompressedAt,
			&message.CompressionSummaryID,
			&message.CreatedAt,
			&message.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan message: %w", err)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate messages: %w", err)
	}
	return messages, nil
}

// ListAllBySession 获取会话的全部消息（含已压缩），用于前端展示完整历史。
// 前端需要能回溯压缩之前的消息，因此不做任何 compressed_at 过滤。
func (r *MessageRepository) ListAllBySession(sessionID int64) ([]*model.Message, error) {
	return r.ListAllBySessionContext(context.Background(), sessionID)
}

func (r *MessageRepository) ListAllBySessionContext(ctx context.Context, sessionID int64) ([]*model.Message, error) {
	query := fmt.Sprintf(`
		SELECT m.id, m.session_id, m.schema_version, m.message_data, m.role,
		       m.has_tool_calls, m.has_reasoning, m.has_multimodal, m.answer_attempt_id,
		       m.compressed_at, m.compression_summary_id, m.created_at, m.updated_at
		FROM messages m
		LEFT JOIN answer_attempts a ON a.id = m.answer_attempt_id
		WHERE m.session_id = $1
		  AND m.deleted_at IS NULL
		  AND (m.answer_attempt_id IS NULL OR a.selected)
		ORDER BY %s ASC, %s ASC, m.id ASC
	`, messageLogicalIDSQL, messageLogicalRankSQL)

	rows, err := r.db.QueryContext(ctx, query, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to list all messages: %w", err)
	}
	defer rows.Close()

	return scanMessages(rows)
}

// ListBySessionPaged 按游标分页获取会话消息（含已压缩，用于前端展示完整历史）。
// API 仍传消息 id，查询先还原该消息的逻辑位置；受保护压缩摘要据此排在保留消息之前。
// beforeID<=0 表示取最新一页；否则取该逻辑位置之前的更早一页。
// 内部按逻辑位置倒序取 limit+1 条用于判定 hasMore，返回时砍掉多余项并反转为展示顺序。
func (r *MessageRepository) ListBySessionPaged(sessionID int64, limit int, beforeID int64) ([]*model.Message, bool, error) {
	if limit <= 0 {
		limit = 30
	}
	query := fmt.Sprintf(`
		WITH positioned_messages AS (
			SELECT m.id, m.session_id, m.schema_version, m.message_data, m.role,
			       m.has_tool_calls, m.has_reasoning, m.has_multimodal, m.answer_attempt_id,
			       m.compressed_at, m.compression_summary_id, m.created_at, m.updated_at,
			       %s AS logical_id,
			       %s AS logical_rank
			FROM messages m
			WHERE m.session_id = $1
			  AND m.deleted_at IS NULL
		), ordered_messages AS (
			SELECT m.*
			FROM positioned_messages m
			LEFT JOIN answer_attempts a ON a.id = m.answer_attempt_id
			WHERE m.answer_attempt_id IS NULL OR a.selected
		), cursor_position AS (
			SELECT logical_id, logical_rank, id
			FROM positioned_messages
			WHERE id = $2
		)
		SELECT m.id, m.session_id, m.schema_version, m.message_data, m.role,
		       m.has_tool_calls, m.has_reasoning, m.has_multimodal, m.answer_attempt_id,
		       m.compressed_at, m.compression_summary_id, m.created_at, m.updated_at
		FROM ordered_messages m
		WHERE $2 <= 0
		   OR (m.logical_id, m.logical_rank, m.id) < (
			SELECT logical_id, logical_rank, id FROM cursor_position
		   )
		ORDER BY m.logical_id DESC, m.logical_rank DESC, m.id DESC
		LIMIT $3
	`, messageLogicalIDSQL, messageLogicalRankSQL)

	rows, err := r.db.Query(query, sessionID, beforeID, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("failed to list paged messages: %w", err)
	}
	defer rows.Close()

	messages, err := scanMessages(rows)
	if err != nil {
		return nil, false, err
	}

	hasMore := len(messages) > limit
	if hasMore {
		messages = messages[:limit]
	}
	// 当前为逻辑位置倒序（新→旧），反转成展示顺序供前端渲染。
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	return messages, hasMore, nil
}

func (r *MessageRepository) ListConversationTurns(sessionID int64, limit int, beforeTurnID int64) ([]ConversationTurn, int64, bool, error) {
	if limit <= 0 {
		limit = 500
	}
	if limit > 500 {
		limit = 500
	}
	var total int64
	if err := r.db.QueryRow(`
		SELECT COUNT(*)
		FROM messages
		WHERE session_id = $1
		  AND deleted_at IS NULL
		  AND role = 'user'
		  AND COALESCE(message_data->'metadata'->>'compaction_summary', '') <> 'true'
	`, sessionID).Scan(&total); err != nil {
		return nil, 0, false, fmt.Errorf("count conversation turns: %w", err)
	}

	rows, err := r.db.Query(`
		WITH turns AS (
			SELECT id,
			       ROW_NUMBER() OVER (ORDER BY id ASC) AS sequence,
			       COALESCE(message_data->>'content', '') AS content,
			       created_at
			FROM messages
			WHERE session_id = $1
			  AND deleted_at IS NULL
			  AND role = 'user'
			  AND COALESCE(message_data->'metadata'->>'compaction_summary', '') <> 'true'
		)
		SELECT id, sequence, content, created_at
		FROM turns
		WHERE $2 <= 0 OR id < $2
		ORDER BY id DESC
		LIMIT $3
	`, sessionID, beforeTurnID, limit+1)
	if err != nil {
		return nil, 0, false, fmt.Errorf("list conversation turns: %w", err)
	}
	defer rows.Close()

	turns := make([]ConversationTurn, 0, limit+1)
	for rows.Next() {
		var turn ConversationTurn
		if err := rows.Scan(&turn.ID, &turn.Sequence, &turn.Content, &turn.CreatedAt); err != nil {
			return nil, 0, false, fmt.Errorf("scan conversation turn: %w", err)
		}
		turns = append(turns, turn)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, false, fmt.Errorf("iterate conversation turns: %w", err)
	}
	hasMore := len(turns) > limit
	if hasMore {
		turns = turns[:limit]
	}
	for i, j := 0, len(turns)-1; i < j; i, j = i+1, j-1 {
		turns[i], turns[j] = turns[j], turns[i]
	}
	return turns, total, hasMore, nil
}

func (r *MessageRepository) ListMessageWindow(sessionID int64, mode MessageWindowMode, targetTurnID int64, turnLimit int) (*MessageWindow, error) {
	if mode == "" {
		mode = MessageWindowLatest
	}
	if turnLimit <= 0 {
		turnLimit = 16
	}
	if turnLimit > 16 {
		turnLimit = 16
	}

	turnIDs, err := r.messageWindowTurnIDs(sessionID, mode, targetTurnID, turnLimit)
	if err != nil {
		return nil, err
	}
	window := &MessageWindow{}
	includeCheckpoint := false
	if len(turnIDs) > 0 {
		window.FirstTurnID = turnIDs[0]
		window.LastTurnID = turnIDs[len(turnIDs)-1]

		if err := r.db.QueryRow(`
			SELECT EXISTS (
				SELECT 1 FROM messages
				WHERE session_id = $1 AND deleted_at IS NULL AND role = 'user'
				  AND COALESCE(message_data->'metadata'->>'compaction_summary', '') <> 'true'
				  AND id < $2
			), EXISTS (
				SELECT 1 FROM messages
				WHERE session_id = $1 AND deleted_at IS NULL AND role = 'user'
				  AND COALESCE(message_data->'metadata'->>'compaction_summary', '') <> 'true'
				  AND id > $3
			)
		`, sessionID, window.FirstTurnID, window.LastTurnID).Scan(&window.HasOlder, &window.HasNewer); err != nil {
			return nil, fmt.Errorf("inspect message window boundaries: %w", err)
		}
		// Compression changes the Agent context, not the user's visible history.
		// Attach the active checkpoint to the one UI page containing the final
		// user turn it compressed, so the divider keeps its logical position
		// without replacing old messages or appearing on multiple pages.
		var checkpointAnchor sql.NullInt64
		if err := r.db.QueryRow(`
			WITH active_checkpoint AS (
				SELECT id,
				       CASE
					 WHEN COALESCE(message_data->'metadata'->>'compaction_before_message_id', '') ~ '^[1-9][0-9]*$'
					 THEN (message_data->'metadata'->>'compaction_before_message_id')::BIGINT
					 ELSE NULL
				       END AS boundary
				FROM messages
				WHERE session_id = $1
				  AND deleted_at IS NULL
				  AND compressed_at IS NULL
				  AND COALESCE(message_data->'metadata'->>'compaction_summary', '') = 'true'
				ORDER BY id DESC
				LIMIT 1
			)
			SELECT CASE
				WHEN (SELECT boundary FROM active_checkpoint) IS NOT NULL THEN (
					SELECT MAX(history.id)
					FROM messages history
					WHERE history.session_id = $1
					  AND history.deleted_at IS NULL
					  AND history.role = 'user'
					  AND COALESCE(history.message_data->'metadata'->>'compaction_summary', '') <> 'true'
					  AND history.id < (SELECT boundary FROM active_checkpoint)
				)
				ELSE (
					SELECT MAX(history.id)
					FROM active_checkpoint checkpoint
					JOIN messages history
					  ON history.compression_summary_id = checkpoint.id
					 AND history.id <> checkpoint.id
					WHERE history.deleted_at IS NULL
					  AND history.role = 'user'
					  AND COALESCE(history.message_data->'metadata'->>'compaction_summary', '') <> 'true'
				)
			END
		`, sessionID).Scan(&checkpointAnchor); err != nil {
			return nil, fmt.Errorf("locate active checkpoint anchor: %w", err)
		}
		if checkpointAnchor.Valid {
			for _, turnID := range turnIDs {
				if turnID == checkpointAnchor.Int64 {
					includeCheckpoint = true
					break
				}
			}
		}
	}

	query := fmt.Sprintf(`
		WITH visible_messages AS (
			SELECT m.*,
			       CASE
				 WHEN m.role = 'user' AND COALESCE(m.message_data->'metadata'->>'compaction_summary', '') <> 'true' THEN m.id
				 WHEN m.answer_attempt_id IS NOT NULL THEN a.user_message_id
				 ELSE NULL
			       END AS turn_id,
			       %s AS logical_id,
			       %s AS logical_rank
			FROM messages m
			LEFT JOIN answer_attempts a ON a.id = m.answer_attempt_id
			WHERE m.session_id = $1
			  AND m.deleted_at IS NULL
			  AND (m.answer_attempt_id IS NULL OR a.selected)
		), window_messages AS (
			SELECT *
			FROM visible_messages
			WHERE turn_id = ANY($2::BIGINT[])
			   OR (
				$3
				AND compressed_at IS NULL
				AND COALESCE(message_data->'metadata'->>'compaction_summary', '') = 'true'
			   )
		)
		SELECT id, session_id, schema_version, message_data, role,
		       has_tool_calls, has_reasoning, has_multimodal, answer_attempt_id,
		       compressed_at, compression_summary_id, created_at, updated_at
		FROM window_messages
		ORDER BY logical_id ASC, logical_rank ASC, id ASC
	`, messageLogicalIDSQL, messageLogicalRankSQL)
	rows, err := r.db.Query(query, sessionID, pq.Array(turnIDs), includeCheckpoint)
	if err != nil {
		return nil, fmt.Errorf("list message window: %w", err)
	}
	defer rows.Close()
	window.Messages, err = scanMessages(rows)
	if err != nil {
		return nil, err
	}
	return window, nil
}

func (r *MessageRepository) messageWindowTurnIDs(sessionID int64, mode MessageWindowMode, targetTurnID int64, limit int) ([]int64, error) {
	if mode == "" {
		mode = MessageWindowLatest
	}
	if mode != MessageWindowLatest && targetTurnID <= 0 {
		return nil, ErrNotFound
	}

	var query string
	var args []interface{}
	switch mode {
	case MessageWindowLatest:
		query = `
			SELECT id FROM messages
			WHERE session_id = $1 AND deleted_at IS NULL AND role = 'user'
			  AND COALESCE(message_data->'metadata'->>'compaction_summary', '') <> 'true'
			ORDER BY id DESC LIMIT $2`
		args = []interface{}{sessionID, limit}
	case MessageWindowBefore:
		query = `
			SELECT id FROM messages
			WHERE session_id = $1 AND deleted_at IS NULL AND role = 'user'
			  AND COALESCE(message_data->'metadata'->>'compaction_summary', '') <> 'true'
			  AND id < $2
			ORDER BY id DESC LIMIT $3`
		args = []interface{}{sessionID, targetTurnID, limit}
	case MessageWindowAfter:
		query = `
			SELECT id FROM messages
			WHERE session_id = $1 AND deleted_at IS NULL AND role = 'user'
			  AND COALESCE(message_data->'metadata'->>'compaction_summary', '') <> 'true'
			  AND id > $2
			ORDER BY id ASC LIMIT $3`
		args = []interface{}{sessionID, targetTurnID, limit}
	case MessageWindowAround:
		query = `
			WITH turns AS (
				SELECT id,
				       ROW_NUMBER() OVER (ORDER BY id ASC) AS position,
				       COUNT(*) OVER () AS total
				FROM messages
				WHERE session_id = $1 AND deleted_at IS NULL AND role = 'user'
				  AND COALESCE(message_data->'metadata'->>'compaction_summary', '') <> 'true'
			), target AS (
				SELECT position, total FROM turns WHERE id = $2
			), bounds AS (
				SELECT GREATEST(
					1,
					LEAST(
						position - (($3 - 1) / 2),
						GREATEST(total - $3 + 1, 1)
					)
				) AS start_position
				FROM target
			)
			SELECT id FROM turns
			WHERE position BETWEEN (SELECT start_position FROM bounds)
			                   AND (SELECT start_position FROM bounds) + $3 - 1
			ORDER BY id ASC`
		args = []interface{}{sessionID, targetTurnID, limit}
	default:
		return nil, fmt.Errorf("invalid message window mode")
	}

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("select message window turns: %w", err)
	}
	defer rows.Close()
	ids := make([]int64, 0, limit)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan message window turn: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate message window turns: %w", err)
	}
	if mode == MessageWindowLatest || mode == MessageWindowBefore {
		for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
			ids[i], ids[j] = ids[j], ids[i]
		}
	}
	if len(ids) == 0 && mode == MessageWindowAround {
		return nil, ErrNotFound
	}
	return ids, nil
}

// CountBySessions 批量统计多个会话的消息数。
//
// 侧边栏一次默认加载 100 个会话；如果逐个 CountBySession，会把一次列表请求放大成
// 101 次 SQL。这里用 GROUP BY 一次带回所有计数，缺失的会话由调用方按 0 处理。
func (r *MessageRepository) CountBySessions(sessionIDs []int64) (map[int64]int, error) {
	counts := make(map[int64]int, len(sessionIDs))
	if len(sessionIDs) == 0 {
		return counts, nil
	}
	query := `
		SELECT m.session_id, COUNT(*)
		FROM messages m
		LEFT JOIN answer_attempts a ON a.id = m.answer_attempt_id
		WHERE m.deleted_at IS NULL
		  AND m.session_id = ANY($1)
		  AND (m.answer_attempt_id IS NULL OR a.selected)
		GROUP BY m.session_id
	`
	rows, err := r.db.Query(query, pq.Array(sessionIDs))
	if err != nil {
		return nil, fmt.Errorf("failed to count messages by sessions: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var sessionID int64
		var count int
		if err := rows.Scan(&sessionID, &count); err != nil {
			return nil, fmt.Errorf("failed to scan message count: %w", err)
		}
		counts[sessionID] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate message counts: %w", err)
	}
	return counts, nil
}

// MarkAsCompressed 标记消息为已压缩
func (r *MessageRepository) MarkAsCompressed(sessionID int64, beforeMessageID, summaryMessageID int64) error {
	return markAsCompressed(context.Background(), r.db, sessionID, beforeMessageID, summaryMessageID)
}

func markAsCompressed(ctx context.Context, exec dbExecutor, sessionID int64, beforeMessageID, summaryMessageID int64) error {
	// A checkpoint is stored after the messages it summarizes but is ordered at
	// compaction_before_message_id. Supersession must therefore compare that
	// logical boundary as well as the physical row id, or a checkpoint that sits
	// at/after the new physical boundary can remain active beside its successor.
	query := `
		UPDATE messages
		SET compressed_at = NOW(), compression_summary_id = $1
		WHERE session_id = $2
		  AND deleted_at IS NULL
		  AND (compressed_at IS NULL OR id = compression_summary_id)
		  AND (
			id < $3
			OR (
				compressed_at IS NULL
				AND COALESCE(message_data->'metadata'->>'compaction_summary', '') = 'true'
				AND COALESCE(message_data->'metadata'->>'compaction_before_message_id', '') ~ '^[1-9][0-9]*$'
				AND (message_data->'metadata'->>'compaction_before_message_id')::BIGINT < $3
			)
		  )
	`

	_, err := exec.ExecContext(ctx, query, summaryMessageID, sessionID, beforeMessageID)
	if err != nil {
		return fmt.Errorf("failed to mark messages as compressed: %w", err)
	}

	return nil
}

// PersistCheckpoint 原子地落一次压缩检查点：在单事务内先写摘要消息（回填其 ID），
// 再把 beforeMessageID 之前的消息标记为已压缩并指向该摘要。任一步失败整体回滚，
// 杜绝「孤立摘要」或「旧消息未标记导致前端重复显示」的中间态。
func (r *MessageRepository) PersistCheckpoint(summary *model.Message, beforeMessageID int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin checkpoint tx: %w", err)
	}
	defer tx.Rollback()

	if err := createMessage(context.Background(), tx, summary); err != nil {
		return fmt.Errorf("failed to persist compression summary: %w", err)
	}
	if err := markAsCompressed(context.Background(), tx, summary.SessionID, beforeMessageID, summary.ID); err != nil {
		return fmt.Errorf("failed to mark messages as compressed: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit checkpoint tx: %w", err)
	}
	return nil
}

func (r *MessageRepository) PersistCheckpointForActiveSession(ctx context.Context, sessionID, userID int64, summary *model.Message, beforeMessageID int64) error {
	return r.persistCheckpointForActiveSession(ctx, sessionID, userID, summary, beforeMessageID)
}

func (r *MessageRepository) persistCheckpointForActiveSession(ctx context.Context, sessionID, userID int64, summary *model.Message, beforeMessageID int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin checkpoint tx: %w", err)
	}
	defer tx.Rollback()
	if err := lockActiveSession(ctx, tx, sessionID, userID); err != nil {
		return err
	}
	if summary.SessionID != sessionID {
		return fmt.Errorf("summary belongs to another session")
	}
	if err := createMessage(ctx, tx, summary); err != nil {
		return fmt.Errorf("failed to persist compression summary: %w", err)
	}
	if err := markAsCompressed(ctx, tx, sessionID, beforeMessageID, summary.ID); err != nil {
		return fmt.Errorf("failed to mark messages as compressed: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit checkpoint tx: %w", err)
	}
	return nil
}

// PersistCheckpointAndTransitionActiveRun locks the session selection before
// the run, then commits the summary, compressed-message pointers and terminal
// run record together. A revision conflict or any write failure leaves no
// partial checkpoint. As with message terminals, callers publish terminal SSE
// only from the canonical record returned after commit.
func (r *MessageRepository) PersistCheckpointAndTransitionActiveRun(ctx context.Context, sessionID, userID int64, runID string, summary *model.Message, beforeMessageID int64, input ChatRunTransitionInput, expectedAnswerSelectionRevision *int64) (ChatRunRecord, bool, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" || input.RunID != runID {
		return ChatRunRecord{}, false, fmt.Errorf("run transition does not match compression checkpoint")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return ChatRunRecord{}, false, fmt.Errorf("failed to begin terminal checkpoint tx: %w", err)
	}
	defer tx.Rollback()
	if input.Status == "completed" {
		answerSelectionRevision, err := lockActiveSessionAnswerSelectionRevision(ctx, tx, sessionID, userID)
		if err != nil {
			return ChatRunRecord{}, false, err
		}
		if err := ensureAnswerSelectionRevision(answerSelectionRevision, expectedAnswerSelectionRevision); err != nil {
			if errors.Is(err, ErrAnswerSelectionRevisionConflict) {
				record, terminal, loadErr := loadTerminalChatRunForScope(ctx, tx, runID, sessionID, userID)
				if loadErr != nil {
					return ChatRunRecord{}, false, loadErr
				}
				if terminal {
					return record, false, nil
				}
			}
			return ChatRunRecord{}, false, err
		}
	}
	if err := lockActiveChatRun(ctx, tx, runID, sessionID, userID); err != nil {
		if errors.Is(err, ErrChatRunTerminal) {
			record, terminal, loadErr := loadTerminalChatRunForScope(ctx, tx, runID, sessionID, userID)
			if loadErr != nil {
				return ChatRunRecord{}, false, loadErr
			}
			if terminal {
				return record, false, nil
			}
		}
		return ChatRunRecord{}, false, err
	}
	if input.Status == "completed" {
		if summary == nil || summary.SessionID != sessionID {
			return ChatRunRecord{}, false, fmt.Errorf("summary belongs to another session")
		}
		if err := createMessage(ctx, tx, summary); err != nil {
			return ChatRunRecord{}, false, fmt.Errorf("failed to persist compression summary: %w", err)
		}
		if err := markAsCompressed(ctx, tx, sessionID, beforeMessageID, summary.ID); err != nil {
			return ChatRunRecord{}, false, fmt.Errorf("failed to mark messages as compressed: %w", err)
		}
	}
	record, transitioned, err := transitionChatRun(ctx, tx, input)
	if err != nil {
		return ChatRunRecord{}, false, err
	}
	if !transitioned {
		return ChatRunRecord{}, false, ErrChatRunTerminal
	}
	if err := tx.Commit(); err != nil {
		return ChatRunRecord{}, false, fmt.Errorf("failed to commit terminal checkpoint tx: %w", err)
	}
	return record, true, nil
}

// UndoCompressionCheckpoint 撤销一次压缩检查点（原子事务）：
//  1. 清除被该摘要压缩的所有消息的 compressed_at / compression_summary_id（恢复 Agent 原文上下文）
//  2. 软删除摘要消息本身
//
// 返回被恢复的消息条数。UI 历史始终可见；撤销后 ListBySession 也重新返回完整原文。
func (r *MessageRepository) UndoCompressionCheckpoint(sessionID, summaryMessageID int64) (int64, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin undo tx: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.Exec(`
		UPDATE messages
		SET compressed_at = NULL, compression_summary_id = NULL
		WHERE session_id = $1 AND compression_summary_id = $2 AND deleted_at IS NULL
	`, sessionID, summaryMessageID)
	if err != nil {
		return 0, fmt.Errorf("failed to restore compressed messages: %w", err)
	}
	restored, _ := res.RowsAffected()

	if _, err := tx.Exec(`
		UPDATE messages SET deleted_at = NOW()
		WHERE id = $1 AND session_id = $2 AND deleted_at IS NULL
	`, summaryMessageID, sessionID); err != nil {
		return 0, fmt.Errorf("failed to soft-delete summary message: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit undo tx: %w", err)
	}
	return restored, nil
}

func (r *MessageRepository) UndoLatestManualCheckpointForActiveSession(ctx context.Context, sessionID, userID int64) (int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to begin undo tx: %w", err)
	}
	defer tx.Rollback()
	if err := lockActiveSession(ctx, tx, sessionID, userID); err != nil {
		return 0, err
	}

	var summaryMessageID int64
	err = tx.QueryRowContext(ctx, `
		SELECT id
		FROM messages
		WHERE session_id = $1
		  AND deleted_at IS NULL
		  AND message_data->'metadata'->>'compaction_summary' = 'true'
		ORDER BY id DESC
		LIMIT 1
	`, sessionID).Scan(&summaryMessageID)
	if err == sql.ErrNoRows {
		return 0, ErrCompactionNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("failed to find latest compaction checkpoint: %w", err)
	}

	var kind string
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(message_data->'metadata'->>'compaction_kind', '')
		FROM messages WHERE id = $1
	`, summaryMessageID).Scan(&kind); err != nil {
		return 0, fmt.Errorf("failed to read compaction checkpoint: %w", err)
	}
	if kind != "manual" {
		return 0, ErrCompactionUndoDenied
	}

	var hasNewMessages bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM messages
			WHERE session_id = $1
			  AND id > $2
			  AND deleted_at IS NULL
			  AND COALESCE(message_data->'metadata'->>'ephemeral_error', 'false') <> 'true'
		)
	`, sessionID, summaryMessageID).Scan(&hasNewMessages); err != nil {
		return 0, fmt.Errorf("failed to validate compaction checkpoint: %w", err)
	}
	if hasNewMessages {
		return 0, ErrCompactionUndoStale
	}

	res, err := tx.ExecContext(ctx, `
		UPDATE messages
		SET compressed_at = NULL, compression_summary_id = NULL
		WHERE session_id = $1 AND compression_summary_id = $2 AND deleted_at IS NULL
	`, sessionID, summaryMessageID)
	if err != nil {
		return 0, fmt.Errorf("failed to restore compressed messages: %w", err)
	}
	restored, _ := res.RowsAffected()
	if _, err := tx.ExecContext(ctx, `
		UPDATE messages SET deleted_at = NOW()
		WHERE id = $1 AND session_id = $2 AND deleted_at IS NULL
	`, summaryMessageID, sessionID); err != nil {
		return 0, fmt.Errorf("failed to soft-delete summary message: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit undo tx: %w", err)
	}
	return restored, nil
}

// ParseMessageData 解析 message_data JSONB 为 map
func ParseMessageData(data []byte) (map[string]interface{}, error) {
	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse message data: %w", err)
	}
	return result, nil
}

func isCompactionSummaryMessageData(raw []byte) bool {
	data, err := ParseMessageData(raw)
	if err != nil {
		return false
	}
	metadata, ok := data["metadata"].(map[string]interface{})
	if !ok {
		return false
	}
	flag, _ := metadata["compaction_summary"].(bool)
	return flag
}
