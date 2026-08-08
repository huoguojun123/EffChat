package repository

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/huoguojun123/EffChat/internal/model"
)

func TestAnswerAttemptHistoricalSelectionAndDeletion(t *testing.T) {
	db := setupTestDB(t)
	t.Cleanup(func() { _ = db.Close() })
	userID := createRepositoryTestUser(t, db, "answer_attempt_history")
	session := &model.Session{UserID: userID, Title: "attempt history", ModelID: "m", Provider: "p", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := NewSessionRepository(db).Create(session); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM messages WHERE session_id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM answer_attempts WHERE session_id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", userID)
	})

	messageRepo := NewMessageRepository(db)
	firstUser := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"user","content":"first question"}`)}
	if err := messageRepo.Create(firstUser); err != nil {
		t.Fatal(err)
	}
	firstAttemptID := insertWindowAttempt(t, db, session.ID, firstUser.ID, 1, true)
	secondAttemptID := insertWindowAttempt(t, db, session.ID, firstUser.ID, 2, false)
	for attemptID, content := range map[int64]string{firstAttemptID: "first answer", secondAttemptID: "second answer"} {
		id := attemptID
		message := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"assistant","content":"` + content + `"}`), AnswerAttemptID: &id}
		if err := messageRepo.Create(message); err != nil {
			t.Fatal(err)
		}
	}

	latestUser := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"user","content":"later question"}`)}
	if err := messageRepo.Create(latestUser); err != nil {
		t.Fatal(err)
	}
	latestAttemptID := insertWindowAttempt(t, db, session.ID, latestUser.ID, 1, true)
	latestAnswer := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"assistant","content":"later answer"}`), AnswerAttemptID: &latestAttemptID}
	if err := messageRepo.Create(latestAnswer); err != nil {
		t.Fatal(err)
	}

	repo := NewAnswerAttemptRepository(db)
	navigation, err := repo.NavigationForAttemptIDs(context.Background(), []int64{firstAttemptID})
	if err != nil {
		t.Fatal(err)
	}
	if nav := navigation[firstAttemptID]; !nav.CanSwitch || nav.AttemptCount != 2 || nav.NextID == nil || *nav.NextID != secondAttemptID {
		t.Fatalf("historical navigation = %+v", nav)
	}

	selected, err := repo.SelectForActiveSession(context.Background(), session.ID, userID, secondAttemptID)
	if err != nil {
		t.Fatalf("select historical attempt: %v", err)
	}
	if !selected.Selected || !selected.SelectionChanged || selected.SelectionRevision != 1 {
		t.Fatalf("selected attempt = %+v", selected)
	}
	selectedMessages, err := messageRepo.ListBySessionContext(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if joined := joinedMessageData(selectedMessages); !strings.Contains(joined, "second answer") || strings.Contains(joined, "first answer") {
		t.Fatalf("selected historical context = %s", joined)
	}

	deletion, err := repo.DeleteForActiveSession(context.Background(), session.ID, userID, secondAttemptID)
	if err != nil {
		t.Fatalf("delete selected historical attempt: %v", err)
	}
	if !deletion.SelectionChanged || deletion.SelectedAttempt == nil || deletion.SelectedAttempt.ID != firstAttemptID || deletion.SelectionRevision != 2 {
		t.Fatalf("deletion = %+v", deletion)
	}
	var deletedAttempts, deletedMessages int
	if err := db.QueryRow("SELECT COUNT(*) FROM answer_attempts WHERE id = $1", secondAttemptID).Scan(&deletedAttempts); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM messages WHERE answer_attempt_id = $1", secondAttemptID).Scan(&deletedMessages); err != nil {
		t.Fatal(err)
	}
	if deletedAttempts != 0 || deletedMessages != 0 {
		t.Fatalf("deleted attempt rows=%d messages=%d", deletedAttempts, deletedMessages)
	}
	restoredMessages, err := messageRepo.ListBySessionContext(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if joined := joinedMessageData(restoredMessages); !strings.Contains(joined, "first answer") || strings.Contains(joined, "second answer") {
		t.Fatalf("replacement historical context = %s", joined)
	}
	if _, err := repo.DeleteForActiveSession(context.Background(), session.ID, userID, firstAttemptID); !errors.Is(err, ErrAnswerAttemptLastRemaining) {
		t.Fatalf("delete last attempt error = %v", err)
	}
}

func joinedMessageData(messages []*model.Message) string {
	var builder strings.Builder
	for _, message := range messages {
		if message != nil {
			builder.Write(message.MessageData)
		}
	}
	return builder.String()
}
