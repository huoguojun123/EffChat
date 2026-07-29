package repository

import (
	"fmt"
	"testing"

	"github.com/huoguojun123/EffChat/internal/model"
)

func TestConversationSearchRepositoryScopesVisibleContent(t *testing.T) {
	db := setupTestDB(t)
	t.Cleanup(func() { _ = db.Close() })
	ownerID := createRepositoryTestUser(t, db, "conversation_search_owner")
	otherID := createRepositoryTestUser(t, db, "conversation_search_other")
	folder := &model.SessionFolder{UserID: ownerID, Name: "Search folder"}
	if err := NewSessionFolderRepository(db).Create(folder); err != nil {
		t.Fatal(err)
	}

	sessionRepo := NewSessionRepository(db)
	messageRepo := NewMessageRepository(db)
	createSession := func(userID int64, title string, folderID *int64) *model.Session {
		t.Helper()
		session := &model.Session{UserID: userID, FolderID: folderID, Title: title, ModelID: "m", Provider: "p", MessageFormat: "v1", Metadata: []byte(`{}`)}
		if err := sessionRepo.Create(session); err != nil {
			t.Fatal(err)
		}
		return session
	}
	createMessage := func(sessionID int64, data string, attemptID *int64) *model.Message {
		t.Helper()
		message := &model.Message{SessionID: sessionID, SchemaVersion: "v1", MessageData: []byte(data), AnswerAttemptID: attemptID}
		if err := messageRepo.Create(message); err != nil {
			t.Fatal(err)
		}
		return message
	}

	folderSession := createSession(ownerID, "检索目标标题", &folder.ID)
	for i := 0; i < 4; i++ {
		createMessage(folderSession.ID, fmt.Sprintf(`{"role":"user","content":"检索目标额外命中 %d"}`, i), nil)
	}
	userTurn := createMessage(folderSession.ID, `{"role":"user","content":"用户正文包含检索目标"}`, nil)
	unselectedAttempt := insertWindowAttempt(t, db, folderSession.ID, userTurn.ID, 1, false)
	createMessage(folderSession.ID, `{"role":"assistant","content":"检索目标不应出现的旧回答"}`, &unselectedAttempt)
	selectedAttempt := insertWindowAttempt(t, db, folderSession.ID, userTurn.ID, 2, true)
	selectedAnswer := createMessage(folderSession.ID, `{"role":"assistant","content":"当前回答包含检索目标"}`, &selectedAttempt)
	createMessage(folderSession.ID, `{"role":"tool","content":"检索目标工具结果"}`, &selectedAttempt)
	createMessage(folderSession.ID, `{"role":"assistant","content":"检索目标压缩摘要","metadata":{"compaction_summary":true}}`, nil)

	unfiledSession := createSession(ownerID, "普通标题", nil)
	unfiledTurn := createMessage(unfiledSession.ID, `{"role":"user","content":"未分组检索目标"}`, nil)
	otherSession := createSession(otherID, "检索目标他人标题", nil)
	createMessage(otherSession.ID, `{"role":"user","content":"检索目标他人正文"}`, nil)
	deletedSession := createSession(ownerID, "检索目标已删除", nil)
	createMessage(deletedSession.ID, `{"role":"user","content":"检索目标已删除正文"}`, nil)
	if _, err := db.Exec(`UPDATE sessions SET deleted_at = NOW() WHERE id = $1`, deletedSession.ID); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM messages WHERE session_id IN ($1, $2, $3, $4)`, folderSession.ID, unfiledSession.ID, otherSession.ID, deletedSession.ID)
		_, _ = db.Exec(`DELETE FROM answer_attempts WHERE session_id IN ($1, $2, $3, $4)`, folderSession.ID, unfiledSession.ID, otherSession.ID, deletedSession.ID)
		_, _ = db.Exec(`DELETE FROM sessions WHERE id IN ($1, $2, $3, $4)`, folderSession.ID, unfiledSession.ID, otherSession.ID, deletedSession.ID)
		_, _ = db.Exec(`DELETE FROM session_folders WHERE id = $1`, folder.ID)
		_, _ = db.Exec(`DELETE FROM users WHERE id IN ($1, $2)`, ownerID, otherID)
	})

	repo := NewConversationSearchRepository(db)
	all, err := repo.Search(t.Context(), ownerID, "检索目标", ConversationSearchAll, nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	messageHits := 0
	foundTitle, foundSelectedAnswer, foundUnfiled := false, false, false
	for _, result := range all {
		if result.SessionID == otherSession.ID || result.SessionID == deletedSession.ID || result.Snippet == "检索目标不应出现的旧回答" || result.Snippet == "检索目标压缩摘要" || result.Snippet == "检索目标工具结果" {
			t.Fatalf("hidden content leaked into search: %+v", result)
		}
		if result.SessionID == folderSession.ID && result.Kind == "session" {
			foundTitle = true
		}
		if result.SessionID == folderSession.ID && result.Kind == "message" {
			messageHits++
			if result.MessageID != nil && *result.MessageID == selectedAnswer.ID && result.TurnID != nil && *result.TurnID == userTurn.ID {
				foundSelectedAnswer = true
			}
		}
		if result.SessionID == unfiledSession.ID && result.TurnID != nil && *result.TurnID == unfiledTurn.ID {
			foundUnfiled = true
		}
	}
	if !foundTitle || !foundSelectedAnswer || !foundUnfiled || messageHits != 3 {
		t.Fatalf("unexpected all-scope results: title=%v selected=%v unfiled=%v folder_message_hits=%d results=%+v", foundTitle, foundSelectedAnswer, foundUnfiled, messageHits, all)
	}
	for _, hiddenQuery := range []string{"旧回答", "工具结果", "压缩摘要"} {
		hidden, err := repo.Search(t.Context(), ownerID, hiddenQuery, ConversationSearchAll, nil, 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(hidden) != 0 {
			t.Fatalf("query %q exposed hidden content: %+v", hiddenQuery, hidden)
		}
	}

	folderResults, err := repo.Search(t.Context(), ownerID, "检索目标", ConversationSearchFolder, &folder.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range folderResults {
		if result.SessionID != folderSession.ID {
			t.Fatalf("folder scope leaked session: %+v", result)
		}
	}
	unfiledResults, err := repo.Search(t.Context(), ownerID, "检索目标", ConversationSearchUnfiled, nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(unfiledResults) != 1 || unfiledResults[0].SessionID != unfiledSession.ID {
		t.Fatalf("unexpected unfiled results: %+v", unfiledResults)
	}
}
