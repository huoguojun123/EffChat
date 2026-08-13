package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/testutil"
)

func TestPromptRepositoryClassifiesMissingWriteTargets(t *testing.T) {
	db := testutil.OpenPostgresTestDB(t)
	repo := NewPromptRepository(db)

	missing := &model.Prompt{ID: 999999999, UserID: 999999999, Title: "fixture", Content: "fixture", GroupName: "默认分组", Tags: []string{}}
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "personal update", run: func() error {
			_, err := repo.PatchContext(context.Background(), missing.ID, missing.UserID, PromptPatch{Title: &missing.Title})
			return err
		}},
		{name: "shared update", run: func() error {
			_, err := repo.PatchSharedContext(context.Background(), missing.ID, PromptPatch{Title: &missing.Title})
			return err
		}},
		{name: "personal delete", run: func() error { return repo.Delete(missing.ID, missing.UserID) }},
		{name: "shared delete", run: func() error { return repo.DeleteShared(missing.ID) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); !errors.Is(err, ErrNotFound) {
				t.Fatalf("error = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestPromptRepositoryClassifiesMissingGroup(t *testing.T) {
	db := testutil.OpenPostgresTestDB(t)
	userRepo := NewUserRepository(db)
	user := &model.User{Username: "prompt_repository_owner", PasswordHash: "fixture-hash", Role: "user", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`)}
	if err := userRepo.Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	missingGroupID := int64(999999999)
	prompt := &model.Prompt{UserID: user.ID, Title: "fixture", Content: "fixture", GroupID: &missingGroupID, Tags: []string{}}
	if err := NewPromptRepository(db).CreateContext(context.Background(), prompt); !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}
