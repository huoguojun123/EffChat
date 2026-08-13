package service

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
)

func TestMessageMutationDistinguishesMissingStateFromRepositoryFailure(t *testing.T) {
	t.Run("missing session", func(t *testing.T) {
		db := setupMessageTestDB(t)
		defer db.Close()
		svc := NewMessageService(repository.NewMessageRepository(db), repository.NewSessionRepository(db), repository.NewFileRepository(db), repository.NewAnswerAttemptRepository(db))

		if _, err := svc.ListForAgentContext(t.Context(), 9_999_999_999, 9_999_999_999); !errors.Is(err, ErrSessionNotFound) {
			t.Fatalf("agent history error = %v, want ErrSessionNotFound", err)
		}
		if _, err := svc.BuildUserMessagePreviewContext(t.Context(), 9_999_999_999, 9_999_999_999, &SendMessageRequest{Content: "fictional message"}); !errors.Is(err, ErrSessionNotFound) {
			t.Fatalf("message preview error = %v, want ErrSessionNotFound", err)
		}
		if _, _, err := svc.RetryAgentContext(t.Context(), 9_999_999_999, 9_999_999_999, 1); !errors.Is(err, ErrSessionNotFound) {
			t.Fatalf("retry error = %v, want ErrSessionNotFound", err)
		}
		if _, err := svc.UndoLastCompaction(9_999_999_999, 9_999_999_999); !errors.Is(err, ErrSessionNotFound) {
			t.Fatalf("undo error = %v, want ErrSessionNotFound", err)
		}
	})

	t.Run("missing checkpoint", func(t *testing.T) {
		db := setupMessageTestDB(t)
		defer db.Close()
		userRepo := repository.NewUserRepository(db)
		user := &model.User{Username: fmt.Sprintf("undo_empty_%d", time.Now().UnixNano()), PasswordHash: "x", Role: "user", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`)}
		if err := userRepo.Create(user); err != nil {
			t.Fatalf("create user: %v", err)
		}
		t.Cleanup(func() { _, _ = db.Exec("DELETE FROM users WHERE id = $1", user.ID) })
		sessionRepo := repository.NewSessionRepository(db)
		session := &model.Session{UserID: user.ID, Title: "empty undo", ModelID: "test-model", Provider: "test-provider", MessageFormat: "v1", Metadata: []byte(`{}`)}
		if err := sessionRepo.Create(session); err != nil {
			t.Fatalf("create session: %v", err)
		}
		svc := NewMessageService(repository.NewMessageRepository(db), sessionRepo, repository.NewFileRepository(db), repository.NewAnswerAttemptRepository(db))
		if _, err := svc.UndoLastCompaction(session.ID, user.ID); !errors.Is(err, ErrCompactionNotFound) {
			t.Fatalf("empty undo error = %v, want ErrCompactionNotFound", err)
		}
	})

	t.Run("closed database", func(t *testing.T) {
		db := setupMessageTestDB(t)
		svc := NewMessageService(repository.NewMessageRepository(db), repository.NewSessionRepository(db), repository.NewFileRepository(db), repository.NewAnswerAttemptRepository(db))
		if err := db.Close(); err != nil {
			t.Fatalf("close database: %v", err)
		}
		if _, err := svc.ListForAgentContext(t.Context(), 1, 1); err == nil || errors.Is(err, ErrSessionNotFound) {
			t.Fatalf("closed history error = %v, want internal failure", err)
		}
		if _, err := svc.BuildUserMessagePreviewContext(t.Context(), 1, 1, &SendMessageRequest{Content: "fictional message"}); err == nil || errors.Is(err, ErrSessionNotFound) {
			t.Fatalf("closed message preview error = %v, want internal failure", err)
		}
		if _, _, err := svc.RetryAgentContext(t.Context(), 1, 1, 1); err == nil || errors.Is(err, ErrSessionNotFound) {
			t.Fatalf("closed retry error = %v, want internal failure", err)
		}
		if _, err := svc.UndoLastCompaction(1, 1); err == nil || errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrCompactionNotFound) {
			t.Fatalf("closed undo error = %v, want internal failure", err)
		}
	})
}
