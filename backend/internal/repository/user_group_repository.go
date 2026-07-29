package repository

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/huoguojun123/EffChat/internal/model"
)

var ErrDefaultUserGroupRequired = errors.New("cannot remove the final default user group")

const userGroupDefaultInvariantLock = int64(0x4653484752504446)

// UserGroupRepository 用户分级组数据访问
type UserGroupRepository struct {
	db *sql.DB
}

func NewUserGroupRepository(db *sql.DB) *UserGroupRepository {
	return &UserGroupRepository{db: db}
}

func lockUserGroupDefaultInvariant(tx *sql.Tx) error {
	if _, err := tx.Exec("SELECT pg_advisory_xact_lock($1)", userGroupDefaultInvariantLock); err != nil {
		return fmt.Errorf("lock default user group invariant: %w", err)
	}
	return nil
}

const userGroupColumns = `id, name, level, description, is_default, daily_message_limit, daily_token_limit, concurrent_run_limit, daily_tool_call_limit, daily_web_search_limit, daily_web_extract_limit, daily_ocr_file_limit, daily_ocr_page_limit, created_at, updated_at`

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
	g, err := scanUserGroup(r.db.QueryRow(`SELECT `+userGroupColumns+` FROM user_groups WHERE id = $1`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user group: %w", err)
	}
	return g, nil
}

// Create 新建分级组。
func (r *UserGroupRepository) Create(g *model.UserGroup) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin create user group transaction: %w", err)
	}
	defer tx.Rollback()
	if err := lockUserGroupDefaultInvariant(tx); err != nil {
		return err
	}
	if g.IsDefault {
		if _, err := tx.Exec(`UPDATE user_groups SET is_default = false WHERE is_default = true`); err != nil {
			return fmt.Errorf("clear existing default user group: %w", err)
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
	err = tx.QueryRow(
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
		return fmt.Errorf("failed to create user group: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit create user group: %w", err)
	}
	return nil
}

// Update 全量更新分级组字段。
func (r *UserGroupRepository) Update(g *model.UserGroup) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin update user group transaction: %w", err)
	}
	defer tx.Rollback()
	if err := lockUserGroupDefaultInvariant(tx); err != nil {
		return err
	}
	var currentDefault bool
	if err := tx.QueryRow(`SELECT is_default FROM user_groups WHERE id = $1 FOR UPDATE`, g.ID).Scan(&currentDefault); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("user group not found: %w", ErrNotFound)
		}
		return fmt.Errorf("load user group for update: %w", err)
	}
	if currentDefault && !g.IsDefault {
		var hasReplacement bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM user_groups WHERE is_default = true AND id <> $1)`, g.ID).Scan(&hasReplacement); err != nil {
			return fmt.Errorf("check replacement default user group: %w", err)
		}
		if !hasReplacement {
			return ErrDefaultUserGroupRequired
		}
	}
	if g.IsDefault {
		if _, err := tx.Exec(`UPDATE user_groups SET is_default = false WHERE is_default = true AND id <> $1`, g.ID); err != nil {
			return fmt.Errorf("clear existing default user group: %w", err)
		}
	}
	query := `UPDATE user_groups
		SET name = $1, level = $2, description = $3, is_default = $4,
			daily_message_limit = $5, daily_token_limit = $6, concurrent_run_limit = $7,
			daily_tool_call_limit = $8, daily_web_search_limit = $9,
			daily_web_extract_limit = $10,
			daily_ocr_file_limit = $11, daily_ocr_page_limit = $12
		WHERE id = $13`
	result, err := tx.Exec(
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
		g.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update user group: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("user group not found: %w", ErrNotFound)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit update user group: %w", err)
	}
	return nil
}

// Delete 删除分级组。默认组必须先由另一个组接替，避免用户无声回退到无限额配置。
func (r *UserGroupRepository) Delete(id int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin delete user group transaction: %w", err)
	}
	defer tx.Rollback()
	if err := lockUserGroupDefaultInvariant(tx); err != nil {
		return err
	}
	var currentDefault bool
	if err := tx.QueryRow(`SELECT is_default FROM user_groups WHERE id = $1 FOR UPDATE`, id).Scan(&currentDefault); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("user group not found: %w", ErrNotFound)
		}
		return fmt.Errorf("load user group for delete: %w", err)
	}
	if currentDefault {
		var hasReplacement bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM user_groups WHERE is_default = true AND id <> $1)`, id).Scan(&hasReplacement); err != nil {
			return fmt.Errorf("check replacement default user group: %w", err)
		}
		if !hasReplacement {
			return ErrDefaultUserGroupRequired
		}
	}
	result, err := tx.Exec(`DELETE FROM user_groups WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete user group: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("user group not found: %w", ErrNotFound)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete user group: %w", err)
	}
	return nil
}
