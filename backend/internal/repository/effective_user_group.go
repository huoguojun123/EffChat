package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/huoguojun123/EffChat/internal/model"
)

// model.EffectiveUserGroup is the group whose permissions and quotas apply to a user.
// GroupID remains the raw assignment on users; a NULL assignment inherits the
// single current default group dynamically, so changing the default group does
// not require rewriting every inheriting user.
const effectiveUserGroupJoinSQL = `
	LEFT JOIN user_groups assigned_group ON assigned_group.id = u.group_id
	LEFT JOIN LATERAL (
		SELECT id, name, level,
		       daily_message_limit, daily_token_limit, concurrent_run_limit,
		       daily_tool_call_limit, daily_web_search_limit, daily_web_extract_limit,
		       daily_ocr_file_limit, daily_ocr_page_limit
		FROM user_groups
		WHERE is_default = true
		ORDER BY level ASC, id ASC
		LIMIT 1
	) default_group ON true`

const effectiveUserGroupIdentitySQL = `
	COALESCE(assigned_group.id, default_group.id),
	COALESCE(assigned_group.name, default_group.name),
	COALESCE(assigned_group.level, default_group.level)`

func scanEffectiveUserGroup(scanner interface {
	Scan(dest ...interface{}) error
}) (model.EffectiveUserGroup, error) {
	var group model.EffectiveUserGroup
	if err := scanner.Scan(&group.ID, &group.Name, &group.Level); err != nil {
		return model.EffectiveUserGroup{}, err
	}
	return group, nil
}

func (r *UserRepository) GetEffectiveGroupContext(ctx context.Context, userID int64) (model.EffectiveUserGroup, error) {
	query := `SELECT ` + effectiveUserGroupIdentitySQL + `
		FROM users u` + effectiveUserGroupJoinSQL + `
		WHERE u.id = $1`
	group, err := scanEffectiveUserGroup(r.db.QueryRowContext(ctx, query, userID))
	if err == sql.ErrNoRows {
		return model.EffectiveUserGroup{}, fmt.Errorf("user not found: %w", ErrNotFound)
	}
	if err != nil {
		return model.EffectiveUserGroup{}, fmt.Errorf("failed to get effective user group: %w", err)
	}
	return group, nil
}
