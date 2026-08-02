package handler

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/middleware"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
)

// ListMessagesHandler 获取会话消息列表（游标分页：默认返回最新一页，向上回溯传 before_id）
func ListMessagesHandler(messageService *service.MessageService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)

		sessionIDStr := c.Param("id")
		sessionID, err := strconv.ParseInt(sessionIDStr, 10, 64)
		if err != nil {
			writePublicError(c, http.StatusBadRequest, "session_id_invalid", "invalid session id", false)
			return
		}

		// limit 默认 30，封顶 100 防止一次拉取过多。
		limit := 30
		if v, err := strconv.Atoi(c.Query("limit")); err == nil && v > 0 {
			limit = v
		}
		if limit > 100 {
			limit = 100
		}
		// before_id 为游标：取 id 小于它的更早一页；缺省/<=0 表示取最新一页。
		var beforeID int64
		if v, err := strconv.ParseInt(c.Query("before_id"), 10, 64); err == nil && v > 0 {
			beforeID = v
		}

		messages, hasMore, err := messageService.ListBySessionPaged(sessionID, userID, limit, beforeID)
		if err != nil {
			writeMessageReadError(c, "list", err)
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"messages": messages,
			"has_more": hasMore,
		})
	}
}

func ListConversationTurnsHandler(messageService *service.MessageService) gin.HandlerFunc {
	return func(c *gin.Context) {
		sessionID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || sessionID <= 0 {
			writePublicError(c, http.StatusBadRequest, "session_id_invalid", "invalid session id", false)
			return
		}

		limit := 500
		if raw := c.Query("limit"); raw != "" {
			limit, err = strconv.Atoi(raw)
			if err != nil || limit < 1 || limit > 500 {
				writePublicError(c, http.StatusBadRequest, "message_limit_invalid", "limit must be between 1 and 500", false)
				return
			}
		}

		var beforeTurnID int64
		if raw := c.Query("before_turn_id"); raw != "" {
			beforeTurnID, err = strconv.ParseInt(raw, 10, 64)
			if err != nil || beforeTurnID <= 0 {
				writePublicError(c, http.StatusBadRequest, "before_turn_id_invalid", "invalid before_turn_id", false)
				return
			}
		}

		page, err := messageService.ListConversationTurns(sessionID, middleware.GetUserID(c), limit, beforeTurnID)
		if err != nil {
			writeMessageReadError(c, "list", err)
			return
		}
		c.JSON(http.StatusOK, page)
	}
}

func ListMessageWindowHandler(messageService *service.MessageService) gin.HandlerFunc {
	return func(c *gin.Context) {
		sessionID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || sessionID <= 0 {
			writePublicError(c, http.StatusBadRequest, "session_id_invalid", "invalid session id", false)
			return
		}

		mode, targetTurnID, err := messageWindowQuery(c)
		if err != nil {
			writePublicError(c, http.StatusBadRequest, "message_window_invalid", err.Error(), false)
			return
		}
		turnLimit := 16
		if raw := c.Query("turn_limit"); raw != "" {
			turnLimit, err = strconv.Atoi(raw)
			if err != nil || turnLimit < 1 || turnLimit > 16 {
				writePublicError(c, http.StatusBadRequest, "turn_limit_invalid", "turn_limit must be between 1 and 16", false)
				return
			}
		}

		window, err := messageService.ListMessageWindow(sessionID, middleware.GetUserID(c), mode, targetTurnID, turnLimit)
		if err != nil {
			writeMessageReadError(c, "window", err)
			return
		}
		c.JSON(http.StatusOK, window)
	}
}

func messageWindowQuery(c *gin.Context) (repository.MessageWindowMode, int64, error) {
	type option struct {
		name string
		mode repository.MessageWindowMode
	}
	options := []option{
		{name: "before_turn_id", mode: repository.MessageWindowBefore},
		{name: "after_turn_id", mode: repository.MessageWindowAfter},
		{name: "around_turn_id", mode: repository.MessageWindowAround},
	}

	mode := repository.MessageWindowLatest
	var targetTurnID int64
	selected := 0
	if raw := c.Query("latest"); raw != "" {
		latest, err := strconv.ParseBool(raw)
		if err != nil || !latest {
			return "", 0, fmt.Errorf("latest must be true")
		}
		selected++
	}
	for _, candidate := range options {
		raw := c.Query(candidate.name)
		if raw == "" {
			continue
		}
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			return "", 0, fmt.Errorf("invalid %s", candidate.name)
		}
		selected++
		mode = candidate.mode
		targetTurnID = id
	}
	if selected > 1 {
		return "", 0, fmt.Errorf("message window selectors are mutually exclusive")
	}
	return mode, targetTurnID, nil
}

// UndoCompactionHandler 撤销会话最近一次压缩检查点。
func UndoCompactionHandler(messageService *service.MessageService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)

		sessionID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			writePublicError(c, http.StatusBadRequest, "session_id_invalid", "invalid session id", false)
			return
		}

		restored, err := messageService.UndoLastCompaction(sessionID, userID)
		if err != nil {
			writeCompactionUndoError(c, err)
			return
		}

		c.JSON(http.StatusOK, gin.H{"restored": restored})
	}
}
