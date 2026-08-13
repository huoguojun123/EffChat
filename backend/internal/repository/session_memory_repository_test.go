package repository

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	sessionmemory "github.com/huoguojun123/EffChat/internal/memory"
	"github.com/huoguojun123/EffChat/internal/model"
)

func TestMemoryMaintenanceRunCommitIsAtomicAndIdempotent(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	userID := createRepositoryTestUser(t, db, "memory_run_atomic")
	session := &model.Session{UserID: userID, Title: "memory run atomic", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", MemoryEnabled: true, Metadata: []byte(`{}`)}
	if err := NewSessionRepository(db).Create(session); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM users WHERE id = $1", userID) })
	repo := NewSessionMemoryRepository(db)
	if err := repo.Set(session.ID, "## Current Progress\n- Current: before."); err != nil {
		t.Fatal(err)
	}
	runID := "memory-run-atomic"
	quotaRepo := NewQuotaRepository(db)
	if _, err := quotaRepo.ReserveChatRun(t.Context(), ChatRunReservationInput{
		UserID: userID, AuthVersion: 1, SessionID: session.ID, RunID: runID,
		Kind: "memory_maintenance", Operation: "memory_compact", IntentVersion: 1, IntentHash: "v1:memory-run-atomic",
		ExpiresAt: time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	transition := ChatRunTransitionInput{
		RunID: runID, Status: "completed", ExpiresAt: time.Now().Add(time.Minute),
		TerminalEvent: json.RawMessage(`{"event":"memory_maintenance_complete","data":{"updated":true}}`),
	}
	memory := SaveSessionMemoryInput{
		SessionID: session.ID, UserID: userID, Content: "## Current Progress\n- Current: after.",
		Source: "compact", Action: "compact", Summary: "organized", ExpectedBefore: "## Current Progress\n- Current: before.", CheckBefore: true, MaxChars: 4000,
	}
	invalidTask := RecordModelTaskRunInput{TaskKey: "invalid", UserID: userID, SessionID: session.ID, RunID: runID, Source: ModelTaskSourceManual, Status: ModelTaskStatusSuccess}
	if _, _, err := repo.CommitMaintenanceRun(t.Context(), MemoryMaintenanceRunCommitInput{Run: transition, Memory: &memory, TaskRun: invalidTask}); err == nil {
		t.Fatal("invalid task record committed memory mutation")
	}
	if content, _ := repo.Get(session.ID); !strings.Contains(content, "before") || strings.Contains(content, "after") {
		t.Fatalf("memory changed despite rollback: %q", content)
	}
	if run, err := quotaRepo.GetChatRun(t.Context(), runID); err != nil || run.Status != "running" {
		t.Fatalf("run changed despite rollback: %+v err=%v", run, err)
	}

	task := RecordModelTaskRunInput{
		TaskKey: ModelTaskMemoryMaintenance, UserID: userID, SessionID: session.ID, RunID: runID,
		Source: ModelTaskSourceManual, Status: ModelTaskStatusSuccess, TargetType: "memory",
		StartedAt: time.Now(), FinishedAt: time.Now(),
	}
	record, transitioned, err := repo.CommitMaintenanceRun(t.Context(), MemoryMaintenanceRunCommitInput{Run: transition, Memory: &memory, TaskRun: task})
	if err != nil || !transitioned || record.Status != "completed" {
		t.Fatalf("commit = transitioned:%v record:%+v err:%v", transitioned, record, err)
	}
	if content, _ := repo.Get(session.ID); !strings.Contains(content, "after") {
		t.Fatalf("memory was not committed: %q", content)
	}
	var changes, tasks int
	if err := db.QueryRow("SELECT COUNT(*) FROM session_memory_changes WHERE run_id = $1", runID).Scan(&changes); err != nil || changes != 1 {
		t.Fatalf("memory changes=%d err=%v", changes, err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM model_task_runs WHERE run_id = $1 AND task_key = 'memory_maintenance'", runID).Scan(&tasks); err != nil || tasks != 1 {
		t.Fatalf("task runs=%d err=%v", tasks, err)
	}

	recovered, transitioned, err := repo.CommitMaintenanceRun(t.Context(), MemoryMaintenanceRunCommitInput{Run: transition, Memory: &memory, TaskRun: task})
	if err != nil || transitioned || recovered.Status != "completed" {
		t.Fatalf("recovery = transitioned:%v record:%+v err:%v", transitioned, recovered, err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM session_memory_changes WHERE run_id = $1", runID).Scan(&changes); err != nil || changes != 1 {
		t.Fatalf("recovery duplicated memory changes=%d err=%v", changes, err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM model_task_runs WHERE run_id = $1 AND task_key = 'memory_maintenance'", runID).Scan(&tasks); err != nil || tasks != 1 {
		t.Fatalf("recovery duplicated task runs=%d err=%v", tasks, err)
	}
}

func TestSessionMemoryRepositoryCompareAndSetSerializesFirstWrite(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	userID := createRepositoryTestUser(t, db, "memory_first_write")
	session := &model.Session{UserID: userID, Title: "memory first write", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := NewSessionRepository(db).Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", userID)
	})

	repo := NewSessionMemoryRepository(db)
	start := make(chan struct{})
	results := make(chan bool, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, content := range []string{
		"## Decisions\n- Keep option A.",
		"## Decisions\n- Keep option B.",
	} {
		content := content
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			saved, err := repo.CompareAndSetWithChange(t.Context(), session.ID, userID, "", content, "tool", "update", "first write", 4000)
			results <- saved
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)

	savedCount := 0
	for saved := range results {
		if saved {
			savedCount++
		}
	}
	for err := range errs {
		if err != nil {
			t.Fatalf("compare and set: %v", err)
		}
	}
	if savedCount != 1 {
		t.Fatalf("saved writes=%d, want exactly 1", savedCount)
	}
	content, err := repo.Get(session.ID)
	if err != nil {
		t.Fatalf("get memory: %v", err)
	}
	if !strings.Contains(content, "option A") && !strings.Contains(content, "option B") {
		t.Fatalf("unexpected final memory: %q", content)
	}
}

func TestSessionMemoryRepositorySavesEnabledAndContentAtomically(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	userID := createRepositoryTestUser(t, db, "memory_atomic_setting")
	session := &model.Session{UserID: userID, Title: "memory atomic setting", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", MemoryEnabled: true, Metadata: []byte(`{}`)}
	if err := NewSessionRepository(db).Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM users WHERE id = $1", userID) })

	repo := NewSessionMemoryRepository(db)
	if err := repo.Set(session.ID, "## Current Progress\n- Current: original state."); err != nil {
		t.Fatalf("seed memory: %v", err)
	}
	disabled := false
	_, err := repo.SaveWithChange(t.Context(), SaveSessionMemoryInput{
		SessionID:     session.ID,
		UserID:        userID,
		MemoryEnabled: &disabled,
		Content:       "## Current Progress\n- Current: changed state.",
		Source:        "invalid-source",
		Action:        "update",
		Summary:       "must roll back",
		MaxChars:      4000,
	})
	if err == nil {
		t.Fatal("expected invalid change source to fail")
	}
	storedSession, err := NewSessionRepository(db).GetByID(session.ID, userID)
	if err != nil {
		t.Fatalf("get session after rollback: %v", err)
	}
	if !storedSession.MemoryEnabled {
		t.Fatal("memory_enabled changed despite failed memory transaction")
	}
	content, err := repo.Get(session.ID)
	if err != nil {
		t.Fatalf("get memory after rollback: %v", err)
	}
	if !strings.Contains(content, "original state") || strings.Contains(content, "changed state") {
		t.Fatalf("memory content changed despite rollback: %q", content)
	}

	if _, err := repo.SaveWithChange(t.Context(), SaveSessionMemoryInput{
		SessionID:     session.ID,
		UserID:        userID,
		MemoryEnabled: &disabled,
		Content:       content,
		Source:        "manual",
		Action:        "update",
		Summary:       "toggle only",
		MaxChars:      4000,
	}); err != nil {
		t.Fatalf("toggle memory setting with unchanged content: %v", err)
	}
	storedSession, err = NewSessionRepository(db).GetByID(session.ID, userID)
	if err != nil {
		t.Fatalf("get session after toggle: %v", err)
	}
	if storedSession.MemoryEnabled {
		t.Fatal("memory_enabled toggle was not committed")
	}
}

func TestSessionMemoryRepositoryRejectsSecretBeforePersistence(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	userID := createRepositoryTestUser(t, db, "memory_secret_guard")
	session := &model.Session{UserID: userID, Title: "memory secret guard", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", MemoryEnabled: true, Metadata: []byte(`{}`)}
	if err := NewSessionRepository(db).Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM users WHERE id = $1", userID) })

	secret := "fixture-password-42"
	_, err := NewSessionMemoryRepository(db).SaveWithChange(t.Context(), SaveSessionMemoryInput{
		SessionID: session.ID,
		UserID:    userID,
		Content:   "## Decisions\n- password=" + secret,
		Source:    "manual",
		Action:    "update",
		Summary:   "must reject credential",
		MaxChars:  4000,
	})
	if !errors.Is(err, sessionmemory.ErrSensitiveValue) {
		t.Fatalf("SaveWithChange error = %v, want ErrSensitiveValue", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("repository error leaked rejected secret: %v", err)
	}

	var memoryCount, changeCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM session_memories WHERE session_id = $1 AND content <> ''`, session.ID).Scan(&memoryCount); err != nil {
		t.Fatalf("count memory rows: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM session_memory_changes WHERE session_id = $1`, session.ID).Scan(&changeCount); err != nil {
		t.Fatalf("count change rows: %v", err)
	}
	if memoryCount != 0 || changeCount != 0 {
		t.Fatalf("secret write left durable state: memory=%d changes=%d", memoryCount, changeCount)
	}
}

func TestSessionMemoryRepositoryRejectsStaleAnswerSelectionRevision(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	userID := createRepositoryTestUser(t, db, "memory_answer_revision")
	session := &model.Session{UserID: userID, Title: "memory selection revision", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", MemoryEnabled: true, Metadata: []byte(`{}`)}
	if err := NewSessionRepository(db).Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM users WHERE id = $1", userID) })

	repo := NewSessionMemoryRepository(db)
	before := "## Current Progress\n- Current: original answer remains selected."
	if err := repo.Set(session.ID, before); err != nil {
		t.Fatalf("seed memory: %v", err)
	}
	if _, err := db.Exec("UPDATE sessions SET answer_selection_revision = 2 WHERE id = $1", session.ID); err != nil {
		t.Fatalf("advance answer selection revision: %v", err)
	}

	expectedRevision := int64(1)
	_, err := repo.SaveWithChange(t.Context(), SaveSessionMemoryInput{
		SessionID:                       session.ID,
		UserID:                          userID,
		Content:                         "## Current Progress\n- Current: stale answer changed the memory.",
		Source:                          "auto",
		Action:                          "update",
		Summary:                         "stale answer update",
		ExpectedAnswerSelectionRevision: &expectedRevision,
		MaxChars:                        4000,
	})
	if !errors.Is(err, ErrAnswerSelectionRevisionConflict) {
		t.Fatalf("stale selection error = %v, want ErrAnswerSelectionRevisionConflict", err)
	}

	stored, err := repo.Get(session.ID)
	if err != nil {
		t.Fatalf("get memory after stale write: %v", err)
	}
	if strings.TrimSpace(stored) != strings.TrimSpace(before) {
		t.Fatalf("stale answer overwrote memory: %q", stored)
	}
	var changes int
	if err := db.QueryRow("SELECT count(*) FROM session_memory_changes WHERE session_id = $1", session.ID).Scan(&changes); err != nil {
		t.Fatalf("count memory changes: %v", err)
	}
	if changes != 0 {
		t.Fatalf("stale answer created %d memory changes, want 0", changes)
	}
}

func TestSessionMemoryRepositoryUndoCompactChange(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	userID := createRepositoryTestUser(t, db, "memory_undo")
	session := &model.Session{UserID: userID, Title: "memory undo", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := NewSessionRepository(db).Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", userID)
	})

	repo := NewSessionMemoryRepository(db)
	before := "## User Preferences\n- Prefer concise answers.\n\n## Current Progress\n- Current: planning the launch."
	after := "## User Preferences\n- Prefer concise answers.\n\n## Current Progress\n- Current: launch plan is compacted."
	if _, err := repo.SaveWithChange(t.Context(), SaveSessionMemoryInput{
		SessionID: session.ID,
		UserID:    userID,
		Content:   before,
		Source:    "manual",
		Action:    "update",
		Summary:   "seed memory",
		MaxChars:  4000,
	}); err != nil {
		t.Fatalf("seed memory: %v", err)
	}
	compactChange, err := repo.SaveWithChange(t.Context(), SaveSessionMemoryInput{
		SessionID: session.ID,
		UserID:    userID,
		Content:   after,
		Source:    "compact",
		Action:    "compact",
		Summary:   "compacted memory",
		MaxChars:  4000,
	})
	if err != nil {
		t.Fatalf("compact memory: %v", err)
	}
	if compactChange == nil {
		t.Fatal("expected compact change")
	}

	if _, err := repo.UndoChange(t.Context(), session.ID, userID, compactChange.ID); err != nil {
		t.Fatalf("undo compact: %v", err)
	}
	got, err := repo.Get(session.ID)
	if err != nil {
		t.Fatalf("get memory: %v", err)
	}
	if !strings.Contains(got, "planning the launch") || strings.Contains(got, "compacted") {
		t.Fatalf("memory was not restored:\n%s", got)
	}
}

func TestSessionMemoryRepositoryUndoDoesNotRestoreLegacySecret(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	userID := createRepositoryTestUser(t, db, "memory_undo_secret")
	session := &model.Session{UserID: userID, Title: "memory undo secret", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := NewSessionRepository(db).Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM users WHERE id = $1", userID) })

	repo := NewSessionMemoryRepository(db)
	if _, err := repo.SaveWithChange(t.Context(), SaveSessionMemoryInput{
		SessionID: session.ID,
		UserID:    userID,
		Content:   "## Decisions\n- 使用虚构项目编号 EC-2026-041。",
		Source:    "manual",
		Action:    "update",
		Summary:   "seed memory",
		MaxChars:  4000,
	}); err != nil {
		t.Fatalf("seed memory: %v", err)
	}
	compactChange, err := repo.SaveWithChange(t.Context(), SaveSessionMemoryInput{
		SessionID: session.ID,
		UserID:    userID,
		Content:   "## Decisions\n- 使用虚构项目编号 EC-2026-041，并保持简洁。",
		Source:    "compact",
		Action:    "compact",
		Summary:   "compact memory",
		MaxChars:  4000,
	})
	if err != nil || compactChange == nil {
		t.Fatalf("compact memory: change=%+v err=%v", compactChange, err)
	}

	secret := "fixture-password-42"
	legacyBefore := "## Decisions\n- password=" + secret + "\n- 使用虚构项目编号 EC-2026-041。"
	if _, err := db.Exec(`UPDATE session_memory_changes SET before_content = $1 WHERE id = $2`, legacyBefore, compactChange.ID); err != nil {
		t.Fatalf("seed legacy change content: %v", err)
	}
	if _, err := repo.UndoChange(t.Context(), session.ID, userID, compactChange.ID); err != nil {
		t.Fatalf("undo compact: %v", err)
	}
	stored, err := repo.Get(session.ID)
	if err != nil {
		t.Fatalf("get restored memory: %v", err)
	}
	if strings.Contains(stored, secret) || !strings.Contains(stored, sessionmemory.SensitiveValuePlaceholder) || !strings.Contains(stored, "EC-2026-041") {
		t.Fatalf("undo restored unsafe or damaged memory: %q", stored)
	}
}

func TestSessionMemoryRepositoryUndoOnlyLatestCompactChange(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	userID := createRepositoryTestUser(t, db, "memory_undo_latest")
	session := &model.Session{UserID: userID, Title: "memory undo latest", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := NewSessionRepository(db).Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", userID)
	})

	repo := NewSessionMemoryRepository(db)
	if _, err := repo.SaveWithChange(t.Context(), SaveSessionMemoryInput{
		SessionID: session.ID,
		UserID:    userID,
		Content:   "## Current Progress\n- Current: draft plan.",
		Source:    "manual",
		Action:    "update",
		Summary:   "seed memory",
		MaxChars:  4000,
	}); err != nil {
		t.Fatalf("seed memory: %v", err)
	}
	compactChange, err := repo.SaveWithChange(t.Context(), SaveSessionMemoryInput{
		SessionID: session.ID,
		UserID:    userID,
		Content:   "## Current Progress\n- Current: compacted plan.",
		Source:    "compact",
		Action:    "compact",
		Summary:   "compacted memory",
		MaxChars:  4000,
	})
	if err != nil {
		t.Fatalf("compact memory: %v", err)
	}
	if _, err := repo.SaveWithChange(t.Context(), SaveSessionMemoryInput{
		SessionID: session.ID,
		UserID:    userID,
		Content:   "## Current Progress\n- Current: compacted plan.\n\n## Decisions\n- Keep the daily review.",
		Source:    "tool",
		Action:    "update",
		Summary:   "tool memory",
		MaxChars:  4000,
	}); err != nil {
		t.Fatalf("tool memory: %v", err)
	}
	if _, err := repo.UndoChange(t.Context(), session.ID, userID, compactChange.ID); !errors.Is(err, ErrMemoryChangeNotUndoable) {
		t.Fatalf("undo stale compact error = %v, want ErrMemoryChangeNotUndoable", err)
	}
}

func TestSessionMemoryRepositoryUndoRejectsToolChange(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	userID := createRepositoryTestUser(t, db, "memory_undo_tool")
	session := &model.Session{UserID: userID, Title: "memory undo tool", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := NewSessionRepository(db).Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", userID)
	})

	change, err := NewSessionMemoryRepository(db).SaveWithChange(t.Context(), SaveSessionMemoryInput{
		SessionID: session.ID,
		UserID:    userID,
		Content:   "## Decisions\n- Use one compact checklist.",
		Source:    "tool",
		Action:    "update",
		Summary:   "tool memory",
		MaxChars:  4000,
	})
	if err != nil {
		t.Fatalf("tool memory: %v", err)
	}
	if _, err := NewSessionMemoryRepository(db).UndoChange(t.Context(), session.ID, userID, change.ID); !errors.Is(err, ErrMemoryChangeNotUndoable) {
		t.Fatalf("undo tool error = %v, want ErrMemoryChangeNotUndoable", err)
	}
}

func TestSessionMemoryRepositoryRejectsWritesAfterSessionDeletion(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	userID := createRepositoryTestUser(t, db, "memory_deleted")
	session := &model.Session{UserID: userID, Title: "memory deleted", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := NewSessionRepository(db).Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := NewSessionRepository(db).Delete(session.ID, userID); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, err := NewSessionMemoryRepository(db).SaveWithChange(t.Context(), SaveSessionMemoryInput{
		SessionID: session.ID,
		UserID:    userID,
		Content:   "## Current Progress\n- Current: late write.",
		Source:    "auto",
		Action:    "update",
		Summary:   "late write",
		MaxChars:  4000,
	}); err == nil {
		t.Fatal("late memory write was accepted")
	}
}
