package repository

import (
	"database/sql"
	"fmt"

	"github.com/huoguojun123/effchat/internal/model"
	"github.com/huoguojun123/effchat/internal/modelbank"
)

// ModelRepository 模型能力表数据访问
type ModelRepository struct {
	db *sql.DB
}

func NewModelRepository(db *sql.DB) *ModelRepository {
	return &ModelRepository{db: db}
}

const modelColumns = `id, display_name, provider, vision, tool_use, reasoning,
	thinking_format, search_impl, context_window, max_output, enabled, min_group_level, sort_order, created_at, updated_at`

func scanModel(s interface {
	Scan(dest ...interface{}) error
}) (*model.Model, error) {
	m := &model.Model{}
	err := s.Scan(
		&m.ID, &m.DisplayName, &m.Provider, &m.Vision, &m.ToolUse, &m.Reasoning,
		&m.ThinkingFormat, &m.SearchImpl, &m.ContextWindow, &m.MaxOutput, &m.Enabled, &m.MinGroupLevel, &m.SortOrder,
		&m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return m, nil
}

// List 返回所有模型，按 sort_order 升序。onlyEnabled 为 true 时仅返回启用项。
func (r *ModelRepository) List(onlyEnabled bool) ([]*model.Model, error) {
	query := `SELECT ` + modelColumns + ` FROM models`
	if onlyEnabled {
		query += ` WHERE enabled = true`
	}
	query += ` ORDER BY sort_order ASC, id ASC`

	rows, err := r.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to list models: %w", err)
	}
	defer rows.Close()

	var result []*model.Model
	for rows.Next() {
		m, err := scanModel(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan model: %w", err)
		}
		result = append(result, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate models: %w", err)
	}
	return result, nil
}

// ListVisible 返回对指定组等级可见的启用模型：enabled=true 且 min_group_level <= maxLevel。
// 用于普通用户的模型列表（管理员请直接用 List(false) 看全部）。
func (r *ModelRepository) ListVisible(maxLevel int) ([]*model.Model, error) {
	query := `SELECT ` + modelColumns + ` FROM models
		WHERE enabled = true AND min_group_level <= $1
		ORDER BY sort_order ASC, id ASC`

	rows, err := r.db.Query(query, maxLevel)
	if err != nil {
		return nil, fmt.Errorf("failed to list visible models: %w", err)
	}
	defer rows.Close()

	var result []*model.Model
	for rows.Next() {
		m, err := scanModel(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan model: %w", err)
		}
		result = append(result, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate models: %w", err)
	}
	return result, nil
}
func (r *ModelRepository) Get(id string) (*model.Model, error) {
	query := `SELECT ` + modelColumns + ` FROM models WHERE id = $1`
	m, err := scanModel(r.db.QueryRow(query, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get model: %w", err)
	}
	return m, nil
}

// Upsert 插入或更新模型（以 id 为主键）
func (r *ModelRepository) Upsert(m *model.Model) error {
	query := `
		INSERT INTO models (id, display_name, provider, vision, tool_use, reasoning,
			thinking_format, search_impl, context_window, max_output, enabled, min_group_level, sort_order)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (id) DO UPDATE SET
			display_name = EXCLUDED.display_name,
			provider = EXCLUDED.provider,
			vision = EXCLUDED.vision,
			tool_use = EXCLUDED.tool_use,
			reasoning = EXCLUDED.reasoning,
			thinking_format = EXCLUDED.thinking_format,
			search_impl = EXCLUDED.search_impl,
			context_window = EXCLUDED.context_window,
			max_output = EXCLUDED.max_output,
			enabled = EXCLUDED.enabled,
			min_group_level = EXCLUDED.min_group_level,
			sort_order = EXCLUDED.sort_order
		RETURNING created_at, updated_at
	`
	err := r.db.QueryRow(
		query,
		m.ID, m.DisplayName, m.Provider, m.Vision, m.ToolUse, m.Reasoning,
		modelbank.NormalizeThinkingFormat(m.ThinkingFormat), m.SearchImpl, m.ContextWindow, m.MaxOutput, m.Enabled, m.MinGroupLevel, m.SortOrder,
	).Scan(&m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to upsert model: %w", err)
	}
	return nil
}

// Delete 删除模型
func (r *ModelRepository) Delete(id string) error {
	res, err := r.db.Exec(`DELETE FROM models WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete model: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("model not found: %s", id)
	}
	return nil
}
