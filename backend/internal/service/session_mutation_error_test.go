package service

import (
	"errors"
	"testing"

	"github.com/huoguojun123/EffChat/internal/repository"
)

func TestSessionMutationDistinguishesValidationNotFoundAndRepositoryFailure(t *testing.T) {
	t.Run("validation", func(t *testing.T) {
		tooLong := string(make([]byte, maxSessionSystemPromptBytes+1))
		svc := NewSessionService(nil, nil, nil)
		if _, err := svc.Create(1, &CreateSessionRequest{SystemPrompt: &tooLong}); !errors.Is(err, ErrSessionInvalid) {
			t.Fatalf("create validation error = %v, want ErrSessionInvalid", err)
		}
	})

	t.Run("not found", func(t *testing.T) {
		db := setupMessageTestDB(t)
		defer db.Close()
		svc := NewSessionService(repository.NewSessionRepository(db), repository.NewMessageRepository(db), repository.NewConfigRepository(db))

		title := "Updated"
		if err := svc.Update(9_999_999_999, 9_999_999_999, &UpdateSessionRequest{Title: &title}); !errors.Is(err, ErrSessionNotFound) {
			t.Fatalf("missing update error = %v, want ErrSessionNotFound", err)
		}
		if err := svc.Delete(9_999_999_999, 9_999_999_999); !errors.Is(err, ErrSessionNotFound) {
			t.Fatalf("missing delete error = %v, want ErrSessionNotFound", err)
		}
	})

	t.Run("repository failure", func(t *testing.T) {
		db := setupMessageTestDB(t)
		svc := NewSessionService(repository.NewSessionRepository(db), repository.NewMessageRepository(db), repository.NewConfigRepository(db))
		if err := db.Close(); err != nil {
			t.Fatalf("close database: %v", err)
		}

		title := "Updated"
		if err := svc.Update(1, 1, &UpdateSessionRequest{Title: &title}); err == nil || errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrSessionInvalid) {
			t.Fatalf("closed database update error = %v, want internal failure", err)
		}
		if err := svc.Delete(1, 1); err == nil || errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrSessionInvalid) {
			t.Fatalf("closed database delete error = %v, want internal failure", err)
		}
	})

	t.Run("default model repository failure", func(t *testing.T) {
		db := setupMessageTestDB(t)
		svc := NewSessionService(repository.NewSessionRepository(db), repository.NewMessageRepository(db), repository.NewConfigRepository(db))
		if err := db.Close(); err != nil {
			t.Fatalf("close database: %v", err)
		}
		if _, err := svc.Create(1, &CreateSessionRequest{}); err == nil || errors.Is(err, ErrSessionInvalid) {
			t.Fatalf("closed default model lookup error = %v, want internal failure", err)
		}
	})
}
