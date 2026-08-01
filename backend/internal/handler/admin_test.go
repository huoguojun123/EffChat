package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/middleware"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
)

type adminTestEnv struct {
	router           *gin.Engine
	userAdminService *service.UserAdminService
	runHub           *service.RunHub
	adminToken       string
	adminID          int64
	userToken        string
	userID           int64
}

func setupAdminTestEnv(t *testing.T) *adminTestEnv {
	t.Helper()
	db := setupHandlerTestDB(t)

	userRepo := repository.NewUserRepository(db)
	authService := service.NewAuthService(userRepo, "test-admin-secret")
	userAdminService := service.NewUserAdminService(userRepo)
	runHub := service.NewRunHub(time.Minute, 1<<20)
	userAdminService.SetRunHub(runHub)

	r := gin.New()
	r.POST("/api/v1/auth/register", RegisterHandler(authService))

	auth := r.Group("/api/v1")
	auth.Use(middleware.AuthMiddleware(authService))
	{
		admin := auth.Group("/admin")
		admin.Use(middleware.AdminMiddleware())
		{
			admin.GET("/users", ListUsersHandler(userAdminService))
			admin.PATCH("/users/:id", UpdateUserHandler(userAdminService))
			admin.PUT("/users/:id/password", ResetUserPasswordHandler(userAdminService))
		}
	}

	// First user = admin (per project design)
	adminUsername := fmt.Sprintf("admin_%d", time.Now().UnixNano())
	adminResp := registerUser(t, r, adminUsername, "adminpass123")
	var adminUserID int64
	if adminResp.User != nil {
		adminUserID = adminResp.User.ID
	} else {
		if err := db.QueryRow("SELECT id FROM users WHERE username = $1", adminUsername).Scan(&adminUserID); err != nil {
			t.Fatalf("lookup admin user failed: %v", err)
		}
	}
	if _, err := db.Exec("UPDATE users SET role = 'admin', is_active = true WHERE id = $1", adminUserID); err != nil {
		t.Fatalf("promote admin user failed: %v", err)
	}
	// Re-generate token with admin role
	adminResp = loginUser(t, r, adminUsername, "adminpass123", authService)

	// Second user = regular user
	userUsername := fmt.Sprintf("user_%d", time.Now().UnixNano())
	userResp := registerUser(t, r, userUsername, "userpass123")
	if userResp.User == nil {
		var pendingUserID int64
		if err := db.QueryRow("SELECT id FROM users WHERE username = $1", userUsername).Scan(&pendingUserID); err != nil {
			t.Fatalf("lookup pending user failed: %v", err)
		}
		if _, err := db.Exec("UPDATE users SET is_active = true WHERE id = $1", pendingUserID); err != nil {
			t.Fatalf("activate pending user failed: %v", err)
		}
		userResp = loginUser(t, r, userUsername, "userpass123", authService)
	}

	t.Cleanup(func() {
		db.Exec("DELETE FROM users WHERE id IN ($1, $2)", adminResp.User.ID, userResp.User.ID)
		db.Close()
	})

	return &adminTestEnv{
		router:           r,
		userAdminService: userAdminService,
		runHub:           runHub,
		adminToken:       adminResp.Token,
		adminID:          adminResp.User.ID,
		userToken:        userResp.Token,
		userID:           userResp.User.ID,
	}
}

func registerUser(t *testing.T, r *gin.Engine, username, password string) *service.AuthResponse {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("register %s failed: %d %s", username, w.Code, w.Body.String())
	}
	var registerResp struct {
		Approved bool        `json:"approved"`
		Token    string      `json:"token"`
		User     *model.User `json:"user"`
	}
	json.Unmarshal(w.Body.Bytes(), &registerResp)
	if registerResp.Approved && registerResp.User != nil && registerResp.Token != "" {
		return &service.AuthResponse{Token: registerResp.Token, User: registerResp.User}
	}
	return &service.AuthResponse{}
}

func loginUser(t *testing.T, r *gin.Engine, username, password string, authSvc *service.AuthService) *service.AuthResponse {
	t.Helper()
	resp, err := authSvc.Login(&service.LoginRequest{Username: username, Password: password})
	if err != nil {
		t.Fatalf("login %s failed: %v", username, err)
	}
	return resp
}

func (e *adminTestEnv) doAdmin(method, path string, body interface{}) *httptest.ResponseRecorder {
	return e.doWithToken(method, path, body, e.adminToken)
}

func (e *adminTestEnv) doUser(method, path string, body interface{}) *httptest.ResponseRecorder {
	return e.doWithToken(method, path, body, e.userToken)
}

func (e *adminTestEnv) doWithToken(method, path string, body interface{}, token string) *httptest.ResponseRecorder {
	var bodyReader *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		bodyReader = bytes.NewReader(b)
	} else {
		bodyReader = bytes.NewReader(nil)
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, bodyReader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	e.router.ServeHTTP(w, req)
	return w
}

func TestAdminListUsers(t *testing.T) {
	env := setupAdminTestEnv(t)

	w := env.doAdmin(http.MethodGet, "/api/v1/admin/users", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list users: want 200, got %d body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Users []*service.UserResponse `json:"users"`
		Total int                     `json:"total"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Total < 2 {
		t.Errorf("want total >= 2 (admin + user), got %d", resp.Total)
	}

	// Verify admin user is in list
	found := false
	for _, u := range resp.Users {
		if u.ID == env.adminID {
			if u.Role != "admin" {
				t.Errorf("first user should be admin, got role=%s", u.Role)
			}
			found = true
		}
	}
	if !found {
		t.Error("admin user not found in list")
	}
}

func TestAdminListUsers_Forbidden(t *testing.T) {
	env := setupAdminTestEnv(t)

	// Regular user cannot access admin routes
	w := env.doUser(http.MethodGet, "/api/v1/admin/users", nil)
	if w.Code != http.StatusForbidden {
		t.Errorf("non-admin access: want 403, got %d", w.Code)
	}
}

func TestAdminUpdateUser_DisableUser(t *testing.T) {
	env := setupAdminTestEnv(t)
	run, err := env.runHub.Start(1, env.userID, 0, "disable-user", service.RunKindChat)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	canceled := make(chan struct{}, 1)
	env.runHub.Bind(run.RunID, func() { canceled <- struct{}{} })

	// Admin disables the regular user
	w := env.doAdmin(http.MethodPatch, fmt.Sprintf("/api/v1/admin/users/%d", env.userID), map[string]interface{}{
		"is_active": false,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("disable user: want 200, got %d body=%s", w.Code, w.Body.String())
	}

	var resp service.UserResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.IsActive {
		t.Error("user should be inactive after disable")
	}
	select {
	case <-canceled:
	default:
		t.Fatal("disabled user's active run was not canceled")
	}
	users, total, err := env.userAdminService.List(100, 0)
	if err != nil || total < 2 {
		t.Fatalf("list users after suspension: total=%d err=%v", total, err)
	}
	for _, user := range users {
		if user.ID == env.userID {
			if user.IsActive {
				t.Fatal("suspended user was not retained as inactive")
			}
			return
		}
	}
	t.Fatal("suspended user disappeared from the administrator list")
}

func TestAdminUserDeletionRouteIsNotRegistered(t *testing.T) {
	env := setupAdminTestEnv(t)
	w := env.doAdmin(http.MethodDelete, fmt.Sprintf("/api/v1/admin/users/%d", env.userID), nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("deleted user route: want 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestAdminResetPasswordCancelsActiveRuns(t *testing.T) {
	env := setupAdminTestEnv(t)
	run, err := env.runHub.Start(1, env.userID, 0, "reset-password", service.RunKindChat)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	canceled := make(chan struct{}, 1)
	env.runHub.Bind(run.RunID, func() { canceled <- struct{}{} })

	w := env.doAdmin(http.MethodPut, fmt.Sprintf("/api/v1/admin/users/%d/password", env.userID), map[string]string{
		"password": "resetpass123",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("reset password: want 200, got %d body=%s", w.Code, w.Body.String())
	}
	select {
	case <-canceled:
	default:
		t.Fatal("reset user's active run was not canceled")
	}
}

func TestAdminUpdateUser_PromoteToAdmin(t *testing.T) {
	env := setupAdminTestEnv(t)

	w := env.doAdmin(http.MethodPatch, fmt.Sprintf("/api/v1/admin/users/%d", env.userID), map[string]interface{}{
		"role": "admin",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("promote user: want 200, got %d body=%s", w.Code, w.Body.String())
	}

	var resp service.UserResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Role != "admin" {
		t.Errorf("want role=admin, got %s", resp.Role)
	}
}

func TestAdminUpdateUser_InvalidRole(t *testing.T) {
	env := setupAdminTestEnv(t)

	w := env.doAdmin(http.MethodPatch, fmt.Sprintf("/api/v1/admin/users/%d", env.userID), map[string]interface{}{
		"role": "superuser",
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid role: want 400, got %d", w.Code)
	}
}

func TestUserAdminService_PreventsRemovingLastActiveAdmin(t *testing.T) {
	env := setupAdminTestEnv(t)
	role := "user"
	_, err := env.userAdminService.Update(env.adminID, &service.UpdateUserRequest{Role: &role})
	if !errors.Is(err, repository.ErrLastActiveAdmin) {
		t.Fatalf("demote last active admin: got %v, want ErrLastActiveAdmin", err)
	}
	admin, total, err := env.userAdminService.List(100, 0)
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if total < 2 {
		t.Fatalf("user list total = %d, want at least 2", total)
	}
	for _, user := range admin {
		if user.ID == env.adminID {
			if user.Role != "admin" || !user.IsActive {
				t.Fatalf("last admin was modified: %+v", user)
			}
			return
		}
	}
	t.Fatal("last admin was not retained")
}

func TestUserAdminService_ConcurrentAdminChangesKeepOneActiveAdmin(t *testing.T) {
	env := setupAdminTestEnv(t)
	adminRole := "admin"
	if _, err := env.userAdminService.Update(env.userID, &service.UpdateUserRequest{Role: &adminRole}); err != nil {
		t.Fatalf("promote second admin: %v", err)
	}

	inactive := false
	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, userID := range []int64{env.adminID, env.userID} {
		go func(id int64) {
			<-start
			_, err := env.userAdminService.Update(id, &service.UpdateUserRequest{IsActive: &inactive})
			errs <- err
		}(userID)
	}
	close(start)
	first, second := <-errs, <-errs
	var succeeded, blocked int
	for _, err := range []error{first, second} {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, repository.ErrLastActiveAdmin):
			blocked++
		default:
			t.Fatalf("unexpected concurrent update error: %v", err)
		}
	}
	if succeeded != 1 || blocked != 1 {
		t.Fatalf("concurrent updates: succeeded=%d blocked=%d, want 1/1", succeeded, blocked)
	}

	users, _, err := env.userAdminService.List(100, 0)
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	activeAdmins := 0
	for _, user := range users {
		if user.Role == "admin" && user.IsActive {
			activeAdmins++
		}
	}
	if activeAdmins != 1 {
		t.Fatalf("active admins = %d, want 1", activeAdmins)
	}
}

func TestAdminUpdateUser_NotFound(t *testing.T) {
	env := setupAdminTestEnv(t)

	w := env.doAdmin(http.MethodPatch, "/api/v1/admin/users/999999999", map[string]interface{}{
		"is_active": false,
	})
	if w.Code != http.StatusNotFound {
		t.Errorf("nonexistent user: want 404, got %d", w.Code)
	}
}

func TestAdminListUsers_Pagination(t *testing.T) {
	env := setupAdminTestEnv(t)

	w := env.doAdmin(http.MethodGet, "/api/v1/admin/users?limit=1&offset=0", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d", w.Code)
	}
	var resp struct {
		Users []*model.User `json:"users"`
		Total int           `json:"total"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Users) != 1 {
		t.Errorf("limit=1: want 1 user returned, got %d", len(resp.Users))
	}
	if resp.Total < 2 {
		t.Errorf("total should reflect all users (>= 2), got %d", resp.Total)
	}
}
