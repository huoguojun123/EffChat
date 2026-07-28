package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/huoguojun123/effchat/internal/model"
)

var (
	ErrNotFound        = errors.New("not found")
	ErrLastActiveAdmin = errors.New("cannot remove the last active admin")
)

const userAdminInvariantLock = int64(0x4653484154434841)

type UserRepository struct {
	db *sql.DB
}

func lockUserAdminInvariant(tx *sql.Tx) error {
	if _, err := tx.Exec("SELECT pg_advisory_xact_lock($1)", userAdminInvariantLock); err != nil {
		return fmt.Errorf("lock user admin invariant: %w", err)
	}
	return nil
}

func NewUserRepository(db *sql.DB) *UserRepository {
	return &UserRepository{db: db}
}

// Create 创建用户
func (r *UserRepository) Create(user *model.User) error {
	query := `
		INSERT INTO users (username, email, password_hash, nickname, role, permissions, preferences, is_active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, auth_version, created_at, updated_at
	`

	err := r.db.QueryRow(
		query,
		user.Username,
		user.Email,
		user.PasswordHash,
		user.Nickname,
		user.Role,
		user.Permissions,
		user.Preferences,
		user.IsActive,
	).Scan(&user.ID, &user.AuthVersion, &user.CreatedAt, &user.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}

	return nil
}

func (r *UserRepository) CreateRegistrationUser(user *model.User) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin registration user transaction: %w", err)
	}
	defer tx.Rollback()
	if err := lockUserAdminInvariant(tx); err != nil {
		return err
	}

	var count int
	if err := tx.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return fmt.Errorf("count users: %w", err)
	}
	user.Role = "user"
	user.IsActive = false
	if count == 0 {
		user.Role = "admin"
		user.IsActive = true
	}
	query := `
		INSERT INTO users (username, email, password_hash, nickname, role, permissions, preferences, is_active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, auth_version, created_at, updated_at
	`
	if err := tx.QueryRow(query, user.Username, user.Email, user.PasswordHash, user.Nickname, user.Role, user.Permissions, user.Preferences, user.IsActive).Scan(&user.ID, &user.AuthVersion, &user.CreatedAt, &user.UpdatedAt); err != nil {
		return fmt.Errorf("create registration user: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit registration user transaction: %w", err)
	}
	return nil
}

// GetByID 根据 ID 获取用户
func (r *UserRepository) GetByID(id int64) (*model.User, error) {
	return r.GetByIDContext(context.Background(), id)
}

func (r *UserRepository) GetByIDContext(ctx context.Context, id int64) (*model.User, error) {
	user := &model.User{}
	query := `
		SELECT id, username, email, password_hash, nickname, avatar_url, role,
		       permissions, preferences, is_active, auth_version, created_at, updated_at, last_login_at
		FROM users
		WHERE id = $1 AND is_active = true
	`

	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.PasswordHash,
		&user.Nickname,
		&user.AvatarURL,
		&user.Role,
		&user.Permissions,
		&user.Preferences,
		&user.IsActive,
		&user.AuthVersion,
		&user.CreatedAt,
		&user.UpdatedAt,
		&user.LastLoginAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("user not found: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	return user, nil
}

// GetByUsername 根据用户名获取用户
func (r *UserRepository) GetByUsername(username string) (*model.User, error) {
	user := &model.User{}
	query := `
		SELECT id, username, email, password_hash, nickname, avatar_url, role,
		       permissions, preferences, is_active, auth_version, created_at, updated_at, last_login_at
		FROM users
		WHERE username = $1
	`

	err := r.db.QueryRow(query, username).Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.PasswordHash,
		&user.Nickname,
		&user.AvatarURL,
		&user.Role,
		&user.Permissions,
		&user.Preferences,
		&user.IsActive,
		&user.AuthVersion,
		&user.CreatedAt,
		&user.UpdatedAt,
		&user.LastLoginAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("user not found: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user by username: %w", err)
	}

	return user, nil
}

// CountUsers 统计用户数量
func (r *UserRepository) CountUsers() (int, error) {
	var count int
	err := r.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count users: %w", err)
	}
	return count, nil
}

// UpdateLastLogin 更新最后登录时间
func (r *UserRepository) UpdateLastLogin(userID int64) error {
	query := `UPDATE users SET last_login_at = $1 WHERE id = $2`
	_, err := r.db.Exec(query, time.Now(), userID)
	if err != nil {
		return fmt.Errorf("failed to update last login: %w", err)
	}
	return nil
}

// Update 更新用户信息
func (r *UserRepository) Update(user *model.User) error {
	query := `
		UPDATE users
		SET email = $1, nickname = $2, avatar_url = $3, permissions = $4, preferences = $5, updated_at = NOW()
		WHERE id = $6
	`

	_, err := r.db.Exec(
		query,
		user.Email,
		user.Nickname,
		user.AvatarURL,
		user.Permissions,
		user.Preferences,
		user.ID,
	)

	if err != nil {
		return fmt.Errorf("failed to update user: %w", err)
	}

	return nil
}

// UpdateAdminFields 更新管理员可维护的用户资料、角色、状态和权限。
func (r *UserRepository) UpdateAdminFields(user *model.User) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin admin user update transaction: %w", err)
	}
	defer tx.Rollback()
	if err := lockUserAdminInvariant(tx); err != nil {
		return err
	}

	var currentRole string
	var currentActive bool
	if err := tx.QueryRow(`SELECT role, is_active FROM users WHERE id = $1 FOR UPDATE`, user.ID).Scan(&currentRole, &currentActive); err == sql.ErrNoRows {
		return fmt.Errorf("user not found: %w", ErrNotFound)
	} else if err != nil {
		return fmt.Errorf("load user for admin update: %w", err)
	}
	if currentRole == "admin" && currentActive && (user.Role != "admin" || !user.IsActive) {
		var activeAdmins int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM users WHERE role = 'admin' AND is_active = true`).Scan(&activeAdmins); err != nil {
			return fmt.Errorf("count active admins: %w", err)
		}
		if activeAdmins <= 1 {
			return ErrLastActiveAdmin
		}
	}
	invalidateRuns := currentRole != user.Role || currentActive != user.IsActive

	query := `
		UPDATE users
		SET email = $1, nickname = $2, avatar_url = $3, role = $4,
		    permissions = $5, preferences = $6, is_active = $7,
		    auth_version = auth_version + CASE WHEN $8 THEN 1 ELSE 0 END,
		    updated_at = NOW()
		WHERE id = $9
	`
	result, err := tx.Exec(
		query,
		user.Email,
		user.Nickname,
		user.AvatarURL,
		user.Role,
		user.Permissions,
		user.Preferences,
		user.IsActive,
		invalidateRuns,
		user.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update user: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("user not found: %w", ErrNotFound)
	}
	if invalidateRuns {
		if err := cancelRunningChatRuns(context.Background(), tx, user.ID, nil, "account_changed", "account_changed", "账号状态已变更，请重新登录", false); err != nil {
			return fmt.Errorf("cancel runs after account change: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit admin user update transaction: %w", err)
	}
	return nil
}

// UpdatePassword 更新用户密码
func (r *UserRepository) UpdatePassword(userID int64, hashedPassword string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin password update transaction: %w", err)
	}
	defer tx.Rollback()
	query := `UPDATE users SET password_hash = $1, auth_version = auth_version + 1, updated_at = NOW() WHERE id = $2`
	result, err := tx.Exec(query, hashedPassword, userID)
	if err != nil {
		return fmt.Errorf("failed to update password: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated password rows: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("user not found: %w", ErrNotFound)
	}
	if err := cancelRunningChatRuns(context.Background(), tx, userID, nil, "account_changed", "account_changed", "账号状态已变更，请重新登录", false); err != nil {
		return fmt.Errorf("cancel runs after password update: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit password update transaction: %w", err)
	}
	return nil
}

// ListAll 获取所有用户（管理员用，含禁用账户）
func (r *UserRepository) ListAll(limit, offset int) ([]*model.User, error) {
	query := `
		SELECT id, username, email, password_hash, nickname, avatar_url, role, group_id,
		       permissions, preferences, is_active, auth_version, created_at, updated_at, last_login_at
		FROM users
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`

	rows, err := r.db.Query(query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}
	defer rows.Close()

	var users []*model.User
	for rows.Next() {
		user := &model.User{}
		err := rows.Scan(
			&user.ID,
			&user.Username,
			&user.Email,
			&user.PasswordHash,
			&user.Nickname,
			&user.AvatarURL,
			&user.Role,
			&user.GroupID,
			&user.Permissions,
			&user.Preferences,
			&user.IsActive,
			&user.AuthVersion,
			&user.CreatedAt,
			&user.UpdatedAt,
			&user.LastLoginAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan user: %w", err)
		}
		users = append(users, user)
	}
	return users, nil
}

// SetGroup 设置用户所属分级组（groupID 为 nil 时清空，视为默认最低级）。
func (r *UserRepository) SetGroup(userID int64, groupID *int64) error {
	result, err := r.db.Exec(`UPDATE users SET group_id = $1, updated_at = NOW() WHERE id = $2`, groupID, userID)
	if err != nil {
		return fmt.Errorf("failed to set user group: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("user not found: %w", ErrNotFound)
	}
	return nil
}

// GetGroupLevel 返回用户所属组的 level；未分组（NULL）或组不存在时返回 0（默认最低级）。
func (r *UserRepository) GetGroupLevel(userID int64) (int, error) {
	return r.GetGroupLevelContext(context.Background(), userID)
}

func (r *UserRepository) GetGroupLevelContext(ctx context.Context, userID int64) (int, error) {
	var level int
	query := `SELECT COALESCE(g.level, 0)
		FROM users u
		LEFT JOIN user_groups g ON g.id = u.group_id
		WHERE u.id = $1`
	err := r.db.QueryRowContext(ctx, query, userID).Scan(&level)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("user not found: %w", ErrNotFound)
	}
	if err != nil {
		return 0, fmt.Errorf("failed to get user group level: %w", err)
	}
	return level, nil
}

// CountAll 统计全部用户数量（含禁用）
func (r *UserRepository) CountAll() (int, error) {
	var count int
	err := r.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count all users: %w", err)
	}
	return count, nil
}

// GetByIDIncludeInactive 根据 ID 获取用户（含禁用账户，管理员用）
func (r *UserRepository) GetByIDIncludeInactive(id int64) (*model.User, error) {
	user := &model.User{}
	query := `
		SELECT id, username, email, password_hash, nickname, avatar_url, role, group_id,
		       permissions, preferences, is_active, auth_version, created_at, updated_at, last_login_at
		FROM users
		WHERE id = $1
	`
	err := r.db.QueryRow(query, id).Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.PasswordHash,
		&user.Nickname,
		&user.AvatarURL,
		&user.Role,
		&user.GroupID,
		&user.Permissions,
		&user.Preferences,
		&user.IsActive,
		&user.AuthVersion,
		&user.CreatedAt,
		&user.UpdatedAt,
		&user.LastLoginAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("user not found: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	return user, nil
}
