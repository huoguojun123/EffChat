package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/huoguojun123/effchat/internal/model"
	"github.com/huoguojun123/effchat/internal/repository"
	"github.com/huoguojun123/effchat/internal/testutil"
)

func setupMessageTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return testutil.OpenPostgresTestDB(t)
}

func TestToInt64AcceptsOnlyPositiveWholeAttachmentIDs(t *testing.T) {
	cases := []struct {
		value interface{}
		want  int64
		ok    bool
	}{
		{value: float64(42), want: 42, ok: true},
		{value: "42", want: 42, ok: true},
		{value: json.Number("42"), want: 42, ok: true},
		{value: float64(42.5), ok: false},
		{value: "42.0", ok: false},
		{value: "+42", ok: false},
		{value: float64(0), ok: false},
	}
	for _, tc := range cases {
		got, ok := toInt64(tc.value)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("toInt64(%#v) = (%d, %t), want (%d, %t)", tc.value, got, ok, tc.want, tc.ok)
		}
	}
}

func TestBuildEditedRetryMessagePreservesSourceEnvelope(t *testing.T) {
	source := &model.Message{
		ID: 42, SessionID: 7, SchemaVersion: "v1", Role: "user",
		MessageData: []byte(`{
			"role":"user",
			"content":"before",
			"attachments":[{"file_id":9,"filename":"report.pdf","file_type":"application/pdf"}],
			"metadata":{"run_id":"old-run","thinking_effort":"high"}
		}`),
	}
	replacement, err := buildEditedRetryMessage(source, "after  ", "new-run")
	if err != nil {
		t.Fatal(err)
	}
	if replacement.ID != source.ID || replacement.SessionID != source.SessionID || replacement.SchemaVersion != source.SchemaVersion {
		t.Fatalf("replacement envelope = %+v", replacement)
	}
	data, err := repository.ParseMessageData(replacement.MessageData)
	if err != nil {
		t.Fatal(err)
	}
	if data["content"] != "after  " {
		t.Fatalf("replacement content = %#v", data["content"])
	}
	metadata, _ := data["metadata"].(map[string]interface{})
	if metadata["run_id"] != "new-run" || metadata["thinking_effort"] != "high" {
		t.Fatalf("replacement metadata = %#v", metadata)
	}
	attachments, err := attachmentIDsFromMessagePayload(data)
	if err != nil || len(attachments) != 1 || attachments[0] != 9 {
		t.Fatalf("replacement attachments = %v err=%v", attachments, err)
	}
}

func TestBuildEditedRetryMessageRejectsUnchangedOrEmptyContent(t *testing.T) {
	source := &model.Message{
		ID: 42, SessionID: 7, SchemaVersion: "v1", Role: "user",
		MessageData: []byte(`{"role":"user","content":"before"}`),
	}
	if _, err := buildEditedRetryMessage(source, "before", "new-run"); !errors.Is(err, ErrMessageUnchanged) {
		t.Fatalf("unchanged edit error = %v", err)
	}
	if _, err := buildEditedRetryMessage(source, "  ", "new-run"); !errors.Is(err, ErrInvalidMessageInput) {
		t.Fatalf("empty edit error = %v", err)
	}
}

func TestMessageService_ListForAgent_SkipsEphemeralErrorMessages(t *testing.T) {
	db := setupMessageTestDB(t)
	defer db.Close()

	userRepo := repository.NewUserRepository(db)
	user := &model.User{
		Username:     fmt.Sprintf("msgsvc_%d", time.Now().UnixNano()),
		PasswordHash: "x",
		Role:         "user",
		IsActive:     true,
		Permissions:  []byte(`{}`),
		Preferences:  []byte(`{}`),
	}
	if err := userRepo.Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { db.Exec("DELETE FROM users WHERE id = $1", user.ID) })

	sessionRepo := repository.NewSessionRepository(db)
	session := &model.Session{
		UserID:        user.ID,
		Title:         "消息测试",
		ModelID:       "gpt-4o",
		Provider:      "openai",
		MessageFormat: "v2",
		Metadata:      []byte(`{}`),
	}
	if err := sessionRepo.Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() {
		db.Exec("DELETE FROM messages WHERE session_id = $1", session.ID)
		db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
	})

	messageRepo := repository.NewMessageRepository(db)
	svc := NewMessageService(messageRepo, sessionRepo, repository.NewFileRepository(db), repository.NewAnswerAttemptRepository(db))

	if _, err := svc.CreateUserMessage(session.ID, user.ID, &SendMessageRequest{
		Content:       "成功的问题",
		SchemaVersion: "v2",
	}); err != nil {
		t.Fatalf("create first user message: %v", err)
	}
	assistantMsg, err := svc.CreateAssistantMessage(session.ID, user.ID, map[string]interface{}{
		"role":    "assistant",
		"content": "成功的回答",
	}, "v2")
	if err != nil {
		t.Fatalf("create first assistant message: %v", err)
	}

	failedUser, err := svc.CreateUserMessage(session.ID, user.ID, &SendMessageRequest{
		Content:       "失败的问题",
		SchemaVersion: "v2",
	})
	if err != nil {
		t.Fatalf("create failed user message: %v", err)
	}
	errorAssistant, err := svc.CreateErrorAssistantMessage(session.ID, user.ID, "请求失败：上游异常", "v2")
	if err != nil {
		t.Fatalf("create error assistant message: %v", err)
	}
	var failedAttemptID int64
	if err := db.QueryRow(`
		INSERT INTO answer_attempts (session_id, user_message_id, attempt_number, status, selected, completed_at)
		VALUES ($1, $2, 1, 'failed', true, NOW())
		RETURNING id
	`, session.ID, failedUser.ID).Scan(&failedAttemptID); err != nil {
		t.Fatalf("create selected failed attempt: %v", err)
	}
	if _, err := db.Exec(`UPDATE messages SET answer_attempt_id = $1 WHERE id = $2`, failedAttemptID, errorAssistant.ID); err != nil {
		t.Fatalf("bind error message to failed attempt: %v", err)
	}

	messages, err := svc.ListForAgent(session.ID, user.ID)
	if err != nil {
		t.Fatalf("ListForAgent: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("want 2 messages in agent context, got %d", len(messages))
	}
	if messages[0].Role != "user" {
		t.Fatalf("first message mismatch: id=%d role=%s", messages[0].ID, messages[0].Role)
	}
	if messages[1].ID != assistantMsg.ID || messages[1].Role != "assistant" {
		t.Fatalf("second message mismatch: id=%d role=%s", messages[1].ID, messages[1].Role)
	}
	if strings.Contains(string(messages[1].MessageData), "ephemeral_error") {
		t.Fatal("agent context should not include ephemeral error assistant messages")
	}

	retryMessages, err := svc.ListForRetryAgentContext(context.Background(), session.ID, user.ID, errorAssistant.ID)
	if err != nil {
		t.Fatalf("ListForRetryAgentContext on ephemeral error: %v", err)
	}
	if len(retryMessages) != 3 || retryMessages[2].ID != failedUser.ID {
		t.Fatalf("retry context ids = %#v, want successful turn plus failed user %d", messageIDs(retryMessages), failedUser.ID)
	}
}

func TestValidateSendMessageInputBounds(t *testing.T) {
	if err := validateSendMessageInput(&SendMessageRequest{Content: strings.Repeat("中", MaxMessageContentRunes+1)}); !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("rune limit error = %v", err)
	}
	if err := validateSendMessageInput(&SendMessageRequest{ClientRunID: strings.Repeat("x", MaxClientRunIDBytes+1)}); !errors.Is(err, ErrInvalidMessageInput) {
		t.Fatalf("run ID error = %v", err)
	}
	attachments := make([]int64, MaxMessageAttachments+1)
	if err := validateSendMessageInput(&SendMessageRequest{Content: "ok", Attachments: attachments}); !errors.Is(err, ErrTooManyAttachments) {
		t.Fatalf("attachment limit error = %v", err)
	}
}

func TestMessageService_PrepareRetry_AllowsLastUserMessage(t *testing.T) {
	db := setupMessageTestDB(t)
	defer db.Close()

	userRepo := repository.NewUserRepository(db)
	user := &model.User{
		Username:     fmt.Sprintf("retry_user_%d", time.Now().UnixNano()),
		PasswordHash: "x",
		Role:         "user",
		IsActive:     true,
		Permissions:  []byte(`{}`),
		Preferences:  []byte(`{}`),
	}
	if err := userRepo.Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { db.Exec("DELETE FROM users WHERE id = $1", user.ID) })

	sessionRepo := repository.NewSessionRepository(db)
	session := &model.Session{
		UserID:        user.ID,
		Title:         "重试测试",
		ModelID:       "gpt-4o",
		Provider:      "openai",
		MessageFormat: "v2",
		Metadata:      []byte(`{}`),
	}
	if err := sessionRepo.Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() {
		db.Exec("DELETE FROM messages WHERE session_id = $1", session.ID)
		db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
	})

	messageRepo := repository.NewMessageRepository(db)
	svc := NewMessageService(messageRepo, sessionRepo, repository.NewFileRepository(db), repository.NewAnswerAttemptRepository(db))

	firstUser, err := svc.CreateUserMessage(session.ID, user.ID, &SendMessageRequest{
		Content:       "第一问",
		SchemaVersion: "v2",
	})
	if err != nil {
		t.Fatalf("create first user message: %v", err)
	}
	if _, err := svc.CreateAssistantMessage(session.ID, user.ID, map[string]interface{}{
		"role":    "assistant",
		"content": "第一答",
	}, "v2"); err != nil {
		t.Fatalf("create first assistant message: %v", err)
	}
	lastUser, err := svc.CreateUserMessage(session.ID, user.ID, &SendMessageRequest{
		Content:       "最后一问",
		SchemaVersion: "v2",
	})
	if err != nil {
		t.Fatalf("create last user message: %v", err)
	}

	retryUser, err := svc.PrepareRetry(session.ID, user.ID, lastUser.ID)
	if err != nil {
		t.Fatalf("PrepareRetry on last user: %v", err)
	}
	if retryUser.ID != lastUser.ID {
		t.Fatalf("want retry user id %d, got %d", lastUser.ID, retryUser.ID)
	}

	messages, err := messageRepo.ListAllBySession(session.ID)
	if err != nil {
		t.Fatalf("ListAllBySession: %v", err)
	}

	visibleIDs := make([]int64, 0, len(messages))
	for _, msg := range messages {
		if msg.DeletedAt == nil {
			visibleIDs = append(visibleIDs, msg.ID)
		}
	}

	if len(visibleIDs) != 3 {
		t.Fatalf("want 3 visible messages after retry prepare, got %v", visibleIDs)
	}
	if visibleIDs[0] != firstUser.ID || visibleIDs[1] == lastUser.ID && visibleIDs[2] != lastUser.ID {
		t.Fatalf("unexpected visible order after retry prepare: %v", visibleIDs)
	}
}

func TestMessageService_PrepareRetryRejectsHistoricalTarget(t *testing.T) {
	db := setupMessageTestDB(t)
	defer db.Close()

	userRepo := repository.NewUserRepository(db)
	user := &model.User{Username: fmt.Sprintf("retry_stale_%d", time.Now().UnixNano()), PasswordHash: "x", Role: "user", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`)}
	if err := userRepo.Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM users WHERE id = $1", user.ID) })

	sessionRepo := repository.NewSessionRepository(db)
	session := &model.Session{UserID: user.ID, Title: "retry stale", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := sessionRepo.Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM messages WHERE session_id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
	})

	messageRepo := repository.NewMessageRepository(db)
	svc := NewMessageService(messageRepo, sessionRepo, repository.NewFileRepository(db), repository.NewAnswerAttemptRepository(db))
	firstUser, err := svc.CreateUserMessage(session.ID, user.ID, &SendMessageRequest{Content: "first", SchemaVersion: "v1"})
	if err != nil {
		t.Fatalf("create first user: %v", err)
	}
	if _, err := svc.CreateAssistantMessage(session.ID, user.ID, map[string]interface{}{"role": "assistant", "content": "first answer"}, "v1"); err != nil {
		t.Fatalf("create first assistant: %v", err)
	}
	if _, err := svc.CreateUserMessage(session.ID, user.ID, &SendMessageRequest{Content: "second", SchemaVersion: "v1"}); err != nil {
		t.Fatalf("create second user: %v", err)
	}
	if _, err := svc.PrepareRetry(session.ID, user.ID, firstUser.ID); !errors.Is(err, ErrRetryTargetStale) {
		t.Fatalf("PrepareRetry error = %v, want ErrRetryTargetStale", err)
	}
	messages, err := messageRepo.ListAllBySession(session.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("visible messages = %d, want 3", len(messages))
	}
}

func TestMessageServiceRetryAndCompactionContextsPreserveRetriedUserTurn(t *testing.T) {
	db := setupMessageTestDB(t)
	defer db.Close()

	userRepo := repository.NewUserRepository(db)
	user := &model.User{Username: fmt.Sprintf("retry_ctx_%d", time.Now().UnixNano()), PasswordHash: "x", Role: "user", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`)}
	if err := userRepo.Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM users WHERE id = $1", user.ID) })

	sessionRepo := repository.NewSessionRepository(db)
	session := &model.Session{UserID: user.ID, Title: "retry context", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := sessionRepo.Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM messages WHERE session_id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
	})

	svc := NewMessageService(repository.NewMessageRepository(db), sessionRepo, repository.NewFileRepository(db), repository.NewAnswerAttemptRepository(db))
	firstUser, err := svc.CreateUserMessage(session.ID, user.ID, &SendMessageRequest{Content: "first question", SchemaVersion: "v1"})
	if err != nil {
		t.Fatalf("create first user: %v", err)
	}
	firstAssistant, err := svc.CreateAssistantMessage(session.ID, user.ID, map[string]interface{}{"role": "assistant", "content": "first answer"}, "v1")
	if err != nil {
		t.Fatalf("create first assistant: %v", err)
	}
	secondUser, err := svc.CreateUserMessage(session.ID, user.ID, &SendMessageRequest{Content: "retry this", SchemaVersion: "v1"})
	if err != nil {
		t.Fatalf("create second user: %v", err)
	}
	secondAssistant, err := svc.CreateAssistantMessage(session.ID, user.ID, map[string]interface{}{"role": "assistant", "content": "old answer"}, "v1")
	if err != nil {
		t.Fatalf("create second assistant: %v", err)
	}

	retryContext, err := svc.ListForRetryAgentContext(context.Background(), session.ID, user.ID, secondAssistant.ID)
	if err != nil {
		t.Fatalf("list retry context: %v", err)
	}
	if len(retryContext) != 3 || retryContext[0].ID != firstUser.ID || retryContext[1].ID != firstAssistant.ID || retryContext[2].ID != secondUser.ID {
		t.Fatalf("retry context ids = %#v, want first turn + retried user", messageIDs(retryContext))
	}
	memoryContext := RecentConversationTextForMemoryMessages(retryContext, 5)
	if !strings.Contains(memoryContext, "retry this") || strings.Contains(memoryContext, "old answer") {
		t.Fatalf("protected retry memory context=%q", memoryContext)
	}

	compactionContext, err := svc.ListForCompactionBeforeMessageContext(context.Background(), session.ID, user.ID, secondUser.ID)
	if err != nil {
		t.Fatalf("list protected compaction context: %v", err)
	}
	if len(compactionContext) != 2 || compactionContext[0].ID != firstUser.ID || compactionContext[1].ID != firstAssistant.ID {
		t.Fatalf("compaction context ids = %#v, want only history before retried user", messageIDs(compactionContext))
	}
	memoryMessages, err := svc.ListForAgentThroughMessageContext(context.Background(), session.ID, user.ID, secondUser.ID)
	if err != nil {
		t.Fatalf("list protected memory context: %v", err)
	}
	if len(memoryMessages) != 3 || memoryMessages[0].ID != firstUser.ID || memoryMessages[1].ID != firstAssistant.ID || memoryMessages[2].ID != secondUser.ID {
		t.Fatalf("protected memory context ids = %#v, want earlier turn plus retried user", messageIDs(memoryMessages))
	}
}

func TestProtectedCompactionKeepsSummaryBeforeRetriedTurn(t *testing.T) {
	db := setupMessageTestDB(t)
	defer db.Close()

	userRepo := repository.NewUserRepository(db)
	user := &model.User{Username: fmt.Sprintf("protected_compaction_%d", time.Now().UnixNano()), PasswordHash: "x", Role: "user", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`)}
	if err := userRepo.Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM users WHERE id = $1", user.ID) })

	sessionRepo := repository.NewSessionRepository(db)
	session := &model.Session{UserID: user.ID, Title: "protected compaction", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := sessionRepo.Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM messages WHERE session_id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
	})

	messageRepo := repository.NewMessageRepository(db)
	svc := NewMessageService(messageRepo, sessionRepo, repository.NewFileRepository(db), repository.NewAnswerAttemptRepository(db))
	oldUser, err := svc.CreateUserMessage(session.ID, user.ID, &SendMessageRequest{Content: "old question", SchemaVersion: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	oldAssistant, err := svc.CreateAssistantMessage(session.ID, user.ID, map[string]interface{}{"role": "assistant", "content": "old answer"}, "v1")
	if err != nil {
		t.Fatal(err)
	}
	retryUser, err := svc.CreateUserMessage(session.ID, user.ID, &SendMessageRequest{Content: "retry question", SchemaVersion: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	retryAssistant, err := svc.CreateAssistantMessage(session.ID, user.ID, map[string]interface{}{"role": "assistant", "content": "discarded answer"}, "v1")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.PersistCompressionCheckpoint(session.ID, user.ID, []byte(`{"role":"user","content":"older context summary"}`), retryUser.ID, CompactionKindAuto); err != nil {
		t.Fatalf("persist protected checkpoint: %v", err)
	}

	agentMessages, err := svc.ListForAgent(session.ID, user.ID)
	if err != nil {
		t.Fatalf("list agent messages: %v", err)
	}
	if len(agentMessages) != 3 || !isCompactionSummaryMessage(agentMessages[0]) || agentMessages[1].ID != retryUser.ID || agentMessages[2].ID != retryAssistant.ID {
		t.Fatalf("agent order = %#v, want summary then protected turn", messageIDs(agentMessages))
	}
	retryMessages, err := svc.ListForRetryAgentContext(context.Background(), session.ID, user.ID, retryAssistant.ID)
	if err != nil {
		t.Fatalf("list retry after protected compaction: %v", err)
	}
	if len(retryMessages) != 2 || !isCompactionSummaryMessage(retryMessages[0]) || retryMessages[1].ID != retryUser.ID {
		t.Fatalf("retry order = %#v, want summary then retried user", messageIDs(retryMessages))
	}

	page, hasMore, err := messageRepo.ListBySessionPaged(session.ID, 5, 0)
	if err != nil {
		t.Fatalf("list paged messages: %v", err)
	}
	if hasMore || len(page) != 5 || page[0].ID != oldUser.ID || page[1].ID != oldAssistant.ID || !isCompactionSummaryMessage(page[2]) || page[3].ID != retryUser.ID || page[4].ID != retryAssistant.ID {
		t.Fatalf("paged order = %#v hasMore=%v", messageIDs(page), hasMore)
	}

	latestPage, hasMore, err := messageRepo.ListBySessionPaged(session.ID, 2, 0)
	if err != nil {
		t.Fatalf("list latest page: %v", err)
	}
	if !hasMore || len(latestPage) != 2 || latestPage[0].ID != retryUser.ID || latestPage[1].ID != retryAssistant.ID {
		t.Fatalf("latest page = %#v hasMore=%v", messageIDs(latestPage), hasMore)
	}
	middlePage, hasMore, err := messageRepo.ListBySessionPaged(session.ID, 2, latestPage[0].ID)
	if err != nil {
		t.Fatalf("list middle page: %v", err)
	}
	if !hasMore || len(middlePage) != 2 || middlePage[0].ID != oldAssistant.ID || !isCompactionSummaryMessage(middlePage[1]) {
		t.Fatalf("middle page = %#v hasMore=%v", messageIDs(middlePage), hasMore)
	}
	oldestPage, hasMore, err := messageRepo.ListBySessionPaged(session.ID, 2, middlePage[0].ID)
	if err != nil {
		t.Fatalf("list oldest page: %v", err)
	}
	if hasMore || len(oldestPage) != 1 || oldestPage[0].ID != oldUser.ID {
		t.Fatalf("oldest page = %#v hasMore=%v", messageIDs(oldestPage), hasMore)
	}
}

func messageIDs(messages []*model.Message) []int64 {
	ids := make([]int64, 0, len(messages))
	for _, message := range messages {
		if message != nil {
			ids = append(ids, message.ID)
		}
	}
	return ids
}

func TestMessageServiceRejectsWritesAfterSessionDeletion(t *testing.T) {
	db := setupMessageTestDB(t)
	defer db.Close()

	userRepo := repository.NewUserRepository(db)
	user := &model.User{Username: fmt.Sprintf("deleted_session_%d", time.Now().UnixNano()), PasswordHash: "x", Role: "user", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`)}
	if err := userRepo.Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM users WHERE id = $1", user.ID) })

	sessionRepo := repository.NewSessionRepository(db)
	session := &model.Session{UserID: user.ID, Title: "deleted", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := sessionRepo.Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := sessionRepo.Delete(session.ID, user.ID); err != nil {
		t.Fatalf("delete session: %v", err)
	}

	svc := NewMessageService(repository.NewMessageRepository(db), sessionRepo, repository.NewFileRepository(db), repository.NewAnswerAttemptRepository(db))
	if _, err := svc.CreateAssistantMessage(session.ID, user.ID, map[string]interface{}{"role": "assistant", "content": "late"}, "v1"); err == nil {
		t.Fatal("late assistant write was accepted")
	}
	if err := svc.PersistCompressionCheckpoint(session.ID, user.ID, []byte(`{"role":"user","content":"summary"}`), 0, CompactionKindManual); err == nil {
		t.Fatal("late checkpoint write was accepted")
	}
}

func TestMessageServiceTouchesSessionAfterSuccessfulWrite(t *testing.T) {
	db := setupMessageTestDB(t)
	defer db.Close()

	userRepo := repository.NewUserRepository(db)
	user := &model.User{Username: fmt.Sprintf("session_touch_%d", time.Now().UnixNano()), PasswordHash: "x", Role: "user", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`)}
	if err := userRepo.Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM users WHERE id = $1", user.ID) })
	sessionRepo := repository.NewSessionRepository(db)
	first := &model.Session{UserID: user.ID, Title: "first", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`)}
	second := &model.Session{UserID: user.ID, Title: "second", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := sessionRepo.Create(first); err != nil {
		t.Fatalf("create first session: %v", err)
	}
	if err := sessionRepo.Create(second); err != nil {
		t.Fatalf("create second session: %v", err)
	}
	if _, err := db.Exec("UPDATE sessions SET updated_at = NOW() - INTERVAL '1 hour' WHERE id = $1", first.ID); err != nil {
		t.Fatalf("age first session: %v", err)
	}
	svc := NewMessageService(repository.NewMessageRepository(db), sessionRepo, repository.NewFileRepository(db), repository.NewAnswerAttemptRepository(db))
	if _, err := svc.CreateUserMessage(first.ID, user.ID, &SendMessageRequest{Content: "touch", SchemaVersion: "v1"}); err != nil {
		t.Fatalf("create user message: %v", err)
	}
	listed, err := sessionRepo.ListByUser(user.ID, 10, 0, nil, false)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(listed) != 2 || listed[0].ID != first.ID {
		t.Fatalf("message session was not moved to the top: %+v", listed)
	}
}

func TestMessageService_ListForAgent_ListsTextAttachmentsWithoutInjectingFullText(t *testing.T) {
	db := setupMessageTestDB(t)
	defer db.Close()

	userRepo := repository.NewUserRepository(db)
	user := &model.User{
		Username:     fmt.Sprintf("att_%d", time.Now().UnixNano()),
		PasswordHash: "x", Role: "user", IsActive: true,
		Permissions: []byte(`{}`), Preferences: []byte(`{}`),
	}
	if err := userRepo.Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { db.Exec("DELETE FROM users WHERE id = $1", user.ID) })

	sessionRepo := repository.NewSessionRepository(db)
	session := &model.Session{
		UserID: user.ID, Title: "附件测试", ModelID: "gpt-4o",
		Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`),
	}
	if err := sessionRepo.Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() {
		db.Exec("DELETE FROM messages WHERE session_id = $1", session.ID)
		db.Exec("DELETE FROM files WHERE user_id = $1", user.ID)
		db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
	})

	fileRepo := repository.NewFileRepository(db)
	extractedPath := fmt.Sprintf("/tmp/spec_%d.md.txt", time.Now().UnixNano())
	f := &model.File{
		UserID: user.ID, SessionID: &session.ID, FileName: "spec.md", FilePath: fmt.Sprintf("/tmp/spec_%d.md", time.Now().UnixNano()),
		FileType: "text/markdown", FileSize: 42,
		ExtractedTextPath: &extractedPath, ExtractStatus: "ready", TokenEstimate: 5,
	}
	if err := fileRepo.Create(f); err != nil {
		t.Fatalf("create file: %v", err)
	}

	messageRepo := repository.NewMessageRepository(db)
	svc := NewMessageService(messageRepo, sessionRepo, fileRepo, repository.NewAnswerAttemptRepository(db))

	if _, err := svc.CreateUserMessage(session.ID, user.ID, &SendMessageRequest{
		Content:       "看下这个文件",
		SchemaVersion: "v1",
		Attachments:   []int64{f.ID},
	}); err != nil {
		t.Fatalf("create user message: %v", err)
	}

	msgs, err := svc.ListForAgent(session.ID, user.ID)
	if err != nil {
		t.Fatalf("list for agent: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want 1 message, got %d", len(msgs))
	}
	content := string(msgs[0].MessageData)
	if strings.Contains(content, "PROJECT GOAL: ship it") {
		t.Errorf("extracted text must not be injected into agent history, got: %s", content)
	}
	if !strings.Contains(content, "file_read") {
		t.Errorf("expected file_read hint, got: %s", content)
	}
	if !strings.Contains(content, "file_id") || !strings.Contains(content, `filename=\"spec.md\"`) {
		t.Errorf("expected attachment list with file_id and filename, got: %s", content)
	}
	if !strings.Contains(content, "看下这个文件") {
		t.Errorf("expected original content preserved, got: %s", content)
	}

	// 库里存的原始消息 content 必须保持干净：既不含文件正文，也不含运行期 file_read 提示。
	raw, err := messageRepo.ListBySession(session.ID)
	if err != nil {
		t.Fatalf("list raw: %v", err)
	}
	if strings.Contains(string(raw[0].MessageData), "PROJECT GOAL") {
		t.Errorf("DB-stored message must not contain injected text, got: %s", string(raw[0].MessageData))
	}
	if strings.Contains(string(raw[0].MessageData), "file_read") {
		t.Errorf("DB-stored message must not contain runtime file_read hint, got: %s", string(raw[0].MessageData))
	}
}

func TestMessageService_ListForAgentWithDraft_DoesNotDuplicateAttachmentList(t *testing.T) {
	db := setupMessageTestDB(t)
	defer db.Close()

	userRepo := repository.NewUserRepository(db)
	user := &model.User{
		Username:     fmt.Sprintf("att_draft_%d", time.Now().UnixNano()),
		PasswordHash: "x", Role: "user", IsActive: true,
		Permissions: []byte(`{}`), Preferences: []byte(`{}`),
	}
	if err := userRepo.Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { db.Exec("DELETE FROM users WHERE id = $1", user.ID) })

	sessionRepo := repository.NewSessionRepository(db)
	session := &model.Session{
		UserID: user.ID, Title: "附件预览测试", ModelID: "gpt-4o",
		Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`),
	}
	if err := sessionRepo.Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() {
		db.Exec("DELETE FROM messages WHERE session_id = $1", session.ID)
		db.Exec("DELETE FROM files WHERE user_id = $1", user.ID)
		db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
	})

	fileRepo := repository.NewFileRepository(db)
	file := &model.File{
		UserID: user.ID, SessionID: &session.ID, FileName: "notes.md", FilePath: fmt.Sprintf("/tmp/notes_%d.md", time.Now().UnixNano()),
		FileType: "text/markdown", FileSize: 42, ExtractStatus: "ready", TokenEstimate: 5,
	}
	if err := fileRepo.Create(file); err != nil {
		t.Fatalf("create file: %v", err)
	}

	messageRepo := repository.NewMessageRepository(db)
	svc := NewMessageService(messageRepo, sessionRepo, fileRepo, repository.NewAnswerAttemptRepository(db))
	if _, err := svc.CreateUserMessage(session.ID, user.ID, &SendMessageRequest{
		Content:       "先看这个文件",
		SchemaVersion: "v1",
		Attachments:   []int64{file.ID},
	}); err != nil {
		t.Fatalf("create user message: %v", err)
	}
	draftFile := &model.File{
		UserID: user.ID, SessionID: &session.ID, FileName: "draft.md", FilePath: fmt.Sprintf("/tmp/draft_%d.md", time.Now().UnixNano()),
		FileType: "text/markdown", FileSize: 24, ExtractStatus: "ready", TokenEstimate: 3,
	}
	if err := fileRepo.Create(draftFile); err != nil {
		t.Fatalf("create draft file: %v", err)
	}

	msgs, err := svc.ListForAgentWithDraft(session.ID, user.ID, &SendMessageRequest{
		Content:       "这是发送前预览",
		SchemaVersion: "v1",
		Attachments:   []int64{draftFile.ID},
	})
	if err != nil {
		t.Fatalf("list with draft: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("want 2 messages, got %d", len(msgs))
	}
	if got := strings.Count(string(msgs[0].MessageData), "[Attachment list]"); got != 1 {
		t.Fatalf("history attachment note count = %d, want 1:\n%s", got, string(msgs[0].MessageData))
	}
	draftData := string(msgs[1].MessageData)
	if !strings.Contains(draftData, fmt.Sprintf("file_id=%d", draftFile.ID)) || !strings.Contains(draftData, "status=ready") {
		t.Fatalf("draft should include its staged attachment metadata:\n%s", draftData)
	}
	if strings.Contains(draftData, "status=unavailable") {
		t.Fatalf("staged draft attachment was incorrectly treated as deleted:\n%s", draftData)
	}
}

// 图片附件不进 content，而是被记进 _image_parts（含 file_path/file_type），供 agent 层转多模态。
func TestMessageService_ListForAgent_InjectsImageParts(t *testing.T) {
	db := setupMessageTestDB(t)
	defer db.Close()

	userRepo := repository.NewUserRepository(db)
	user := &model.User{
		Username:     fmt.Sprintf("img_%d", time.Now().UnixNano()),
		PasswordHash: "x", Role: "user", IsActive: true,
		Permissions: []byte(`{}`), Preferences: []byte(`{}`),
	}
	if err := userRepo.Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { db.Exec("DELETE FROM users WHERE id = $1", user.ID) })

	sessionRepo := repository.NewSessionRepository(db)
	session := &model.Session{
		UserID: user.ID, Title: "图片测试", ModelID: "gpt-4o",
		Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`),
	}
	if err := sessionRepo.Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() {
		db.Exec("DELETE FROM messages WHERE session_id = $1", session.ID)
		db.Exec("DELETE FROM files WHERE user_id = $1", user.ID)
		db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
	})

	imgPath := fmt.Sprintf("/tmp/pic_%d.png", time.Now().UnixNano())
	fileRepo := repository.NewFileRepository(db)
	f := &model.File{
		UserID: user.ID, SessionID: &session.ID, FileName: "pic.png", FilePath: imgPath,
		FileType: "image/png", FileSize: 100, ExtractStatus: "ready",
	}
	if err := fileRepo.Create(f); err != nil {
		t.Fatalf("create file: %v", err)
	}

	messageRepo := repository.NewMessageRepository(db)
	svc := NewMessageService(messageRepo, sessionRepo, fileRepo, repository.NewAnswerAttemptRepository(db))

	if _, err := svc.CreateUserMessage(session.ID, user.ID, &SendMessageRequest{
		Content:       "看这张图",
		SchemaVersion: "v1",
		Attachments:   []int64{f.ID},
	}); err != nil {
		t.Fatalf("create user message: %v", err)
	}

	msgs, err := svc.ListForAgent(session.ID, user.ID)
	if err != nil {
		t.Fatalf("list for agent: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want 1 message, got %d", len(msgs))
	}
	var data map[string]interface{}
	if err := json.Unmarshal(msgs[0].MessageData, &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	parts, ok := data["_image_parts"].([]interface{})
	if !ok || len(parts) != 1 {
		t.Fatalf("_image_parts = %v, want 1 entry", data["_image_parts"])
	}
	part := parts[0].(map[string]interface{})
	if part["file_path"] != imgPath || part["file_type"] != "image/png" {
		t.Errorf("image part = %v, want path=%s type=image/png", part, imgPath)
	}
	// content 不应被图片污染（仍是用户原文，不含 <attachment> 块）
	if c, _ := data["content"].(string); c != "看这张图" {
		t.Errorf("content = %q, want 看这张图 (image must not touch content)", c)
	}
}

// TestMessageService_CreateUserMessage_RejectsEmptyAndInvalidAttachments 验证归一化层契约：
// 无文字且无有效附件（包括 file id 全部越权/不存在被跳过的情况）必须被拒，不落空 turn。
func TestMessageService_CreateUserMessage_RejectsEmptyAndInvalidAttachments(t *testing.T) {
	db := setupMessageTestDB(t)
	defer db.Close()

	userRepo := repository.NewUserRepository(db)
	user := &model.User{
		Username:     fmt.Sprintf("att_guard_%d", time.Now().UnixNano()),
		PasswordHash: "x", Role: "user", IsActive: true,
		Permissions: []byte(`{}`), Preferences: []byte(`{}`),
	}
	if err := userRepo.Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { db.Exec("DELETE FROM users WHERE id = $1", user.ID) })

	sessionRepo := repository.NewSessionRepository(db)
	session := &model.Session{
		UserID: user.ID, Title: "契约测试", ModelID: "gpt-4o",
		Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`),
	}
	if err := sessionRepo.Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() {
		db.Exec("DELETE FROM messages WHERE session_id = $1", session.ID)
		db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
	})

	messageRepo := repository.NewMessageRepository(db)
	svc := NewMessageService(messageRepo, sessionRepo, repository.NewFileRepository(db), repository.NewAnswerAttemptRepository(db))

	// 无文字 + 无附件 → 拒。
	if _, err := svc.CreateUserMessage(session.ID, user.ID, &SendMessageRequest{
		Content: "  ", SchemaVersion: "v1",
	}); err == nil {
		t.Error("empty content + no attachments should be rejected")
	}

	// 无文字 + 全是不存在的 file id（会被跳过 → 有效附件 0）→ 拒。
	if _, err := svc.CreateUserMessage(session.ID, user.ID, &SendMessageRequest{
		Content: "", SchemaVersion: "v1", Attachments: []int64{99999998, 99999999},
	}); err == nil {
		t.Error("empty content + all-invalid attachments should be rejected (no empty turn)")
	}

	// 有文字、无附件 → 放行。
	if _, err := svc.CreateUserMessage(session.ID, user.ID, &SendMessageRequest{
		Content: "正常一句话", SchemaVersion: "v1",
	}); err != nil {
		t.Errorf("text-only message should be allowed, got %v", err)
	}
}

func TestMessageService_CreateUserMessageRejectsCrossSessionAttachment(t *testing.T) {
	db := setupMessageTestDB(t)
	defer db.Close()
	user := &model.User{Username: fmt.Sprintf("cross_session_file_%d", time.Now().UnixNano()), PasswordHash: "x", Role: "user", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`)}
	if err := repository.NewUserRepository(db).Create(user); err != nil {
		t.Fatal(err)
	}
	sessions := repository.NewSessionRepository(db)
	first := &model.Session{UserID: user.ID, Title: "first", ModelID: "gpt-4o-mini", Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`)}
	second := &model.Session{UserID: user.ID, Title: "second", ModelID: "gpt-4o-mini", Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := sessions.Create(first); err != nil {
		t.Fatal(err)
	}
	if err := sessions.Create(second); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM files WHERE user_id = $1", user.ID)
		_, _ = db.Exec("DELETE FROM sessions WHERE user_id = $1", user.ID)
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", user.ID)
	})
	file := &model.File{UserID: user.ID, SessionID: &first.ID, FileName: "first.txt", FilePath: fmt.Sprintf("./storage/attachments/extracted/%d/first.txt", user.ID), FileType: "text/plain", FileSize: 1, ExtractStatus: "ready"}
	files := repository.NewFileRepository(db)
	if err := files.Create(file); err != nil {
		t.Fatal(err)
	}
	service := NewMessageService(repository.NewMessageRepository(db), sessions, files, repository.NewAnswerAttemptRepository(db))
	if _, err := service.CreateUserMessage(second.ID, user.ID, &SendMessageRequest{Content: "attach", SchemaVersion: "v1", Attachments: []int64{file.ID}}); err == nil || !strings.Contains(err.Error(), "not available in this conversation") {
		t.Fatalf("cross-session attachment should be rejected, err=%v", err)
	}
}

func TestMessageService_CreateUserMessage_RejectsUnreadyDocumentAttachments(t *testing.T) {
	db := setupMessageTestDB(t)
	defer db.Close()

	user := &model.User{
		Username:     fmt.Sprintf("ocr_attachment_guard_%d", time.Now().UnixNano()),
		PasswordHash: "x", Role: "user", IsActive: true,
		Permissions: []byte(`{}`), Preferences: []byte(`{}`),
	}
	if err := repository.NewUserRepository(db).Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM users WHERE id = $1", user.ID) })

	session := &model.Session{
		UserID: user.ID, Title: "OCR attachment guard", ModelID: "gpt-4o",
		Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`),
	}
	sessionRepo := repository.NewSessionRepository(db)
	if err := sessionRepo.Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM messages WHERE session_id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM files WHERE user_id = $1", user.ID)
		_, _ = db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
	})

	fileRepo := repository.NewFileRepository(db)
	provider := "mineru"
	for _, status := range []string{"ocr_pending", "ocr_running", "failed"} {
		file := &model.File{
			UserID: user.ID, SessionID: &session.ID, FileName: status + ".pdf", FilePath: fmt.Sprintf("/tmp/%s_%d.txt", status, time.Now().UnixNano()),
			FileType: "application/pdf", FileSize: 64, ExtractStatus: status, OCRProvider: &provider,
		}
		if err := fileRepo.Create(file); err != nil {
			t.Fatalf("create %s file: %v", status, err)
		}
		service := NewMessageService(repository.NewMessageRepository(db), sessionRepo, fileRepo, repository.NewAnswerAttemptRepository(db))
		if _, err := service.CreateUserMessage(session.ID, user.ID, &SendMessageRequest{Content: "请阅读", SchemaVersion: "v1", Attachments: []int64{file.ID}}); err == nil || !strings.Contains(err.Error(), "仍在解析") {
			t.Fatalf("%s attachment must be rejected, err=%v", status, err)
		}
	}

	image := &model.File{
		UserID: user.ID, SessionID: &session.ID, FileName: "photo.png", FilePath: fmt.Sprintf("/tmp/photo_%d.png", time.Now().UnixNano()),
		FileType: "image/png", FileSize: 64, ExtractStatus: "pending",
	}
	if err := fileRepo.Create(image); err != nil {
		t.Fatalf("create image file: %v", err)
	}
	service := NewMessageService(repository.NewMessageRepository(db), sessionRepo, fileRepo, repository.NewAnswerAttemptRepository(db))
	if _, err := service.CreateUserMessage(session.ID, user.ID, &SendMessageRequest{Content: "请看图片", SchemaVersion: "v1", Attachments: []int64{image.ID}}); err != nil {
		t.Fatalf("image attachment should remain allowed: %v", err)
	}
}

func TestMessageService_RunIDIdempotency(t *testing.T) {
	db := setupMessageTestDB(t)
	defer db.Close()

	userRepo := repository.NewUserRepository(db)
	user := &model.User{
		Username:     fmt.Sprintf("run_id_user_%d", time.Now().UnixNano()),
		PasswordHash: "x",
		Role:         "user",
		IsActive:     true,
		Permissions:  []byte(`{}`),
		Preferences:  []byte(`{}`),
	}
	if err := userRepo.Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { db.Exec("DELETE FROM users WHERE id = $1", user.ID) })

	sessionRepo := repository.NewSessionRepository(db)
	session := &model.Session{
		UserID:        user.ID,
		Title:         "run id test",
		ModelID:       "gpt-4o",
		Provider:      "openai",
		MessageFormat: "v2",
		Metadata:      []byte(`{}`),
	}
	if err := sessionRepo.Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() {
		db.Exec("DELETE FROM messages WHERE session_id = $1", session.ID)
		db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
	})

	messageRepo := repository.NewMessageRepository(db)
	svc := NewMessageService(messageRepo, sessionRepo, repository.NewFileRepository(db), repository.NewAnswerAttemptRepository(db))
	req := &SendMessageRequest{Content: "hello", SchemaVersion: "v2", ClientRunID: "run-idem-1"}
	quotaRepo := repository.NewQuotaRepository(db)
	if _, err := quotaRepo.ReserveChatRun(context.Background(), repository.ChatRunReservationInput{
		UserID: user.ID, AuthVersion: user.AuthVersion, SessionID: session.ID, RunID: req.ClientRunID, Kind: RunKindChat,
		ExpiresAt: time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatalf("reserve chat run: %v", err)
	}

	firstUser, err := svc.CreateUserMessage(session.ID, user.ID, req)
	if err != nil {
		t.Fatalf("create first user: %v", err)
	}
	secondUser, err := svc.CreateUserMessage(session.ID, user.ID, req)
	if err != nil {
		t.Fatalf("create second user: %v", err)
	}
	if secondUser.ID != firstUser.ID {
		t.Fatalf("duplicate user message id = %d, want %d", secondUser.ID, firstUser.ID)
	}
	if bound, err := quotaRepo.BindChatRunUserMessage(context.Background(), req.ClientRunID, firstUser.ID); err != nil || !bound {
		t.Fatalf("bind chat run user message = %v, %v", bound, err)
	}

	produced := []map[string]interface{}{
		{"role": "assistant", "content": "calling", "tool_calls": []interface{}{map[string]interface{}{"id": "call-1", "type": "function", "function": map[string]interface{}{"name": "web_search", "arguments": "{}"}}}},
		{"role": "tool", "tool_call_id": "call-1", "content": `{"ok":true}`},
		{"role": "assistant", "content": "final"},
	}
	firstBatch, err := svc.PersistAgentMessages(session.ID, user.ID, produced, "v2", "run-idem-1")
	if err != nil {
		t.Fatalf("persist first batch: %v", err)
	}
	secondBatch, err := svc.PersistAgentMessages(session.ID, user.ID, produced, "v2", "run-idem-1")
	if err != nil {
		t.Fatalf("persist second batch: %v", err)
	}
	if len(secondBatch) != len(firstBatch) {
		t.Fatalf("second batch len = %d, want %d", len(secondBatch), len(firstBatch))
	}
	if secondBatch[0].ID != firstBatch[0].ID {
		t.Fatalf("duplicate assistant id = %d, want existing %d", secondBatch[0].ID, firstBatch[0].ID)
	}

	all, err := messageRepo.FindByRunID(session.ID, "run-idem-1", nil)
	if err != nil {
		t.Fatalf("FindByRunID: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("messages for run = %d, want 4 (1 user + 3 produced)", len(all))
	}
	var data map[string]interface{}
	if err := json.Unmarshal(firstBatch[0].MessageData, &data); err != nil {
		t.Fatalf("unmarshal persisted assistant: %v", err)
	}
	meta, ok := data["metadata"].(map[string]interface{})
	if !ok || meta["run_id"] != "run-idem-1" {
		t.Fatalf("metadata.run_id missing: %#v", data["metadata"])
	}
	if meta["run_sequence"] != float64(0) {
		t.Fatalf("run_sequence = %#v", meta["run_sequence"])
	}
}

func TestMessageServiceUndoRejectsAutomaticCompaction(t *testing.T) {
	db := setupMessageTestDB(t)
	defer db.Close()

	user := &model.User{
		Username:     fmt.Sprintf("undo_auto_%d", time.Now().UnixNano()),
		PasswordHash: "x",
		Role:         "user",
		IsActive:     true,
		Permissions:  []byte(`{}`),
		Preferences:  []byte(`{}`),
	}
	if err := repository.NewUserRepository(db).Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM users WHERE id = $1", user.ID) })

	sessionRepo := repository.NewSessionRepository(db)
	session := &model.Session{
		UserID: user.ID, Title: "automatic compaction", ModelID: "gpt-4o",
		Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`),
	}
	if err := sessionRepo.Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}

	messageRepo := repository.NewMessageRepository(db)
	svc := NewMessageService(messageRepo, sessionRepo, repository.NewFileRepository(db), repository.NewAnswerAttemptRepository(db))
	userMessage, err := svc.CreateUserMessage(session.ID, user.ID, &SendMessageRequest{Content: "A fictional test message", SchemaVersion: "v1"})
	if err != nil {
		t.Fatalf("create user message: %v", err)
	}
	summary := []byte(`{"role":"user","content":"Automatic summary"}`)
	if err := svc.PersistCompressionCheckpoint(session.ID, user.ID, summary, userMessage.ID+1, CompactionKindAuto); err != nil {
		t.Fatalf("persist automatic checkpoint: %v", err)
	}

	if _, err := svc.UndoLastCompaction(session.ID, user.ID); err == nil || !strings.Contains(err.Error(), "latest manual compaction") {
		t.Fatalf("undo automatic compaction error = %v", err)
	}
	messages, err := messageRepo.ListAllBySession(session.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) != 2 || messages[0].CompressedAt == nil || !isCompactionSummaryMessage(messages[1]) {
		t.Fatalf("automatic checkpoint changed after rejected undo: %+v", messages)
	}
}

func TestMessageServiceUndoManualCompactionPreservesEarlierCheckpoint(t *testing.T) {
	db := setupMessageTestDB(t)
	defer db.Close()

	user := &model.User{
		Username:     fmt.Sprintf("undo_nested_%d", time.Now().UnixNano()),
		PasswordHash: "x",
		Role:         "user",
		IsActive:     true,
		Permissions:  []byte(`{}`),
		Preferences:  []byte(`{}`),
	}
	if err := repository.NewUserRepository(db).Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM users WHERE id = $1", user.ID) })

	sessionRepo := repository.NewSessionRepository(db)
	session := &model.Session{UserID: user.ID, Title: "nested undo", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := sessionRepo.Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	service := NewMessageService(repository.NewMessageRepository(db), sessionRepo, repository.NewFileRepository(db), repository.NewAnswerAttemptRepository(db))

	firstUser, err := service.CreateUserMessage(session.ID, user.ID, &SendMessageRequest{Content: "old user", SchemaVersion: "v1"})
	if err != nil {
		t.Fatalf("create first user: %v", err)
	}
	firstAssistant, err := service.CreateAssistantMessage(session.ID, user.ID, map[string]interface{}{"role": "assistant", "content": "old assistant"}, "v1")
	if err != nil {
		t.Fatalf("create first assistant: %v", err)
	}
	if err := service.PersistCompressionCheckpoint(session.ID, user.ID, []byte(`{"role":"user","content":"auto summary"}`), firstAssistant.ID+1, CompactionKindAuto); err != nil {
		t.Fatalf("persist auto checkpoint: %v", err)
	}

	secondUser, err := service.CreateUserMessage(session.ID, user.ID, &SendMessageRequest{Content: "new user", SchemaVersion: "v1"})
	if err != nil {
		t.Fatalf("create second user: %v", err)
	}
	secondAssistant, err := service.CreateAssistantMessage(session.ID, user.ID, map[string]interface{}{"role": "assistant", "content": "new assistant"}, "v1")
	if err != nil {
		t.Fatalf("create second assistant: %v", err)
	}
	if err := service.PersistCompressionCheckpoint(session.ID, user.ID, []byte(`{"role":"user","content":"manual summary"}`), secondAssistant.ID+1, CompactionKindManual); err != nil {
		t.Fatalf("persist manual checkpoint: %v", err)
	}

	if _, err := service.UndoLastCompaction(session.ID, user.ID); err != nil {
		t.Fatalf("undo manual checkpoint: %v", err)
	}

	visible, err := service.ListForAgent(session.ID, user.ID)
	if err != nil {
		t.Fatalf("list visible messages: %v", err)
	}
	if len(visible) != 3 {
		t.Fatalf("visible messages = %d, want auto summary plus two new messages", len(visible))
	}
	for _, message := range visible {
		if message.ID == firstUser.ID || message.ID == firstAssistant.ID {
			t.Fatalf("old message %d was incorrectly restored after nested undo", message.ID)
		}
	}
	if visible[1].ID != secondUser.ID || visible[2].ID != secondAssistant.ID {
		t.Fatalf("visible order after undo = [%d %d %d], want [auto summary %d %d]", visible[0].ID, visible[1].ID, visible[2].ID, secondUser.ID, secondAssistant.ID)
	}
}
