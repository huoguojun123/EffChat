package handler

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/agent"
	"github.com/huoguojun123/EffChat/internal/extractor"
	"github.com/huoguojun123/EffChat/internal/filepolicy"
	"github.com/huoguojun123/EffChat/internal/middleware"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
	"github.com/huoguojun123/EffChat/internal/usage"
	"github.com/huoguojun123/EffChat/pkg/config"
	"github.com/huoguojun123/EffChat/pkg/logger"
)

const diagnosticRetention = 90 * 24 * time.Hour

type diagnosticRetentionStore interface {
	DeleteOlderThan(context.Context, time.Time) error
}

func cleanupDiagnostics(stores []diagnosticRetentionStore, now time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cutoff := now.Add(-diagnosticRetention)
	for _, store := range stores {
		if err := store.DeleteOlderThan(ctx, cutoff); err != nil {
			logger.Error("diagnostic retention cleanup failed: %v", err)
		}
	}
}

func startDiagnosticRetention(stores ...diagnosticRetentionStore) func() {
	cleanup := func() { cleanupDiagnostics(stores, time.Now()) }
	cleanup()
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				cleanup()
			}
		}
	}()
	return func() { close(stop) }
}

func RegisterRoutes(r *gin.Engine, db *sql.DB, cfg *config.Config) (*service.RunHub, func()) {
	// 初始化 Repository
	userRepo := repository.NewUserRepository(db)
	sessionRepo := repository.NewSessionRepository(db)
	messageRepo := repository.NewMessageRepository(db)
	conversationSearchRepo := repository.NewConversationSearchRepository(db)
	answerAttemptRepo := repository.NewAnswerAttemptRepository(db)
	modelRepo := repository.NewModelRepository(db)
	configRepo := repository.NewConfigRepository(db)
	promptRepo := repository.NewPromptRepository(db)
	fileRepo := repository.NewFileRepository(db)
	fontRepo := repository.NewFontRepository(db)
	userGroupRepo := repository.NewUserGroupRepository(db)
	memoryRepo := repository.NewSessionMemoryRepository(db)
	taskRunRepo := repository.NewModelTaskRunRepository(db)
	skillRepo := repository.NewSkillRepository(db)
	sessionFolderRepo := repository.NewSessionFolderRepository(db)
	promptGroupRepo := repository.NewPromptGroupRepository(db)
	channelRepo := repository.NewChannelRepository(db)
	quotaRepo := repository.NewQuotaRepository(db)
	toolConfigRepo := repository.NewToolConfigRepository(db)
	governanceRepo := repository.NewGovernanceRepository(db)
	usageRepo := usage.NewRepository(db)

	// 从数据库加载模型能力表作为运行时唯一事实来源。
	if models, err := modelRepo.List(false); err != nil {
		modelbank.LoadModels(nil)
		logger.Info("加载 models 表失败，运行时模型表置空: %v", err)
	} else {
		modelbank.LoadModels(models)
		logger.Info("已从数据库加载 %d 个模型", len(models))
	}

	// 初始化 Service
	authService := service.NewAuthService(userRepo, cfg.JWT.Secret)
	avatarHandler := NewAvatarHandler(authService, filepolicy.AvatarRoot)
	authRateLimiter := NewAuthRateLimiter(cfg.Security.AuthRateLimit)
	sessionService := service.NewSessionService(sessionRepo, messageRepo, configRepo, sessionFolderRepo)
	messageService := service.NewMessageService(messageRepo, sessionRepo, fileRepo, answerAttemptRepo)
	sessionFolderService := service.NewSessionFolderService(sessionFolderRepo)
	runHub := service.NewRunHub(10*time.Minute, 5*1024*1024)
	runHub.SetStore(quotaRepo)
	authService.SetRunHub(runHub)
	sessionService.SetRunHub(runHub)
	channelService := service.NewChannelService(channelRepo)
	sessionService.SetRuntimeModelDependencies(modelRepo, channelService, userRepo)
	modelService := service.NewModelService(modelRepo, channelService)
	userAdminService := service.NewUserAdminService(userRepo)
	userAdminService.SetRunHub(runHub)
	userGroupService := service.NewUserGroupService(userGroupRepo)
	skillService := service.NewSkillService(skillRepo, userRepo, sessionRepo)
	usageService := usage.NewService(usageRepo)
	stopDiagnosticRetention := startDiagnosticRetention(usageRepo, taskRunRepo)
	quotaService := service.NewQuotaService(quotaRepo)
	toolConfigService := service.NewToolConfigService(toolConfigRepo)
	toolConfigService.SetGovernanceRepository(governanceRepo)

	titleService := service.NewTitleService(sessionRepo, messageRepo, configRepo, channelService, usageService)
	titleService.SetTaskRunRepository(taskRunRepo)

	// 初始化 Eino Agent
	einoAgent := agent.NewEinoAgent(channelService, toolConfigService, cfg.AI.CompressionMaxTokens, configRepo, memoryRepo, taskRunRepo, fileRepo, usageService, quotaService)

	var extractorClient *extractor.SidecarClient
	if cfg.Extractor.Enabled && cfg.Extractor.URL != "" {
		extractorClient = extractor.NewSidecarClient(cfg.Extractor.URL, cfg.Extractor.Timeout)
		logger.Info("Python extractor enabled: url=%s timeout=%s", cfg.Extractor.URL, cfg.Extractor.Timeout)
	} else {
		logger.Info("Python extractor disabled")
	}
	ocrRecoveryRunner := NewOCRRecoveryRunner(fileRepo, channelService, extractorClient, quotaService, configRepo)
	ocrRecoveryRunner.Start()

	// API v1
	v1 := r.Group("/api/v1")
	{
		// 公开路由（无需认证）
		auth := v1.Group("/auth")
		{
			auth.POST("/register", RegisterHandler(authService, authRateLimiter))
			auth.POST("/login", LoginHandler(authService, authRateLimiter))
		}

		// 公开系统信息（首屏标题、标签页标题用，无需认证）
		v1.GET("/system/info", SystemInfoHandler(configRepo, fontRepo))
		v1.GET("/fonts/:id/file", DownloadFontFileHandler(fontRepo))
		v1.GET("/avatars/:filename", avatarHandler.Serve)

		// 需要认证的路由
		authenticated := v1.Group("")
		authenticated.Use(middleware.AuthMiddleware(authService))
		{
			authenticated.GET("/search/conversations", SearchConversationsHandler(conversationSearchRepo))
			// 会话路由
			sessions := authenticated.Group("/sessions")
			{
				sessions.GET("/readiness", SessionCreateReadinessHandler(sessionService))
				sessions.POST("", CreateSessionHandler(sessionService))
				sessions.GET("", ListSessionsHandler(sessionService))
				sessions.GET("/:id", GetSessionHandler(sessionService))
				sessions.PATCH("/:id", UpdateSessionHandler(sessionService))
				sessions.DELETE("/:id", DeleteSessionHandler(sessionService))
				sessions.GET("/:id/export.md", ExportSessionMarkdownHandler(messageService))
				sessions.GET("/:id/memory", GetSessionMemoryHandler(sessionService, memoryRepo, taskRunRepo, configRepo))
				sessions.PUT("/:id/memory", SaveSessionMemoryHandler(sessionService, memoryRepo, taskRunRepo, configRepo))
				sessions.POST("/:id/memory/compact", MemoryMaintenanceStreamHandler(sessionService, authService, messageRepo, memoryRepo, einoAgent, runHub, quotaService, cfg.Run.HeartbeatInterval, cfg.Run.FirstOutputTimeout, service.RunOperationMemoryCompact))
				sessions.POST("/:id/memory/retry", MemoryMaintenanceStreamHandler(sessionService, authService, messageRepo, memoryRepo, einoAgent, runHub, quotaService, cfg.Run.HeartbeatInterval, cfg.Run.FirstOutputTimeout, service.RunOperationMemoryRetry))
				sessions.POST("/:id/memory/changes/:change_id/undo", UndoSessionMemoryChangeHandler(sessionService, memoryRepo, taskRunRepo, configRepo))
				// 消息路由
				sessions.PUT("/:id/skills", UpdateSessionSkillsHandler(skillService))
				sessions.POST("/:id/messages/preflight", MessagePreflightHandler(messageService, sessionService, authService, skillService, einoAgent, titleService, taskRunRepo))
				sessions.POST("/:id/messages/stream", SendMessageStreamHandler(messageService, sessionService, authService, skillService, einoAgent, titleService, runHub, quotaService, taskRunRepo, cfg.Run.HeartbeatInterval, cfg.Run.FirstOutputTimeout))
				sessions.POST("/:id/messages/:message_id/retry", RetryMessageStreamHandler(messageService, sessionService, authService, skillService, einoAgent, titleService, runHub, quotaService, taskRunRepo, cfg.Run.HeartbeatInterval, cfg.Run.FirstOutputTimeout))
				sessions.POST("/:id/messages/:message_id/edit-retry", EditRetryMessageStreamHandler(messageService, sessionService, authService, skillService, einoAgent, titleService, runHub, quotaService, taskRunRepo, cfg.Run.HeartbeatInterval, cfg.Run.FirstOutputTimeout))
				sessions.POST("/:id/answer-attempts/:attempt_id/select", SelectAnswerAttemptHandler(messageService, sessionService, authService, einoAgent))
				sessions.POST("/:id/compact", CompactSessionHandler(messageService, sessionService, authService, skillService, einoAgent, titleService, runHub, quotaService, taskRunRepo, cfg.Run.HeartbeatInterval, cfg.Run.FirstOutputTimeout))
				sessions.POST("/:id/compact/undo", UndoCompactionHandler(messageService))
				sessions.GET("/:id/messages", ListMessagesHandler(messageService))
				sessions.GET("/:id/turns", ListConversationTurnsHandler(messageService))
				sessions.GET("/:id/message-window", ListMessageWindowHandler(messageService))
				sessions.GET("/:id/runs/active", ActiveRunHandler(runHub, sessionService))
				sessions.GET("/:id/runs/:run_id", RunStatusHandler(quotaService, sessionService))
				sessions.GET("/:id/runs/:run_id/resume", ResumeRunHandler(runHub, sessionService, cfg.Run.HeartbeatInterval))
				sessions.DELETE("/:id/runs/:run_id", CancelRunHandler(runHub, sessionService))
			}

			sessionFolders := authenticated.Group("/session-folders")
			{
				sessionFolders.GET("", ListSessionFoldersHandler(sessionFolderService))
				sessionFolders.POST("", CreateSessionFolderHandler(sessionFolderService))
				sessionFolders.PATCH("/:id", UpdateSessionFolderHandler(sessionFolderService))
				sessionFolders.DELETE("/:id", DeleteSessionFolderHandler(sessionFolderService))
			}

			// 模型列表（登录用户只读，前端模型选择器用）
			authenticated.GET("/models", ListModelsHandler(modelService, userRepo))
			authenticated.GET("/models/*id", GetModelHandler(modelService, userRepo))
			authenticated.GET("/skills", ListSkillsHandler(skillService))
			authenticated.GET("/skills/:id/files", ListSkillFilesHandler(skillService))
			authenticated.GET("/skills/:id/files/content", ReadSkillFileHandler(skillService))

			// 用户自身操作
			authenticated.GET("/users/me", GetMeHandler(authService))
			authenticated.PATCH("/users/me", UpdateMeHandler(authService))
			authenticated.PUT("/users/me/password", ChangePasswordHandler(authService))
			authenticated.POST("/users/me/avatar", avatarHandler.Upload)
			authenticated.DELETE("/users/me/avatar", avatarHandler.Delete)

			// 提示词路由
			prompts := authenticated.Group("/prompts")
			{
				prompts.POST("", CreatePromptHandler(promptRepo))
				prompts.GET("", ListPromptsHandler(promptRepo))
				prompts.GET("/public", ListPublicPromptsHandler(promptRepo))
				prompts.GET("/:id", GetPromptHandler(promptRepo))
				prompts.PATCH("/:id", UpdatePromptHandler(promptRepo))
				prompts.DELETE("/:id", DeletePromptHandler(promptRepo))
			}

			promptGroups := authenticated.Group("/prompt-groups")
			{
				promptGroups.GET("", ListPromptGroupsHandler(promptGroupRepo))
				promptGroups.POST("", CreatePromptGroupHandler(promptGroupRepo))
				promptGroups.PATCH("/:id", UpdatePromptGroupHandler(promptGroupRepo))
				promptGroups.DELETE("/:id", DeletePromptGroupHandler(promptGroupRepo))
			}

			// 文件路由
			files := authenticated.Group("/files")
			{
				files.POST("", UploadFileHandler(fileRepo, configRepo, WithUploadSessionRepo(sessionRepo), WithUploadExtractorClient(extractorClient), WithUploadChannelService(channelService), WithUploadQuotaService(quotaService), WithUploadOCRRecoveryRunner(ocrRecoveryRunner)))
				files.GET("", ListFilesHandler(fileRepo))
				files.GET("/upload-limits", UploadLimitsHandler(configRepo))
				files.POST("/:id/ocr-refresh", RefreshOCRFileHandler(fileRepo, channelService, extractorClient))
				files.POST("/:id/ocr-retry", RetryOCRFileHandler(fileRepo, configRepo, channelService, extractorClient, ocrRecoveryRunner))
				files.GET("/:id/preview", PreviewFileHandler(fileRepo, channelService, extractorClient))
				files.GET("/:id", DownloadFileHandler(fileRepo))
				files.DELETE("/:id", DeleteFileHandler(fileRepo))
			}

			// 管理员路由（需 admin 角色）
			admin := authenticated.Group("/admin")
			admin.Use(middleware.AdminMiddleware())
			{
				admin.GET("/models/available", ListAvailableModelsHandler(modelService, channelService))
				admin.GET("/models/catalog", ListModelsDevCatalogHandler(modelService))
				admin.GET("/models/catalog/*id", GetModelsDevCatalogModelHandler())
				admin.POST("/models/test", TestModelHandler(einoAgent))
				admin.POST("/models", CreateModelHandler(modelService))
				admin.PATCH("/models/*id", UpdateModelHandler(modelService))
				admin.DELETE("/models/*id", DeleteModelHandler(modelService))
				admin.GET("/usage", AdminUsageHandler(usageService))
				admin.GET("/system/status", AdminSystemStatusHandler(db, extractorClient, time.Now()))

				admin.GET("/channels", ListAIChannelsHandler(channelService))
				admin.POST("/channels", SaveAIChannelHandler(channelService))
				admin.DELETE("/channels/:key", DeleteAIChannelHandler(channelService))
				admin.GET("/external-services", ListExternalServicesHandler(channelService))
				admin.POST("/external-services", SaveExternalServiceHandler(channelService))
				admin.PUT("/external-services/order", ReorderExternalServicesHandler(channelService))
				admin.POST("/external-services/test", TestExternalServiceHandler(channelService))
				admin.DELETE("/external-services/:key", DeleteExternalServiceHandler(channelService))
				admin.GET("/tools", ListToolConfigsHandler(toolConfigService))
				admin.POST("/tools", SaveToolConfigHandler(toolConfigService))
				admin.GET("/tools/:key/history", ListToolConfigHistoryHandler(toolConfigService))
				admin.POST("/tools/events/:id/rollback", RollbackToolConfigHandler(toolConfigService))
				admin.POST("/files/cleanup-orphans", CleanupOrphanFilesHandler(fileRepo))

				admin.GET("/users", ListUsersHandler(userAdminService))
				admin.POST("/users", CreateUserHandler(userAdminService))
				admin.PATCH("/users/:id", UpdateUserHandler(userAdminService, authRateLimiter))
				admin.PUT("/users/:id/skills", UpdateUserSkillsHandler(skillService))
				admin.PUT("/users/:id/password", ResetUserPasswordHandler(userAdminService))
				admin.PUT("/users/:id/group", SetUserGroupHandler(userAdminService))

				admin.GET("/groups", ListUserGroupsHandler(userGroupService))
				admin.POST("/groups", CreateUserGroupHandler(userGroupService))
				admin.PATCH("/groups/:id", UpdateUserGroupHandler(userGroupService))
				admin.DELETE("/groups/:id", DeleteUserGroupHandler(userGroupService))

				admin.GET("/config", ListConfigHandler(configRepo))
				admin.PATCH("/config", UpdateConfigBatchHandler(configRepo, modelService))
				admin.PATCH("/config/:key", UpdateConfigHandler(configRepo, modelService))

				admin.GET("/fonts", ListAdminFontsHandler(fontRepo))
				admin.POST("/fonts", UploadFontHandler(fontRepo))
				admin.PUT("/fonts/selected", SelectFontHandler(fontRepo))
				admin.PATCH("/fonts/:id", UpdateFontHandler(fontRepo))
				admin.DELETE("/fonts/:id", DeleteFontHandler(fontRepo))

				admin.GET("/prompts", ListSharedPromptsHandler(promptRepo))
				admin.POST("/prompts", CreateSharedPromptHandler(promptRepo))
				admin.PATCH("/prompts/:id", UpdateSharedPromptHandler(promptRepo))
				admin.DELETE("/prompts/:id", DeleteSharedPromptHandler(promptRepo))

				admin.GET("/skills", ListAdminSkillsHandler(skillService))
				admin.POST("/skills", CreateSkillHandler(skillService))
				admin.PATCH("/skills/:id", UpdateSkillHandler(skillService))
				admin.DELETE("/skills/:id", DeleteSkillHandler(skillService))
				admin.GET("/skills/:id/files", ListAdminSkillFilesHandler(skillService))
				admin.GET("/skills/:id/files/content", ReadAdminSkillFileHandler(skillService))
				admin.GET("/skills/:id/import-records", ListSkillImportRecordsHandler(skillService))
				admin.POST("/skills/:id/update/git/preview", PreviewSkillGitUpdateHandler(skillService))
				admin.POST("/skills/:id/update/git", ApplySkillGitUpdateHandler(skillService))
				admin.POST("/skills/:id/update/zip/preview", PreviewSkillZipUpdateHandler(skillService))
				admin.POST("/skills/:id/update/zip", ApplySkillZipUpdateHandler(skillService))
				admin.POST("/skills/import/git/preview", PreviewSkillsFromGitHandler(skillService))
				admin.POST("/skills/import/git", ImportSkillsFromGitHandler(skillService))
				admin.POST("/skills/import/zip/preview", PreviewSkillsFromZipHandler(skillService))
				admin.POST("/skills/import/zip", ImportSkillsFromZipHandler(skillService))
			}
		}
	}
	var stopOnce sync.Once
	return runHub, func() {
		stopOnce.Do(func() {
			stopDiagnosticRetention()
			drainCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var drains sync.WaitGroup
			drain := func(name string, fn func(context.Context) bool) {
				drains.Add(1)
				go func() {
					defer drains.Done()
					if !fn(drainCtx) {
						logger.Error("%s drain timed out", name)
					}
				}()
			}
			drain("OCR recovery", ocrRecoveryRunner.Drain)
			drain("memory maintenance", einoAgent.DrainMemoryTasks)
			drain("title generation", titleService.DrainBackgroundTasks)
			drain("usage", usageService.Drain)
			drains.Wait()
		})
	}
}

// RegisterHandler 用户注册
func RegisterHandler(authService *service.AuthService, limiters ...*AuthRateLimiter) gin.HandlerFunc {
	limiter := firstAuthRateLimiter(limiters)
	return func(c *gin.Context) {
		var req service.RegisterRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}
		if retryAfter, ok := limiter.Allow(requestClientIP(c), req.Username); !ok {
			writeAuthRateLimitError(c, retryAfter)
			return
		}

		resp, err := authService.Register(&req)
		if err != nil {
			limiter.RecordFailure(requestClientIP(c), req.Username)
			writeAuthError(c, "register", err)
			return
		}
		limiter.Reset(requestClientIP(c), req.Username)

		c.JSON(http.StatusCreated, resp)
	}
}

// LoginHandler 用户登录
func LoginHandler(authService *service.AuthService, limiters ...*AuthRateLimiter) gin.HandlerFunc {
	limiter := firstAuthRateLimiter(limiters)
	return func(c *gin.Context) {
		var req service.LoginRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}
		if retryAfter, ok := limiter.Allow(requestClientIP(c), req.Username); !ok {
			writeAuthRateLimitError(c, retryAfter)
			return
		}

		resp, err := authService.Login(&req)
		if err != nil {
			limiter.RecordFailure(requestClientIP(c), req.Username)
			writeAuthError(c, "login", err)
			return
		}
		limiter.Reset(requestClientIP(c), req.Username)

		c.JSON(http.StatusOK, resp)
	}
}

func firstAuthRateLimiter(limiters []*AuthRateLimiter) *AuthRateLimiter {
	if len(limiters) == 0 {
		return nil
	}
	return limiters[0]
}

func requestClientIP(c *gin.Context) string {
	if ip := c.GetString("client_ip"); ip != "" {
		return ip
	}
	return c.RemoteIP()
}

// CreateSessionHandler 创建会话
func CreateSessionHandler(sessionService *service.SessionService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)

		var req service.CreateSessionRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}

		session, err := sessionService.Create(userID, &req)
		if err != nil {
			writeSessionMutationError(c, "create", err)
			return
		}

		c.JSON(http.StatusCreated, session)
	}
}

func SessionCreateReadinessHandler(sessionService *service.SessionService) gin.HandlerFunc {
	return func(c *gin.Context) {
		readiness, err := sessionService.CreateReadiness(middleware.GetUserID(c))
		if err != nil {
			writeServerError(c, http.StatusInternalServerError, "session_readiness_failed", "failed to load session readiness", err)
			return
		}
		c.JSON(http.StatusOK, readiness)
	}
}

// ListSessionsHandler 获取会话列表
func ListSessionsHandler(sessionService *service.SessionService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)

		limit, offset, ok := parseSessionListPagination(c)
		if !ok {
			return
		}

		filter, err := parseSessionListFilter(c.Query("folder_id"))
		if err != nil {
			writePublicError(c, http.StatusBadRequest, "session_list_query_invalid", "folder_id must be all, unfiled, or a positive integer", false)
			return
		}

		result, err := sessionService.List(userID, limit, offset, filter)
		if err != nil {
			writeServerError(c, http.StatusInternalServerError, "session_list_failed", "failed to list sessions", err)
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"sessions":    result.Sessions,
			"has_more":    result.HasMore,
			"next_offset": result.NextOffset,
		})
	}
}

func parseSessionListPagination(c *gin.Context) (int, int, bool) {
	limit := 100
	if raw := c.Query("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > 100 {
			writePublicError(c, http.StatusBadRequest, "session_list_query_invalid", "limit must be between 1 and 100", false)
			return 0, 0, false
		}
		limit = parsed
	}
	offset := 0
	if raw := c.Query("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			writePublicError(c, http.StatusBadRequest, "session_list_query_invalid", "offset must be zero or greater", false)
			return 0, 0, false
		}
		offset = parsed
	}
	return limit, offset, true
}

func parseSessionListFilter(raw string) (service.SessionListFilter, error) {
	if raw == "" || raw == "all" {
		return service.SessionListFilter{}, nil
	}
	if raw == "unfiled" {
		return service.SessionListFilter{Unfiled: true}, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return service.SessionListFilter{}, errors.New("invalid folder_id")
	}
	return service.SessionListFilter{FolderID: &id}, nil
}

// GetSessionHandler 获取单个会话
func GetSessionHandler(sessionService *service.SessionService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)

		var uri struct {
			ID int64 `uri:"id" binding:"required"`
		}
		if err := c.ShouldBindUri(&uri); err != nil {
			writePublicError(c, http.StatusBadRequest, "session_id_invalid", "invalid session id", false)
			return
		}

		session, err := sessionService.GetByID(uri.ID, userID)
		if err != nil {
			writeSessionLookupError(c, "load", err)
			return
		}

		c.JSON(http.StatusOK, session)
	}
}

// UpdateSessionHandler 更新会话
func UpdateSessionHandler(sessionService *service.SessionService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)

		var uri struct {
			ID int64 `uri:"id" binding:"required"`
		}
		if err := c.ShouldBindUri(&uri); err != nil {
			writePublicError(c, http.StatusBadRequest, "session_id_invalid", "invalid session id", false)
			return
		}

		var req service.UpdateSessionRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}

		if err := sessionService.Update(uri.ID, userID, &req); err != nil {
			writeSessionMutationError(c, "update", err)
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "session updated"})
	}
}

// DeleteSessionHandler 删除会话
func DeleteSessionHandler(sessionService *service.SessionService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)

		var uri struct {
			ID int64 `uri:"id" binding:"required"`
		}
		if err := c.ShouldBindUri(&uri); err != nil {
			writePublicError(c, http.StatusBadRequest, "session_id_invalid", "invalid session id", false)
			return
		}

		if err := sessionService.Delete(uri.ID, userID); err != nil {
			writeSessionMutationError(c, "delete", err)
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "session deleted"})
	}
}
