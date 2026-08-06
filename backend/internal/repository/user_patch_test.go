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

func TestUserRepositoryUpdateFieldsPreservesConcurrentProfileAndAdminPatches(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repo := NewUserRepository(db)
	user := seedPatchUser(t, db, repo, "concurrent")

	blocker, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin blocker: %v", err)
	}
	if _, err := blocker.ExecContext(context.Background(), `SELECT id FROM users WHERE id = $1 FOR UPDATE`, user.ID); err != nil {
		_ = blocker.Rollback()
		t.Fatalf("lock user row: %v", err)
	}

	nickname := "patched nickname"
	role := "admin"
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, patch := range []UserPatch{
		{NicknameSet: true, Nickname: &nickname},
		{Role: &role},
	} {
		patch := patch
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, updateErr := repo.UpdateFieldsContext(context.Background(), user.ID, patch)
			results <- updateErr
		}()
	}

	waitForUserPatchWaiters(t, db, 2)
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

	updated, err := repo.GetByIDIncludeInactive(user.ID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if updated.Nickname == nil || *updated.Nickname != nickname || updated.Role != role {
		t.Fatalf("concurrent partial updates lost a field: nickname=%v role=%q", updated.Nickname, updated.Role)
	}
}

func TestUserRepositoryUpdateFieldsHonorsContextCancellation(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repo := NewUserRepository(db)
	user := seedPatchUser(t, db, repo, "cancel")
	blocker, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin blocker: %v", err)
	}
	defer blocker.Rollback()
	if _, err := blocker.ExecContext(context.Background(), `SELECT id FROM users WHERE id = $1 FOR UPDATE`, user.ID); err != nil {
		t.Fatalf("lock user row: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	nickname := "must not commit"
	_, _, err = repo.UpdateFieldsContext(ctx, user.ID, UserPatch{NicknameSet: true, Nickname: &nickname})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canceled update error = %v, want deadline exceeded", err)
	}
	if err := blocker.Commit(); err != nil {
		t.Fatalf("release blocker: %v", err)
	}

	updated, err := repo.GetByIDIncludeInactive(user.ID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if updated.Nickname != nil {
		t.Fatalf("canceled update committed nickname=%q", *updated.Nickname)
	}
}

func seedPatchUser(t *testing.T, db *sql.DB, repo *UserRepository, suffix string) *model.User {
	t.Helper()
	unique := fmt.Sprintf("user-patch-%s-%d", suffix, time.Now().UnixNano())
	user := &model.User{
		Username:     unique,
		PasswordHash: "fixture-hash",
		Role:         "user",
		Permissions:  []byte(`{}`),
		Preferences:  []byte(`{}`),
		IsActive:     true,
	}
	if err := repo.Create(user); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM users WHERE id = $1", user.ID) })
	return user
}

func waitForUserPatchWaiters(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		if err := db.QueryRow(`
			SELECT count(*)
			FROM pg_stat_activity
			WHERE pid <> pg_backend_pid()
			  AND wait_event_type = 'Lock'
			  AND query LIKE '%FROM users WHERE id = $1 FOR UPDATE%'
		`).Scan(&waiting); err == nil && waiting >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("user patches did not reach the row lock: want %d waiters", want)
}
