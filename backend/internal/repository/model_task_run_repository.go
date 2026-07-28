package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	ModelTaskTitleGeneration    = "title_generation"
	ModelTaskCompression        = "compression"
	ModelTaskToolExtractSummary = "tool_extract_summary"
	ModelTaskMemoryMaintenance  = "memory_maintenance"
	ModelTaskSourceAuto         = "auto"
	ModelTaskSourceManual       = "manual"
	ModelTaskSourceTool         = "tool"
	ModelTaskSourceSystem       = "system"

	ModelTaskStatusSuccess = "success"
	ModelTaskStatusFailed  = "failed"
	ModelTaskStatusSkipped = "skipped"
)

const maxModelTaskErrorRunes = 500

type ModelTaskRunRepository struct {
	db *sql.DB
}

type ModelTaskRun struct {
	ID           int64           `json:"id"`
	TaskKey      string          `json:"task_key"`
	UserID       *int64          `json:"user_id,omitempty"`
	SessionID    *int64          `json:"session_id,omitempty"`
	RunID        string          `json:"run_id"`
	Source       string          `json:"source"`
	Status       string          `json:"status"`
	Provider     string          `json:"provider"`
	ModelID      string          `json:"model_id"`
	TargetType   string          `json:"target_type"`
	TargetID     string          `json:"target_id"`
	ErrorType    string          `json:"error_type,omitempty"`
	ErrorMessage string          `json:"error_message,omitempty"`
	RetryAfter   *time.Time      `json:"retry_after,omitempty"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
	StartedAt    time.Time       `json:"started_at"`
	FinishedAt   time.Time       `json:"finished_at"`
	DurationMs   int64           `json:"duration_ms"`
}

type RecordModelTaskRunInput struct {
	TaskKey      string
	UserID       int64
	SessionID    int64
	RunID        string
	Source       string
	Status       string
	Provider     string
	ModelID      string
	TargetType   string
	TargetID     string
	ErrorType    string
	ErrorMessage string
	RetryAfter   *time.Time
	Metadata     json.RawMessage
	StartedAt    time.Time
	FinishedAt   time.Time
}

func NewModelTaskRunRepository(db *sql.DB) *ModelTaskRunRepository {
	return &ModelTaskRunRepository{db: db}
}

func (r *ModelTaskRunRepository) DeleteOlderThan(ctx context.Context, cutoff time.Time) error {
	if r == nil || r.db == nil {
		return nil
	}
	if _, err := r.db.ExecContext(ctx, `DELETE FROM model_task_runs WHERE finished_at < $1`, cutoff); err != nil {
		return fmt.Errorf("delete expired model task runs: %w", err)
	}
	return nil
}

func (r *ModelTaskRunRepository) Record(ctx context.Context, input RecordModelTaskRunInput) (*ModelTaskRun, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("model task run repository is unavailable")
	}
	input = normalizeModelTaskRunInput(input)
	if input.TaskKey == "" {
		return nil, fmt.Errorf("task_key is required")
	}
	if input.Source == "" {
		return nil, fmt.Errorf("source is required")
	}
	if input.Status == "" {
		return nil, fmt.Errorf("status is required")
	}
	run := &ModelTaskRun{}
	var userID, sessionID sql.NullInt64
	var retryAfter sql.NullTime
	var metadata []byte
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO model_task_runs (
			task_key, user_id, session_id, run_id, source, status,
			provider, model_id, target_type, target_id, error_type, error_message,
			retry_after, metadata, started_at, finished_at, duration_ms
		)
		VALUES ($1, NULLIF($2, 0), NULLIF($3, 0), $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14::jsonb, $15, $16, $17)
		RETURNING id, task_key, user_id, session_id, run_id, source, status,
			provider, model_id, target_type, target_id, error_type, error_message,
			retry_after, metadata, started_at, finished_at, duration_ms
	`, input.TaskKey, input.UserID, input.SessionID, input.RunID, input.Source, input.Status,
		input.Provider, input.ModelID, input.TargetType, input.TargetID, input.ErrorType, input.ErrorMessage,
		input.RetryAfter, string(input.Metadata), input.StartedAt, input.FinishedAt, input.FinishedAt.Sub(input.StartedAt).Milliseconds(),
	).Scan(
		&run.ID, &run.TaskKey, &userID, &sessionID, &run.RunID, &run.Source, &run.Status,
		&run.Provider, &run.ModelID, &run.TargetType, &run.TargetID, &run.ErrorType, &run.ErrorMessage,
		&retryAfter, &metadata, &run.StartedAt, &run.FinishedAt, &run.DurationMs,
	)
	if err != nil {
		return nil, fmt.Errorf("record model task run: %w", err)
	}
	assignModelTaskNullable(run, userID, sessionID, retryAfter, metadata)
	return run, nil
}

func (r *ModelTaskRunRepository) LatestForSession(ctx context.Context, sessionID, userID int64, taskKey string) (*ModelTaskRun, error) {
	if r == nil || r.db == nil || sessionID <= 0 || userID <= 0 {
		return nil, nil
	}
	runs, err := r.ListForSession(ctx, sessionID, userID, taskKey, 1)
	if err != nil || len(runs) == 0 {
		return nil, err
	}
	return &runs[0], nil
}

func (r *ModelTaskRunRepository) LatestEffectiveAttemptForSession(ctx context.Context, sessionID, userID int64, taskKey string) (*ModelTaskRun, error) {
	if r == nil || r.db == nil || sessionID <= 0 || userID <= 0 {
		return nil, nil
	}
	var run ModelTaskRun
	var rowUserID, rowSessionID sql.NullInt64
	var retryAfter sql.NullTime
	var metadata []byte
	err := r.db.QueryRowContext(ctx, `
		SELECT id, task_key, user_id, session_id, run_id, source, status,
		       provider, model_id, target_type, target_id, error_type, error_message,
		       retry_after, metadata, started_at, finished_at, duration_ms
		FROM model_task_runs
		WHERE session_id = $1 AND user_id = $2 AND task_key = $3
		  AND status IN ($4, $5)
		ORDER BY finished_at DESC, id DESC
		LIMIT 1
	`, sessionID, userID, strings.TrimSpace(taskKey), ModelTaskStatusSuccess, ModelTaskStatusSkipped).Scan(
		&run.ID, &run.TaskKey, &rowUserID, &rowSessionID, &run.RunID, &run.Source, &run.Status,
		&run.Provider, &run.ModelID, &run.TargetType, &run.TargetID, &run.ErrorType, &run.ErrorMessage,
		&retryAfter, &metadata, &run.StartedAt, &run.FinishedAt, &run.DurationMs,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("latest effective model task run: %w", err)
	}
	assignModelTaskNullable(&run, rowUserID, rowSessionID, retryAfter, metadata)
	return &run, nil
}

func (r *ModelTaskRunRepository) ListForSession(ctx context.Context, sessionID, userID int64, taskKey string, limit int) ([]ModelTaskRun, error) {
	if r == nil || r.db == nil || sessionID <= 0 || userID <= 0 {
		return nil, nil
	}
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, task_key, user_id, session_id, run_id, source, status,
		       provider, model_id, target_type, target_id, error_type, error_message,
		       retry_after, metadata, started_at, finished_at, duration_ms
		FROM model_task_runs
		WHERE session_id = $1 AND user_id = $2 AND task_key = $3
		ORDER BY started_at DESC, id DESC
		LIMIT $4
	`, sessionID, userID, strings.TrimSpace(taskKey), limit)
	if err != nil {
		return nil, fmt.Errorf("list model task runs: %w", err)
	}
	defer rows.Close()
	runs := make([]ModelTaskRun, 0, limit)
	for rows.Next() {
		var run ModelTaskRun
		var rowUserID, rowSessionID sql.NullInt64
		var retryAfter sql.NullTime
		var metadata []byte
		if err := rows.Scan(
			&run.ID, &run.TaskKey, &rowUserID, &rowSessionID, &run.RunID, &run.Source, &run.Status,
			&run.Provider, &run.ModelID, &run.TargetType, &run.TargetID, &run.ErrorType, &run.ErrorMessage,
			&retryAfter, &metadata, &run.StartedAt, &run.FinishedAt, &run.DurationMs,
		); err != nil {
			return nil, err
		}
		assignModelTaskNullable(&run, rowUserID, rowSessionID, retryAfter, metadata)
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (r *ModelTaskRunRepository) LatestAutoCooldown(ctx context.Context, sessionID, userID int64, taskKey string, now time.Time) (*ModelTaskRun, error) {
	return r.LatestCooldown(ctx, sessionID, userID, taskKey, ModelTaskSourceAuto, now)
}

func (r *ModelTaskRunRepository) LatestCooldown(ctx context.Context, sessionID, userID int64, taskKey, source string, now time.Time) (*ModelTaskRun, error) {
	if r == nil || r.db == nil || sessionID <= 0 || userID <= 0 {
		return nil, nil
	}
	run, err := r.LatestForSession(ctx, sessionID, userID, taskKey)
	if err != nil || run == nil {
		return run, err
	}
	if run.Source == normalizeModelTaskSource(source) && run.Status == ModelTaskStatusFailed && run.RetryAfter != nil && run.RetryAfter.After(now) {
		return run, nil
	}
	return nil, nil
}

func normalizeModelTaskRunInput(input RecordModelTaskRunInput) RecordModelTaskRunInput {
	input.TaskKey = normalizeModelTaskKey(input.TaskKey)
	input.Source = normalizeModelTaskSource(input.Source)
	input.Status = normalizeModelTaskStatus(input.Status)
	input.RunID = strings.TrimSpace(input.RunID)
	input.Provider = strings.TrimSpace(input.Provider)
	input.ModelID = strings.TrimSpace(input.ModelID)
	input.TargetType = strings.TrimSpace(input.TargetType)
	input.TargetID = strings.TrimSpace(input.TargetID)
	input.ErrorType = strings.TrimSpace(input.ErrorType)
	input.ErrorMessage = truncateRunesLocal(strings.TrimSpace(input.ErrorMessage), maxModelTaskErrorRunes)
	if len(input.Metadata) == 0 || !json.Valid(input.Metadata) {
		input.Metadata = json.RawMessage(`{}`)
	}
	if input.StartedAt.IsZero() {
		input.StartedAt = time.Now()
	}
	if input.FinishedAt.IsZero() || input.FinishedAt.Before(input.StartedAt) {
		input.FinishedAt = input.StartedAt
	}
	return input
}

func normalizeModelTaskKey(value string) string {
	switch strings.TrimSpace(value) {
	case ModelTaskTitleGeneration, ModelTaskCompression, ModelTaskToolExtractSummary, ModelTaskMemoryMaintenance:
		return strings.TrimSpace(value)
	default:
		return ""
	}
}

func normalizeModelTaskSource(value string) string {
	switch strings.TrimSpace(value) {
	case ModelTaskSourceAuto, ModelTaskSourceManual, ModelTaskSourceTool, ModelTaskSourceSystem:
		return strings.TrimSpace(value)
	default:
		return ModelTaskSourceSystem
	}
}

func normalizeModelTaskStatus(value string) string {
	switch strings.TrimSpace(value) {
	case ModelTaskStatusSuccess, ModelTaskStatusFailed, ModelTaskStatusSkipped:
		return strings.TrimSpace(value)
	default:
		return ModelTaskStatusFailed
	}
}

func assignModelTaskNullable(run *ModelTaskRun, userID, sessionID sql.NullInt64, retryAfter sql.NullTime, metadata []byte) {
	if userID.Valid {
		v := userID.Int64
		run.UserID = &v
	}
	if sessionID.Valid {
		v := sessionID.Int64
		run.SessionID = &v
	}
	if retryAfter.Valid {
		v := retryAfter.Time
		run.RetryAfter = &v
	}
	if len(metadata) == 0 {
		run.Metadata = json.RawMessage(`{}`)
	} else {
		run.Metadata = append(json.RawMessage(nil), metadata...)
	}
}

func truncateRunesLocal(value string, max int) string {
	if max <= 0 || utf8.RuneCountInString(value) <= max {
		return value
	}
	runes := []rune(value)
	return string(runes[:max])
}
