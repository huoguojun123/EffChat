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
	"github.com/huoguojun123/EffChat/internal/middleware"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
	"github.com/huoguojun123/EffChat/pkg/streaming"
)

const (
	eventMemoryMaintenanceStart    = "memory_maintenance_start"
	eventMemoryMaintenanceComplete = "memory_maintenance_complete"
)

// MemoryMaintenanceStreamHandler gives manual memory compact/retry the same
// durable ownership as chat and conversation compaction. The HTTP connection
// only observes the run; RunHub owns cancellation and the model lifecycle, so a
// browser timeout, refresh or closed dialog cannot abort work that has started.
func MemoryMaintenanceStreamHandler(
	sessionService *service.SessionService,
	authService *service.AuthService,
	messageRepo *repository.MessageRepository,
	memoryRepo *repository.SessionMemoryRepository,
	einoAgent *agent.EinoAgent,
	runHub *service.RunHub,
	quotaService *service.QuotaService,
	heartbeat, configuredFirstOutputTimeout time.Duration,
	operation string,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		sessionID, ok := parseSessionID(c)
		if !ok {
			return
		}
		session, err := sessionService.GetByID(sessionID, userID)
		if err != nil {
			writeSessionLookupError(c, "load", err)
			return
		}
		if operation == service.RunOperationMemoryRetry && !session.MemoryEnabled {
			writePublicError(c, http.StatusBadRequest, "memory_disabled", "session memory is disabled", false)
			return
		}
		if einoAgent == nil {
			writePublicError(c, http.StatusServiceUnavailable, "memory_maintenance_unavailable", "memory maintenance is unavailable", true)
			return
		}

		clientRunID := strings.TrimSpace(c.Query("client_run_id"))
		intent := service.BuildMemoryMaintenanceRunIntent(operation)
		if replayKnownSessionRun(c, runHub, quotaService, heartbeat, sessionID, userID, clientRunID, service.RunKindMemoryMaintenance, intent) {
			return
		}
		firstOutputTimeout := effectiveFirstOutputTimeout(configuredFirstOutputTimeout, agent.MemoryMaintenanceFirstOutputTimeout)
		runSnapshot, handled := reserveSessionRun(c, runHub, heartbeat, firstOutputTimeout, sessionID, userID, clientRunID, service.RunKindMemoryMaintenance, intent)
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
				UserID:      userID,
				AuthVersion: middleware.GetAuthVersion(c),
				SessionID:   sessionID,
				RunID:       runSnapshot.RunID,
				Kind:        service.RunKindMemoryMaintenance,
				Intent:      intent,
				AcceptedAt:  runSnapshot.AcceptedAt,
				ExpiresAt:   runSnapshot.ExpiresAt,
			})
		})
		if err != nil {
			if transitionReservationFailure(c, runHub, runSnapshot.RunID, sessionID, userID, runContext, err) || failQuotaAdmission(c, runHub, runSnapshot.RunID, err) {
				return
			}
			payload := failRunWithPublicError(c, runHub, runSnapshot.RunID, "memory_maintenance_start_failed", "记忆任务启动失败，请重试", err)
			c.JSON(http.StatusInternalServerError, payload)
			return
		}
		runSnapshot = durableSnapshot
		if runSnapshot.Status != service.RunStatusRunning {
			writer, writerErr := streaming.NewSSEWriter(c)
			if writerErr != nil {
				writeServerError(c, http.StatusInternalServerError, "stream_unavailable", "streaming not supported", writerErr)
				return
			}
			replayExistingRun(c, writer, runHub, heartbeat, sessionID, userID, runSnapshot.RunID, 0)
			return
		}
		executionContext, err := runHub.BeginExecution(runSnapshot.RunID)
		if err != nil {
			writer, writerErr := streaming.NewSSEWriter(c)
			if writerErr == nil && (errors.Is(err, service.ErrRunTerminal) || errors.Is(err, service.ErrRunExecutionOwned)) {
				replayExistingRun(c, writer, runHub, heartbeat, sessionID, userID, runSnapshot.RunID, 0)
				return
			}
			payload := failRunWithPublicError(c, runHub, runSnapshot.RunID, "run_execution_unavailable", "记忆任务执行状态异常，请重试", err)
			c.JSON(http.StatusInternalServerError, payload)
			return
		}
		runContext = executionContext

		writer, err := streaming.NewSSEWriter(c)
		if err != nil {
			payload := failRunWithPublicError(c, runHub, runSnapshot.RunID, "stream_unavailable", "当前连接不支持流式响应", err)
			c.JSON(http.StatusInternalServerError, payload)
			return
		}
		writer.SetEventHook(func(event string, data interface{}) bool {
			return runHub.Record(runSnapshot.RunID, event, data)
		})
		_ = writer.WriteEvent(eventMemoryMaintenanceStart, gin.H{"run_id": runSnapshot.RunID, "operation": operation})

		// Setup uses the bounded pre-output context. Once the model starts, the
		// detached RunHub context is the only owner and modelstream disarms the
		// first-output guard on the first meaningful content/reasoning/tool chunk.
		setupContext, setupCancel := newRunSetupContext(runContext, effectiveRunSetupTimeout(firstOutputTimeout))
		session, err = sessionService.GetByIDContext(setupContext, sessionID, userID)
		var user *model.User
		if err == nil {
			user, err = authService.GetProfileContext(setupContext, userID)
		}
		if err != nil {
			setupCancel()
			transitionMemoryMaintenanceFailure(writer, runHub, memoryRepo, runSnapshot.RunID, memoryTaskFailure(sessionID, userID, runSnapshot.RunID, operation, err), err)
			return
		}

		request := agent.MemoryMaintenanceRequest{
			SessionID:     sessionID,
			UserID:        userID,
			RunID:         runSnapshot.RunID,
			MemoryEnabled: session.MemoryEnabled,
			Source:        "compact",
			ModelRequest:  memoryModelRequest(session, userID, user.Preferences),
		}
		if operation == service.RunOperationMemoryRetry {
			request.Source = "manual"
			request.Force = true
			request.IgnoreCooldown = true
			request.UserText, err = latestMemoryRetryUserText(setupContext, messageRepo, sessionID)
			if err != nil {
				setupCancel()
				transitionMemoryMaintenanceFailure(writer, runHub, memoryRepo, runSnapshot.RunID, memoryTaskFailure(sessionID, userID, runSnapshot.RunID, operation, err), err)
				return
			}
		}
		if finishRunSetup(c, writer, runHub, runSnapshot.RunID, runContext, setupCancel) {
			return
		}
		expectedRevision := session.AnswerSelectionRevision
		request.ExpectedAnswerSelectionRevision = &expectedRevision
		prepared := agent.PreparedMemoryMaintenanceResult{}
		var taskRun repository.RecordModelTaskRunInput
		request.PreparedResult = &prepared
		request.TaskRunSink = func(input repository.RecordModelTaskRunInput) { taskRun = input }

		if operation == service.RunOperationMemoryRetry {
			err = einoAgent.RetrySessionMemory(runContext, request)
		} else {
			err = einoAgent.CompactSessionMemory(runContext, request)
		}
		if cause := effectiveRunCancelCause(runHub, runSnapshot.RunID, runContext); cause != "" {
			if taskRun.TaskKey == "" {
				taskRun = memoryTaskFailure(sessionID, userID, runSnapshot.RunID, operation, context.Cause(runContext))
			}
			if cause == service.RunCancelUserStop {
				taskRun.Status = repository.ModelTaskStatusSkipped
				taskRun.ErrorType = ""
				taskRun.ErrorMessage = ""
				taskRun.Metadata, _ = json.Marshal(gin.H{"reason": "canceled", "cancel_cause": cause})
			}
			transitionMemoryMaintenanceCanceled(writer, runHub, memoryRepo, runSnapshot.RunID, taskRun, cause)
			return
		}
		if taskRun.TaskKey == "" {
			taskRun = memoryTaskFailure(sessionID, userID, runSnapshot.RunID, operation, err)
		}
		if err != nil {
			transitionMemoryMaintenanceFailure(writer, runHub, memoryRepo, runSnapshot.RunID, taskRun, err)
			return
		}

		updated := prepared.SaveInput != nil
		terminal := service.RunTerminal{
			Status: service.RunStatusCompleted,
			Event:  eventMemoryMaintenanceComplete,
			Data:   gin.H{"updated": updated, "operation": operation},
		}
		persistCtx, persistCancel := runFinalizationContext()
		_, _, terminalEvent, err := runHub.TransitionWithCommit(persistCtx, runSnapshot.RunID, func(ctx context.Context, transition repository.ChatRunTransitionInput) (repository.ChatRunRecord, bool, error) {
			return memoryRepo.CommitMaintenanceRun(ctx, repository.MemoryMaintenanceRunCommitInput{Run: transition, Memory: prepared.SaveInput, TaskRun: taskRun})
		}, terminal)
		persistCancel()
		if err != nil && (errors.Is(err, repository.ErrAnswerSelectionRevisionConflict) || errors.Is(err, repository.ErrSessionMemoryConflict)) {
			taskRun.Status = repository.ModelTaskStatusSkipped
			taskRun.ErrorType = ""
			taskRun.ErrorMessage = ""
			reason := "answer_selection_changed"
			if errors.Is(err, repository.ErrSessionMemoryConflict) {
				reason = "memory_changed"
			}
			taskRun.Metadata, _ = json.Marshal(gin.H{"reason": reason})
			terminal.Data = gin.H{"updated": false, "operation": operation, "reason": reason}
			persistCtx, persistCancel = runFinalizationContext()
			_, _, terminalEvent, err = runHub.TransitionWithCommit(persistCtx, runSnapshot.RunID, func(ctx context.Context, transition repository.ChatRunTransitionInput) (repository.ChatRunRecord, bool, error) {
				return memoryRepo.CommitMaintenanceRun(ctx, repository.MemoryMaintenanceRunCommitInput{Run: transition, TaskRun: taskRun})
			}, terminal)
			persistCancel()
		}
		if err != nil {
			transitionMemoryMaintenanceFailure(writer, runHub, memoryRepo, runSnapshot.RunID, memoryTaskFailure(sessionID, userID, runSnapshot.RunID, operation, err), err)
			return
		}
		if terminalEvent != nil {
			_ = writer.WriteEventWithoutRecord(terminalEvent.Event, terminalEvent.Data)
		}
	}
}

func memoryTaskFailure(sessionID, userID int64, runID, operation string, err error) repository.RecordModelTaskRunInput {
	message := "memory maintenance failed"
	if err != nil {
		message = err.Error()
	}
	source := repository.ModelTaskSourceManual
	return repository.RecordModelTaskRunInput{
		TaskKey: repository.ModelTaskMemoryMaintenance, UserID: userID, SessionID: sessionID, RunID: runID,
		Source: source, Status: repository.ModelTaskStatusFailed, TargetType: "memory", ErrorMessage: message,
		StartedAt: time.Now(), FinishedAt: time.Now(), Metadata: json.RawMessage(`{}`),
	}
}

func transitionMemoryMaintenanceFailure(writer *streaming.SSEWriter, runHub *service.RunHub, memoryRepo *repository.SessionMemoryRepository, runID string, taskRun repository.RecordModelTaskRunInput, err error) {
	payload := gin.H{"error": "记忆维护失败，请重试", "code": "memory_maintenance_failed", "retryable": true}
	if public, ok := memoryMaintenanceFailurePayload(err); ok {
		for key, value := range public {
			payload[key] = value
		}
	}
	persistCtx, persistCancel := runFinalizationContext()
	_, _, event, transitionErr := runHub.TransitionWithCommit(persistCtx, runID, func(ctx context.Context, transition repository.ChatRunTransitionInput) (repository.ChatRunRecord, bool, error) {
		return memoryRepo.CommitMaintenanceRun(ctx, repository.MemoryMaintenanceRunCommitInput{Run: transition, TaskRun: taskRun})
	}, service.RunTerminal{
		Status: service.RunStatusFailed, PublicErrorCode: payload["code"].(string), PublicErrorMessage: payload["error"].(string),
		Event: streaming.EventError, Data: payload,
	})
	persistCancel()
	if transitionErr == nil && event != nil {
		_ = writer.WriteEventWithoutRecord(event.Event, event.Data)
	}
}

func transitionMemoryMaintenanceCanceled(writer *streaming.SSEWriter, runHub *service.RunHub, memoryRepo *repository.SessionMemoryRepository, runID string, taskRun repository.RecordModelTaskRunInput, cause service.RunCancelCause) {
	persistCtx, persistCancel := runFinalizationContext()
	_, _, event, err := runHub.TransitionWithCommit(persistCtx, runID, func(ctx context.Context, transition repository.ChatRunTransitionInput) (repository.ChatRunRecord, bool, error) {
		return memoryRepo.CommitMaintenanceRun(ctx, repository.MemoryMaintenanceRunCommitInput{Run: transition, TaskRun: taskRun})
	}, service.RunTerminal{Status: service.RunStatusCanceled, CancelCause: cause})
	persistCancel()
	if err == nil && event != nil {
		_ = writer.WriteEventWithoutRecord(event.Event, event.Data)
	}
}
