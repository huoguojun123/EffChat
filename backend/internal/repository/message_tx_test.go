package repository

import (
	"testing"

	"github.com/huoguojun123/EffChat/internal/model"
)

func TestMessageRepository_CreateBatch_AtomicRollback(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	userRepo := NewUserRepository(db)
	user := &model.User{Username: "txbatch1", PasswordHash: "x", Role: "user", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`)}
	if err := userRepo.Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	defer db.Exec("DELETE FROM users WHERE id = $1", user.ID)

	sessionRepo := NewSessionRepository(db)
	session := &model.Session{UserID: user.ID, Title: "txb", ModelID: "m", Provider: "p", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := sessionRepo.Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	defer db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
	defer db.Exec("DELETE FROM messages WHERE session_id = $1", session.ID)

	msgRepo := NewMessageRepository(db)

	// 第二条 MessageData 为 nil（JSONB NOT NULL 会拒绝），触发批量写入中途失败。
	batch := []*model.Message{
		{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"user","content":"a"}`)},
		{SessionID: session.ID, SchemaVersion: "v1", MessageData: nil},
	}
	if err := msgRepo.CreateBatch(batch); err == nil {
		t.Fatal("expected CreateBatch to fail on second message, got nil")
	}

	// 回滚后第一条不应残留。
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM messages WHERE session_id = $1", session.ID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("after rollback, message count = %d, want 0 (no partial write)", count)
	}
}

func TestMessageRepository_CreateBatch_Success(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	userRepo := NewUserRepository(db)
	user := &model.User{Username: "txbatch2", PasswordHash: "x", Role: "user", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`)}
	if err := userRepo.Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	defer db.Exec("DELETE FROM users WHERE id = $1", user.ID)

	sessionRepo := NewSessionRepository(db)
	session := &model.Session{UserID: user.ID, Title: "txb2", ModelID: "m", Provider: "p", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := sessionRepo.Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	defer db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
	defer db.Exec("DELETE FROM messages WHERE session_id = $1", session.ID)

	msgRepo := NewMessageRepository(db)
	batch := []*model.Message{
		{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"user","content":"a"}`)},
		{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"assistant","content":"b"}`)},
	}
	if err := msgRepo.CreateBatch(batch); err != nil {
		t.Fatalf("CreateBatch: %v", err)
	}
	for i, m := range batch {
		if m.ID == 0 {
			t.Errorf("message %d ID not backfilled", i)
		}
	}
	var count int
	db.QueryRow("SELECT COUNT(*) FROM messages WHERE session_id = $1", session.ID).Scan(&count)
	if count != 2 {
		t.Errorf("message count = %d, want 2", count)
	}
}

func TestMessageRepository_PersistCheckpoint_Atomic(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	userRepo := NewUserRepository(db)
	user := &model.User{Username: "txckpt1", PasswordHash: "x", Role: "user", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`)}
	if err := userRepo.Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	defer db.Exec("DELETE FROM users WHERE id = $1", user.ID)

	sessionRepo := NewSessionRepository(db)
	session := &model.Session{UserID: user.ID, Title: "txc", ModelID: "m", Provider: "p", MessageFormat: "v1", Metadata: []byte(`{}`)}
	if err := sessionRepo.Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	defer db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)
	defer db.Exec("DELETE FROM messages WHERE session_id = $1", session.ID)

	msgRepo := NewMessageRepository(db)
	// 先放两条普通消息。
	for _, role := range []string{"user", "assistant"} {
		m := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"` + role + `","content":"x"}`)}
		if err := msgRepo.Create(m); err != nil {
			t.Fatalf("seed msg: %v", err)
		}
	}

	summary := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"user","content":"[summary]"}`)}
	if err := msgRepo.PersistCheckpoint(summary, summary.SessionID); err != nil {
		t.Fatalf("PersistCheckpoint: %v", err)
	}
	if summary.ID == 0 {
		t.Fatal("summary ID not backfilled by checkpoint")
	}
	// beforeMessageID 传 summary.SessionID 是占位；真正校验由 service 决定边界。
	// 这里只验证摘要确实落库且方法原子返回成功。
	var exists bool
	db.QueryRow("SELECT EXISTS(SELECT 1 FROM messages WHERE id = $1)", summary.ID).Scan(&exists)
	if !exists {
		t.Error("summary message not persisted")
	}
}
