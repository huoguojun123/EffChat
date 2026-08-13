package repository

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/testutil"
)

func TestPromptCatalogHasNoUnusedPopularityState(t *testing.T) {
	db := testutil.OpenPostgresTestDB(t)
	defer db.Close()

	var useCountExists bool
	if err := db.QueryRow(`
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = 'prompts' AND column_name = 'use_count'
		)
	`).Scan(&useCountExists); err != nil {
		t.Fatalf("inspect prompts schema: %v", err)
	}
	if useCountExists {
		t.Fatal("prompts.use_count still exists after current migrations")
	}

	suffix := time.Now().UnixNano()
	owner := &model.User{
		Username: fmt.Sprintf("prompt_catalog_%d", suffix), PasswordHash: "fixture-hash",
		Role: "admin", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`),
	}
	if err := NewUserRepository(db).Create(owner); err != nil {
		t.Fatalf("create prompt owner: %v", err)
	}
	repo := NewPromptRepository(db)
	older := &model.Prompt{UserID: owner.ID, Title: "Older", Content: "fixture older", Tags: []string{}, IsPublic: true}
	newer := &model.Prompt{UserID: owner.ID, Title: "Newer", Content: "fixture newer", Tags: []string{}, IsPublic: true}
	for _, prompt := range []*model.Prompt{older, newer} {
		if err := repo.Create(prompt); err != nil {
			t.Fatalf("create prompt %q: %v", prompt.Title, err)
		}
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM prompts WHERE id IN ($1, $2)", older.ID, newer.ID)
		_, _ = db.Exec("DELETE FROM users WHERE id = $1", owner.ID)
	})

	if _, err := db.Exec(`UPDATE prompts SET content = content WHERE id = $1`, older.ID); err != nil {
		t.Fatalf("touch older prompt: %v", err)
	}
	if _, err := db.Exec(`SELECT pg_sleep(0.01)`); err != nil {
		t.Fatalf("separate prompt update timestamps: %v", err)
	}
	if _, err := db.Exec(`UPDATE prompts SET content = content WHERE id = $1`, newer.ID); err != nil {
		t.Fatalf("touch newer prompt: %v", err)
	}
	listed, err := repo.ListPublic(10, 0)
	if err != nil {
		t.Fatalf("list public prompts: %v", err)
	}
	if len(listed) < 2 || listed[0].ID != newer.ID || listed[1].ID != older.ID {
		t.Fatalf("public prompt order = %v, want updated_at desc", promptIDs(listed))
	}
	encoded, err := json.Marshal(listed[0])
	if err != nil {
		t.Fatalf("marshal prompt response: %v", err)
	}
	if bytes.Contains(encoded, []byte(`"use_count"`)) {
		t.Fatalf("prompt API still exposes unused popularity state: %s", encoded)
	}
}

func promptIDs(prompts []*model.Prompt) []int64 {
	ids := make([]int64, len(prompts))
	for i, prompt := range prompts {
		ids[i] = prompt.ID
	}
	return ids
}
