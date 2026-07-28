package repository

import (
	"errors"
	"testing"

	"github.com/huoguojun123/effchat/internal/model"
)

func TestMessageRepository_MarkAsCompressed_And_Filter(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	userRepo := NewUserRepository(db)
	user := &model.User{
		Username:     "ctest1",
		PasswordHash: "x",
		Role:         "user",
		IsActive:     true,
		Permissions:  []byte(`{}`),
		Preferences:  []byte(`{}`),
	}
	if err := userRepo.Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	defer db.Exec("DELETE FROM users WHERE id = $1", user.ID)

	sessionRepo := NewSessionRepository(db)
	session := &model.Session{
		UserID: user.ID, Title: "ct1", ModelID: "m", Provider: "p",
		MessageFormat: "v1", Metadata: []byte(`{}`),
	}
	if err := sessionRepo.Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	defer db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)

	msgRepo := NewMessageRepository(db)

	// 创建 4 条普通消息
	for i, role := range []string{"user", "assistant", "user", "assistant"} {
		msg := &model.Message{
			SessionID:     session.ID,
			SchemaVersion: "v1",
			MessageData:   []byte(`{"role":"` + role + `","content":"` + string(rune('a'+i)) + `"}`),
		}
		if err := msgRepo.Create(msg); err != nil {
			t.Fatalf("create msg %d: %v", i, err)
		}
	}
	defer db.Exec("DELETE FROM messages WHERE session_id = $1", session.ID)

	// 创建摘要消息（会成为检查点）
	summary := &model.Message{
		SessionID:     session.ID,
		SchemaVersion: "v1",
		MessageData:   []byte(`{"role":"user","content":"[summary]","extra":{"_eino_summarization_content_type":"summary"}}`),
	}
	if err := msgRepo.Create(summary); err != nil {
		t.Fatalf("create summary: %v", err)
	}

	// 标记摘要之前的 4 条消息为已压缩。
	if err := msgRepo.MarkAsCompressed(session.ID, summary.ID, summary.ID); err != nil {
		t.Fatalf("MarkAsCompressed: %v", err)
	}

	// 再添加一条新消息（检查点之后）
	newMsg := &model.Message{
		SessionID:     session.ID,
		SchemaVersion: "v1",
		MessageData:   []byte(`{"role":"user","content":"new message after checkpoint"}`),
	}
	if err := msgRepo.Create(newMsg); err != nil {
		t.Fatalf("create new msg: %v", err)
	}

	// ListBySession (agent path): 应返回 summary + newMsg，不含 4 条已压缩消息
	agentMsgs, err := msgRepo.ListBySession(session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	if len(agentMsgs) != 2 {
		t.Errorf("agent path: want 2 messages (summary + new), got %d", len(agentMsgs))
		for _, m := range agentMsgs {
			t.Logf("  id=%d role=%s", m.ID, m.Role)
		}
	}
	if agentMsgs[0].ID != summary.ID {
		t.Errorf("agent path: first message should be summary (id=%d), got id=%d", summary.ID, agentMsgs[0].ID)
	}
	if agentMsgs[1].ID != newMsg.ID {
		t.Errorf("agent path: second message should be new (id=%d), got id=%d", newMsg.ID, agentMsgs[1].ID)
	}

	// ListAllBySession (frontend path): 应返回所有 6 条（4 压缩 + summary + new）
	allMsgs, err := msgRepo.ListAllBySession(session.ID)
	if err != nil {
		t.Fatalf("ListAllBySession: %v", err)
	}
	if len(allMsgs) != 6 {
		t.Errorf("frontend path: want 6 messages, got %d", len(allMsgs))
	}
}

func TestMessageRepository_UndoCompressionCheckpoint(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	userRepo := NewUserRepository(db)
	user := &model.User{
		Username: "ctest_undo", PasswordHash: "x", Role: "user",
		IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`),
	}
	if err := userRepo.Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	defer db.Exec("DELETE FROM users WHERE id = $1", user.ID)

	sessionRepo := NewSessionRepository(db)
	session := &model.Session{
		UserID: user.ID, Title: "undo", ModelID: "m", Provider: "p",
		MessageFormat: "v1", Metadata: []byte(`{}`),
	}
	if err := sessionRepo.Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	defer db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)

	msgRepo := NewMessageRepository(db)
	defer db.Exec("DELETE FROM messages WHERE session_id = $1", session.ID)

	// 2 条历史消息 + 摘要 + 标记压缩。
	for i, role := range []string{"user", "assistant"} {
		msg := &model.Message{
			SessionID: session.ID, SchemaVersion: "v1",
			MessageData: []byte(`{"role":"` + role + `","content":"` + string(rune('a'+i)) + `"}`),
		}
		if err := msgRepo.Create(msg); err != nil {
			t.Fatalf("create msg %d: %v", i, err)
		}
	}
	summary := &model.Message{
		SessionID: session.ID, SchemaVersion: "v1",
		MessageData: []byte(`{"role":"user","content":"[summary]"}`),
	}
	if err := msgRepo.Create(summary); err != nil {
		t.Fatalf("create summary: %v", err)
	}
	if err := msgRepo.MarkAsCompressed(session.ID, summary.ID, summary.ID); err != nil {
		t.Fatalf("MarkAsCompressed: %v", err)
	}

	// 撤销：应恢复 2 条历史、软删摘要。
	restored, err := msgRepo.UndoCompressionCheckpoint(session.ID, summary.ID)
	if err != nil {
		t.Fatalf("UndoCompressionCheckpoint: %v", err)
	}
	if restored != 2 {
		t.Errorf("restored = %d, want 2", restored)
	}

	// 撤销后 agent path 应返回 2 条历史（摘要已软删，无压缩标记）。
	msgs, err := msgRepo.ListBySession(session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	if len(msgs) != 2 {
		t.Errorf("after undo: want 2 messages, got %d", len(msgs))
		for _, m := range msgs {
			t.Logf("  id=%d role=%s", m.ID, m.Role)
		}
	}
	for _, m := range msgs {
		if m.ID == summary.ID {
			t.Error("summary should be soft-deleted after undo")
		}
	}
}

func TestMessageRepository_GetByID_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	msgRepo := NewMessageRepository(db)
	_, err := msgRepo.GetByID(999999999)
	if err == nil {
		t.Fatal("expected error for non-existent ID")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestUserRepository_GetByUsername_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repo := NewUserRepository(db)
	_, err := repo.GetByUsername("this_user_does_not_exist_xyz")
	if err == nil {
		t.Fatal("expected error for non-existent username")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestSessionRepository_GetByID_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repo := NewSessionRepository(db)
	_, err := repo.GetByID(999999999, 1)
	if err == nil {
		t.Fatal("expected error for non-existent session")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}
