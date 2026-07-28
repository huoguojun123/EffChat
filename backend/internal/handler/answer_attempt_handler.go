package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/effchat/internal/agent"
	"github.com/huoguojun123/effchat/internal/middleware"
	"github.com/huoguojun123/effchat/internal/model"
	"github.com/huoguojun123/effchat/internal/repository"
	"github.com/huoguojun123/effchat/internal/service"
	"github.com/huoguojun123/effchat/pkg/logger"
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
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid answer attempt id", "code": "answer_attempt_invalid"})
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

func writeAnswerAttemptSelectionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found", "code": "session_not_found"})
	case errors.Is(err, repository.ErrAnswerAttemptNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "answer attempt not found", "code": "answer_attempt_not_found"})
	case errors.Is(err, repository.ErrAnswerAttemptNotLatest):
		c.JSON(http.StatusConflict, gin.H{"error": "只能切换当前最后一轮的回答", "code": "answer_attempt_not_latest"})
	case errors.Is(err, repository.ErrAnswerAttemptNotSelectable):
		c.JSON(http.StatusConflict, gin.H{"error": "该回答不可切换", "code": "answer_attempt_not_selectable"})
	default:
		logger.Error("select answer attempt failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "切换回答失败，请重试", "code": "answer_attempt_select_failed", "retryable": true})
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
