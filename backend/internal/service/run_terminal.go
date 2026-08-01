package service

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/huoguojun123/EffChat/internal/modelstream"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/pkg/logger"
	"github.com/huoguojun123/EffChat/pkg/streaming"
)

const (
	runFinalizationTimeout         = 5 * time.Second
	terminalRecoveryInitialBackoff = 25 * time.Millisecond
	terminalRecoveryMaxBackoff     = time.Second
)

type RunCancelCause string

const (
	RunCancelUserStop           RunCancelCause = "user_stop"
	RunCancelFirstOutputTimeout RunCancelCause = "first_output_timeout"
	RunCancelServerDrain        RunCancelCause = "server_draining"
	RunCancelAccountChanged     RunCancelCause = "account_changed"
	RunCancelSessionDeleted     RunCancelCause = "session_deleted"
	RunCancelUpstream           RunCancelCause = "upstream_canceled"
)

type runCancellationError struct {
	Cause RunCancelCause
}

func (e runCancellationError) Error() string {
	return string(e.Cause)
}

// Unwrap keeps the durable RunHub cause while preserving the standard error
// family expected by provider adapters, usage accounting, and Tool runtimes.
// A first-output timeout is a real timeout; every other RunHub cause is a
// semantic cancellation rather than a provider/model failure.
func (e runCancellationError) Unwrap() error {
	if e.Cause == RunCancelFirstOutputTimeout {
		return modelstream.ErrFirstOutputTimeout
	}
	return context.Canceled
}

type chatRunStore interface {
	BindChatRunUserMessage(ctx context.Context, runID string, userMessageID int64) (bool, error)
	TransitionChatRun(ctx context.Context, input repository.ChatRunTransitionInput) (repository.ChatRunRecord, bool, error)
}

type RunTerminalCommit func(context.Context, repository.ChatRunTransitionInput) (repository.ChatRunRecord, bool, error)
type RunAdmissionCommit func(context.Context) (repository.ChatRunRecord, error)

type RunTerminal struct {
	Status              string
	CancelCause         RunCancelCause
	PublicErrorCode     string
	PublicErrorMessage  string
	Event               string
	Data                interface{}
	TerminalMessageID   int64
	Usage               interface{}
	Runtime             map[string]interface{}
	FinalizationFailure bool
}

func (h *RunHub) SetStore(store chatRunStore) {
	h.mu.Lock()
	h.store = store
	h.mu.Unlock()
}

func (h *RunHub) PersistDurable(ctx context.Context, runID string, persist func(context.Context) error) error {
	if persist == nil {
		return fmt.Errorf("persist durable run: callback is required")
	}
	h.mu.RLock()
	state := h.runs[runID]
	h.mu.RUnlock()
	if state == nil {
		return ErrRunTerminal
	}

	state.transitionMu.Lock()
	defer state.transitionMu.Unlock()

	h.mu.RLock()
	state = h.runs[runID]
	running := state != nil && state.Status == RunStatusRunning && !state.finishing
	h.mu.RUnlock()
	if !running {
		return ErrRunTerminal
	}
	if err := persist(ctx); err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	state = h.runs[runID]
	if state == nil || state.Status != RunStatusRunning || state.finishing {
		return ErrRunTerminal
	}
	state.durable = true
	return nil
}

func (h *RunHub) PersistAdmission(ctx context.Context, runID string, persist RunAdmissionCommit) (*RunSnapshot, error) {
	if persist == nil {
		return nil, fmt.Errorf("persist chat run admission: callback is required")
	}
	h.mu.RLock()
	state := h.runs[runID]
	h.mu.RUnlock()
	if state == nil {
		return nil, ErrRunTerminal
	}

	state.transitionMu.Lock()
	defer state.transitionMu.Unlock()

	h.mu.RLock()
	state = h.runs[runID]
	running := state != nil && state.Status == RunStatusRunning && !state.finishing
	h.mu.RUnlock()
	if !running {
		return nil, ErrRunTerminal
	}
	record, err := persist(ctx)
	if err != nil {
		return nil, err
	}

	h.mu.Lock()
	state = h.runs[runID]
	if state == nil || state.Status != RunStatusRunning || state.finishing {
		h.mu.Unlock()
		return nil, ErrRunTerminal
	}
	if !storedRunMatchesState(record, state) {
		h.mu.Unlock()
		return nil, ErrRunIDConflict
	}
	state.durable = true
	state.UserMessageID = record.UserMessageID
	state.TerminalMessageID = record.TerminalMessageID
	state.RuntimeSnapshot = append(json.RawMessage(nil), record.RuntimeSnapshot...)
	state.AcceptedAt = record.AcceptedAt
	state.ExpiresAt = record.ExpiresAt
	if record.Status == RunStatusRunning {
		state.UpdatedAt = time.Now()
		snapshot := cloneStateSnapshot(state)
		h.mu.Unlock()
		return snapshot, nil
	}
	terminalEvent := h.applyStoredRunTerminal(state, record)
	h.closeSubscribersLocked(state)
	firstOutputStop := state.firstOutputStop
	cancelCause := state.cancelCause
	boundCancel := state.boundCancel
	snapshot := cloneStateSnapshot(state)
	h.mu.Unlock()
	if terminalEvent == nil {
		return nil, fmt.Errorf("stored terminal run has no replay event")
	}
	if firstOutputStop != nil {
		firstOutputStop()
	}
	if cancelCause != nil {
		cancelCause(nil)
	}
	if boundCancel != nil {
		boundCancel()
	}
	return snapshot, nil
}

func (h *RunHub) RestoreTerminal(record repository.ChatRunRecord, intent RunIntent) (*RunSnapshot, error) {
	if record.RunID == "" || record.Status == RunStatusRunning {
		return nil, fmt.Errorf("terminal chat run is required")
	}
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	if existing := h.runs[record.RunID]; existing != nil {
		if !sameRunScope(existing, record.SessionID, record.UserID, record.Kind, intent) {
			return nil, ErrRunIDConflict
		}
		return cloneStateSnapshot(existing), nil
	}
	state := &runState{
		RunSnapshot: RunSnapshot{
			RunID:                record.RunID,
			SessionID:            record.SessionID,
			UserID:               record.UserID,
			Kind:                 record.Kind,
			Operation:            intent.Operation,
			IntentVersion:        intent.Version,
			IntentHash:           intent.Hash,
			RetryTargetMessageID: intent.RetryTargetMessageID,
			RuntimeSnapshot:      append(json.RawMessage(nil), record.RuntimeSnapshot...),
			Status:               RunStatusRunning,
			CreatedAt:            record.AcceptedAt,
			AcceptedAt:           record.AcceptedAt,
			UpdatedAt:            now,
			ExpiresAt:            record.ExpiresAt,
		},
		subscribers: make(map[chan RunEvent]struct{}),
		durable:     true,
	}
	if !storedRunMatchesState(record, state) {
		return nil, ErrRunIDConflict
	}
	if h.applyStoredRunTerminal(state, record) == nil {
		return nil, fmt.Errorf("stored terminal run has no replay event")
	}
	h.runs[record.RunID] = state
	return cloneStateSnapshot(state), nil
}

func storedRunMatchesState(record repository.ChatRunRecord, state *runState) bool {
	if state == nil || record.RunID != state.RunID || record.UserID != state.UserID || record.SessionID != state.SessionID || record.Kind != state.Kind {
		return false
	}
	if record.IntentVersion == 0 {
		return record.Operation == state.Operation
	}
	return record.Operation == state.Operation &&
		record.IntentVersion == state.IntentVersion &&
		record.IntentHash == state.IntentHash &&
		record.RetryTargetMessageID == state.RetryTargetMessageID
}

func newRunContext(timeout time.Duration) (context.Context, context.CancelCauseFunc, func()) {
	return modelstream.WithDeferredFirstOutputTimeout(
		context.Background(),
		timeout,
		runCancellationError{Cause: RunCancelFirstOutputTimeout},
	)
}

func RunCancelCauseFromContext(ctx context.Context) RunCancelCause {
	if ctx == nil {
		return ""
	}
	var cancellation runCancellationError
	if errors.As(context.Cause(ctx), &cancellation) {
		return cancellation.Cause
	}
	return ""
}

func (h *RunHub) Context(runID string) (context.Context, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	state := h.runs[runID]
	if state == nil || state.runContext == nil {
		return nil, false
	}
	return state.runContext, true
}

// BeginExecution atomically transfers a durable running reservation to its
// single in-process execution owner and starts the run-level first-output
// guard. Admission I/O is intentionally outside this budget; every setup step
// after ownership (session/history/config/Agent construction) is inside it.
//
// The caller must launch or enter the worker immediately after this method
// returns. No network or database work may be inserted between ownership and
// worker start, otherwise a process panic could strand a durable running row.
func (h *RunHub) BeginExecution(runID string) (context.Context, error) {
	h.mu.Lock()
	state := h.runs[runID]
	if state == nil || state.Status != RunStatusRunning || state.finishing {
		h.mu.Unlock()
		return nil, ErrRunTerminal
	}
	if !state.durable {
		h.mu.Unlock()
		return nil, ErrRunNotDurable
	}
	if state.executionOwned {
		h.mu.Unlock()
		return nil, ErrRunExecutionOwned
	}
	state.executionOwned = true
	runContext := state.runContext
	h.mu.Unlock()

	if context.Cause(runContext) == nil {
		modelstream.ArmFirstOutputTimeout(runContext)
	}
	return runContext, nil
}

func (h *RunHub) CancelCause(runID string) RunCancelCause {
	h.mu.RLock()
	defer h.mu.RUnlock()
	state := h.runs[runID]
	if state == nil {
		return ""
	}
	return RunCancelCause(state.CancelCause)
}

func (h *RunHub) CancelWithCause(runID string, sessionID, userID int64, cause RunCancelCause) bool {
	if cause == "" {
		cause = RunCancelUserStop
	}
	h.mu.Lock()
	state := h.runs[runID]
	if state == nil || state.SessionID != sessionID || state.UserID != userID || state.Status != RunStatusRunning || state.finishing {
		h.mu.Unlock()
		return false
	}
	if state.cancelRequested {
		alreadyRequested := state.CancelCause == string(cause)
		h.mu.Unlock()
		return alreadyRequested
	}
	state.cancelRequested = true
	state.CancelCause = string(cause)
	state.UpdatedAt = time.Now()
	cancelCause := state.cancelCause
	boundCancel := state.boundCancel
	h.mu.Unlock()
	if cancelCause != nil {
		cancelCause(runCancellationError{Cause: cause})
	}
	if boundCancel != nil {
		boundCancel()
	}
	return true
}

func (h *RunHub) SetUserMessageIDContext(ctx context.Context, runID string, userMessageID int64) (bool, error) {
	if userMessageID <= 0 {
		return false, nil
	}
	h.mu.RLock()
	state := h.runs[runID]
	h.mu.RUnlock()
	if state == nil {
		return false, nil
	}

	state.transitionMu.Lock()
	defer state.transitionMu.Unlock()

	h.mu.Lock()
	state = h.runs[runID]
	if state == nil || state.Status != RunStatusRunning || state.finishing || (state.UserMessageID != 0 && state.UserMessageID != userMessageID) {
		h.mu.Unlock()
		return false, nil
	}
	store := h.store
	durable := state.durable
	h.mu.Unlock()

	if store != nil && durable {
		bound, err := store.BindChatRunUserMessage(ctx, runID, userMessageID)
		if err != nil {
			return false, err
		}
		if !bound {
			return false, nil
		}
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	state = h.runs[runID]
	if state == nil || state.Status != RunStatusRunning || state.finishing || (state.UserMessageID != 0 && state.UserMessageID != userMessageID) {
		return false, nil
	}
	state.UserMessageID = userMessageID
	state.UpdatedAt = time.Now()
	return true, nil
}

func (h *RunHub) Transition(ctx context.Context, runID string, terminal RunTerminal) (*RunSnapshot, bool, *RunEvent, error) {
	return h.transition(ctx, runID, nil, terminal)
}

func (h *RunHub) TransitionWithCommit(ctx context.Context, runID string, commit RunTerminalCommit, terminal RunTerminal) (*RunSnapshot, bool, *RunEvent, error) {
	if commit == nil {
		return nil, false, nil, fmt.Errorf("terminal commit callback is required")
	}
	return h.transition(ctx, runID, commit, terminal)
}

func (h *RunHub) transition(ctx context.Context, runID string, commit RunTerminalCommit, terminal RunTerminal) (*RunSnapshot, bool, *RunEvent, error) {
	if terminal.Status != RunStatusCompleted && terminal.Status != RunStatusFailed && terminal.Status != RunStatusCanceled {
		return nil, false, nil, fmt.Errorf("invalid terminal status %q", terminal.Status)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	h.mu.RLock()
	state := h.runs[runID]
	h.mu.RUnlock()
	if state == nil {
		return nil, false, nil, fmt.Errorf("run not found")
	}

	state.transitionMu.Lock()
	defer state.transitionMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, false, nil, err
	}

	h.mu.Lock()
	if state.Status != RunStatusRunning {
		snapshot := cloneStateSnapshot(state)
		h.mu.Unlock()
		return snapshot, false, nil, nil
	}
	if state.CancelCause != "" {
		terminal.CancelCause = RunCancelCause(state.CancelCause)
	}
	if terminal.CancelCause == "" {
		if cause := RunCancelCauseFromContext(state.runContext); cause != "" {
			terminal.CancelCause = cause
			state.CancelCause = string(cause)
			state.cancelRequested = true
		}
	}
	if state.cancelRequested && terminal.Status != RunStatusCanceled && !terminal.FinalizationFailure {
		terminal.Status = RunStatusCanceled
		if !preservesIncompleteMessageCompletion(terminal) || terminal.CancelCause == RunCancelSessionDeleted {
			terminal.Event = ""
			terminal.Data = nil
		}
		terminal.PublicErrorCode = ""
		terminal.PublicErrorMessage = ""
	}
	if terminal.Status != RunStatusCanceled {
		if terminal.PublicErrorCode == "" && state.ErrorCode != "" {
			terminal.PublicErrorCode = state.ErrorCode
			terminal.PublicErrorMessage = state.Error
		} else if terminal.PublicErrorMessage == "" {
			terminal.PublicErrorMessage = state.Error
		}
	}
	terminal = normalizeRunTerminal(state.Kind, terminal)
	state.finishing = true
	store := h.store
	durable := state.durable
	h.mu.Unlock()

	record := repository.ChatRunRecord{}
	transitioned := true
	useStore := store != nil || commit != nil
	if useStore {
		if commit != nil && !durable {
			h.clearFinishing(state)
			return nil, false, nil, fmt.Errorf("terminal commit requires durable run")
		}
		terminalEvent, err := marshalTerminalEvent(terminal.Event, terminal.Data)
		if err != nil {
			h.clearFinishing(state)
			return nil, false, nil, err
		}
		input := repository.ChatRunTransitionInput{
			RunID:              runID,
			Status:             terminal.Status,
			CancelCause:        string(terminal.CancelCause),
			PublicErrorCode:    terminal.PublicErrorCode,
			PublicErrorMessage: terminal.PublicErrorMessage,
			TerminalMessageID:  terminal.TerminalMessageID,
			TerminalEvent:      terminalEvent,
			ExpiresAt:          time.Now().Add(h.ttl),
		}
		record, transitioned, useStore, err = persistFrozenRunTerminal(ctx, store, durable, commit, input)
		if err != nil {
			h.clearFinishing(state)
			return nil, false, nil, err
		}
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	state = h.runs[runID]
	if state == nil {
		return nil, false, nil, fmt.Errorf("run not found")
	}
	if state.Status != RunStatusRunning {
		state.finishing = false
		return cloneStateSnapshot(state), false, nil, nil
	}
	var terminalEvent *RunEvent
	if useStore {
		terminalEvent = h.applyStoredRunTerminal(state, record)
		if transitioned {
			state.Usage = terminal.Usage
			state.Runtime = terminal.Runtime
		}
	} else {
		if terminal.Event != "" {
			entry := h.appendEventLocked(state, terminal.Event, terminal.Data)
			terminalEvent = &entry
		}
		now := time.Now()
		state.Status = terminal.Status
		state.CancelCause = string(terminal.CancelCause)
		state.ErrorCode = terminal.PublicErrorCode
		if terminal.PublicErrorMessage != "" {
			state.Error = terminal.PublicErrorMessage
		}
		state.TerminalMessageID = terminal.TerminalMessageID
		state.Usage = terminal.Usage
		state.Runtime = terminal.Runtime
		state.TerminalAt = &now
		state.UpdatedAt = now
		state.ExpiresAt = now.Add(h.ttl)
	}
	state.finishing = false
	if state.firstOutputStop != nil {
		state.firstOutputStop()
	}
	if state.cancelCause != nil {
		state.cancelCause(nil)
	}
	h.closeSubscribersLocked(state)
	return cloneStateSnapshot(state), transitioned, terminalEvent, nil
}

// persistFrozenRunTerminal is the durable half of a terminal transition.
//
// The caller has already set finishing and still owns transitionMu. That freeze
// is intentional: after the terminal decision is formed, Cancel and Record must
// not replace it while a retryable database/network failure is being recovered.
// We keep retrying the original commit (rather than manufacturing a second
// failure terminal), and publish the canonical stored record only after it is
// durable. If the process exits during recovery, startup reconciliation turns
// the remaining durable running row into the existing server_restarted terminal.
func persistFrozenRunTerminal(ctx context.Context, store chatRunStore, durable bool, commit RunTerminalCommit, input repository.ChatRunTransitionInput) (repository.ChatRunRecord, bool, bool, error) {
	for retry := 0; ; retry++ {
		attemptCtx := ctx
		cancel := func() {}
		if retry > 0 {
			attemptCtx, cancel = context.WithTimeout(context.Background(), runFinalizationTimeout)
		}

		var (
			record       repository.ChatRunRecord
			transitioned bool
			err          error
		)
		if commit != nil {
			record, transitioned, err = commit(attemptCtx, input)
		} else {
			record, transitioned, err = store.TransitionChatRun(attemptCtx, input)
		}
		cancel()
		if err == nil {
			if retry > 0 {
				logger.Info("terminal persistence recovered: run_id=%q attempts=%d", input.RunID, retry+1)
			}
			return record, transitioned, true, nil
		}
		if commit == nil && errors.Is(err, repository.ErrNotFound) && !durable {
			return repository.ChatRunRecord{}, true, false, nil
		}
		if !isRetryableTerminalPersistenceError(err) {
			return repository.ChatRunRecord{}, false, true, err
		}
		attempt := retry + 1
		if attempt == 1 || attempt&(attempt-1) == 0 {
			logger.Error("terminal persistence pending: run_id=%q attempts=%d err=%v", input.RunID, attempt, err)
		}

		// The initial caller context commonly belongs to a completed worker. Once
		// the terminal decision is frozen, every retry must get a fresh bounded
		// context so its cancellation cannot strand the durable running record.
		time.Sleep(terminalRecoveryBackoff(retry))
	}
}

func terminalRecoveryBackoff(retry int) time.Duration {
	delay := terminalRecoveryInitialBackoff
	for step := 0; step < retry && delay < terminalRecoveryMaxBackoff; step++ {
		delay *= 2
	}
	if delay > terminalRecoveryMaxBackoff {
		return terminalRecoveryMaxBackoff
	}
	return delay
}

func isRetryableTerminalPersistenceError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
		errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) {
		return true
	}
	var sqlErr interface{ SQLState() string }
	if !errors.As(err, &sqlErr) {
		return false
	}
	state := sqlErr.SQLState()
	if len(state) < 2 {
		return false
	}
	switch state[:2] {
	case "08", "40", "53", "57", "58":
		return true
	default:
		return false
	}
}

func preservesIncompleteMessageCompletion(terminal RunTerminal) bool {
	if terminal.Event != streaming.EventMessageComplete {
		return false
	}
	switch data := terminal.Data.(type) {
	case streaming.MessageCompleteEvent:
		return data.Incomplete
	case *streaming.MessageCompleteEvent:
		return data != nil && data.Incomplete
	case map[string]interface{}:
		incomplete, _ := data["incomplete"].(bool)
		return incomplete
	default:
		return false
	}
}

func (h *RunHub) Complete(runID string, usage interface{}, runtime map[string]interface{}) {
	ctx, cancel := context.WithTimeout(context.Background(), runFinalizationTimeout)
	defer cancel()
	_, _, _, _ = h.Transition(ctx, runID, RunTerminal{Status: RunStatusCompleted, Usage: usage, Runtime: runtime})
}

func (h *RunHub) Canceled(runID string, usage interface{}, runtime map[string]interface{}) {
	ctx, cancel := context.WithTimeout(context.Background(), runFinalizationTimeout)
	defer cancel()
	_, _, _, _ = h.Transition(ctx, runID, RunTerminal{Status: RunStatusCanceled, Usage: usage, Runtime: runtime})
}

func (h *RunHub) Fail(runID, errText string) {
	ctx, cancel := context.WithTimeout(context.Background(), runFinalizationTimeout)
	defer cancel()
	_, _, _, _ = h.Transition(ctx, runID, RunTerminal{
		Status: RunStatusFailed, PublicErrorMessage: errText,
	})
}

func (h *RunHub) FailIfRunning(runID, errText string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), runFinalizationTimeout)
	defer cancel()
	_, transitioned, _, err := h.Transition(ctx, runID, RunTerminal{
		Status: RunStatusFailed, PublicErrorCode: "run_incomplete", PublicErrorMessage: errText,
	})
	return err == nil && transitioned
}

func (h *RunHub) FinalizeForDrain(ctx context.Context) (int, error) {
	h.mu.RLock()
	type pendingRun struct {
		runID string
		cause RunCancelCause
	}
	pending := make([]pendingRun, 0)
	for runID, state := range h.runs {
		if state.Status != RunStatusRunning {
			continue
		}
		cause := RunCancelCause(state.CancelCause)
		if cause == "" {
			cause = RunCancelServerDrain
		}
		pending = append(pending, pendingRun{runID: runID, cause: cause})
	}
	h.mu.RUnlock()

	type result struct {
		transitioned bool
		err          error
	}
	results := make(chan result, len(pending))
	for _, run := range pending {
		run := run
		go func() {
			_, transitioned, _, err := h.Transition(ctx, run.runID, RunTerminal{
				Status: RunStatusCanceled, CancelCause: run.cause,
			})
			if err != nil {
				results <- result{err: fmt.Errorf("finalize run %s: %w", run.runID, err)}
				return
			}
			results <- result{transitioned: transitioned}
		}()
	}

	finalized := 0
	transitionErrors := make([]error, 0)
	for range pending {
		select {
		case result := <-results:
			if result.err != nil {
				transitionErrors = append(transitionErrors, result.err)
			} else if result.transitioned {
				finalized++
			}
		case <-ctx.Done():
			transitionErrors = append(transitionErrors, ctx.Err())
			return finalized, errors.Join(transitionErrors...)
		}
	}
	return finalized, errors.Join(transitionErrors...)
}

func (h *RunHub) clearFinishing(state *runState) {
	h.mu.Lock()
	if current := h.runs[state.RunID]; current == state {
		state.finishing = false
	}
	h.mu.Unlock()
}

func normalizeRunTerminal(kind string, terminal RunTerminal) RunTerminal {
	if terminal.Status == RunStatusCanceled {
		if terminal.CancelCause == RunCancelUserStop {
			terminal.PublicErrorCode = ""
			terminal.PublicErrorMessage = ""
			if kind == RunKindCompaction {
				if terminal.Event == "" {
					terminal.Event = "compaction_skip"
					terminal.Data = map[string]interface{}{"reason": "canceled"}
				}
				return terminal
			}
			if kind == RunKindMemoryMaintenance {
				if terminal.Event == "" {
					terminal.Event = "memory_maintenance_canceled"
					terminal.Data = map[string]interface{}{"reason": "canceled"}
				}
				return terminal
			}
			if terminal.Event == "" {
				terminal.Event = "message_complete"
				terminal.Data = map[string]interface{}{
					"message_id": terminal.TerminalMessageID, "finish_reason": "canceled",
				}
			}
			return terminal
		}
		var retryable bool
		terminal.PublicErrorCode, terminal.PublicErrorMessage, retryable = RunCancellationPublicError(terminal.CancelCause)
		if terminal.Event != "" {
			return terminal
		}
		terminal.Event = "error"
		terminal.Data = map[string]interface{}{
			"error": terminal.PublicErrorMessage, "code": terminal.PublicErrorCode, "retryable": retryable,
		}
		return terminal
	}
	if terminal.Event != "" {
		return terminal
	}
	switch terminal.Status {
	case RunStatusFailed:
		if terminal.PublicErrorCode == "" {
			terminal.PublicErrorCode = "run_failed"
		}
		if terminal.PublicErrorMessage == "" {
			terminal.PublicErrorMessage = "任务未能完成，请重试"
		}
		terminal.Event = "error"
		terminal.Data = map[string]interface{}{
			"error": terminal.PublicErrorMessage, "code": terminal.PublicErrorCode, "retryable": true,
		}
	case RunStatusCompleted:
		if kind == RunKindChat {
			terminal.Event = "message_complete"
			terminal.Data = map[string]interface{}{
				"message_id": terminal.TerminalMessageID, "finish_reason": "stop",
			}
		} else if kind == RunKindCompaction {
			terminal.Event = "compaction_complete"
			terminal.Data = map[string]interface{}{"compacted": true}
		} else {
			terminal.Event = "memory_maintenance_complete"
			terminal.Data = map[string]interface{}{"updated": true}
		}
	}
	return terminal
}

func RunCancellationPublicError(cause RunCancelCause) (string, string, bool) {
	switch cause {
	case RunCancelFirstOutputTimeout:
		return "first_output_timeout", "等待模型首个输出超时，请重试", true
	case RunCancelServerDrain:
		return "server_draining", "服务正在更新，请重试", true
	case RunCancelAccountChanged:
		return "account_changed", "账号状态已变更，请重新登录", false
	case RunCancelSessionDeleted:
		return "session_deleted", "会话已删除", false
	case RunCancelUpstream:
		return "upstream_canceled", "上游请求已中断，请重试", true
	default:
		return "run_canceled", "任务已取消", true
	}
}

func marshalTerminalEvent(event string, data interface{}) (json.RawMessage, error) {
	if strings.TrimSpace(event) == "" {
		return nil, nil
	}
	raw, err := json.Marshal(map[string]interface{}{"event": event, "data": data})
	if err != nil {
		return nil, fmt.Errorf("marshal run terminal event: %w", err)
	}
	return raw, nil
}

func (h *RunHub) applyStoredRunTerminal(state *runState, record repository.ChatRunRecord) *RunEvent {
	var terminalEvent *RunEvent
	if len(record.TerminalEvent) > 0 {
		var payload struct {
			Event string      `json:"event"`
			Data  interface{} `json:"data"`
		}
		if json.Unmarshal(record.TerminalEvent, &payload) == nil && payload.Event != "" {
			entry := h.appendEventLocked(state, payload.Event, payload.Data)
			terminalEvent = &entry
		}
	}
	if terminalEvent == nil {
		event, data := fallbackStoredTerminalEvent(record)
		entry := h.appendEventLocked(state, event, data)
		terminalEvent = &entry
	}
	state.Status = record.Status
	state.CancelCause = record.CancelCause
	state.ErrorCode = record.PublicErrorCode
	if record.PublicErrorMessage != "" {
		state.Error = record.PublicErrorMessage
	}
	state.UserMessageID = record.UserMessageID
	state.TerminalMessageID = record.TerminalMessageID
	if record.IntentVersion > 0 {
		state.Operation = record.Operation
		state.IntentVersion = record.IntentVersion
		state.IntentHash = record.IntentHash
		state.RetryTargetMessageID = record.RetryTargetMessageID
	}
	state.AcceptedAt = record.AcceptedAt
	state.TerminalAt = record.TerminalAt
	state.ExpiresAt = record.ExpiresAt
	state.UpdatedAt = time.Now()
	return terminalEvent
}

func fallbackStoredTerminalEvent(record repository.ChatRunRecord) (string, interface{}) {
	if record.Status == RunStatusCompleted {
		if record.Operation == RunOperationCompaction || record.Kind == RunKindCompaction {
			return "compaction_complete", map[string]interface{}{"compacted": true}
		}
		if record.Kind == RunKindMemoryMaintenance {
			return "memory_maintenance_complete", map[string]interface{}{"updated": true}
		}
		return "message_complete", map[string]interface{}{"message_id": record.TerminalMessageID, "finish_reason": "stop"}
	}
	code := record.PublicErrorCode
	if code == "" {
		code = "run_failed"
	}
	message := record.PublicErrorMessage
	if message == "" {
		message = "任务未能完成，请重试"
	}
	return "error", map[string]interface{}{"error": message, "code": code, "retryable": true}
}
