package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/agent"
	"github.com/huoguojun123/EffChat/internal/middleware"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
	"github.com/huoguojun123/EffChat/pkg/logger"
)

func SelectAnswerAttemptHandler(messageService *service.MessageService, sessionService *service.SessionService, authService *service.AuthService, einoAgent *agent.EinoAgent) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		sessionID, ok := parseSessionID(c)
		if !ok {
			return
		}
		attemptID, err := strconv.ParseInt(c.Param("attempt_id"), 10, 64)
		if err != nil || attemptID <= 0 {
			writePublicError(c, http.StatusBadRequest, "answer_attempt_invalid", "invalid answer attempt id", false)
			return
		}

		attempt, err := messageService.SelectAnswerAttempt(c.Request.Context(), sessionID, userID, attemptID)
		if err != nil {
			writeAnswerAttemptSelectionError(c, err)
			return
		}

		memoryReconciliationStarted := startAnswerSelectionMemoryReconciliation(c, messageService, sessionService, authService, einoAgent, sessionID, userID, attempt)
		c.JSON(http.StatusOK, gin.H{
			"attempt_id":                    attempt.ID,
			"selected":                      attempt.Selected,
			"selection_changed":             attempt.SelectionChanged,
			"answer_selection_revision":     attempt.SelectionRevision,
			"memory_reconciliation_started": memoryReconciliationStarted,
		})
	}
}

func DeleteAnswerAttemptHandler(messageService *service.MessageService, sessionService *service.SessionService, authService *service.AuthService, einoAgent *agent.EinoAgent) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		sessionID, ok := parseSessionID(c)
		if !ok {
			return
		}
		attemptID, err := strconv.ParseInt(c.Param("attempt_id"), 10, 64)
		if err != nil || attemptID <= 0 {
			writePublicError(c, http.StatusBadRequest, "answer_attempt_invalid", "invalid answer attempt id", false)
			return
		}

		deletion, err := messageService.DeleteAnswerAttempt(c.Request.Context(), sessionID, userID, attemptID)
		if err != nil {
			writeAnswerAttemptDeletionError(c, err)
			return
		}
		memoryReconciliationStarted := false
		selectedAttemptID := int64(0)
		if deletion.SelectedAttempt != nil {
			selectedAttemptID = deletion.SelectedAttempt.ID
			memoryReconciliationStarted = startAnswerSelectionMemoryReconciliation(c, messageService, sessionService, authService, einoAgent, sessionID, userID, deletion.SelectedAttempt)
		}
		c.JSON(http.StatusOK, gin.H{
			"deleted_attempt_id":            deletion.DeletedAttemptID,
			"selected_attempt_id":           selectedAttemptID,
			"selection_changed":             deletion.SelectionChanged,
			"answer_selection_revision":     deletion.SelectionRevision,
			"memory_reconciliation_started": memoryReconciliationStarted,
		})
	}
}

func writeAnswerAttemptSelectionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		writePublicError(c, http.StatusNotFound, "session_not_found", "session not found", false)
	case errors.Is(err, repository.ErrAnswerAttemptNotFound):
		writePublicError(c, http.StatusNotFound, "answer_attempt_not_found", "answer attempt not found", false)
	case errors.Is(err, repository.ErrAnswerAttemptNotSelectable):
		writePublicError(c, http.StatusConflict, "answer_attempt_not_selectable", "该回答不可切换", false)
	default:
		writeServerError(c, http.StatusInternalServerError, "answer_attempt_select_failed", "切换回答失败，请重试", err)
	}
}

func writeAnswerAttemptDeletionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		writePublicError(c, http.StatusNotFound, "session_not_found", "session not found", false)
	case errors.Is(err, repository.ErrAnswerAttemptNotFound):
		writePublicError(c, http.StatusNotFound, "answer_attempt_not_found", "answer attempt not found", false)
	case errors.Is(err, repository.ErrAnswerAttemptNotSelectable):
		writePublicError(c, http.StatusConflict, "answer_attempt_not_selectable", "该回答不可删除", false)
	case errors.Is(err, repository.ErrAnswerAttemptLastRemaining):
		writePublicError(c, http.StatusConflict, "answer_attempt_last_remaining", "每轮至少需要保留一个回答", false)
	default:
		writeServerError(c, http.StatusInternalServerError, "answer_attempt_delete_failed", "删除回答失败，请重试", err)
	}
}

func startAnswerSelectionMemoryReconciliation(c *gin.Context, messageService *service.MessageService, sessionService *service.SessionService, authService *service.AuthService, einoAgent *agent.EinoAgent, sessionID, userID int64, attempt *repository.AnswerAttempt) bool {
	if attempt == nil || attempt.ID <= 0 || attempt.UserMessageID <= 0 || !attempt.SelectionChanged || attempt.SelectionRevision <= 0 {
		return false
	}
	if einoAgent == nil {
		return false
	}
	session, err := sessionService.GetByIDContext(c.Request.Context(), sessionID, userID)
	if err != nil {
		logger.Error("reload selected session for memory reconciliation failed: session=%d err=%v", sessionID, err)
		return false
	}
	if !session.MemoryEnabled {
		return false
	}
	user, err := authService.GetProfileContext(c.Request.Context(), userID)
	if err != nil {
		logger.Error("load user for answer selection memory reconciliation failed: user=%d err=%v", userID, err)
		return false
	}
	messages, err := messageService.ListForAgentContext(c.Request.Context(), sessionID, userID)
	if err != nil {
		logger.Error("load selected answer memory context failed: session=%d err=%v", sessionID, err)
		return false
	}
	messages, err = selectedAnswerMemoryMessages(messages, attempt)
	if err != nil {
		logger.Error("scope selected answer memory context failed: session=%d attempt=%d err=%v", sessionID, attempt.ID, err)
		return false
	}
	userText, err := latestMemoryRetryUserTextFromMessages(messages)
	if err != nil {
		logger.Error("load selected answer user message failed: session=%d err=%v", sessionID, err)
		return false
	}
	revision := attempt.SelectionRevision
	einoAgent.MaintainSessionMemoryAsync(agent.MemoryMaintenanceRequest{
		SessionID:                       sessionID,
		UserID:                          userID,
		UserText:                        userText,
		ContextText:                     service.RecentConversationTextForMemoryMessages(messages, 5),
		MemoryEnabled:                   session.MemoryEnabled,
		ExpectedAnswerSelectionRevision: &revision,
		Source:                          "auto",
		Force:                           true,
		IgnoreCooldown:                  true,
		ModelRequest:                    memoryModelRequest(session, userID, user.Preferences),
	})
	return true
}

func selectedAnswerMemoryMessages(messages []*model.Message, attempt *repository.AnswerAttempt) ([]*model.Message, error) {
	result := make([]*model.Message, 0, len(messages))
	foundTarget := false
	foundAttemptOutput := false
	for _, message := range messages {
		if message == nil {
			continue
		}
		if message.ID == attempt.UserMessageID {
			foundTarget = true
			result = append(result, message)
			continue
		}
		if !foundTarget {
			result = append(result, message)
			continue
		}
		if message.Role == "user" {
			break
		}
		if message.AnswerAttemptID != nil && *message.AnswerAttemptID == attempt.ID {
			result = append(result, message)
			if message.Role == "assistant" {
				foundAttemptOutput = true
			}
		}
	}
	if !foundTarget || !foundAttemptOutput {
		return nil, repository.ErrRetryTargetStale
	}
	return result, nil
}
