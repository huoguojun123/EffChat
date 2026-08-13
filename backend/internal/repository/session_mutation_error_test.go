package repository

import (
	"errors"
	"testing"
)

func TestSessionMutationRowsAffectedZeroPreservesNotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	repo := NewSessionRepository(db)

	title := "Updated"
	if err := repo.UpdateFields(9_999_999_999, 9_999_999_999, SessionPatch{Title: &title}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing update error = %v, want ErrNotFound", err)
	}
	if err := repo.Delete(9_999_999_999, 9_999_999_999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing delete error = %v, want ErrNotFound", err)
	}
}
