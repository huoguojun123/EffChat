package usage

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type Repository struct {
	db *sql.DB
}

func (r *Repository) DeleteOlderThan(ctx context.Context, cutoff time.Time) error {
	if r == nil || r.db == nil {
		return nil
	}
	if _, err := r.db.ExecContext(ctx, `DELETE FROM model_usage_events WHERE created_at < $1`, cutoff); err != nil {
		return fmt.Errorf("delete expired model usage events: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, `DELETE FROM tool_usage_events WHERE created_at < $1`, cutoff); err != nil {
		return fmt.Errorf("delete expired tool usage events: %w", err)
	}
	return nil
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Create(ctx context.Context, event *Event) error {
	query := `
		INSERT INTO model_usage_events (
			user_id, session_id, message_id, run_id, kind, provider, model_id, success,
			prompt_tokens, completion_tokens, total_tokens, cached_tokens, reasoning_tokens,
			duration_ms, error_type, error_message
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		RETURNING id, created_at
	`
	err := r.db.QueryRowContext(
		ctx,
		query,
		nullInt64(event.UserID),
		nullInt64(event.SessionID),
		nullInt64(event.MessageID),
		nullString(event.RunID),
		event.Kind,
		event.Provider,
		event.ModelID,
		event.Success,
		event.PromptTokens,
		event.CompletionTokens,
		event.TotalTokens,
		event.CachedTokens,
		event.ReasoningTokens,
		event.DurationMs,
		nullString(event.ErrorType),
		nullString(event.ErrorMessage),
	).Scan(&event.ID, &event.CreatedAt)
	if err != nil {
		return fmt.Errorf("create model usage event: %w", err)
	}
	return nil
}

func (r *Repository) CreateToolEvent(ctx context.Context, event *ToolEvent) error {
	query := `
		INSERT INTO tool_usage_events (
			user_id, session_id, run_id, call_id, tool_key, success,
			context_tokens, truncated, duration_ms, error_type, error_message
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, created_at
	`
	err := r.db.QueryRowContext(
		ctx,
		query,
		nullInt64(event.UserID),
		nullInt64(event.SessionID),
		nullString(event.RunID),
		nullString(event.CallID),
		event.ToolKey,
		event.Success,
		event.ContextTokens,
		event.Truncated,
		event.DurationMs,
		nullString(event.ErrorType),
		nullString(event.ErrorMessage),
	).Scan(&event.ID, &event.CreatedAt)
	if err != nil {
		return fmt.Errorf("create tool usage event: %w", err)
	}
	return nil
}

func (r *Repository) UpdateToolEvent(ctx context.Context, event *ToolEvent) error {
	query := `
		UPDATE tool_usage_events
		SET success = $2,
			context_tokens = $3,
			truncated = $4,
			duration_ms = $5,
			error_type = $6,
			error_message = $7
		WHERE id = $1
	`
	result, err := r.db.ExecContext(
		ctx,
		query,
		event.ID,
		event.Success,
		event.ContextTokens,
		event.Truncated,
		event.DurationMs,
		nullString(event.ErrorType),
		nullString(event.ErrorMessage),
	)
	if err != nil {
		return fmt.Errorf("update tool usage event: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("update tool usage event: id %d not found", event.ID)
	}
	return nil
}

func (r *Repository) Aggregate(ctx context.Context, start, end time.Time) (*Summary, error) {
	totals, err := r.aggregateTotals(ctx, start, end)
	if err != nil {
		return nil, err
	}
	runTotals, err := r.aggregateRunTotals(ctx, start, end)
	if err != nil {
		return nil, err
	}
	byUser, err := r.aggregateByUser(ctx, start, end)
	if err != nil {
		return nil, err
	}
	byModel, err := r.aggregateByModel(ctx, start, end)
	if err != nil {
		return nil, err
	}
	byKind, err := r.aggregateByKind(ctx, start, end)
	if err != nil {
		return nil, err
	}
	return &Summary{Totals: totals, RunTotals: runTotals, ByUser: byUser, ByModel: byModel, ByKind: byKind}, nil
}

func (r *Repository) AggregateToolUsage(ctx context.Context, start, end time.Time) (ToolTotals, []ByTool, error) {
	totals, err := r.aggregateToolTotals(ctx, start, end)
	if err != nil {
		return ToolTotals{}, nil, err
	}
	byTool, err := r.aggregateByTool(ctx, start, end)
	if err != nil {
		return ToolTotals{}, nil, err
	}
	return totals, byTool, nil
}

func (r *Repository) QuotaUsersForToday(ctx context.Context) ([]QuotaUserUsage, error) {
	query := `
		WITH bounds AS (
			SELECT date_trunc('day', NOW()) AS start_at, date_trunc('day', NOW()) + INTERVAL '1 day' AS reset_at
		),
		default_group AS (
			SELECT *
			FROM user_groups
			WHERE is_default = true
			ORDER BY level ASC, id ASC
			LIMIT 1
		),
		message_usage AS (
			SELECT s.user_id, COUNT(*) AS daily_messages
			FROM messages m
			JOIN sessions s ON s.id = m.session_id
			CROSS JOIN bounds b
			WHERE m.role = 'user' AND m.created_at >= b.start_at
			GROUP BY s.user_id
		),
		model_usage AS (
			SELECT user_id, COALESCE(SUM(total_tokens), 0) AS daily_model_tokens
			FROM model_usage_events e
			CROSS JOIN bounds b
			WHERE e.created_at >= b.start_at
			GROUP BY user_id
		),
		tool_usage AS (
			SELECT
				user_id,
				COUNT(*) AS daily_tool_calls,
				COUNT(*) FILTER (WHERE tool_key = 'web_search') AS daily_web_searches,
				COUNT(*) FILTER (WHERE tool_key = 'web_extract') AS daily_web_extracts
			FROM tool_usage_events e
			CROSS JOIN bounds b
			WHERE e.created_at >= b.start_at
			GROUP BY user_id
		),
		ocr_usage AS (
			SELECT
				user_id,
				COUNT(*) AS daily_ocr_files,
				COALESCE(SUM(ocr_page_count), 0) AS daily_ocr_pages
			FROM files f
			CROSS JOIN bounds b
			WHERE f.ocr_provider IN ('mineru', 'mineru_light') AND f.created_at >= b.start_at
			GROUP BY user_id
		)
		SELECT
			u.id,
			u.username,
			COALESCE(g.id, dg.id),
			COALESCE(g.name, dg.name, ''),
			COALESCE(g.daily_message_limit, dg.daily_message_limit, 0),
			COALESCE(g.daily_token_limit, dg.daily_token_limit, 0),
			COALESCE(g.concurrent_run_limit, dg.concurrent_run_limit, 0),
			COALESCE(g.daily_tool_call_limit, dg.daily_tool_call_limit, 0),
			COALESCE(g.daily_web_search_limit, dg.daily_web_search_limit, 0),
			COALESCE(g.daily_web_extract_limit, dg.daily_web_extract_limit, 0),
			COALESCE(g.daily_ocr_file_limit, dg.daily_ocr_file_limit, 0),
			COALESCE(g.daily_ocr_page_limit, dg.daily_ocr_page_limit, 0),
			COALESCE(mu.daily_messages, 0),
			COALESCE(mou.daily_model_tokens, 0),
			COALESCE(tu.daily_tool_calls, 0),
			COALESCE(tu.daily_web_searches, 0),
			COALESCE(tu.daily_web_extracts, 0),
			COALESCE(ou.daily_ocr_files, 0),
			COALESCE(ou.daily_ocr_pages, 0),
			(SELECT reset_at FROM bounds)
		FROM users u
		LEFT JOIN user_groups g ON g.id = u.group_id
		LEFT JOIN default_group dg ON true
		LEFT JOIN message_usage mu ON mu.user_id = u.id
		LEFT JOIN model_usage mou ON mou.user_id = u.id
		LEFT JOIN tool_usage tu ON tu.user_id = u.id
		LEFT JOIN ocr_usage ou ON ou.user_id = u.id
		ORDER BY
			GREATEST(
				CASE WHEN COALESCE(g.daily_message_limit, dg.daily_message_limit, 0) > 0
					THEN COALESCE(mu.daily_messages, 0)::DOUBLE PRECISION / COALESCE(g.daily_message_limit, dg.daily_message_limit, 0)
					ELSE -1 END,
				CASE WHEN COALESCE(g.daily_token_limit, dg.daily_token_limit, 0) > 0
					THEN COALESCE(mou.daily_model_tokens, 0)::DOUBLE PRECISION / COALESCE(g.daily_token_limit, dg.daily_token_limit, 0)
					ELSE -1 END,
				CASE WHEN COALESCE(g.daily_tool_call_limit, dg.daily_tool_call_limit, 0) > 0
					THEN COALESCE(tu.daily_tool_calls, 0)::DOUBLE PRECISION / COALESCE(g.daily_tool_call_limit, dg.daily_tool_call_limit, 0)
					ELSE -1 END,
				CASE WHEN COALESCE(g.daily_web_search_limit, dg.daily_web_search_limit, 0) > 0
					THEN COALESCE(tu.daily_web_searches, 0)::DOUBLE PRECISION / COALESCE(g.daily_web_search_limit, dg.daily_web_search_limit, 0)
					ELSE -1 END,
				CASE WHEN COALESCE(g.daily_web_extract_limit, dg.daily_web_extract_limit, 0) > 0
					THEN COALESCE(tu.daily_web_extracts, 0)::DOUBLE PRECISION / COALESCE(g.daily_web_extract_limit, dg.daily_web_extract_limit, 0)
					ELSE -1 END,
				CASE WHEN COALESCE(g.daily_ocr_file_limit, dg.daily_ocr_file_limit, 0) > 0
					THEN COALESCE(ou.daily_ocr_files, 0)::DOUBLE PRECISION / COALESCE(g.daily_ocr_file_limit, dg.daily_ocr_file_limit, 0)
					ELSE -1 END,
				CASE WHEN COALESCE(g.daily_ocr_page_limit, dg.daily_ocr_page_limit, 0) > 0
					THEN COALESCE(ou.daily_ocr_pages, 0)::DOUBLE PRECISION / COALESCE(g.daily_ocr_page_limit, dg.daily_ocr_page_limit, 0)
					ELSE -1 END
			) DESC,
			COALESCE(tu.daily_tool_calls, 0) + COALESCE(mou.daily_model_tokens, 0) + COALESCE(mu.daily_messages, 0) + COALESCE(ou.daily_ocr_files, 0) DESC,
			u.id ASC
		LIMIT 100
	`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("aggregate quota users: %w", err)
	}
	defer rows.Close()

	out := []QuotaUserUsage{}
	for rows.Next() {
		var item QuotaUserUsage
		var groupID sql.NullInt64
		if err := rows.Scan(
			&item.UserID,
			&item.Username,
			&groupID,
			&item.GroupName,
			&item.DailyMessageLimit,
			&item.DailyTokenLimit,
			&item.ConcurrentRunLimit,
			&item.DailyToolCallLimit,
			&item.DailyWebSearchLimit,
			&item.DailyWebExtractLimit,
			&item.DailyOCRFileLimit,
			&item.DailyOCRPageLimit,
			&item.DailyMessages,
			&item.DailyModelTokens,
			&item.DailyToolCalls,
			&item.DailyWebSearches,
			&item.DailyWebExtracts,
			&item.DailyOCRFiles,
			&item.DailyOCRPages,
			&item.ResetAt,
		); err != nil {
			return nil, fmt.Errorf("scan quota user usage: %w", err)
		}
		if groupID.Valid {
			id := groupID.Int64
			item.GroupID = &id
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate quota user usage: %w", err)
	}
	return out, nil
}

func (r *Repository) aggregateToolTotals(ctx context.Context, start, end time.Time) (ToolTotals, error) {
	query := `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE success),
			COUNT(*) FILTER (
				WHERE NOT success
				  AND NOT (
					tool_key = 'web_extract'
					AND (
						COALESCE(error_type, '') LIKE 'degraded_%'
						OR (
							COALESCE(error_type, '') IN ('refinement_cooldown', 'refinement_disabled', 'refinement_failed', 'refinement_unavailable', 'source_truncated')
							AND COALESCE(error_message, '') = 'tool returned a degraded result'
						)
					)
				  )
			),
			COUNT(*) FILTER (
				WHERE tool_key = 'web_extract'
				  AND (
					COALESCE(error_type, '') LIKE 'degraded_%'
					OR (
						COALESCE(error_type, '') IN ('refinement_cooldown', 'refinement_disabled', 'refinement_failed', 'refinement_unavailable', 'source_truncated')
						AND COALESCE(error_message, '') = 'tool returned a degraded result'
					)
				  )
			),
			COUNT(*) FILTER (WHERE tool_key = 'web_search'),
			COUNT(*) FILTER (WHERE tool_key = 'web_extract'),
			COALESCE(SUM(context_tokens), 0),
			COALESCE(SUM(CASE WHEN truncated THEN 1 ELSE 0 END), 0),
			COALESCE(ROUND(AVG(duration_ms)), 0)::BIGINT,
			MAX(created_at)
		FROM tool_usage_events
		WHERE created_at >= $1 AND created_at < $2
	`
	var totals ToolTotals
	var last sql.NullTime
	if err := r.db.QueryRowContext(ctx, query, start, end).Scan(
		&totals.Calls,
		&totals.Successes,
		&totals.Failures,
		&totals.Degraded,
		&totals.WebSearchCalls,
		&totals.WebExtractCalls,
		&totals.ContextTokens,
		&totals.Truncated,
		&totals.AvgDurationMs,
		&last,
	); err != nil {
		return totals, fmt.Errorf("aggregate tool usage totals: %w", err)
	}
	if last.Valid {
		totals.LastCalledAt = &last.Time
	}
	return totals, nil
}

func (r *Repository) aggregateByTool(ctx context.Context, start, end time.Time) ([]ByTool, error) {
	query := `
		SELECT
			tool_key,
			COUNT(*),
			COUNT(*) FILTER (WHERE success),
			COUNT(*) FILTER (
				WHERE NOT success
				  AND NOT (
					tool_key = 'web_extract'
					AND (
						COALESCE(error_type, '') LIKE 'degraded_%'
						OR (
							COALESCE(error_type, '') IN ('refinement_cooldown', 'refinement_disabled', 'refinement_failed', 'refinement_unavailable', 'source_truncated')
							AND COALESCE(error_message, '') = 'tool returned a degraded result'
						)
					)
				  )
			),
			COUNT(*) FILTER (
				WHERE tool_key = 'web_extract'
				  AND (
					COALESCE(error_type, '') LIKE 'degraded_%'
					OR (
						COALESCE(error_type, '') IN ('refinement_cooldown', 'refinement_disabled', 'refinement_failed', 'refinement_unavailable', 'source_truncated')
						AND COALESCE(error_message, '') = 'tool returned a degraded result'
					)
				  )
			),
			COALESCE(SUM(context_tokens), 0),
			COALESCE(SUM(CASE WHEN truncated THEN 1 ELSE 0 END), 0),
			COALESCE(ROUND(AVG(duration_ms)), 0)::BIGINT,
			MAX(created_at)
		FROM tool_usage_events
		WHERE created_at >= $1 AND created_at < $2
		GROUP BY tool_key
		ORDER BY COUNT(*) DESC, COALESCE(SUM(context_tokens), 0) DESC
		LIMIT 100
	`
	rows, err := r.db.QueryContext(ctx, query, start, end)
	if err != nil {
		return nil, fmt.Errorf("aggregate usage by tool: %w", err)
	}
	defer rows.Close()

	out := []ByTool{}
	for rows.Next() {
		var item ByTool
		var last sql.NullTime
		if err := rows.Scan(
			&item.ToolKey,
			&item.Calls,
			&item.Successes,
			&item.Failures,
			&item.Degraded,
			&item.ContextTokens,
			&item.Truncated,
			&item.AvgDurationMs,
			&last,
		); err != nil {
			return nil, fmt.Errorf("scan usage by tool: %w", err)
		}
		if last.Valid {
			item.LastCalledAt = &last.Time
		}
		if item.ToolKey == "web_search" {
			item.WebSearchCalls = item.Calls
		}
		if item.ToolKey == "web_extract" {
			item.WebExtractCalls = item.Calls
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate usage by tool: %w", err)
	}
	return out, nil
}

func (r *Repository) aggregateTotals(ctx context.Context, start, end time.Time) (Totals, error) {
	query := `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE success),
			COUNT(*) FILTER (WHERE NOT success AND COALESCE(error_type, '') <> 'canceled'),
			COUNT(*) FILTER (WHERE NOT success AND COALESCE(error_type, '') = 'canceled'),
			COALESCE(SUM(prompt_tokens), 0),
			COALESCE(SUM(completion_tokens), 0),
			COALESCE(SUM(total_tokens), 0),
			COALESCE(SUM(cached_tokens), 0),
			COALESCE(SUM(reasoning_tokens), 0),
			COALESCE(ROUND(AVG(duration_ms)), 0)::BIGINT,
			MAX(created_at)
		FROM model_usage_events
		WHERE created_at >= $1 AND created_at < $2
	`
	var totals Totals
	var last sql.NullTime
	if err := r.db.QueryRowContext(ctx, query, start, end).Scan(
		&totals.Requests,
		&totals.Successes,
		&totals.Failures,
		&totals.Canceled,
		&totals.PromptTokens,
		&totals.CompletionTokens,
		&totals.TotalTokens,
		&totals.CachedTokens,
		&totals.ReasoningTokens,
		&totals.AvgDurationMs,
		&last,
	); err != nil {
		return totals, fmt.Errorf("aggregate usage totals: %w", err)
	}
	if last.Valid {
		totals.LastCalledAt = &last.Time
	}
	ocr, err := r.aggregateOCRTotals(ctx, start, end)
	if err != nil {
		return totals, err
	}
	totals.OCRFiles = ocr.OCRFiles
	totals.OCRPages = ocr.OCRPages
	totals.OCRFailures = ocr.OCRFailures
	return totals, nil
}

func (r *Repository) aggregateRunTotals(ctx context.Context, start, end time.Time) (RunTotals, error) {
	query := `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE status = 'running'),
			COUNT(*) FILTER (WHERE status = 'completed'),
			COUNT(*) FILTER (WHERE status = 'failed'),
			COUNT(*) FILTER (WHERE status = 'canceled'),
			COUNT(*) FILTER (WHERE status = 'canceled' AND cancel_cause = 'user_stop'),
			COUNT(*) FILTER (WHERE status = 'canceled' AND cancel_cause <> 'user_stop'),
			COALESCE(ROUND(AVG(EXTRACT(EPOCH FROM (terminal_at - accepted_at)) * 1000)
				FILTER (WHERE terminal_at IS NOT NULL)), 0)::BIGINT,
			MAX(accepted_at)
		FROM chat_run_reservations
		WHERE kind = 'chat' AND accepted_at >= $1 AND accepted_at < $2
	`
	var totals RunTotals
	var last sql.NullTime
	if err := r.db.QueryRowContext(ctx, query, start, end).Scan(
		&totals.Runs,
		&totals.Running,
		&totals.Completed,
		&totals.Failed,
		&totals.Canceled,
		&totals.UserStopped,
		&totals.SystemCanceled,
		&totals.AvgDurationMs,
		&last,
	); err != nil {
		return totals, fmt.Errorf("aggregate chat run totals: %w", err)
	}
	if last.Valid {
		totals.LastAcceptedAt = &last.Time
	}
	return totals, nil
}

func (r *Repository) aggregateOCRTotals(ctx context.Context, start, end time.Time) (Totals, error) {
	query := `
		SELECT
			COUNT(*),
			COALESCE(SUM(ocr_page_count), 0),
			COUNT(*) FILTER (WHERE extract_status = 'failed')
		FROM files
		WHERE ocr_provider IN ('mineru', 'mineru_light') AND created_at >= $1 AND created_at < $2
	`
	var totals Totals
	if err := r.db.QueryRowContext(ctx, query, start, end).Scan(&totals.OCRFiles, &totals.OCRPages, &totals.OCRFailures); err != nil {
		return totals, fmt.Errorf("aggregate OCR totals: %w", err)
	}
	return totals, nil
}

func (r *Repository) aggregateByUser(ctx context.Context, start, end time.Time) ([]ByUser, error) {
	query := `
		SELECT
			e.user_id,
			COALESCE(u.username, CASE WHEN e.user_id IS NULL THEN '未知用户' ELSE '用户 #' || e.user_id::TEXT END),
			COUNT(*),
			COUNT(*) FILTER (WHERE e.success),
			COUNT(*) FILTER (WHERE NOT e.success AND COALESCE(e.error_type, '') <> 'canceled'),
			COUNT(*) FILTER (WHERE NOT e.success AND COALESCE(e.error_type, '') = 'canceled'),
			COALESCE(SUM(e.prompt_tokens), 0),
			COALESCE(SUM(e.completion_tokens), 0),
			COALESCE(SUM(e.total_tokens), 0),
			COALESCE(SUM(e.cached_tokens), 0),
			COALESCE(SUM(e.reasoning_tokens), 0),
			COALESCE(ROUND(AVG(e.duration_ms)), 0)::BIGINT,
			MAX(e.created_at)
		FROM model_usage_events e
		LEFT JOIN users u ON u.id = e.user_id
		WHERE e.created_at >= $1 AND e.created_at < $2
		GROUP BY e.user_id, u.username
		ORDER BY COALESCE(SUM(e.total_tokens), 0) DESC, COUNT(*) DESC
		LIMIT 50
	`
	rows, err := r.db.QueryContext(ctx, query, start, end)
	if err != nil {
		return nil, fmt.Errorf("aggregate usage by user: %w", err)
	}
	defer rows.Close()

	out := []ByUser{}
	for rows.Next() {
		var item ByUser
		var userID sql.NullInt64
		var last sql.NullTime
		if err := rows.Scan(
			&userID,
			&item.Username,
			&item.Requests,
			&item.Successes,
			&item.Failures,
			&item.Canceled,
			&item.PromptTokens,
			&item.CompletionTokens,
			&item.TotalTokens,
			&item.CachedTokens,
			&item.ReasoningTokens,
			&item.AvgDurationMs,
			&last,
		); err != nil {
			return nil, fmt.Errorf("scan usage by user: %w", err)
		}
		if userID.Valid {
			item.UserID = userID.Int64
		}
		if last.Valid {
			item.LastCalledAt = &last.Time
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate usage by user: %w", err)
	}
	return out, nil
}

func (r *Repository) aggregateByModel(ctx context.Context, start, end time.Time) ([]ByModel, error) {
	query := `
		SELECT
			provider,
			model_id,
			COUNT(*),
			COUNT(*) FILTER (WHERE success),
			COUNT(*) FILTER (WHERE NOT success AND COALESCE(error_type, '') <> 'canceled'),
			COUNT(*) FILTER (WHERE NOT success AND COALESCE(error_type, '') = 'canceled'),
			COALESCE(SUM(prompt_tokens), 0),
			COALESCE(SUM(completion_tokens), 0),
			COALESCE(SUM(total_tokens), 0),
			COALESCE(SUM(cached_tokens), 0),
			COALESCE(SUM(reasoning_tokens), 0),
			COALESCE(ROUND(AVG(duration_ms)), 0)::BIGINT,
			MAX(created_at)
		FROM model_usage_events
		WHERE created_at >= $1 AND created_at < $2
		GROUP BY provider, model_id
		ORDER BY COALESCE(SUM(total_tokens), 0) DESC, COUNT(*) DESC
		LIMIT 100
	`
	rows, err := r.db.QueryContext(ctx, query, start, end)
	if err != nil {
		return nil, fmt.Errorf("aggregate usage by model: %w", err)
	}
	defer rows.Close()

	out := []ByModel{}
	for rows.Next() {
		var item ByModel
		var last sql.NullTime
		if err := rows.Scan(
			&item.Provider,
			&item.ModelID,
			&item.Requests,
			&item.Successes,
			&item.Failures,
			&item.Canceled,
			&item.PromptTokens,
			&item.CompletionTokens,
			&item.TotalTokens,
			&item.CachedTokens,
			&item.ReasoningTokens,
			&item.AvgDurationMs,
			&last,
		); err != nil {
			return nil, fmt.Errorf("scan usage by model: %w", err)
		}
		if last.Valid {
			item.LastCalledAt = &last.Time
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate usage by model: %w", err)
	}
	return out, nil
}

func (r *Repository) aggregateByKind(ctx context.Context, start, end time.Time) ([]ByKind, error) {
	query := `
		SELECT
			kind,
			COUNT(*),
			COUNT(*) FILTER (WHERE success),
			COUNT(*) FILTER (WHERE NOT success AND COALESCE(error_type, '') <> 'canceled'),
			COUNT(*) FILTER (WHERE NOT success AND COALESCE(error_type, '') = 'canceled'),
			COALESCE(SUM(prompt_tokens), 0),
			COALESCE(SUM(completion_tokens), 0),
			COALESCE(SUM(total_tokens), 0),
			COALESCE(SUM(cached_tokens), 0),
			COALESCE(SUM(reasoning_tokens), 0),
			COALESCE(ROUND(AVG(duration_ms)), 0)::BIGINT,
			MAX(created_at)
		FROM model_usage_events
		WHERE created_at >= $1 AND created_at < $2
		GROUP BY kind
		ORDER BY COUNT(*) DESC
	`
	rows, err := r.db.QueryContext(ctx, query, start, end)
	if err != nil {
		return nil, fmt.Errorf("aggregate usage by kind: %w", err)
	}
	defer rows.Close()

	out := []ByKind{}
	for rows.Next() {
		var item ByKind
		var last sql.NullTime
		if err := rows.Scan(
			&item.Kind,
			&item.Requests,
			&item.Successes,
			&item.Failures,
			&item.Canceled,
			&item.PromptTokens,
			&item.CompletionTokens,
			&item.TotalTokens,
			&item.CachedTokens,
			&item.ReasoningTokens,
			&item.AvgDurationMs,
			&last,
		); err != nil {
			return nil, fmt.Errorf("scan usage by kind: %w", err)
		}
		if last.Valid {
			item.LastCalledAt = &last.Time
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate usage by kind: %w", err)
	}
	return out, nil
}

func nullInt64(value int64) interface{} {
	if value <= 0 {
		return nil
	}
	return value
}

func nullString(value string) interface{} {
	if value == "" {
		return nil
	}
	return value
}
