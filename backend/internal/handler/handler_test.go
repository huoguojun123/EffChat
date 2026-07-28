package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/effchat/internal/middleware"
	"github.com/huoguojun123/effchat/internal/model"
	"github.com/huoguojun123/effchat/internal/repository"
	"github.com/huoguojun123/effchat/internal/service"
	"github.com/huoguojun123/effchat/internal/testutil"
)

func init() {
	gin.SetMode(gin.TestMode)
}

type retentionStoreStub struct {
	cutoff time.Time
}

func (s *retentionStoreStub) DeleteOlderThan(_ context.Context, cutoff time.Time) error {
	s.cutoff = cutoff
	return nil
}

func TestCleanupDiagnosticsUsesFixedRetentionWindow(t *testing.T) {
	now := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
	store := &retentionStoreStub{}
	cleanupDiagnostics([]diagnosticRetentionStore{store}, now)
	if want := now.Add(-diagnosticRetention); !store.cutoff.Equal(want) {
		t.Fatalf("cutoff = %s, want %s", store.cutoff, want)
	}
}

func setupHandlerTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return testutil.OpenPostgresTestDB(t)
}

type testEnv struct {
	db             *sql.DB
	router         *gin.Engine
	authService    *service.AuthService
	sessionService *service.SessionService
	messageService *service.MessageService
	token          string
	userID         int64
	channelKey     string
}

func setupTestEnv(t *testing.T) *testEnv {
	t.Helper()
	db := setupHandlerTestDB(t)

	userRepo := repository.NewUserRepository(db)
	sessionRepo := repository.NewSessionRepository(db)
	sessionFolderRepo := repository.NewSessionFolderRepository(db)
	messageRepo := repository.NewMessageRepository(db)
	configRepo := repository.NewConfigRepository(db)
	fileRepo := repository.NewFileRepository(db)
	channelRepo := repository.NewChannelRepository(db)
	modelRepo := repository.NewModelRepository(db)
	channelKey := fmt.Sprintf("handler-channel-%d", time.Now().UnixNano())
	enabled := true
	channelService := service.NewChannelService(channelRepo)
	if _, err := channelService.SaveAIChannel(&service.AIChannelInput{Key: channelKey, DisplayName: "Handler test", Adapter: service.AdapterOpenAICompatible, BaseURL: "https://example.test/v1", APIKey: "test-key", Enabled: &enabled}); err != nil {
		t.Fatalf("seed test channel: %v", err)
	}
	if err := modelRepo.Upsert(&model.Model{ID: "gpt-4o-mini", DisplayName: "Handler test model", Provider: channelKey, ContextWindow: 4096, MaxOutput: 1024, Enabled: true, ThinkingFormat: "auto"}); err != nil {
		t.Fatalf("seed test model: %v", err)
	}

	authService := service.NewAuthService(userRepo, "test-handler-secret")
	sessionService := service.NewSessionService(sessionRepo, messageRepo, configRepo, sessionFolderRepo)
	sessionService.SetRuntimeModelDependencies(modelRepo, channelService, userRepo)
	messageService := service.NewMessageService(messageRepo, sessionRepo, fileRepo, repository.NewAnswerAttemptRepository(db))
	sessionFolderService := service.NewSessionFolderService(sessionFolderRepo)

	r := gin.New()

	// Public routes
	r.POST("/api/v1/auth/register", RegisterHandler(authService))
	r.POST("/api/v1/auth/login", LoginHandler(authService))

	// Authenticated routes
	auth := r.Group("/api/v1")
	auth.Use(middleware.AuthMiddleware(authService))
	{
		auth.POST("/sessions", CreateSessionHandler(sessionService))
		auth.GET("/sessions", ListSessionsHandler(sessionService))
		auth.GET("/sessions/:id", GetSessionHandler(sessionService))
		auth.PATCH("/sessions/:id", UpdateSessionHandler(sessionService))
		auth.DELETE("/sessions/:id", DeleteSessionHandler(sessionService))
		auth.GET("/sessions/:id/export.md", ExportSessionMarkdownHandler(messageService))
		auth.GET("/sessions/:id/messages", ListMessagesHandler(messageService))
		auth.POST("/sessions/:id/answer-attempts/:attempt_id/select", SelectAnswerAttemptHandler(messageService, sessionService, authService, nil))
		auth.GET("/session-folders", ListSessionFoldersHandler(sessionFolderService))
		auth.POST("/session-folders", CreateSessionFolderHandler(sessionFolderService))
		auth.PATCH("/session-folders/:id", UpdateSessionFolderHandler(sessionFolderService))
		auth.DELETE("/session-folders/:id", DeleteSessionFolderHandler(sessionFolderService))
		auth.POST("/files", UploadFileHandler(fileRepo, configRepo))
		auth.GET("/files", ListFilesHandler(fileRepo))
		auth.DELETE("/files/:id", DeleteFileHandler(fileRepo))
		auth.GET("/files/upload-limits", UploadLimitsHandler(configRepo))
		auth.POST("/files/:id/ocr-refresh", RefreshOCRFileHandler(fileRepo, nil, nil))
	}

	// Register a test user and get token
	username := fmt.Sprintf("handler_test_%d", time.Now().UnixNano())
	regBody, _ := json.Marshal(map[string]string{
		"username": username,
		"password": "testpass123",
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(regBody))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("register failed: %d %s", w.Code, w.Body.String())
	}

	var regResp struct {
		Approved bool        `json:"approved"`
		Token    string      `json:"token"`
		User     *model.User `json:"user"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &regResp); err != nil {
		t.Fatalf("unmarshal register response: %v", err)
	}
	if !regResp.Approved || regResp.User == nil || regResp.Token == "" {
		var pendingUserID int64
		if err := db.QueryRow("SELECT id FROM users WHERE username = $1", username).Scan(&pendingUserID); err != nil {
			t.Fatalf("lookup pending user failed: %v", err)
		}
		if _, err := db.Exec("UPDATE users SET is_active = true WHERE id = $1", pendingUserID); err != nil {
			t.Fatalf("activate pending user failed: %v", err)
		}
		loginResp, err := authService.Login(&service.LoginRequest{Username: username, Password: "testpass123"})
		if err != nil {
			t.Fatalf("login activated user failed: %v", err)
		}
		regResp.Approved = true
		regResp.Token = loginResp.Token
		regResp.User = loginResp.User
	}

	t.Cleanup(func() {
		db.Exec("DELETE FROM messages WHERE session_id IN (SELECT id FROM sessions WHERE user_id = $1)", regResp.User.ID)
		db.Exec("DELETE FROM sessions WHERE user_id = $1", regResp.User.ID)
		db.Exec("DELETE FROM session_folders WHERE user_id = $1", regResp.User.ID)
		db.Exec("DELETE FROM users WHERE id = $1", regResp.User.ID)
		db.Exec("DELETE FROM models WHERE id = 'gpt-4o-mini'")
		db.Exec("DELETE FROM ai_channels WHERE channel_key = $1", channelKey)
		db.Close()
	})

	return &testEnv{
		db:             db,
		router:         r,
		authService:    authService,
		sessionService: sessionService,
		messageService: messageService,
		token:          regResp.Token,
		userID:         regResp.User.ID,
		channelKey:     channelKey,
	}
}

func TestSelectAnswerAttemptHandlerSwitchesVisibleAnswerAndNavigation(t *testing.T) {
	env := setupTestEnv(t)
	created := env.doRequest(http.MethodPost, "/api/v1/sessions", map[string]interface{}{
		"model_id": "gpt-4o-mini",
		"provider": env.channelKey,
		"title":    "Answer attempts",
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create session: status=%d body=%s", created.Code, created.Body.String())
	}
	var session model.Session
	if err := json.Unmarshal(created.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode session: %v", err)
	}

	userMessage, err := env.messageService.CreateUserMessage(session.ID, env.userID, &service.SendMessageRequest{Content: "给我两个答案", SchemaVersion: "v1"})
	if err != nil {
		t.Fatalf("create user message: %v", err)
	}
	firstAnswer, err := env.messageService.CreateAssistantMessage(session.ID, env.userID, map[string]interface{}{"role": "assistant", "content": "第一个答案"}, "v1")
	if err != nil {
		t.Fatalf("create first answer: %v", err)
	}
	secondAnswer, err := env.messageService.CreateAssistantMessage(session.ID, env.userID, map[string]interface{}{"role": "assistant", "content": "第二个答案"}, "v1")
	if err != nil {
		t.Fatalf("create second answer: %v", err)
	}
	incompleteAnswer, err := env.messageService.CreateAssistantMessage(session.ID, env.userID, map[string]interface{}{"role": "assistant", "content": "未完成的答案", "metadata": map[string]interface{}{"incomplete": true}}, "v1")
	if err != nil {
		t.Fatalf("create incomplete answer: %v", err)
	}

	var firstAttemptID, secondAttemptID, incompleteAttemptID int64
	if err := env.db.QueryRow(`
		INSERT INTO answer_attempts (session_id, user_message_id, attempt_number, status, selected)
		VALUES ($1, $2, 1, 'completed', true)
		RETURNING id
	`, session.ID, userMessage.ID).Scan(&firstAttemptID); err != nil {
		t.Fatalf("create first attempt: %v", err)
	}
	var failedAttemptID int64
	if err := env.db.QueryRow(`
		INSERT INTO answer_attempts (session_id, user_message_id, attempt_number, status, selected)
		VALUES ($1, $2, 2, 'failed', false)
		RETURNING id
	`, session.ID, userMessage.ID).Scan(&failedAttemptID); err != nil {
		t.Fatalf("create failed attempt: %v", err)
	}
	if err := env.db.QueryRow(`
		INSERT INTO answer_attempts (session_id, user_message_id, attempt_number, status, selected)
		VALUES ($1, $2, 3, 'completed', false)
		RETURNING id
	`, session.ID, userMessage.ID).Scan(&secondAttemptID); err != nil {
		t.Fatalf("create second attempt: %v", err)
	}
	if err := env.db.QueryRow(`
		INSERT INTO answer_attempts (session_id, user_message_id, attempt_number, status, selected)
		VALUES ($1, $2, 4, 'incomplete', false)
		RETURNING id
	`, session.ID, userMessage.ID).Scan(&incompleteAttemptID); err != nil {
		t.Fatalf("create incomplete attempt: %v", err)
	}
	if _, err := env.db.Exec(`UPDATE messages SET answer_attempt_id = $1 WHERE id = $2`, firstAttemptID, firstAnswer.ID); err != nil {
		t.Fatalf("bind first answer: %v", err)
	}
	if _, err := env.db.Exec(`UPDATE messages SET answer_attempt_id = $1 WHERE id = $2`, secondAttemptID, secondAnswer.ID); err != nil {
		t.Fatalf("bind second answer: %v", err)
	}
	if _, err := env.db.Exec(`UPDATE messages SET answer_attempt_id = $1 WHERE id = $2`, incompleteAttemptID, incompleteAnswer.ID); err != nil {
		t.Fatalf("bind incomplete answer: %v", err)
	}

	selected := env.doRequest(http.MethodPost, fmt.Sprintf("/api/v1/sessions/%d/answer-attempts/%d/select", session.ID, secondAttemptID), nil)
	if selected.Code != http.StatusOK {
		t.Fatalf("select second attempt: status=%d body=%s", selected.Code, selected.Body.String())
	}
	var selectionPayload struct {
		SelectionChanged        bool  `json:"selection_changed"`
		AnswerSelectionRevision int64 `json:"answer_selection_revision"`
	}
	if err := json.Unmarshal(selected.Body.Bytes(), &selectionPayload); err != nil || !selectionPayload.SelectionChanged || selectionPayload.AnswerSelectionRevision != 1 {
		t.Fatalf("selection payload=%+v err=%v, want changed revision 1", selectionPayload, err)
	}
	cursorPage, _, err := repository.NewMessageRepository(env.db).ListBySessionPaged(session.ID, 30, firstAnswer.ID)
	if err != nil {
		t.Fatalf("list messages before unselected answer cursor: %v", err)
	}
	if len(cursorPage) != 1 || cursorPage[0].ID != userMessage.ID {
		t.Fatalf("messages before unselected answer cursor=%+v, want user message %d", cursorPage, userMessage.ID)
	}

	listed := env.doRequest(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%d/messages", session.ID), nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("list messages: status=%d body=%s", listed.Code, listed.Body.String())
	}
	var listResponse struct {
		Messages []*service.MessageResponse `json:"messages"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &listResponse); err != nil {
		t.Fatalf("decode messages: %v", err)
	}
	if len(listResponse.Messages) != 2 {
		t.Fatalf("visible messages=%d, want user plus selected answer", len(listResponse.Messages))
	}
	answer := listResponse.Messages[1]
	if answer.MessageData["content"] != "第二个答案" || answer.AnswerAttemptID == nil || *answer.AnswerAttemptID != secondAttemptID {
		t.Fatalf("visible answer=%+v, want second attempt", answer)
	}
	if answer.AnswerNavigation == nil || answer.AnswerNavigation.AttemptNumber != 2 || answer.AnswerNavigation.AttemptCount != 3 || answer.AnswerNavigation.PreviousAttemptID == nil || *answer.AnswerNavigation.PreviousAttemptID != firstAttemptID || answer.AnswerNavigation.NextAttemptID == nil || *answer.AnswerNavigation.NextAttemptID != incompleteAttemptID || !answer.AnswerNavigation.CanSwitch {
		t.Fatalf("answer navigation=%+v", answer.AnswerNavigation)
	}

	var revision int64
	if err := env.db.QueryRow(`SELECT answer_selection_revision FROM sessions WHERE id = $1`, session.ID).Scan(&revision); err != nil || revision != 1 {
		t.Fatalf("revision=%d err=%v, want 1", revision, err)
	}

	selected = env.doRequest(http.MethodPost, fmt.Sprintf("/api/v1/sessions/%d/answer-attempts/%d/select", session.ID, firstAttemptID), nil)
	if selected.Code != http.StatusOK {
		t.Fatalf("select first attempt: status=%d body=%s", selected.Code, selected.Body.String())
	}
	if err := env.db.QueryRow(`SELECT answer_selection_revision FROM sessions WHERE id = $1`, session.ID).Scan(&revision); err != nil || revision != 2 {
		t.Fatalf("revision=%d err=%v, want 2", revision, err)
	}
	reselected := env.doRequest(http.MethodPost, fmt.Sprintf("/api/v1/sessions/%d/answer-attempts/%d/select", session.ID, firstAttemptID), nil)
	if reselected.Code != http.StatusOK {
		t.Fatalf("reselect first attempt: status=%d body=%s", reselected.Code, reselected.Body.String())
	}
	selectionPayload = struct {
		SelectionChanged        bool  `json:"selection_changed"`
		AnswerSelectionRevision int64 `json:"answer_selection_revision"`
	}{}
	if err := json.Unmarshal(reselected.Body.Bytes(), &selectionPayload); err != nil || selectionPayload.SelectionChanged || selectionPayload.AnswerSelectionRevision != 2 {
		t.Fatalf("reselection payload=%+v err=%v, want unchanged revision 2", selectionPayload, err)
	}
	listed = env.doRequest(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%d/messages", session.ID), nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("list messages after switching back: status=%d body=%s", listed.Code, listed.Body.String())
	}
	listResponse = struct {
		Messages []*service.MessageResponse `json:"messages"`
	}{}
	if err := json.Unmarshal(listed.Body.Bytes(), &listResponse); err != nil {
		t.Fatalf("decode messages after switching back: %v", err)
	}
	answer = listResponse.Messages[1]
	if answer.MessageData["content"] != "第一个答案" || answer.AnswerAttemptID == nil || *answer.AnswerAttemptID != firstAttemptID {
		t.Fatalf("visible answer after switching back=%+v, want first attempt", answer)
	}
	if answer.AnswerNavigation == nil || answer.AnswerNavigation.PreviousAttemptID != nil || answer.AnswerNavigation.NextAttemptID == nil || *answer.AnswerNavigation.NextAttemptID != secondAttemptID {
		t.Fatalf("answer navigation after switching back=%+v", answer.AnswerNavigation)
	}

	selected = env.doRequest(http.MethodPost, fmt.Sprintf("/api/v1/sessions/%d/answer-attempts/%d/select", session.ID, incompleteAttemptID), nil)
	if selected.Code != http.StatusOK {
		t.Fatalf("select incomplete attempt: status=%d body=%s", selected.Code, selected.Body.String())
	}
	if err := env.db.QueryRow(`SELECT answer_selection_revision FROM sessions WHERE id = $1`, session.ID).Scan(&revision); err != nil || revision != 3 {
		t.Fatalf("revision=%d err=%v, want 3", revision, err)
	}
	listed = env.doRequest(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%d/messages", session.ID), nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("list incomplete answer: status=%d body=%s", listed.Code, listed.Body.String())
	}
	listResponse = struct {
		Messages []*service.MessageResponse `json:"messages"`
	}{}
	if err := json.Unmarshal(listed.Body.Bytes(), &listResponse); err != nil {
		t.Fatalf("decode incomplete answer: %v", err)
	}
	answer = listResponse.Messages[1]
	if answer.MessageData["content"] != "未完成的答案" || answer.AnswerAttemptID == nil || *answer.AnswerAttemptID != incompleteAttemptID {
		t.Fatalf("visible incomplete answer=%+v", answer)
	}
	if answer.AnswerNavigation == nil || answer.AnswerNavigation.AttemptNumber != 3 || answer.AnswerNavigation.AttemptCount != 3 || answer.AnswerNavigation.PreviousAttemptID == nil || *answer.AnswerNavigation.PreviousAttemptID != secondAttemptID || answer.AnswerNavigation.NextAttemptID != nil {
		t.Fatalf("incomplete answer navigation=%+v", answer.AnswerNavigation)
	}

	if _, err := env.db.Exec(`UPDATE messages SET compressed_at = NOW() WHERE id IN ($1, $2, $3, $4)`, userMessage.ID, firstAnswer.ID, secondAnswer.ID, incompleteAnswer.ID); err != nil {
		t.Fatalf("mark answer turn compressed: %v", err)
	}
	listed = env.doRequest(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%d/messages", session.ID), nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("list compressed messages: status=%d body=%s", listed.Code, listed.Body.String())
	}
	listResponse = struct {
		Messages []*service.MessageResponse `json:"messages"`
	}{}
	if err := json.Unmarshal(listed.Body.Bytes(), &listResponse); err != nil {
		t.Fatalf("decode compressed messages: %v", err)
	}
	if listResponse.Messages[1].AnswerNavigation != nil {
		t.Fatalf("compressed answer navigation=%+v, want hidden", listResponse.Messages[1].AnswerNavigation)
	}
	rejected := env.doRequest(http.MethodPost, fmt.Sprintf("/api/v1/sessions/%d/answer-attempts/%d/select", session.ID, secondAttemptID), nil)
	if rejected.Code != http.StatusConflict {
		t.Fatalf("select compressed attempt: status=%d body=%s", rejected.Code, rejected.Body.String())
	}
	rejectedPayload := struct {
		Code string `json:"code"`
	}{}
	if err := json.Unmarshal(rejected.Body.Bytes(), &rejectedPayload); err != nil || rejectedPayload.Code != "answer_attempt_not_latest" {
		t.Fatalf("compressed selection payload=%+v err=%v", rejectedPayload, err)
	}
}

func TestSelectedAnswerMemoryMessagesStopBeforeANewerUserTurn(t *testing.T) {
	attemptID := int64(42)
	otherAttemptID := int64(43)
	message := func(id int64, role, content string, answerAttemptID *int64) *model.Message {
		data, err := json.Marshal(map[string]interface{}{"role": role, "content": content})
		if err != nil {
			t.Fatalf("marshal message: %v", err)
		}
		return &model.Message{ID: id, Role: role, MessageData: data, AnswerAttemptID: answerAttemptID}
	}
	messages := []*model.Message{
		message(10, "user", "earlier question", nil),
		message(11, "assistant", "earlier answer", nil),
		message(20, "user", "selected question", nil),
		message(21, "assistant", "selected answer", &attemptID),
		message(30, "user", "newer question", nil),
		message(31, "assistant", "newer answer", &otherAttemptID),
	}

	selected, err := selectedAnswerMemoryMessages(messages, &repository.AnswerAttempt{ID: attemptID, UserMessageID: 20})
	if err != nil {
		t.Fatalf("scope selected answer messages: %v", err)
	}
	selectedIDs := make([]int64, 0, len(selected))
	for _, selectedMessage := range selected {
		selectedIDs = append(selectedIDs, selectedMessage.ID)
	}
	if got := selectedIDs; !slices.Equal(got, []int64{10, 11, 20, 21}) {
		t.Fatalf("selected memory message ids=%v", got)
	}
	userText, err := latestMemoryRetryUserTextFromMessages(selected)
	if err != nil || userText != "selected question" {
		t.Fatalf("selected user text=%q err=%v", userText, err)
	}
	contextText := service.RecentConversationTextForMemoryMessages(selected, 5)
	if !strings.Contains(contextText, "selected answer") || strings.Contains(contextText, "newer question") {
		t.Fatalf("selected memory context=%q", contextText)
	}

	if _, err := selectedAnswerMemoryMessages([]*model.Message{
		message(20, "user", "selected question", nil),
		message(22, "assistant", "different selected answer", &otherAttemptID),
	}, &repository.AnswerAttempt{ID: attemptID, UserMessageID: 20}); !errors.Is(err, repository.ErrRetryTargetStale) {
		t.Fatalf("missing selected attempt output error=%v, want retry target stale", err)
	}
}

func TestFilesHandler_ListsAndDeletesReferencedAttachment(t *testing.T) {
	env := setupTestEnv(t)
	created := env.doRequest(http.MethodPost, "/api/v1/sessions", map[string]interface{}{
		"model_id": "gpt-4o-mini",
		"provider": env.channelKey,
		"title":    "Referenced files",
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create session: status=%d body=%s", created.Code, created.Body.String())
	}
	var session model.Session
	if err := json.Unmarshal(created.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}

	filePath := fmt.Sprintf("./storage/attachments/extracted/%d/referenced_%d.txt", env.userID, time.Now().UnixNano())
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath, []byte("attachment"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(filePath) })
	sessionID := session.ID
	file := &model.File{UserID: env.userID, SessionID: &sessionID, FileName: "referenced.txt", FilePath: filePath, FileType: "text/plain", FileSize: 10, ExtractStatus: "ready"}
	if err := repository.NewFileRepository(env.db).Create(file); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]interface{}{"role": "user", "content": "with attachment", "attachments": []map[string]interface{}{{"file_id": file.ID, "filename": file.FileName, "file_type": file.FileType}}})
	if err := repository.NewMessageRepository(env.db).CreateForActiveSession(context.Background(), session.ID, env.userID, &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: data}); err != nil {
		t.Fatal(err)
	}

	listed := env.doRequest(http.MethodGet, fmt.Sprintf("/api/v1/files?session_id=%d&referenced=true", session.ID), nil)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), "referenced.txt") {
		t.Fatalf("referenced list status=%d body=%s", listed.Code, listed.Body.String())
	}
	deleted := env.doRequest(http.MethodDelete, fmt.Sprintf("/api/v1/files/%d", file.ID), nil)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete referenced file status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	if _, err := os.Stat(filePath); err != nil {
		t.Fatalf("referenced file should remain during the 24-hour retention period: %v", err)
	}
	listed = env.doRequest(http.MethodGet, fmt.Sprintf("/api/v1/files?session_id=%d&referenced=true", session.ID), nil)
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), "referenced.txt") {
		t.Fatalf("deleted referenced list status=%d body=%s", listed.Code, listed.Body.String())
	}
}

func (e *testEnv) doRequest(method, path string, body interface{}) *httptest.ResponseRecorder {
	var bodyReader *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		bodyReader = bytes.NewReader(b)
	} else {
		bodyReader = bytes.NewReader(nil)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, bodyReader)
	req.Header.Set("Content-Type", "application/json")
	if e.token != "" {
		req.Header.Set("Authorization", "Bearer "+e.token)
	}
	e.router.ServeHTTP(w, req)
	return w
}

// --- Auth Tests ---

func TestRegister_DuplicateUsername(t *testing.T) {
	env := setupTestEnv(t)

	// Re-register same user (extracted from token)
	body := map[string]string{
		"username": fmt.Sprintf("handler_test_%d", time.Now().UnixNano()),
		"password": "testpass123",
	}
	w := env.doRequest(http.MethodPost, "/api/v1/auth/register", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("first register should succeed: %d", w.Code)
	}

	// Second register with same username
	w = env.doRequest(http.MethodPost, "/api/v1/auth/register", body)
	if w.Code != http.StatusBadRequest {
		t.Errorf("duplicate register: want 400, got %d", w.Code)
	}
}

func TestRegister_PendingApprovalForLaterUsers(t *testing.T) {
	env := setupTestEnv(t)

	body := map[string]string{
		"username": fmt.Sprintf("pending_test_%d", time.Now().UnixNano()),
		"password": "testpass123",
	}
	w := env.doRequest(http.MethodPost, "/api/v1/auth/register", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("second register should succeed: %d %s", w.Code, w.Body.String())
	}

	var resp struct {
		Approved bool        `json:"approved"`
		Message  string      `json:"message"`
		Token    string      `json:"token"`
		User     *model.User `json:"user"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal register response: %v", err)
	}
	if resp.Approved {
		t.Fatal("later users should require approval")
	}
	if resp.Token != "" || resp.User != nil {
		t.Fatal("pending user should not receive token or user payload")
	}
}

func TestRegister_InvalidBody(t *testing.T) {
	env := setupTestEnv(t)

	// Missing required fields
	w := env.doRequest(http.MethodPost, "/api/v1/auth/register", map[string]string{
		"username": "ab", // too short (min=3)
		"password": "testpass123",
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("short username: want 400, got %d", w.Code)
	}
}

func TestLogin_InvalidCredentials(t *testing.T) {
	env := setupTestEnv(t)

	w := env.doRequest(http.MethodPost, "/api/v1/auth/login", map[string]string{
		"username": "nonexistent_user_xyz",
		"password": "wrong",
	})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("bad login: want 401, got %d", w.Code)
	}
}

func TestChangePasswordCancelsActiveRuns(t *testing.T) {
	env := setupTestEnv(t)
	runHub := service.NewRunHub(time.Minute, 1<<20)
	env.authService.SetRunHub(runHub)
	run, err := runHub.Start(1, env.userID, 0, "password-change", service.RunKindChat)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	canceled := make(chan struct{}, 1)
	runHub.Bind(run.RunID, func() { canceled <- struct{}{} })

	if err := env.authService.ChangePassword(env.userID, &service.ChangePasswordRequest{
		OldPassword: "testpass123",
		NewPassword: "newpass123",
	}); err != nil {
		t.Fatalf("change password: %v", err)
	}
	select {
	case <-canceled:
	default:
		t.Fatal("active run was not canceled")
	}
}

// --- Auth Middleware Tests ---

func TestMiddleware_NoToken(t *testing.T) {
	env := setupTestEnv(t)
	env.token = "" // clear token
	w := env.doRequest(http.MethodGet, "/api/v1/sessions", nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("no token: want 401, got %d", w.Code)
	}
}

func TestMiddleware_InvalidToken(t *testing.T) {
	env := setupTestEnv(t)
	env.token = "invalid.token.here"
	w := env.doRequest(http.MethodGet, "/api/v1/sessions", nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("invalid token: want 401, got %d", w.Code)
	}
}

// --- Session CRUD Tests ---

func TestSessionCRUD(t *testing.T) {
	env := setupTestEnv(t)

	// Create
	createReq := map[string]interface{}{
		"model_id": "gpt-4o-mini",
		"provider": env.channelKey,
		"title":    "Test Session",
	}
	w := env.doRequest(http.MethodPost, "/api/v1/sessions", createReq)
	if w.Code != http.StatusCreated {
		t.Fatalf("create session: want 201, got %d body=%s", w.Code, w.Body.String())
	}

	var session model.Session
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatalf("unmarshal session: %v", err)
	}
	if session.Title != "Test Session" {
		t.Errorf("title: want 'Test Session', got %q", session.Title)
	}
	if !session.MemoryEnabled {
		t.Error("memory_enabled: want true by default")
	}

	// Get
	w = env.doRequest(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%d", session.ID), nil)
	if w.Code != http.StatusOK {
		t.Errorf("get session: want 200, got %d", w.Code)
	}

	// List
	w = env.doRequest(http.MethodGet, "/api/v1/sessions", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list sessions: want 200, got %d", w.Code)
	}
	var listResp struct {
		Sessions []interface{} `json:"sessions"`
		HasMore  bool          `json:"has_more"`
	}
	json.Unmarshal(w.Body.Bytes(), &listResp)
	if len(listResp.Sessions) < 1 {
		t.Errorf("list: want at least 1 session, got %d", len(listResp.Sessions))
	}

	// Update
	newTitle := "Updated Title"
	w = env.doRequest(http.MethodPatch, fmt.Sprintf("/api/v1/sessions/%d", session.ID), map[string]interface{}{
		"title": newTitle,
	})
	if w.Code != http.StatusOK {
		t.Errorf("update session: want 200, got %d body=%s", w.Code, w.Body.String())
	}

	// Verify update
	w = env.doRequest(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%d", session.ID), nil)
	var updated model.Session
	json.Unmarshal(w.Body.Bytes(), &updated)
	if updated.Title != newTitle {
		t.Errorf("after update: want title %q, got %q", newTitle, updated.Title)
	}

	// Delete
	w = env.doRequest(http.MethodDelete, fmt.Sprintf("/api/v1/sessions/%d", session.ID), nil)
	if w.Code != http.StatusOK {
		t.Errorf("delete session: want 200, got %d", w.Code)
	}

	// Verify deleted
	w = env.doRequest(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%d", session.ID), nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("get after delete: want 404, got %d", w.Code)
	}
}

func TestCreateSession_RejectsMissingModelWhenDefaultUnset(t *testing.T) {
	env := setupTestEnv(t)
	var previousDefault []byte
	hadDefault := env.db.QueryRow("SELECT value FROM system_config WHERE key = 'default_model_id'").Scan(&previousDefault) == nil
	if _, err := env.db.Exec(`
		INSERT INTO system_config (key, value, config_type)
		VALUES ('default_model_id', '""'::jsonb, 'string')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, config_type = EXCLUDED.config_type
	`); err != nil {
		t.Fatalf("clear default_model_id: %v", err)
	}
	t.Cleanup(func() {
		if hadDefault {
			_, _ = env.db.Exec("UPDATE system_config SET value = $1 WHERE key = 'default_model_id'", previousDefault)
		} else {
			_, _ = env.db.Exec("DELETE FROM system_config WHERE key = 'default_model_id'")
		}
	})

	w := env.doRequest(http.MethodPost, "/api/v1/sessions", map[string]interface{}{
		"title": "No model",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing model_id without configured default: want 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestGetSession_NotFound(t *testing.T) {
	env := setupTestEnv(t)

	w := env.doRequest(http.MethodGet, "/api/v1/sessions/999999999", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("nonexistent session: want 404, got %d", w.Code)
	}
}

func TestGetSession_InvalidID(t *testing.T) {
	env := setupTestEnv(t)

	w := env.doRequest(http.MethodGet, "/api/v1/sessions/abc", nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid id: want 400, got %d", w.Code)
	}
}

// --- Pagination Tests ---

func TestListSessions_Pagination(t *testing.T) {
	env := setupTestEnv(t)

	// Create 3 sessions
	for i := 0; i < 3; i++ {
		env.doRequest(http.MethodPost, "/api/v1/sessions", map[string]interface{}{
			"model_id": "gpt-4o-mini",
			"provider": env.channelKey,
			"title":    fmt.Sprintf("Session %d", i),
		})
	}

	// Request with limit=2
	w := env.doRequest(http.MethodGet, "/api/v1/sessions?limit=2&offset=0", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list: got %d", w.Code)
	}
	var resp struct {
		Sessions   []interface{} `json:"sessions"`
		HasMore    bool          `json:"has_more"`
		NextOffset int           `json:"next_offset"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Sessions) != 2 {
		t.Errorf("limit=2: want 2 sessions, got %d", len(resp.Sessions))
	}
	if !resp.HasMore || resp.NextOffset != 2 {
		t.Errorf("limit=2: want has_more=true next_offset=2, got has_more=%v next_offset=%d", resp.HasMore, resp.NextOffset)
	}

	// offset=2 should get the third
	w = env.doRequest(http.MethodGet, "/api/v1/sessions?limit=10&offset=2", nil)
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Sessions) != 1 {
		t.Errorf("offset=2: want 1 session, got %d", len(resp.Sessions))
	}
	if resp.HasMore {
		t.Errorf("offset=2: want has_more=false")
	}
}

func TestSessionFolders_CRUDAndFiltering(t *testing.T) {
	env := setupTestEnv(t)

	w := env.doRequest(http.MethodPost, "/api/v1/session-folders", map[string]interface{}{
		"name": "工作",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create folder: got %d %s", w.Code, w.Body.String())
	}
	var folder model.SessionFolder
	if err := json.Unmarshal(w.Body.Bytes(), &folder); err != nil {
		t.Fatalf("unmarshal folder: %v", err)
	}

	w = env.doRequest(http.MethodPost, "/api/v1/sessions", map[string]interface{}{
		"model_id":  "gpt-4o-mini",
		"provider":  env.channelKey,
		"title":     "Foldered",
		"folder_id": folder.ID,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create foldered session: got %d %s", w.Code, w.Body.String())
	}
	var foldered model.Session
	if err := json.Unmarshal(w.Body.Bytes(), &foldered); err != nil {
		t.Fatalf("unmarshal foldered session: %v", err)
	}

	w = env.doRequest(http.MethodPost, "/api/v1/sessions", map[string]interface{}{
		"model_id": "gpt-4o-mini",
		"provider": env.channelKey,
		"title":    "Unfiled",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create unfiled session: got %d %s", w.Code, w.Body.String())
	}

	w = env.doRequest(http.MethodGet, fmt.Sprintf("/api/v1/sessions?folder_id=%d", folder.ID), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list foldered: got %d %s", w.Code, w.Body.String())
	}
	var listResp struct {
		Sessions []model.Session `json:"sessions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal foldered list: %v", err)
	}
	if len(listResp.Sessions) != 1 || listResp.Sessions[0].ID != foldered.ID {
		t.Fatalf("foldered list mismatch: %+v", listResp.Sessions)
	}

	w = env.doRequest(http.MethodGet, "/api/v1/sessions?folder_id=unfiled", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list unfiled: got %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal unfiled list: %v", err)
	}
	if len(listResp.Sessions) != 1 || listResp.Sessions[0].FolderID != nil {
		t.Fatalf("unfiled list mismatch: %+v", listResp.Sessions)
	}

	w = env.doRequest(http.MethodPatch, fmt.Sprintf("/api/v1/sessions/%d", foldered.ID), map[string]interface{}{
		"pinned": true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("pin session: got %d %s", w.Code, w.Body.String())
	}
	w = env.doRequest(http.MethodPatch, fmt.Sprintf("/api/v1/sessions/%d", foldered.ID), map[string]interface{}{
		"folder_id": nil,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("remove folder: got %d %s", w.Code, w.Body.String())
	}
	w = env.doRequest(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%d", foldered.ID), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get moved session: got %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &foldered); err != nil {
		t.Fatalf("unmarshal moved session: %v", err)
	}
	if foldered.FolderID != nil {
		t.Fatalf("want folder_id nil after moving out, got %v", *foldered.FolderID)
	}
	if foldered.PinnedAt == nil {
		t.Fatal("moving a pinned session must preserve pinned_at")
	}
	w = env.doRequest(http.MethodGet, "/api/v1/sessions?folder_id=unfiled", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list moved pinned session: got %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal moved list: %v", err)
	}
	if len(listResp.Sessions) != 2 || listResp.Sessions[0].ID != foldered.ID {
		t.Fatalf("pinned session should lead its new folder: %+v", listResp.Sessions)
	}

	w = env.doRequest(http.MethodPatch, fmt.Sprintf("/api/v1/session-folders/%d", folder.ID), map[string]interface{}{
		"pinned": true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("pin folder: got %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &folder); err != nil || folder.PinnedAt == nil {
		t.Fatalf("folder pin response = %s, err = %v", w.Body.String(), err)
	}
	w = env.doRequest(http.MethodPatch, fmt.Sprintf("/api/v1/session-folders/%d", folder.ID), map[string]interface{}{
		"name": "项目",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("rename folder: got %d %s", w.Code, w.Body.String())
	}
	w = env.doRequest(http.MethodDelete, fmt.Sprintf("/api/v1/session-folders/%d", folder.ID), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("delete folder: got %d %s", w.Code, w.Body.String())
	}
}

func TestSessionFolders_CrossUserIsolation(t *testing.T) {
	env := setupTestEnv(t)

	var otherUserID int64
	err := env.db.QueryRow(
		`INSERT INTO users (username, password_hash, role, is_active, permissions, preferences)
		 VALUES ($1, 'hash', 'user', true, '{}', '{}') RETURNING id`,
		fmt.Sprintf("folder_other_%d", time.Now().UnixNano()),
	).Scan(&otherUserID)
	if err != nil {
		t.Fatalf("insert other user: %v", err)
	}
	t.Cleanup(func() {
		env.db.Exec("DELETE FROM users WHERE id = $1", otherUserID)
	})

	var otherFolderID int64
	if err := env.db.QueryRow(
		`INSERT INTO session_folders (user_id, name) VALUES ($1, 'Other') RETURNING id`,
		otherUserID,
	).Scan(&otherFolderID); err != nil {
		t.Fatalf("insert other folder: %v", err)
	}

	w := env.doRequest(http.MethodPost, "/api/v1/sessions", map[string]interface{}{
		"model_id": "gpt-4o-mini",
		"provider": env.channelKey,
		"title":    "Mine",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create session: got %d %s", w.Code, w.Body.String())
	}
	var session model.Session
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatalf("unmarshal session: %v", err)
	}

	w = env.doRequest(http.MethodPatch, fmt.Sprintf("/api/v1/sessions/%d", session.ID), map[string]interface{}{
		"folder_id": otherFolderID,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("cross-user folder move should fail: got %d %s", w.Code, w.Body.String())
	}
}

// --- Messages Tests ---

func TestListMessages_EmptySession(t *testing.T) {
	env := setupTestEnv(t)

	// Create session
	w := env.doRequest(http.MethodPost, "/api/v1/sessions", map[string]interface{}{
		"model_id": "gpt-4o-mini",
		"provider": env.channelKey,
	})
	var session model.Session
	json.Unmarshal(w.Body.Bytes(), &session)

	// List messages (should be empty)
	w = env.doRequest(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%d/messages", session.ID), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list messages: want 200, got %d", w.Code)
	}
	var msgResp struct {
		Messages []interface{} `json:"messages"`
		Total    int           `json:"total"`
	}
	json.Unmarshal(w.Body.Bytes(), &msgResp)
	if msgResp.Total != 0 {
		t.Errorf("empty session: want 0 messages, got %d", msgResp.Total)
	}
}
