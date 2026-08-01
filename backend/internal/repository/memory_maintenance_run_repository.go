package repository

import (
	"context"
	"fmt"
)

// MemoryMaintenanceRunCommitInput is the single durable commit boundary for a
// user-triggered memory task. Transitioning the RunHub record first inside the
// transaction makes retries idempotent: an ambiguous successful commit is
// observed as an already-terminal run and never replays the memory mutation.
type MemoryMaintenanceRunCommitInput struct {
	Run     ChatRunTransitionInput
	Memory  *SaveSessionMemoryInput
	TaskRun RecordModelTaskRunInput
}

func (r *SessionMemoryRepository) CommitMaintenanceRun(ctx context.Context, input MemoryMaintenanceRunCommitInput) (ChatRunRecord, bool, error) {
	if r == nil || r.db == nil {
		return ChatRunRecord{}, false, fmt.Errorf("session memory repository is unavailable")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return ChatRunRecord{}, false, fmt.Errorf("begin memory maintenance run commit: %w", err)
	}
	defer tx.Rollback()

	record, transitioned, err := transitionChatRun(ctx, tx, input.Run)
	if err != nil || !transitioned {
		return record, transitioned, err
	}
	if input.Memory != nil {
		memoryInput := *input.Memory
		memoryInput.RunID = input.Run.RunID
		if _, err := saveSessionMemoryWithChange(ctx, tx, memoryInput); err != nil {
			return ChatRunRecord{}, false, err
		}
	}
	if err := insertModelTaskRun(ctx, tx, input.TaskRun); err != nil {
		return ChatRunRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return ChatRunRecord{}, false, fmt.Errorf("commit memory maintenance run: %w", err)
	}
	return record, true, nil
}

func insertModelTaskRun(ctx context.Context, exec dbExecutor, input RecordModelTaskRunInput) error {
	input = normalizeModelTaskRunInput(input)
	if input.TaskKey == "" {
		return fmt.Errorf("task_key is required")
	}
	_, err := exec.ExecContext(ctx, `
		INSERT INTO model_task_runs (
			task_key, user_id, session_id, run_id, source, status,
			provider, model_id, target_type, target_id, error_type, error_message,
			retry_after, metadata, started_at, finished_at, duration_ms
		)
		VALUES ($1, NULLIF($2, 0), NULLIF($3, 0), $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14::jsonb, $15, $16, $17)
	`, input.TaskKey, input.UserID, input.SessionID, input.RunID, input.Source, input.Status,
		input.Provider, input.ModelID, input.TargetType, input.TargetID, input.ErrorType, input.ErrorMessage,
		input.RetryAfter, string(input.Metadata), input.StartedAt, input.FinishedAt, input.FinishedAt.Sub(input.StartedAt).Milliseconds())
	if err != nil {
		return fmt.Errorf("record model task run: %w", err)
	}
	return nil
}
