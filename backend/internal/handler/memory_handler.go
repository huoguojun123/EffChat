package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/agent"
	sessionmemory "github.com/huoguojun123/EffChat/internal/memory"
	"github.com/huoguojun123/EffChat/internal/middleware"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
)

type sessionMemoryResponse struct {
	Enabled           bool                          `json:"enabled"`
	Content           string                        `json:"content"`
	Sections          []sessionmemory.Section       `json:"sections"`
	Stats             sessionmemory.Stats           `json:"stats"`
	Changes           []sessionMemoryChangeResponse `json:"changes"`
	LastAutoUpdatedAt *time.Time                    `json:"last_auto_updated_at,omitempty"`
	LastTaskRun       *modelTaskRunResponse         `json:"last_task_run,omitempty"`
	TaskRuns          []modelTaskRunResponse        `json:"task_runs,omitempty"`
	UpdatedAt         *time.Time                    `json:"updated_at,omitempty"`
}

type sessionMemoryChangeResponse struct {
	ID        int64      `json:"id"`
	SessionID int64      `json:"session_id"`
	UserID    int64      `json:"user_id"`
	Source    string     `json:"source"`
	Action    string     `json:"action"`
	Summary   string     `json:"summary"`
	CreatedAt time.Time  `json:"created_at"`
	UndoneAt  *time.Time `json:"undone_at,omitempty"`
}

type saveSessionMemoryRequest struct {
	Enabled           *bool                   `json:"enabled"`
	Content           string                  `json:"content"`
	Sections          []sessionmemory.Section `json:"sections"`
	ExpectedUpdatedAt *time.Time              `json:"expected_updated_at"`
}

func GetSessionMemoryHandler(sessionService *service.SessionService, memoryRepo *repository.SessionMemoryRepository, taskRunRepo *repository.ModelTaskRunRepository, configRepo *repository.ConfigRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		sessionID, ok := parseSessionID(c)
		if !ok {
			return
		}
		session, err := sessionService.GetByID(sessionID, userID)
		if err != nil {
			writePublicError(c, http.StatusNotFound, "session_not_found", "session not found", false)
			return
		}
		resp, err := buildSessionMemoryResponse(c.Request.Context(), memoryRepo, taskRunRepo, configRepo, session.ID, userID, session.MemoryEnabled)
		if err != nil {
			writeServerError(c, http.StatusInternalServerError, "memory_load_failed", "failed to load memory", err)
			return
		}
		c.JSON(http.StatusOK, resp)
	}
}

func SaveSessionMemoryHandler(sessionService *service.SessionService, memoryRepo *repository.SessionMemoryRepository, taskRunRepo *repository.ModelTaskRunRepository, configRepo *repository.ConfigRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		sessionID, ok := parseSessionID(c)
		if !ok {
			return
		}
		session, err := sessionService.GetByID(sessionID, userID)
		if err != nil {
			writePublicError(c, http.StatusNotFound, "session_not_found", "session not found", false)
			return
		}
		var req saveSessionMemoryRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeInvalidJSON(c)
			return
		}
		content := req.Content
		if req.Sections != nil {
			content = sessionmemory.Serialize(sessionmemory.Document{Sections: req.Sections})
		}
		limits := memoryLimitsFromConfig(configRepo)
		normalized, _, err := sessionmemory.NormalizeWithLimits(content, limits)
		if err != nil {
			writePublicError(c, http.StatusBadRequest, "invalid_memory_content", err.Error(), false)
			return
		}
		before, updatedAt, err := memoryRepo.GetWithUpdatedAt(c.Request.Context(), sessionID)
		if err != nil {
			writeServerError(c, http.StatusInternalServerError, "memory_save_failed", "failed to save memory", err)
			return
		}
		if req.ExpectedUpdatedAt != nil && (updatedAt.IsZero() || !updatedAt.Equal(*req.ExpectedUpdatedAt)) {
			c.JSON(http.StatusConflict, gin.H{"error": "会话记忆已在后台更新，请重新加载后再保存", "code": "session_memory_conflict", "retryable": true})
			return
		}
		action := "update"
		if strings.TrimSpace(normalized) == "" {
			action = "clear"
		}
		if _, err := memoryRepo.SaveWithChange(c.Request.Context(), repository.SaveSessionMemoryInput{
			SessionID:      sessionID,
			UserID:         userID,
			MemoryEnabled:  req.Enabled,
			Content:        normalized,
			Source:         "manual",
			Action:         action,
			Summary:        sessionmemory.Summary(before, normalized),
			ExpectedBefore: before,
			CheckBefore:    true,
			MaxChars:       limits.MaxChars,
		}); err != nil {
			if errors.Is(err, repository.ErrSessionMemoryConflict) {
				c.JSON(http.StatusConflict, sessionMemoryConflictPayload())
				return
			}
			writeServerError(c, http.StatusInternalServerError, "memory_save_failed", "failed to save memory", err)
			return
		}
		if req.Enabled != nil {
			session.MemoryEnabled = *req.Enabled
		}
		resp, err := buildSessionMemoryResponse(c.Request.Context(), memoryRepo, taskRunRepo, configRepo, session.ID, userID, session.MemoryEnabled)
		if err != nil {
			writeServerError(c, http.StatusInternalServerError, "memory_load_failed", "failed to load memory", err)
			return
		}
		c.JSON(http.StatusOK, resp)
	}
}

func answerSelectionChangedPayload() gin.H {
	return gin.H{
		"error":     "回答版本已变化，请重新执行记忆维护",
		"code":      "answer_selection_changed",
		"retryable": true,
	}
}

func sessionMemoryConflictPayload() gin.H {
	return gin.H{
		"error":     "会话记忆已在后台更新，请重新加载后再操作",
		"code":      "session_memory_conflict",
		"retryable": true,
	}
}

func memoryMaintenanceFailurePayload(err error) (gin.H, bool) {
	switch {
	case errors.Is(err, agent.ErrMemoryMaintenanceOutputBudgetInsufficient):
		return gin.H{
			"error":     err.Error(),
			"code":      "memory_output_budget_insufficient",
			"retryable": false,
		}, true
	case errors.Is(err, agent.ErrMemoryMaintenanceOutputLimit):
		return gin.H{
			"error":     "模型达到输出上限，会话记忆没有变化；请降低记忆容量或切换到更高输出模型后重试",
			"code":      "memory_output_limit",
			"retryable": true,
		}, true
	default:
		return nil, false
	}
}

func UndoSessionMemoryChangeHandler(sessionService *service.SessionService, memoryRepo *repository.SessionMemoryRepository, taskRunRepo *repository.ModelTaskRunRepository, configRepo *repository.ConfigRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		sessionID, ok := parseSessionID(c)
		if !ok {
			return
		}
		if _, err := sessionService.GetByID(sessionID, userID); err != nil {
			writePublicError(c, http.StatusNotFound, "session_not_found", "session not found", false)
			return
		}
		changeID, err := strconv.ParseInt(c.Param("change_id"), 10, 64)
		if err != nil || changeID <= 0 {
			writePublicError(c, http.StatusBadRequest, "memory_change_id_invalid", "invalid change_id", false)
			return
		}
		if _, err := memoryRepo.UndoChange(c.Request.Context(), sessionID, userID, changeID); err != nil {
			writeMemoryUndoError(c, err)
			return
		}
		session, _ := sessionService.GetByID(sessionID, userID)
		resp, err := buildSessionMemoryResponse(c.Request.Context(), memoryRepo, taskRunRepo, configRepo, sessionID, userID, session != nil && session.MemoryEnabled)
		if err != nil {
			writeServerError(c, http.StatusInternalServerError, "memory_load_failed", "failed to load memory", err)
			return
		}
		c.JSON(http.StatusOK, resp)
	}
}

func writeMemoryUndoError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, repository.ErrMemoryChangeNotFound):
		writePublicError(c, http.StatusNotFound, "memory_change_not_found", "memory change not found", false)
	case errors.Is(err, repository.ErrMemoryChangeNotUndoable):
		writePublicError(c, http.StatusConflict, "memory_undo_unavailable", "only the latest memory organization can be undone", false)
	default:
		writeServerError(c, http.StatusInternalServerError, "memory_undo_failed", "failed to undo memory change", err)
	}
}

func memoryModelRequest(session *model.Session, userID int64, userPreferences []byte) *agent.ChatRequest {
	if session == nil {
		return nil
	}
	modelInfo := modelbank.GetOrDefault(session.ModelID, session.Provider)
	req := &agent.ChatRequest{
		UserID:          userID,
		SessionID:       session.ID,
		ModelID:         session.ModelID,
		Provider:        session.Provider,
		MessageFormat:   session.MessageFormat,
		MemoryEnabled:   session.MemoryEnabled,
		UserPreferences: userPreferences,
		ContextWindow:   modelInfo.Capabilities.ContextWindow,
		ModelMaxOutput:  modelInfo.Capabilities.MaxOutput,
		Vision:          modelInfo.Capabilities.Vision,
		ToolUse:         modelInfo.Capabilities.ToolUse,
		Reasoning:       modelInfo.Capabilities.Reasoning,
		ThinkingFormat:  modelInfo.ThinkingFormat,
		SearchImpl:      modelInfo.Capabilities.SearchImpl,
	}
	if session.SystemPrompt != nil {
		req.SystemPrompt = *session.SystemPrompt
	}
	if session.Temperature != nil {
		req.Temperature = session.Temperature
	}
	if session.MaxTokens != nil {
		req.MaxTokens = *session.MaxTokens
	}
	return req
}

func latestMemoryRetryUserText(ctx context.Context, messageRepo *repository.MessageRepository, sessionID int64) (string, error) {
	if messageRepo == nil {
		return "", fmt.Errorf("message repository is unavailable")
	}
	messages, err := messageRepo.ListBySessionContext(ctx, sessionID)
	if err != nil {
		return "", err
	}
	return latestMemoryRetryUserTextFromMessages(messages)
}

func latestMemoryRetryUserTextFromMessages(messages []*model.Message) (string, error) {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg == nil {
			continue
		}
		data, err := repository.ParseMessageData(msg.MessageData)
		if err != nil {
			continue
		}
		role, _ := data["role"].(string)
		if role == "" {
			role = msg.Role
		}
		if role != "user" || isMemoryRetryCompactionSummary(data) {
			continue
		}
		content, _ := data["content"].(string)
		content = strings.TrimSpace(content)
		if content != "" {
			return content, nil
		}
	}
	return "", fmt.Errorf("no user message available for memory retry")
}

func isMemoryRetryCompactionSummary(data map[string]interface{}) bool {
	extra, ok := data["extra"].(map[string]interface{})
	if ok {
		ct, _ := extra["_eino_summarization_content_type"].(string)
		if ct == "summary" {
			return true
		}
	}
	meta, ok := data["metadata"].(map[string]interface{})
	if !ok {
		return false
	}
	flag, _ := meta["compaction_summary"].(bool)
	return flag
}

func buildSessionMemoryResponse(ctx context.Context, memoryRepo *repository.SessionMemoryRepository, taskRunRepo *repository.ModelTaskRunRepository, configRepo *repository.ConfigRepository, sessionID, userID int64, enabled bool) (*sessionMemoryResponse, error) {
	content, updatedAt, err := memoryRepo.GetWithUpdatedAt(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	doc, err := sessionmemory.Parse(content)
	if err != nil {
		return nil, err
	}
	changes, err := memoryRepo.ListChanges(ctx, sessionID, userID, sessionmemory.MaxChangeList)
	if err != nil {
		return nil, err
	}
	changeResponses := make([]sessionMemoryChangeResponse, 0, len(changes))
	var lastAuto *time.Time
	for _, change := range changes {
		changeResponses = append(changeResponses, sessionMemoryChangeResponse{
			ID:        change.ID,
			SessionID: change.SessionID,
			UserID:    change.UserID,
			Source:    change.Source,
			Action:    change.Action,
			Summary:   change.Summary,
			CreatedAt: change.CreatedAt,
			UndoneAt:  change.UndoneAt,
		})
		if change.Source == "auto" {
			t := change.CreatedAt
			lastAuto = &t
			break
		}
	}
	var updated *time.Time
	if !updatedAt.IsZero() {
		updated = &updatedAt
	}
	var taskRuns []repository.ModelTaskRun
	if taskRunRepo != nil {
		var err error
		taskRuns, err = taskRunRepo.ListForSession(ctx, sessionID, userID, repository.ModelTaskMemoryMaintenance, 5)
		if err != nil {
			return nil, err
		}
	}
	taskRunResponses := make([]modelTaskRunResponse, 0, len(taskRuns))
	for i := range taskRuns {
		if response := toModelTaskRunResponse(&taskRuns[i]); response != nil {
			taskRunResponses = append(taskRunResponses, *response)
		}
	}
	var lastRun *modelTaskRunResponse
	if len(taskRunResponses) > 0 {
		lastRun = &taskRunResponses[0]
	}
	return &sessionMemoryResponse{
		Enabled:           enabled,
		Content:           sessionmemory.Serialize(doc),
		Sections:          doc.Sections,
		Stats:             sessionmemory.StatsForWithLimits(sessionmemory.Serialize(doc), memoryLimitsFromConfig(configRepo)),
		Changes:           changeResponses,
		LastAutoUpdatedAt: lastAuto,
		LastTaskRun:       lastRun,
		TaskRuns:          taskRunResponses,
		UpdatedAt:         updated,
	}, nil
}

func memoryLimitsFromConfig(configRepo *repository.ConfigRepository) sessionmemory.Limits {
	if configRepo != nil {
		return configRepo.GetMemoryLimits()
	}
	return sessionmemory.DefaultLimits()
}

func parseSessionID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session id"})
		return 0, false
	}
	return id, true
}
