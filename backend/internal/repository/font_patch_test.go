package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestFontPatchPreservesConcurrentFields(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	repo := NewFontRepository(db)
	font := createFontFixture(t, repo, "patch-concurrency")
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM font_assets WHERE id = $1", font.ID) })

	blocker, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin blocker: %v", err)
	}
	if _, err := blocker.ExecContext(context.Background(), `SELECT id FROM font_assets WHERE id = $1 FOR UPDATE`, font.ID); err != nil {
		_ = blocker.Rollback()
		t.Fatalf("lock font row: %v", err)
	}

	displayName := "After display"
	displayDone := make(chan error, 1)
	go func() {
		_, _, err := repo.PatchContext(context.Background(), font.ID, FontPatch{DisplayName: &displayName})
		displayDone <- err
	}()
	waitForFontPatchWaiters(t, db, 1)

	enabled := false
	enabledDone := make(chan error, 1)
	go func() {
		_, _, err := repo.PatchContext(context.Background(), font.ID, FontPatch{Enabled: &enabled})
		enabledDone <- err
	}()
	waitForFontPatchWaiters(t, db, 2)
	if err := blocker.Commit(); err != nil {
		t.Fatalf("release blocker: %v", err)
	}
	if err := <-displayDone; err != nil {
		t.Fatalf("display patch: %v", err)
	}
	if err := <-enabledDone; err != nil {
		t.Fatalf("enabled patch: %v", err)
	}
	updated, err := repo.Get(font.ID)
	if err != nil {
		t.Fatalf("reload font: %v", err)
	}
	if updated.DisplayName != displayName || updated.Enabled {
		t.Fatalf("concurrent Font patches lost state: display=%q enabled=%v", updated.DisplayName, updated.Enabled)
	}
}

func TestFontPatchHonorsContextCancellation(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	repo := NewFontRepository(db)
	font := createFontFixture(t, repo, "patch-cancel")
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM font_assets WHERE id = $1", font.ID) })
	blocker, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin blocker: %v", err)
	}
	defer blocker.Rollback()
	if _, err := blocker.ExecContext(context.Background(), `SELECT id FROM font_assets WHERE id = $1 FOR UPDATE`, font.ID); err != nil {
		t.Fatalf("lock font row: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	displayName := "Must not commit"
	_, _, err = repo.PatchContext(ctx, font.ID, FontPatch{DisplayName: &displayName})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canceled Font patch error = %v, want deadline exceeded", err)
	}
	if err := blocker.Commit(); err != nil {
		t.Fatalf("release blocker: %v", err)
	}
	updated, err := repo.Get(font.ID)
	if err != nil {
		t.Fatalf("reload font: %v", err)
	}
	if updated.DisplayName == displayName {
		t.Fatalf("canceled Font patch committed display=%q", updated.DisplayName)
	}
}

func waitForFontPatchWaiters(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		if err := db.QueryRow(`
			SELECT count(*) FROM pg_stat_activity
			WHERE pid <> pg_backend_pid()
			  AND wait_event_type = 'Lock'
			  AND query LIKE '%FROM font_assets WHERE id = $1%'
		`).Scan(&waiting); err == nil && waiting >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Font patches did not reach the row lock: want %d waiters", want)
}
