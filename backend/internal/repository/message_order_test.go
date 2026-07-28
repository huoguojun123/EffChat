package repository

import (
	"encoding/json"
	"testing"

	"github.com/huoguojun123/effchat/internal/model"
)

func TestMessageRepository_ListsMessagesByID(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	userID := createRepositoryTestUser(t, db, "message_order")
	session := &model.Session{
		UserID:        userID,
		Title:         "message order",
		ModelID:       "test-model",
		Provider:      "openai",
		MessageFormat: "v1",
		Metadata:      []byte(`{}`),
	}
	if err := NewSessionRepository(db).Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM users WHERE id = $1", userID) })

	repo := NewMessageRepository(db)
	messages := make([]*model.Message, 3)
	for i := range messages {
		data, err := json.Marshal(map[string]interface{}{
			"role":    "user",
			"content": string(rune('a' + i)),
		})
		if err != nil {
			t.Fatalf("marshal message %d: %v", i, err)
		}
		messages[i] = &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: data}
		if err := repo.Create(messages[i]); err != nil {
			t.Fatalf("create message %d: %v", i, err)
		}
	}

	for i, message := range messages {
		if _, err := db.Exec(
			"UPDATE messages SET created_at = $1::timestamptz WHERE id = $2",
			[]string{"2026-01-03T00:00:00Z", "2026-01-02T00:00:00Z", "2026-01-01T00:00:00Z"}[i],
			message.ID,
		); err != nil {
			t.Fatalf("set created_at %d: %v", i, err)
		}
	}

	assertIDs := func(name string, got []*model.Message, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(got) != len(messages) {
			t.Fatalf("%s len = %d, want %d", name, len(got), len(messages))
		}
		for i := range messages {
			if got[i].ID != messages[i].ID {
				t.Fatalf("%s ids[%d] = %d, want %d", name, i, got[i].ID, messages[i].ID)
			}
		}
	}

	listed, err := repo.ListBySession(session.ID)
	assertIDs("ListBySession", listed, err)
	all, err := repo.ListAllBySession(session.ID)
	assertIDs("ListAllBySession", all, err)
}
