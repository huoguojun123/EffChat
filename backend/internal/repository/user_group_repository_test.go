package repository

import (
	"errors"
	"testing"

	"github.com/huoguojun123/EffChat/internal/model"
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

func TestUserRepository_EffectiveGroupContract(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	userRepo := NewUserRepository(db)
	groupRepo := NewUserGroupRepository(db)
	quotaRepo := NewQuotaRepository(db)

	var originalDefaultID int64
	if err := db.QueryRow(`SELECT id FROM user_groups WHERE is_default = true`).Scan(&originalDefaultID); err != nil {
		t.Fatalf("load original default group: %v", err)
	}

	groups := []*model.UserGroup{
		{Name: "zz_effective_default_a", Level: 7, IsDefault: false, DailyMessageLimit: 17},
		{Name: "zz_effective_default_b", Level: 19, IsDefault: false, DailyMessageLimit: 29},
		{Name: "zz_effective_explicit", Level: 41, IsDefault: false, DailyMessageLimit: 43},
	}
	for _, group := range groups {
		_, _ = db.Exec("DELETE FROM user_groups WHERE name = $1", group.Name)
		if err := groupRepo.Create(group); err != nil {
			t.Fatalf("create group %s: %v", group.Name, err)
		}
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`UPDATE user_groups SET is_default = false WHERE is_default = true`)
		_, _ = db.Exec(`UPDATE user_groups SET is_default = true WHERE id = $1`, originalDefaultID)
		for _, group := range groups {
			_, _ = db.Exec("DELETE FROM user_groups WHERE id = $1", group.ID)
		}
	})

	uname := "zz_grp_user"
	_, _ = db.Exec("DELETE FROM users WHERE username = $1", uname)
	u := &model.User{Username: uname, PasswordHash: "x", Role: "user", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`)}
	if err := userRepo.Create(u); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM users WHERE id = $1", u.ID) })

	setDefault := func(group *model.UserGroup) {
		t.Helper()
		group.IsDefault = true
		if err := groupRepo.Update(group); err != nil {
			t.Fatalf("set default group %s: %v", group.Name, err)
		}
	}
	assertEffective := func(want *model.UserGroup) {
		t.Helper()
		got, err := userRepo.GetEffectiveGroupContext(t.Context(), u.ID)
		if err != nil {
			t.Fatalf("get effective group: %v", err)
		}
		if got.ID != want.ID || got.Name != want.Name || got.Level != want.Level {
			t.Fatalf("effective group = %+v, want id=%d name=%q level=%d", got, want.ID, want.Name, want.Level)
		}
		level, err := userRepo.GetGroupLevel(u.ID)
		if err != nil || level != want.Level {
			t.Fatalf("effective level = %d err=%v, want %d", level, err, want.Level)
		}
		limits, err := quotaRepo.LimitsForUser(t.Context(), u.ID)
		if err != nil || limits.DailyMessageLimit != want.DailyMessageLimit {
			t.Fatalf("effective message limit = %d err=%v, want %d", limits.DailyMessageLimit, err, want.DailyMessageLimit)
		}
		adminUser, err := userRepo.GetByIDIncludeInactive(u.ID)
		if err != nil {
			t.Fatalf("load administrator user view: %v", err)
		}
		if adminUser.EffectiveGroup == nil || adminUser.EffectiveGroup.ID != want.ID || adminUser.EffectiveGroup.Name != want.Name || adminUser.EffectiveGroup.Level != want.Level {
			t.Fatalf("administrator effective group = %+v, want %+v", adminUser.EffectiveGroup, want)
		}
	}

	// NULL users inherit both permissions and quotas from the current default.
	setDefault(groups[0])
	assertEffective(groups[0])

	groups[0].Name = "zz_effective_default_a_updated"
	groups[0].Level = 11
	groups[0].DailyMessageLimit = 23
	if err := groupRepo.Update(groups[0]); err != nil {
		t.Fatalf("update active default group: %v", err)
	}
	assertEffective(groups[0])

	setDefault(groups[1])
	assertEffective(groups[1])

	// An explicit assignment remains stable when the default changes.
	if err := userRepo.SetGroup(u.ID, &groups[2].ID); err != nil {
		t.Fatalf("set explicit group: %v", err)
	}
	assertEffective(groups[2])
	setDefault(groups[0])
	assertEffective(groups[2])

	// ON DELETE SET NULL returns the user to dynamic default inheritance.
	if err := groupRepo.Delete(groups[2].ID); err != nil {
		t.Fatalf("delete explicit group: %v", err)
	}
	groups[2].ID = 0
	assertEffective(groups[0])

	if _, err := userRepo.GetEffectiveGroupContext(t.Context(), -1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing user error = %v, want ErrNotFound", err)
	}
}
