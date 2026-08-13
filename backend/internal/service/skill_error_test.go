package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/huoguojun123/EffChat/internal/repository"
	skillparser "github.com/huoguojun123/EffChat/internal/skill"
	"github.com/huoguojun123/EffChat/internal/testutil"
)

func TestSkillInputFailuresAreTyped(t *testing.T) {
	service := &SkillService{}
	tests := []struct {
		name string
		call func() error
		kind SkillErrorKind
	}{
		{
			name: "manual package",
			call: func() error {
				_, err := service.CreateManual(7, &SkillInput{Name: "fixture"})
				return err
			},
			kind: SkillErrorInvalid,
		},
		{
			name: "zip archive",
			call: func() error {
				_, err := service.PreviewZip([]byte("not-a-zip"))
				return err
			},
			kind: SkillErrorInvalid,
		},
		{
			name: "git url",
			call: func() error {
				_, err := service.PreviewGit(context.Background(), &SkillGitPreviewRequest{URL: "file:///srv/private/skill"})
				return err
			},
			kind: SkillErrorInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var skillErr *SkillError
			if err := tt.call(); !errors.As(err, &skillErr) || skillErr.Kind != tt.kind {
				t.Fatalf("error=%v typed=%+v", err, skillErr)
			}
		})
	}
}

func TestSkillMetadataUpdateNotFoundIsTyped(t *testing.T) {
	db := testutil.OpenPostgresTestDB(t)
	service := NewSkillService(repository.NewSkillRepository(db), nil, nil)
	description := "fixture"

	_, err := service.Update(7, fmt.Sprintf("missing-skill-%d", time.Now().UnixNano()), &SkillUpdateInput{Description: &description})
	var skillErr *SkillError
	if !errors.As(err, &skillErr) || skillErr.Kind != SkillErrorNotFound {
		t.Fatalf("error=%v typed=%+v", err, skillErr)
	}
}

func TestSkillCRUDSuccessWithManagedPackage(t *testing.T) {
	db := testutil.OpenPostgresTestDB(t)
	userID := time.Now().UnixNano()
	if _, err := db.Exec(
		`INSERT INTO users (id, username, password_hash, role, is_active, permissions, preferences)
		 VALUES ($1, $2, 'fixture-hash', 'admin', true, '{}', '{}')`,
		userID,
		fmt.Sprintf("skill_crud_%d", userID),
	); err != nil {
		t.Fatalf("seed Skill admin: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM users WHERE id = $1", userID) })

	previousRoot := skillUploadDir
	skillUploadDir = t.TempDir()
	t.Cleanup(func() { skillUploadDir = previousRoot })

	service := NewSkillService(repository.NewSkillRepository(db), repository.NewUserRepository(db), repository.NewSessionRepository(db))
	service.SetGovernanceRepository(repository.NewGovernanceRepository(db))
	t.Cleanup(func() {
		service.cleanupMu.Lock()
		defer service.cleanupMu.Unlock()
		if service.cleanupTimer != nil {
			service.cleanupTimer.Stop()
		}
	})

	created, err := service.CreateManual(userID, &SkillInput{
		ID:           "contract-fixture",
		Name:         "Contract Fixture",
		EntryContent: "---\nname: Contract Fixture\n---\n\nUse this fixture.",
	})
	if err != nil {
		t.Fatalf("create Skill: %v", err)
	}
	if created.ID != "contract-fixture" || len(created.Files) != 1 {
		t.Fatalf("created Skill=%+v", created)
	}
	content, _, err := service.ReadFileAdmin(created.ID, "SKILL.md")
	if err != nil || content == "" {
		t.Fatalf("read Skill entry: content=%q err=%v", content, err)
	}
	name := "Updated Contract Fixture"
	updated, err := service.Update(userID, created.ID, &SkillUpdateInput{Name: &name})
	if err != nil || updated.Name != name || len(updated.Files) != 1 {
		t.Fatalf("update Skill=%+v err=%v", updated, err)
	}
	if err := service.Delete(userID, created.ID); err != nil {
		t.Fatalf("delete Skill: %v", err)
	}
	if _, _, err := service.ReadFileAdmin(created.ID, "SKILL.md"); err == nil {
		t.Fatal("deleted Skill remained readable")
	}
	history, err := service.ListHistory(created.ID)
	if err != nil || len(history) != 3 || history[0].Action != "delete" {
		t.Fatalf("governance history=%+v err=%v", history, err)
	}
	restored, reverse, err := service.Rollback(userID, history[0].ID, "restore fixture deletion")
	if err != nil || restored == nil || restored.Name != name || reverse.RollbackOfEventID == nil || *reverse.RollbackOfEventID != history[0].ID {
		t.Fatalf("rollback restored=%+v reverse=%+v err=%v", restored, reverse, err)
	}
	if content, _, err := service.ReadFileAdmin(created.ID, "SKILL.md"); err != nil || content == "" {
		t.Fatalf("restored Skill content=%q err=%v", content, err)
	}
}

func TestSkillBatchImportRollsBackAndRetriesWithoutDuplicateHistory(t *testing.T) {
	db := testutil.OpenPostgresTestDB(t)
	userID := time.Now().UnixNano()
	username := fmt.Sprintf("skill_batch_%d", userID)
	if _, err := db.Exec(
		`INSERT INTO users (id, username, password_hash, role, is_active, permissions, preferences)
		 VALUES ($1, $2, 'fixture-hash', 'admin', true, '{}', '{}')`,
		userID, username,
	); err != nil {
		t.Fatalf("seed Skill batch admin: %v", err)
	}
	firstID := fmt.Sprintf("batch-first-%d", userID)
	secondID := fmt.Sprintf("batch-second-%d", userID)
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM skills WHERE id IN ($1, $2)", firstID, secondID)
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", userID)
	})

	previousRoot := skillUploadDir
	skillUploadDir = t.TempDir()
	t.Cleanup(func() { skillUploadDir = previousRoot })
	service := NewSkillService(repository.NewSkillRepository(db), repository.NewUserRepository(db), repository.NewSessionRepository(db))
	t.Cleanup(func() {
		service.cleanupMu.Lock()
		defer service.cleanupMu.Unlock()
		if service.cleanupTimer != nil {
			service.cleanupTimer.Stop()
		}
	})

	initial, err := service.CreateManual(userID, &SkillInput{
		ID: firstID, Name: "Batch First", EntryContent: "---\nname: Batch First\n---\n\nInitial.",
	})
	if err != nil {
		t.Fatalf("seed existing Skill: %v", err)
	}
	first, err := skillparser.ParseTextSkill("upstream/first/SKILL.md", []byte("---\nname: Batch First Updated\n---\n\nUpdated."))
	if err != nil {
		t.Fatal(err)
	}
	first.PackageChecksum = skillparser.PackageChecksum(first.Files)
	second, err := skillparser.ParseTextSkill("upstream/second/SKILL.md", []byte("---\nname: Batch Second\n---\n\nCreated."))
	if err != nil {
		t.Fatal(err)
	}
	second.ID = secondID
	second.PackageChecksum = skillparser.PackageChecksum(second.Files)

	triggerName := fmt.Sprintf("reject_skill_batch_%d", userID)
	quotedSecondID := "'" + strings.ReplaceAll(secondID, "'", "''") + "'"
	if _, err := db.Exec(fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.id = %s THEN RAISE EXCEPTION 'fixture batch failure'; END IF;
			RETURN NEW;
		END $$;
		CREATE TRIGGER %s BEFORE INSERT OR UPDATE ON skills
		FOR EACH ROW EXECUTE FUNCTION %s()
	`, triggerName, quotedSecondID, triggerName, triggerName)); err != nil {
		t.Fatalf("install batch failure trigger: %v", err)
	}
	dropTrigger := func() {
		_, _ = db.Exec(fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON skills; DROP FUNCTION IF EXISTS %s()", triggerName, triggerName))
	}
	t.Cleanup(dropTrigger)

	parsed := []skillparser.ParsedSkill{first, second}
	targets := map[string]string{first.SourcePath: firstID}
	if _, err := service.persistParsed(userID, parsed, skillparser.ImportReport{}, SkillSourceZip, nil, nil, targets); err == nil {
		t.Fatal("expected second Skill failure to roll back the batch")
	}
	var activeChecksum string
	if err := db.QueryRow("SELECT package_checksum FROM skills WHERE id = $1", firstID).Scan(&activeChecksum); err != nil {
		t.Fatal(err)
	}
	if activeChecksum != initial.PackageChecksum {
		t.Fatalf("first Skill changed after failed batch: got %s want %s", activeChecksum, initial.PackageChecksum)
	}
	var secondCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM skills WHERE id = $1", secondID).Scan(&secondCount); err != nil || secondCount != 0 {
		t.Fatalf("second Skill count=%d err=%v", secondCount, err)
	}
	dropTrigger()

	result, err := service.persistParsed(userID, parsed, skillparser.ImportReport{}, SkillSourceZip, nil, nil, targets)
	if err != nil {
		t.Fatalf("commit batch: %v", err)
	}
	if len(result.Created) != 1 || result.Created[0] != secondID || len(result.Updated) != 1 || result.Updated[0] != firstID || len(result.Unchanged) != 0 {
		t.Fatalf("batch result=%+v", result)
	}
	assertSkillBatchHistoryCounts(t, db, firstID, secondID, 2, 1)
	retry, err := service.persistParsed(userID, parsed, skillparser.ImportReport{}, SkillSourceZip, nil, nil, targets)
	if err != nil {
		t.Fatalf("retry unchanged batch: %v", err)
	}
	if len(retry.Unchanged) != 2 || len(retry.Created) != 0 || len(retry.Updated) != 0 {
		t.Fatalf("retry result=%+v", retry)
	}
	assertSkillBatchHistoryCounts(t, db, firstID, secondID, 2, 1)
}

func assertSkillBatchHistoryCounts(t *testing.T, db interface {
	QueryRow(query string, args ...any) *sql.Row
}, firstID, secondID string, firstWant, secondWant int) {
	t.Helper()
	for id, want := range map[string]int{firstID: firstWant, secondID: secondWant} {
		var records, events int
		if err := db.QueryRow("SELECT COUNT(*) FROM skill_import_records WHERE skill_id = $1", id).Scan(&records); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT COUNT(*) FROM governance_events WHERE resource_type = 'skill' AND resource_key = $1", id).Scan(&events); err != nil {
			t.Fatal(err)
		}
		if records != want || events != want {
			t.Fatalf("Skill %s history records/events=%d/%d want=%d", id, records, events, want)
		}
	}
}
