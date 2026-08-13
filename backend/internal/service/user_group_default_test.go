package service

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/huoguojun123/EffChat/internal/repository"
)

func TestUserGroupService_PreservesDefaultGroupInvariant(t *testing.T) {
	db := setupMessageTestDB(t)
	defer db.Close()

	repo := repository.NewUserGroupRepository(db)
	svc := NewUserGroupService(repo)
	suffix := time.Now().UnixNano()
	first, err := svc.Create(&CreateGroupRequest{Name: fmt.Sprintf("group-default-first-%d", suffix), IsDefault: true, DailyMessageLimit: 10})
	if err != nil {
		t.Fatalf("create first default: %v", err)
	}
	second, err := svc.Create(&CreateGroupRequest{Name: fmt.Sprintf("group-default-second-%d", suffix), IsDefault: true, DailyMessageLimit: 20})
	if err != nil {
		t.Fatalf("replace default: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM user_groups WHERE id IN ($1, $2)", first.ID, second.ID)
	})

	storedFirst, err := repo.Get(first.ID)
	if err != nil {
		t.Fatalf("reload first group: %v", err)
	}
	if storedFirst.IsDefault {
		t.Fatal("previous default remained enabled")
	}

	unset := false
	_, err = svc.Update(second.ID, &UpdateGroupRequest{IsDefault: &unset})
	if !errors.Is(err, repository.ErrDefaultUserGroupRequired) {
		t.Fatalf("unset final default error = %v, want ErrDefaultUserGroupRequired", err)
	}
	if err := svc.Delete(second.ID); !errors.Is(err, repository.ErrDefaultUserGroupRequired) {
		t.Fatalf("delete final default error = %v, want ErrDefaultUserGroupRequired", err)
	}

	if err := svc.Delete(first.ID); err != nil {
		t.Fatalf("delete non-default group: %v", err)
	}
}
