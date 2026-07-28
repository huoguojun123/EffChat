package repository

import (
	"database/sql"
	"fmt"

	"github.com/huoguojun123/effchat/internal/model"
)

type ToolConfigRepository struct {
	db *sql.DB
}

func NewToolConfigRepository(db *sql.DB) *ToolConfigRepository {
	return &ToolConfigRepository{db: db}
}

const toolConfigColumns = `id, tool_key, display_name, enabled, timeout_seconds, sort_order, created_at, updated_at`

func scanToolConfig(s interface {
	Scan(dest ...interface{}) error
}) (*model.ToolConfig, error) {
	item := &model.ToolConfig{}
	if err := s.Scan(
		&item.ID, &item.Key, &item.DisplayName, &item.Enabled, &item.TimeoutSeconds,
		&item.SortOrder, &item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return item, nil
}

func (r *ToolConfigRepository) List() ([]*model.ToolConfig, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("tool config repository is unavailable")
	}
	rows, err := r.db.Query(`SELECT ` + toolConfigColumns + ` FROM tool_configs ORDER BY sort_order ASC, tool_key ASC`)
	if err != nil {
		return nil, fmt.Errorf("list tool configs: %w", err)
	}
	defer rows.Close()

	out := make([]*model.ToolConfig, 0)
	for rows.Next() {
		item, err := scanToolConfig(rows)
		if err != nil {
			return nil, fmt.Errorf("scan tool config: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tool configs: %w", err)
	}
	return out, nil
}

func (r *ToolConfigRepository) Upsert(item *model.ToolConfig) (*model.ToolConfig, error) {
	if item == nil {
		return nil, fmt.Errorf("tool config is required")
	}
	if err := r.db.QueryRow(`
		INSERT INTO tool_configs (tool_key, display_name, enabled, timeout_seconds, sort_order)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (tool_key) DO UPDATE SET
			display_name = EXCLUDED.display_name,
			enabled = EXCLUDED.enabled,
			timeout_seconds = EXCLUDED.timeout_seconds,
			sort_order = EXCLUDED.sort_order,
			updated_at = NOW()
		RETURNING `+toolConfigColumns,
		item.Key, item.DisplayName, item.Enabled, item.TimeoutSeconds, item.SortOrder,
	).Scan(
		&item.ID, &item.Key, &item.DisplayName, &item.Enabled, &item.TimeoutSeconds,
		&item.SortOrder, &item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("upsert tool config: %w", err)
	}
	return item, nil
}
