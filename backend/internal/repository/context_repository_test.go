package repository

import (
	"context"
	"errors"
	"testing"
)

type failedMessageRows struct {
	err error
}

func (r failedMessageRows) Next() bool                { return false }
func (r failedMessageRows) Scan(...interface{}) error { return nil }
func (r failedMessageRows) Err() error                { return r.err }

func TestContextAwareRepositoriesHonorCancellation(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "session", run: func() error { _, err := NewSessionRepository(db).GetByIDContext(ctx, 1, 1); return err }},
		{name: "user", run: func() error { _, err := NewUserRepository(db).GetByIDContext(ctx, 1); return err }},
		{name: "messages", run: func() error { _, err := NewMessageRepository(db).ListBySessionContext(ctx, 1); return err }},
		{name: "skills", run: func() error { _, err := NewSkillRepository(db).ListContext(ctx, false); return err }},
		{name: "attachments", run: func() error {
			_, err := NewFileRepository(db).GetActiveFilesForSessionContext(ctx, 1, 1, []int64{1})
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v, want context.Canceled", err)
			}
		})
	}

	configRepo := NewConfigRepository(db)
	t.Run("config string", func(t *testing.T) {
		if _, err := configRepo.GetStringContext(ctx, "system_name", "fallback"); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	})
	t.Run("config int", func(t *testing.T) {
		if _, err := configRepo.GetIntContext(ctx, "title_generation_trigger", 2); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	})
}

func TestScanMessagesPropagatesIterationFailure(t *testing.T) {
	want := errors.New("connection interrupted")
	if _, err := scanMessages(failedMessageRows{err: want}); !errors.Is(err, want) {
		t.Fatalf("scan messages error = %v", err)
	}
}
