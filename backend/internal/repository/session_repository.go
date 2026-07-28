package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/huoguojun123/effchat/internal/model"
)

type SessionRepository struct {
	db *sql.DB
}

type SessionPatch struct {
	ModelID       *string
	Provider      *string
	Title         *string
	FolderIDSet   bool
	FolderID      *int64
	SystemPrompt  *string
	Temperature   *float64
	MaxTokens     *int
	SearchMode    *string
	MemoryEnabled *bool
	Pinned        *bool
}

func NewSessionRepository(db *sql.DB) *SessionRepository {
	return &SessionRepository{db: db}
}

// Create 创建会话
func (r *SessionRepository) Create(session *model.Session) error {
	// 兜底：search_mode 有 CHECK 约束（off/auto/on），空值会被拒绝。
	// 直接构造 model.Session 的调用方（含测试）可能不设此字段，统一回退到 auto。
	if session.SearchMode == "" {
		session.SearchMode = "auto"
	}
	query := `
		INSERT INTO sessions (user_id, title, title_generated, model_id, provider,
		                      folder_id, system_prompt, temperature, max_tokens, message_format, search_mode, memory_enabled, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id, created_at, updated_at
	`

	err := r.db.QueryRow(
		query,
		session.UserID,
		session.Title,
		session.TitleGenerated,
		session.ModelID,
		session.Provider,
		session.FolderID,
		session.SystemPrompt,
		session.Temperature,
		session.MaxTokens,
		session.MessageFormat,
		session.SearchMode,
		session.MemoryEnabled,
		session.Metadata,
	).Scan(&session.ID, &session.CreatedAt, &session.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}

	return nil
}

// GetByID 根据 ID 获取会话
func (r *SessionRepository) GetByID(id, userID int64) (*model.Session, error) {
	return r.GetByIDContext(context.Background(), id, userID)
}

func (r *SessionRepository) GetByIDContext(ctx context.Context, id, userID int64) (*model.Session, error) {
	session := &model.Session{}
	query := `
		SELECT id, user_id, title, title_generated, model_id, provider,
		       folder_id, pinned_at, system_prompt, temperature, max_tokens, message_format, search_mode, memory_enabled, answer_selection_revision, metadata,
		       created_at, updated_at, deleted_at
		FROM sessions
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
	`

	err := r.db.QueryRowContext(ctx, query, id, userID).Scan(
		&session.ID,
		&session.UserID,
		&session.Title,
		&session.TitleGenerated,
		&session.ModelID,
		&session.Provider,
		&session.FolderID,
		&session.PinnedAt,
		&session.SystemPrompt,
		&session.Temperature,
		&session.MaxTokens,
		&session.MessageFormat,
		&session.SearchMode,
		&session.MemoryEnabled,
		&session.AnswerSelectionRevision,
		&session.Metadata,
		&session.CreatedAt,
		&session.UpdatedAt,
		&session.DeletedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("session not found: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	return session, nil
}

// ListByUser 获取用户的会话列表
func (r *SessionRepository) ListByUser(userID int64, limit, offset int, folderID *int64, unfiled bool) ([]*model.Session, error) {
	query := `
		SELECT id, user_id, title, title_generated, model_id, provider,
		       folder_id, pinned_at, system_prompt, temperature, max_tokens, message_format, search_mode, memory_enabled, answer_selection_revision, metadata,
		       created_at, updated_at
		FROM sessions
		WHERE user_id = $1
		  AND deleted_at IS NULL
		  AND ($4::BOOLEAN = false OR folder_id IS NULL)
		  AND ($5::BIGINT IS NULL OR folder_id = $5)
		ORDER BY pinned_at DESC NULLS LAST, updated_at DESC, id DESC
		LIMIT $2 OFFSET $3
	`

	var folderArg interface{}
	if folderID != nil {
		folderArg = *folderID
	}

	rows, err := r.db.Query(query, userID, limit, offset, unfiled, folderArg)
	if err != nil {
		return nil, fmt.Errorf("failed to list sessions: %w", err)
	}
	defer rows.Close()

	sessions := []*model.Session{}
	for rows.Next() {
		session := &model.Session{}
		err := rows.Scan(
			&session.ID,
			&session.UserID,
			&session.Title,
			&session.TitleGenerated,
			&session.ModelID,
			&session.Provider,
			&session.FolderID,
			&session.PinnedAt,
			&session.SystemPrompt,
			&session.Temperature,
			&session.MaxTokens,
			&session.MessageFormat,
			&session.SearchMode,
			&session.MemoryEnabled,
			&session.AnswerSelectionRevision,
			&session.Metadata,
			&session.CreatedAt,
			&session.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan session: %w", err)
		}
		sessions = append(sessions, session)
	}

	return sessions, nil
}

func (r *SessionRepository) UpdateFields(sessionID, userID int64, patch SessionPatch) error {
	result, err := r.db.Exec(`
		UPDATE sessions
		SET model_id = CASE WHEN $1 THEN $2 ELSE model_id END,
		    provider = CASE WHEN $3 THEN $4 ELSE provider END,
		    title = CASE WHEN $5 THEN $6 ELSE title END,
		    folder_id = CASE WHEN $7 THEN $8 ELSE folder_id END,
		    system_prompt = CASE WHEN $9 THEN $10 ELSE system_prompt END,
		    temperature = CASE WHEN $11 THEN $12 ELSE temperature END,
		    max_tokens = CASE WHEN $13 THEN $14 ELSE max_tokens END,
		    search_mode = CASE WHEN $15 THEN $16 ELSE search_mode END,
		    memory_enabled = CASE WHEN $17 THEN $18 ELSE memory_enabled END,
		    pinned_at = CASE WHEN $19 THEN CASE WHEN $20 THEN NOW() ELSE NULL END ELSE pinned_at END
		WHERE id = $21 AND user_id = $22 AND deleted_at IS NULL
	`,
		patch.ModelID != nil, patch.ModelID,
		patch.Provider != nil, patch.Provider,
		patch.Title != nil, patch.Title,
		patch.FolderIDSet, patch.FolderID,
		patch.SystemPrompt != nil, patch.SystemPrompt,
		patch.Temperature != nil, patch.Temperature,
		patch.MaxTokens != nil, patch.MaxTokens,
		patch.SearchMode != nil, patch.SearchMode,
		patch.MemoryEnabled != nil, patch.MemoryEnabled,
		patch.Pinned != nil, patch.Pinned,
		sessionID, userID,
	)
	if err != nil {
		return fmt.Errorf("failed to update session fields: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("session not found or already deleted")
	}
	return nil
}

func (r *SessionRepository) UpdateEnabledSkills(sessionID, userID int64, ids []string) error {
	raw, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	result, err := r.db.Exec(`
		UPDATE sessions
		SET metadata = jsonb_set(COALESCE(metadata, '{}'::jsonb), '{skills_enabled}', $1::jsonb, true)
		WHERE id = $2 AND user_id = $3 AND deleted_at IS NULL
	`, raw, sessionID, userID)
	if err != nil {
		return fmt.Errorf("failed to update session skills: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("session not found or already deleted")
	}
	return nil
}

func (r *SessionRepository) UpdateAutomaticTitle(sessionID, userID int64, title string, generated bool) error {
	return r.UpdateAutomaticTitleContext(context.Background(), sessionID, userID, title, generated)
}

func (r *SessionRepository) UpdateAutomaticTitleContext(ctx context.Context, sessionID, userID int64, title string, generated bool) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE sessions
		SET title = $1, title_generated = $2
		WHERE id = $3 AND user_id = $4 AND deleted_at IS NULL
		  AND title IN ('新对话', 'New Conversation')
	`, title, generated, sessionID, userID)
	if err != nil {
		return fmt.Errorf("failed to update automatic title: %w", err)
	}
	return nil
}

func (r *SessionRepository) UpdateAutomaticTitleAtAnswerRevision(ctx context.Context, sessionID, userID int64, title string, generated bool, expectedRevision int64) (bool, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE sessions
		SET title = $1, title_generated = $2
		WHERE id = $3 AND user_id = $4 AND deleted_at IS NULL
		  AND title IN ('新对话', 'New Conversation')
		  AND answer_selection_revision = $5
	`, title, generated, sessionID, userID, expectedRevision)
	if err != nil {
		return false, fmt.Errorf("failed to update automatic title at answer revision: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read automatic title update result: %w", err)
	}
	return rows == 1, nil
}

// Delete 软删除会话
func (r *SessionRepository) Delete(id, userID int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin session delete: %w", err)
	}
	defer tx.Rollback()

	query := `
		UPDATE sessions
		SET deleted_at = NOW()
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
	`

	result, err := tx.Exec(query, id, userID)
	if err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("session not found or already deleted")
	}
	if err := cancelRunningChatRuns(context.Background(), tx, userID, &id, "session_deleted", "session_deleted", "会话已删除", false); err != nil {
		return fmt.Errorf("failed to cancel deleted session runs: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit session delete: %w", err)
	}

	return nil
}

// CountByUser 统计用户的会话数量
func (r *SessionRepository) CountByUser(userID int64) (int, error) {
	var count int
	query := `SELECT COUNT(*) FROM sessions WHERE user_id = $1 AND deleted_at IS NULL`
	err := r.db.QueryRow(query, userID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count sessions: %w", err)
	}
	return count, nil
}
