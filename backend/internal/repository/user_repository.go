package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/lib/pq"
)

var (
	ErrNotFound            = errors.New("not found")
	ErrLastActiveAdmin     = errors.New("cannot remove the last active admin")
	ErrUserConflict        = errors.New("user identity conflict")
	ErrUserGroupMissing    = errors.New("user group not found")
	ErrProtectedSuperAdmin = errors.New("super administrator identity is protected")
	// ErrUserCommitUnknown marks the only mutation phase where deleting a
	// staged avatar without re-reading ownership could remove a committed file.
	ErrUserCommitUnknown = errors.New("user update commit outcome is unknown")
)

const userAdminInvariantLock = int64(0x4653484154434841)

type UserRepository struct {
	db *sql.DB
}

// UserPatch contains only fields present in a profile or administrator PATCH.
// Nullable profile fields carry a separate Set flag so JSON null/blank values
// remain distinguishable from fields omitted by the client.
type UserPatch struct {
	EmailSet       bool
	Email          *string
	NicknameSet    bool
	Nickname       *string
	AvatarURLSet   bool
	AvatarURL      *string
	Role           *string
	PermissionsSet bool
	Permissions    []byte
	IsActive       *bool
}

func (p UserPatch) Apply(user *model.User) {
	if p.EmailSet {
		user.Email = p.Email
	}
	if p.NicknameSet {
		user.Nickname = p.Nickname
	}
	if p.AvatarURLSet {
		user.AvatarURL = p.AvatarURL
	}
	if p.Role != nil {
		user.Role = *p.Role
	}
	if p.PermissionsSet {
		user.Permissions = p.Permissions
	}
	if p.IsActive != nil {
		user.IsActive = *p.IsActive
	}
}

type UserUpdateResult struct {
	User            *model.User
	InvalidatedRuns bool
	// ReplacedAvatarURL is the canonical URL held by the locked row before an
	// avatar change. It is returned even on an uncertain commit so the file
	// owner can re-check database references before deciding what to remove.
	ReplacedAvatarURL *string
}

func cloneOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func sameOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

const userColumns = `id, username, email, password_hash, nickname, avatar_url, role, is_super_admin, group_id,
	permissions, preferences, is_active, auth_version, created_at, updated_at, last_login_at`

func scanUser(s interface {
	Scan(dest ...interface{}) error
}) (*model.User, error) {
	user := &model.User{}
	err := s.Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.PasswordHash,
		&user.Nickname,
		&user.AvatarURL,
		&user.Role,
		&user.IsSuperAdmin,
		&user.GroupID,
		&user.Permissions,
		&user.Preferences,
		&user.IsActive,
		&user.AuthVersion,
		&user.CreatedAt,
		&user.UpdatedAt,
		&user.LastLoginAt,
	)
	return user, err
}

func lockUserAdminInvariant(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", userAdminInvariantLock); err != nil {
		return fmt.Errorf("lock user admin invariant: %w", userContextError(ctx, err))
	}
	return nil
}

func NewUserRepository(db *sql.DB) *UserRepository {
	return &UserRepository{db: db}
}

// Create 创建用户
func (r *UserRepository) Create(user *model.User) error {
	query := `
		INSERT INTO users (username, email, password_hash, nickname, role, is_super_admin, permissions, preferences, is_active)
		VALUES ($1, $2, $3, $4, $5, false, $6, $7, $8)
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
		if IsUniqueViolation(err) {
			return fmt.Errorf("%w: username or email already exists", ErrUserConflict)
		}
		return fmt.Errorf("failed to create user: %w", err)
	}

	return nil
}

func (r *UserRepository) CreateRegistrationUserContext(ctx context.Context, user *model.User) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin registration user transaction: %w", userContextError(ctx, err))
	}
	defer tx.Rollback()
	if err := lockUserAdminInvariant(ctx, tx); err != nil {
		return err
	}

	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return fmt.Errorf("count users: %w", userContextError(ctx, err))
	}
	user.Role = "user"
	user.IsSuperAdmin = false
	user.IsActive = false
	if count == 0 {
		user.Role = "admin"
		user.IsSuperAdmin = true
		user.IsActive = true
	}
	query := `
		INSERT INTO users (username, email, password_hash, nickname, role, is_super_admin, permissions, preferences, is_active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, auth_version, created_at, updated_at
	`
	if err := tx.QueryRowContext(ctx, query, user.Username, user.Email, user.PasswordHash, user.Nickname, user.Role, user.IsSuperAdmin, user.Permissions, user.Preferences, user.IsActive).Scan(&user.ID, &user.AuthVersion, &user.CreatedAt, &user.UpdatedAt); err != nil {
		if IsUniqueViolation(err) {
			return fmt.Errorf("%w: username or email already exists", ErrUserConflict)
		}
		return fmt.Errorf("create registration user: %w", userContextError(ctx, err))
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit registration user transaction: %w", userContextError(ctx, err))
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
		SELECT id, username, email, password_hash, nickname, avatar_url, role, is_super_admin,
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
		&user.IsSuperAdmin,
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
	return r.GetByUsernameContext(context.Background(), username)
}

func (r *UserRepository) GetByUsernameContext(ctx context.Context, username string) (*model.User, error) {
	user := &model.User{}
	query := `
		SELECT id, username, email, password_hash, nickname, avatar_url, role, is_super_admin,
		       permissions, preferences, is_active, auth_version, created_at, updated_at, last_login_at
		FROM users
		WHERE username = $1
	`

	err := r.db.QueryRowContext(ctx, query, username).Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.PasswordHash,
		&user.Nickname,
		&user.AvatarURL,
		&user.Role,
		&user.IsSuperAdmin,
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
		return nil, fmt.Errorf("failed to get user by username: %w", userContextError(ctx, err))
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

// UpdateAdminFields 更新管理员可维护的用户资料、角色、状态和权限。
func (r *UserRepository) UpdateAdminFields(user *model.User) error {
	return r.UpdateAdminFieldsContext(context.Background(), user)
}

func (r *UserRepository) UpdateAdminFieldsContext(ctx context.Context, user *model.User) error {
	result, err := r.UpdateFieldsContext(ctx, user.ID, UserPatch{
		EmailSet: true, Email: user.Email,
		NicknameSet: true, Nickname: user.Nickname,
		Role:           &user.Role,
		PermissionsSet: true, Permissions: user.Permissions,
		IsActive: &user.IsActive,
	})
	if err != nil {
		return err
	}
	user.Email = result.User.Email
	user.Nickname = result.User.Nickname
	user.Role = result.User.Role
	user.IsSuperAdmin = result.User.IsSuperAdmin
	user.Permissions = result.User.Permissions
	user.IsActive = result.User.IsActive
	user.AuthVersion = result.User.AuthVersion
	user.UpdatedAt = result.User.UpdatedAt
	return nil
}

// UpdateFieldsContext locks and reloads the canonical user before applying a
// partial mutation. The row lock is deliberately acquired before reading any
// mutable field: serializing only the final UPDATE would still write values
// copied from an older service snapshot over a concurrent request.
//
// The result reports authentication invalidation and, for an avatar swap,
// the exact old URL that this committed mutation replaced.
func (r *UserRepository) UpdateFieldsContext(ctx context.Context, userID int64, patch UserPatch) (UserUpdateResult, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return UserUpdateResult{}, fmt.Errorf("begin user update transaction: %w", userContextError(ctx, err))
	}
	defer tx.Rollback()
	// Profile-only patches need only the target row lock. Role or account-state
	// changes also take the global admin invariant lock so two different user
	// rows cannot concurrently remove the final active administrator.
	if patch.Role != nil || patch.IsActive != nil {
		if err := lockUserAdminInvariant(ctx, tx); err != nil {
			return UserUpdateResult{}, err
		}
	}

	current, err := scanUser(tx.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1 FOR UPDATE`, userID))
	if err == sql.ErrNoRows {
		return UserUpdateResult{}, fmt.Errorf("user not found: %w", ErrNotFound)
	}
	if err != nil {
		return UserUpdateResult{}, fmt.Errorf("load user for update: %w", userContextError(ctx, err))
	}
	currentRole := current.Role
	currentActive := current.IsActive
	if current.IsSuperAdmin && ((patch.Role != nil && *patch.Role != "admin") || (patch.IsActive != nil && !*patch.IsActive)) {
		return UserUpdateResult{}, ErrProtectedSuperAdmin
	}
	oldAvatarURL := cloneOptionalString(current.AvatarURL)
	patch.Apply(current)
	if currentRole == "admin" && currentActive && (current.Role != "admin" || !current.IsActive) {
		var activeAdmins int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role = 'admin' AND is_active = true`).Scan(&activeAdmins); err != nil {
			return UserUpdateResult{}, fmt.Errorf("count active admins: %w", userContextError(ctx, err))
		}
		if activeAdmins <= 1 {
			return UserUpdateResult{}, ErrLastActiveAdmin
		}
	}
	invalidateRuns := currentRole != current.Role || currentActive != current.IsActive

	query := `
		UPDATE users
		SET email = $1, nickname = $2, avatar_url = $3, role = $4,
		    permissions = $5, is_active = $6,
		    auth_version = auth_version + CASE WHEN $7 THEN 1 ELSE 0 END,
		    updated_at = NOW()
		WHERE id = $8
		RETURNING ` + userColumns
	updated, err := scanUser(tx.QueryRowContext(
		ctx,
		query,
		current.Email,
		current.Nickname,
		current.AvatarURL,
		current.Role,
		current.Permissions,
		current.IsActive,
		invalidateRuns,
		userID,
	))
	if err != nil {
		if IsUniqueViolation(err) {
			return UserUpdateResult{}, fmt.Errorf("%w: email already exists", ErrUserConflict)
		}
		return UserUpdateResult{}, fmt.Errorf("failed to update user: %w", userContextError(ctx, err))
	}
	if invalidateRuns {
		if err := cancelRunningChatRuns(ctx, tx, userID, nil, "account_changed", "account_changed", "账号状态已变更，请重新登录", false); err != nil {
			return UserUpdateResult{}, fmt.Errorf("cancel runs after account change: %w", userContextError(ctx, err))
		}
	}
	result := UserUpdateResult{User: updated, InvalidatedRuns: invalidateRuns}
	if patch.AvatarURLSet && !sameOptionalString(oldAvatarURL, updated.AvatarURL) {
		result.ReplacedAvatarURL = oldAvatarURL
	}
	if err := tx.Commit(); err != nil {
		return result, errors.Join(
			ErrUserCommitUnknown,
			fmt.Errorf("commit user update transaction: %w", userContextError(ctx, err)),
		)
	}
	return result, nil
}

func (r *UserRepository) IsAvatarURLReferencedContext(ctx context.Context, avatarURL string) (bool, error) {
	var referenced bool
	if err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE avatar_url = $1)`, avatarURL).Scan(&referenced); err != nil {
		return false, fmt.Errorf("check avatar URL reference: %w", userContextError(ctx, err))
	}
	return referenced, nil
}

// UpdatePassword 更新用户密码
func (r *UserRepository) UpdatePassword(userID int64, hashedPassword string) error {
	return r.UpdatePasswordContext(context.Background(), userID, hashedPassword)
}

func (r *UserRepository) UpdatePasswordContext(ctx context.Context, userID int64, hashedPassword string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin password update transaction: %w", userContextError(ctx, err))
	}
	defer tx.Rollback()
	query := `UPDATE users SET password_hash = $1, auth_version = auth_version + 1, updated_at = NOW() WHERE id = $2`
	result, err := tx.ExecContext(ctx, query, hashedPassword, userID)
	if err != nil {
		return fmt.Errorf("failed to update password: %w", userContextError(ctx, err))
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated password rows: %w", userContextError(ctx, err))
	}
	if rows != 1 {
		return fmt.Errorf("user not found: %w", ErrNotFound)
	}
	if err := cancelRunningChatRuns(ctx, tx, userID, nil, "account_changed", "account_changed", "账号状态已变更，请重新登录", false); err != nil {
		return fmt.Errorf("cancel runs after password update: %w", userContextError(ctx, err))
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit password update transaction: %w", userContextError(ctx, err))
	}
	return nil
}

// ListAll 获取所有用户（管理员用，含禁用账户）
func (r *UserRepository) ListAll(limit, offset int) ([]*model.User, error) {
	query := `
		SELECT u.id, u.username, u.email, u.password_hash, u.nickname, u.avatar_url, u.role, u.is_super_admin, u.group_id,
		       u.permissions, u.preferences, u.is_active, u.auth_version, u.created_at, u.updated_at, u.last_login_at,` +
		effectiveUserGroupIdentitySQL + `
		FROM users u` + effectiveUserGroupJoinSQL + `
		ORDER BY u.created_at DESC, u.id DESC
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
		effectiveGroup := &model.EffectiveUserGroup{}
		err := rows.Scan(
			&user.ID,
			&user.Username,
			&user.Email,
			&user.PasswordHash,
			&user.Nickname,
			&user.AvatarURL,
			&user.Role,
			&user.IsSuperAdmin,
			&user.GroupID,
			&user.Permissions,
			&user.Preferences,
			&user.IsActive,
			&user.AuthVersion,
			&user.CreatedAt,
			&user.UpdatedAt,
			&user.LastLoginAt,
			&effectiveGroup.ID,
			&effectiveGroup.Name,
			&effectiveGroup.Level,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan user: %w", err)
		}
		user.EffectiveGroup = effectiveGroup
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate users: %w", err)
	}
	return users, nil
}

// SetGroup 设置原始用户组；nil 保持为 NULL，并动态继承当前默认组。
func (r *UserRepository) SetGroup(userID int64, groupID *int64) error {
	result, err := r.db.Exec(`UPDATE users SET group_id = $1, updated_at = NOW() WHERE id = $2`, groupID, userID)
	if err != nil {
		if isUserGroupForeignKeyViolation(err) {
			return fmt.Errorf("%w: selected user group does not exist", ErrUserGroupMissing)
		}
		return fmt.Errorf("failed to set user group: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read assigned user group rows: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("user not found: %w", ErrNotFound)
	}
	return nil
}

func isUserGroupForeignKeyViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23503" && pqErr.Constraint == "users_group_id_fkey"
}

// GetGroupLevel 返回用户当前有效组的 level；NULL 用户动态继承默认组。
func (r *UserRepository) GetGroupLevel(userID int64) (int, error) {
	return r.GetGroupLevelContext(context.Background(), userID)
}

func (r *UserRepository) GetGroupLevelContext(ctx context.Context, userID int64) (int, error) {
	group, err := r.GetEffectiveGroupContext(ctx, userID)
	if err != nil {
		return 0, err
	}
	return group.Level, nil
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
	return r.GetByIDIncludeInactiveContext(context.Background(), id)
}

func (r *UserRepository) GetByIDIncludeInactiveContext(ctx context.Context, id int64) (*model.User, error) {
	user := &model.User{}
	effectiveGroup := &model.EffectiveUserGroup{}
	query := `
		SELECT u.id, u.username, u.email, u.password_hash, u.nickname, u.avatar_url, u.role, u.is_super_admin, u.group_id,
		       u.permissions, u.preferences, u.is_active, u.auth_version, u.created_at, u.updated_at, u.last_login_at,` +
		effectiveUserGroupIdentitySQL + `
		FROM users u` + effectiveUserGroupJoinSQL + `
		WHERE u.id = $1
	`
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.PasswordHash,
		&user.Nickname,
		&user.AvatarURL,
		&user.Role,
		&user.IsSuperAdmin,
		&user.GroupID,
		&user.Permissions,
		&user.Preferences,
		&user.IsActive,
		&user.AuthVersion,
		&user.CreatedAt,
		&user.UpdatedAt,
		&user.LastLoginAt,
		&effectiveGroup.ID,
		&effectiveGroup.Name,
		&effectiveGroup.Level,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("user not found: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", userContextError(ctx, err))
	}
	user.EffectiveGroup = effectiveGroup
	return user, nil
}

func userContextError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}
