package handler

import (
	"time"

	"github.com/huoguojun123/EffChat/internal/repository"
)

type modelTaskRunResponse struct {
	ID           int64      `json:"id"`
	TaskKey      string     `json:"task_key"`
	Source       string     `json:"source"`
	Status       string     `json:"status"`
	Provider     string     `json:"provider,omitempty"`
	ModelID      string     `json:"model_id,omitempty"`
	TargetType   string     `json:"target_type,omitempty"`
	TargetID     string     `json:"target_id,omitempty"`
	ErrorType    string     `json:"error_type,omitempty"`
	ErrorMessage string     `json:"error_message,omitempty"`
	RetryAfter   *time.Time `json:"retry_after,omitempty"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   time.Time  `json:"finished_at"`
	DurationMs   int64      `json:"duration_ms"`
}

func toModelTaskRunResponse(run *repository.ModelTaskRun) *modelTaskRunResponse {
	if run == nil {
		return nil
	}
	return &modelTaskRunResponse{
		ID:           run.ID,
		TaskKey:      run.TaskKey,
		Source:       run.Source,
		Status:       run.Status,
		Provider:     run.Provider,
		ModelID:      run.ModelID,
		TargetType:   run.TargetType,
		TargetID:     run.TargetID,
		ErrorType:    run.ErrorType,
		ErrorMessage: run.ErrorMessage,
		RetryAfter:   run.RetryAfter,
		StartedAt:    run.StartedAt,
		FinishedAt:   run.FinishedAt,
		DurationMs:   run.DurationMs,
	}
}
