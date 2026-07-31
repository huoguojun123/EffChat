package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/agent"
	"github.com/huoguojun123/EffChat/internal/middleware"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelstream"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
	modelusage "github.com/huoguojun123/EffChat/internal/usage"
	"github.com/huoguojun123/EffChat/pkg/logger"
	"github.com/huoguojun123/EffChat/pkg/streaming"
)

// stream_handler 是 HTTP/SSE 适配层和当前 Chat run 生命周期的临时编排点。
//
// 这里的核心边界是：浏览器连接可以断，但后端 run 不能因为连接断开就丢失结果。
// 因此 Agent 调用使用脱离请求生命周期的 context，并通过 RunHub 记录每个 SSE 事件；
// 前端刷新或重连后可按 run_id 重放事件，最终仍以数据库消息为真实历史。
//
// 后续如果继续拆分，应把 run 生命周期下沉到 ChatRunService，handler 保留参数解析、
// SSE writer、错误响应和重放适配，不要把 Eino/压缩/标题/usage 的细节继续堆在这里。

const runTerminalWriteTimeout = 5 * time.Second

func reserveSessionRun(c *gin.Context, runHub *service.RunHub, heartbeat, firstOutputTimeout time.Duration, sessionID, userID int64, clientRunID, kind string, intent service.RunIntent) (*service.RunSnapshot, bool) {
	snapshot, err := runHub.StartWithIntentAndFirstOutputTimeout(sessionID, userID, 0, clientRunID, kind, intent, firstOutputTimeout)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrRunIDConflict):
			c.JSON(http.StatusConflict, gin.H{
				"error":     "run_id conflict",
				"code":      "run_id_conflict",
				"retryable": false,
			})
		case errors.Is(err, service.ErrSessionRunActive):
			payload := gin.H{
				"error":     "当前会话仍有任务正在处理，请等待完成或先停止后重试",
				"code":      "session_run_active",
				"retryable": true,
			}
			if active := runHub.Active(sessionID, userID); active != nil {
				payload["active_run_id"] = active.RunID
				payload["active_run_kind"] = active.Kind
			}
			c.JSON(http.StatusConflict, payload)
		case errors.Is(err, service.ErrServerDraining):
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error":     "服务正在更新，请稍后重试",
				"code":      "server_draining",
				"retryable": true,
			})
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		}
		return nil, true
	}
	if !snapshot.Reused {
		return snapshot, false
	}

	writer, err := streaming.NewSSEWriter(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming not supported"})
		return nil, true
	}
	replayExistingRun(c, writer, runHub, heartbeat, sessionID, userID, snapshot.RunID, 0)
	return nil, true
}

func replayKnownSessionRun(c *gin.Context, runHub *service.RunHub, quotaService *service.QuotaService, heartbeat time.Duration, sessionID, userID int64, clientRunID, kind string, intent service.RunIntent) bool {
	runID := strings.TrimSpace(clientRunID)
	if runID == "" {
		return false
	}
	record, err := quotaService.MatchChatRun(c.Request.Context(), service.ChatRunQuotaInput{
		UserID: userID, SessionID: sessionID, RunID: runID, Kind: kind, Intent: intent,
	})
	if err == nil {
		if record.Status != service.RunStatusRunning {
			if _, err := runHub.RestoreTerminal(record, intent); err != nil {
				writeRunIDConflict(c)
				return true
			}
		} else if _, ok, matchErr := runHub.Match(runID, sessionID, userID, kind, intent); matchErr != nil {
			writeRunIDConflict(c)
			return true
		} else if !ok {
			c.JSON(http.StatusConflict, gin.H{
				"error":     "任务状态正在恢复，请稍后重试",
				"code":      "run_state_unavailable",
				"retryable": true,
			})
			return true
		}
	} else if errors.Is(err, service.ErrRunIDConflict) {
		writeRunIDConflict(c)
		return true
	} else if !errors.Is(err, repository.ErrNotFound) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "任务状态读取失败，请重试", "code": "run_state_load_failed", "retryable": true})
		return true
	} else if _, ok, matchErr := runHub.Match(runID, sessionID, userID, kind, intent); matchErr != nil {
		writeRunIDConflict(c)
		return true
	} else if !ok {
		return false
	}
	writer, err := streaming.NewSSEWriter(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming not supported"})
		return true
	}
	replayExistingRun(c, writer, runHub, heartbeat, sessionID, userID, runID, 0)
	return true
}

func writeRunIDConflict(c *gin.Context) {
	c.JSON(http.StatusConflict, gin.H{
		"error":     "run_id conflict",
		"code":      "run_id_conflict",
		"retryable": false,
	})
}

func failRunWithPublicError(c *gin.Context, runHub *service.RunHub, runID, code, message string, err error) gin.H {
	return failRunWithPublicErrorRetryable(c, runHub, runID, code, message, true, err)
}

func failRunWithPublicErrorRetryable(c *gin.Context, runHub *service.RunHub, runID, code, message string, retryable bool, err error) gin.H {
	return failRunWithRequestID(runHub, runID, c.GetString("request_id"), code, message, retryable, err)
}

func failRunWithRequestID(runHub *service.RunHub, runID, requestID, code, message string, retryable bool, err error) gin.H {
	logger.Error("run failed: request_id=%q run_id=%q code=%s err=%v", requestID, runID, code, err)
	payload := gin.H{"error": message, "code": code, "retryable": retryable}
	if requestID != "" {
		payload["request_id"] = requestID
	}
	if _, transitionErr := transitionRun(runHub, runID, service.RunTerminal{
		Status: service.RunStatusFailed, PublicErrorCode: code, PublicErrorMessage: message,
		Event: streaming.EventError, Data: payload,
	}); transitionErr != nil {
		logger.Error("persist run failure failed: request_id=%q run_id=%q code=%s err=%v", requestID, runID, code, transitionErr)
	}
	return payload
}

func transitionRun(runHub *service.RunHub, runID string, terminal service.RunTerminal) (*service.RunEvent, error) {
	ctx, cancel := context.WithTimeout(context.Background(), runTerminalWriteTimeout)
	defer cancel()
	_, _, event, err := runHub.Transition(ctx, runID, terminal)
	return event, err
}

func writeRunTerminal(writer *streaming.SSEWriter, runHub *service.RunHub, runID string, terminal service.RunTerminal) error {
	event, err := transitionRun(runHub, runID, terminal)
	if err != nil {
		return err
	}
	if event == nil || writer == nil {
		return nil
	}
	return writer.WriteEventWithoutRecord(event.Event, event.Data)
}

func transitionRunWithMessages(ctx context.Context, messageService *service.MessageService, runHub *service.RunHub, sessionID, userID int64, runID, schemaVersion string, messages []map[string]interface{}, terminal service.RunTerminal) ([]*model.Message, *service.RunSnapshot, *service.RunEvent, error) {
	var saved []*model.Message
	snapshot, _, event, err := runHub.TransitionWithCommit(ctx, runID, func(commitCtx context.Context, input repository.ChatRunTransitionInput) (repository.ChatRunRecord, bool, error) {
		var record repository.ChatRunRecord
		var transitioned bool
		var commitErr error
		saved, record, transitioned, commitErr = messageService.PersistAgentMessagesAndTransitionContext(commitCtx, sessionID, userID, messages, schemaVersion, runID, input)
		return record, transitioned, commitErr
	}, terminal)
	return saved, snapshot, event, err
}

func runFinalizationContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), runTerminalWriteTimeout)
}

func effectiveRunCancelCause(runHub *service.RunHub, runID string, ctx context.Context) service.RunCancelCause {
	if cause := runHub.CancelCause(runID); cause != "" {
		return cause
	}
	return service.RunCancelCauseFromContext(ctx)
}

func transitionCanceledRun(c *gin.Context, writer *streaming.SSEWriter, runHub *service.RunHub, runID string, runContext context.Context) bool {
	cause := effectiveRunCancelCause(runHub, runID, runContext)
	if cause == "" {
		return false
	}
	event, err := transitionRun(runHub, runID, service.RunTerminal{Status: service.RunStatusCanceled, CancelCause: cause})
	if err != nil {
		logger.Error("persist run cancellation failed: run_id=%q cause=%s err=%v", runID, cause, err)
		if writer == nil && c != nil && !c.Writer.Written() {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "任务取消状态保存失败，请重试", "code": "run_terminal_failed", "retryable": true})
		}
		return true
	}
	if writer != nil {
		if event != nil {
			_ = writer.WriteEventWithoutRecord(event.Event, event.Data)
		}
		return true
	}
	if c != nil && !c.Writer.Written() {
		code, message, retryable := service.RunCancellationPublicError(cause)
		status := http.StatusConflict
		if cause == service.RunCancelServerDrain {
			status = http.StatusServiceUnavailable
		} else if cause == service.RunCancelAccountChanged {
			status = http.StatusUnauthorized
		} else if cause == service.RunCancelSessionDeleted {
			status = http.StatusGone
		}
		payload := gin.H{"error": message, "code": code, "retryable": retryable}
		if requestID := c.GetString("request_id"); requestID != "" {
			payload["request_id"] = requestID
		}
		c.JSON(status, payload)
	}
	return true
}

func transitionReservationFailure(c *gin.Context, runHub *service.RunHub, runID string, sessionID, userID int64, runContext context.Context, err error) bool {
	if !errors.Is(err, service.ErrAuthenticationUnavailable) {
		return false
	}
	runHub.CancelWithCause(runID, sessionID, userID, service.RunCancelAccountChanged)
	return transitionCanceledRun(c, nil, runHub, runID, runContext)
}

func messageCreationFailure(err error) (int, string, string, bool) {
	if errors.Is(err, service.ErrMessageTooLarge) {
		return http.StatusRequestEntityTooLarge, "message_too_large", "消息过长，请拆分发送或作为文件上传", false
	}
	if errors.Is(err, service.ErrTooManyAttachments) {
		return http.StatusRequestEntityTooLarge, "too_many_attachments", "附件数量过多，请分批发送", false
	}
	if errors.Is(err, service.ErrInvalidMessageInput) {
		return http.StatusBadRequest, "message_input_invalid", "消息内容或附件不可用，请检查后重试", false
	}
	return http.StatusInternalServerError, "message_create_failed", "消息创建失败，请重试", true
}

func bindMessageRequest(c *gin.Context, req *service.SendMessageRequest) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.MaxMessageRequestBytes)
	if err := c.ShouldBindJSON(req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "消息过长，请拆分发送或作为文件上传", "code": "message_too_large"})
			return false
		}
		writeInvalidJSON(c)
		return false
	}
	req.ClientRunID = strings.TrimSpace(req.ClientRunID)
	return true
}

// SendMessageStreamHandler 发送消息（SSE 流式）
func SendMessageStreamHandler(messageService *service.MessageService, sessionService *service.SessionService, authService *service.AuthService, skillService *service.SkillService, einoAgent *agent.EinoAgent, titleService *service.TitleService, runHub *service.RunHub, quotaService *service.QuotaService, taskRunRepo *repository.ModelTaskRunRepository, heartbeat, configuredFirstOutputTimeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)

		sessionIDStr := c.Param("id")
		sessionID, err := strconv.ParseInt(sessionIDStr, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session id"})
			return
		}

		var req service.SendMessageRequest
		if !bindMessageRequest(c, &req) {
			return
		}
		session, err := sessionService.GetByID(sessionID, userID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		intent := service.BuildSendRunIntent(session, &req)
		if replayKnownSessionRun(c, runHub, quotaService, heartbeat, sessionID, userID, req.ClientRunID, service.RunKindChat, intent) {
			return
		}
		if err := sessionService.ValidateRunnableModelForUser(session, userID); err != nil {
			writeRuntimeModelError(c, err)
			return
		}
		preflight, failure := evaluateMessagePreflight(c.Request.Context(), messageService, authService, skillService, einoAgent, titleService, taskRunRepo, session, userID, &req)
		if failure != nil {
			writeMessagePreflightFailure(c, failure)
			return
		}
		if preflight.NeedsCompaction {
			c.JSON(http.StatusConflict, gin.H{
				"error":         preflight.Message,
				"message":       preflight.Message,
				"code":          "compaction_required",
				"retryable":     preflight.Retryable,
				"tokens":        preflight.Tokens,
				"threshold":     preflight.Threshold,
				"last_task_run": preflight.LastTaskRun,
			})
			return
		}
		if err := quotaService.CheckBeforeRun(c.Request.Context(), userID, service.QuotaCheck{}); err != nil {
			writeQuotaError(c, err)
			return
		}
		userMessage, err := messageService.BuildUserMessagePreviewContext(c.Request.Context(), sessionID, userID, &req)
		if err != nil {
			status, code, message, retryable := messageCreationFailure(err)
			c.JSON(status, gin.H{"error": message, "code": code, "retryable": retryable})
			return
		}
		firstOutputTimeout := effectiveFirstOutputTimeout(configuredFirstOutputTimeout, defaultChatFirstOutputTimeout)
		runSnapshot, handled := reserveSessionRun(c, runHub, heartbeat, firstOutputTimeout, sessionID, userID, req.ClientRunID, service.RunKindChat, intent)
		if handled {
			return
		}
		runContext, ok := runHub.Context(runSnapshot.RunID)
		if !ok {
			payload := failRunWithPublicError(c, runHub, runSnapshot.RunID, "run_context_missing", "任务状态异常，请重试", errors.New("run context missing"))
			c.JSON(http.StatusInternalServerError, payload)
			return
		}
		var admission service.ChatRunAdmission
		durableSnapshot, err := runHub.PersistAdmission(runContext, runSnapshot.RunID, func(ctx context.Context) (repository.ChatRunRecord, error) {
			admission, err = quotaService.AdmitChatMessage(ctx, service.ChatRunQuotaInput{
				UserID: userID, AuthVersion: middleware.GetAuthVersion(c), SessionID: sessionID, RunID: runSnapshot.RunID, Kind: service.RunKindChat, Intent: intent, ReserveMessage: true,
				RuntimeSnapshot: preflight.runtimeSnapshot,
				AcceptedAt:      runSnapshot.AcceptedAt,
				ExpiresAt:       runSnapshot.ExpiresAt,
			}, userMessage)
			return admission.Record, err
		})
		if err != nil {
			if transitionReservationFailure(c, runHub, runSnapshot.RunID, sessionID, userID, runContext, err) {
				return
			}
			if transitionCanceledRun(c, nil, runHub, runSnapshot.RunID, runContext) {
				return
			}
			if errors.Is(err, service.ErrRunTerminal) {
				c.JSON(http.StatusConflict, gin.H{"error": "任务已结束，请刷新会话", "code": "run_terminal"})
				return
			}
			if errors.Is(err, service.ErrRunIDConflict) {
				writeRunIDConflict(c)
				return
			}
			if failQuotaAdmission(c, runHub, runSnapshot.RunID, err) {
				return
			}
			if errors.Is(err, repository.ErrAttachmentUnavailable) {
				payload := failRunWithPublicErrorRetryable(c, runHub, runSnapshot.RunID, "attachment_unavailable", "附件状态已变化，请刷新后重新选择附件", false, err)
				c.JSON(http.StatusConflict, payload)
				return
			}
			payload := failRunWithPublicError(c, runHub, runSnapshot.RunID, "run_admission_failed", "消息提交失败，请重试", err)
			c.JSON(http.StatusInternalServerError, payload)
			return
		}
		runSnapshot = durableSnapshot
		if runSnapshot.Status != service.RunStatusRunning {
			writer, writerErr := streaming.NewSSEWriter(c)
			if writerErr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming not supported"})
				return
			}
			replayExistingRun(c, writer, runHub, heartbeat, sessionID, userID, runSnapshot.RunID, 0)
			return
		}
		userMessage = admission.Message

		runAgentStream(c, messageService, sessionService, authService, skillService, einoAgent, titleService, runHub, taskRunRepo, heartbeat, firstOutputTimeout, sessionID, userID, userMessage, runSnapshot, modelusage.KindChat)
	}
}

type editRetryRequest struct {
	Content     string `json:"content"`
	ClientRunID string `json:"client_run_id"`
}

func RetryMessageStreamHandler(messageService *service.MessageService, sessionService *service.SessionService, authService *service.AuthService, skillService *service.SkillService, einoAgent *agent.EinoAgent, titleService *service.TitleService, runHub *service.RunHub, quotaService *service.QuotaService, taskRunRepo *repository.ModelTaskRunRepository, heartbeat, configuredFirstOutputTimeout time.Duration) gin.HandlerFunc {
	return retryMessageStreamHandler(messageService, sessionService, authService, skillService, einoAgent, titleService, runHub, quotaService, taskRunRepo, heartbeat, configuredFirstOutputTimeout, false)
}

func EditRetryMessageStreamHandler(messageService *service.MessageService, sessionService *service.SessionService, authService *service.AuthService, skillService *service.SkillService, einoAgent *agent.EinoAgent, titleService *service.TitleService, runHub *service.RunHub, quotaService *service.QuotaService, taskRunRepo *repository.ModelTaskRunRepository, heartbeat, configuredFirstOutputTimeout time.Duration) gin.HandlerFunc {
	return retryMessageStreamHandler(messageService, sessionService, authService, skillService, einoAgent, titleService, runHub, quotaService, taskRunRepo, heartbeat, configuredFirstOutputTimeout, true)
}

func retryMessageStreamHandler(messageService *service.MessageService, sessionService *service.SessionService, authService *service.AuthService, skillService *service.SkillService, einoAgent *agent.EinoAgent, titleService *service.TitleService, runHub *service.RunHub, quotaService *service.QuotaService, taskRunRepo *repository.ModelTaskRunRepository, heartbeat, configuredFirstOutputTimeout time.Duration, edited bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)

		sessionID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session id"})
			return
		}
		messageID, err := strconv.ParseInt(c.Param("message_id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid message id"})
			return
		}
		clientRunID := strings.TrimSpace(c.Query("client_run_id"))
		editedContent := ""
		if edited {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.MaxMessageRequestBytes)
			var req editRetryRequest
			if err := c.ShouldBindJSON(&req); err != nil {
				var maxBytesErr *http.MaxBytesError
				if errors.As(err, &maxBytesErr) {
					c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "消息过长，请缩短后重试", "code": "message_too_large"})
					return
				}
				writeInvalidJSON(c)
				return
			}
			editedContent = req.Content
			clientRunID = strings.TrimSpace(req.ClientRunID)
			if clientRunID == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "缺少运行标识，请重新提交", "code": "client_run_id_required"})
				return
			}
		}
		session, err := sessionService.GetByID(sessionID, userID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		intent := service.BuildRetryRunIntent(messageID)
		if edited {
			intent = service.BuildEditRetryRunIntent(messageID, editedContent)
		}
		if replayKnownSessionRun(c, runHub, quotaService, heartbeat, sessionID, userID, clientRunID, service.RunKindChat, intent) {
			return
		}
		if err := sessionService.ValidateRunnableModelForUser(session, userID); err != nil {
			writeRuntimeModelError(c, err)
			return
		}
		if einoAgent == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "compaction is unavailable"})
			return
		}
		var retryUser *model.Message
		var retryMessages []*model.Message
		if edited {
			retryUser, retryMessages, err = messageService.EditRetryAgentContext(c.Request.Context(), sessionID, userID, messageID, editedContent, clientRunID)
		} else {
			retryUser, retryMessages, err = messageService.RetryAgentContext(c.Request.Context(), sessionID, userID, messageID)
		}
		if err != nil {
			if errors.Is(err, service.ErrMessageAlreadyAnswered) {
				c.JSON(http.StatusConflict, gin.H{"error": "助手已开始输出，不能再修改这条消息", "code": "message_already_answered", "retryable": false})
				return
			}
			if errors.Is(err, service.ErrMessageUnchanged) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "内容没有变化，请直接使用重试", "code": "message_unchanged", "retryable": false})
				return
			}
			if errors.Is(err, service.ErrRetryTargetStale) {
				code := "retry_target_stale"
				message := "会话已有新消息，请刷新后重试最后一条"
				if edited {
					code = "edit_target_stale"
					message = "这条消息已不是会话末尾，请刷新后再编辑"
				}
				c.JSON(http.StatusConflict, gin.H{"error": message, "code": code, "retryable": false})
				return
			}
			if errors.Is(err, repository.ErrAttachmentUnavailable) {
				c.JSON(http.StatusConflict, gin.H{"error": "原附件已删除，无法完整重试", "code": "attachment_unavailable", "retryable": false})
				return
			}
			if errors.Is(err, service.ErrInvalidMessageInput) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "修改后的消息内容无效", "code": "message_input_invalid", "retryable": false})
				return
			}
			if errors.Is(err, service.ErrMessageTooLarge) {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "消息过长，请缩短后重试", "code": "message_too_large", "retryable": false})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": "重试上下文加载失败，请重试", "code": "retry_context_load_failed", "retryable": true})
			return
		}
		user, err := authService.GetProfileContext(c.Request.Context(), userID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "user not found"})
			return
		}
		enabledSkills, err := skillService.EnabledInstructionsForSessionContext(c.Request.Context(), user, session.Metadata)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "failed to load skills"})
			return
		}
		retryRequest := buildAgentRequestFromSession(session, user, retryMessages, titleService, enabledSkills, thinkingEffortFromMessage(retryUser))
		retryRequest.SchemaVersion = retryUser.SchemaVersion
		runtimeSnapshot, err := einoAgent.CaptureAcceptedRuntimeSnapshot(c.Request.Context(), retryRequest)
		if err != nil {
			writeRuntimeSnapshotError(c, err)
			return
		}
		needsCompaction, tokens, threshold, err := einoAgent.NeedsPreCompaction(c.Request.Context(), retryRequest)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check compaction"})
			return
		}
		if taskRunRepo != nil {
			if lastRun, _ := taskRunRepo.LatestForSession(c.Request.Context(), sessionID, userID, repository.ModelTaskCompression); lastRun != nil && lastRun.Status == repository.ModelTaskStatusFailed {
				needsCompaction = true
			}
		}
		if needsCompaction {
			c.JSON(http.StatusConflict, gin.H{
				"error":               "重新生成前需要先整理上下文",
				"message":             "重新生成前需要先整理上下文",
				"code":                "compaction_required",
				"retryable":           true,
				"preserve_message_id": retryUser.ID,
				"tokens":              tokens,
				"threshold":           threshold,
			})
			return
		}
		if err := quotaService.CheckBeforeRun(c.Request.Context(), userID, service.QuotaCheck{}); err != nil {
			writeQuotaError(c, err)
			return
		}
		firstOutputTimeout := effectiveFirstOutputTimeout(configuredFirstOutputTimeout, defaultChatFirstOutputTimeout)
		runSnapshot, handled := reserveSessionRun(c, runHub, heartbeat, firstOutputTimeout, sessionID, userID, clientRunID, service.RunKindChat, intent)
		if handled {
			return
		}
		runContext, ok := runHub.Context(runSnapshot.RunID)
		if !ok {
			payload := failRunWithPublicError(c, runHub, runSnapshot.RunID, "run_context_missing", "任务状态异常，请重试", errors.New("run context missing"))
			c.JSON(http.StatusInternalServerError, payload)
			return
		}
		var admission service.ChatRunAdmission
		durableSnapshot, err := runHub.PersistAdmission(runContext, runSnapshot.RunID, func(ctx context.Context) (repository.ChatRunRecord, error) {
			input := service.ChatRunQuotaInput{
				UserID: userID, AuthVersion: middleware.GetAuthVersion(c), SessionID: sessionID, RunID: runSnapshot.RunID, Kind: service.RunKindChat, Intent: intent, ReserveMessage: edited,
				RuntimeSnapshot: runtimeSnapshot,
				AcceptedAt:      runSnapshot.AcceptedAt,
				ExpiresAt:       runSnapshot.ExpiresAt,
			}
			if edited {
				admission, err = quotaService.AdmitEditedRetryChatRun(ctx, input, messageID, retryUser)
			} else {
				admission, err = quotaService.AdmitRetryChatRun(ctx, input, messageID)
			}
			return admission.Record, err
		})
		if err != nil {
			if transitionReservationFailure(c, runHub, runSnapshot.RunID, sessionID, userID, runContext, err) {
				return
			}
			if transitionCanceledRun(c, nil, runHub, runSnapshot.RunID, runContext) {
				return
			}
			if errors.Is(err, service.ErrRunTerminal) {
				c.JSON(http.StatusConflict, gin.H{"error": "任务已结束，请刷新会话", "code": "run_terminal"})
				return
			}
			if errors.Is(err, service.ErrRunIDConflict) {
				writeRunIDConflict(c)
				return
			}
			if failQuotaAdmission(c, runHub, runSnapshot.RunID, err) {
				return
			}
			if errors.Is(err, service.ErrRetryTargetStale) {
				code := "retry_target_stale"
				message := "会话已有新消息，请刷新后重试最后一条"
				if edited {
					code = "edit_target_stale"
					message = "这条消息已不是会话末尾，请刷新后再编辑"
				}
				payload := failRunWithPublicErrorRetryable(c, runHub, runSnapshot.RunID, code, message, false, err)
				c.JSON(http.StatusConflict, payload)
				return
			}
			if errors.Is(err, service.ErrMessageAlreadyAnswered) {
				payload := failRunWithPublicErrorRetryable(c, runHub, runSnapshot.RunID, "message_already_answered", "助手已开始输出，不能再修改这条消息", false, err)
				c.JSON(http.StatusConflict, payload)
				return
			}
			if errors.Is(err, service.ErrMessageUnchanged) {
				payload := failRunWithPublicErrorRetryable(c, runHub, runSnapshot.RunID, "message_unchanged", "内容没有变化，请直接使用重试", false, err)
				c.JSON(http.StatusBadRequest, payload)
				return
			}
			if errors.Is(err, service.ErrChatRunActive) {
				payload := failRunWithPublicErrorRetryable(c, runHub, runSnapshot.RunID, "run_in_progress", "当前回复仍在结束处理中，请稍后再保存修改", true, err)
				c.JSON(http.StatusConflict, payload)
				return
			}
			if errors.Is(err, repository.ErrAttachmentUnavailable) {
				payload := failRunWithPublicErrorRetryable(c, runHub, runSnapshot.RunID, "attachment_unavailable", "原附件已删除，无法完整重试", false, err)
				c.JSON(http.StatusConflict, payload)
				return
			}
			payload := failRunWithPublicError(c, runHub, runSnapshot.RunID, "retry_prepare_failed", "重试准备失败，请重试", err)
			c.JSON(http.StatusInternalServerError, payload)
			return
		}
		runSnapshot = durableSnapshot
		if runSnapshot.Status != service.RunStatusRunning {
			writer, writerErr := streaming.NewSSEWriter(c)
			if writerErr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming not supported"})
				return
			}
			replayExistingRun(c, writer, runHub, heartbeat, sessionID, userID, runSnapshot.RunID, 0)
			return
		}
		userMessage := admission.Message

		runAgentStream(c, messageService, sessionService, authService, skillService, einoAgent, titleService, runHub, taskRunRepo, heartbeat, firstOutputTimeout, sessionID, userID, userMessage, runSnapshot, modelusage.KindRetry)
	}
}

func writeQuotaError(c *gin.Context, err error) {
	var quotaErr *service.QuotaError
	if errors.As(err, &quotaErr) {
		payload := gin.H{
			"error": quotaErr.Message,
			"code":  quotaErr.Code,
			"limit": quotaErr.Limit,
			"used":  quotaErr.Used,
		}
		if !quotaErr.ResetAt.IsZero() {
			payload["reset_at"] = quotaErr.ResetAt
		}
		c.JSON(http.StatusTooManyRequests, payload)
		return
	}
	writeServerError(c, http.StatusInternalServerError, "quota_check_failed", "failed to check quota", err)
}

func failQuotaAdmission(c *gin.Context, runHub *service.RunHub, runID string, err error) bool {
	var quotaErr *service.QuotaError
	if !errors.As(err, &quotaErr) {
		return false
	}
	payload := gin.H{
		"error":     quotaErr.Message,
		"code":      quotaErr.Code,
		"limit":     quotaErr.Limit,
		"used":      quotaErr.Used,
		"retryable": true,
	}
	if !quotaErr.ResetAt.IsZero() {
		payload["reset_at"] = quotaErr.ResetAt
	}
	if requestID := c.GetString("request_id"); requestID != "" {
		payload["request_id"] = requestID
	}
	if _, transitionErr := transitionRun(runHub, runID, service.RunTerminal{
		Status: service.RunStatusFailed, PublicErrorCode: quotaErr.Code, PublicErrorMessage: quotaErr.Message,
		Event: streaming.EventError, Data: payload,
	}); transitionErr != nil {
		logger.Error("persist quota admission failure failed: request_id=%q run_id=%q code=%s err=%v", c.GetString("request_id"), runID, quotaErr.Code, transitionErr)
	}
	c.JSON(http.StatusTooManyRequests, payload)
	return true
}

func writeRuntimeModelError(c *gin.Context, err error) {
	var modelErr *service.RuntimeModelError
	if errors.As(err, &modelErr) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":    modelErr.Message,
			"code":     modelErr.Code,
			"provider": modelErr.Provider,
			"model_id": modelErr.ModelID,
		})
		return
	}
	c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
}

func runtimeSnapshotPublicMessage(err error) string {
	var runtimeErr *agent.RuntimeError
	if errors.As(err, &runtimeErr) && strings.TrimSpace(runtimeErr.Message) != "" {
		return runtimeErr.Message
	}
	return "当前运行配置暂不可用，请刷新后重试"
}

func writeRuntimeSnapshotError(c *gin.Context, err error) {
	c.JSON(http.StatusConflict, gin.H{
		"error":     runtimeSnapshotPublicMessage(err),
		"code":      "runtime_dependency_changed",
		"retryable": false,
	})
}

type messagePreflightResponse struct {
	Status          string                `json:"status"`
	NeedsCompaction bool                  `json:"needs_compaction"`
	Retryable       bool                  `json:"retryable,omitempty"`
	Message         string                `json:"message,omitempty"`
	Tokens          int                   `json:"tokens,omitempty"`
	Threshold       int                   `json:"threshold,omitempty"`
	LastTaskRun     *modelTaskRunResponse `json:"last_task_run,omitempty"`
	runtimeSnapshot json.RawMessage
}

type messagePreflightFailure struct {
	status    int
	message   string
	code      string
	retryable bool
}

func writeMessagePreflightFailure(c *gin.Context, failure *messagePreflightFailure) {
	payload := gin.H{"error": failure.message}
	if failure.code != "" {
		payload["code"] = failure.code
		payload["retryable"] = failure.retryable
	}
	c.JSON(failure.status, payload)
}

func evaluateMessagePreflight(ctx context.Context, messageService *service.MessageService, authService *service.AuthService, skillService *service.SkillService, einoAgent *agent.EinoAgent, titleService *service.TitleService, taskRunRepo *repository.ModelTaskRunRepository, session *model.Session, userID int64, req *service.SendMessageRequest) (messagePreflightResponse, *messagePreflightFailure) {
	if einoAgent == nil {
		return messagePreflightResponse{}, &messagePreflightFailure{status: http.StatusServiceUnavailable, message: "compaction is unavailable"}
	}
	user, err := authService.GetProfileContext(ctx, userID)
	if err != nil {
		return messagePreflightResponse{}, &messagePreflightFailure{status: http.StatusBadRequest, message: "user not found"}
	}
	enabledSkills, err := skillService.EnabledInstructionsForSessionContext(ctx, user, session.Metadata)
	if err != nil {
		return messagePreflightResponse{}, &messagePreflightFailure{status: http.StatusBadRequest, message: "failed to load skills"}
	}
	messages, err := messageService.ListForAgentWithDraftContext(ctx, session.ID, userID, req)
	if err != nil {
		return messagePreflightResponse{}, &messagePreflightFailure{status: http.StatusBadRequest, message: err.Error()}
	}
	agentReq := buildAgentRequestFromSession(session, user, messages, titleService, enabledSkills, req.ThinkingEffort)
	agentReq.SchemaVersion = req.SchemaVersion
	if agentReq.SchemaVersion == "" {
		agentReq.SchemaVersion = session.MessageFormat
	}
	runtimeSnapshot, err := einoAgent.CaptureAcceptedRuntimeSnapshot(ctx, agentReq)
	if err != nil {
		return messagePreflightResponse{}, &messagePreflightFailure{
			status: http.StatusConflict, message: runtimeSnapshotPublicMessage(err),
			code: "runtime_dependency_changed", retryable: false,
		}
	}
	needed, tokens, threshold, err := einoAgent.NeedsPreCompaction(ctx, agentReq)
	if err != nil {
		return messagePreflightResponse{}, &messagePreflightFailure{status: http.StatusInternalServerError, message: "failed to check compaction"}
	}
	var lastRun *repository.ModelTaskRun
	if taskRunRepo != nil {
		lastRun, _ = taskRunRepo.LatestForSession(ctx, session.ID, userID, repository.ModelTaskCompression)
	}
	if lastRun != nil && lastRun.Status == repository.ModelTaskStatusFailed {
		needed = true
	}
	if !needed {
		return messagePreflightResponse{Status: "ok", Tokens: tokens, Threshold: threshold, runtimeSnapshot: runtimeSnapshot}, nil
	}
	resp := messagePreflightResponse{
		Status:          "needs_compaction",
		NeedsCompaction: true,
		Retryable:       true,
		Message:         "发送前需要先整理上下文",
		Tokens:          tokens,
		Threshold:       threshold,
		runtimeSnapshot: runtimeSnapshot,
	}
	if lastRun != nil {
		resp.LastTaskRun = toModelTaskRunResponse(lastRun)
	}
	return resp, nil
}

func MessagePreflightHandler(messageService *service.MessageService, sessionService *service.SessionService, authService *service.AuthService, skillService *service.SkillService, einoAgent *agent.EinoAgent, titleService *service.TitleService, taskRunRepo *repository.ModelTaskRunRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		sessionID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session id"})
			return
		}
		var req service.SendMessageRequest
		if !bindMessageRequest(c, &req) {
			return
		}
		session, err := sessionService.GetByID(sessionID, userID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := sessionService.ValidateRunnableModelForUser(session, userID); err != nil {
			writeRuntimeModelError(c, err)
			return
		}
		resp, failure := evaluateMessagePreflight(c.Request.Context(), messageService, authService, skillService, einoAgent, titleService, taskRunRepo, session, userID, &req)
		if failure != nil {
			writeMessagePreflightFailure(c, failure)
			return
		}
		c.JSON(http.StatusOK, resp)
	}
}

// CompactSessionHandler 手动压缩当前会话（/compact 指令）。
//
// 复用 RunHub + SSE：压缩本质是一次 LLM 生成，因此与对话流共用断点续传通道。
// 客户端断线/刷新后可经 runs/active + runs/:id/resume 重连，拿到最终结果。
// RunHub 持有与 HTTP 请求解耦、受首包保护的 context，客户端断开后仍可继续并落库。
func CompactSessionHandler(messageService *service.MessageService, sessionService *service.SessionService, authService *service.AuthService, skillService *service.SkillService, einoAgent *agent.EinoAgent, titleService *service.TitleService, runHub *service.RunHub, quotaService *service.QuotaService, taskRunRepo *repository.ModelTaskRunRepository, heartbeat, configuredFirstOutputTimeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		sessionID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session id"})
			return
		}

		session, err := sessionService.GetByID(sessionID, userID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		clientRunID := strings.TrimSpace(c.Query("client_run_id"))
		source := normalizeCompactionSource(c.Query("source"))
		thinkingEffort := c.Query("thinking_effort")
		preserveMessageID := int64(0)
		if rawPreserveMessageID := strings.TrimSpace(c.Query("preserve_message_id")); rawPreserveMessageID != "" {
			preserveMessageID, err = strconv.ParseInt(rawPreserveMessageID, 10, 64)
			if err != nil || preserveMessageID <= 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid preserve message id"})
				return
			}
		}
		intent := service.BuildCompactionRunIntent(source, thinkingEffort, preserveMessageID)
		if replayKnownSessionRun(c, runHub, quotaService, heartbeat, sessionID, userID, clientRunID, service.RunKindCompaction, intent) {
			return
		}
		if einoAgent == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "compaction is unavailable"})
			return
		}
		if err := sessionService.ValidateRunnableModelForUser(session, userID); err != nil {
			writeRuntimeModelError(c, err)
			return
		}
		admissionUser, err := authService.GetProfileContext(c.Request.Context(), userID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "user not found"})
			return
		}
		admissionSkills, err := skillService.EnabledInstructionsForSessionContext(c.Request.Context(), admissionUser, session.Metadata)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "failed to load skills"})
			return
		}
		admissionRequest := buildAgentRequestFromSession(session, admissionUser, nil, titleService, admissionSkills, thinkingEffort)
		admissionRequest.SchemaVersion = session.MessageFormat
		runtimeSnapshot, err := einoAgent.CaptureAcceptedRuntimeSnapshot(c.Request.Context(), admissionRequest)
		if err != nil {
			writeRuntimeSnapshotError(c, err)
			return
		}

		firstOutputTimeout := effectiveFirstOutputTimeout(configuredFirstOutputTimeout, defaultCompactionFirstOutputTimeout)
		runSnapshot, handled := reserveSessionRun(c, runHub, heartbeat, firstOutputTimeout, sessionID, userID, clientRunID, service.RunKindCompaction, intent)
		if handled {
			return
		}
		runContext, ok := runHub.Context(runSnapshot.RunID)
		if !ok {
			payload := failRunWithPublicError(c, runHub, runSnapshot.RunID, "run_context_missing", "任务状态异常，请重试", errors.New("run context missing"))
			c.JSON(http.StatusInternalServerError, payload)
			return
		}
		durableSnapshot, err := runHub.PersistAdmission(runContext, runSnapshot.RunID, func(ctx context.Context) (repository.ChatRunRecord, error) {
			return quotaService.ReserveChatRun(ctx, service.ChatRunQuotaInput{
				UserID: userID, AuthVersion: middleware.GetAuthVersion(c), SessionID: sessionID, RunID: runSnapshot.RunID, Kind: service.RunKindCompaction, Intent: intent,
				RuntimeSnapshot: runtimeSnapshot,
				AcceptedAt:      runSnapshot.AcceptedAt,
				ExpiresAt:       runSnapshot.ExpiresAt,
			})
		})
		if err != nil {
			if transitionReservationFailure(c, runHub, runSnapshot.RunID, sessionID, userID, runContext, err) {
				return
			}
			if transitionCanceledRun(c, nil, runHub, runSnapshot.RunID, runContext) {
				return
			}
			if errors.Is(err, service.ErrRunTerminal) {
				c.JSON(http.StatusConflict, gin.H{"error": "任务已结束，请刷新会话", "code": "run_terminal"})
				return
			}
			if errors.Is(err, service.ErrRunIDConflict) {
				writeRunIDConflict(c)
				return
			}
			if failQuotaAdmission(c, runHub, runSnapshot.RunID, err) {
				return
			}
			payload := failRunWithPublicError(c, runHub, runSnapshot.RunID, "compaction_start_failed", "压缩任务启动失败，请重试", err)
			c.JSON(http.StatusInternalServerError, payload)
			return
		}
		runSnapshot = durableSnapshot
		if runSnapshot.Status != service.RunStatusRunning {
			writer, writerErr := streaming.NewSSEWriter(c)
			if writerErr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming not supported"})
				return
			}
			replayExistingRun(c, writer, runHub, heartbeat, sessionID, userID, runSnapshot.RunID, 0)
			return
		}
		executionContext, err := runHub.BeginExecution(runSnapshot.RunID)
		if err != nil {
			if errors.Is(err, service.ErrRunTerminal) || errors.Is(err, service.ErrRunExecutionOwned) {
				writer, writerErr := streaming.NewSSEWriter(c)
				if writerErr != nil {
					c.JSON(http.StatusConflict, gin.H{"error": "任务已开始，请通过 run_id 恢复", "code": "run_execution_owned", "run_id": runSnapshot.RunID})
					return
				}
				replayExistingRun(c, writer, runHub, heartbeat, sessionID, userID, runSnapshot.RunID, 0)
				return
			}
			payload := failRunWithPublicError(c, runHub, runSnapshot.RunID, "run_execution_unavailable", "压缩任务执行状态异常，请重试", err)
			c.JSON(http.StatusInternalServerError, payload)
			return
		}
		runContext = executionContext
		setupContext, setupCancel := newRunSetupContext(runContext, effectiveRunSetupTimeout(firstOutputTimeout))
		defer setupCancel()

		var writer *streaming.SSEWriter
		defer func() {
			event, err := transitionRun(runHub, runSnapshot.RunID, service.RunTerminal{
				Status: service.RunStatusFailed, PublicErrorCode: "run_incomplete", PublicErrorMessage: "压缩任务未能完成，请重试",
			})
			if err != nil {
				logger.Error("finalize incomplete compaction run failed: run_id=%q err=%v", runSnapshot.RunID, err)
				return
			}
			if event != nil && writer != nil {
				_ = writer.WriteEventWithoutRecord(event.Event, event.Data)
			}
		}()

		session, err = sessionService.GetByIDContext(setupContext, sessionID, userID)
		if err != nil {
			if transitionRunSetupInterruption(c, writer, runHub, runSnapshot.RunID, c.GetString("request_id"), runContext, setupContext) {
				return
			}
			payload := failRunWithPublicError(c, runHub, runSnapshot.RunID, "session_load_failed", "会话加载失败，请重试", err)
			c.JSON(http.StatusInternalServerError, payload)
			return
		}
		user, err := authService.GetProfileContext(setupContext, userID)
		if err != nil {
			if transitionRunSetupInterruption(c, writer, runHub, runSnapshot.RunID, c.GetString("request_id"), runContext, setupContext) {
				return
			}
			payload := failRunWithPublicError(c, runHub, runSnapshot.RunID, "user_profile_load_failed", "用户信息加载失败，请重试", err)
			c.JSON(http.StatusInternalServerError, payload)
			return
		}
		enabledSkills, err := skillService.EnabledInstructionsForSessionContext(setupContext, user, session.Metadata)
		if err != nil {
			if transitionRunSetupInterruption(c, writer, runHub, runSnapshot.RunID, c.GetString("request_id"), runContext, setupContext) {
				return
			}
			payload := failRunWithPublicError(c, runHub, runSnapshot.RunID, "skill_context_load_failed", "Skill 上下文加载失败，请重试", err)
			c.JSON(http.StatusInternalServerError, payload)
			return
		}
		var messages []*model.Message
		var memoryMessages []*model.Message
		if preserveMessageID > 0 {
			messages, err = messageService.ListForCompactionBeforeMessageContext(setupContext, sessionID, userID, preserveMessageID)
			if err == nil {
				memoryMessages, err = messageService.ListForAgentThroughMessageContext(setupContext, sessionID, userID, preserveMessageID)
			}
		} else {
			messages, err = messageService.ListForAgentContext(setupContext, sessionID, userID)
			memoryMessages = messages
		}
		if err != nil {
			if transitionRunSetupInterruption(c, writer, runHub, runSnapshot.RunID, c.GetString("request_id"), runContext, setupContext) {
				return
			}
			payload := failRunWithPublicError(c, runHub, runSnapshot.RunID, "conversation_history_load_failed", "会话历史加载失败，请重试", err)
			c.JSON(http.StatusInternalServerError, payload)
			return
		}

		writer, err = streaming.NewSSEWriter(c)
		if err != nil {
			if transitionRunSetupInterruption(c, nil, runHub, runSnapshot.RunID, c.GetString("request_id"), runContext, setupContext) {
				return
			}
			payload := failRunWithPublicError(c, runHub, runSnapshot.RunID, "stream_unavailable", "当前连接不支持流式响应", err)
			c.JSON(http.StatusInternalServerError, payload)
			return
		}
		writer.SetEventHook(func(event string, data interface{}) bool {
			return runHub.Record(runSnapshot.RunID, event, data)
		})
		_ = writer.WriteEvent(streaming.EventMessageStart, streaming.MessageStartEvent{RunID: runSnapshot.RunID})
		taskSource := repository.ModelTaskSourceManual
		kind := service.CompactionKindManual
		if source == "auto" {
			taskSource = repository.ModelTaskSourceAuto
			kind = service.CompactionKindAuto
		}
		_ = writer.WriteEvent(streaming.EventCompactionStart, gin.H{"run_id": runSnapshot.RunID, "source": source})

		if len(messages) == 0 {
			if finishRunSetup(c, writer, runHub, runSnapshot.RunID, runContext, setupCancel) {
				return
			}
			recordCompressionTaskRun(taskRunRepo, sessionID, userID, runSnapshot.RunID, taskSource, repository.ModelTaskStatusSkipped, "", nil, time.Now(), "", "")
			payload := gin.H{"reason": "no history to compact"}
			_ = writeRunTerminal(writer, runHub, runSnapshot.RunID, service.RunTerminal{
				Status: service.RunStatusCompleted, Event: streaming.EventCompactionSkip, Data: payload,
			})
			return
		}
		taskStarted := time.Now()

		stopHeartbeat := make(chan struct{})
		if heartbeat > 0 {
			go func() {
				ticker := time.NewTicker(heartbeat)
				defer ticker.Stop()
				for {
					select {
					case <-stopHeartbeat:
						return
					case <-c.Request.Context().Done():
						return
					case <-ticker.C:
						if err := writer.WritePing(); err != nil {
							return
						}
					}
				}
			}()
		}
		defer close(stopHeartbeat)

		agentReq := buildAgentRequestFromSession(session, user, messages, titleService, enabledSkills, thinkingEffort)
		agentReq.SchemaVersion = session.MessageFormat
		if err := einoAgent.ValidateAcceptedRuntimeSnapshot(setupContext, agentReq, runSnapshot.RuntimeSnapshot); err != nil {
			if transitionRunSetupInterruption(c, writer, runHub, runSnapshot.RunID, c.GetString("request_id"), runContext, setupContext) {
				return
			}
			recordCompressionTaskRun(taskRunRepo, sessionID, userID, runSnapshot.RunID, taskSource, repository.ModelTaskStatusFailed, err.Error(), err, taskStarted, "", "")
			payload := gin.H{
				"error":     runtimeSnapshotPublicMessage(err),
				"code":      "runtime_dependency_changed",
				"retryable": false,
			}
			_ = writeRunTerminal(writer, runHub, runSnapshot.RunID, service.RunTerminal{
				Status: service.RunStatusFailed, PublicErrorCode: "runtime_dependency_changed", PublicErrorMessage: payload["error"].(string),
				Event: streaming.EventError, Data: payload,
			})
			return
		}
		if finishRunSetup(c, writer, runHub, runSnapshot.RunID, runContext, setupCancel) {
			return
		}
		expectedAnswerSelectionRevision := session.AnswerSelectionRevision

		ctx := modelusage.WithMeta(runContext, modelusage.Meta{
			UserID:    userID,
			SessionID: sessionID,
			RunID:     runSnapshot.RunID,
			Kind:      modelusage.KindCompression,
		})
		checkpoint, err := runCompactionWithMemoryGate(ctx, einoAgent, session, userID, runSnapshot.RunID, source, agentReq, memoryMessages, &expectedAnswerSelectionRevision)
		cancelCause := effectiveRunCancelCause(runHub, runSnapshot.RunID, ctx)
		if cancelCause == "" && errors.Is(err, context.Canceled) {
			cancelCause = service.RunCancelUpstream
		}
		if cancelCause != "" {
			terminal := service.RunTerminal{Status: service.RunStatusCanceled, CancelCause: cancelCause}
			if cancelCause == service.RunCancelUserStop {
				terminal.Event = streaming.EventCompactionSkip
				terminal.Data = gin.H{"reason": "canceled"}
			}
			_ = writeRunTerminal(writer, runHub, runSnapshot.RunID, terminal)
			return
		}
		if err != nil {
			if errors.Is(err, repository.ErrAnswerSelectionRevisionConflict) {
				recordCompressionTaskRun(taskRunRepo, sessionID, userID, runSnapshot.RunID, taskSource, repository.ModelTaskStatusSkipped, "answer_selection_changed", nil, taskStarted, "", "")
				payload := gin.H{"reason": "answer_selection_changed"}
				_ = writeRunTerminal(writer, runHub, runSnapshot.RunID, service.RunTerminal{
					Status: service.RunStatusCompleted, Event: streaming.EventCompactionSkip, Data: payload,
				})
				return
			}
			recordCompressionTaskRun(taskRunRepo, sessionID, userID, runSnapshot.RunID, taskSource, repository.ModelTaskStatusFailed, err.Error(), err, taskStarted, "", "")
			payload := compactionErrorPayload()
			_ = writeRunTerminal(writer, runHub, runSnapshot.RunID, service.RunTerminal{
				Status: service.RunStatusFailed, PublicErrorCode: "compaction_failed", PublicErrorMessage: payload["error"].(string),
				Event: streaming.EventError, Data: payload,
			})
			return
		}
		if checkpoint == nil {
			recordCompressionTaskRun(taskRunRepo, sessionID, userID, runSnapshot.RunID, taskSource, repository.ModelTaskStatusSkipped, "nothing_to_compact", nil, taskStarted, "", "")
			payload := gin.H{"reason": "nothing to compact"}
			_ = writeRunTerminal(writer, runHub, runSnapshot.RunID, service.RunTerminal{
				Status: service.RunStatusCompleted, Event: streaming.EventCompactionSkip, Data: payload,
			})
			return
		}
		if preserveMessageID > 0 {
			checkpoint.CompressBefore = preserveMessageID
		}

		persistCtx, persistCancel := runFinalizationContext()
		terminalSnapshot, _, terminalEvent, err := runHub.TransitionWithCommit(persistCtx, runSnapshot.RunID, func(ctx context.Context, input repository.ChatRunTransitionInput) (repository.ChatRunRecord, bool, error) {
			return messageService.PersistCompressionCheckpointAndTransitionContext(ctx, sessionID, userID, runSnapshot.RunID, checkpoint.SummaryData, checkpoint.CompressBefore, kind, input, &expectedAnswerSelectionRevision)
		}, service.RunTerminal{
			Status: service.RunStatusCompleted, Event: streaming.EventCompactionComplete, Data: gin.H{"compacted": true},
		})
		persistCancel()
		if err != nil {
			if errors.Is(err, repository.ErrAnswerSelectionRevisionConflict) {
				recordCompressionTaskRun(taskRunRepo, sessionID, userID, runSnapshot.RunID, taskSource, repository.ModelTaskStatusSkipped, "answer_selection_changed", nil, taskStarted, checkpoint.Provider, checkpoint.ModelID)
				payload := gin.H{"reason": "answer_selection_changed"}
				_ = writeRunTerminal(writer, runHub, runSnapshot.RunID, service.RunTerminal{
					Status: service.RunStatusCompleted, Event: streaming.EventCompactionSkip, Data: payload,
				})
				return
			}
			if transitionCanceledRun(c, writer, runHub, runSnapshot.RunID, runContext) {
				return
			}
			recordCompressionTaskRun(taskRunRepo, sessionID, userID, runSnapshot.RunID, taskSource, repository.ModelTaskStatusFailed, err.Error(), err, taskStarted, checkpoint.Provider, checkpoint.ModelID)
			logger.Error("persist manual compaction failed: session=%d err=%v", sessionID, err)
			payload := compactionErrorPayload()
			_ = writeRunTerminal(writer, runHub, runSnapshot.RunID, service.RunTerminal{
				Status: service.RunStatusFailed, PublicErrorCode: "compaction_failed", PublicErrorMessage: payload["error"].(string),
				Event: streaming.EventError, Data: payload,
			})
			return
		}
		if terminalEvent != nil {
			_ = writer.WriteEventWithoutRecord(terminalEvent.Event, terminalEvent.Data)
		}
		if terminalSnapshot == nil || terminalSnapshot.Status != service.RunStatusCompleted {
			return
		}

		recordCompressionTaskRun(taskRunRepo, sessionID, userID, runSnapshot.RunID, taskSource, repository.ModelTaskStatusSuccess, "", nil, taskStarted, checkpoint.Provider, checkpoint.ModelID)
	}
}

func normalizeCompactionSource(source string) string {
	if strings.TrimSpace(source) == "auto" {
		return "auto"
	}
	return "manual"
}

func compactionErrorPayload() gin.H {
	message := "压缩失败，请联系管理员"
	return gin.H{
		"error":      message,
		"message":    message,
		"code":       "compaction_failed",
		"error_code": "compaction_failed",
		"retryable":  true,
	}
}

func runCompactionWithMemoryGate(ctx context.Context, einoAgent *agent.EinoAgent, session *model.Session, userID int64, runID, source string, agentReq *agent.ChatRequest, memoryMessages []*model.Message, expectedAnswerSelectionRevision *int64) (*agent.CompressionCheckpoint, error) {
	return runCompactionTasks(ctx, func(taskCtx context.Context) error {
		if len(memoryMessages) == 0 {
			memoryMessages = agentReq.Messages
		}
		contextText := service.RecentConversationTextForMemoryMessages(memoryMessages, 5)
		userText, err := latestMemoryRetryUserTextFromMessages(memoryMessages)
		if err != nil {
			return err
		}
		memReq := agent.MemoryMaintenanceRequest{
			SessionID:                       session.ID,
			UserID:                          userID,
			RunID:                           runID,
			UserText:                        userText,
			ContextText:                     contextText,
			MemoryEnabled:                   session.MemoryEnabled,
			ExpectedAnswerSelectionRevision: expectedAnswerSelectionRevision,
			Source:                          source,
			Force:                           true,
			IgnoreCooldown:                  true,
			ModelRequest:                    agentReq,
		}
		err = nil
		for i := 0; i < 2; i++ {
			err = einoAgent.MaintainSessionMemory(taskCtx, memReq)
			if err == nil || errors.Is(err, repository.ErrAnswerSelectionRevisionConflict) {
				break
			}
			if taskCtx.Err() != nil {
				return taskCtx.Err()
			}
		}
		return err
	}, func(taskCtx context.Context) (*agent.CompressionCheckpoint, error) {
		return einoAgent.CompactConversation(taskCtx, agentReq)
	})
}

func runCompactionTasks(ctx context.Context, maintainMemory func(context.Context) error, compact func(context.Context) (*agent.CompressionCheckpoint, error)) (*agent.CompressionCheckpoint, error) {
	type result struct {
		checkpoint *agent.CompressionCheckpoint
		err        error
	}
	taskCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan result, 2)
	go func() {
		// Memory maintenance is a sibling prerequisite, not the output owner
		// of the compaction run. Its model chunks must not disarm the outer
		// RunHub guard while the actual conversation compression is silent.
		results <- result{err: maintainMemory(modelstream.IsolateFirstOutputTimeout(taskCtx))}
	}()
	go func() {
		checkpoint, err := compact(taskCtx)
		results <- result{checkpoint: checkpoint, err: err}
	}()

	first := <-results
	if first.err != nil {
		cancel()
		<-results
		return nil, first.err
	}
	second := <-results
	if second.err != nil {
		cancel()
		return nil, second.err
	}
	if first.checkpoint != nil {
		return first.checkpoint, nil
	}
	return second.checkpoint, nil
}

func recordCompressionTaskRun(repo *repository.ModelTaskRunRepository, sessionID, userID int64, runID, source, status, metadataReason string, taskErr error, started time.Time, provider, modelID string) {
	if repo == nil {
		return
	}
	recordCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var retryAfter *time.Time
	errorType, errorMessage := "", ""
	if taskErr != nil {
		errorType = modelusage.ErrorType(taskErr)
		errorMessage = taskErr.Error()
		if source == repository.ModelTaskSourceAuto {
			t := time.Now().Add(30 * time.Minute)
			retryAfter = &t
		}
	}
	metadata := []byte(`{}`)
	if metadataReason != "" && taskErr == nil {
		metadata = []byte(`{"reason":` + strconv.Quote(metadataReason) + `}`)
	}
	if _, err := repo.Record(recordCtx, repository.RecordModelTaskRunInput{
		TaskKey:      repository.ModelTaskCompression,
		UserID:       userID,
		SessionID:    sessionID,
		RunID:        runID,
		Source:       source,
		Status:       status,
		Provider:     provider,
		ModelID:      modelID,
		TargetType:   "session",
		TargetID:     strconv.FormatInt(sessionID, 10),
		ErrorType:    errorType,
		ErrorMessage: errorMessage,
		RetryAfter:   retryAfter,
		Metadata:     metadata,
		StartedAt:    started,
		FinishedAt:   time.Now(),
	}); err != nil {
		logger.Error("record compression task run failed: session=%d err=%v", sessionID, err)
	}
}
