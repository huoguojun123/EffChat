package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
)

type promptPageResponse struct {
	Prompts    []json.RawMessage `json:"prompts"`
	Total      int               `json:"total"`
	HasMore    bool              `json:"has_more"`
	NextOffset int               `json:"next_offset"`
}

func TestPromptListPaginationMetadata(t *testing.T) {
	db := setupHandlerTestDB(t)
	suffix := time.Now().UnixNano()
	userRepo := repository.NewUserRepository(db)
	owner := &model.User{Username: fmt.Sprintf("pagination_owner_%d", suffix), PasswordHash: "fixture-hash", Role: "user", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`)}
	admin := &model.User{Username: fmt.Sprintf("pagination_admin_%d", suffix), PasswordHash: "fixture-hash", Role: "admin", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`)}
	for _, user := range []*model.User{owner, admin} {
		if err := userRepo.Create(user); err != nil {
			t.Fatalf("create user: %v", err)
		}
	}

	promptRepo := repository.NewPromptRepository(db)
	prompts := []*model.Prompt{
		{UserID: owner.ID, Title: "Private A", Content: "fixture private a", GroupName: "Fixture", Tags: []string{}, IsPublic: false},
		{UserID: owner.ID, Title: "Private B", Content: "fixture private b", GroupName: "Fixture", Tags: []string{}, IsPublic: false},
		{UserID: admin.ID, Title: "Shared A", Content: "fixture shared a", GroupName: "Fixture", Tags: []string{}, IsPublic: true},
		{UserID: admin.ID, Title: "Shared B", Content: "fixture shared b", GroupName: "Fixture", Tags: []string{}, IsPublic: true},
	}
	for _, prompt := range prompts {
		if err := promptRepo.Create(prompt); err != nil {
			t.Fatalf("create prompt: %v", err)
		}
	}
	t.Cleanup(func() {
		for _, prompt := range prompts {
			_, _ = db.Exec("DELETE FROM prompts WHERE id = $1", prompt.ID)
		}
		_, _ = db.Exec("DELETE FROM users WHERE id IN ($1, $2)", owner.ID, admin.ID)
		_ = db.Close()
	})

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", owner.ID)
		c.Next()
	})
	router.GET("/prompts", ListPromptsHandler(promptRepo))
	router.GET("/prompts/public", ListPublicPromptsHandler(promptRepo))
	router.GET("/admin/prompts", ListSharedPromptsHandler(promptRepo))

	for _, path := range []string{"/prompts", "/prompts/public", "/admin/prompts"} {
		t.Run(path, func(t *testing.T) {
			first := requestPromptPage(t, router, path+"?limit=1&offset=0")
			if len(first.Prompts) != 1 || first.Total != 2 || !first.HasMore || first.NextOffset != 1 {
				t.Fatalf("first page = %+v, want one of two and next offset 1", first)
			}
			last := requestPromptPage(t, router, path+"?limit=1&offset=1")
			if len(last.Prompts) != 1 || last.Total != 2 || last.HasMore || last.NextOffset != 2 {
				t.Fatalf("last page = %+v, want final item and next offset 2", last)
			}
		})
	}
}

func requestPromptPage(t *testing.T, router *gin.Engine, path string) promptPageResponse {
	t.Helper()
	recorder := doPromptRequest(router, http.MethodGet, path, 0, "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET %s: status=%d body=%s", path, recorder.Code, recorder.Body.String())
	}
	var page promptPageResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode GET %s: %v", path, err)
	}
	return page
}
