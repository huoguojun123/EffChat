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

// PromptPatch carries JSON field presence across the repository boundary. A
// nil Description or GroupID is meaningful only when its corresponding Set
// flag is true, so PATCH can clear nullable columns without rebuilding a stale
// full Prompt snapshot.
type PromptPatch struct {
	Title          *string
	Content        *string
	Description    *string
	DescriptionSet bool
	Tags           []string
	TagsSet        bool
	GroupID        *int64
	GroupIDSet     bool
	GroupName      *string
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
		ORDER BY updated_at DESC, id DESC
		LIMIT $2 OFFSET $3
	`
	return r.scanPrompts(query, userID, limit, offset)
}

func (r *PromptRepository) CountByUser(userID int64) (int, error) {
	var total int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM prompts WHERE user_id = $1`, userID).Scan(&total); err != nil {
		return 0, fmt.Errorf("failed to count user prompts: %w", err)
	}
	return total, nil
}

func (r *PromptRepository) ListPublic(limit, offset int) ([]*model.Prompt, error) {
	query := `
		SELECT id, user_id, title, content, description, tags, group_id, COALESCE(group_name, '默认分组'), is_public, use_count, created_at, updated_at
		FROM prompts
		WHERE is_public = true
		ORDER BY use_count DESC, updated_at DESC, id DESC
		LIMIT $1 OFFSET $2
	`
	return r.scanPrompts(query, limit, offset)
}

func (r *PromptRepository) CountPublic() (int, error) {
	var total int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM prompts WHERE is_public = true`).Scan(&total); err != nil {
		return 0, fmt.Errorf("failed to count public prompts: %w", err)
	}
	return total, nil
}

func (r *PromptRepository) ListShared(limit, offset int) ([]*model.Prompt, error) {
	query := `
		SELECT id, user_id, title, content, description, tags, group_id, COALESCE(group_name, '默认分组'), is_public, use_count, created_at, updated_at
		FROM prompts
		WHERE is_public = true
		ORDER BY updated_at DESC, id DESC
		LIMIT $1 OFFSET $2
	`
	return r.scanPrompts(query, limit, offset)
}

func (r *PromptRepository) CountShared() (int, error) {
	return r.CountPublic()
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

func (r *PromptRepository) PatchContext(ctx context.Context, id, userID int64, patch PromptPatch) (*model.Prompt, error) {
	return r.patchContext(ctx, id, userID, false, patch)
}

func (r *PromptRepository) PatchSharedContext(ctx context.Context, id int64, patch PromptPatch) (*model.Prompt, error) {
	patch.GroupID, patch.GroupIDSet, patch.GroupName = nil, false, nil
	return r.patchContext(ctx, id, 0, true, patch)
}

func (r *PromptRepository) patchContext(ctx context.Context, id, userID int64, shared bool, patch PromptPatch) (*model.Prompt, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin prompt patch: %w", err)
	}
	defer tx.Rollback()
	p, err := getPromptForUpdateTx(ctx, tx, id, userID, shared)
	if err != nil {
		return nil, err
	}
	groupName, groupNameSet, err := applyPromptPatch(ctx, tx, p, patch)
	if err != nil {
		return nil, err
	}
	sets := []string{}
	args := []interface{}{}
	if patch.Title != nil {
		sets = append(sets, fmt.Sprintf("title = $%d", len(args)+1))
		args = append(args, p.Title)
	}
	if patch.Content != nil {
		sets = append(sets, fmt.Sprintf("content = $%d", len(args)+1))
		args = append(args, p.Content)
	}
	if patch.DescriptionSet {
		sets = append(sets, fmt.Sprintf("description = $%d", len(args)+1))
		args = append(args, p.Description)
	}
	if patch.TagsSet {
		sets = append(sets, fmt.Sprintf("tags = $%d", len(args)+1))
		args = append(args, pq.Array(p.Tags))
	}
	if patch.GroupIDSet {
		sets = append(sets, fmt.Sprintf("group_id = $%d", len(args)+1))
		args = append(args, p.GroupID)
	}
	if groupNameSet {
		sets = append(sets, fmt.Sprintf("group_name = $%d", len(args)+1))
		args = append(args, groupName)
	}
	if len(sets) == 0 {
		return p, nil
	}
	args = append(args, p.ID)
	if _, err := tx.ExecContext(ctx, "UPDATE prompts SET "+strings.Join(sets, ", ")+", updated_at = NOW() WHERE id = $"+fmt.Sprint(len(args)), args...); err != nil {
		return nil, fmt.Errorf("failed to patch prompt: %w", promptContextError(ctx, err))
	}
	updated, err := getPromptForUpdateTx(ctx, tx, id, userID, shared)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit prompt patch: %w", promptContextError(ctx, err))
	}
	return updated, nil
}

func applyPromptPatch(ctx context.Context, tx *sql.Tx, p *model.Prompt, patch PromptPatch) (string, bool, error) {
	if patch.Title != nil {
		p.Title = *patch.Title
	}
	if patch.Content != nil {
		p.Content = *patch.Content
	}
	if patch.DescriptionSet {
		p.Description = patch.Description
	}
	if patch.TagsSet {
		p.Tags = append([]string(nil), patch.Tags...)
	}
	groupName := p.GroupName
	groupNameSet := false
	if patch.GroupIDSet {
		p.GroupID = patch.GroupID
		groupNameSet = true
		groupName = normalizePromptGroup(groupName)
		if p.GroupID != nil {
			name, err := promptGroupNameForWrite(ctx, tx, *p.GroupID, p.UserID)
			if err != nil {
				return "", false, fmt.Errorf("prompt group not found or access denied: %w", err)
			}
			if name != "" {
				groupName = name
			}
		}
	}
	if patch.GroupName != nil {
		groupName = normalizePromptGroup(*patch.GroupName)
		groupNameSet = true
		p.GroupName = groupName
	}
	return groupName, groupNameSet, nil
}

func (r *PromptRepository) Delete(id, userID int64) error {
	result, err := r.db.Exec(`DELETE FROM prompts WHERE id = $1 AND user_id = $2 AND is_public = false`, id, userID)
	if err != nil {
		return fmt.Errorf("failed to delete prompt: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read deleted prompt rows: %w", err)
	}
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
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read deleted shared prompt rows: %w", err)
	}
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
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate prompts: %w", err)
	}
	return prompts, nil
}

func getPromptForUpdateTx(ctx context.Context, tx *sql.Tx, id, userID int64, shared bool) (*model.Prompt, error) {
	query := `
		SELECT id, user_id, title, content, description, tags, group_id,
		       COALESCE(group_name, '默认分组'), is_public, use_count, created_at, updated_at
		FROM prompts WHERE id = $1`
	args := []interface{}{id}
	if shared {
		query += " AND is_public = true"
	} else {
		query += " AND user_id = $2 AND is_public = false"
		args = append(args, userID)
	}
	query += " FOR UPDATE"
	p := &model.Prompt{}
	err := tx.QueryRowContext(ctx, query, args...).Scan(
		&p.ID, &p.UserID, &p.Title, &p.Content, &p.Description,
		pq.Array(&p.Tags), &p.GroupID, &p.GroupName, &p.IsPublic, &p.UseCount, &p.CreatedAt, &p.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("prompt not found or access denied: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get prompt for update: %w", promptContextError(ctx, err))
	}
	return p, nil
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
	err = promptContextError(ctx, err)
	if err == sql.ErrNoRows {
		return "", ErrNotFound
	}
	return name, err
}

func promptContextError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}
