package service

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
)

func TestSessionService_ValidateRunnableModelDeletedChannel(t *testing.T) {
	db := setupMessageTestDB(t)
	defer db.Close()

	suffix := time.Now().UnixNano()
	channelKey := fmt.Sprintf("runtime-channel-%d", suffix)
	modelID := fmt.Sprintf("runtime-model-%d", suffix)

	userRepo := repository.NewUserRepository(db)
	user := &model.User{
		Username:     fmt.Sprintf("runtime_user_%d", suffix),
		PasswordHash: "x",
		Role:         "user",
		IsActive:     true,
		Permissions:  []byte(`{}`),
		Preferences:  []byte(`{}`),
	}
	if err := userRepo.Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	channelRepo := repository.NewChannelRepository(db)
	channelService := NewChannelService(channelRepo)
	enabled := true
	if _, err := channelService.SaveAIChannel(&AIChannelInput{
		Key:         channelKey,
		DisplayName: "Runtime Channel",
		Adapter:     AdapterOpenAICompatible,
		BaseURL:     "https://example.test/v1",
		APIKey:      "test-key",
		Enabled:     &enabled,
	}); err != nil {
		t.Fatalf("save channel: %v", err)
	}

	modelRepo := repository.NewModelRepository(db)
	if err := modelRepo.Upsert(&model.Model{
		ID:             modelID,
		DisplayName:    "Runtime Model",
		Provider:       channelKey,
		SearchImpl:     "",
		ContextWindow:  1024,
		MaxOutput:      256,
		Enabled:        true,
		MinGroupLevel:  0,
		SortOrder:      1,
		ThinkingFormat: "auto",
	}); err != nil {
		t.Fatalf("save model: %v", err)
	}

	sessionRepo := repository.NewSessionRepository(db)
	messageRepo := repository.NewMessageRepository(db)
	configRepo := repository.NewConfigRepository(db)
	session := &model.Session{
		UserID:        user.ID,
		Title:         "runtime validation",
		ModelID:       modelID,
		Provider:      channelKey,
		MessageFormat: "v2",
		Metadata:      []byte(`{}`),
	}
	if err := sessionRepo.Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM messages WHERE session_id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
		_, _ = db.Exec("DELETE FROM models WHERE id = $1", modelID)
		_, _ = db.Exec("DELETE FROM ai_channels WHERE channel_key = $1", channelKey)
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", user.ID)
	})

	modelService := NewModelService(modelRepo, channelService)
	models, err := modelService.List(false)
	if err != nil {
		t.Fatalf("list models: %v", err)
	}
	var listed *model.Model
	for _, item := range models {
		if item.ID == modelID {
			listed = item
			break
		}
	}
	if listed == nil {
		t.Fatalf("model %q not listed", modelID)
	}
	if listed.ChannelDisplayName != "Runtime Channel" || listed.ChannelAdapter != AdapterOpenAICompatible || !listed.ChannelEnabled || !listed.ChannelConfigured {
		t.Fatalf("channel metadata = name:%q adapter:%q enabled:%v configured:%v", listed.ChannelDisplayName, listed.ChannelAdapter, listed.ChannelEnabled, listed.ChannelConfigured)
	}

	svc := NewSessionService(sessionRepo, messageRepo, configRepo)
	svc.SetRuntimeModelDependencies(modelRepo, channelService, userRepo)
	if err := svc.ValidateRunnableModel(session); err != nil {
		t.Fatalf("validate before delete: %v", err)
	}

	if err := channelService.DeleteAIChannel(channelKey); err != nil {
		t.Fatalf("delete channel: %v", err)
	}
	err = svc.ValidateRunnableModel(session)
	var runtimeErr *RuntimeModelError
	if !errors.As(err, &runtimeErr) {
		t.Fatalf("error = %T %v, want RuntimeModelError", err, err)
	}
	if runtimeErr.Code != "channel_not_configured" {
		t.Fatalf("code = %q, want channel_not_configured", runtimeErr.Code)
	}
}

func TestSessionServiceDeleteCancelsOnlyItsActiveRun(t *testing.T) {
	db := setupMessageTestDB(t)
	defer db.Close()

	suffix := time.Now().UnixNano()
	userRepo := repository.NewUserRepository(db)
	user := &model.User{Username: fmt.Sprintf("delete_run_%d", suffix), PasswordHash: "x", Role: "user", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`)}
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

	hub := NewRunHub(time.Minute, 1<<20)
	firstRun, err := hub.Start(first.ID, user.ID, 0, "delete-first", RunKindChat)
	if err != nil {
		t.Fatalf("start first run: %v", err)
	}
	secondRun, err := hub.Start(second.ID, user.ID, 0, "delete-second", RunKindChat)
	if err != nil {
		t.Fatalf("start second run: %v", err)
	}
	canceled := make(chan struct{}, 1)
	hub.Bind(firstRun.RunID, func() { canceled <- struct{}{} })

	svc := NewSessionService(sessionRepo, repository.NewMessageRepository(db), repository.NewConfigRepository(db))
	svc.SetRunHub(hub)
	if err := svc.Delete(first.ID, user.ID); err != nil {
		t.Fatalf("delete first session: %v", err)
	}
	select {
	case <-canceled:
	default:
		t.Fatal("deleted session run was not canceled")
	}
	if hub.IsCanceled(firstRun.RunID) {
		t.Fatal("cancellation request must not become terminal before the worker finishes")
	}
	if cause := hub.CancelCause(firstRun.RunID); cause != RunCancelSessionDeleted {
		t.Fatalf("deleted session cancel cause = %q", cause)
	}
	if hub.IsCanceled(secondRun.RunID) {
		t.Fatal("other session run should remain active")
	}
	hub.Canceled(firstRun.RunID, nil, nil)
	if !hub.IsCanceled(firstRun.RunID) {
		t.Fatal("deleted session run should reach canceled terminal state")
	}
}
