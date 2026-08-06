package repository

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
)

func TestUserGroupRepositoryUpdateFieldsPreservesConcurrentPartialUpdates(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repo := NewUserGroupRepository(db)
	group := &model.UserGroup{
		Name:  fmt.Sprintf("patch-group-%d", time.Now().UnixNano()),
		Level: 10, Description: "before", DailyMessageLimit: 10,
	}
	if err := repo.CreateContext(context.Background(), group); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM user_groups WHERE id = $1", group.ID) })

	blocker, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin blocker: %v", err)
	}
	if _, err := blocker.ExecContext(context.Background(), `SELECT id FROM user_groups WHERE id = $1 FOR UPDATE`, group.ID); err != nil {
		_ = blocker.Rollback()
		t.Fatalf("lock group row: %v", err)
	}

	name := "after"
	limit := 99
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, patch := range []UserGroupPatch{{Name: &name}, {DailyMessageLimit: &limit}} {
		patch := patch
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, updateErr := repo.UpdateFieldsContext(context.Background(), group.ID, patch, nil)
			results <- updateErr
		}()
	}

	waitForUserGroupPatchWaiter(t, db)
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

	updated, err := repo.Get(group.ID)
	if err != nil {
		t.Fatalf("reload group: %v", err)
	}
	if updated.Name != name || updated.DailyMessageLimit != limit {
		t.Fatalf("concurrent partial updates lost a field: got name=%q daily_message_limit=%d", updated.Name, updated.DailyMessageLimit)
	}
}

func waitForUserGroupPatchWaiter(t *testing.T, db *sql.DB) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		if err := db.QueryRow(`
			SELECT count(*)
			FROM pg_stat_activity
			WHERE pid <> pg_backend_pid()
			  AND wait_event_type = 'Lock'
			  AND query LIKE '%FROM user_groups WHERE id = $1 FOR UPDATE%'
		`).Scan(&waiting); err == nil && waiting >= 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("concurrent user-group updates did not reach the row lock")
}
