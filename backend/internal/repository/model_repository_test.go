package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
)

func TestModelRepositoryUpdateFieldsPreservesConcurrentPartialUpdates(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	id := fmt.Sprintf("patch-concurrency-%d", time.Now().UnixNano())
	repo := NewModelRepository(db)
	seed := &model.Model{
		ID: id, DisplayName: "Before", Provider: "fixture", Vision: false,
		ThinkingFormat: "auto", ContextWindow: 1024, MaxOutput: 256,
		Enabled: true,
	}
	if err := repo.Upsert(seed); err != nil {
		t.Fatalf("seed model: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM models WHERE id = $1", id) })

	blocker, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin blocker: %v", err)
	}
	if _, err := blocker.ExecContext(context.Background(), `SELECT id FROM models WHERE id = $1 FOR UPDATE`, id); err != nil {
		_ = blocker.Rollback()
		t.Fatalf("lock model row: %v", err)
	}

	displayName := "After"
	vision := true
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, patch := range []ModelPatch{
		{DisplayName: &displayName},
		{Vision: &vision},
	} {
		patch := patch
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, updateErr := repo.UpdateFields(context.Background(), id, patch, nil)
			results <- updateErr
		}()
	}

	waitForModelUpdateWaiters(t, db)
	if err := blocker.Commit(); err != nil {
		t.Fatalf("release blocker: %v", err)
	}
	wg.Wait()
	close(results)
	for updateErr := range results {
		if updateErr != nil {
			t.Fatalf("concurrent partial update: %v", updateErr)
		}
	}

	updated, err := repo.Get(id)
	if err != nil {
		t.Fatalf("reload model: %v", err)
	}
	if updated.DisplayName != displayName || !updated.Vision {
		t.Fatalf("concurrent partial updates lost a field: got display_name=%q vision=%v", updated.DisplayName, updated.Vision)
	}
}

func TestModelRepositoryUpdateFieldsHonorsContextCancellation(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	id := fmt.Sprintf("patch-cancel-%d", time.Now().UnixNano())
	repo := NewModelRepository(db)
	if err := repo.Upsert(&model.Model{
		ID: id, DisplayName: "Before", Provider: "fixture", ThinkingFormat: "auto", Enabled: true,
	}); err != nil {
		t.Fatalf("seed model: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM models WHERE id = $1", id) })

	blocker, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin blocker: %v", err)
	}
	defer blocker.Rollback()
	if _, err := blocker.ExecContext(context.Background(), `SELECT id FROM models WHERE id = $1 FOR UPDATE`, id); err != nil {
		t.Fatalf("lock model row: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	displayName := "Canceled"
	_, err = repo.UpdateFields(ctx, id, ModelPatch{DisplayName: &displayName}, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canceled update error = %v, want deadline exceeded", err)
	}
	if err := blocker.Commit(); err != nil {
		t.Fatalf("release blocker: %v", err)
	}

	stored, err := repo.Get(id)
	if err != nil {
		t.Fatalf("reload model: %v", err)
	}
	if stored.DisplayName != "Before" {
		t.Fatalf("canceled update committed display_name=%q", stored.DisplayName)
	}
}

func waitForModelUpdateWaiters(t *testing.T, db *sql.DB) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		if err := db.QueryRow(`
			SELECT count(*)
			FROM pg_stat_activity
			WHERE pid <> pg_backend_pid()
			  AND wait_event_type = 'Lock'
			  AND query LIKE '%FROM models WHERE id = $1 FOR UPDATE%'
		`).Scan(&waiting); err == nil && waiting >= 2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("concurrent model updates did not reach the row lock")
}
