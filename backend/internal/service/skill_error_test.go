package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/huoguojun123/EffChat/internal/repository"
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
