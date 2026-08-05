package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/huoguojun123/EffChat/internal/model"
)

var ErrExternalServiceOrderInvalid = errors.New("invalid external service order")

type ChannelRepository struct {
	db *sql.DB
}

func NewChannelRepository(db *sql.DB) *ChannelRepository {
	return &ChannelRepository{db: db}
}

const aiChannelColumns = `id, channel_key, display_name, adapter, base_url, api_key, enabled, sort_order, created_at, updated_at`

func scanAIChannel(s interface {
	Scan(dest ...interface{}) error
}) (*model.AIChannel, error) {
	item := &model.AIChannel{}
	var apiKey sql.NullString
	if err := s.Scan(
		&item.ID, &item.Key, &item.DisplayName, &item.Adapter, &item.BaseURL, &apiKey,
		&item.Enabled, &item.SortOrder, &item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if apiKey.Valid {
		item.APIKey = apiKey.String
		item.APIKeySet = strings.TrimSpace(apiKey.String) != ""
	}
	return item, nil
}

func (r *ChannelRepository) ListAIChannels(includeDisabled bool) ([]*model.AIChannel, error) {
	return r.ListAIChannelsContext(context.Background(), includeDisabled)
}

func (r *ChannelRepository) ListAIChannelsContext(ctx context.Context, includeDisabled bool) ([]*model.AIChannel, error) {
	query := `SELECT ` + aiChannelColumns + ` FROM ai_channels WHERE deleted_at IS NULL`
	if !includeDisabled {
		query += ` AND enabled = true`
	}
	query += ` ORDER BY sort_order ASC, channel_key ASC`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list ai channels: %w", channelContextError(ctx, err))
	}
	defer rows.Close()

	var out []*model.AIChannel
	for rows.Next() {
		item, err := scanAIChannel(rows)
		if err != nil {
			return nil, fmt.Errorf("scan ai channel: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ai channels: %w", channelContextError(ctx, err))
	}
	return out, nil
}

func (r *ChannelRepository) GetAIChannel(key string) (*model.AIChannel, error) {
	return r.GetAIChannelContext(context.Background(), key)
}

func (r *ChannelRepository) GetAIChannelContext(ctx context.Context, key string) (*model.AIChannel, error) {
	item, err := scanAIChannel(r.db.QueryRowContext(ctx, `SELECT `+aiChannelColumns+` FROM ai_channels WHERE channel_key = $1 AND deleted_at IS NULL`, normalizeConfigKey(key)))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get ai channel: %w", channelContextError(ctx, err))
	}
	return item, nil
}

func (r *ChannelRepository) UpsertAIChannel(item *model.AIChannel, replaceAPIKey bool) error {
	if item == nil {
		return fmt.Errorf("ai channel is required")
	}
	item.Key = normalizeConfigKey(item.Key)
	item.Adapter = normalizeConfigKey(item.Adapter)
	if replaceAPIKey {
		return r.db.QueryRow(`
			INSERT INTO ai_channels (channel_key, display_name, adapter, base_url, api_key, enabled, sort_order, deleted_at)
			VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, NULL)
			ON CONFLICT (channel_key) DO UPDATE SET
				display_name = EXCLUDED.display_name,
				adapter = EXCLUDED.adapter,
				base_url = EXCLUDED.base_url,
				api_key = EXCLUDED.api_key,
				enabled = EXCLUDED.enabled,
				sort_order = EXCLUDED.sort_order,
				deleted_at = NULL,
				updated_at = NOW()
			RETURNING id, created_at, updated_at
		`, item.Key, item.DisplayName, item.Adapter, strings.TrimSpace(item.BaseURL), strings.TrimSpace(item.APIKey), item.Enabled, item.SortOrder).
			Scan(&item.ID, &item.CreatedAt, &item.UpdatedAt)
	}
	return r.db.QueryRow(`
		INSERT INTO ai_channels (channel_key, display_name, adapter, base_url, enabled, sort_order, deleted_at)
		VALUES ($1, $2, $3, $4, $5, $6, NULL)
	ON CONFLICT (channel_key) DO UPDATE SET
			display_name = EXCLUDED.display_name,
				adapter = EXCLUDED.adapter,
				base_url = EXCLUDED.base_url,
				api_key = CASE WHEN ai_channels.deleted_at IS NOT NULL THEN NULL ELSE ai_channels.api_key END,
				enabled = EXCLUDED.enabled,
			sort_order = EXCLUDED.sort_order,
			deleted_at = NULL,
			updated_at = NOW()
		RETURNING id, created_at, updated_at
	`, item.Key, item.DisplayName, item.Adapter, strings.TrimSpace(item.BaseURL), item.Enabled, item.SortOrder).
		Scan(&item.ID, &item.CreatedAt, &item.UpdatedAt)
}

func (r *ChannelRepository) DeleteAIChannel(key string) error {
	res, err := r.db.Exec(`UPDATE ai_channels SET deleted_at = NOW(), enabled = false, updated_at = NOW() WHERE channel_key = $1 AND deleted_at IS NULL`, normalizeConfigKey(key))
	if err != nil {
		return fmt.Errorf("delete ai channel: %w", err)
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return fmt.Errorf("ai channel not found: %w", ErrNotFound)
	}
	return nil
}

const externalServiceColumns = `id, service_key, display_name, kind, base_url, api_key, enabled, sort_order, max_concurrency, created_at, updated_at`

func scanExternalService(s interface {
	Scan(dest ...interface{}) error
}) (*model.ExternalService, error) {
	item := &model.ExternalService{}
	var apiKey sql.NullString
	if err := s.Scan(
		&item.ID, &item.Key, &item.DisplayName, &item.Kind, &item.BaseURL, &apiKey,
		&item.Enabled, &item.SortOrder, &item.MaxConcurrency, &item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if apiKey.Valid {
		item.APIKey = apiKey.String
		item.APIKeySet = strings.TrimSpace(apiKey.String) != ""
	}
	return item, nil
}

func (r *ChannelRepository) ListExternalServices(includeDisabled bool) ([]*model.ExternalService, error) {
	return r.ListExternalServicesContext(context.Background(), includeDisabled)
}

func (r *ChannelRepository) ListExternalServicesContext(ctx context.Context, includeDisabled bool) ([]*model.ExternalService, error) {
	query := `SELECT ` + externalServiceColumns + ` FROM external_services WHERE deleted_at IS NULL`
	if !includeDisabled {
		query += ` AND enabled = true`
	}
	query += ` ORDER BY sort_order ASC, service_key ASC`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list external services: %w", channelContextError(ctx, err))
	}
	defer rows.Close()

	var out []*model.ExternalService
	for rows.Next() {
		item, err := scanExternalService(rows)
		if err != nil {
			return nil, fmt.Errorf("scan external service: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate external services: %w", channelContextError(ctx, err))
	}
	return out, nil
}

func (r *ChannelRepository) GetExternalService(key string) (*model.ExternalService, error) {
	return r.GetExternalServiceContext(context.Background(), key)
}

func (r *ChannelRepository) GetExternalServiceContext(ctx context.Context, key string) (*model.ExternalService, error) {
	item, err := scanExternalService(r.db.QueryRowContext(ctx, `SELECT `+externalServiceColumns+` FROM external_services WHERE service_key = $1 AND deleted_at IS NULL`, normalizeConfigKey(key)))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get external service: %w", channelContextError(ctx, err))
	}
	return item, nil
}

func (r *ChannelRepository) SaveExternalServiceContext(ctx context.Context, item *model.ExternalService, replaceAPIKey bool) error {
	if item == nil {
		return fmt.Errorf("external service is required")
	}
	item.Key = normalizeConfigKey(item.Key)
	item.Kind = normalizeConfigKey(item.Kind)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin external service save: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('effchat_external_service_order'), hashtext($1))`, item.Kind); err != nil {
		return fmt.Errorf("lock external service order: %w", channelContextError(ctx, err))
	}
	var currentSortOrder int
	err = tx.QueryRowContext(ctx, `
		SELECT sort_order
		FROM external_services
		WHERE service_key = $1 AND deleted_at IS NULL
		FOR UPDATE
	`, item.Key).Scan(&currentSortOrder)
	switch {
	case err == nil:
		item.SortOrder = currentSortOrder
	case err == sql.ErrNoRows:
		if err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(MAX(sort_order), 0) + 10
			FROM external_services
			WHERE kind = $1 AND deleted_at IS NULL
		`, item.Kind).Scan(&item.SortOrder); err != nil {
			return fmt.Errorf("next external service sort order: %w", channelContextError(ctx, err))
		}
	default:
		return fmt.Errorf("get external service sort order: %w", channelContextError(ctx, err))
	}

	if replaceAPIKey {
		err = tx.QueryRowContext(ctx, `
			INSERT INTO external_services (service_key, display_name, kind, base_url, api_key, enabled, sort_order, max_concurrency, deleted_at)
			VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, NULL)
			ON CONFLICT (service_key) DO UPDATE SET
				display_name = EXCLUDED.display_name,
				kind = EXCLUDED.kind,
				base_url = EXCLUDED.base_url,
				api_key = EXCLUDED.api_key,
				enabled = EXCLUDED.enabled,
				sort_order = EXCLUDED.sort_order,
				max_concurrency = EXCLUDED.max_concurrency,
				deleted_at = NULL,
				updated_at = NOW()
			RETURNING id, created_at, updated_at
		`, item.Key, item.DisplayName, item.Kind, strings.TrimSpace(item.BaseURL), strings.TrimSpace(item.APIKey), item.Enabled, item.SortOrder, item.MaxConcurrency).
			Scan(&item.ID, &item.CreatedAt, &item.UpdatedAt)
	} else {
		err = tx.QueryRowContext(ctx, `
			INSERT INTO external_services (service_key, display_name, kind, base_url, enabled, sort_order, max_concurrency, deleted_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, NULL)
			ON CONFLICT (service_key) DO UPDATE SET
				display_name = EXCLUDED.display_name,
				kind = EXCLUDED.kind,
				base_url = EXCLUDED.base_url,
				api_key = CASE WHEN external_services.deleted_at IS NOT NULL THEN NULL ELSE external_services.api_key END,
				enabled = EXCLUDED.enabled,
				sort_order = EXCLUDED.sort_order,
				max_concurrency = EXCLUDED.max_concurrency,
				deleted_at = NULL,
				updated_at = NOW()
			RETURNING id, created_at, updated_at
		`, item.Key, item.DisplayName, item.Kind, strings.TrimSpace(item.BaseURL), item.Enabled, item.SortOrder, item.MaxConcurrency).
			Scan(&item.ID, &item.CreatedAt, &item.UpdatedAt)
	}
	if err != nil {
		return fmt.Errorf("save external service: %w", channelContextError(ctx, err))
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit external service save: %w", channelContextError(ctx, err))
	}
	return nil
}

func (r *ChannelRepository) DeleteExternalService(key string) error {
	res, err := r.db.Exec(`UPDATE external_services SET deleted_at = NOW(), enabled = false, updated_at = NOW() WHERE service_key = $1 AND deleted_at IS NULL`, normalizeConfigKey(key))
	if err != nil {
		return fmt.Errorf("delete external service: %w", err)
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return fmt.Errorf("external service not found: %w", ErrNotFound)
	}
	return nil
}

func (r *ChannelRepository) ReorderExternalServices(kind string, keys []string) error {
	return r.ReorderExternalServicesContext(context.Background(), kind, keys)
}

func (r *ChannelRepository) ReorderExternalServicesContext(ctx context.Context, kind string, keys []string) error {
	kind = normalizeConfigKey(kind)
	if len(keys) == 0 {
		return fmt.Errorf("%w: service order cannot be empty", ErrExternalServiceOrderInvalid)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin external service reorder: %w", channelContextError(ctx, err))
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT service_key FROM external_services WHERE kind = $1 AND deleted_at IS NULL AND service_key <> 'basic' FOR UPDATE`, kind)
	if err != nil {
		return fmt.Errorf("lock external services: %w", channelContextError(ctx, err))
	}
	existing := make(map[string]struct{})
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return fmt.Errorf("scan external service key: %w", channelContextError(ctx, err))
		}
		existing[normalizeConfigKey(key)] = struct{}{}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close external service keys: %w", channelContextError(ctx, err))
	}
	if len(existing) != len(keys) {
		return fmt.Errorf("%w: service order must include every configured %s service", ErrExternalServiceOrderInvalid, kind)
	}
	seen := make(map[string]struct{}, len(keys))
	for index, raw := range keys {
		key := normalizeConfigKey(raw)
		if _, ok := existing[key]; !ok {
			return fmt.Errorf("%w: service %q is not configured for %s", ErrExternalServiceOrderInvalid, raw, kind)
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("%w: service order contains duplicate %q", ErrExternalServiceOrderInvalid, raw)
		}
		seen[key] = struct{}{}
		if _, err := tx.ExecContext(ctx, `UPDATE external_services SET sort_order = $1, updated_at = NOW() WHERE service_key = $2 AND kind = $3 AND deleted_at IS NULL`, (index+1)*10, key, kind); err != nil {
			return fmt.Errorf("update external service order: %w", channelContextError(ctx, err))
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit external service reorder: %w", channelContextError(ctx, err))
	}
	return nil
}

func normalizeConfigKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func channelContextError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}
