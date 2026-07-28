package repository

import (
	"testing"

	"github.com/huoguojun123/effchat/internal/model"
)

// seedModel 插入一个测试模型并注册清理。
func seedModel(t *testing.T, repo *ModelRepository, id string, enabled bool, minLevel int) {
	t.Helper()
	m := &model.Model{
		ID:            id,
		DisplayName:   id,
		Provider:      "test",
		Enabled:       enabled,
		MinGroupLevel: minLevel,
		SortOrder:     0,
	}
	if err := repo.Upsert(m); err != nil {
		t.Fatalf("seed model %s: %v", id, err)
	}
	t.Cleanup(func() { _, _ = repo.db.Exec("DELETE FROM models WHERE id = $1", id) })
}

func TestModelRepository_ListVisible(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repo := NewModelRepository(db)
	prefix := "zz_vis_"
	seedModel(t, repo, prefix+"public", true, 0)     // 所有人可见
	seedModel(t, repo, prefix+"pro", true, 50)       // level>=50 可见
	seedModel(t, repo, prefix+"internal", true, 100) // level>=100 可见
	seedModel(t, repo, prefix+"disabled", false, 0)  // 禁用，任何级别都不可见

	// 统计本测试 prefix 下、level <= maxLevel 的可见模型数量。
	countVisible := func(maxLevel int) int {
		models, err := repo.ListVisible(maxLevel)
		if err != nil {
			t.Fatalf("ListVisible(%d): %v", maxLevel, err)
		}
		n := 0
		for _, m := range models {
			if len(m.ID) >= len(prefix) && m.ID[:len(prefix)] == prefix {
				n++
			}
		}
		return n
	}

	cases := []struct {
		level int
		want  int
	}{
		{level: 0, want: 1},   // 仅 public
		{level: 50, want: 2},  // public + pro
		{level: 100, want: 3}, // public + pro + internal
	}
	for _, tc := range cases {
		if got := countVisible(tc.level); got != tc.want {
			t.Errorf("level=%d: want %d visible, got %d", tc.level, tc.want, got)
		}
	}

	// disabled 模型在最高级别下也不应出现。
	models, _ := repo.ListVisible(1000)
	for _, m := range models {
		if m.ID == prefix+"disabled" {
			t.Errorf("disabled model should never be visible")
		}
	}
}

func TestUserGroupRepository_CRUD(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repo := NewUserGroupRepository(db)
	name := "zz_test_group"
	_, _ = db.Exec("DELETE FROM user_groups WHERE name = $1", name)

	g := &model.UserGroup{Name: name, Level: 42, Description: "tmp", IsDefault: false}
	if err := repo.Create(g); err != nil {
		t.Fatalf("create group: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM user_groups WHERE id = $1", g.ID) })
	if g.ID == 0 {
		t.Fatal("expected group ID to be set")
	}

	got, err := repo.Get(g.ID)
	if err != nil || got == nil {
		t.Fatalf("get group: %v (got=%v)", err, got)
	}
	if got.Level != 42 {
		t.Errorf("want level 42, got %d", got.Level)
	}

	got.Level = 7
	if err := repo.Update(got); err != nil {
		t.Fatalf("update group: %v", err)
	}
	after, _ := repo.Get(g.ID)
	if after.Level != 7 {
		t.Errorf("want updated level 7, got %d", after.Level)
	}

	if err := repo.Delete(g.ID); err != nil {
		t.Fatalf("delete group: %v", err)
	}
	gone, _ := repo.Get(g.ID)
	if gone != nil {
		t.Errorf("expected group deleted, still present: %v", gone)
	}
}

// TestUserRepository_GroupLevel 验证用户组等级解析：未分组→0，分组后→组 level。
func TestUserRepository_GroupLevel(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	userRepo := NewUserRepository(db)
	groupRepo := NewUserGroupRepository(db)

	uname := "zz_grp_user"
	_, _ = db.Exec("DELETE FROM users WHERE username = $1", uname)
	u := &model.User{Username: uname, PasswordHash: "x", Role: "user", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`)}
	if err := userRepo.Create(u); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM users WHERE id = $1", u.ID) })

	// 未分组 → level 0
	if lv, err := userRepo.GetGroupLevel(u.ID); err != nil || lv != 0 {
		t.Fatalf("ungrouped level: want 0 err nil, got %d err %v", lv, err)
	}

	g := &model.UserGroup{Name: "zz_grp_lvl", Level: 88}
	_, _ = db.Exec("DELETE FROM user_groups WHERE name = $1", g.Name)
	if err := groupRepo.Create(g); err != nil {
		t.Fatalf("create group: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM user_groups WHERE id = $1", g.ID) })

	if err := userRepo.SetGroup(u.ID, &g.ID); err != nil {
		t.Fatalf("set group: %v", err)
	}
	if lv, err := userRepo.GetGroupLevel(u.ID); err != nil || lv != 88 {
		t.Fatalf("grouped level: want 88 err nil, got %d err %v", lv, err)
	}

	// 清空分组 → 回落 0
	if err := userRepo.SetGroup(u.ID, nil); err != nil {
		t.Fatalf("clear group: %v", err)
	}
	if lv, _ := userRepo.GetGroupLevel(u.ID); lv != 0 {
		t.Errorf("after clear: want 0, got %d", lv)
	}
}
