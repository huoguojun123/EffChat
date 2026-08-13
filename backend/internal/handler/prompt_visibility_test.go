package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
)

func TestPromptHandlers_SeparatePrivateDraftsFromSharedLibrary(t *testing.T) {
	db := setupHandlerTestDB(t)
	defer db.Close()
	suffix := time.Now().UnixNano()
	userRepo := repository.NewUserRepository(db)
	owner := &model.User{Username: fmt.Sprintf("prompt_owner_%d", suffix), PasswordHash: "x", Role: "user", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`)}
	admin := &model.User{Username: fmt.Sprintf("prompt_admin_%d", suffix), PasswordHash: "x", Role: "admin", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`)}
	for _, user := range []*model.User{owner, admin} {
		if err := userRepo.Create(user); err != nil {
			t.Fatalf("create user: %v", err)
		}
	}
	promptRepo := repository.NewPromptRepository(db)
	private := &model.Prompt{UserID: owner.ID, Title: "Private", Content: "private content", GroupName: "Personal", Tags: []string{}, IsPublic: false}
	shared := &model.Prompt{UserID: admin.ID, Title: "Shared", Content: "shared content", GroupName: "默认分组", Tags: []string{}, IsPublic: true}
	for _, prompt := range []*model.Prompt{private, shared} {
		if err := promptRepo.Create(prompt); err != nil {
			t.Fatalf("create prompt: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM prompts WHERE id IN ($1, $2)", private.ID, shared.ID)
		_, _ = db.Exec("DELETE FROM users WHERE id IN ($1, $2)", owner.ID, admin.ID)
	})

	router := gin.New()
	router.Use(func(c *gin.Context) {
		if c.GetHeader("X-Test-Role") == "admin" {
			c.Set("user_id", admin.ID)
		} else {
			c.Set("user_id", owner.ID)
		}
		c.Next()
	})
	router.POST("/prompts", CreatePromptHandler(promptRepo))
	router.GET("/prompts/public", ListPublicPromptsHandler(promptRepo))
	router.GET("/prompts/:id", GetPromptHandler(promptRepo))
	router.PATCH("/prompts/:id", UpdatePromptHandler(promptRepo))
	router.DELETE("/prompts/:id", DeletePromptHandler(promptRepo))
	router.GET("/admin/prompts", ListSharedPromptsHandler(promptRepo))
	router.PATCH("/admin/prompts/:id", UpdateSharedPromptHandler(promptRepo))
	router.DELETE("/admin/prompts/:id", DeleteSharedPromptHandler(promptRepo))

	createResponse := doPromptRequest(router, http.MethodPost, "/prompts", owner.ID, "user", map[string]any{
		"title": "Attempted share", "content": "must remain private", "group_name": "Personal", "tags": []string{}, "is_public": true,
	})
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create prompt status = %d: %s", createResponse.Code, createResponse.Body.String())
	}
	var created model.Prompt
	if err := json.Unmarshal(createResponse.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created prompt: %v", err)
	}
	if created.IsPublic {
		t.Fatal("ordinary user created a shared prompt")
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM prompts WHERE id = $1", created.ID) })

	listResponse := doPromptRequest(router, http.MethodGet, "/admin/prompts", admin.ID, "admin", nil)
	if listResponse.Code != http.StatusOK || bytes.Contains(listResponse.Body.Bytes(), []byte("private content")) {
		t.Fatalf("shared list leaked private prompt: status=%d body=%s", listResponse.Code, listResponse.Body.String())
	}

	publicResponse := doPromptRequest(router, http.MethodGet, "/prompts/public", owner.ID, "user", nil)
	if publicResponse.Code != http.StatusOK {
		t.Fatalf("public prompt list status=%d body=%s", publicResponse.Code, publicResponse.Body.String())
	}
	if bytes.Contains(publicResponse.Body.Bytes(), []byte(`"user_id"`)) {
		t.Fatalf("public prompt list leaked owner id: %s", publicResponse.Body.String())
	}
	if !bytes.Contains(publicResponse.Body.Bytes(), []byte("shared content")) || bytes.Contains(publicResponse.Body.Bytes(), []byte("private content")) {
		t.Fatalf("public prompt list returned incorrect visibility: %s", publicResponse.Body.String())
	}

	publicDetail := doPromptRequest(router, http.MethodGet, fmt.Sprintf("/prompts/%d", shared.ID), owner.ID, "user", nil)
	if publicDetail.Code != http.StatusOK {
		t.Fatalf("public prompt detail status=%d body=%s", publicDetail.Code, publicDetail.Body.String())
	}
	if bytes.Contains(publicDetail.Body.Bytes(), []byte(`"user_id"`)) {
		t.Fatalf("public prompt detail leaked owner id: %s", publicDetail.Body.String())
	}

	for _, request := range []struct {
		method string
		path   string
		body   map[string]any
	}{
		{http.MethodPatch, fmt.Sprintf("/admin/prompts/%d", private.ID), map[string]any{"title": "mutated"}},
		{http.MethodDelete, fmt.Sprintf("/admin/prompts/%d", private.ID), nil},
		{http.MethodPatch, fmt.Sprintf("/prompts/%d", shared.ID), map[string]any{"title": "mutated"}},
		{http.MethodDelete, fmt.Sprintf("/prompts/%d", shared.ID), nil},
	} {
		response := doPromptRequest(router, request.method, request.path, owner.ID, "user", request.body)
		if response.Code != http.StatusNotFound && response.Code != http.StatusForbidden && response.Code != http.StatusBadRequest {
			t.Fatalf("%s %s status=%d body=%s", request.method, request.path, response.Code, response.Body.String())
		}
	}
}

func doPromptRequest(router *gin.Engine, method, path string, userID int64, role string, body map[string]any) *httptest.ResponseRecorder {
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Test-Role", role)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}
