package repository

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/huoguojun123/EffChat/internal/model"
)

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
