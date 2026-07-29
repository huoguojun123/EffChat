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

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/testutil"
)

func setupTitleTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return testutil.OpenPostgresTestDB(t)
}

// createTitleTestSession 建临时用户 + 会话，返回 sessionID + userID，清理由 t.Cleanup 注册
func createTitleTestSession(t *testing.T, db *sql.DB) (sessionID, userID int64) {
	t.Helper()

	userRepo := repository.NewUserRepository(db)
	user := &model.User{
		Username:     fmt.Sprintf("title_svc_%d", time.Now().UnixNano()),
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
		Title:         "新对话",
		ModelID:       "gpt-4o-mini",
		Provider:      "openai",
		MessageFormat: "v1",
		Metadata:      []byte(`{}`),
	}
	if err := sessionRepo.Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() {
		db.Exec("DELETE FROM messages WHERE session_id = $1", session.ID)
		db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
	})

	return session.ID, user.ID
}

func addMessage(t *testing.T, db *sql.DB, sessionID int64, data []byte) {
	t.Helper()
	msgRepo := repository.NewMessageRepository(db)
	msg := &model.Message{
		SessionID:     sessionID,
		SchemaVersion: "v1",
		MessageData:   data,
	}
	if err := msgRepo.Create(msg); err != nil {
		t.Fatalf("create message: %v", err)
	}
}

func titleTestMessage(t *testing.T, role, content string, extra map[string]interface{}) *model.Message {
	t.Helper()
	data := map[string]interface{}{
		"role":    role,
		"content": content,
	}
	for key, value := range extra {
		data[key] = value
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal title message: %v", err)
	}
	return &model.Message{Role: role, MessageData: raw}
}

func TestBuildTitleSeed_TruncatesAndSkipsNonTitleContent(t *testing.T) {
	svc := &TitleService{}
	longUser := strings.Repeat("论文正文", 120)
	messages := []*model.Message{
		titleTestMessage(t, "user", longUser, nil),
		titleTestMessage(t, "tool", "tool result should be ignored", nil),
		titleTestMessage(t, "assistant", "assistant with tool call", map[string]interface{}{
			"tool_calls": []interface{}{map[string]interface{}{"id": "call_1"}},
		}),
		titleTestMessage(t, "user", "压缩摘要不应进入标题", map[string]interface{}{
			"extra": map[string]interface{}{"_eino_summarization_content_type": "summary"},
		}),
		titleTestMessage(t, "assistant", "这是可用于标题的助手正文", nil),
	}

	seed := svc.buildTitleSeed(messages)
	if strings.Contains(seed, "tool result") || strings.Contains(seed, "压缩摘要") || strings.Contains(seed, "assistant with tool call") {
		t.Fatalf("seed contains skipped content: %s", seed)
	}
	if !strings.Contains(seed, "User:") || !strings.Contains(seed, "Assistant: 这是可用于标题的助手正文") {
		t.Fatalf("seed missing expected title content: %s", seed)
	}
	if len([]rune(seed)) > titleSeedMaxTotal {
		t.Fatalf("seed length = %d, want <= %d", len([]rune(seed)), titleSeedMaxTotal)
	}
	if strings.Count(seed, "论文正文") > 80 {
		t.Fatalf("long user content was not truncated enough")
	}
}

func TestUseFallbackTitle_TruncatesFirstUserMessageToFifteenRunes(t *testing.T) {
	db := setupTitleTestDB(t)
	defer db.Close()

	sessionID, userID := createTitleTestSession(t, db)
	sessionRepo := repository.NewSessionRepository(db)
	svc := &TitleService{sessionRepo: sessionRepo}
	messages := []*model.Message{
		titleTestMessage(t, "assistant", "ignored assistant", nil),
		titleTestMessage(t, "user", "一二三四五六七八九十甲乙丙丁戊己庚", nil),
	}

	if err := svc.useFallbackTitle(context.Background(), sessionID, userID, messages, nil); err != nil {
		t.Fatalf("useFallbackTitle: %v", err)
	}
	session, err := sessionRepo.GetByID(sessionID, userID)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if session.Title != "一二三四五六七八九十甲乙丙丁戊" {
		t.Fatalf("fallback title = %q", session.Title)
	}
	if strings.Contains(session.Title, "...") {
		t.Fatalf("fallback title should not include ellipsis: %q", session.Title)
	}
	if session.TitleGenerated {
		t.Fatalf("fallback title must not be marked model-generated")
	}
}

func TestUseFallbackTitle_EmptyUserMessageFallsBackToNewConversation(t *testing.T) {
	db := setupTitleTestDB(t)
	defer db.Close()

	sessionID, userID := createTitleTestSession(t, db)
	sessionRepo := repository.NewSessionRepository(db)
	svc := &TitleService{sessionRepo: sessionRepo}
	messages := []*model.Message{
		titleTestMessage(t, "assistant", "only assistant content", nil),
		titleTestMessage(t, "user", "   ", nil),
	}

	if err := svc.useFallbackTitle(context.Background(), sessionID, userID, messages, nil); err != nil {
		t.Fatalf("useFallbackTitle: %v", err)
	}
	session, err := sessionRepo.GetByID(sessionID, userID)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if session.Title != "新对话" {
		t.Fatalf("fallback title = %q", session.Title)
	}
	if session.TitleGenerated {
		t.Fatalf("fallback title must not be marked model-generated")
	}
}

func TestUseFallbackTitleRejectsStaleAnswerRevision(t *testing.T) {
	db := setupTitleTestDB(t)
	defer db.Close()

	sessionID, userID := createTitleTestSession(t, db)
	if _, err := db.Exec("UPDATE sessions SET answer_selection_revision = 1 WHERE id = $1", sessionID); err != nil {
		t.Fatalf("advance answer selection revision: %v", err)
	}
	svc := &TitleService{sessionRepo: repository.NewSessionRepository(db)}
	expectedRevision := int64(0)
	err := svc.useFallbackTitle(context.Background(), sessionID, userID, []*model.Message{
		titleTestMessage(t, "user", "stale title input", nil),
	}, &expectedRevision)
	if !errors.Is(err, repository.ErrAnswerSelectionRevisionConflict) {
		t.Fatalf("stale fallback error=%v, want answer selection conflict", err)
	}
	session, err := repository.NewSessionRepository(db).GetByID(sessionID, userID)
	if err != nil {
		t.Fatalf("load session after stale fallback: %v", err)
	}
	if session.Title != "新对话" {
		t.Fatalf("stale fallback title=%q", session.Title)
	}
}

func TestBuildTitleOpenAIConfigDisablesGPT56Reasoning(t *testing.T) {
	gpt56 := buildTitleOpenAIConfig(&model.AIChannel{APIKey: "test"}, "gpt-5.6")
	if gpt56.MaxCompletionTokens == nil || *gpt56.MaxCompletionTokens != 64 || gpt56.MaxTokens != nil {
		t.Fatalf("GPT-5.6 token fields = max=%v completion=%v", gpt56.MaxTokens, gpt56.MaxCompletionTokens)
	}
	if got := gpt56.ExtraFields["reasoning_effort"]; got != "none" {
		t.Fatalf("GPT-5.6 title reasoning effort = %#v, want none", got)
	}

	regular := buildTitleOpenAIConfig(&model.AIChannel{APIKey: "test"}, "gpt-4o-mini")
	if regular.MaxTokens == nil || *regular.MaxTokens != 64 || regular.MaxCompletionTokens != nil {
		t.Fatalf("regular token fields = max=%v completion=%v", regular.MaxTokens, regular.MaxCompletionTokens)
	}
	if len(regular.ExtraFields) != 0 {
		t.Fatalf("regular title request must not add reasoning fields: %#v", regular.ExtraFields)
	}
}

func TestTitleTaskRunRecordIgnoresCanceledContext(t *testing.T) {
	db := setupTitleTestDB(t)
	defer db.Close()

	sessionID, userID := createTitleTestSession(t, db)
	taskRuns := repository.NewModelTaskRunRepository(db)
	svc := &TitleService{taskRuns: taskRuns}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	svc.recordTitleTaskRun(ctx, sessionID, userID, titleModelProfile{Provider: "openai", ModelID: "gpt-4o-mini"}, time.Now(), repository.ModelTaskStatusFailed, context.DeadlineExceeded)

	latest, err := taskRuns.LatestForSession(context.Background(), sessionID, userID, repository.ModelTaskTitleGeneration)
	if err != nil {
		t.Fatalf("latest task run: %v", err)
	}
	if latest == nil || latest.Status != repository.ModelTaskStatusFailed || latest.ErrorType == "" {
		t.Fatalf("task run not recorded from canceled context: %+v", latest)
	}
}

func TestTitleDrainRejectsNewBackgroundTasks(t *testing.T) {
	svc := &TitleService{}
	if !svc.startBackgroundTask() {
		t.Fatal("title background task should start before drain")
	}
	drained := make(chan bool, 1)
	go func() {
		drained <- svc.DrainBackgroundTasks(context.Background())
	}()
	select {
	case <-drained:
		t.Fatal("title drain returned before the active task completed")
	case <-time.After(10 * time.Millisecond):
	}
	svc.backgroundTasks.Done()
	if !<-drained {
		t.Fatal("title service should drain after the active task completes")
	}
	if !svc.DrainBackgroundTasks(context.Background()) {
		t.Fatal("empty title service should drain immediately")
	}
	if svc.startBackgroundTask() {
		t.Fatal("title background task should be rejected after drain begins")
	}
}

func TestShouldGenerateTitle_TriggerOnSecondUserMessage(t *testing.T) {
	db := setupTitleTestDB(t)
	defer db.Close()

	sessionID, userID := createTitleTestSession(t, db)
	svc := &TitleService{
		sessionRepo: repository.NewSessionRepository(db),
		messageRepo: repository.NewMessageRepository(db),
	}
	ctx := context.Background()

	// 0 messages → false
	if ok, _ := svc.ShouldGenerateTitle(ctx, sessionID, userID); ok {
		t.Error("0 messages: want false")
	}

	// 1 user message → false
	addMessage(t, db, sessionID, []byte(`{"role":"user","content":"hello"}`))
	if ok, _ := svc.ShouldGenerateTitle(ctx, sessionID, userID); ok {
		t.Error("1 user message: want false")
	}

	// 1 assistant message → false (still only 1 user)
	addMessage(t, db, sessionID, []byte(`{"role":"assistant","content":"hi"}`))
	if ok, _ := svc.ShouldGenerateTitle(ctx, sessionID, userID); ok {
		t.Error("1 user + 1 assistant: want false")
	}

	// 2nd user message → true
	addMessage(t, db, sessionID, []byte(`{"role":"user","content":"second"}`))
	if ok, _ := svc.ShouldGenerateTitle(ctx, sessionID, userID); !ok {
		t.Error("2 user messages: want true")
	}

	// 3rd user message → true so a missed/restarted second-message task can recover.
	addMessage(t, db, sessionID, []byte(`{"role":"user","content":"third"}`))
	if ok, _ := svc.ShouldGenerateTitle(ctx, sessionID, userID); !ok {
		t.Error("3 user messages: want true for recovery")
	}
}

func TestShouldGenerateTitle_ExcludesSummaryMessages(t *testing.T) {
	db := setupTitleTestDB(t)
	defer db.Close()

	sessionID, userID := createTitleTestSession(t, db)
	svc := &TitleService{
		sessionRepo: repository.NewSessionRepository(db),
		messageRepo: repository.NewMessageRepository(db),
	}
	ctx := context.Background()

	// 1 real user message + 1 compression summary (role=user but has summary marker)
	addMessage(t, db, sessionID, []byte(`{"role":"user","content":"real message"}`))
	addMessage(t, db, sessionID, []byte(`{"role":"user","content":"[summary]","extra":{"_eino_summarization_content_type":"summary"}}`))

	// Should still be false — summary doesn't count toward user message tally
	if ok, _ := svc.ShouldGenerateTitle(ctx, sessionID, userID); ok {
		t.Error("1 real + 1 summary: want false (summary must not be counted)")
	}

	// Add the real second user message → now should be true
	addMessage(t, db, sessionID, []byte(`{"role":"user","content":"second real"}`))
	if ok, _ := svc.ShouldGenerateTitle(ctx, sessionID, userID); !ok {
		t.Error("2 real + 1 summary: want true")
	}
}

func TestShouldGenerateTitleStopsOnCanceledContext(t *testing.T) {
	db := setupTitleTestDB(t)
	defer db.Close()

	sessionID, userID := createTitleTestSession(t, db)
	svc := &TitleService{
		sessionRepo: repository.NewSessionRepository(db),
		messageRepo: repository.NewMessageRepository(db),
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := svc.ShouldGenerateTitle(ctx, sessionID, userID); !errors.Is(err, context.Canceled) {
		t.Fatalf("ShouldGenerateTitle error = %v, want context canceled", err)
	}
}

func TestUseFallbackTitleDoesNotWriteAfterCancellation(t *testing.T) {
	db := setupTitleTestDB(t)
	defer db.Close()

	sessionID, userID := createTitleTestSession(t, db)
	sessionRepo := repository.NewSessionRepository(db)
	svc := &TitleService{sessionRepo: sessionRepo}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := svc.useFallbackTitle(ctx, sessionID, userID, []*model.Message{
		titleTestMessage(t, "user", "must not become the title", nil),
	}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("fallback error = %v, want context canceled", err)
	}
	session, err := sessionRepo.GetByID(sessionID, userID)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if session.Title != "新对话" {
		t.Fatalf("canceled fallback changed title to %q", session.Title)
	}
}
