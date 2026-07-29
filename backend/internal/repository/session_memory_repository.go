package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	sessionmemory "github.com/huoguojun123/EffChat/internal/memory"
)

var (
	ErrMemoryChangeNotFound    = errors.New("memory change not found")
	ErrMemoryChangeNotUndoable = errors.New("only the latest memory compaction can be undone")
	ErrSessionMemoryConflict   = errors.New("session memory changed before save")
)

// SessionMemoryRepository 管理每会话一条的持久记忆（session_memories 表）。
// 记忆由 memory 工具读写，不进压缩，每轮注入系统提示。
type SessionMemoryRepository struct {
	db *sql.DB
}

type SessionMemoryChange struct {
	ID            int64      `json:"id"`
	SessionID     int64      `json:"session_id"`
	UserID        int64      `json:"user_id"`
	Source        string     `json:"source"`
	Action        string     `json:"action"`
	BeforeContent string     `json:"before_content,omitempty"`
	AfterContent  string     `json:"after_content,omitempty"`
	Summary       string     `json:"summary"`
	CreatedAt     time.Time  `json:"created_at"`
	UndoneAt      *time.Time `json:"undone_at,omitempty"`
}

func NewSessionMemoryRepository(db *sql.DB) *SessionMemoryRepository {
	return &SessionMemoryRepository{db: db}
}

// Get 返回会话记忆正文；无记录时返回空串（非错误）。
func (r *SessionMemoryRepository) Get(sessionID int64) (string, error) {
	var content string
	err := r.db.QueryRow(
		`SELECT content FROM session_memories WHERE session_id = $1`, sessionID,
	).Scan(&content)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to get session memory: %w", err)
	}
	return content, nil
}

// Set upsert 会话记忆正文（整体覆盖）。
func (r *SessionMemoryRepository) Set(sessionID int64, content string) error {
	_, err := r.db.Exec(`
		INSERT INTO session_memories (session_id, content, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (session_id) DO UPDATE SET content = EXCLUDED.content, updated_at = NOW()
	`, sessionID, content)
	if err != nil {
		return fmt.Errorf("failed to set session memory: %w", err)
	}
	return nil
}

func (r *SessionMemoryRepository) SetWithChange(ctx context.Context, sessionID, userID int64, content, source, action, summary string, maxChars int) error {
	_, err := r.SaveWithChange(ctx, SaveSessionMemoryInput{
		SessionID: sessionID,
		UserID:    userID,
		Content:   content,
		Source:    source,
		Action:    action,
		Summary:   summary,
		MaxChars:  maxChars,
	})
	return err
}

func (r *SessionMemoryRepository) CompareAndSetWithChange(ctx context.Context, sessionID, userID int64, expectedBefore, content, source, action, summary string, maxChars int) (bool, error) {
	_, err := r.SaveWithChange(ctx, SaveSessionMemoryInput{
		SessionID:      sessionID,
		UserID:         userID,
		Content:        content,
		Source:         source,
		Action:         action,
		Summary:        summary,
		ExpectedBefore: expectedBefore,
		CheckBefore:    true,
		MaxChars:       maxChars,
	})
	if errors.Is(err, ErrSessionMemoryConflict) {
		return false, nil
	}
	return err == nil, err
}

func (r *SessionMemoryRepository) GetWithUpdatedAt(ctx context.Context, sessionID int64) (string, time.Time, error) {
	var content string
	var updatedAt time.Time
	err := r.db.QueryRowContext(
		ctx,
		`SELECT content, updated_at FROM session_memories WHERE session_id = $1`, sessionID,
	).Scan(&content, &updatedAt)
	if err == sql.ErrNoRows {
		return "", time.Time{}, nil
	}
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to get session memory: %w", err)
	}
	return content, updatedAt, nil
}

type SaveSessionMemoryInput struct {
	SessionID                       int64
	UserID                          int64
	MemoryEnabled                   *bool
	Content                         string
	Source                          string
	Action                          string
	Summary                         string
	ExpectedBefore                  string
	CheckBefore                     bool
	ExpectedAnswerSelectionRevision *int64
	MaxChars                        int
}

func (r *SessionMemoryRepository) SaveWithChange(ctx context.Context, input SaveSessionMemoryInput) (*SessionMemoryChange, error) {
	if input.SessionID <= 0 || input.UserID <= 0 {
		return nil, fmt.Errorf("session_id and user_id are required")
	}
	content, _, err := sessionmemory.NormalizeWithLimits(input.Content, sessionmemory.NormalizeLimits(input.MaxChars, 0))
	if err != nil {
		return nil, err
	}
	source := normalizeMemoryChangeValue(input.Source, "manual")
	action := normalizeMemoryChangeValue(input.Action, "update")
	summary := strings.TrimSpace(input.Summary)
	if summary == "" {
		summary = "updated session memory"
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	answerSelectionRevision, err := lockActiveSessionAnswerSelectionRevision(ctx, tx, input.SessionID, input.UserID)
	if err != nil {
		return nil, fmt.Errorf("session not found or access denied: %w", err)
	}
	if err := ensureAnswerSelectionRevision(answerSelectionRevision, input.ExpectedAnswerSelectionRevision); err != nil {
		return nil, err
	}
	if input.MemoryEnabled != nil {
		result, err := tx.ExecContext(ctx, `
			UPDATE sessions
			SET memory_enabled = $1, updated_at = NOW()
			WHERE id = $2 AND user_id = $3 AND deleted_at IS NULL
		`, *input.MemoryEnabled, input.SessionID, input.UserID)
		if err != nil {
			return nil, fmt.Errorf("failed to update session memory setting: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil || rows != 1 {
			return nil, fmt.Errorf("session not found or access denied")
		}
	}

	before, err := getSessionMemoryForUpdate(ctx, tx, input.SessionID)
	if err != nil {
		return nil, err
	}
	if input.CheckBefore && strings.TrimSpace(before) != strings.TrimSpace(input.ExpectedBefore) {
		return nil, ErrSessionMemoryConflict
	}
	if strings.TrimSpace(before) == strings.TrimSpace(content) {
		if input.MemoryEnabled != nil {
			if err := tx.Commit(); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}
	if err := upsertSessionMemory(ctx, tx, input.SessionID, content); err != nil {
		return nil, err
	}
	change, err := insertSessionMemoryChange(ctx, tx, input.SessionID, input.UserID, source, action, before, content, summary)
	if err != nil {
		return nil, err
	}
	if err := pruneSessionMemoryChanges(ctx, tx, input.SessionID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return change, nil
}

func (r *SessionMemoryRepository) ListChanges(ctx context.Context, sessionID, userID int64, limit int) ([]SessionMemoryChange, error) {
	if limit <= 0 || limit > sessionmemory.MaxChangeList {
		limit = sessionmemory.MaxChangeList
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, session_id, user_id, source, action, before_content, after_content, summary, created_at, undone_at
		FROM session_memory_changes
		WHERE session_id = $1 AND user_id = $2
		ORDER BY created_at DESC, id DESC
		LIMIT $3
	`, sessionID, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list session memory changes: %w", err)
	}
	defer rows.Close()
	changes := make([]SessionMemoryChange, 0, limit)
	for rows.Next() {
		var change SessionMemoryChange
		if err := rows.Scan(&change.ID, &change.SessionID, &change.UserID, &change.Source, &change.Action, &change.BeforeContent, &change.AfterContent, &change.Summary, &change.CreatedAt, &change.UndoneAt); err != nil {
			return nil, err
		}
		changes = append(changes, change)
	}
	return changes, rows.Err()
}

func (r *SessionMemoryRepository) UndoChange(ctx context.Context, sessionID, userID, changeID int64) (*SessionMemoryChange, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var target SessionMemoryChange
	err = tx.QueryRowContext(ctx, `
		SELECT id, session_id, user_id, source, action, before_content, after_content, summary, created_at, undone_at
		FROM session_memory_changes
		WHERE id = $1 AND session_id = $2 AND user_id = $3
		FOR UPDATE
	`, changeID, sessionID, userID).Scan(&target.ID, &target.SessionID, &target.UserID, &target.Source, &target.Action, &target.BeforeContent, &target.AfterContent, &target.Summary, &target.CreatedAt, &target.UndoneAt)
	if err == sql.ErrNoRows {
		return nil, ErrMemoryChangeNotFound
	}
	if err != nil {
		return nil, err
	}
	if target.UndoneAt != nil || target.Source != "compact" || target.Action != "compact" {
		return nil, ErrMemoryChangeNotUndoable
	}
	current, err := getSessionMemoryForUpdate(ctx, tx, sessionID)
	if err != nil {
		return nil, err
	}
	var latestID int64
	err = tx.QueryRowContext(ctx, `
		SELECT id
		FROM session_memory_changes
		WHERE session_id = $1 AND user_id = $2
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, sessionID, userID).Scan(&latestID)
	if err != nil {
		return nil, err
	}
	if latestID != target.ID {
		return nil, ErrMemoryChangeNotUndoable
	}
	if strings.TrimSpace(current) != strings.TrimSpace(target.AfterContent) {
		return nil, ErrMemoryChangeNotUndoable
	}
	if err := upsertSessionMemory(ctx, tx, sessionID, target.BeforeContent); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE session_memory_changes SET undone_at = NOW() WHERE id = $1`, changeID); err != nil {
		return nil, err
	}
	change, err := insertSessionMemoryChange(ctx, tx, sessionID, userID, "undo", "undo", current, target.BeforeContent, "undid memory change")
	if err != nil {
		return nil, err
	}
	if err := pruneSessionMemoryChanges(ctx, tx, sessionID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return change, nil
}

func getSessionMemoryForUpdate(ctx context.Context, tx *sql.Tx, sessionID int64) (string, error) {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO session_memories (session_id, content, updated_at)
		VALUES ($1, '', NOW())
		ON CONFLICT (session_id) DO NOTHING
	`, sessionID); err != nil {
		return "", fmt.Errorf("failed to initialize session memory: %w", err)
	}
	var before string
	err := tx.QueryRowContext(ctx, `SELECT content FROM session_memories WHERE session_id = $1 FOR UPDATE`, sessionID).Scan(&before)
	if err != nil {
		return "", fmt.Errorf("failed to lock session memory: %w", err)
	}
	return before, nil
}

func upsertSessionMemory(ctx context.Context, tx *sql.Tx, sessionID int64, content string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO session_memories (session_id, content, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (session_id) DO UPDATE SET content = EXCLUDED.content, updated_at = NOW()
	`, sessionID, content)
	if err != nil {
		return fmt.Errorf("failed to set session memory: %w", err)
	}
	return nil
}

func insertSessionMemoryChange(ctx context.Context, tx *sql.Tx, sessionID, userID int64, source, action, before, after, summary string) (*SessionMemoryChange, error) {
	change := &SessionMemoryChange{}
	err := tx.QueryRowContext(ctx, `
		INSERT INTO session_memory_changes (session_id, user_id, source, action, before_content, after_content, summary)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, session_id, user_id, source, action, before_content, after_content, summary, created_at, undone_at
	`, sessionID, userID, source, action, before, after, summary).Scan(
		&change.ID, &change.SessionID, &change.UserID, &change.Source, &change.Action,
		&change.BeforeContent, &change.AfterContent, &change.Summary, &change.CreatedAt, &change.UndoneAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert session memory change: %w", err)
	}
	return change, nil
}

func pruneSessionMemoryChanges(ctx context.Context, tx *sql.Tx, sessionID int64) error {
	_, err := tx.ExecContext(ctx, `
		DELETE FROM session_memory_changes
		WHERE session_id = $1
		  AND id NOT IN (
		    SELECT id FROM session_memory_changes
		    WHERE session_id = $1
		    ORDER BY created_at DESC, id DESC
		    LIMIT $2
		  )
	`, sessionID, sessionmemory.MaxChangeList)
	return err
}

func normalizeMemoryChangeValue(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
