package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/huoguojun123/EffChat/internal/model"
)

type toolConfigState struct {
	Key            string `json:"key"`
	DisplayName    string `json:"display_name"`
	Enabled        bool   `json:"enabled"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	SortOrder      int    `json:"sort_order"`
}

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

func (r *ToolConfigRepository) ListContext(ctx context.Context) ([]*model.ToolConfig, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("tool config repository is unavailable")
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+toolConfigColumns+` FROM tool_configs ORDER BY sort_order ASC, tool_key ASC`)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
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
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("iterate tool configs: %w", err)
	}
	return out, nil
}

// SaveGoverned owns both the catalog write and its audit event. Keeping them in
// one transaction prevents a successful Tool mutation from becoming invisible
// in governance history, including when the event insert itself fails.
func (r *ToolConfigRepository) SaveGoverned(ctx context.Context, item *model.ToolConfig, actorUserID int64, reason string) (*model.ToolConfig, *model.GovernanceEvent, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("begin governed tool save: %w", err)
	}
	defer tx.Rollback()
	before, err := getToolConfigForUpdateTx(ctx, tx, item.Key)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, nil, err
	}
	action := "update"
	if errors.Is(err, ErrNotFound) {
		before = nil
		action = "create"
	}
	saved, err := upsertToolConfigTx(ctx, tx, item)
	if err != nil {
		return nil, nil, err
	}
	event := &model.GovernanceEvent{
		ResourceType: "tool", ResourceKey: saved.Key, Action: action,
		ActorType: "admin", ActorUserID: &actorUserID, Reason: reason,
		BeforeState: marshalToolConfigState(before), AfterState: marshalToolConfigState(saved),
	}
	if err := InsertGovernanceEventTx(ctx, tx, event); err != nil {
		return nil, nil, fmt.Errorf("audit governed tool save: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit governed tool save: %w", err)
	}
	return saved, event, nil
}

// RollbackGoverned never rewrites the source event. It locks the source and the
// current Tool row, requires current state to equal the source after-state, and
// appends one reverse event. The partial unique index on rollback_of_event_id is
// the final cross-process guard against applying the same rollback twice.
func (r *ToolConfigRepository) RollbackGoverned(ctx context.Context, eventID, actorUserID int64, reason string) (*model.ToolConfig, *model.GovernanceEvent, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("begin governed tool rollback: %w", err)
	}
	defer tx.Rollback()
	source, err := GetGovernanceEventForUpdateTx(ctx, tx, eventID)
	if err != nil {
		return nil, nil, err
	}
	if source.ResourceType != "tool" {
		return nil, nil, fmt.Errorf("%w: event does not target a Tool", ErrGovernanceConflict)
	}
	var alreadyRolledBack bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM governance_events WHERE rollback_of_event_id = $1)`, eventID).Scan(&alreadyRolledBack); err != nil {
		return nil, nil, fmt.Errorf("check prior Tool rollback: %w", err)
	}
	if alreadyRolledBack {
		return nil, nil, fmt.Errorf("%w: event was already rolled back", ErrGovernanceConflict)
	}
	current, err := getToolConfigForUpdateTx(ctx, tx, source.ResourceKey)
	if err != nil {
		return nil, nil, err
	}
	if !equalJSON(marshalToolConfigState(current), source.AfterState) {
		return nil, nil, fmt.Errorf("%w: Tool changed after the selected event", ErrGovernanceConflict)
	}
	var restored *model.ToolConfig
	if len(source.BeforeState) == 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM tool_configs WHERE tool_key = $1`, source.ResourceKey); err != nil {
			return nil, nil, fmt.Errorf("delete rolled back Tool config: %w", err)
		}
	} else {
		state := toolConfigState{}
		if err := json.Unmarshal(source.BeforeState, &state); err != nil {
			return nil, nil, fmt.Errorf("decode Tool rollback state: %w", err)
		}
		restored, err = upsertToolConfigTx(ctx, tx, state.model())
		if err != nil {
			return nil, nil, err
		}
	}
	rollbackID := source.ID
	reverse := &model.GovernanceEvent{
		ResourceType: "tool", ResourceKey: source.ResourceKey, Action: "rollback",
		ActorType: "admin", ActorUserID: &actorUserID, Reason: reason,
		BeforeState: marshalToolConfigState(current), AfterState: source.BeforeState,
		RollbackOfEventID: &rollbackID,
	}
	if err := InsertGovernanceEventTx(ctx, tx, reverse); err != nil {
		return nil, nil, fmt.Errorf("audit governed Tool rollback: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit governed Tool rollback: %w", err)
	}
	return restored, reverse, nil
}

func getToolConfigForUpdateTx(ctx context.Context, tx *sql.Tx, key string) (*model.ToolConfig, error) {
	item, err := scanToolConfig(tx.QueryRowContext(ctx, `SELECT `+toolConfigColumns+` FROM tool_configs WHERE tool_key = $1 FOR UPDATE`, key))
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("tool config not found: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get Tool config for update: %w", err)
	}
	return item, nil
}

func upsertToolConfigTx(ctx context.Context, tx *sql.Tx, item *model.ToolConfig) (*model.ToolConfig, error) {
	saved := *item
	if err := tx.QueryRowContext(ctx, `
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
	).Scan(&saved.ID, &saved.Key, &saved.DisplayName, &saved.Enabled, &saved.TimeoutSeconds,
		&saved.SortOrder, &saved.CreatedAt, &saved.UpdatedAt); err != nil {
		return nil, fmt.Errorf("upsert governed Tool config: %w", err)
	}
	return &saved, nil
}

func marshalToolConfigState(item *model.ToolConfig) json.RawMessage {
	if item == nil {
		return nil
	}
	value, _ := json.Marshal(toolConfigState{item.Key, item.DisplayName, item.Enabled, item.TimeoutSeconds, item.SortOrder})
	return value
}

func (state toolConfigState) model() *model.ToolConfig {
	return &model.ToolConfig{Key: state.Key, DisplayName: state.DisplayName, Enabled: state.Enabled, TimeoutSeconds: state.TimeoutSeconds, SortOrder: state.SortOrder}
}

func equalJSON(left, right []byte) bool {
	var a, b interface{}
	return json.Unmarshal(left, &a) == nil && json.Unmarshal(right, &b) == nil && reflect.DeepEqual(a, b)
}
