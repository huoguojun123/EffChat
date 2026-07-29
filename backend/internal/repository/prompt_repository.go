package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/lib/pq"
)

type PromptRepository struct {
	db *sql.DB
}

func NewPromptRepository(db *sql.DB) *PromptRepository {
	return &PromptRepository{db: db}
}

func (r *PromptRepository) Create(p *model.Prompt) error {
	return r.CreateContext(context.Background(), p)
}

func (r *PromptRepository) CreateContext(ctx context.Context, p *model.Prompt) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin prompt creation: %w", err)
	}
	defer tx.Rollback()

	groupName := normalizePromptGroup(p.GroupName)
	if p.GroupID != nil {
		name, err := promptGroupNameForWrite(ctx, tx, *p.GroupID, p.UserID)
		if err != nil {
			return fmt.Errorf("prompt group not found or access denied: %w", err)
		}
		if name != "" {
			groupName = name
		}
	}
	query := `
		INSERT INTO prompts (user_id, title, content, description, tags, group_id, group_name, is_public)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, use_count, created_at, updated_at
	`
	err = tx.QueryRowContext(ctx,
		query, p.UserID, p.Title, p.Content, p.Description, pq.Array(p.Tags), p.GroupID, groupName, p.IsPublic,
	).Scan(&p.ID, &p.UseCount, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to create prompt: %w", promptContextError(ctx, err))
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit prompt creation: %w", promptContextError(ctx, err))
	}
	p.GroupName = groupName
	return nil
}

func (r *PromptRepository) GetByID(id, userID int64) (*model.Prompt, error) {
	p := &model.Prompt{}
	query := `
		SELECT id, user_id, title, content, description, tags, group_id, COALESCE(group_name, '默认分组'), is_public, use_count, created_at, updated_at
		FROM prompts
		WHERE id = $1 AND (user_id = $2 OR is_public = true)
	`
	err := r.db.QueryRow(query, id, userID).Scan(
		&p.ID, &p.UserID, &p.Title, &p.Content, &p.Description,
		pq.Array(&p.Tags), &p.GroupID, &p.GroupName, &p.IsPublic, &p.UseCount, &p.CreatedAt, &p.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("prompt not found: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get prompt: %w", err)
	}
	return p, nil
}

func (r *PromptRepository) ListByUser(userID int64, limit, offset int) ([]*model.Prompt, error) {
	query := `
		SELECT id, user_id, title, content, description, tags, group_id, COALESCE(group_name, '默认分组'), is_public, use_count, created_at, updated_at
		FROM prompts
		WHERE user_id = $1
		ORDER BY updated_at DESC
		LIMIT $2 OFFSET $3
	`
	return r.scanPrompts(query, userID, limit, offset)
}

func (r *PromptRepository) ListPublic(limit, offset int) ([]*model.Prompt, error) {
	query := `
		SELECT id, user_id, title, content, description, tags, group_id, COALESCE(group_name, '默认分组'), is_public, use_count, created_at, updated_at
		FROM prompts
		WHERE is_public = true
		ORDER BY use_count DESC, updated_at DESC
		LIMIT $1 OFFSET $2
	`
	return r.scanPrompts(query, limit, offset)
}

func (r *PromptRepository) ListShared(limit, offset int) ([]*model.Prompt, error) {
	query := `
		SELECT id, user_id, title, content, description, tags, group_id, COALESCE(group_name, '默认分组'), is_public, use_count, created_at, updated_at
		FROM prompts
		WHERE is_public = true
		ORDER BY updated_at DESC
		LIMIT $1 OFFSET $2
	`
	return r.scanPrompts(query, limit, offset)
}

func (r *PromptRepository) GetSharedByID(id int64) (*model.Prompt, error) {
	p := &model.Prompt{}
	query := `
		SELECT id, user_id, title, content, description, tags, group_id, COALESCE(group_name, '默认分组'), is_public, use_count, created_at, updated_at
		FROM prompts
		WHERE id = $1 AND is_public = true
	`
	err := r.db.QueryRow(query, id).Scan(
		&p.ID, &p.UserID, &p.Title, &p.Content, &p.Description,
		pq.Array(&p.Tags), &p.GroupID, &p.GroupName, &p.IsPublic, &p.UseCount, &p.CreatedAt, &p.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("prompt not found: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get prompt: %w", err)
	}
	return p, nil
}

func (r *PromptRepository) Update(p *model.Prompt) error {
	return r.UpdateContext(context.Background(), p)
}

func (r *PromptRepository) UpdateContext(ctx context.Context, p *model.Prompt) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin prompt update: %w", err)
	}
	defer tx.Rollback()

	groupName := normalizePromptGroup(p.GroupName)
	if p.GroupID != nil {
		name, err := promptGroupNameForWrite(ctx, tx, *p.GroupID, p.UserID)
		if err != nil {
			return fmt.Errorf("prompt group not found or access denied: %w", err)
		}
		if name != "" {
			groupName = name
		}
	}
	query := `
		UPDATE prompts
		SET title = $1, content = $2, description = $3, tags = $4, group_id = $5, group_name = $6, is_public = $7
		WHERE id = $8 AND user_id = $9
	`
	result, err := tx.ExecContext(ctx, query, p.Title, p.Content, p.Description, pq.Array(p.Tags), p.GroupID, groupName, p.IsPublic, p.ID, p.UserID)
	if err != nil {
		return fmt.Errorf("failed to update prompt: %w", promptContextError(ctx, err))
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("prompt not found or access denied: %w", ErrNotFound)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit prompt update: %w", promptContextError(ctx, err))
	}
	p.GroupName = groupName
	return nil
}

func (r *PromptRepository) UpdateShared(p *model.Prompt) error {
	query := `
		UPDATE prompts
		SET title = $1, content = $2, description = $3, tags = $4
		WHERE id = $5 AND is_public = true
	`
	result, err := r.db.Exec(query, p.Title, p.Content, p.Description, pq.Array(p.Tags), p.ID)
	if err != nil {
		return fmt.Errorf("failed to update prompt: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("prompt not found: %w", ErrNotFound)
	}
	return nil
}

func (r *PromptRepository) Delete(id, userID int64) error {
	result, err := r.db.Exec(`DELETE FROM prompts WHERE id = $1 AND user_id = $2 AND is_public = false`, id, userID)
	if err != nil {
		return fmt.Errorf("failed to delete prompt: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("prompt not found or access denied: %w", ErrNotFound)
	}
	return nil
}

func (r *PromptRepository) DeleteShared(id int64) error {
	result, err := r.db.Exec(`DELETE FROM prompts WHERE id = $1 AND is_public = true`, id)
	if err != nil {
		return fmt.Errorf("failed to delete prompt: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("prompt not found: %w", ErrNotFound)
	}
	return nil
}

func (r *PromptRepository) IncrementUseCount(id int64) error {
	_, err := r.db.Exec(`UPDATE prompts SET use_count = use_count + 1 WHERE id = $1`, id)
	return err
}

func (r *PromptRepository) scanPrompts(query string, args ...interface{}) ([]*model.Prompt, error) {
	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query prompts: %w", err)
	}
	defer rows.Close()

	var prompts []*model.Prompt
	for rows.Next() {
		p := &model.Prompt{}
		if err := rows.Scan(
			&p.ID, &p.UserID, &p.Title, &p.Content, &p.Description,
			pq.Array(&p.Tags), &p.GroupID, &p.GroupName, &p.IsPublic, &p.UseCount, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan prompt: %w", err)
		}
		prompts = append(prompts, p)
	}
	return prompts, nil
}

func normalizePromptGroup(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "默认分组"
	}
	return value
}

func promptGroupNameForWrite(ctx context.Context, tx *sql.Tx, id int64, userID int64) (string, error) {
	var name string
	err := tx.QueryRowContext(ctx, `SELECT name FROM prompt_groups WHERE id = $1 AND user_id = $2 FOR SHARE`, id, userID).Scan(&name)
	return name, promptContextError(ctx, err)
}

func promptContextError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}
