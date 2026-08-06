package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
)

// ModelRepository 模型能力表数据访问
type ModelRepository struct {
	db *sql.DB
}

func NewModelRepository(db *sql.DB) *ModelRepository {
	return &ModelRepository{db: db}
}

const modelColumns = `id, display_name, provider, vision, tool_use, reasoning,
	thinking_format, search_impl, context_window, max_output, enabled, min_group_level, sort_order,
	catalog_source, catalog_checked_at, lifecycle_status, temperature_policy, temperature_value,
	openai_top_p, openai_n, openai_presence_penalty, openai_frequency_penalty, created_at, updated_at`

// ModelPatch contains only fields present in an admin partial update. The
// repository applies it to a row selected inside the same transaction that
// writes the row so a concurrent update cannot be overwritten by a stale
// service snapshot.
type ModelPatch struct {
	DisplayName          *string
	Provider             *string
	Vision               *bool
	ToolUse              *bool
	Reasoning            *bool
	ThinkingFormat       *string
	SearchImpl           *string
	ContextWindow        *int
	MaxOutput            *int
	Enabled              *bool
	MinGroupLevel        *int
	SortOrder            *int
	CatalogSource        *string
	CatalogCheckedAt     *time.Time
	LifecycleStatus      *string
	TemperaturePolicy    *string
	TemperatureValue     *float64
	OpenAIRequestProfile *model.OpenAIRequestProfile
}

func (p ModelPatch) Apply(m *model.Model) {
	if p.DisplayName != nil {
		m.DisplayName = *p.DisplayName
	}
	if p.Provider != nil {
		m.Provider = *p.Provider
	}
	if p.Vision != nil {
		m.Vision = *p.Vision
	}
	if p.ToolUse != nil {
		m.ToolUse = *p.ToolUse
	}
	if p.Reasoning != nil {
		m.Reasoning = *p.Reasoning
	}
	if p.ThinkingFormat != nil {
		m.ThinkingFormat = modelbank.NormalizeThinkingFormat(*p.ThinkingFormat)
	}
	if p.SearchImpl != nil {
		m.SearchImpl = *p.SearchImpl
	}
	if p.ContextWindow != nil {
		m.ContextWindow = *p.ContextWindow
	}
	if p.MaxOutput != nil {
		m.MaxOutput = *p.MaxOutput
	}
	if p.Enabled != nil {
		m.Enabled = *p.Enabled
	}
	if p.MinGroupLevel != nil {
		m.MinGroupLevel = *p.MinGroupLevel
	}
	if p.SortOrder != nil {
		m.SortOrder = *p.SortOrder
	}
	if p.CatalogSource != nil {
		m.CatalogSource = model.NormalizeCatalogSource(*p.CatalogSource)
		if m.CatalogSource == model.CatalogSourceManual && p.CatalogCheckedAt == nil {
			m.CatalogCheckedAt = nil
		}
	}
	if p.CatalogCheckedAt != nil {
		m.CatalogCheckedAt = p.CatalogCheckedAt
	}
	if p.LifecycleStatus != nil {
		m.LifecycleStatus = model.NormalizeModelLifecycleStatus(*p.LifecycleStatus)
	}
	if p.TemperaturePolicy != nil {
		m.TemperaturePolicy = model.NormalizeTemperaturePolicy(*p.TemperaturePolicy)
		if m.TemperaturePolicy != model.TemperaturePolicyFixed {
			m.TemperatureValue = nil
		}
	}
	if p.TemperatureValue != nil {
		m.TemperatureValue = p.TemperatureValue
	}
	if p.OpenAIRequestProfile != nil {
		m.OpenAIRequestProfile = model.CloneOpenAIRequestProfile(*p.OpenAIRequestProfile)
	}
}

func scanModel(s interface {
	Scan(dest ...interface{}) error
}) (*model.Model, error) {
	m := &model.Model{}
	err := s.Scan(
		&m.ID, &m.DisplayName, &m.Provider, &m.Vision, &m.ToolUse, &m.Reasoning,
		&m.ThinkingFormat, &m.SearchImpl, &m.ContextWindow, &m.MaxOutput, &m.Enabled, &m.MinGroupLevel, &m.SortOrder,
		&m.CatalogSource, &m.CatalogCheckedAt, &m.LifecycleStatus, &m.TemperaturePolicy, &m.TemperatureValue,
		&m.OpenAIRequestProfile.TopP, &m.OpenAIRequestProfile.N,
		&m.OpenAIRequestProfile.PresencePenalty, &m.OpenAIRequestProfile.FrequencyPenalty,
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

// UpdateFields applies a partial model mutation against the current row.
// SELECT FOR UPDATE and the full-row write share one transaction so the
// service's older snapshot cannot overwrite fields committed by a concurrent
// request. This is intentionally model-specific instead of a generic PATCH
// framework: model validation and catalog semantics remain explicit. The
// validator runs after the lock and patch are applied, so cross-field rules
// are checked against the same snapshot that will be committed.
func (r *ModelRepository) UpdateFields(ctx context.Context, id string, patch ModelPatch, validate func(*model.Model) error) (*model.Model, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("failed to begin model update: %w", err)
	}
	defer tx.Rollback()

	current, err := scanModel(tx.QueryRow(`SELECT `+modelColumns+` FROM models WHERE id = $1 FOR UPDATE`, id))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("failed to lock model: %w", err)
	}

	patch.Apply(current)
	if validate != nil {
		if err := validate(current); err != nil {
			return nil, err
		}
	}
	updated, err := scanModel(tx.QueryRow(`
		UPDATE models SET
			display_name = $2, provider = $3, vision = $4, tool_use = $5, reasoning = $6,
			thinking_format = $7, search_impl = $8, context_window = $9, max_output = $10,
			enabled = $11, min_group_level = $12, sort_order = $13, catalog_source = $14,
			catalog_checked_at = $15, lifecycle_status = $16, temperature_policy = $17,
			temperature_value = $18, openai_top_p = $19, openai_n = $20,
			openai_presence_penalty = $21, openai_frequency_penalty = $22
		WHERE id = $1
		RETURNING `+modelColumns,
		id, current.DisplayName, current.Provider, current.Vision, current.ToolUse, current.Reasoning,
		current.ThinkingFormat, current.SearchImpl, current.ContextWindow, current.MaxOutput,
		current.Enabled, current.MinGroupLevel, current.SortOrder, current.CatalogSource,
		current.CatalogCheckedAt, current.LifecycleStatus, current.TemperaturePolicy, current.TemperatureValue,
		current.OpenAIRequestProfile.TopP, current.OpenAIRequestProfile.N,
		current.OpenAIRequestProfile.PresencePenalty, current.OpenAIRequestProfile.FrequencyPenalty,
	))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("failed to update model fields: %w", err)
	}
	if err := tx.Commit(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("failed to commit model update: %w", err)
	}
	return updated, nil
}

// Upsert 插入或更新模型（以 id 为主键）
func (r *ModelRepository) Upsert(m *model.Model) error {
	query := `
		INSERT INTO models (id, display_name, provider, vision, tool_use, reasoning,
			thinking_format, search_impl, context_window, max_output, enabled, min_group_level, sort_order,
			catalog_source, catalog_checked_at, lifecycle_status, temperature_policy, temperature_value,
			openai_top_p, openai_n, openai_presence_penalty, openai_frequency_penalty)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22)
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
			sort_order = EXCLUDED.sort_order,
			catalog_source = EXCLUDED.catalog_source,
			catalog_checked_at = EXCLUDED.catalog_checked_at,
			lifecycle_status = EXCLUDED.lifecycle_status,
			temperature_policy = EXCLUDED.temperature_policy,
			temperature_value = EXCLUDED.temperature_value,
			openai_top_p = EXCLUDED.openai_top_p,
			openai_n = EXCLUDED.openai_n,
			openai_presence_penalty = EXCLUDED.openai_presence_penalty,
			openai_frequency_penalty = EXCLUDED.openai_frequency_penalty
		RETURNING created_at, updated_at
	`
	err := r.db.QueryRow(
		query,
		m.ID, m.DisplayName, m.Provider, m.Vision, m.ToolUse, m.Reasoning,
		modelbank.NormalizeThinkingFormat(m.ThinkingFormat), m.SearchImpl, m.ContextWindow, m.MaxOutput, m.Enabled, m.MinGroupLevel, m.SortOrder,
		model.NormalizeCatalogSource(m.CatalogSource), m.CatalogCheckedAt, model.NormalizeModelLifecycleStatus(m.LifecycleStatus),
		model.NormalizeTemperaturePolicy(m.TemperaturePolicy), m.TemperatureValue,
		m.OpenAIRequestProfile.TopP, m.OpenAIRequestProfile.N,
		m.OpenAIRequestProfile.PresencePenalty, m.OpenAIRequestProfile.FrequencyPenalty,
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
