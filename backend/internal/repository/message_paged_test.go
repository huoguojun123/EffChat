package repository

import (
	"testing"

	"github.com/huoguojun123/effchat/internal/model"
)

func TestMessageRepository_ListBySessionPaged(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	sessionRepo := NewSessionRepository(db)
	msgRepo := NewMessageRepository(db)

	userID := createRepositoryTestUser(t, db, "paged_messages")
	defer db.Exec("DELETE FROM users WHERE id = $1", userID)

	session := &model.Session{
		UserID: userID, Title: "Paged Test", ModelID: "claude-opus-4-7",
		Provider: "anthropic", MessageFormat: "v1", Metadata: []byte(`{}`),
	}
	if err := sessionRepo.Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	defer db.Exec("DELETE FROM sessions WHERE id = $1", session.ID)

	// 顺序插入 7 条，id 单调递增。
	ids := make([]int64, 0, 7)
	for i := 0; i < 7; i++ {
		m := &model.Message{SessionID: session.ID, SchemaVersion: "v1", MessageData: []byte(`{"role":"user","content":"hi"}`)}
		if err := msgRepo.Create(m); err != nil {
			t.Fatalf("create message %d: %v", i, err)
		}
		ids = append(ids, m.ID)
	}

	// 第一页：取最新 3 条 -> 应为 ids[4],ids[5],ids[6]（升序返回），hasMore=true。
	page1, hasMore, err := msgRepo.ListBySessionPaged(session.ID, 3, 0)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 3 || !hasMore {
		t.Fatalf("page1: got len=%d hasMore=%v, want 3/true", len(page1), hasMore)
	}
	if page1[0].ID != ids[4] || page1[2].ID != ids[6] {
		t.Errorf("page1 order wrong: got [%d..%d], want [%d..%d]", page1[0].ID, page1[2].ID, ids[4], ids[6])
	}

	// 第二页：before_id = page1 最旧的 id -> ids[1],ids[2],ids[3]，hasMore=true。
	page2, hasMore, err := msgRepo.ListBySessionPaged(session.ID, 3, page1[0].ID)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 3 || !hasMore {
		t.Fatalf("page2: got len=%d hasMore=%v, want 3/true", len(page2), hasMore)
	}
	if page2[0].ID != ids[1] || page2[2].ID != ids[3] {
		t.Errorf("page2 order wrong: got [%d..%d], want [%d..%d]", page2[0].ID, page2[2].ID, ids[1], ids[3])
	}

	// 第三页：before_id = page2 最旧 -> 只剩 ids[0]，hasMore=false（到头）。
	page3, hasMore, err := msgRepo.ListBySessionPaged(session.ID, 3, page2[0].ID)
	if err != nil {
		t.Fatalf("page3: %v", err)
	}
	if len(page3) != 1 || hasMore {
		t.Fatalf("page3: got len=%d hasMore=%v, want 1/false", len(page3), hasMore)
	}
	if page3[0].ID != ids[0] {
		t.Errorf("page3: got id=%d, want %d", page3[0].ID, ids[0])
	}
}
