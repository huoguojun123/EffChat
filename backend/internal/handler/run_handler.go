package handler

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/effchat/internal/middleware"
	"github.com/huoguojun123/effchat/internal/repository"
	"github.com/huoguojun123/effchat/internal/service"
	"github.com/huoguojun123/effchat/pkg/streaming"
)

func ActiveRunHandler(runHub *service.RunHub, sessionService *service.SessionService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		sessionID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session id"})
			return
		}
		if _, err := sessionService.GetByID(sessionID, userID); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		snapshot := runHub.Active(sessionID, userID)
		c.JSON(http.StatusOK, gin.H{
			"run": snapshot,
		})
	}
}

type chatRunStatusResponse struct {
	RunID             string `json:"run_id"`
	SessionID         int64  `json:"session_id"`
	Kind              string `json:"kind"`
	Status            string `json:"status"`
	UserMessageID     int64  `json:"user_message_id,omitempty"`
	TerminalMessageID int64  `json:"terminal_message_id,omitempty"`
	ErrorCode         string `json:"error_code,omitempty"`
	Error             string `json:"error,omitempty"`
}

func chatRunStatusPayload(record repository.ChatRunRecord) chatRunStatusResponse {
	return chatRunStatusResponse{
		RunID:             record.RunID,
		SessionID:         record.SessionID,
		Kind:              record.Kind,
		Status:            record.Status,
		UserMessageID:     record.UserMessageID,
		TerminalMessageID: record.TerminalMessageID,
		ErrorCode:         record.PublicErrorCode,
		Error:             record.PublicErrorMessage,
	}
}

func RunStatusHandler(quotaService *service.QuotaService, sessionService *service.SessionService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		sessionID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session id"})
			return
		}
		if _, err := sessionService.GetByID(sessionID, userID); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		record, err := quotaService.GetChatRunForSession(c.Request.Context(), userID, sessionID, c.Param("run_id"))
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read run status"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"run": chatRunStatusPayload(record)})
	}
}

func ResumeRunHandler(runHub *service.RunHub, sessionService *service.SessionService, heartbeat time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		sessionID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session id"})
			return
		}
		if _, err := sessionService.GetByID(sessionID, userID); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		cursor, _ := strconv.ParseInt(c.Query("cursor"), 10, 64)
		runID := c.Param("run_id")

		events, ch, cleanup, snapshot, err := runHub.EventsAfter(runID, sessionID, userID, cursor)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		if cleanup != nil {
			defer cleanup()
		}

		writer, err := streaming.NewSSEWriter(c)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming not supported"})
			return
		}

		if payload, gap := replayGapPayload(snapshot, cursor); gap {
			if err := writer.WriteEvent("replay_gap", payload); err != nil {
				return
			}
			if err := writer.WriteEvent("run_snapshot", snapshot); err != nil {
				return
			}
			cursor = snapshot.Cursor
		}
		forwardRunEvents(c, writer, runHub, heartbeat, sessionID, userID, runID, events, ch, cursor)
	}
}

func replayGapPayload(snapshot *service.RunSnapshot, requestedCursor int64) (gin.H, bool) {
	if snapshot == nil || !snapshot.OutputTruncated || snapshot.ReplayFrom <= 0 || requestedCursor >= snapshot.ReplayFrom {
		return nil, false
	}
	return gin.H{
		"requested_cursor": requestedCursor,
		"replay_from":      snapshot.ReplayFrom,
	}, true
}

func forwardRunEvents(c *gin.Context, writer *streaming.SSEWriter, runHub *service.RunHub, heartbeat time.Duration, sessionID, userID int64, runID string, events []service.RunEvent, ch <-chan service.RunEvent, cursor int64) {
	lastCursor := cursor
	if err := writeRunEvents(writer, events, &lastCursor); err != nil || ch == nil {
		return
	}
	var heartbeatTicker *time.Ticker
	var heartbeatC <-chan time.Time
	if heartbeat > 0 {
		heartbeatTicker = time.NewTicker(heartbeat)
		heartbeatC = heartbeatTicker.C
		defer heartbeatTicker.Stop()
	}
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-heartbeatC:
			if err := writer.WritePing(); err != nil {
				return
			}
		case event, ok := <-ch:
			if !ok {
				_ = writeMissedRunEvents(writer, runHub, sessionID, userID, runID, &lastCursor)
				return
			}
			if event.Cursor > lastCursor+1 {
				if err := writeMissedRunEvents(writer, runHub, sessionID, userID, runID, &lastCursor); err != nil {
					return
				}
				continue
			}
			if err := writeRunEvents(writer, []service.RunEvent{event}, &lastCursor); err != nil {
				return
			}
		}
	}
}

func writeMissedRunEvents(writer *streaming.SSEWriter, runHub *service.RunHub, sessionID, userID int64, runID string, cursor *int64) error {
	missed, snapshot, err := runHub.EventsSince(runID, sessionID, userID, *cursor)
	if err != nil {
		return err
	}
	if payload, gap := replayGapPayload(snapshot, *cursor); gap {
		if err := writer.WriteEvent("replay_gap", payload); err != nil {
			return err
		}
		if err := writer.WriteEvent("run_snapshot", snapshot); err != nil {
			return err
		}
		*cursor = snapshot.Cursor
		return nil
	}
	return writeRunEvents(writer, missed, cursor)
}

func writeRunEvents(writer *streaming.SSEWriter, events []service.RunEvent, cursor *int64) error {
	for _, event := range events {
		if event.Cursor <= *cursor {
			continue
		}
		if err := writer.WriteEvent(event.Event, event.Data); err != nil {
			return err
		}
		*cursor = event.Cursor
	}
	return nil
}

// CancelRunHandler 主动中止一个进行中的 run（前端"停止"按钮）。
// 校验会话归属后取消其 context；后端生成方会保存已产出的部分内容并正常结束。
func CancelRunHandler(runHub *service.RunHub, sessionService *service.SessionService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		sessionID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session id"})
			return
		}
		if _, err := sessionService.GetByID(sessionID, userID); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		runID := c.Param("run_id")
		canceled := runHub.Cancel(runID, sessionID, userID)
		c.JSON(http.StatusOK, gin.H{"canceled": canceled})
	}
}
