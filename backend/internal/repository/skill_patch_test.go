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

func TestSkillMetadataAndPackageUpdatesPreserveConcurrentOwners(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	userID := createRepositoryTestUser(t, db, "skill_patch_concurrency")
	skillID := fmt.Sprintf("skill-patch-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM skills WHERE id = $1", skillID)
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", userID)
	})
	repo := NewSkillRepository(db)
	seedSkillPackage(t, repo, userID, skillID, "Before", true, "a", "/fixture/skill-patch/v1/SKILL.md")

	blocker, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin blocker: %v", err)
	}
	if _, err := blocker.ExecContext(context.Background(), `SELECT id FROM skills WHERE id = $1 FOR UPDATE`, skillID); err != nil {
		_ = blocker.Rollback()
		t.Fatalf("lock Skill row: %v", err)
	}

	enabled := false
	metadataDone := make(chan error, 1)
	go func() {
		_, _, updateErr := repo.UpdateMetadataGoverned(context.Background(), skillID, SkillMetadataPatch{Enabled: &enabled}, SkillGovernanceMutation{
			Action: "update", ActorType: "admin", ActorUserID: userID, Reason: "disable fixture Skill",
		})
		metadataDone <- updateErr
	}()
	waitForSkillPatchWaiters(t, db, 1)

	name := "After package"
	description := "package replacement"
	packageSkill, packageFiles, packageRecord := skillPackageFixture(userID, skillID, name, true, "b", "/fixture/skill-patch/v2/SKILL.md")
	packageDone := make(chan error, 1)
	go func() {
		_, updateErr := repo.UpsertPackageGoverned(context.Background(), packageSkill, packageFiles, packageRecord, SkillMetadataPatch{
			Name: &name, Description: &description,
		}, SkillGovernanceMutation{
			Action: "import", ActorType: "import", ActorUserID: userID, Reason: "replace fixture Skill package",
		})
		packageDone <- updateErr
	}()
	waitForSkillPatchWaiters(t, db, 2)

	if err := blocker.Commit(); err != nil {
		t.Fatalf("release blocker: %v", err)
	}
	for _, result := range []<-chan error{metadataDone, packageDone} {
		if err := <-result; err != nil {
			t.Fatalf("concurrent Skill update: %v", err)
		}
	}

	updated, err := repo.Get(skillID, true)
	if err != nil {
		t.Fatalf("reload Skill: %v", err)
	}
	if updated.Name != name || updated.Description != description || updated.Enabled || updated.PackageChecksum != repeatedChecksum("b") {
		t.Fatalf("concurrent Skill owners lost state: name=%q description=%q enabled=%v package=%q", updated.Name, updated.Description, updated.Enabled, updated.PackageChecksum)
	}
	if len(updated.Files) != 1 || updated.Files[0].StoragePath != "/fixture/skill-patch/v2/SKILL.md" {
		t.Fatalf("active Skill files = %+v", updated.Files)
	}
}

func TestSkillMetadataPatchHonorsContextCancellation(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	userID := createRepositoryTestUser(t, db, "skill_patch_cancel")
	skillID := fmt.Sprintf("skill-patch-cancel-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM skills WHERE id = $1", skillID)
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", userID)
	})
	repo := NewSkillRepository(db)
	seedSkillPackage(t, repo, userID, skillID, "Before", true, "c", "/fixture/skill-patch/cancel/SKILL.md")

	blocker, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin blocker: %v", err)
	}
	defer blocker.Rollback()
	if _, err := blocker.ExecContext(context.Background(), `SELECT id FROM skills WHERE id = $1 FOR UPDATE`, skillID); err != nil {
		t.Fatalf("lock Skill row: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	description := "must not commit"
	_, _, err = repo.UpdateMetadataGoverned(ctx, skillID, SkillMetadataPatch{Description: &description}, SkillGovernanceMutation{
		Action: "update", ActorType: "admin", ActorUserID: userID, Reason: "canceled fixture update",
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canceled Skill update error = %v, want deadline exceeded", err)
	}
	if err := blocker.Commit(); err != nil {
		t.Fatalf("release blocker: %v", err)
	}

	updated, err := repo.Get(skillID, true)
	if err != nil {
		t.Fatalf("reload Skill: %v", err)
	}
	if updated.Description != "fixture" {
		t.Fatalf("canceled Skill update committed description=%q", updated.Description)
	}
}

func seedSkillPackage(t *testing.T, repo *SkillRepository, userID int64, skillID, name string, enabled bool, checksumSeed, path string) {
	t.Helper()
	skill, files, record := skillPackageFixture(userID, skillID, name, enabled, checksumSeed, path)
	if err := repo.UpsertPackageWithRecord(skill, files, record); err != nil {
		t.Fatalf("seed Skill package: %v", err)
	}
}

func skillPackageFixture(userID int64, skillID, name string, enabled bool, checksumSeed, path string) (*model.Skill, []model.SkillFile, *model.SkillImportRecord) {
	checksum := repeatedChecksum(checksumSeed)
	skill := &model.Skill{
		ID: skillID, Name: name, Description: "fixture", SourceType: "manual",
		Checksum: checksum, PackageChecksum: checksum, EntryPath: "SKILL.md",
		Enabled: enabled, CreatedBy: &userID,
	}
	files := []model.SkillFile{{RelativePath: "SKILL.md", StoragePath: path, Kind: "entry", Size: 7, Checksum: checksum}}
	record := &model.SkillImportRecord{Action: "update", SourceType: "manual", PackageChecksum: checksum, CreatedBy: &userID}
	return skill, files, record
}

func repeatedChecksum(seed string) string {
	var checksum string
	for len(checksum) < 64 {
		checksum += seed
	}
	return checksum[:64]
}

func waitForSkillPatchWaiters(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		if err := db.QueryRow(`
			SELECT count(*)
			FROM pg_stat_activity
			WHERE pid <> pg_backend_pid()
			  AND wait_event_type = 'Lock'
			  AND query LIKE '%FROM skills WHERE id = $1 FOR UPDATE%'
		`).Scan(&waiting); err == nil && waiting >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Skill updates did not reach the row lock: want %d waiters", want)
}
