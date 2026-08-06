package repository

import (
	"errors"
	"testing"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/testutil"
)

func TestUserRepositoryUpdateClassifiesConstraintAndMissingFailures(t *testing.T) {
	db := testutil.OpenPostgresTestDB(t)
	repo := NewUserRepository(db)

	firstEmail := "repository-first@example.test"
	first := &model.User{
		Username:     "repository_profile_a",
		Email:        &firstEmail,
		PasswordHash: "fixture-hash",
		Role:         "user",
		Permissions:  []byte(`{}`),
		Preferences:  []byte(`{}`),
		IsActive:     true,
	}
	if err := repo.Create(first); err != nil {
		t.Fatalf("create first user: %v", err)
	}
	secondEmail := "repository-second@example.test"
	second := &model.User{
		Username:     "repository_profile_b",
		Email:        &secondEmail,
		PasswordHash: "fixture-hash",
		Role:         "user",
		Permissions:  []byte(`{}`),
		Preferences:  []byte(`{}`),
		IsActive:     true,
	}
	if err := repo.Create(second); err != nil {
		t.Fatalf("create second user: %v", err)
	}

	if _, err := repo.UpdateFieldsContext(t.Context(), second.ID, UserPatch{EmailSet: true, Email: &firstEmail}); !errors.Is(err, ErrUserConflict) {
		t.Fatalf("duplicate email error = %v, want ErrUserConflict", err)
	}

	if _, err := repo.UpdateFieldsContext(t.Context(), 999999999, UserPatch{EmailSet: true}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing user error = %v, want ErrNotFound", err)
	}
}
