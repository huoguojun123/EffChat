package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/testutil"
)

func setupTestDB(t *testing.T) *sql.DB {
	return testutil.OpenPostgresTestDB(t)
}

func createRepositoryTestUser(t *testing.T, db *sql.DB, name string) int64 {
	t.Helper()

	repo := NewUserRepository(db)
	user := &model.User{
		Username:     fmt.Sprintf("repo_%s_%x", name, time.Now().UnixNano()),
		PasswordHash: "hashed_password",
		Role:         "user",
		IsActive:     true,
		Permissions:  []byte(`{}`),
		Preferences:  []byte(`{}`),
	}
	if err := repo.Create(user); err != nil {
		t.Fatalf("failed to create repository test user: %v", err)
	}

	return user.ID
}

func TestUserRepository_Create(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repo := NewUserRepository(db)

	user := &model.User{
		Username:     "testuser",
		PasswordHash: "hashed_password",
		Role:         "user",
		IsActive:     true,
		Permissions:  []byte(`{}`),
		Preferences:  []byte(`{}`),
	}

	err := repo.Create(user)
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	if user.ID == 0 {
		t.Error("expected user ID to be set")
	}

	t.Logf("Created user with ID: %d", user.ID)

	// 清理
	_, _ = db.Exec("DELETE FROM users WHERE id = $1", user.ID)
}

func TestUserRepository_GetByUsername(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repo := NewUserRepository(db)

	// 创建测试用户
	user := &model.User{
		Username:     "testuser2",
		PasswordHash: "hashed_password",
		Role:         "user",
		IsActive:     true,
		Permissions:  []byte(`{}`),
		Preferences:  []byte(`{}`),
	}

	if err := repo.Create(user); err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// 测试获取
	found, err := repo.GetByUsername("testuser2")
	if err != nil {
		t.Fatalf("failed to get user: %v", err)
	}

	if found.Username != "testuser2" {
		t.Errorf("expected username %s, got %s", "testuser2", found.Username)
	}

	t.Logf("Found user: %+v", found)

	// 清理
	_, _ = db.Exec("DELETE FROM users WHERE id = $1", user.ID)
}

func TestUserRepository_UpdatePasswordInvalidatesAuthVersion(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repo := NewUserRepository(db)
	user := &model.User{
		Username:     fmt.Sprintf("password_version_%d", time.Now().UnixNano()),
		PasswordHash: "old-hash",
		Role:         "user",
		IsActive:     true,
		Permissions:  []byte(`{}`),
		Preferences:  []byte(`{}`),
	}
	if err := repo.Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM users WHERE id = $1", user.ID) })
	if user.AuthVersion != 1 {
		t.Fatalf("initial auth version = %d, want 1", user.AuthVersion)
	}

	if err := repo.UpdatePassword(user.ID, "new-hash"); err != nil {
		t.Fatalf("update password: %v", err)
	}
	updated, err := repo.GetByID(user.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if updated.AuthVersion != 2 {
		t.Fatalf("auth version = %d, want 2", updated.AuthVersion)
	}
}

func TestUserRepository_CountUsers(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repo := NewUserRepository(db)

	count, err := repo.CountUsers()
	if err != nil {
		t.Fatalf("failed to count users: %v", err)
	}

	t.Logf("Total users: %d", count)

	if count < 0 {
		t.Error("expected non-negative count")
	}
}

func TestSessionRepository_Create(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repo := NewSessionRepository(db)

	userID := createRepositoryTestUser(t, db, "session_create")
	defer db.Exec("DELETE FROM users WHERE id = $1", userID)

	session := &model.Session{
		UserID:        userID,
		Title:         "Test Session",
		ModelID:       "claude-opus-4-7",
		Provider:      "anthropic",
		MessageFormat: "v1",
		Metadata:      []byte(`{}`),
	}

	err := repo.Create(session)
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	if session.ID == 0 {
		t.Error("expected session ID to be set")
	}

	t.Logf("Created session with ID: %d", session.ID)

	// 清理
	_, _ = db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
}

func TestSessionRepository_ListByUser(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repo := NewSessionRepository(db)

	// 使用固定测试用户 ID（id=2）
	sessions, err := repo.ListByUser(2, 10, 0, nil, false)
	if err != nil {
		t.Fatalf("failed to list sessions: %v", err)
	}

	t.Logf("Found %d sessions", len(sessions))

	for _, s := range sessions {
		t.Logf("Session: ID=%d, Title=%s, Model=%s", s.ID, s.Title, s.ModelID)
	}
}

func TestSessionRepositoryFieldUpdatesDoNotOverwriteEachOther(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	userID := createRepositoryTestUser(t, db, "session_fields")
	session := &model.Session{UserID: userID, Title: "新对话", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", SearchMode: "auto", Metadata: []byte(`{"existing":true}`)}
	repo := NewSessionRepository(db)
	if err := repo.Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM users WHERE id = $1", userID) })
	searchMode := "on"
	if err := repo.UpdateFields(session.ID, userID, SessionPatch{SearchMode: &searchMode}); err != nil {
		t.Fatalf("update search mode: %v", err)
	}
	if err := repo.UpdateEnabledSkills(session.ID, userID, []string{"skill-a"}); err != nil {
		t.Fatalf("update skills: %v", err)
	}
	if err := repo.UpdateAutomaticTitle(session.ID, userID, "generated title", true); err != nil {
		t.Fatalf("update title: %v", err)
	}
	stored, err := repo.GetByID(session.ID, userID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if stored.SearchMode != "on" || stored.Title != "generated title" || !stored.TitleGenerated {
		t.Fatalf("stored session lost scalar update: %+v", stored)
	}
	var metadata map[string]interface{}
	if err := json.Unmarshal(stored.Metadata, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["existing"] != true {
		t.Fatalf("metadata existing value was lost: %s", stored.Metadata)
	}
	skills, ok := metadata["skills_enabled"].([]interface{})
	if !ok || len(skills) != 1 || skills[0] != "skill-a" {
		t.Fatalf("skills metadata = %#v", metadata["skills_enabled"])
	}
}

func TestSessionRepositoryLoadsAnswerSelectionRevision(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	userID := createRepositoryTestUser(t, db, "session_answer_revision")
	session := &model.Session{UserID: userID, Title: "answer selection revision", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`)}
	repo := NewSessionRepository(db)
	if err := repo.Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM users WHERE id = $1", userID) })
	if _, err := db.Exec("UPDATE sessions SET answer_selection_revision = 7 WHERE id = $1", session.ID); err != nil {
		t.Fatalf("set answer selection revision: %v", err)
	}

	loaded, err := repo.GetByID(session.ID, userID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if loaded.AnswerSelectionRevision != 7 {
		t.Fatalf("get revision = %d, want 7", loaded.AnswerSelectionRevision)
	}

	listed, err := repo.ListByUser(userID, 10, 0, nil, false)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	for _, candidate := range listed {
		if candidate.ID == session.ID {
			if candidate.AnswerSelectionRevision != 7 {
				t.Fatalf("list revision = %d, want 7", candidate.AnswerSelectionRevision)
			}
			return
		}
	}
	t.Fatalf("created session %d missing from list", session.ID)
}

func TestSessionRepositoryAutomaticTitleDoesNotOverrideCustomTitle(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	userID := createRepositoryTestUser(t, db, "title_guard")
	session := &model.Session{UserID: userID, Title: "新对话", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`)}
	repo := NewSessionRepository(db)
	if err := repo.Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM users WHERE id = $1", userID) })
	custom := "custom title"
	if err := repo.UpdateFields(session.ID, userID, SessionPatch{Title: &custom}); err != nil {
		t.Fatalf("save custom title: %v", err)
	}
	if err := repo.UpdateAutomaticTitle(session.ID, userID, "late generated title", true); err != nil {
		t.Fatalf("write automatic title: %v", err)
	}
	stored, err := repo.GetByID(session.ID, userID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if stored.Title != custom || stored.TitleGenerated {
		t.Fatalf("automatic title overwrote custom value: %+v", stored)
	}
}

func TestSessionRepositoryAutomaticTitleChecksAnswerSelectionRevision(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	userID := createRepositoryTestUser(t, db, "title_answer_revision")
	session := &model.Session{UserID: userID, Title: "新对话", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`)}
	repo := NewSessionRepository(db)
	if err := repo.Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM users WHERE id = $1", userID) })
	if _, err := db.Exec("UPDATE sessions SET answer_selection_revision = 2 WHERE id = $1", session.ID); err != nil {
		t.Fatalf("advance answer selection revision: %v", err)
	}

	updated, err := repo.UpdateAutomaticTitleAtAnswerRevision(context.Background(), session.ID, userID, "stale title", true, 1)
	if err != nil {
		t.Fatalf("reject stale title: %v", err)
	}
	if updated {
		t.Fatal("stale title unexpectedly updated the session")
	}
	stored, err := repo.GetByID(session.ID, userID)
	if err != nil {
		t.Fatalf("get session after stale title: %v", err)
	}
	if stored.Title != "新对话" || stored.TitleGenerated {
		t.Fatalf("stale title changed session: %+v", stored)
	}

	updated, err = repo.UpdateAutomaticTitleAtAnswerRevision(context.Background(), session.ID, userID, "current title", true, 2)
	if err != nil || !updated {
		t.Fatalf("write current title: updated=%v err=%v", updated, err)
	}
}

func TestMessageRepository_ListBySession(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repo := NewMessageRepository(db)

	// 使用测试数据中的会话 ID（session_id=1）
	messages, err := repo.ListBySession(1)
	if err != nil {
		t.Fatalf("failed to list messages: %v", err)
	}

	t.Logf("Found %d messages", len(messages))

	for _, m := range messages {
		data, _ := ParseMessageData(m.MessageData)
		t.Logf("Message: ID=%d, Role=%s, HasToolCalls=%v, Content=%v",
			m.ID, m.Role, m.HasToolCalls, data["content"])
	}
}

func TestMessageRepository_CountBySessions(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	userRepo := NewUserRepository(db)
	user := &model.User{
		Username:     fmt.Sprintf("count_sessions_%d", time.Now().UnixNano()),
		PasswordHash: "x",
		Role:         "user",
		IsActive:     true,
		Permissions:  []byte(`{}`),
		Preferences:  []byte(`{}`),
	}
	if err := userRepo.Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	sessionRepo := NewSessionRepository(db)
	s1 := &model.Session{UserID: user.ID, Title: "s1", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`)}
	s2 := &model.Session{UserID: user.ID, Title: "s2", ModelID: "gpt-4o", Provider: "openai", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := sessionRepo.Create(s1); err != nil {
		t.Fatalf("create s1: %v", err)
	}
	if err := sessionRepo.Create(s2); err != nil {
		t.Fatalf("create s2: %v", err)
	}
	t.Cleanup(func() {
		db.Exec("DELETE FROM messages WHERE session_id IN ($1, $2)", s1.ID, s2.ID)
		db.Exec("DELETE FROM sessions WHERE id IN ($1, $2)", s1.ID, s2.ID)
		db.Exec("DELETE FROM users WHERE id = $1", user.ID)
	})

	msgRepo := NewMessageRepository(db)
	for i := 0; i < 2; i++ {
		raw, _ := json.Marshal(map[string]interface{}{"role": "user", "content": fmt.Sprintf("m%d", i)})
		if err := msgRepo.Create(&model.Message{SessionID: s1.ID, SchemaVersion: "v1", MessageData: raw}); err != nil {
			t.Fatalf("create message: %v", err)
		}
	}

	counts, err := msgRepo.CountBySessions([]int64{s1.ID, s2.ID})
	if err != nil {
		t.Fatalf("CountBySessions: %v", err)
	}
	if counts[s1.ID] != 2 {
		t.Fatalf("s1 count = %d, want 2", counts[s1.ID])
	}
	if _, ok := counts[s2.ID]; ok {
		t.Fatalf("s2 should be omitted from count map when it has no messages: %#v", counts)
	}
}
