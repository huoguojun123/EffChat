package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/huoguojun123/EffChat/internal/model"
)

var (
	ErrDefaultUserGroupRequired = errors.New("cannot remove the final default user group")
	ErrUserGroupConflict        = errors.New("user group conflict")
)

const userGroupDefaultInvariantLock = int64(0x4653484752504446)

// UserGroupRepository 用户分级组数据访问
type UserGroupRepository struct {
	db *sql.DB
}

func NewUserGroupRepository(db *sql.DB) *UserGroupRepository {
	return &UserGroupRepository{db: db}
}

func lockUserGroupDefaultInvariant(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", userGroupDefaultInvariantLock); err != nil {
		return fmt.Errorf("lock default user group invariant: %w", userGroupContextError(ctx, err))
	}
	return nil
}

const userGroupColumns = `id, name, level, description, is_default, daily_message_limit, daily_token_limit, concurrent_run_limit, daily_tool_call_limit, daily_web_search_limit, daily_web_extract_limit, daily_ocr_file_limit, daily_ocr_page_limit, created_at, updated_at`

// UserGroupPatch preserves the presence of fields from an HTTP partial update.
// It stays resource-specific because default-group and quota invariants are
// owned by this repository transaction, not by a generic PATCH layer.
type UserGroupPatch struct {
	Name                 *string
	Level                *int
	Description          *string
	IsDefault            *bool
	DailyMessageLimit    *int
	DailyTokenLimit      *int
	ConcurrentRunLimit   *int
	DailyToolCallLimit   *int
	DailyWebSearchLimit  *int
	DailyWebExtractLimit *int
	DailyOCRFileLimit    *int
	DailyOCRPageLimit    *int
}

func (p UserGroupPatch) Apply(g *model.UserGroup) {
	if p.Name != nil {
		g.Name = *p.Name
	}
	if p.Level != nil {
		g.Level = *p.Level
	}
	if p.Description != nil {
		g.Description = *p.Description
	}
	if p.IsDefault != nil {
		g.IsDefault = *p.IsDefault
	}
	if p.DailyMessageLimit != nil {
		g.DailyMessageLimit = *p.DailyMessageLimit
	}
	if p.DailyTokenLimit != nil {
		g.DailyTokenLimit = *p.DailyTokenLimit
	}
	if p.ConcurrentRunLimit != nil {
		g.ConcurrentRunLimit = *p.ConcurrentRunLimit
	}
	if p.DailyToolCallLimit != nil {
		g.DailyToolCallLimit = *p.DailyToolCallLimit
	}
	if p.DailyWebSearchLimit != nil {
		g.DailyWebSearchLimit = *p.DailyWebSearchLimit
	}
	if p.DailyWebExtractLimit != nil {
		g.DailyWebExtractLimit = *p.DailyWebExtractLimit
	}
	if p.DailyOCRFileLimit != nil {
		g.DailyOCRFileLimit = *p.DailyOCRFileLimit
	}
	if p.DailyOCRPageLimit != nil {
		g.DailyOCRPageLimit = *p.DailyOCRPageLimit
	}
}

func scanUserGroup(s interface {
	Scan(dest ...interface{}) error
}) (*model.UserGroup, error) {
	g := &model.UserGroup{}
	err := s.Scan(
		&g.ID, &g.Name, &g.Level, &g.Description, &g.IsDefault,
		&g.DailyMessageLimit, &g.DailyTokenLimit, &g.ConcurrentRunLimit,
		&g.DailyToolCallLimit, &g.DailyWebSearchLimit, &g.DailyWebExtractLimit,
		&g.DailyOCRFileLimit, &g.DailyOCRPageLimit,
		&g.CreatedAt, &g.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return g, nil
}

// List 返回所有分级组，按 level 升序。
func (r *UserGroupRepository) List() ([]*model.UserGroup, error) {
	rows, err := r.db.Query(`SELECT ` + userGroupColumns + ` FROM user_groups ORDER BY level ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("failed to list user groups: %w", err)
	}
	defer rows.Close()

	var result []*model.UserGroup
	for rows.Next() {
		g, err := scanUserGroup(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan user group: %w", err)
		}
		result = append(result, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate user groups: %w", err)
	}
	return result, nil
}

// Get 按 ID 获取分级组，不存在返回 (nil, nil)。
func (r *UserGroupRepository) Get(id int64) (*model.UserGroup, error) {
	return r.GetContext(context.Background(), id)
}

func (r *UserGroupRepository) GetContext(ctx context.Context, id int64) (*model.UserGroup, error) {
	g, err := scanUserGroup(r.db.QueryRowContext(ctx, `SELECT `+userGroupColumns+` FROM user_groups WHERE id = $1`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user group: %w", userGroupContextError(ctx, err))
	}
	return g, nil
}

// Create 新建分级组。
func (r *UserGroupRepository) Create(g *model.UserGroup) error {
	return r.CreateContext(context.Background(), g)
}

func (r *UserGroupRepository) CreateContext(ctx context.Context, g *model.UserGroup) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin create user group transaction: %w", userGroupContextError(ctx, err))
	}
	defer tx.Rollback()
	if err := lockUserGroupDefaultInvariant(ctx, tx); err != nil {
		return err
	}
	if g.IsDefault {
		if _, err := tx.ExecContext(ctx, `UPDATE user_groups SET is_default = false WHERE is_default = true`); err != nil {
			return fmt.Errorf("clear existing default user group: %w", userGroupContextError(ctx, err))
		}
	}
	query := `INSERT INTO user_groups (
			name, level, description, is_default,
			daily_message_limit, daily_token_limit, concurrent_run_limit,
			daily_tool_call_limit, daily_web_search_limit, daily_web_extract_limit,
			daily_ocr_file_limit, daily_ocr_page_limit
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id, created_at, updated_at`
	err = tx.QueryRowContext(
		ctx,
		query,
		g.Name,
		g.Level,
		g.Description,
		g.IsDefault,
		g.DailyMessageLimit,
		g.DailyTokenLimit,
		g.ConcurrentRunLimit,
		g.DailyToolCallLimit,
		g.DailyWebSearchLimit,
		g.DailyWebExtractLimit,
		g.DailyOCRFileLimit,
		g.DailyOCRPageLimit,
	).
		Scan(&g.ID, &g.CreatedAt, &g.UpdatedAt)
	if err != nil {
		if IsUniqueViolation(err) {
			return fmt.Errorf("%w: user group name already exists", ErrUserGroupConflict)
		}
		return fmt.Errorf("failed to create user group: %w", userGroupContextError(ctx, err))
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit create user group: %w", userGroupContextError(ctx, err))
	}
	return nil
}

// Update 全量更新分级组字段。
func (r *UserGroupRepository) Update(g *model.UserGroup) error {
	return r.UpdateContext(context.Background(), g)
}

func (r *UserGroupRepository) UpdateContext(ctx context.Context, g *model.UserGroup) error {
	updated, err := r.UpdateFieldsContext(ctx, g.ID, UserGroupPatch{
		Name: &g.Name, Level: &g.Level, Description: &g.Description, IsDefault: &g.IsDefault,
		DailyMessageLimit: &g.DailyMessageLimit, DailyTokenLimit: &g.DailyTokenLimit,
		ConcurrentRunLimit: &g.ConcurrentRunLimit, DailyToolCallLimit: &g.DailyToolCallLimit,
		DailyWebSearchLimit: &g.DailyWebSearchLimit, DailyWebExtractLimit: &g.DailyWebExtractLimit,
		DailyOCRFileLimit: &g.DailyOCRFileLimit, DailyOCRPageLimit: &g.DailyOCRPageLimit,
	}, nil)
	if err != nil {
		return err
	}
	*g = *updated
	return nil
}

// UpdateFieldsContext locks and reloads the canonical group before applying
// the requested fields. The default-group advisory lock and row lock must be
// acquired before the snapshot is read; otherwise a serialized write could
// still overwrite unrelated quota or metadata fields read earlier.
func (r *UserGroupRepository) UpdateFieldsContext(ctx context.Context, id int64, patch UserGroupPatch, validate func(*model.UserGroup) error) (*model.UserGroup, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin update user group transaction: %w", userGroupContextError(ctx, err))
	}
	defer tx.Rollback()
	if err := lockUserGroupDefaultInvariant(ctx, tx); err != nil {
		return nil, err
	}
	current, err := scanUserGroup(tx.QueryRowContext(ctx, `SELECT `+userGroupColumns+` FROM user_groups WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("user group not found: %w", ErrNotFound)
		}
		return nil, fmt.Errorf("load user group for update: %w", userGroupContextError(ctx, err))
	}
	currentDefault := current.IsDefault
	patch.Apply(current)
	if validate != nil {
		if err := validate(current); err != nil {
			return nil, err
		}
	}
	if currentDefault && !current.IsDefault {
		var hasReplacement bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM user_groups WHERE is_default = true AND id <> $1)`, id).Scan(&hasReplacement); err != nil {
			return nil, fmt.Errorf("check replacement default user group: %w", userGroupContextError(ctx, err))
		}
		if !hasReplacement {
			return nil, ErrDefaultUserGroupRequired
		}
	}
	if current.IsDefault {
		if _, err := tx.ExecContext(ctx, `UPDATE user_groups SET is_default = false WHERE is_default = true AND id <> $1`, id); err != nil {
			return nil, fmt.Errorf("clear existing default user group: %w", userGroupContextError(ctx, err))
		}
	}
	query := `UPDATE user_groups
		SET name = $1, level = $2, description = $3, is_default = $4,
			daily_message_limit = $5, daily_token_limit = $6, concurrent_run_limit = $7,
			daily_tool_call_limit = $8, daily_web_search_limit = $9,
			daily_web_extract_limit = $10,
			daily_ocr_file_limit = $11, daily_ocr_page_limit = $12
		WHERE id = $13
		RETURNING ` + userGroupColumns
	updated, err := scanUserGroup(tx.QueryRowContext(
		ctx,
		query,
		current.Name,
		current.Level,
		current.Description,
		current.IsDefault,
		current.DailyMessageLimit,
		current.DailyTokenLimit,
		current.ConcurrentRunLimit,
		current.DailyToolCallLimit,
		current.DailyWebSearchLimit,
		current.DailyWebExtractLimit,
		current.DailyOCRFileLimit,
		current.DailyOCRPageLimit,
		id,
	))
	if err != nil {
		if IsUniqueViolation(err) {
			return nil, fmt.Errorf("%w: user group name already exists", ErrUserGroupConflict)
		}
		return nil, fmt.Errorf("failed to update user group: %w", userGroupContextError(ctx, err))
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit update user group: %w", userGroupContextError(ctx, err))
	}
	return updated, nil
}

// Delete 删除分级组。默认组必须先由另一个组接替，避免用户无声回退到无限额配置。
func (r *UserGroupRepository) Delete(id int64) error {
	return r.DeleteContext(context.Background(), id)
}

func (r *UserGroupRepository) DeleteContext(ctx context.Context, id int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete user group transaction: %w", userGroupContextError(ctx, err))
	}
	defer tx.Rollback()
	if err := lockUserGroupDefaultInvariant(ctx, tx); err != nil {
		return err
	}
	var currentDefault bool
	if err := tx.QueryRowContext(ctx, `SELECT is_default FROM user_groups WHERE id = $1 FOR UPDATE`, id).Scan(&currentDefault); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("user group not found: %w", ErrNotFound)
		}
		return fmt.Errorf("load user group for delete: %w", userGroupContextError(ctx, err))
	}
	if currentDefault {
		var hasReplacement bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM user_groups WHERE is_default = true AND id <> $1)`, id).Scan(&hasReplacement); err != nil {
			return fmt.Errorf("check replacement default user group: %w", userGroupContextError(ctx, err))
		}
		if !hasReplacement {
			return ErrDefaultUserGroupRequired
		}
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM user_groups WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete user group: %w", userGroupContextError(ctx, err))
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get deleted user group count: %w", userGroupContextError(ctx, err))
	}
	if rows == 0 {
		return fmt.Errorf("user group not found: %w", ErrNotFound)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete user group: %w", userGroupContextError(ctx, err))
	}
	return nil
}

func userGroupContextError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}
