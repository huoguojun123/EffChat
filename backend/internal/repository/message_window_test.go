package repository

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/huoguojun123/EffChat/internal/model"
)

func TestMessageRepositoryConversationTurnsAndWindows(t *testing.T) {
	db := setupTestDB(t)
	t.Cleanup(func() { _ = db.Close() })
	userID := createRepositoryTestUser(t, db, "message_window")
	session := &model.Session{UserID: userID, Title: "window", ModelID: "m", Provider: "p", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := NewSessionRepository(db).Create(session); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM messages WHERE session_id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM answer_attempts WHERE session_id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", userID)
	})

	repo := NewMessageRepository(db)
	turnIDs := make([]int64, 0, 6)
	selectedAssistantIDs := make(map[int64]int64)
	for i := 1; i <= 6; i++ {
		user := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(fmt.Sprintf(`{"role":"user","content":"question %d"}`, i))}
		if err := repo.Create(user); err != nil {
			t.Fatal(err)
		}
		turnIDs = append(turnIDs, user.ID)

		if i == 3 {
			unselected := insertWindowAttempt(t, db, session.ID, user.ID, 1, false)
			message := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"assistant","content":"discarded"}`), AnswerAttemptID: &unselected}
			if err := repo.Create(message); err != nil {
				t.Fatal(err)
			}
		}
		attemptID := insertWindowAttempt(t, db, session.ID, user.ID, 2, true)
		toolCall := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"assistant","content":"","tool_calls":[{"id":"call","type":"function","function":{"name":"search","arguments":"{}"}}]}`), AnswerAttemptID: &attemptID}
		toolResult := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"tool","content":"result","tool_call_id":"call"}`), AnswerAttemptID: &attemptID}
		assistant := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(fmt.Sprintf(`{"role":"assistant","content":"answer %d"}`, i)), AnswerAttemptID: &attemptID}
		if err := repo.CreateBatch([]*model.Message{toolCall, toolResult, assistant}); err != nil {
			t.Fatal(err)
		}
		selectedAssistantIDs[user.ID] = assistant.ID
	}

	turns, total, hasMore, err := repo.ListConversationTurns(session.ID, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 6 || !hasMore || len(turns) != 2 || turns[0].ID != turnIDs[4] || turns[0].Sequence != 5 || turns[1].Sequence != 6 {
		t.Fatalf("latest turns = %+v total=%d hasMore=%v", turns, total, hasMore)
	}
	older, _, olderHasMore, err := repo.ListConversationTurns(session.ID, 2, turns[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !olderHasMore || len(older) != 2 || older[0].Sequence != 3 || older[1].Sequence != 4 {
		t.Fatalf("older turns = %+v hasMore=%v", older, olderHasMore)
	}

	assertWindow := func(mode MessageWindowMode, target int64, want []int64, hasOlder, hasNewer bool) {
		t.Helper()
		window, err := repo.ListMessageWindow(session.ID, mode, target, len(want))
		if err != nil {
			t.Fatal(err)
		}
		if window.FirstTurnID != want[0] || window.LastTurnID != want[len(want)-1] || window.HasOlder != hasOlder || window.HasNewer != hasNewer {
			t.Fatalf("window %s = %+v", mode, window)
		}
		for _, turnID := range want {
			foundUser, foundAnswer, foundTool := false, false, false
			for _, message := range window.Messages {
				if message.ID == turnID {
					foundUser = true
				}
				if message.ID == selectedAssistantIDs[turnID] {
					foundAnswer = true
				}
				if message.Role == "tool" && message.AnswerAttemptID != nil {
					foundTool = true
				}
				if string(message.MessageData) == `{"role":"assistant","content":"discarded"}` {
					t.Fatal("unselected answer attempt leaked into window")
				}
			}
			if !foundUser || !foundAnswer || !foundTool {
				t.Fatalf("turn %d incomplete: user=%v answer=%v tool=%v", turnID, foundUser, foundAnswer, foundTool)
			}
		}
	}

	assertWindow(MessageWindowLatest, 0, turnIDs[4:6], true, false)
	assertWindow(MessageWindowBefore, turnIDs[4], turnIDs[2:4], true, true)
	assertWindow(MessageWindowAfter, turnIDs[1], turnIDs[2:4], true, true)
	assertWindow(MessageWindowAround, turnIDs[0], turnIDs[:4], false, true)
	assertWindow(MessageWindowAround, turnIDs[5], turnIDs[2:], true, false)
}

func TestMessageRepositoryWindowUsesActiveCompactionCheckpoint(t *testing.T) {
	db := setupTestDB(t)
	t.Cleanup(func() { _ = db.Close() })
	userID := createRepositoryTestUser(t, db, "message_window_checkpoint")
	session := &model.Session{UserID: userID, Title: "checkpoint window", ModelID: "m", Provider: "p", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := NewSessionRepository(db).Create(session); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM messages WHERE session_id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM answer_attempts WHERE session_id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", userID)
	})

	repo := NewMessageRepository(db)
	createTurn := func(label string) (int64, int64) {
		t.Helper()
		user := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(fmt.Sprintf(`{"role":"user","content":"question %s"}`, label))}
		if err := repo.Create(user); err != nil {
			t.Fatal(err)
		}
		attemptID := insertWindowAttempt(t, db, session.ID, user.ID, 1, true)
		assistant := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(fmt.Sprintf(`{"role":"assistant","content":"answer %s"}`, label)), AnswerAttemptID: &attemptID}
		if err := repo.Create(assistant); err != nil {
			t.Fatal(err)
		}
		return user.ID, assistant.ID
	}

	for i := 1; i <= 3; i++ {
		createTurn(fmt.Sprintf("old-%d", i))
	}
	var beforeMessageID int64
	if err := db.QueryRow("SELECT COALESCE(MAX(id), 0) + 1 FROM messages WHERE session_id = $1", session.ID).Scan(&beforeMessageID); err != nil {
		t.Fatal(err)
	}
	summary := &model.Message{
		SessionID:     session.ID,
		SchemaVersion: "v1",
		MessageData: []byte(fmt.Sprintf(
			`{"role":"user","content":"checkpoint summary","metadata":{"compaction_summary":true,"compaction_kind":"manual","compaction_before_message_id":%d}}`,
			beforeMessageID,
		)),
	}
	if err := repo.PersistCheckpoint(summary, beforeMessageID); err != nil {
		t.Fatal(err)
	}

	immediate, err := repo.ListMessageWindow(session.ID, MessageWindowLatest, 0, 16)
	if err != nil {
		t.Fatal(err)
	}
	if len(immediate.Messages) != 1 || immediate.Messages[0].ID != summary.ID {
		t.Fatalf("immediate checkpoint window = %+v, want only summary %d", immediate.Messages, summary.ID)
	}
	if immediate.FirstTurnID != 0 || immediate.LastTurnID != 0 || immediate.HasOlder || immediate.HasNewer {
		t.Fatalf("immediate checkpoint bounds = %+v", immediate)
	}

	postCheckpointTurns := make([]int64, 0, 17)
	for i := 1; i <= 17; i++ {
		turnID, _ := createTurn(fmt.Sprintf("new-%d", i))
		postCheckpointTurns = append(postCheckpointTurns, turnID)
	}

	latest, err := repo.ListMessageWindow(session.ID, MessageWindowLatest, 0, 16)
	if err != nil {
		t.Fatal(err)
	}
	if !latest.HasOlder || latest.HasNewer || latest.FirstTurnID != postCheckpointTurns[1] || latest.LastTurnID != postCheckpointTurns[16] {
		t.Fatalf("latest post-checkpoint bounds = %+v", latest)
	}
	for _, message := range latest.Messages {
		if message.ID == summary.ID {
			t.Fatal("checkpoint leaked into a page that still has older uncompressed turns")
		}
	}

	oldest, err := repo.ListMessageWindow(session.ID, MessageWindowBefore, latest.FirstTurnID, 16)
	if err != nil {
		t.Fatal(err)
	}
	if oldest.HasOlder || !oldest.HasNewer || oldest.FirstTurnID != postCheckpointTurns[0] || oldest.LastTurnID != postCheckpointTurns[0] {
		t.Fatalf("oldest post-checkpoint bounds = %+v", oldest)
	}
	foundSummary := false
	for _, message := range oldest.Messages {
		if message.ID == summary.ID {
			foundSummary = true
		}
	}
	if !foundSummary {
		t.Fatal("oldest uncompressed page did not include the active checkpoint")
	}

	turns, total, hasMore, err := repo.ListConversationTurns(session.ID, 500, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 17 || hasMore || len(turns) != 17 || turns[0].ID != postCheckpointTurns[0] {
		t.Fatalf("post-checkpoint turn index = len:%d total:%d hasMore:%v first:%d", len(turns), total, hasMore, turns[0].ID)
	}
}

func insertWindowAttempt(t *testing.T, db interface {
	QueryRow(query string, args ...interface{}) *sql.Row
}, sessionID, userMessageID int64, attemptNumber int, selected bool) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`
		INSERT INTO answer_attempts (session_id, user_message_id, attempt_number, status, selected, completed_at)
		VALUES ($1, $2, $3, 'completed', $4, NOW()) RETURNING id
	`, sessionID, userMessageID, attemptNumber, selected).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
