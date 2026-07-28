package repository

import (
	"testing"
	"time"

	"github.com/huoguojun123/effchat/internal/model"
)

func TestModelTaskRunRepositoryLatestAndCooldown(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	userID := createRepositoryTestUser(t, db, "task_run")
	session := &model.Session{UserID: userID, Title: "task run session", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := NewSessionRepository(db).Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", userID)
	})

	repo := NewModelTaskRunRepository(db)
	now := time.Now()
	retryAfter := now.Add(30 * time.Minute)
	if _, err := repo.Record(t.Context(), RecordModelTaskRunInput{
		TaskKey:      ModelTaskMemoryMaintenance,
		UserID:       userID,
		SessionID:    session.ID,
		Source:       ModelTaskSourceAuto,
		Status:       ModelTaskStatusFailed,
		ErrorType:    "timeout",
		ErrorMessage: "mock timeout",
		RetryAfter:   &retryAfter,
		StartedAt:    now,
		FinishedAt:   now.Add(time.Second),
	}); err != nil {
		t.Fatalf("record failed run: %v", err)
	}

	latest, err := repo.LatestForSession(t.Context(), session.ID, userID, ModelTaskMemoryMaintenance)
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if latest == nil || latest.Status != ModelTaskStatusFailed {
		t.Fatalf("latest = %+v, want failed run", latest)
	}
	cooling, err := repo.LatestAutoCooldown(t.Context(), session.ID, userID, ModelTaskMemoryMaintenance, now)
	if err != nil {
		t.Fatalf("cooldown: %v", err)
	}
	if cooling == nil || cooling.ID != latest.ID {
		t.Fatalf("cooling = %+v, want latest failed run", cooling)
	}
	if _, err := repo.Record(t.Context(), RecordModelTaskRunInput{
		TaskKey:    ModelTaskMemoryMaintenance,
		UserID:     userID,
		SessionID:  session.ID,
		Source:     ModelTaskSourceManual,
		Status:     ModelTaskStatusSuccess,
		StartedAt:  now.Add(2 * time.Second),
		FinishedAt: now.Add(3 * time.Second),
	}); err != nil {
		t.Fatalf("record success run: %v", err)
	}
	runs, err := repo.ListForSession(t.Context(), session.ID, userID, ModelTaskMemoryMaintenance, 5)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 2 || runs[0].Status != ModelTaskStatusSuccess || runs[1].Status != ModelTaskStatusFailed {
		t.Fatalf("runs = %+v, want newest success then failed", runs)
	}
	cooling, err = repo.LatestAutoCooldown(t.Context(), session.ID, userID, ModelTaskMemoryMaintenance, now)
	if err != nil {
		t.Fatalf("cooldown: %v", err)
	}
	if cooling != nil {
		t.Fatalf("cooling = %+v, want nil after newer success", cooling)
	}
}

func TestModelTaskRunRepositoryLatestCooldownMatchesSource(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	userID := createRepositoryTestUser(t, db, "tool_task_cooldown")
	session := &model.Session{UserID: userID, Title: "tool cooldown", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := NewSessionRepository(db).Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", userID)
	})

	repo := NewModelTaskRunRepository(db)
	now := time.Now()
	retryAfter := now.Add(30 * time.Minute)
	if _, err := repo.Record(t.Context(), RecordModelTaskRunInput{
		TaskKey:    ModelTaskToolExtractSummary,
		UserID:     userID,
		SessionID:  session.ID,
		Source:     ModelTaskSourceTool,
		Status:     ModelTaskStatusFailed,
		RetryAfter: &retryAfter,
		StartedAt:  now.Add(-time.Second),
		FinishedAt: now,
	}); err != nil {
		t.Fatalf("record tool failure: %v", err)
	}

	cooling, err := repo.LatestCooldown(t.Context(), session.ID, userID, ModelTaskToolExtractSummary, ModelTaskSourceTool, now)
	if err != nil {
		t.Fatalf("latest cooldown: %v", err)
	}
	if cooling == nil || cooling.Source != ModelTaskSourceTool {
		t.Fatalf("cooling = %+v, want tool cooldown", cooling)
	}
	if auto, err := repo.LatestCooldown(t.Context(), session.ID, userID, ModelTaskToolExtractSummary, ModelTaskSourceAuto, now); err != nil || auto != nil {
		t.Fatalf("auto cooldown = %+v, err = %v, want nil", auto, err)
	}
}

func TestModelTaskRunRepositoryEffectiveAttemptIgnoresLaterFailures(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	userID := createRepositoryTestUser(t, db, "task_run_watermark")
	session := &model.Session{UserID: userID, Title: "task run watermark", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := NewSessionRepository(db).Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", userID)
	})

	repo := NewModelTaskRunRepository(db)
	started := time.Now().Add(-time.Hour)
	if _, err := repo.Record(t.Context(), RecordModelTaskRunInput{
		TaskKey:    ModelTaskMemoryMaintenance,
		UserID:     userID,
		SessionID:  session.ID,
		Source:     ModelTaskSourceAuto,
		Status:     ModelTaskStatusSuccess,
		StartedAt:  started,
		FinishedAt: started.Add(time.Second),
	}); err != nil {
		t.Fatalf("record successful watermark: %v", err)
	}
	for i := 0; i < 25; i++ {
		at := started.Add(time.Duration(i+1) * time.Minute)
		if _, err := repo.Record(t.Context(), RecordModelTaskRunInput{
			TaskKey:    ModelTaskMemoryMaintenance,
			UserID:     userID,
			SessionID:  session.ID,
			Source:     ModelTaskSourceAuto,
			Status:     ModelTaskStatusFailed,
			StartedAt:  at,
			FinishedAt: at.Add(time.Second),
		}); err != nil {
			t.Fatalf("record failed attempt %d: %v", i, err)
		}
	}

	effective, err := repo.LatestEffectiveAttemptForSession(t.Context(), session.ID, userID, ModelTaskMemoryMaintenance)
	if err != nil {
		t.Fatalf("latest effective attempt: %v", err)
	}
	if effective == nil || effective.Status != ModelTaskStatusSuccess {
		t.Fatalf("effective attempt = %+v, want earlier success beyond list limit", effective)
	}

	skippedAt := time.Now()
	if _, err := repo.Record(t.Context(), RecordModelTaskRunInput{
		TaskKey:    ModelTaskMemoryMaintenance,
		UserID:     userID,
		SessionID:  session.ID,
		Source:     ModelTaskSourceAuto,
		Status:     ModelTaskStatusSkipped,
		StartedAt:  skippedAt,
		FinishedAt: skippedAt.Add(time.Second),
	}); err != nil {
		t.Fatalf("record skipped watermark: %v", err)
	}
	effective, err = repo.LatestEffectiveAttemptForSession(t.Context(), session.ID, userID, ModelTaskMemoryMaintenance)
	if err != nil {
		t.Fatalf("latest skipped attempt: %v", err)
	}
	if effective == nil || effective.Status != ModelTaskStatusSkipped {
		t.Fatalf("effective attempt = %+v, want latest skipped", effective)
	}
}
