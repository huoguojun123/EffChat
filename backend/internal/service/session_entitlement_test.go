package service

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/huoguojun123/effchat/internal/model"
	"github.com/huoguojun123/effchat/internal/repository"
)

func TestSessionService_EnforcesModelEntitlementsAtSessionBoundaries(t *testing.T) {
	db := setupMessageTestDB(t)
	defer db.Close()

	suffix := time.Now().UnixNano()
	channelKey := fmt.Sprintf("entitlement-channel-%d", suffix)
	publicModelID := fmt.Sprintf("entitlement-public-%d", suffix)
	restrictedModelID := fmt.Sprintf("entitlement-restricted-%d", suffix)

	userRepo := repository.NewUserRepository(db)
	regularUser := createEntitlementTestUser(t, userRepo, suffix, "user")
	adminUser := createEntitlementTestUser(t, userRepo, suffix+1, "admin")

	channelRepo := repository.NewChannelRepository(db)
	channelService := NewChannelService(channelRepo)
	enabled := true
	if _, err := channelService.SaveAIChannel(&AIChannelInput{
		Key:         channelKey,
		DisplayName: "Entitlement Channel",
		Adapter:     AdapterOpenAICompatible,
		BaseURL:     "https://example.test/v1",
		APIKey:      "test-key",
		Enabled:     &enabled,
	}); err != nil {
		t.Fatalf("save channel: %v", err)
	}

	modelRepo := repository.NewModelRepository(db)
	for _, item := range []*model.Model{
		{ID: publicModelID, DisplayName: "Public", Provider: channelKey, ContextWindow: 1024, MaxOutput: 256, Enabled: true, MinGroupLevel: 0, ThinkingFormat: "auto"},
		{ID: restrictedModelID, DisplayName: "Restricted", Provider: channelKey, ContextWindow: 1024, MaxOutput: 256, Enabled: true, MinGroupLevel: 10, ThinkingFormat: "auto"},
	} {
		if err := modelRepo.Upsert(item); err != nil {
			t.Fatalf("save model %q: %v", item.ID, err)
		}
	}

	sessionRepo := repository.NewSessionRepository(db)
	svc := NewSessionService(sessionRepo, repository.NewMessageRepository(db), repository.NewConfigRepository(db))
	svc.SetRuntimeModelDependencies(modelRepo, channelService, userRepo)

	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM sessions WHERE user_id IN ($1, $2)", regularUser.ID, adminUser.ID)
		_, _ = db.Exec("DELETE FROM models WHERE id IN ($1, $2)", publicModelID, restrictedModelID)
		_, _ = db.Exec("DELETE FROM ai_channels WHERE channel_key = $1", channelKey)
		_, _ = db.Exec("DELETE FROM users WHERE id IN ($1, $2)", regularUser.ID, adminUser.ID)
	})

	_, err := svc.Create(regularUser.ID, &CreateSessionRequest{ModelID: restrictedModelID, Provider: channelKey})
	assertRuntimeModelErrorCode(t, err, "model_access_denied")

	regularSession, err := svc.Create(regularUser.ID, &CreateSessionRequest{ModelID: publicModelID, Provider: channelKey})
	if err != nil {
		t.Fatalf("create public session: %v", err)
	}
	if err := svc.Update(regularSession.ID, regularUser.ID, &UpdateSessionRequest{ModelID: &restrictedModelID, Provider: &channelKey}); err == nil {
		t.Fatal("update to restricted model succeeded")
	} else {
		assertRuntimeModelErrorCode(t, err, "model_access_denied")
	}
	stored, err := sessionRepo.GetByID(regularSession.ID, regularUser.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if stored.ModelID != publicModelID || stored.Provider != channelKey {
		t.Fatalf("restricted update persisted: model=%q provider=%q", stored.ModelID, stored.Provider)
	}

	restrictedSession := *regularSession
	restrictedSession.ModelID = restrictedModelID
	restrictedSession.Provider = channelKey
	assertRuntimeModelErrorCode(t, svc.ValidateRunnableModelForUser(&restrictedSession, regularUser.ID), "model_access_denied")
	if err := svc.ValidateRunnableModelForUser(&restrictedSession, adminUser.ID); err != nil {
		t.Fatalf("admin cannot use enabled restricted model: %v", err)
	}
}

func TestSessionService_ValidatesGenerationParametersAgainstModelLimits(t *testing.T) {
	db := setupMessageTestDB(t)
	defer db.Close()

	suffix := time.Now().UnixNano()
	channelKey := fmt.Sprintf("parameter-channel-%d", suffix)
	modelID := fmt.Sprintf("parameter-model-%d", suffix)
	userRepo := repository.NewUserRepository(db)
	user := createEntitlementTestUser(t, userRepo, suffix, "user")
	channelService := NewChannelService(repository.NewChannelRepository(db))
	enabled := true
	if _, err := channelService.SaveAIChannel(&AIChannelInput{Key: channelKey, DisplayName: "Parameter Channel", Adapter: AdapterOpenAICompatible, BaseURL: "https://example.test/v1", APIKey: "test-key", Enabled: &enabled}); err != nil {
		t.Fatalf("save channel: %v", err)
	}
	modelRepo := repository.NewModelRepository(db)
	if err := modelRepo.Upsert(&model.Model{ID: modelID, DisplayName: "Parameter", Provider: channelKey, ContextWindow: 1024, MaxOutput: 256, Enabled: true, MinGroupLevel: 0, ThinkingFormat: "auto"}); err != nil {
		t.Fatalf("save model: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM sessions WHERE user_id = $1", user.ID)
		_, _ = db.Exec("DELETE FROM models WHERE id = $1", modelID)
		_, _ = db.Exec("DELETE FROM ai_channels WHERE channel_key = $1", channelKey)
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", user.ID)
	})

	svc := NewSessionService(repository.NewSessionRepository(db), repository.NewMessageRepository(db), repository.NewConfigRepository(db))
	svc.SetRuntimeModelDependencies(modelRepo, channelService, userRepo)
	tooHighTemperature := 2.1
	tooManyTokens := 257
	if _, err := svc.Create(user.ID, &CreateSessionRequest{ModelID: modelID, Provider: channelKey, Temperature: &tooHighTemperature}); err == nil {
		t.Fatal("out-of-range temperature was accepted")
	}
	if _, err := svc.Create(user.ID, &CreateSessionRequest{ModelID: modelID, Provider: channelKey, MaxTokens: &tooManyTokens}); err == nil {
		t.Fatal("out-of-range max_tokens was accepted")
	}
	zero := 0.0
	maxTokens := 256
	session, err := svc.Create(user.ID, &CreateSessionRequest{ModelID: modelID, Provider: channelKey, Temperature: &zero, MaxTokens: &maxTokens})
	if err != nil {
		t.Fatalf("create valid session: %v", err)
	}
	if err := modelRepo.Upsert(&model.Model{ID: modelID, DisplayName: "Parameter", Provider: channelKey, ContextWindow: 1024, MaxOutput: 128, Enabled: true, MinGroupLevel: 0, ThinkingFormat: "auto"}); err != nil {
		t.Fatalf("lower model output: %v", err)
	}
	if err := svc.ValidateRunnableModelForUser(session, user.ID); err == nil {
		t.Fatal("existing oversized max_tokens remained runnable after model limit changed")
	}
}

func createEntitlementTestUser(t *testing.T, userRepo *repository.UserRepository, suffix int64, role string) *model.User {
	t.Helper()
	user := &model.User{
		Username:     fmt.Sprintf("entitlement_%s_%d", role, suffix),
		PasswordHash: "x",
		Role:         role,
		IsActive:     true,
		Permissions:  []byte(`{}`),
		Preferences:  []byte(`{}`),
	}
	if err := userRepo.Create(user); err != nil {
		t.Fatalf("create %s: %v", role, err)
	}
	return user
}

func assertRuntimeModelErrorCode(t *testing.T, err error, want string) {
	t.Helper()
	var runtimeErr *RuntimeModelError
	if !errors.As(err, &runtimeErr) {
		t.Fatalf("error = %T %v, want RuntimeModelError", err, err)
	}
	if runtimeErr.Code != want {
		t.Fatalf("code = %q, want %q", runtimeErr.Code, want)
	}
}
