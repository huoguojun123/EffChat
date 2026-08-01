package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/EffChat/internal/agent"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/modelstream"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
	modelusage "github.com/huoguojun123/EffChat/internal/usage"
	"github.com/huoguojun123/EffChat/pkg/logger"
	"github.com/huoguojun123/EffChat/pkg/streaming"
)

func agentProducedSuccessfulToolWrite(messages []map[string]interface{}, toolName string, writeActions map[string]bool) bool {
	writeCalls := map[string]bool{}
	for _, msg := range messages {
		role, _ := msg["role"].(string)
		if role == "assistant" {
			calls, _ := msg["tool_calls"].([]interface{})
			for _, raw := range calls {
				call, _ := raw.(map[string]interface{})
				id, _ := call["id"].(string)
				fn, _ := call["function"].(map[string]interface{})
				name, _ := fn["name"].(string)
				args, _ := fn["arguments"].(string)
				if id == "" || name != toolName {
					continue
				}
				var input struct {
					Action string `json:"action"`
				}
				_ = json.Unmarshal([]byte(args), &input)
				if writeActions[strings.ToLower(strings.TrimSpace(input.Action))] {
					writeCalls[id] = true
				}
			}
			continue
		}
		if role != "tool" {
			continue
		}
		callID, _ := msg["tool_call_id"].(string)
		if !writeCalls[callID] {
			continue
		}
		content, _ := msg["content"].(string)
		var output struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal([]byte(content), &output); err == nil && strings.TrimSpace(output.Error) == "" {
			return true
		}
	}
	return false
}

func agentProducedSuccessfulMemoryWrite(messages []map[string]interface{}) bool {
	return agentProducedSuccessfulToolWrite(messages, "memory", map[string]bool{
		"add": true, "replace": true, "remove": true, "delete": true, "clear": true, "write": true,
	})
}

func messageContentPreview(data []byte) string {
	var msg map[string]interface{}
	if err := json.Unmarshal(data, &msg); err != nil {
		return ""
	}
	content, _ := msg["content"].(string)
	return strings.TrimSpace(content)
}

func assistantContentPreview(messages []map[string]interface{}) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if role, _ := messages[i]["role"].(string); role != "assistant" {
			continue
		}
		content, _ := messages[i]["content"].(string)
		if strings.TrimSpace(content) != "" {
			return strings.TrimSpace(content)
		}
	}
	return ""
}

func buildAgentRequestFromSession(session *model.Session, user *model.User, messages []*model.Message, titleService *service.TitleService, enabledSkills []service.SkillInstruction, thinkingEffort string) *agent.ChatRequest {
	modelInfo := modelbank.GetOrDefault(session.ModelID, session.Provider)
	req := &agent.ChatRequest{
		UserID:                  user.ID,
		SessionID:               session.ID,
		Messages:                messages,
		ModelID:                 session.ModelID,
		Provider:                session.Provider,
		SystemName:              titleService.SystemName(),
		MessageFormat:           session.MessageFormat,
		SessionTitle:            session.Title,
		SessionMetadata:         session.Metadata,
		UserName:                user.Username,
		UserRole:                user.Role,
		UserPreferences:         user.Preferences,
		EnabledSkills:           toAgentSkillInstructions(enabledSkills),
		ContextWindow:           modelInfo.Capabilities.ContextWindow,
		ModelMaxOutput:          modelInfo.Capabilities.MaxOutput,
		Vision:                  modelInfo.Capabilities.Vision,
		ToolUse:                 modelInfo.Capabilities.ToolUse,
		Reasoning:               modelInfo.Capabilities.Reasoning,
		ThinkingFormat:          modelInfo.ThinkingFormat,
		SearchImpl:              modelInfo.Capabilities.SearchImpl,
		ThinkingEffort:          thinkingEffort,
		SearchMode:              resolveSessionSearchMode(session.SearchMode),
		PreferModelNativeSearch: true,
		MemoryEnabled:           session.MemoryEnabled,
	}
	if user.Nickname != nil {
		req.UserNickname = *user.Nickname
	}
	req.UserDisplayName = strings.TrimSpace(req.UserNickname)
	if req.UserDisplayName == "" {
		req.UserDisplayName = strings.TrimSpace(req.UserName)
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

const (
	defaultChatFirstOutputTimeout       = 15 * time.Minute
	defaultCompactionFirstOutputTimeout = 5 * time.Minute
	maxRunSetupTimeout                  = 30 * time.Second
)

type agentRunExecution struct {
	requestID      string
	messageService *service.MessageService
	sessionService *service.SessionService
	authService    *service.AuthService
	skillService   *service.SkillService
	einoAgent      *agent.EinoAgent
	titleService   *service.TitleService
	runHub         *service.RunHub
	taskRunRepo    *repository.ModelTaskRunRepository
	runContext     context.Context
	setupContext   context.Context
	setupCancel    context.CancelFunc
	sessionID      int64
	userID         int64
	userMessage    *model.Message
	runSnapshot    *service.RunSnapshot
	usageKind      string
}

type runHubEventWriter struct {
	runHub *service.RunHub
	runID  string
}

func (w runHubEventWriter) WriteEvent(event string, data interface{}) error {
	if w.runHub == nil || !w.runHub.Record(w.runID, event, data) {
		return service.ErrRunTerminal
	}
	return nil
}

func effectiveFirstOutputTimeout(configured, fallback time.Duration) time.Duration {
	if configured > 0 {
		return configured
	}
	return fallback
}

// effectiveRunSetupTimeout gives durable setup its own bounded budget while
// guaranteeing that it expires before the enclosing first-output guard. The
// outer timeout is already effective (chat/compaction fallback applied) at
// production call sites. Non-positive values fall back to the setup cap; a
// one-nanosecond outer guard yields an immediate setup deadline because no
// smaller positive duration exists.
func effectiveRunSetupTimeout(firstOutputTimeout time.Duration) time.Duration {
	if firstOutputTimeout <= 0 {
		return maxRunSetupTimeout
	}
	half := firstOutputTimeout / 2
	if half < maxRunSetupTimeout {
		return half
	}
	return maxRunSetupTimeout
}

func newRunSetupContext(runContext context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if runContext == nil {
		runContext = context.Background()
	}
	return context.WithTimeout(runContext, timeout)
}

// transitionRunSetupInterruption keeps semantic RunHub cancellation ahead of
// the child setup deadline. A setup timeout is a retryable failed run, not a
// RunCancelCause: user stop, first-output timeout, drain, and invalidation must
// retain their existing canceled terminal facts and public error mapping.
func transitionRunSetupInterruption(c *gin.Context, writer *streaming.SSEWriter, runHub *service.RunHub, runID, requestID string, runContext, setupContext context.Context) bool {
	if transitionCanceledRun(c, writer, runHub, runID, runContext) {
		return true
	}
	if setupContext == nil || !errors.Is(context.Cause(setupContext), context.DeadlineExceeded) {
		return false
	}
	// Close the small race where the parent is canceled after the first check
	// but before the setup terminal is committed.
	if transitionCanceledRun(c, writer, runHub, runID, runContext) {
		return true
	}

	payload := runPublicErrorPayload(requestID, "run_setup_timeout", "任务准备超时，请重试", true)
	logger.Error("run setup timed out: request_id=%q run_id=%q", requestID, runID)
	if err := writeRunTerminal(writer, runHub, runID, service.RunTerminal{
		Status:             service.RunStatusFailed,
		PublicErrorCode:    "run_setup_timeout",
		PublicErrorMessage: "任务准备超时，请重试",
		Event:              streaming.EventError,
		Data:               payload,
	}); err != nil {
		logger.Error("persist run setup timeout failed: request_id=%q run_id=%q err=%v", requestID, runID, err)
		if writer == nil && c != nil && !c.Writer.Written() {
			c.JSON(http.StatusInternalServerError, runPublicErrorPayload(requestID, "run_terminal_failed", "任务状态保存失败，请重试", true))
		}
		return true
	}
	if writer == nil && c != nil && !c.Writer.Written() {
		c.JSON(http.StatusGatewayTimeout, payload)
	}
	return true
}

// finishRunSetup defines the success side of the setup race. Once the final
// setup operation returns successfully, canceling the child must not inspect
// its deadline again: only a semantic cancellation on the durable parent may
// still prevent the model phase from starting.
func finishRunSetup(c *gin.Context, writer *streaming.SSEWriter, runHub *service.RunHub, runID string, runContext context.Context, setupCancel context.CancelFunc) bool {
	if setupCancel != nil {
		setupCancel()
	}
	return transitionCanceledRun(c, writer, runHub, runID, runContext)
}

func runAgentStream(c *gin.Context, messageService *service.MessageService, sessionService *service.SessionService, authService *service.AuthService, skillService *service.SkillService, einoAgent *agent.EinoAgent, titleService *service.TitleService, runHub *service.RunHub, taskRunRepo *repository.ModelTaskRunRepository, heartbeat, firstOutputTimeout time.Duration, sessionID, userID int64, userMessage *model.Message, runSnapshot *service.RunSnapshot, usageKind string) {
	exec := agentRunExecution{
		requestID:      c.GetString("request_id"),
		messageService: messageService,
		sessionService: sessionService,
		authService:    authService,
		skillService:   skillService,
		einoAgent:      einoAgent,
		titleService:   titleService,
		runHub:         runHub,
		taskRunRepo:    taskRunRepo,
		sessionID:      sessionID,
		userID:         userID,
		userMessage:    userMessage,
		runSnapshot:    runSnapshot,
		usageKind:      usageKind,
	}
	runContext, err := runHub.BeginExecution(runSnapshot.RunID)
	if err != nil {
		writer, writerErr := streaming.NewSSEWriter(c)
		if writerErr == nil && (errors.Is(err, service.ErrRunTerminal) || errors.Is(err, service.ErrRunExecutionOwned)) {
			replayExistingRun(c, writer, runHub, heartbeat, sessionID, userID, runSnapshot.RunID, 0)
			return
		}
		payload := failRunWithPublicError(c, runHub, runSnapshot.RunID, "run_execution_unavailable", "任务执行状态异常，请重试", err)
		c.JSON(http.StatusInternalServerError, payload)
		return
	}
	exec.runContext = runContext
	exec.setupContext, exec.setupCancel = newRunSetupContext(runContext, effectiveRunSetupTimeout(firstOutputTimeout))
	// Launch immediately after ownership. The worker records message_start as
	// its first operation, before any setup or model output, so SSE may attach
	// later without becoming part of the execution ownership chain.
	go executeAgentRun(exec)

	writer, err := streaming.NewSSEWriter(c)
	if err != nil {
		// The durable worker owns completion now. A transport that cannot open
		// SSE must not cancel model execution; the client can resume by run_id.
		writeAcceptedRunStreamUnavailable(c, runSnapshot.RunID, err)
		return
	}
	events, ch, cleanup, _, err := runHub.EventsAfter(runSnapshot.RunID, sessionID, userID, 0)
	if err != nil {
		_ = writer.WriteError("任务仍在后台执行，请稍后恢复", map[string]interface{}{
			"code":      "stream_subscription_failed",
			"retryable": true,
			"run_id":    runSnapshot.RunID,
		})
		return
	}
	if cleanup != nil {
		defer cleanup()
	}

	forwardRunEvents(c, writer, runHub, heartbeat, sessionID, userID, runSnapshot.RunID, events, ch, 0)
}

func writeAcceptedRunStreamUnavailable(c *gin.Context, runID string, err error) {
	requestID := c.GetString("request_id")
	logger.Error("open accepted run stream failed: request_id=%q run_id=%q err=%v", requestID, runID, err)
	payload := runPublicErrorPayload(requestID, "stream_unavailable", "当前连接不支持流式响应", true)
	payload["run_id"] = runID
	c.JSON(http.StatusInternalServerError, payload)
}

func executeAgentRun(exec agentRunExecution) {
	messageService := exec.messageService
	sessionService := exec.sessionService
	authService := exec.authService
	skillService := exec.skillService
	einoAgent := exec.einoAgent
	titleService := exec.titleService
	runHub := exec.runHub
	taskRunRepo := exec.taskRunRepo
	sessionID := exec.sessionID
	userID := exec.userID
	userMessage := exec.userMessage
	runSnapshot := exec.runSnapshot
	usageKind := exec.usageKind
	requestID := exec.requestID

	defer func() {
		if recovered := recover(); recovered != nil {
			runID := ""
			if runSnapshot != nil {
				runID = runSnapshot.RunID
			}
			logger.Error("agent run panic recovered: request_id=%q run_id=%q panic_type=%T", requestID, runID, recovered)
			if runHub == nil || runID == "" {
				return
			}
			_, err := transitionRun(runHub, runID, service.RunTerminal{
				Status:              service.RunStatusFailed,
				PublicErrorCode:     "agent_run_panic",
				PublicErrorMessage:  "处理过程中发生异常，请重试",
				FinalizationFailure: true,
			})
			if err != nil {
				logger.Error("finalize panicked chat run failed: request_id=%q run_id=%q err=%v", requestID, runID, err)
			}
			return
		}
		if runHub == nil || runSnapshot == nil {
			return
		}
		_, err := transitionRun(runHub, runSnapshot.RunID, service.RunTerminal{
			Status: service.RunStatusFailed, PublicErrorCode: "run_incomplete", PublicErrorMessage: "任务未能完成，请重试",
		})
		if err != nil {
			logger.Error("finalize incomplete chat run failed: run_id=%q err=%v", runSnapshot.RunID, err)
		}
	}()
	if !runHub.Record(runSnapshot.RunID, streaming.EventMessageStart, streaming.MessageStartEvent{
		MessageID:     0,
		RunID:         runSnapshot.RunID,
		UserMessageID: userMessage.ID,
	}) {
		return
	}
	writer := runHubEventWriter{runHub: runHub, runID: runSnapshot.RunID}

	runContext := exec.runContext
	if runContext == nil {
		var ok bool
		runContext, ok = runHub.Context(runSnapshot.RunID)
		if !ok {
			failRunWithRequestID(runHub, runSnapshot.RunID, requestID, "run_context_missing", "任务状态异常，请重试", true, errors.New("run context missing"))
			return
		}
	}
	modelstream.ArmFirstOutputTimeout(runContext)
	if cause := service.RunCancelCauseFromContext(runContext); cause != "" {
		transitionCanceledRun(nil, nil, runHub, runSnapshot.RunID, runContext)
		return
	}
	setupContext := exec.setupContext
	setupCancel := exec.setupCancel
	if setupContext == nil || setupCancel == nil {
		setupContext, setupCancel = newRunSetupContext(runContext, effectiveRunSetupTimeout(defaultChatFirstOutputTimeout))
	}
	defer setupCancel()

	session, err := sessionService.GetByIDContext(setupContext, sessionID, userID)
	if err != nil {
		if transitionRunSetupInterruption(nil, nil, runHub, runSnapshot.RunID, requestID, runContext, setupContext) {
			return
		}
		failRunWithRequestID(runHub, runSnapshot.RunID, requestID, "session_load_failed", "会话加载失败，请重试", true, err)
		return
	}
	user, err := authService.GetProfileContext(setupContext, userID)
	if err != nil {
		if transitionRunSetupInterruption(nil, nil, runHub, runSnapshot.RunID, requestID, runContext, setupContext) {
			return
		}
		failRunWithRequestID(runHub, runSnapshot.RunID, requestID, "user_profile_load_failed", "用户信息加载失败，请重试", true, err)
		return
	}
	enabledSkills, err := skillService.EnabledInstructionsForSessionContext(setupContext, user, session.Metadata)
	if err != nil {
		if transitionRunSetupInterruption(nil, nil, runHub, runSnapshot.RunID, requestID, runContext, setupContext) {
			return
		}
		failRunWithRequestID(runHub, runSnapshot.RunID, requestID, "skill_context_load_failed", "Skill 上下文加载失败，请重试", true, err)
		return
	}

	// 重试只发送到原用户消息为止，不能把旧回答当作新答案的上下文。
	messages, err := loadRunConversationMessages(setupContext, messageService, sessionID, userID, runSnapshot, usageKind)
	if err != nil {
		if transitionRunSetupInterruption(nil, nil, runHub, runSnapshot.RunID, requestID, runContext, setupContext) {
			return
		}
		logger.Error("load conversation history failed: request_id=%q run_id=%q session=%d err=%v", requestID, runSnapshot.RunID, sessionID, err)
		payload := gin.H{"error": "会话历史加载失败，请重试", "code": "conversation_history_load_failed", "retryable": true}
		if requestID != "" {
			payload["request_id"] = requestID
		}
		_ = writeRunTerminal(nil, runHub, runSnapshot.RunID, service.RunTerminal{
			Status: service.RunStatusFailed, PublicErrorCode: "conversation_history_load_failed", PublicErrorMessage: "会话历史加载失败，请重试",
			Event: streaming.EventError, Data: payload,
		})
		return
	}

	agentReq := buildAgentRequestFromSession(session, user, messages, titleService, enabledSkills, thinkingEffortFromMessage(userMessage))
	agentReq.SchemaVersion = userMessage.SchemaVersion
	if err := einoAgent.ValidateAcceptedRuntimeSnapshot(setupContext, agentReq, runSnapshot.RuntimeSnapshot); err != nil {
		if transitionRunSetupInterruption(nil, nil, runHub, runSnapshot.RunID, requestID, runContext, setupContext) {
			return
		}
		payload := gin.H{
			"error":     runtimeSnapshotPublicMessage(err),
			"code":      "runtime_dependency_changed",
			"retryable": false,
		}
		_ = writeRunTerminal(nil, runHub, runSnapshot.RunID, service.RunTerminal{
			Status: service.RunStatusFailed, PublicErrorCode: "runtime_dependency_changed", PublicErrorMessage: payload["error"].(string),
			Event: streaming.EventError, Data: payload,
		})
		return
	}
	preparedChat, prepareErr := einoAgent.PrepareChat(setupContext, agentReq, writer)
	if prepareErr != nil {
		// Setup interruption classification is valid only while PrepareChat owns
		// the bounded child. Ordinary preparation errors continue through the
		// existing agent error persistence path below.
		if transitionRunSetupInterruption(nil, nil, runHub, runSnapshot.RunID, requestID, runContext, setupContext) {
			return
		}
		setupCancel()
	} else if finishRunSetup(nil, nil, runHub, runSnapshot.RunID, runContext, setupCancel) {
		return
	}
	// Cancel only the setup child. The durable parent remains armed for first
	// model output and owns RunPreparedChat plus all later persistence.
	ctx := modelusage.WithMeta(runContext, modelusage.Meta{
		UserID:    userID,
		SessionID: sessionID,
		MessageID: userMessage.ID,
		RunID:     runSnapshot.RunID,
		Kind:      usageKind,
	})
	var resp *agent.ChatResponse
	err = prepareErr
	if err == nil {
		resp, err = einoAgent.RunPreparedChat(ctx, preparedChat)
	}
	cancelCause := effectiveRunCancelCause(runHub, runSnapshot.RunID, ctx)
	if resp != nil && resp.Canceled && cancelCause == "" {
		cancelCause = service.RunCancelUpstream
	}
	if cancelCause == "" && errors.Is(err, context.Canceled) {
		cancelCause = service.RunCancelUpstream
	}
	if cancelCause != "" {
		terminal := service.RunTerminal{Status: service.RunStatusCanceled, CancelCause: cancelCause}
		if resp != nil {
			terminal.Usage = resp.Usage
		}
		if resp != nil && len(resp.Messages) > 0 && shouldPersistCanceledPartial(cancelCause) {
			if markIncompleteAgentMessages(resp.Messages) {
				complete := durableMessageCompleteEvent(resp, 0)
				complete.Incomplete = true
				terminal.Event = streaming.EventMessageComplete
				terminal.Data = complete
			}
			persistCtx, persistCancel := runFinalizationContext()
			_, _, _, persistErr := transitionRunWithMessages(persistCtx, messageService, runHub, sessionID, userID, runSnapshot.RunID, userMessage.SchemaVersion, resp.Messages, terminal)
			persistCancel()
			if persistErr != nil {
				payload := gin.H{"error": "已生成的部分回复保存失败，请重试", "code": "message_persist_failed", "retryable": true}
				_ = writeRunTerminal(nil, runHub, runSnapshot.RunID, service.RunTerminal{
					Status: service.RunStatusFailed, CancelCause: cancelCause, PublicErrorCode: "message_persist_failed", PublicErrorMessage: payload["error"].(string), FinalizationFailure: true,
					Event: streaming.EventError, Data: payload,
				})
				return
			}
			return
		}
		_ = writeRunTerminal(nil, runHub, runSnapshot.RunID, terminal)
		return
	}
	if err != nil {
		logger.Error("agent stream failed: request_id=%q run_id=%q session=%d err=%v", requestID, runSnapshot.RunID, sessionID, err)
		errorPayload := agentErrorPayload(err, requestID)
		errorContent, _ := errorPayload["error"].(string)
		messages := make([]map[string]interface{}, 0, 1)
		partial := false
		if resp != nil && len(resp.Messages) > 0 {
			messages = append(messages, resp.Messages...)
			partial = markIncompleteAgentMessages(messages)
		}
		if !partial {
			metadata := map[string]interface{}{
				"ephemeral_error": true,
				"error_code":      stringValue(errorPayload["code"]),
			}
			if diagnostic := stringValue(errorPayload["diagnostic"]); diagnostic != "" {
				metadata["error_diagnostic"] = diagnostic
			}
			messages = append(messages, map[string]interface{}{
				"role":     "assistant",
				"content":  errorContent,
				"metadata": metadata,
			})
		}
		terminal := service.RunTerminal{
			Status: service.RunStatusFailed, PublicErrorCode: stringValue(errorPayload["code"]), PublicErrorMessage: errorContent,
			Event: streaming.EventError, Data: errorPayload,
		}
		if partial {
			complete := durableMessageCompleteEvent(resp, 0)
			complete.Incomplete = true
			terminal.Event = streaming.EventMessageComplete
			terminal.Data = complete
		}
		persistCtx, persistCancel := runFinalizationContext()
		_, _, _, persistErr := runHub.TransitionWithCommit(persistCtx, runSnapshot.RunID, func(commitCtx context.Context, input repository.ChatRunTransitionInput) (repository.ChatRunRecord, bool, error) {
			commitMessages := messages
			if input.Status == service.RunStatusCanceled {
				commitMessages = nil
				if resp != nil {
					commitMessages = resp.Messages
				}
			}
			_, record, transitioned, commitErr := messageService.PersistAgentMessagesAndTransitionContext(commitCtx, sessionID, userID, commitMessages, userMessage.SchemaVersion, runSnapshot.RunID, input)
			return record, transitioned, commitErr
		}, terminal)
		persistCancel()
		if persistErr != nil {
			logger.Error("persist error assistant message failed: session=%d err=%v", sessionID, persistErr)
			cancelCause := effectiveRunCancelCause(runHub, runSnapshot.RunID, runContext)
			if !shouldPersistCanceledPartial(cancelCause) {
				_ = writeRunTerminal(nil, runHub, runSnapshot.RunID, service.RunTerminal{Status: service.RunStatusCanceled, CancelCause: cancelCause})
				return
			}
			payload := gin.H{"error": "回复错误状态保存失败，请重试", "code": "message_persist_failed", "retryable": true}
			_ = writeRunTerminal(nil, runHub, runSnapshot.RunID, service.RunTerminal{
				Status: service.RunStatusFailed, PublicErrorCode: "message_persist_failed", PublicErrorMessage: payload["error"].(string), FinalizationFailure: true,
				Event: streaming.EventError, Data: payload,
			})
			return
		}
		return
	}
	if resp.Incomplete {
		markIncompleteAgentMessages(resp.Messages)
	}

	// 持久化本轮全部消息（assistant / tool 结果 / 最终 assistant），
	// 保证工具调用链完整可回放。消息已流式发给前端，但落库失败若静默处理，
	// 前端 message_complete 后 syncSessionMessages 会拉到空结果并覆盖本地，
	// 导致"回复闪现后消失"。因此失败时显式通知前端：回复已生成但保存失败，
	// 用户据此可重试最后一条消息（PrepareRetry 已支持 user/assistant 两种）。
	persistCtx, persistCancel := runFinalizationContext()
	_, terminalSnapshot, _, err := transitionRunWithMessages(persistCtx, messageService, runHub, sessionID, userID, runSnapshot.RunID, userMessage.SchemaVersion, resp.Messages, service.RunTerminal{
		Status: service.RunStatusCompleted, Event: streaming.EventMessageComplete, Data: durableMessageCompleteEvent(resp, 0), Usage: resp.Usage,
	})
	persistCancel()
	if err != nil {
		logger.Error("persist agent messages failed: session=%d err=%v", sessionID, err)
		cancelCause := effectiveRunCancelCause(runHub, runSnapshot.RunID, runContext)
		if !shouldPersistCanceledPartial(cancelCause) {
			_ = writeRunTerminal(nil, runHub, runSnapshot.RunID, service.RunTerminal{
				Status: service.RunStatusCanceled, CancelCause: cancelCause,
			})
			return
		}
		payload := gin.H{"error": "回复已生成但保存失败，请重试最后一条消息", "code": "message_persist_failed", "retryable": true}
		if requestID != "" {
			payload["request_id"] = requestID
		}
		_ = writeRunTerminal(nil, runHub, runSnapshot.RunID, service.RunTerminal{
			Status: service.RunStatusFailed, PublicErrorCode: "message_persist_failed", PublicErrorMessage: payload["error"].(string), FinalizationFailure: true,
			Event: streaming.EventError, Data: payload,
		})
		return
	}
	if terminalSnapshot == nil || terminalSnapshot.Status != service.RunStatusCompleted {
		return
	}
	userText := messageContentPreview(userMessage.MessageData)
	memoryWritten := agentProducedSuccessfulMemoryWrite(resp.Messages)
	explicitMemoryRequest := agent.IsExplicitMemoryMaintenanceRequest(userText)
	memorySession, err := loadPostRunMemorySession(sessionService, sessionID, userID)
	if err != nil {
		logger.Error("reload session for memory maintenance failed: session=%d err=%v", sessionID, err)
	} else if !resp.Canceled && !resp.Incomplete {
		maintenanceCtx, maintenanceCancel := runFinalizationContext()
		shouldMaintain, triggerErr := shouldRunBackgroundMemoryMaintenance(maintenanceCtx, messageService, taskRunRepo, sessionID, userID, userText, memoryWritten, explicitMemoryRequest)
		maintenanceCancel()
		if triggerErr != nil {
			logger.Error("evaluate memory maintenance trigger failed: session=%d err=%v", sessionID, triggerErr)
		} else if shouldMaintain {
			contextText, err := messageService.RecentConversationTextForMemory(sessionID, userID, 5)
			if err != nil {
				logger.Error("load memory maintenance context failed: session=%d err=%v", sessionID, err)
			} else {
				expectedAnswerSelectionRevision := memorySession.AnswerSelectionRevision
				einoAgent.MaintainSessionMemoryAsync(agent.MemoryMaintenanceRequest{
					SessionID:                       sessionID,
					UserID:                          userID,
					RunID:                           runSnapshot.RunID,
					UserText:                        userText,
					AssistantText:                   assistantContentPreview(resp.Messages),
					ContextText:                     contextText,
					MemoryEnabled:                   memorySession.MemoryEnabled,
					ExpectedAnswerSelectionRevision: &expectedAnswerSelectionRevision,
					Source:                          "auto",
					Force:                           explicitMemoryRequest,
					ModelRequest:                    agentReq,
				})
			}
		}
	}

	// 检查是否需要生成标题（异步，不阻塞响应）
	titleService.GenerateTitleAsync(sessionID, userID)
}

func shouldPersistCanceledPartial(cause service.RunCancelCause) bool {
	return cause != service.RunCancelSessionDeleted
}

func stringValue(value interface{}) string {
	text, _ := value.(string)
	return text
}

func markIncompleteAgentMessages(messages []map[string]interface{}) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		if stringValue(messages[i]["role"]) != "assistant" {
			continue
		}
		metadata, _ := messages[i]["metadata"].(map[string]interface{})
		if metadata == nil {
			metadata = map[string]interface{}{}
		}
		metadata["incomplete"] = true
		messages[i]["metadata"] = metadata
		return true
	}
	return false
}

func loadRunConversationMessages(ctx context.Context, messageService *service.MessageService, sessionID, userID int64, runSnapshot *service.RunSnapshot, usageKind string) ([]*model.Message, error) {
	if usageKind != modelusage.KindRetry {
		return messageService.ListForAgentContext(ctx, sessionID, userID)
	}
	if runSnapshot == nil || runSnapshot.RetryTargetMessageID <= 0 {
		return nil, service.ErrRetryTargetStale
	}
	return messageService.ListForRetryAgentContext(ctx, sessionID, userID, runSnapshot.RetryTargetMessageID)
}

func loadPostRunMemorySession(sessionService *service.SessionService, sessionID, userID int64) (*model.Session, error) {
	ctx, cancel := runFinalizationContext()
	defer cancel()
	return sessionService.GetByIDContext(ctx, sessionID, userID)
}

func durableMessageCompleteEvent(resp *agent.ChatResponse, messageID int64) streaming.MessageCompleteEvent {
	event := streaming.MessageCompleteEvent{MessageID: messageID}
	if resp == nil {
		return event
	}
	event.FinishReason = resp.FinishReason
	event.Incomplete = resp.Incomplete
	event.DurationMs = resp.DurationMs
	event.TokensPerSecond = resp.TokensPerSecond
	if resp.Usage != nil {
		event.Usage = &streaming.UsageEvent{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
			CachedTokens:     resp.Usage.CachedTokens,
			ReasoningTokens:  resp.Usage.ReasoningTokens,
		}
	}
	return event
}

func lastAssistantMessageID(messages []*model.Message) int64 {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i] != nil && messages[i].Role == "assistant" {
			return messages[i].ID
		}
	}
	return 0
}

func shouldRunBackgroundMemoryMaintenance(ctx context.Context, messageService *service.MessageService, taskRunRepo *repository.ModelTaskRunRepository, sessionID, userID int64, userText string, memoryWritten, explicitMemoryRequest bool) (bool, error) {
	if memoryWritten {
		return false, nil
	}
	if explicitMemoryRequest {
		return true, nil
	}
	if !agent.ShouldRunMemoryMaintenance(userText) {
		return false, nil
	}
	var since time.Time
	if taskRunRepo != nil {
		last, err := taskRunRepo.LatestEffectiveAttemptForSession(ctx, sessionID, userID, repository.ModelTaskMemoryMaintenance)
		if err != nil {
			return false, err
		}
		if last != nil {
			since = last.FinishedAt
		}
	}
	count, err := messageService.CountUserMessagesSince(sessionID, userID, since)
	if err != nil {
		return false, err
	}
	return count >= 5, nil
}

func agentErrorPayload(err error, requestID string) gin.H {
	payload := gin.H{
		"error":     "模型请求失败，请稍后重试",
		"code":      "agent_run_failed",
		"category":  string(agent.RuntimeErrorServerUpdate),
		"retryable": true,
	}
	var runtimeErr *agent.RuntimeError
	if errors.As(err, &runtimeErr) {
		payload["error"] = runtimeErr.Message
		payload["code"] = runtimeErr.Code
		payload["category"] = string(runtimeErr.Category)
		payload["retryable"] = runtimeErr.Retryable
		if runtimeErr.Diagnostic != "" {
			payload["diagnostic"] = runtimeErr.Diagnostic
		}
		if runtimeErr.FinishReason != "" {
			payload["finish_reason"] = runtimeErr.FinishReason
		}
		if runtimeErr.Usage != nil {
			payload["usage"] = runtimeErr.Usage
		}
	}
	if requestID != "" {
		payload["request_id"] = requestID
	}
	return payload
}

func replayExistingRun(c *gin.Context, writer *streaming.SSEWriter, runHub *service.RunHub, heartbeat time.Duration, sessionID, userID int64, runID string, cursor int64) {
	events, ch, cleanup, _, err := runHub.EventsAfter(runID, sessionID, userID, cursor)
	if err != nil {
		_ = writer.WriteError("无法恢复该任务", map[string]interface{}{"code": "run_not_found"})
		return
	}
	if cleanup != nil {
		defer cleanup()
	}

	forwardRunEvents(c, writer, runHub, heartbeat, sessionID, userID, runID, events, ch, cursor)
}

// resolveSessionSearchMode 将会话存储的搜索模式字符串映射为 modelbank.SearchMode，
// 空值或非法值回退到 auto（自适应），与会话创建时的默认保持一致。
func resolveSessionSearchMode(mode string) modelbank.SearchMode {
	switch modelbank.SearchMode(mode) {
	case modelbank.SearchModeOff:
		return modelbank.SearchModeOff
	case modelbank.SearchModeOn:
		return modelbank.SearchModeOn
	default:
		return modelbank.SearchModeAuto
	}
}

func thinkingEffortFromMessage(message *model.Message) string {
	if message == nil || len(message.MessageData) == 0 {
		return ""
	}
	var data map[string]interface{}
	if err := json.Unmarshal(message.MessageData, &data); err != nil {
		return ""
	}
	meta, ok := data["metadata"].(map[string]interface{})
	if !ok {
		return ""
	}
	effort, _ := meta["thinking_effort"].(string)
	if !modelbank.IsValidThinkingEffort(effort) {
		return ""
	}
	return modelbank.NormalizeThinkingEffort(effort)
}

func toAgentSkillInstructions(skills []service.SkillInstruction) []agent.SkillInstruction {
	if len(skills) == 0 {
		return nil
	}
	out := make([]agent.SkillInstruction, 0, len(skills))
	for _, skill := range skills {
		out = append(out, agent.SkillInstruction{
			ID:          skill.ID,
			Name:        skill.Name,
			Description: skill.Description,
			Files:       skill.Files,
		})
	}
	return out
}
