package repository

import (
	"context"
	"database/sql"
	"encoding/json"
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

func TestSessionMessageCursorTracksDurableMessages(t *testing.T) {
	db := setupTestDB(t)
	t.Cleanup(func() { _ = db.Close() })
	userID := createRepositoryTestUser(t, db, "message_cursor")
	session := &model.Session{UserID: userID, Title: "cursor", ModelID: "m", Provider: "p", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := NewSessionRepository(db).Create(session); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM messages WHERE session_id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", userID)
	})

	repo := NewMessageRepository(db)
	first := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"user","content":"first"}`)}
	if err := repo.CreateForActiveSession(context.Background(), session.ID, userID, first); err != nil {
		t.Fatal(err)
	}
	cursor, err := repo.GetSessionMessageCursor(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cursor.LatestMessageID != first.ID || cursor.UpdatedAt.IsZero() {
		t.Fatalf("cursor after first message = %+v", cursor)
	}

	second := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"assistant","content":"second"}`)}
	if err := repo.CreateForActiveSession(context.Background(), session.ID, userID, second); err != nil {
		t.Fatal(err)
	}
	updated, err := repo.GetSessionMessageCursor(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.LatestMessageID != second.ID || updated.UpdatedAt.Before(cursor.UpdatedAt) {
		t.Fatalf("cursor after second message = %+v, previous = %+v", updated, cursor)
	}
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

	preCheckpointTurns := make([]int64, 0, 3)
	for i := 1; i <= 3; i++ {
		turnID, _ := createTurn(fmt.Sprintf("old-%d", i))
		preCheckpointTurns = append(preCheckpointTurns, turnID)
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
	var summaryCompressionID sql.NullInt64
	if err := db.QueryRow("SELECT compression_summary_id FROM messages WHERE id = $1", summary.ID).Scan(&summaryCompressionID); err != nil {
		t.Fatal(err)
	}
	if summaryCompressionID.Valid {
		t.Fatalf("active checkpoint %d unexpectedly points to compression summary %d", summary.ID, summaryCompressionID.Int64)
	}

	immediate, err := repo.ListMessageWindow(session.ID, MessageWindowLatest, 0, 16)
	if err != nil {
		t.Fatal(err)
	}
	if immediate.FirstTurnID != preCheckpointTurns[0] || immediate.LastTurnID != preCheckpointTurns[2] || immediate.HasOlder || immediate.HasNewer {
		t.Fatalf("immediate checkpoint bounds = %+v", immediate)
	}
	for _, turnID := range preCheckpointTurns {
		if !windowContainsMessage(immediate, turnID) {
			t.Fatalf("immediate checkpoint window hid compressed turn %d", turnID)
		}
	}
	if !windowContainsMessage(immediate, summary.ID) {
		t.Fatalf("immediate checkpoint window omitted summary %d", summary.ID)
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
	if oldest.HasOlder || !oldest.HasNewer || oldest.FirstTurnID != preCheckpointTurns[0] || oldest.LastTurnID != postCheckpointTurns[0] {
		t.Fatalf("oldest post-checkpoint bounds = %+v", oldest)
	}
	for _, turnID := range preCheckpointTurns {
		if !windowContainsMessage(oldest, turnID) {
			t.Fatalf("oldest page hid compressed turn %d", turnID)
		}
	}
	if !windowContainsMessage(oldest, summary.ID) {
		t.Fatal("page containing the checkpoint anchor omitted the active checkpoint")
	}

	turns, total, hasMore, err := repo.ListConversationTurns(session.ID, 500, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 20 || hasMore || len(turns) != 20 || turns[0].ID != preCheckpointTurns[0] {
		t.Fatalf("post-checkpoint turn index = len:%d total:%d hasMore:%v first:%d", len(turns), total, hasMore, turns[0].ID)
	}
}

func TestMessageRepositorySupersedesLogicallyOlderCheckpoint(t *testing.T) {
	db := setupTestDB(t)
	t.Cleanup(func() { _ = db.Close() })
	userID := createRepositoryTestUser(t, db, "checkpoint_supersession")
	session := &model.Session{UserID: userID, Title: "checkpoint supersession", ModelID: "m", Provider: "p", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := NewSessionRepository(db).Create(session); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM messages WHERE session_id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", userID)
	})

	repo := NewMessageRepository(db)
	userMessages := make([]*model.Message, 0, 4)
	for i := 0; i < 4; i++ {
		message := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: fmt.Appendf(nil, `{"role":"user","content":"turn %d"}`, i+1)}
		if err := repo.Create(message); err != nil {
			t.Fatal(err)
		}
		userMessages = append(userMessages, message)
	}

	firstBoundary := userMessages[2].ID
	first := &model.Message{
		SessionID:     session.ID,
		SchemaVersion: "v1",
		MessageData: fmt.Appendf(nil,
			`{"role":"user","content":"first checkpoint","metadata":{"compaction_summary":true,"compaction_kind":"auto","compaction_before_message_id":%d}}`,
			firstBoundary,
		),
	}
	if err := repo.PersistCheckpoint(first, firstBoundary); err != nil {
		t.Fatal(err)
	}

	// The checkpoint is physically newer than the preserved tail but logically
	// belongs at its metadata boundary. A later checkpoint must supersede it by
	// that logical position even when the old physical id equals the new boundary.
	secondBoundary := first.ID
	second := &model.Message{
		SessionID:     session.ID,
		SchemaVersion: "v1",
		MessageData: fmt.Appendf(nil,
			`{"role":"user","content":"second checkpoint","metadata":{"compaction_summary":true,"compaction_kind":"manual","compaction_before_message_id":%d}}`,
			secondBoundary,
		),
	}
	if err := repo.PersistCheckpoint(second, secondBoundary); err != nil {
		t.Fatal(err)
	}

	var activeCount int
	var activeID int64
	if err := db.QueryRow(`
		SELECT COUNT(*), COALESCE(MAX(id), 0)
		FROM messages
		WHERE session_id = $1
		  AND deleted_at IS NULL
		  AND compressed_at IS NULL
		  AND COALESCE(message_data->'metadata'->>'compaction_summary', '') = 'true'
	`, session.ID).Scan(&activeCount, &activeID); err != nil {
		t.Fatal(err)
	}
	if activeCount != 1 || activeID != second.ID {
		t.Fatalf("active checkpoints = count:%d id:%d, want only %d", activeCount, activeID, second.ID)
	}

	var firstCompressedAt sql.NullTime
	var firstOwner sql.NullInt64
	if err := db.QueryRow("SELECT compressed_at, compression_summary_id FROM messages WHERE id = $1", first.ID).Scan(&firstCompressedAt, &firstOwner); err != nil {
		t.Fatal(err)
	}
	if !firstCompressedAt.Valid || !firstOwner.Valid || firstOwner.Int64 != second.ID {
		t.Fatalf("first checkpoint owner = compressed:%v summary:%v, want summary %d", firstCompressedAt.Valid, firstOwner, second.ID)
	}

	agentMessages, err := repo.ListBySessionContext(t.Context(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if containsMessageID(agentMessages, first.ID) || !containsMessageID(agentMessages, second.ID) {
		t.Fatalf("agent checkpoint projection contains first:%v second:%v", containsMessageID(agentMessages, first.ID), containsMessageID(agentMessages, second.ID))
	}
}

func TestMessageRepositoryWindowAnchorsNestedAndLegacyCheckpoints(t *testing.T) {
	db := setupTestDB(t)
	t.Cleanup(func() { _ = db.Close() })
	repo := NewMessageRepository(db)

	createSession := func(username string) (*model.Session, int64) {
		t.Helper()
		userID := createRepositoryTestUser(t, db, username)
		session := &model.Session{UserID: userID, Title: username, ModelID: "m", Provider: "p", MessageFormat: "v1", Metadata: []byte(`{}`)}
		if err := NewSessionRepository(db).Create(session); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = db.Exec("DELETE FROM messages WHERE session_id = $1", session.ID)
			_, _ = db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
			_, _ = db.Exec("DELETE FROM users WHERE id = $1", userID)
		})
		return session, userID
	}
	createUsers := func(sessionID int64, count int) []int64 {
		t.Helper()
		ids := make([]int64, 0, count)
		for i := 0; i < count; i++ {
			message := &model.Message{SessionID: sessionID, SchemaVersion: "v1", MessageData: fmt.Appendf(nil, `{"role":"user","content":"turn %d"}`, i+1)}
			if err := repo.Create(message); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, message.ID)
		}
		return ids
	}

	t.Run("metadata boundary anchors a summary-only checkpoint", func(t *testing.T) {
		session, _ := createSession("nested_checkpoint_anchor")
		turnIDs := createUsers(session.ID, 5)
		firstBoundary := turnIDs[len(turnIDs)-1] + 1
		first := &model.Message{
			SessionID:     session.ID,
			SchemaVersion: "v1",
			MessageData: fmt.Appendf(nil,
				`{"role":"user","content":"first checkpoint","metadata":{"compaction_summary":true,"compaction_kind":"auto","compaction_before_message_id":%d}}`,
				firstBoundary,
			),
		}
		if err := repo.PersistCheckpoint(first, firstBoundary); err != nil {
			t.Fatal(err)
		}

		secondBoundary := first.ID + 1
		second := &model.Message{
			SessionID:     session.ID,
			SchemaVersion: "v1",
			MessageData: fmt.Appendf(nil,
				`{"role":"user","content":"nested checkpoint","metadata":{"compaction_summary":true,"compaction_kind":"auto","compaction_before_message_id":%d}}`,
				secondBoundary,
			),
		}
		if err := repo.PersistCheckpoint(second, secondBoundary); err != nil {
			t.Fatal(err)
		}

		var directUserChildren int
		if err := db.QueryRow(`
			SELECT COUNT(*)
			FROM messages
			WHERE compression_summary_id = $1
			  AND role = 'user'
			  AND COALESCE(message_data->'metadata'->>'compaction_summary', '') <> 'true'
		`, second.ID).Scan(&directUserChildren); err != nil {
			t.Fatal(err)
		}
		if directUserChildren != 0 {
			t.Fatalf("nested checkpoint has %d direct ordinary user children, want 0", directUserChildren)
		}

		latest, err := repo.ListMessageWindow(session.ID, MessageWindowLatest, 0, 2)
		if err != nil {
			t.Fatal(err)
		}
		if !windowContainsMessage(latest, second.ID) || windowContainsMessage(latest, first.ID) || countCompactionSummaries(latest.Messages) != 1 {
			t.Fatalf("latest nested checkpoint projection = %+v", messageIDs(latest.Messages))
		}

		older, err := repo.ListMessageWindow(session.ID, MessageWindowBefore, latest.FirstTurnID, 2)
		if err != nil {
			t.Fatal(err)
		}
		if countCompactionSummaries(older.Messages) != 0 {
			t.Fatalf("checkpoint leaked into older page: %+v", messageIDs(older.Messages))
		}

		around, err := repo.ListMessageWindow(session.ID, MessageWindowAround, turnIDs[len(turnIDs)-1], 2)
		if err != nil {
			t.Fatal(err)
		}
		if !windowContainsMessage(around, second.ID) || countCompactionSummaries(around.Messages) != 1 {
			t.Fatalf("around anchor projection = %+v", messageIDs(around.Messages))
		}
	})

	t.Run("legacy checkpoint falls back to direct children", func(t *testing.T) {
		session, _ := createSession("legacy_checkpoint_anchor")
		turnIDs := createUsers(session.ID, 2)
		legacy := &model.Message{
			SessionID:     session.ID,
			SchemaVersion: "v1",
			MessageData:   []byte(`{"role":"user","content":"legacy checkpoint","metadata":{"compaction_summary":true,"compaction_kind":"manual"}}`),
		}
		if err := repo.PersistCheckpoint(legacy, turnIDs[len(turnIDs)-1]+1); err != nil {
			t.Fatal(err)
		}
		window, err := repo.ListMessageWindow(session.ID, MessageWindowLatest, 0, 16)
		if err != nil {
			t.Fatal(err)
		}
		if !windowContainsMessage(window, legacy.ID) || countCompactionSummaries(window.Messages) != 1 {
			t.Fatalf("legacy checkpoint projection = %+v", messageIDs(window.Messages))
		}
	})
}

func containsMessageID(messages []*model.Message, messageID int64) bool {
	for _, message := range messages {
		if message.ID == messageID {
			return true
		}
	}
	return false
}

func countCompactionSummaries(messages []*model.Message) int {
	count := 0
	for _, message := range messages {
		var data struct {
			Metadata struct {
				CompactionSummary bool `json:"compaction_summary"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(message.MessageData, &data); err == nil && data.Metadata.CompactionSummary {
			count++
		}
	}
	return count
}

func messageIDs(messages []*model.Message) []int64 {
	ids := make([]int64, 0, len(messages))
	for _, message := range messages {
		ids = append(ids, message.ID)
	}
	return ids
}

func windowContainsMessage(window *MessageWindow, messageID int64) bool {
	for _, message := range window.Messages {
		if message.ID == messageID {
			return true
		}
	}
	return false
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
