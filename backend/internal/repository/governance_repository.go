package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/huoguojun123/EffChat/internal/model"
)

var ErrGovernanceConflict = errors.New("governance state conflict")

type GovernanceRepository struct {
	db *sql.DB
}

func NewGovernanceRepository(db *sql.DB) *GovernanceRepository {
	return &GovernanceRepository{db: db}
}

const governanceEventColumns = `id, resource_type, resource_key, action, actor_type, actor_user_id,
	reason, before_state, after_state, skill_import_record_id, rollback_of_event_id, created_at`

type governanceEventScanner interface {
	Scan(dest ...interface{}) error
}

func scanGovernanceEvent(row governanceEventScanner) (*model.GovernanceEvent, error) {
	event := &model.GovernanceEvent{}
	if err := row.Scan(
		&event.ID, &event.ResourceType, &event.ResourceKey, &event.Action,
		&event.ActorType, &event.ActorUserID, &event.Reason, &event.BeforeState,
		&event.AfterState, &event.SkillImportRecordID, &event.RollbackOfEventID,
		&event.CreatedAt,
	); err != nil {
		return nil, err
	}
	return event, nil
}

// InsertEventTx appends audit history inside the same transaction that owns
// the catalog mutation. Callers must never insert an event after committing
// the resource change, because that would allow unaudited successful writes.
func InsertGovernanceEventTx(ctx context.Context, tx *sql.Tx, event *model.GovernanceEvent) error {
	if tx == nil || event == nil {
		return fmt.Errorf("governance transaction and event are required")
	}
	return tx.QueryRowContext(ctx, `
		INSERT INTO governance_events (
			resource_type, resource_key, action, actor_type, actor_user_id, reason,
			before_state, after_state, skill_import_record_id, rollback_of_event_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8::jsonb, $9, $10)
		RETURNING id, created_at
	`, event.ResourceType, event.ResourceKey, event.Action, event.ActorType,
		event.ActorUserID, event.Reason, nullableJSON(event.BeforeState),
		nullableJSON(event.AfterState), event.SkillImportRecordID,
		event.RollbackOfEventID,
	).Scan(&event.ID, &event.CreatedAt)
}

func nullableJSON(value []byte) interface{} {
	if len(value) == 0 {
		return nil
	}
	return string(value)
}

func (r *GovernanceRepository) List(ctx context.Context, resourceType, resourceKey string, limit int) ([]*model.GovernanceEvent, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+governanceEventColumns+`
		FROM governance_events
		WHERE resource_type = $1 AND resource_key = $2
		ORDER BY created_at DESC, id DESC
		LIMIT $3`, resourceType, resourceKey, limit)
	if err != nil {
		return nil, fmt.Errorf("list governance events: %w", err)
	}
	defer rows.Close()
	events := make([]*model.GovernanceEvent, 0)
	for rows.Next() {
		event, err := scanGovernanceEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan governance event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate governance events: %w", err)
	}
	return events, nil
}

func GetGovernanceEventForUpdateTx(ctx context.Context, tx *sql.Tx, id int64) (*model.GovernanceEvent, error) {
	event, err := scanGovernanceEvent(tx.QueryRowContext(ctx, `SELECT `+governanceEventColumns+`
		FROM governance_events WHERE id = $1 FOR UPDATE`, id))
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("governance event not found: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get governance event: %w", err)
	}
	return event, nil
}
