package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/huoguojun123/EffChat/internal/model"
)

var (
	ErrPromptGroupInvalid  = errors.New("invalid prompt group")
	ErrPromptGroupConflict = errors.New("prompt group conflict")
)

type PromptGroupRepository struct {
	db *sql.DB
}

func NewPromptGroupRepository(db *sql.DB) *PromptGroupRepository {
	return &PromptGroupRepository{db: db}
}

func (r *PromptGroupRepository) ListByUser(userID int64) ([]*model.PromptGroup, error) {
	rows, err := r.db.Query(`
		SELECT id, user_id, name, created_at, updated_at
		FROM prompt_groups
		WHERE user_id = $1
		ORDER BY lower(name), id
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list prompt groups: %w", err)
	}
	defer rows.Close()
	return scanPromptGroups(rows)
}

func (r *PromptGroupRepository) Create(userID int64, name string) (*model.PromptGroup, error) {
	name = normalizePromptGroupName(name)
	if name == "" {
		return nil, fmt.Errorf("%w: group name is required", ErrPromptGroupInvalid)
	}
	group := &model.PromptGroup{UserID: userID, Name: name}
	err := r.db.QueryRow(`
		INSERT INTO prompt_groups (user_id, name)
		VALUES ($1, $2)
		RETURNING id, created_at, updated_at
	`, userID, name).Scan(&group.ID, &group.CreatedAt, &group.UpdatedAt)
	if err != nil {
		if IsUniqueViolation(err) {
			return nil, fmt.Errorf("%w: prompt group name already exists", ErrPromptGroupConflict)
		}
		return nil, fmt.Errorf("failed to create prompt group: %w", err)
	}
	return group, nil
}

func (r *PromptGroupRepository) GetByID(id int64, userID int64) (*model.PromptGroup, error) {
	group := &model.PromptGroup{}
	err := r.db.QueryRow(`
		SELECT id, user_id, name, created_at, updated_at
		FROM prompt_groups
		WHERE id = $1 AND user_id = $2
	`, id, userID).Scan(&group.ID, &group.UserID, &group.Name, &group.CreatedAt, &group.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get prompt group: %w", err)
	}
	return group, nil
}

func (r *PromptGroupRepository) Update(id int64, userID int64, name string) (*model.PromptGroup, error) {
	return r.UpdateContext(context.Background(), id, userID, name)
}

func (r *PromptGroupRepository) UpdateContext(ctx context.Context, id int64, userID int64, name string) (*model.PromptGroup, error) {
	name = normalizePromptGroupName(name)
	if name == "" {
		return nil, fmt.Errorf("%w: group name is required", ErrPromptGroupInvalid)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin prompt group update: %w", err)
	}
	defer tx.Rollback()

	group := &model.PromptGroup{ID: id, UserID: userID, Name: name}
	err = tx.QueryRowContext(ctx, `
		UPDATE prompt_groups
		SET name = $1
		WHERE id = $2 AND user_id = $3
		RETURNING created_at, updated_at
	`, name, id, userID).Scan(&group.CreatedAt, &group.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		if IsUniqueViolation(err) {
			return nil, fmt.Errorf("%w: prompt group name already exists", ErrPromptGroupConflict)
		}
		return nil, fmt.Errorf("failed to update prompt group: %w", promptContextError(ctx, err))
	}
	if _, err := tx.ExecContext(ctx, `UPDATE prompts SET group_name = $1, updated_at = NOW() WHERE group_id = $2`, name, id); err != nil {
		return nil, fmt.Errorf("failed to update prompt group names: %w", promptContextError(ctx, err))
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit prompt group update: %w", promptContextError(ctx, err))
	}
	return group, nil
}

func (r *PromptGroupRepository) Delete(id int64, userID int64) error {
	return r.DeleteContext(context.Background(), id, userID)
}

func (r *PromptGroupRepository) DeleteContext(ctx context.Context, id int64, userID int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM prompt_groups WHERE id = $1 AND user_id = $2 FOR UPDATE`, id, userID).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return ErrNotFound
		}
		return fmt.Errorf("failed to get prompt group: %w", promptContextError(ctx, err))
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE prompts
		SET group_id = NULL, group_name = '默认分组', updated_at = NOW()
		WHERE group_id = $1
	`, id); err != nil {
		return fmt.Errorf("failed to move prompts out of group: %w", promptContextError(ctx, err))
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM prompt_groups WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("failed to delete prompt group: %w", promptContextError(ctx, err))
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get deleted prompt group count: %w", promptContextError(ctx, err))
	}
	if rows == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit prompt group deletion: %w", promptContextError(ctx, err))
	}
	return nil
}

func scanPromptGroups(rows *sql.Rows) ([]*model.PromptGroup, error) {
	var groups []*model.PromptGroup
	for rows.Next() {
		group := &model.PromptGroup{}
		if err := rows.Scan(&group.ID, &group.UserID, &group.Name, &group.CreatedAt, &group.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan prompt group: %w", err)
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate prompt groups: %w", err)
	}
	return groups, nil
}

func normalizePromptGroupName(value string) string {
	return strings.TrimSpace(value)
}
