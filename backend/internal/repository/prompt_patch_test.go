package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/testutil"
)

func TestPromptPatchPreservesConcurrentFields(t *testing.T) {
	db := testutil.OpenPostgresTestDB(t)
	defer db.Close()
	user := &model.User{Username: fmt.Sprintf("prompt_patch_%d", time.Now().UnixNano()), PasswordHash: "fixture-hash", Role: "user", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`)}
	if err := NewUserRepository(db).Create(user); err != nil {
		t.Fatalf("create fixture user: %v", err)
	}
	prompt := &model.Prompt{UserID: user.ID, Title: "Before", Content: "Before content", Tags: []string{}}
	if err := NewPromptRepository(db).CreateContext(context.Background(), prompt); err != nil {
		t.Fatalf("create fixture prompt: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM prompts WHERE id = $1", prompt.ID)
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", user.ID)
	})

	blocker, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin blocker: %v", err)
	}
	if _, err := blocker.ExecContext(context.Background(), `SELECT id FROM prompts WHERE id = $1 FOR UPDATE`, prompt.ID); err != nil {
		_ = blocker.Rollback()
		t.Fatalf("lock prompt row: %v", err)
	}

	title := "After title"
	titleDone := make(chan error, 1)
	go func() {
		_, err := NewPromptRepository(db).PatchContext(context.Background(), prompt.ID, user.ID, PromptPatch{Title: &title})
		titleDone <- err
	}()
	waitForPromptPatchWaiters(t, db, 1)

	content := "After content"
	contentDone := make(chan error, 1)
	go func() {
		_, err := NewPromptRepository(db).PatchContext(context.Background(), prompt.ID, user.ID, PromptPatch{Content: &content})
		contentDone <- err
	}()
	waitForPromptPatchWaiters(t, db, 2)

	if err := blocker.Commit(); err != nil {
		t.Fatalf("release blocker: %v", err)
	}
	if err := <-titleDone; err != nil {
		t.Fatalf("title patch: %v", err)
	}
	if err := <-contentDone; err != nil {
		t.Fatalf("content patch: %v", err)
	}

	updated, err := NewPromptRepository(db).GetByID(prompt.ID, user.ID)
	if err != nil {
		t.Fatalf("reload prompt: %v", err)
	}
	if updated.Title != title || updated.Content != content {
		t.Fatalf("concurrent Prompt patches lost state: title=%q content=%q", updated.Title, updated.Content)
	}
	if _, err := NewPromptRepository(db).PatchContext(context.Background(), prompt.ID, user.ID, PromptPatch{
		DescriptionSet: true, Description: nil, TagsSet: true, Tags: []string{}, GroupIDSet: true, GroupID: nil,
	}); err != nil {
		t.Fatalf("clear nullable Prompt fields: %v", err)
	}
	updated, err = NewPromptRepository(db).GetByID(prompt.ID, user.ID)
	if err != nil {
		t.Fatalf("reload cleared Prompt: %v", err)
	}
	if updated.Description != nil || updated.GroupID != nil || len(updated.Tags) != 0 {
		t.Fatalf("Prompt field presence lost: description=%v group_id=%v tags=%v", updated.Description, updated.GroupID, updated.Tags)
	}
}

func TestPromptPatchHonorsContextCancellation(t *testing.T) {
	db := testutil.OpenPostgresTestDB(t)
	defer db.Close()
	user := &model.User{Username: fmt.Sprintf("prompt_patch_cancel_%d", time.Now().UnixNano()), PasswordHash: "fixture-hash", Role: "user", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`)}
	if err := NewUserRepository(db).Create(user); err != nil {
		t.Fatalf("create fixture user: %v", err)
	}
	prompt := &model.Prompt{UserID: user.ID, Title: "Before", Content: "Before content", Tags: []string{}}
	if err := NewPromptRepository(db).CreateContext(context.Background(), prompt); err != nil {
		t.Fatalf("create fixture prompt: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM prompts WHERE id = $1", prompt.ID)
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", user.ID)
	})

	blocker, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin blocker: %v", err)
	}
	defer blocker.Rollback()
	if _, err := blocker.ExecContext(context.Background(), `SELECT id FROM prompts WHERE id = $1 FOR UPDATE`, prompt.ID); err != nil {
		t.Fatalf("lock prompt row: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	title := "Must not commit"
	_, err = NewPromptRepository(db).PatchContext(ctx, prompt.ID, user.ID, PromptPatch{Title: &title})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canceled Prompt patch error = %v, want deadline exceeded", err)
	}
	if err := blocker.Commit(); err != nil {
		t.Fatalf("release blocker: %v", err)
	}
	updated, err := NewPromptRepository(db).GetByID(prompt.ID, user.ID)
	if err != nil {
		t.Fatalf("reload prompt: %v", err)
	}
	if updated.Title != "Before" {
		t.Fatalf("canceled Prompt patch committed title=%q", updated.Title)
	}
}

func waitForPromptPatchWaiters(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		if err := db.QueryRow(`
			SELECT count(*) FROM pg_stat_activity
			WHERE pid <> pg_backend_pid()
			  AND wait_event_type = 'Lock'
			  AND query LIKE '%FROM prompts WHERE id = $1%'
		`).Scan(&waiting); err == nil && waiting >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Prompt patches did not reach the row lock: want %d waiters", want)
}
