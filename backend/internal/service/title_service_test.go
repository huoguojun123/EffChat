package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/modelstream"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/testutil"
)

func setupTitleTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return testutil.OpenPostgresTestDB(t)
}

func newTitleStreamTestService(t *testing.T, db *sql.DB, baseURL string) *TitleService {
	t.Helper()
	channelKey := fmt.Sprintf("title-stream-%d", time.Now().UnixNano())
	channels := NewChannelService(repository.NewChannelRepository(db))
	enabled := true
	if _, err := channels.SaveAIChannel(&AIChannelInput{
		Key:         channelKey,
		DisplayName: "Title stream test",
		Adapter:     AdapterOpenAICompatible,
		BaseURL:     baseURL,
		APIKey:      "test-key",
		Enabled:     &enabled,
	}); err != nil {
		t.Fatalf("save title test channel: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM ai_channels WHERE channel_key = $1", channelKey)
	})

	previous := modelbank.Get("gpt-4o-mini")
	if previous == nil {
		t.Fatal("gpt-4o-mini modelbank entry is required for title tests")
	}
	modelbank.Register(&modelbank.ModelInfo{
		ID:             "gpt-4o-mini",
		DisplayName:    "Title stream test",
		Provider:       channelKey,
		Enabled:        true,
		ThinkingFormat: string(modelbank.ThinkingFormatNone),
		Capabilities: modelbank.ModelCapabilities{
			ContextWindow: 128000,
			MaxOutput:     titleMaxOutputTokens,
		},
	})
	t.Cleanup(func() {
		modelbank.Register(previous)
	})
	return NewTitleService(nil, nil, nil, channels)
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
	if latest.RetryAfter != nil {
		t.Fatalf("canceled title task created provider cooldown: %+v", latest.RetryAfter)
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

func TestTitleDrainTimeoutCancelsActiveBackgroundContext(t *testing.T) {
	svc := &TitleService{}
	if !svc.startBackgroundTask() {
		t.Fatal("title background task should start before drain")
	}
	taskCtx := svc.backgroundTaskContext()
	drainCtx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	if svc.DrainBackgroundTasks(drainCtx) {
		t.Fatal("drain should report timeout while task is still active")
	}
	select {
	case <-taskCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("drain timeout did not cancel title background context")
	}
	svc.backgroundTasks.Done()
}

func TestGenerateTitleWithModelStreamsAfterBoundedSetup(t *testing.T) {
	db := setupTitleTestDB(t)
	t.Cleanup(func() { _ = db.Close() })
	requestBodies := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		select {
		case requestBodies <- body:
		default:
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-title\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-4o-mini\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"流式标题\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	svc := newTitleStreamTestService(t, db, server.URL+"/v1")
	generated, err := svc.generateTitleWithModel(t.Context(), 11, 22, "User: 测试流式标题")
	if err != nil {
		t.Fatalf("generateTitleWithModel() error = %v", err)
	}
	if generated == nil || generated.Title != "流式标题" {
		t.Fatalf("generated title = %#v", generated)
	}
	var providerRequest map[string]interface{}
	select {
	case body := <-requestBodies:
		if err := json.Unmarshal(body, &providerRequest); err != nil {
			t.Fatalf("decode provider request: %v", err)
		}
	default:
		t.Fatal("title provider request was not captured")
	}
	maxTokens, _ := providerRequest["max_tokens"].(float64)
	if maxTokens == 0 {
		maxTokens, _ = providerRequest["max_completion_tokens"].(float64)
	}
	if int(maxTokens) != titleMaxOutputTokens {
		t.Fatalf("title output limit = %v, want %d", maxTokens, titleMaxOutputTokens)
	}
}

func TestOpenAIResponsesTitleModelUsesResponsesEndpoint(t *testing.T) {
	db := setupTitleTestDB(t)
	t.Cleanup(func() { _ = db.Close() })
	requestBodies := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		requestBodies <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"resp_title\",\"status\":\"in_progress\",\"model\":\"gpt-5.1\",\"output\":[]}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"item_id\":\"msg_1\",\"output_index\":0,\"content_index\":0,\"delta\":\"Responses title\",\"logprobs\":[]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":{\"id\":\"resp_title\",\"status\":\"completed\",\"model\":\"gpt-5.1\",\"output\":[],\"usage\":{\"input_tokens\":3,\"output_tokens\":2,\"total_tokens\":5}}}\n\n")
	}))
	defer server.Close()

	channelKey := fmt.Sprintf("title-responses-%d", time.Now().UnixNano())
	channels := NewChannelService(repository.NewChannelRepository(db))
	enabled := true
	if _, err := channels.SaveAIChannel(&AIChannelInput{
		Key: channelKey, DisplayName: "Responses title", Adapter: AdapterOpenAIResponses,
		BaseURL: server.URL + "/v1/responses", APIKey: "test-key", Enabled: &enabled,
	}); err != nil {
		t.Fatalf("save Responses title channel: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM ai_channels WHERE channel_key = $1", channelKey) })

	svc := NewTitleService(nil, nil, nil, channels)
	chatModel, err := svc.buildTitleChatModel(t.Context(), channelKey, "gpt-5.1")
	if err != nil {
		t.Fatalf("build Responses title model: %v", err)
	}
	result, err := modelstream.Collect(t.Context(), chatModel, []*schema.Message{schema.UserMessage("title")}, time.Second)
	if err != nil {
		t.Fatalf("collect Responses title: %v", err)
	}
	if result.Content != "Responses title" {
		t.Fatalf("title content = %q", result.Content)
	}
	body := <-requestBodies
	if body["max_output_tokens"] != float64(titleMaxOutputTokens) || body["store"] != false {
		t.Fatalf("title request = %#v", body)
	}
}

func TestAnthropicTitleModelDoesNotHideSDKRetry(t *testing.T) {
	db := setupTitleTestDB(t)
	t.Cleanup(func() { _ = db.Close() })
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/messages" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprint(w, `{"type":"error","error":{"type":"api_error","message":"temporarily unavailable"}}`)
	}))
	defer server.Close()

	channelKey := fmt.Sprintf("title-anthropic-%d", time.Now().UnixNano())
	channels := NewChannelService(repository.NewChannelRepository(db))
	enabled := true
	if _, err := channels.SaveAIChannel(&AIChannelInput{
		Key: channelKey, DisplayName: "Anthropic title retry ownership", Adapter: AdapterAnthropic,
		BaseURL: server.URL, APIKey: "test-key", Enabled: &enabled,
	}); err != nil {
		t.Fatalf("save Anthropic title channel: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM ai_channels WHERE channel_key = $1", channelKey) })

	svc := NewTitleService(nil, nil, nil, channels)
	chatModel, err := svc.buildTitleChatModel(t.Context(), channelKey, "claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("build Anthropic title model: %v", err)
	}
	_, err = modelstream.Collect(t.Context(), chatModel, []*schema.Message{schema.UserMessage("title")}, time.Second)
	if err == nil {
		t.Fatal("Anthropic 503 unexpectedly succeeded")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("Anthropic title SDK performed %d hidden attempts, want 1", got)
	}
}

func TestGoogleTitleModelDoesNotHideSDKRetry(t *testing.T) {
	db := setupTitleTestDB(t)
	t.Cleanup(func() { _ = db.Close() })
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !strings.HasPrefix(r.URL.Path, "/v1beta/models/") || !strings.HasSuffix(r.URL.Path, ":streamGenerateContent") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprint(w, `{"error":{"code":503,"message":"temporarily unavailable","status":"UNAVAILABLE"}}`)
	}))
	defer server.Close()

	channelKey := fmt.Sprintf("title-google-%d", time.Now().UnixNano())
	channels := NewChannelService(repository.NewChannelRepository(db))
	enabled := true
	if _, err := channels.SaveAIChannel(&AIChannelInput{
		Key: channelKey, DisplayName: "Google title retry ownership", Adapter: AdapterGoogle,
		BaseURL: server.URL, APIKey: "test-key", Enabled: &enabled,
	}); err != nil {
		t.Fatalf("save Google title channel: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM ai_channels WHERE channel_key = $1", channelKey) })

	svc := NewTitleService(nil, nil, nil, channels)
	chatModel, err := svc.buildTitleChatModel(t.Context(), channelKey, "gemini-2.5-pro")
	if err != nil {
		t.Fatalf("build Google title model: %v", err)
	}
	_, err = modelstream.Collect(t.Context(), chatModel, []*schema.Message{schema.UserMessage("title")}, time.Second)
	if err == nil {
		t.Fatal("Google 503 unexpectedly succeeded")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("Google title SDK performed %d hidden attempts, want 1", got)
	}
}

func TestResolveTitleModelProfilePreservesSetupDeadline(t *testing.T) {
	db := setupTitleTestDB(t)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	held, err := db.Conn(t.Context())
	if err != nil {
		t.Fatalf("hold database connection: %v", err)
	}
	defer held.Close()
	defer db.Close()

	svc := NewTitleService(nil, nil, nil, NewChannelService(repository.NewChannelRepository(db)))
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = svc.resolveTitleModelProfile(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("resolveTitleModelProfile() error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("title channel resolution ignored setup deadline: %v", elapsed)
	}
}

func TestTitleDrainCancelsStartedProviderStream(t *testing.T) {
	db := setupTitleTestDB(t)
	t.Cleanup(func() { _ = db.Close() })
	streamStarted := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-title-drain\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-4o-mini\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"部分\"},\"finish_reason\":null}]}\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		select {
		case streamStarted <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	svc := newTitleStreamTestService(t, db, server.URL+"/v1")
	if !svc.startBackgroundTask() {
		t.Fatal("title background task should start before drain")
	}
	runErr := make(chan error, 1)
	go func() {
		defer svc.backgroundTasks.Done()
		_, err := svc.generateTitleWithModel(svc.backgroundTaskContext(), 11, 22, "User: drain")
		runErr <- err
	}()
	select {
	case <-streamStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("title provider stream did not start")
	}
	drainCtx, cancelDrain := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancelDrain()
	if !svc.DrainBackgroundTasks(drainCtx) {
		t.Fatal("title service did not drain after canceling the active stream")
	}
	if err := <-runErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("title stream error = %v, want context canceled", err)
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
