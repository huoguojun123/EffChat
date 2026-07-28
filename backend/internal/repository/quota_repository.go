package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var (
	ErrAccountStateChanged   = errors.New("account state changed")
	ErrChatRunIntentConflict = errors.New("chat run intent conflict")
)

type UserQuotaLimits struct {
	DailyMessageLimit    int
	DailyTokenLimit      int
	ConcurrentRunLimit   int
	DailyToolCallLimit   int
	DailyWebSearchLimit  int
	DailyWebExtractLimit int
	DailyOCRFileLimit    int
	DailyOCRPageLimit    int
}

type QuotaUsage struct {
	DailyMessages    int64
	DailyTokens      int64
	DailyToolCalls   int64
	DailyWebSearches int64
	DailyWebExtracts int64
	DailyOCRFiles    int64
	DailyOCRPages    int64
	ResetAt          time.Time
}

type ToolCallReservationInput struct {
	UserID    int64
	SessionID int64
	RunID     string
	CallID    string
	ToolName  string
}

type ToolCallReservation struct {
	ID        int64
	CreatedAt time.Time
}

type ChatRunReservationInput struct {
	UserID               int64
	AuthVersion          int
	SessionID            int64
	RunID                string
	Kind                 string
	Operation            string
	IntentVersion        int
	IntentHash           string
	RetryTargetMessageID int64
	RuntimeSnapshot      json.RawMessage
	ReserveMessage       bool
	AcceptedAt           time.Time
	ExpiresAt            time.Time
}

type ChatRunReservation struct {
	RunID    string
	Existing bool
	Record   ChatRunRecord
}

type ToolQuotaExceeded struct {
	Code    string
	Limit   int64
	Used    int64
	ResetAt time.Time
}

func (e *ToolQuotaExceeded) Error() string {
	if e == nil {
		return ""
	}
	return e.Code
}

type QuotaRepository struct {
	db *sql.DB
}

func NewQuotaRepository(db *sql.DB) *QuotaRepository {
	return &QuotaRepository{db: db}
}

func (r *QuotaRepository) LimitsForUser(ctx context.Context, userID int64) (UserQuotaLimits, error) {
	query := `
		SELECT
			COALESCE(g.daily_message_limit, dg.daily_message_limit, 0),
			COALESCE(g.daily_token_limit, dg.daily_token_limit, 0),
			COALESCE(g.concurrent_run_limit, dg.concurrent_run_limit, 0),
			COALESCE(g.daily_tool_call_limit, dg.daily_tool_call_limit, 0),
			COALESCE(g.daily_web_search_limit, dg.daily_web_search_limit, 0),
			COALESCE(g.daily_web_extract_limit, dg.daily_web_extract_limit, 0),
			COALESCE(g.daily_ocr_file_limit, dg.daily_ocr_file_limit, 0),
			COALESCE(g.daily_ocr_page_limit, dg.daily_ocr_page_limit, 0)
		FROM users u
		LEFT JOIN user_groups g ON g.id = u.group_id
		LEFT JOIN LATERAL (
			SELECT
				daily_message_limit,
				daily_token_limit,
				concurrent_run_limit,
				daily_tool_call_limit,
				daily_web_search_limit,
				daily_web_extract_limit,
				daily_ocr_file_limit,
				daily_ocr_page_limit
			FROM user_groups
			WHERE is_default = true
			ORDER BY level ASC, id ASC
			LIMIT 1
		) dg ON true
		WHERE u.id = $1
	`
	var limits UserQuotaLimits
	if err := r.db.QueryRowContext(ctx, query, userID).Scan(
		&limits.DailyMessageLimit,
		&limits.DailyTokenLimit,
		&limits.ConcurrentRunLimit,
		&limits.DailyToolCallLimit,
		&limits.DailyWebSearchLimit,
		&limits.DailyWebExtractLimit,
		&limits.DailyOCRFileLimit,
		&limits.DailyOCRPageLimit,
	); err != nil {
		if err == sql.ErrNoRows {
			return limits, ErrNotFound
		}
		return limits, fmt.Errorf("load user quota limits: %w", err)
	}
	return limits, nil
}

func (r *QuotaRepository) UsageForToday(ctx context.Context, userID int64) (QuotaUsage, error) {
	query := `
		WITH bounds AS (
			SELECT date_trunc('day', NOW()) AS start_at, date_trunc('day', NOW()) + INTERVAL '1 day' AS reset_at
		)
		SELECT
			(SELECT COUNT(*)
			 FROM messages m
			 JOIN sessions s ON s.id = m.session_id
			 CROSS JOIN bounds b
			 WHERE s.user_id = $1 AND m.role = 'user' AND m.created_at >= b.start_at),
			(SELECT COALESCE(SUM(total_tokens), 0)
			 FROM model_usage_events e
			 CROSS JOIN bounds b
			 WHERE e.user_id = $1 AND e.created_at >= b.start_at),
			(SELECT COUNT(*)
			 FROM tool_usage_events e
			 CROSS JOIN bounds b
			 WHERE e.user_id = $1 AND e.created_at >= b.start_at),
			(SELECT COUNT(*)
			 FROM tool_usage_events e
			 CROSS JOIN bounds b
			 WHERE e.user_id = $1 AND e.tool_key = 'web_search' AND e.created_at >= b.start_at),
			(SELECT COUNT(*)
			 FROM tool_usage_events e
			 CROSS JOIN bounds b
			 WHERE e.user_id = $1 AND e.tool_key = 'web_extract' AND e.created_at >= b.start_at),
			(SELECT COUNT(*)
			 FROM files f
			 CROSS JOIN bounds b
			 WHERE f.user_id = $1 AND f.ocr_provider IN ('mineru', 'mineru_light') AND f.ocr_started_at >= b.start_at),
			(SELECT COALESCE(SUM(ocr_page_count), 0)
			 FROM files f
			 CROSS JOIN bounds b
			 WHERE f.user_id = $1 AND f.ocr_provider IN ('mineru', 'mineru_light') AND f.ocr_started_at >= b.start_at),
			(SELECT reset_at FROM bounds)
	`
	var usage QuotaUsage
	if err := r.db.QueryRowContext(ctx, query, userID).Scan(
		&usage.DailyMessages,
		&usage.DailyTokens,
		&usage.DailyToolCalls,
		&usage.DailyWebSearches,
		&usage.DailyWebExtracts,
		&usage.DailyOCRFiles,
		&usage.DailyOCRPages,
		&usage.ResetAt,
	); err != nil {
		return usage, fmt.Errorf("load user quota usage: %w", err)
	}
	return usage, nil
}

func (r *QuotaRepository) ReserveToolCall(ctx context.Context, input ToolCallReservationInput) (ToolCallReservation, error) {
	if input.UserID <= 0 {
		return ToolCallReservation{}, nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return ToolCallReservation{}, fmt.Errorf("begin tool quota reservation: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var userID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id = $1 FOR UPDATE`, input.UserID).Scan(&userID); err != nil {
		if err == sql.ErrNoRows {
			return ToolCallReservation{}, ErrNotFound
		}
		return ToolCallReservation{}, fmt.Errorf("lock user for tool quota: %w", err)
	}

	limits, err := limitsForUserInTx(ctx, tx, input.UserID)
	if err != nil {
		return ToolCallReservation{}, err
	}
	usage, err := usageForTodayInTx(ctx, tx, input.UserID)
	if err != nil {
		return ToolCallReservation{}, err
	}
	if err := checkToolQuota(limits, usage, input.ToolName); err != nil {
		return ToolCallReservation{}, err
	}

	var reservation ToolCallReservation
	err = tx.QueryRowContext(
		ctx,
		`INSERT INTO tool_usage_events (
			user_id, session_id, run_id, call_id, tool_key, success,
			context_tokens, truncated, duration_ms
		)
		VALUES ($1, $2, $3, $4, $5, true, 0, false, 0)
		RETURNING id, created_at`,
		nullInt64(input.UserID),
		nullInt64(input.SessionID),
		nullString(input.RunID),
		nullString(input.CallID),
		input.ToolName,
	).Scan(&reservation.ID, &reservation.CreatedAt)
	if err != nil {
		return ToolCallReservation{}, fmt.Errorf("reserve tool usage event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ToolCallReservation{}, fmt.Errorf("commit tool quota reservation: %w", err)
	}
	return reservation, nil
}

func (r *QuotaRepository) ReserveChatRun(ctx context.Context, input ChatRunReservationInput) (ChatRunReservation, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return ChatRunReservation{}, fmt.Errorf("begin chat run reservation: %w", err)
	}
	defer tx.Rollback()
	reservation, err := reserveChatRunInTx(ctx, tx, input)
	if err != nil {
		return ChatRunReservation{}, err
	}
	if err := tx.Commit(); err != nil {
		return ChatRunReservation{}, fmt.Errorf("commit chat run reservation: %w", err)
	}
	return reservation, nil
}

func lockChatRunUser(ctx context.Context, tx *sql.Tx, userID int64, authVersion int) error {
	var currentVersion int
	var active bool
	if err := tx.QueryRowContext(ctx, `SELECT auth_version, is_active FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&currentVersion, &active); err != nil {
		if err == sql.ErrNoRows {
			return ErrNotFound
		}
		return fmt.Errorf("lock user for chat run: %w", err)
	}
	if !active || currentVersion != authVersion {
		return ErrAccountStateChanged
	}
	return nil
}

func (r *QuotaRepository) ReserveOCRSubmission(ctx context.Context, fileID, userID int64, pageCount int) (bool, error) {
	if pageCount < 0 {
		pageCount = 0
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin OCR quota reservation: %w", err)
	}
	defer tx.Rollback()
	if err := lockQuotaUser(ctx, tx, userID); err != nil {
		return false, err
	}
	var alreadyStarted bool
	if err := tx.QueryRowContext(ctx, `
		SELECT ocr_started_at IS NOT NULL
		FROM files
			WHERE id = $1 AND user_id = $2 AND status = 'staged'
		FOR UPDATE
	`, fileID, userID).Scan(&alreadyStarted); err != nil {
		if err == sql.ErrNoRows {
			return false, ErrNotFound
		}
		return false, fmt.Errorf("lock OCR file reservation: %w", err)
	}
	if alreadyStarted {
		result, err := tx.ExecContext(ctx, `
			UPDATE files
			SET ocr_error_type = 'ocr_submission_started',
			    ocr_page_count = GREATEST(ocr_page_count, $3)
			WHERE id = $1
			  AND user_id = $2
			  AND status = 'staged'
			  AND extract_status IN ('ocr_pending', 'ocr_running')
			  AND NULLIF(TRIM(ocr_task_id), '') IS NULL
			  AND ocr_error_type IS NULL
		`, fileID, userID, pageCount)
		if err != nil {
			return false, fmt.Errorf("resume OCR submission: %w", err)
		}
		affected, _ := result.RowsAffected()
		if affected == 0 {
			return false, nil
		}
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("commit resumed OCR submission: %w", err)
		}
		return true, nil
	}
	limits, err := limitsForUserInTx(ctx, tx, userID)
	if err != nil {
		return false, err
	}
	usage, err := usageForTodayInTx(ctx, tx, userID)
	if err != nil {
		return false, err
	}
	if err := checkOCRQuota(limits, usage, pageCount); err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE files
		SET ocr_error_type = 'ocr_submission_started',
		    ocr_page_count = GREATEST(ocr_page_count, $3),
		    ocr_started_at = NOW()
		WHERE id = $1
		  AND user_id = $2
		  AND status = 'staged'
		  AND extract_status IN ('ocr_pending', 'ocr_running')
		  AND NULLIF(TRIM(ocr_task_id), '') IS NULL
		  AND ocr_error_type IS NULL
		  AND ocr_started_at IS NULL
	`, fileID, userID, pageCount)
	if err != nil {
		return false, fmt.Errorf("reserve OCR submission: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return false, nil
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit OCR quota reservation: %w", err)
	}
	return true, nil
}

func lockQuotaUser(ctx context.Context, tx *sql.Tx, userID int64) error {
	var lockedUserID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&lockedUserID); err != nil {
		if err == sql.ErrNoRows {
			return ErrNotFound
		}
		return fmt.Errorf("lock user for quota: %w", err)
	}
	return nil
}

func quotaExceeded(code string, limit, used int64, resetAt time.Time) error {
	return &ToolQuotaExceeded{Code: code, Limit: limit, Used: used, ResetAt: resetAt}
}

func checkOCRQuota(limits UserQuotaLimits, usage QuotaUsage, pageCount int) error {
	if limits.DailyOCRFileLimit > 0 && usage.DailyOCRFiles >= int64(limits.DailyOCRFileLimit) {
		return quotaExceeded("daily_ocr_file_limit_exceeded", int64(limits.DailyOCRFileLimit), usage.DailyOCRFiles, usage.ResetAt)
	}
	if limits.DailyOCRPageLimit > 0 && usage.DailyOCRPages+int64(pageCount) > int64(limits.DailyOCRPageLimit) {
		return quotaExceeded("daily_ocr_page_limit_exceeded", int64(limits.DailyOCRPageLimit), usage.DailyOCRPages, usage.ResetAt)
	}
	return nil
}

func limitsForUserInTx(ctx context.Context, tx *sql.Tx, userID int64) (UserQuotaLimits, error) {
	query := `
		SELECT
			COALESCE(g.daily_message_limit, dg.daily_message_limit, 0),
			COALESCE(g.daily_token_limit, dg.daily_token_limit, 0),
			COALESCE(g.concurrent_run_limit, dg.concurrent_run_limit, 0),
			COALESCE(g.daily_tool_call_limit, dg.daily_tool_call_limit, 0),
			COALESCE(g.daily_web_search_limit, dg.daily_web_search_limit, 0),
			COALESCE(g.daily_web_extract_limit, dg.daily_web_extract_limit, 0),
			COALESCE(g.daily_ocr_file_limit, dg.daily_ocr_file_limit, 0),
			COALESCE(g.daily_ocr_page_limit, dg.daily_ocr_page_limit, 0)
		FROM users u
		LEFT JOIN user_groups g ON g.id = u.group_id
		LEFT JOIN LATERAL (
			SELECT
				daily_message_limit,
				daily_token_limit,
				concurrent_run_limit,
				daily_tool_call_limit,
				daily_web_search_limit,
				daily_web_extract_limit,
				daily_ocr_file_limit,
				daily_ocr_page_limit
			FROM user_groups
			WHERE is_default = true
			ORDER BY level ASC, id ASC
			LIMIT 1
		) dg ON true
		WHERE u.id = $1
	`
	var limits UserQuotaLimits
	if err := tx.QueryRowContext(ctx, query, userID).Scan(
		&limits.DailyMessageLimit,
		&limits.DailyTokenLimit,
		&limits.ConcurrentRunLimit,
		&limits.DailyToolCallLimit,
		&limits.DailyWebSearchLimit,
		&limits.DailyWebExtractLimit,
		&limits.DailyOCRFileLimit,
		&limits.DailyOCRPageLimit,
	); err != nil {
		if err == sql.ErrNoRows {
			return limits, ErrNotFound
		}
		return limits, fmt.Errorf("load user quota limits in reservation: %w", err)
	}
	return limits, nil
}

func usageForTodayInTx(ctx context.Context, tx *sql.Tx, userID int64) (QuotaUsage, error) {
	query := `
		WITH bounds AS (
			SELECT date_trunc('day', NOW()) AS start_at, date_trunc('day', NOW()) + INTERVAL '1 day' AS reset_at
		)
		SELECT
			(SELECT COUNT(*)
			 FROM messages m
			 JOIN sessions s ON s.id = m.session_id
			 CROSS JOIN bounds b
			 WHERE s.user_id = $1 AND m.role = 'user' AND m.created_at >= b.start_at),
			(SELECT COALESCE(SUM(total_tokens), 0)
			 FROM model_usage_events e
			 CROSS JOIN bounds b
			 WHERE e.user_id = $1 AND e.created_at >= b.start_at),
			(SELECT COUNT(*)
			 FROM tool_usage_events e
			 CROSS JOIN bounds b
			 WHERE e.user_id = $1 AND e.created_at >= b.start_at),
			(SELECT COUNT(*)
			 FROM tool_usage_events e
			 CROSS JOIN bounds b
			 WHERE e.user_id = $1 AND e.tool_key = 'web_search' AND e.created_at >= b.start_at),
			(SELECT COUNT(*)
			 FROM tool_usage_events e
			 CROSS JOIN bounds b
			 WHERE e.user_id = $1 AND e.tool_key = 'web_extract' AND e.created_at >= b.start_at),
			(SELECT COUNT(*)
			 FROM files f
			 CROSS JOIN bounds b
			 WHERE f.user_id = $1 AND f.ocr_provider IN ('mineru', 'mineru_light') AND f.ocr_started_at >= b.start_at),
			(SELECT COALESCE(SUM(ocr_page_count), 0)
			 FROM files f
			 CROSS JOIN bounds b
			 WHERE f.user_id = $1 AND f.ocr_provider IN ('mineru', 'mineru_light') AND f.ocr_started_at >= b.start_at),
			(SELECT reset_at FROM bounds)
	`
	var usage QuotaUsage
	if err := tx.QueryRowContext(ctx, query, userID).Scan(
		&usage.DailyMessages,
		&usage.DailyTokens,
		&usage.DailyToolCalls,
		&usage.DailyWebSearches,
		&usage.DailyWebExtracts,
		&usage.DailyOCRFiles,
		&usage.DailyOCRPages,
		&usage.ResetAt,
	); err != nil {
		return usage, fmt.Errorf("load user quota usage in reservation: %w", err)
	}
	return usage, nil
}

func checkToolQuota(limits UserQuotaLimits, usage QuotaUsage, toolName string) error {
	if limits.DailyToolCallLimit > 0 && usage.DailyToolCalls >= int64(limits.DailyToolCallLimit) {
		return &ToolQuotaExceeded{
			Code:    "daily_tool_call_limit_exceeded",
			Limit:   int64(limits.DailyToolCallLimit),
			Used:    usage.DailyToolCalls,
			ResetAt: usage.ResetAt,
		}
	}
	if toolName == "web_search" && limits.DailyWebSearchLimit > 0 && usage.DailyWebSearches >= int64(limits.DailyWebSearchLimit) {
		return &ToolQuotaExceeded{
			Code:    "daily_web_search_limit_exceeded",
			Limit:   int64(limits.DailyWebSearchLimit),
			Used:    usage.DailyWebSearches,
			ResetAt: usage.ResetAt,
		}
	}
	if toolName == "web_extract" && limits.DailyWebExtractLimit > 0 && usage.DailyWebExtracts >= int64(limits.DailyWebExtractLimit) {
		return &ToolQuotaExceeded{
			Code:    "daily_web_extract_limit_exceeded",
			Limit:   int64(limits.DailyWebExtractLimit),
			Used:    usage.DailyWebExtracts,
			ResetAt: usage.ResetAt,
		}
	}
	return nil
}

func nullInt64(value int64) sql.NullInt64 {
	if value <= 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: value, Valid: true}
}

func nullString(value string) sql.NullString {
	if value == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}
