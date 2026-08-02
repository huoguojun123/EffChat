package service

import (
	"errors"
	"testing"

	"github.com/huoguojun123/EffChat/internal/repository"
)

func TestSessionLookupDistinguishesNotFoundFromRepositoryFailure(t *testing.T) {
	db := setupMessageTestDB(t)
	sessionRepo := repository.NewSessionRepository(db)
	messageRepo := repository.NewMessageRepository(db)
	sessionService := NewSessionService(sessionRepo, messageRepo, repository.NewConfigRepository(db))
	messageService := NewMessageService(messageRepo, sessionRepo, nil, nil)

	if _, err := sessionService.GetByID(9_999_999_999, 9_999_999_999); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("missing session error = %v, want session not found", err)
	}
	if _, _, err := messageService.ListBySessionPaged(9_999_999_999, 9_999_999_999, 30, 0); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("missing message session error = %v, want session not found", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}
	if _, err := sessionService.GetByID(1, 1); err == nil || errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("closed database session error = %v, want internal failure", err)
	}
	if _, _, err := messageService.ListBySessionPaged(1, 1, 30, 0); err == nil || errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("closed database message error = %v, want internal failure", err)
	}
}
