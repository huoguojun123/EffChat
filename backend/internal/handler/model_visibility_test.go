package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/effchat/internal/model"
	"github.com/huoguojun123/effchat/internal/repository"
	"github.com/huoguojun123/effchat/internal/service"
)

func TestGetModelHandler_HidesRestrictedModelsFromRegularUsers(t *testing.T) {
	db := setupHandlerTestDB(t)
	defer db.Close()

	suffix := time.Now().UnixNano()
	userRepo := repository.NewUserRepository(db)
	regularUser := &model.User{Username: fmt.Sprintf("model_visibility_user_%d", suffix), PasswordHash: "x", Role: "user", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`)}
	adminUser := &model.User{Username: fmt.Sprintf("model_visibility_admin_%d", suffix), PasswordHash: "x", Role: "admin", IsActive: true, Permissions: []byte(`{}`), Preferences: []byte(`{}`)}
	for _, user := range []*model.User{regularUser, adminUser} {
		if err := userRepo.Create(user); err != nil {
			t.Fatalf("create user: %v", err)
		}
	}

	modelID := fmt.Sprintf("model_visibility_restricted_%d", suffix)
	modelRepo := repository.NewModelRepository(db)
	if err := modelRepo.Upsert(&model.Model{ID: modelID, DisplayName: "Restricted", Provider: "test", ContextWindow: 1024, MaxOutput: 256, Enabled: true, MinGroupLevel: 10, ThinkingFormat: "auto"}); err != nil {
		t.Fatalf("save model: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM models WHERE id = $1", modelID)
		_, _ = db.Exec("DELETE FROM users WHERE id IN ($1, $2)", regularUser.ID, adminUser.ID)
	})

	r := gin.New()
	r.Use(func(c *gin.Context) {
		if c.GetHeader("X-Test-Role") == "admin" {
			c.Set("user_id", adminUser.ID)
			c.Set("role", "admin")
		} else {
			c.Set("user_id", regularUser.ID)
			c.Set("role", "user")
		}
		c.Next()
	})
	r.GET("/models/*id", GetModelHandler(service.NewModelService(modelRepo), userRepo))

	request := httptest.NewRequest(http.MethodGet, "/models/"+modelID, nil)
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("regular user status = %d, want 404: %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/models/"+modelID, nil)
	request.Header.Set("X-Test-Role", "admin")
	response = httptest.NewRecorder()
	r.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("admin status = %d, want 200: %s", response.Code, response.Body.String())
	}
}
