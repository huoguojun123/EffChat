package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
)

func TestPromptGroupRepositoryUpdateRollsBackWhenPromptSyncFails(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	userID, group, prompt := createPromptGroupFixture(t, db, "atomic")
	if _, err := db.Exec(`
		CREATE FUNCTION reject_prompt_group_sync() RETURNS trigger AS $function$
		BEGIN
			IF NEW.group_name = 'Rejected rename' THEN
				RAISE EXCEPTION 'forced prompt group sync failure';
			END IF;
			RETURN NEW;
		END;
		$function$ LANGUAGE plpgsql;

		CREATE TRIGGER reject_prompt_group_sync
		BEFORE UPDATE OF group_name ON prompts
		FOR EACH ROW EXECUTE FUNCTION reject_prompt_group_sync()
	`); err != nil {
		t.Fatalf("install prompt sync failure trigger: %v", err)
	}

	groupRepo := NewPromptGroupRepository(db)
	if _, err := groupRepo.Update(group.ID, userID, "Rejected rename"); err == nil {
		t.Fatal("expected prompt group rename to fail")
	}

	assertPromptGroupState(t, db, group.ID, prompt.ID, group.Name)
}

func TestPromptWritesWaitForPendingGroupRename(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)

	cases := []struct {
		name string
		run  func(*PromptRepository, int64, *model.Prompt) error
	}{
		{
			name: "create",
			run: func(repo *PromptRepository, userID int64, prompt *model.Prompt) error {
				return repo.Create(&model.Prompt{
					UserID:    userID,
					Title:     "created during rename",
					Content:   "content",
					Tags:      []string{},
					GroupID:   prompt.GroupID,
					GroupName: prompt.GroupName,
				})
			},
		},
		{
			name: "update",
			run: func(repo *PromptRepository, _ int64, prompt *model.Prompt) error {
				title := "updated during rename"
				_, err := repo.PatchContext(context.Background(), prompt.ID, prompt.UserID, PromptPatch{Title: &title})
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			userID, group, prompt := createPromptGroupFixture(t, db, tc.name)
			renamed := fmt.Sprintf("Renamed %s", tc.name)
			pendingRename, err := db.Begin()
			if err != nil {
				t.Fatalf("begin pending rename: %v", err)
			}
			defer pendingRename.Rollback()
			if _, err := pendingRename.Exec(`UPDATE prompt_groups SET name = $1 WHERE id = $2`, renamed, group.ID); err != nil {
				t.Fatalf("stage pending rename: %v", err)
			}
			if _, err := pendingRename.Exec(`UPDATE prompts SET group_name = $1 WHERE group_id = $2`, renamed, group.ID); err != nil {
				t.Fatalf("stage pending prompt sync: %v", err)
			}

			applicationName := "prompt_write_" + tc.name
			setAvailableConnectionApplicationName(t, db, applicationName)
			done := make(chan error, 1)
			go func() {
				done <- tc.run(NewPromptRepository(db), userID, prompt)
			}()

			waitForRepositoryLock(t, pendingRename, applicationName, done)
			if err := pendingRename.Commit(); err != nil {
				t.Fatalf("commit pending rename: %v", err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("write prompt after rename: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("prompt write did not resume after rename committed")
			}

			var mismatches int
			if err := db.QueryRow(`
				SELECT COUNT(*)
				FROM prompts
				WHERE group_id = $1 AND group_name <> $2
			`, group.ID, renamed).Scan(&mismatches); err != nil {
				t.Fatalf("count mismatched prompt group names: %v", err)
			}
			if mismatches != 0 {
				t.Fatalf("found %d prompts with a stale group name", mismatches)
			}
		})
	}
}

func TestPromptRepositoryUpdateContextCancelsGroupLockWait(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	_, group, prompt := createPromptGroupFixture(t, db, "context")
	pendingRename, err := db.Begin()
	if err != nil {
		t.Fatalf("begin pending rename: %v", err)
	}
	defer pendingRename.Rollback()
	if _, err := pendingRename.Exec(`UPDATE prompt_groups SET name = $1 WHERE id = $2`, "Waiting rename", group.ID); err != nil {
		t.Fatalf("stage pending rename: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	title := "canceled update"
	_, err = NewPromptRepository(db).PatchContext(ctx, prompt.ID, prompt.UserID, PromptPatch{Title: &title})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("update error = %v, want context deadline", err)
	}
	if _, err := db.Exec(`SELECT 1 FROM prompts WHERE id = $1`, prompt.ID); err != nil {
		t.Fatalf("verify prompt after canceled update: %v", err)
	}
}

func TestPromptGroupRepositoryDeleteMovesPromptsToDefault(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	userID, group, prompt := createPromptGroupFixture(t, db, "delete")
	if err := NewPromptGroupRepository(db).Delete(group.ID, userID); err != nil {
		t.Fatalf("delete prompt group: %v", err)
	}

	var groupID *int64
	var groupName string
	if err := db.QueryRow(`SELECT group_id, group_name FROM prompts WHERE id = $1`, prompt.ID).Scan(&groupID, &groupName); err != nil {
		t.Fatalf("load prompt after group deletion: %v", err)
	}
	if groupID != nil || groupName != "默认分组" {
		t.Fatalf("prompt group after deletion = (%v, %q), want (nil, %q)", groupID, groupName, "默认分组")
	}
}

func TestPromptGroupDeleteBlocksConcurrentPromptBinding(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)

	userID, group, prompt := createPromptGroupFixture(t, db, "delrace")
	if _, err := db.Exec(`
		CREATE FUNCTION block_prompt_group_delete() RETURNS trigger AS $function$
		BEGIN
			IF OLD.group_id IS NOT NULL AND NEW.group_id IS NULL THEN
				PERFORM pg_advisory_xact_lock(93217, 48126);
			END IF;
			RETURN NEW;
		END;
		$function$ LANGUAGE plpgsql;

		CREATE TRIGGER block_prompt_group_delete
		BEFORE UPDATE OF group_id ON prompts
		FOR EACH ROW EXECUTE FUNCTION block_prompt_group_delete()
	`); err != nil {
		t.Fatalf("install prompt group delete blocker: %v", err)
	}

	blocker, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("open advisory lock connection: %v", err)
	}
	defer blocker.Close()
	if _, err := blocker.ExecContext(context.Background(), `SELECT pg_advisory_lock(93217, 48126)`); err != nil {
		t.Fatalf("hold prompt group delete blocker: %v", err)
	}
	blockerHeld := true
	defer func() {
		if blockerHeld {
			_, _ = blocker.ExecContext(context.Background(), `SELECT pg_advisory_unlock(93217, 48126)`)
		}
	}()

	deleteDone := make(chan error, 1)
	go func() {
		deleteDone <- NewPromptGroupRepository(db).DeleteContext(context.Background(), group.ID, userID)
	}()
	waitForAdvisoryLockWait(t, db, 93217, 48126, deleteDone)

	binding := &model.Prompt{
		UserID:    userID,
		Title:     "created during delete",
		Content:   "content",
		Tags:      []string{},
		GroupID:   &group.ID,
		GroupName: group.Name,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err = NewPromptRepository(db).CreateContext(ctx, binding)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("concurrent binding error = %v, want context deadline", err)
	}

	if _, err := blocker.ExecContext(context.Background(), `SELECT pg_advisory_unlock(93217, 48126)`); err != nil {
		t.Fatalf("release prompt group delete blocker: %v", err)
	}
	blockerHeld = false
	select {
	case err := <-deleteDone:
		if err != nil {
			t.Fatalf("delete prompt group after releasing blocker: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("prompt group deletion did not resume")
	}

	var groupID *int64
	var groupName string
	if err := db.QueryRow(`SELECT group_id, group_name FROM prompts WHERE id = $1`, prompt.ID).Scan(&groupID, &groupName); err != nil {
		t.Fatalf("load original prompt after concurrent delete: %v", err)
	}
	if groupID != nil || groupName != "默认分组" {
		t.Fatalf("original prompt group after deletion = (%v, %q), want (nil, %q)", groupID, groupName, "默认分组")
	}
	var concurrentPrompts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM prompts WHERE title = $1`, binding.Title).Scan(&concurrentPrompts); err != nil {
		t.Fatalf("count concurrent prompt bindings: %v", err)
	}
	if concurrentPrompts != 0 {
		t.Fatalf("found %d prompts committed during group deletion", concurrentPrompts)
	}
}

func createPromptGroupFixture(t *testing.T, db *sql.DB, suffix string) (int64, *model.PromptGroup, *model.Prompt) {
	t.Helper()

	userID := createRepositoryTestUser(t, db, "prompt_group_"+suffix)
	group, err := NewPromptGroupRepository(db).Create(userID, "Original "+suffix)
	if err != nil {
		t.Fatalf("create prompt group: %v", err)
	}
	prompt := &model.Prompt{
		UserID:    userID,
		Title:     "prompt " + suffix,
		Content:   "content",
		Tags:      []string{},
		GroupID:   &group.ID,
		GroupName: group.Name,
	}
	if err := NewPromptRepository(db).Create(prompt); err != nil {
		t.Fatalf("create prompt: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", userID)
	})
	return userID, group, prompt
}

func assertPromptGroupState(t *testing.T, db *sql.DB, groupID, promptID int64, wantName string) {
	t.Helper()

	var groupName string
	if err := db.QueryRow(`SELECT name FROM prompt_groups WHERE id = $1`, groupID).Scan(&groupName); err != nil {
		t.Fatalf("load prompt group: %v", err)
	}
	var promptGroupName string
	if err := db.QueryRow(`SELECT group_name FROM prompts WHERE id = $1`, promptID).Scan(&promptGroupName); err != nil {
		t.Fatalf("load prompt: %v", err)
	}
	if groupName != wantName || promptGroupName != wantName {
		t.Fatalf("stored names = (%q, %q), want both %q", groupName, promptGroupName, wantName)
	}
}

func setAvailableConnectionApplicationName(t *testing.T, db *sql.DB, name string) {
	t.Helper()

	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("reserve prompt writer connection: %v", err)
	}
	if _, err := conn.ExecContext(context.Background(), `SELECT set_config('application_name', $1, false)`, name); err != nil {
		_ = conn.Close()
		t.Fatalf("name prompt writer connection: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("release prompt writer connection: %v", err)
	}
}

func waitForRepositoryLock(t *testing.T, tx *sql.Tx, applicationName string, done <-chan error) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("prompt write completed before group rename committed: %v", err)
		default:
		}

		var waiting bool
		if err := tx.QueryRow(`
			SELECT EXISTS (
				SELECT 1
				FROM pg_stat_activity
				WHERE datname = current_database()
				  AND application_name = $1
				  AND wait_event_type = 'Lock'
			)
		`, applicationName).Scan(&waiting); err != nil {
			t.Fatalf("inspect prompt writer lock: %v", err)
		}
		if waiting {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("prompt write did not wait on the pending group rename")
}

func waitForAdvisoryLockWait(t *testing.T, db *sql.DB, classID, objectID int32, done <-chan error) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("prompt group deletion completed before blocker released: %v", err)
		default:
		}

		var waiting bool
		if err := db.QueryRow(`
			SELECT EXISTS (
				SELECT 1
				FROM pg_locks
				WHERE locktype = 'advisory'
				  AND classid = $1
				  AND objid = $2
				  AND NOT granted
			)
		`, classID, objectID).Scan(&waiting); err != nil {
			t.Fatalf("inspect prompt group delete lock: %v", err)
		}
		if waiting {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("prompt group deletion did not reach the controlled lock")
}
