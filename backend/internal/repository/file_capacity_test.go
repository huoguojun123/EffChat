package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/huoguojun123/EffChat/internal/model"
)

func TestFileCapacityAdmissionSharesSessionBudgetAndChecksOwner(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	userID := createRepositoryTestUser(t, db, "capacity")
	otherID := createRepositoryTestUser(t, db, "capacity_other")
	session := &model.Session{UserID: userID, Title: "capacity fixture", ModelID: "test-model", Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := NewSessionRepository(db).Create(session); err != nil {
		t.Fatal(err)
	}
	repo := NewFileRepository(db)
	file := func(owner int64, status string) *model.File {
		return &model.File{UserID: owner, SessionID: &session.ID, FileName: "fixture.pdf", FilePath: "fixture.txt", FileType: "application/pdf", FileSize: 1, ExtractStatus: status}
	}
	if err := repo.CreateWithinSessionLimitContext(context.Background(), file(otherID, "ready"), 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other owner accepted: %v", err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, status := range []string{"ready", "ocr_pending"} {
		f := file(userID, status)
		go func() { <-start; results <- repo.CreateWithinSessionLimitContext(context.Background(), f, 1) }()
	}
	close(start)
	accepted, rejected := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			accepted++
		} else if errors.Is(err, ErrSessionFileLimitReached) {
			rejected++
		} else {
			t.Fatal(err)
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatalf("accepted=%d rejected=%d", accepted, rejected)
	}
	if _, err := db.Exec("UPDATE sessions SET deleted_at=NOW() WHERE id=$1", session.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateWithinSessionLimitContext(context.Background(), file(userID, "ready"), 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted session accepted: %v", err)
	}
}
