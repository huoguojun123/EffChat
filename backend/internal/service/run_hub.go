package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	RunStatusRunning   = "running"
	RunStatusCompleted = "completed"
	RunStatusFailed    = "failed"
	RunStatusCanceled  = "canceled"
)

const (
	RunKindChat              = "chat"
	RunKindCompaction        = "compaction"
	RunKindMemoryMaintenance = "memory_maintenance"
)

var (
	ErrRunIDConflict     = errors.New("run_id conflict")
	ErrRunNotFound       = errors.New("run not found")
	ErrSessionRunActive  = errors.New("session run active")
	ErrServerDraining    = errors.New("server draining")
	ErrRunTerminal       = errors.New("run is no longer running")
	ErrRunNotDurable     = errors.New("run admission is not durable")
	ErrRunExecutionOwned = errors.New("run execution already has an owner")
)

type RunEvent struct {
	Cursor int64       `json:"cursor"`
	Event  string      `json:"event"`
	Data   interface{} `json:"data"`
}

type RunSegment struct {
	Type      string                   `json:"type"`
	Content   string                   `json:"content,omitempty"`
	Thinking  string                   `json:"thinking,omitempty"`
	ToolCalls []map[string]interface{} `json:"tool_calls,omitempty"`
}

type RunSnapshot struct {
	RunID                string                   `json:"run_id"`
	SessionID            int64                    `json:"session_id"`
	UserID               int64                    `json:"-"`
	UserMessageID        int64                    `json:"user_message_id"`
	TerminalMessageID    int64                    `json:"terminal_message_id,omitempty"`
	Kind                 string                   `json:"kind"`
	Operation            string                   `json:"operation"`
	IntentVersion        int                      `json:"-"`
	IntentHash           string                   `json:"-"`
	RetryTargetMessageID int64                    `json:"-"`
	RuntimeSnapshot      json.RawMessage          `json:"-"`
	Status               string                   `json:"status"`
	CancelCause          string                   `json:"cancel_cause,omitempty"`
	Cursor               int64                    `json:"cursor"`
	Content              string                   `json:"content"`
	Thinking             string                   `json:"thinking"`
	ToolCalls            []map[string]interface{} `json:"tool_calls,omitempty"`
	Segments             []RunSegment             `json:"segments,omitempty"`
	Error                string                   `json:"error,omitempty"`
	ErrorCode            string                   `json:"error_code,omitempty"`
	RequestID            string                   `json:"request_id,omitempty"`
	Usage                interface{}              `json:"usage,omitempty"`
	Runtime              map[string]interface{}   `json:"runtime,omitempty"`
	CreatedAt            time.Time                `json:"created_at"`
	AcceptedAt           time.Time                `json:"accepted_at"`
	TerminalAt           *time.Time               `json:"terminal_at,omitempty"`
	UpdatedAt            time.Time                `json:"updated_at"`
	ExpiresAt            time.Time                `json:"expires_at"`
	ReplayFrom           int64                    `json:"replay_from,omitempty"`
	OutputTruncated      bool                     `json:"output_truncated,omitempty"`
	Reused               bool                     `json:"-"`
}

type RunHub struct {
	mu       sync.RWMutex
	runs     map[string]*runState
	ttl      time.Duration
	maxBytes int
	draining bool
	store    chatRunStore
}

type runState struct {
	RunSnapshot
	transitionMu    sync.Mutex
	eventMu         sync.RWMutex
	events          []RunEvent
	bytes           int
	subscribers     map[chan RunEvent]struct{}
	runContext      context.Context
	cancelCause     context.CancelCauseFunc
	firstOutputStop func()
	boundCancel     context.CancelFunc
	cancelRequested bool
	finishing       bool
	durable         bool
	executionOwned  bool
}

func NewRunHub(ttl time.Duration, maxBytes int) *RunHub {
	hub := &RunHub{
		runs:     make(map[string]*runState),
		ttl:      ttl,
		maxBytes: maxBytes,
	}
	go hub.cleanupLoop()
	return hub
}

func (h *RunHub) Start(sessionID, userID, userMessageID int64, clientRunID, kind string) (*RunSnapshot, error) {
	return h.StartWithIntentAndFirstOutputTimeout(sessionID, userID, userMessageID, clientRunID, kind, legacyRunIntent(kind), 0)
}

func (h *RunHub) StartWithFirstOutputTimeout(sessionID, userID, userMessageID int64, clientRunID, kind string, timeout time.Duration) (*RunSnapshot, error) {
	return h.StartWithIntentAndFirstOutputTimeout(sessionID, userID, userMessageID, clientRunID, kind, legacyRunIntent(kind), timeout)
}

func (h *RunHub) StartWithIntent(sessionID, userID, userMessageID int64, clientRunID, kind string, intent RunIntent) (*RunSnapshot, error) {
	return h.StartWithIntentAndFirstOutputTimeout(sessionID, userID, userMessageID, clientRunID, kind, intent, 0)
}

func (h *RunHub) StartWithIntentAndFirstOutputTimeout(sessionID, userID, userMessageID int64, clientRunID, kind string, intent RunIntent, timeout time.Duration) (*RunSnapshot, error) {
	runID := strings.TrimSpace(clientRunID)
	if runID == "" {
		runID = uuid.NewString()
	}
	if kind == "" {
		kind = RunKindChat
	}

	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()

	if existing := h.runs[runID]; existing != nil {
		if !sameRunScope(existing, sessionID, userID, kind, intent) {
			return nil, ErrRunIDConflict
		}
		if existing.Status == RunStatusRunning {
			existing.ExpiresAt = now.Add(h.ttl)
		}
		snapshot := cloneStateSnapshot(existing)
		snapshot.Reused = true
		return snapshot, nil
	}
	if h.draining {
		return nil, ErrServerDraining
	}
	for _, existing := range h.runs {
		if existing.SessionID == sessionID && existing.UserID == userID && existing.Status == RunStatusRunning {
			return nil, ErrSessionRunActive
		}
	}

	runContext, cancelCause, firstOutputStop := newRunContext(timeout)
	state := &runState{
		RunSnapshot: RunSnapshot{
			RunID:                runID,
			SessionID:            sessionID,
			UserID:               userID,
			UserMessageID:        userMessageID,
			Kind:                 kind,
			Operation:            intent.Operation,
			IntentVersion:        intent.Version,
			IntentHash:           intent.Hash,
			RetryTargetMessageID: intent.RetryTargetMessageID,
			Status:               RunStatusRunning,
			CreatedAt:            now,
			AcceptedAt:           now,
			UpdatedAt:            now,
			ExpiresAt:            now.Add(h.ttl),
		},
		runContext:      runContext,
		cancelCause:     cancelCause,
		firstOutputStop: firstOutputStop,
		subscribers:     make(map[chan RunEvent]struct{}),
	}
	h.runs[runID] = state
	return cloneStateSnapshot(state), nil
}

func (h *RunHub) Match(runID string, sessionID, userID int64, kind string, intent RunIntent) (*RunSnapshot, bool, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	state := h.runs[runID]
	if state == nil {
		return nil, false, nil
	}
	if !sameRunScope(state, sessionID, userID, kind, intent) {
		return nil, true, ErrRunIDConflict
	}
	return cloneStateSnapshot(state), true, nil
}

func (h *RunHub) BeginDrain() {
	h.mu.Lock()
	h.draining = true
	h.mu.Unlock()
}

func (h *RunHub) WaitForIdle(ctx context.Context) bool {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if h.activeCount() == 0 {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

func (h *RunHub) CancelForDrain() int {
	return h.cancelMatching(RunCancelServerDrain, func(state *runState) bool { return true })
}

func (h *RunHub) activeCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	count := 0
	for _, state := range h.runs {
		if state.Status == RunStatusRunning {
			count++
		}
	}
	return count
}

func (h *RunHub) Record(runID, event string, data interface{}) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	state := h.runs[runID]
	if state == nil || state.Status != RunStatusRunning || state.finishing {
		return false
	}
	h.appendEventLocked(state, event, data)
	return true
}

func (h *RunHub) appendEventLocked(state *runState, event string, data interface{}) RunEvent {
	state.eventMu.Lock()
	defer state.eventMu.Unlock()

	state.Cursor++
	state.UpdatedAt = time.Now()
	state.ExpiresAt = state.UpdatedAt.Add(h.ttl)
	state.applyEvent(event, data)

	entry := RunEvent{Cursor: state.Cursor, Event: event, Data: data}
	state.events = append(state.events, entry)
	if b, err := json.Marshal(entry); err == nil {
		state.bytes += len(b)
	}
	state.trimLocked(h.maxBytes)

	for ch := range state.subscribers {
		select {
		case ch <- entry:
		default:
		}
	}
	return entry
}

// Bind 登记 run 的 cancel 函数，使其可被 Cancel 主动中止。
// 在 handler 创建可取消 context 后调用。
func (h *RunHub) Bind(runID string, cancel context.CancelFunc) bool {
	h.mu.Lock()
	state := h.runs[runID]
	if state == nil || state.Status != RunStatusRunning || state.finishing {
		h.mu.Unlock()
		return false
	}
	state.boundCancel = cancel
	cancelNow := state.cancelRequested
	h.mu.Unlock()
	if cancelNow {
		cancel()
	}
	return true
}

func (h *RunHub) SetUserMessageID(runID string, userMessageID int64) bool {
	ctx, cancel := context.WithTimeout(context.Background(), runFinalizationTimeout)
	defer cancel()
	ok, _ := h.SetUserMessageIDContext(ctx, runID, userMessageID)
	return ok
}

// Cancel 主动中止指定 run 的后端生成。取消可先于 Bind 到达，随后绑定的 context 会立即取消。
func (h *RunHub) Cancel(runID string, sessionID, userID int64) bool {
	return h.CancelWithCause(runID, sessionID, userID, RunCancelUserStop)
}

// CancelByUser requests cancellation for every active run owned by a user.
func (h *RunHub) CancelByUser(userID int64) int {
	return h.cancelMatching(RunCancelAccountChanged, func(state *runState) bool {
		return state.UserID == userID
	})
}

// CancelSession requests cancellation for every active run in one session.
func (h *RunHub) CancelSession(sessionID, userID int64) int {
	return h.cancelMatching(RunCancelSessionDeleted, func(state *runState) bool {
		return state.SessionID == sessionID && state.UserID == userID
	})
}

func (h *RunHub) cancelMatching(cause RunCancelCause, matches func(*runState) bool) int {
	h.mu.Lock()
	cancelCauses := make([]context.CancelCauseFunc, 0)
	boundCancels := make([]context.CancelFunc, 0)
	count := 0
	for _, state := range h.runs {
		if !matches(state) || state.Status != RunStatusRunning || state.finishing {
			continue
		}
		if state.cancelRequested && !shouldReplaceCancelCause(RunCancelCause(state.CancelCause), cause) {
			continue
		}
		state.cancelRequested = true
		state.CancelCause = string(cause)
		state.UpdatedAt = time.Now()
		count++
		if state.cancelCause != nil {
			cancelCauses = append(cancelCauses, state.cancelCause)
		}
		if state.boundCancel != nil {
			boundCancels = append(boundCancels, state.boundCancel)
		}
	}
	h.mu.Unlock()
	for _, cancel := range cancelCauses {
		cancel(runCancellationError{Cause: cause})
	}
	for _, cancel := range boundCancels {
		cancel()
	}
	return count
}

func shouldReplaceCancelCause(current, next RunCancelCause) bool {
	if current == next {
		return false
	}
	if next == RunCancelSessionDeleted {
		return true
	}
	return next == RunCancelAccountChanged && current != RunCancelSessionDeleted
}

func (h *RunHub) IsCanceled(runID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	state := h.runs[runID]
	return state != nil && state.Status == RunStatusCanceled
}

func (h *RunHub) IsServerDraining(runID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	state := h.runs[runID]
	return state != nil && state.CancelCause == string(RunCancelServerDrain)
}

func (h *RunHub) Active(sessionID, userID int64) *RunSnapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var latest *RunSnapshot
	for _, state := range h.runs {
		if state.SessionID != sessionID || state.UserID != userID || state.Status != RunStatusRunning {
			continue
		}
		snapshot := cloneStateSnapshot(state)
		if latest == nil || snapshot.UpdatedAt.After(latest.UpdatedAt) {
			latest = snapshot
		}
	}
	return latest
}

func (h *RunHub) CountActiveByUser(userID int64, kind string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	count := 0
	for _, state := range h.runs {
		if state.UserID != userID || state.Status != RunStatusRunning {
			continue
		}
		if kind != "" && state.Kind != kind {
			continue
		}
		count++
	}
	return count
}

func (h *RunHub) Get(runID string, sessionID, userID int64) (*RunSnapshot, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	state := h.runs[runID]
	if state == nil || state.SessionID != sessionID || state.UserID != userID {
		return nil, false
	}
	return cloneStateSnapshot(state), true
}

func (h *RunHub) EventsAfter(runID string, sessionID, userID, cursor int64) ([]RunEvent, <-chan RunEvent, func(), *RunSnapshot, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	state := h.runs[runID]
	if state == nil || state.SessionID != sessionID || state.UserID != userID {
		return nil, nil, nil, nil, ErrRunNotFound
	}

	state.eventMu.RLock()
	events := eventsAfterCursor(state.events, cursor)
	snapshot := cloneSnapshot(&state.RunSnapshot)
	state.eventMu.RUnlock()

	if state.Status != RunStatusRunning {
		return events, nil, func() {}, snapshot, nil
	}

	ch := make(chan RunEvent, 32)
	state.subscribers[ch] = struct{}{}
	cleanup := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if current := h.runs[runID]; current != nil {
			delete(current.subscribers, ch)
		}
	}
	return events, ch, cleanup, snapshot, nil
}

func (h *RunHub) EventsSince(runID string, sessionID, userID, cursor int64) ([]RunEvent, *RunSnapshot, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	state := h.runs[runID]
	if state == nil || state.SessionID != sessionID || state.UserID != userID {
		return nil, nil, ErrRunNotFound
	}
	state.eventMu.RLock()
	defer state.eventMu.RUnlock()
	return eventsAfterCursor(state.events, cursor), cloneSnapshot(&state.RunSnapshot), nil
}

func eventsAfterCursor(events []RunEvent, cursor int64) []RunEvent {
	result := make([]RunEvent, 0)
	for _, event := range events {
		if event.Cursor > cursor {
			result = append(result, event)
		}
	}
	return result
}

func (h *RunHub) cleanupLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		h.cleanup()
	}
}

func (h *RunHub) cleanup() {
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	for runID, state := range h.runs {
		if state.Status == RunStatusRunning {
			continue
		}
		if now.After(state.ExpiresAt) {
			h.closeSubscribersLocked(state)
			delete(h.runs, runID)
		}
	}
}

func (h *RunHub) closeSubscribersLocked(state *runState) {
	for ch := range state.subscribers {
		close(ch)
		delete(state.subscribers, ch)
	}
}

func (s *runState) applyEvent(event string, data interface{}) {
	payload := toMap(data)
	switch event {
	case "content_delta":
		if delta, ok := payload["delta"].(string); ok {
			s.Content += delta
			s.appendContentSegment(delta)
		}
	case "thinking_delta":
		if delta, ok := payload["delta"].(string); ok {
			s.Thinking += delta
			s.appendThinkingSegment(delta)
		}
	case "assistant_attempt_reset":
		contentRunes := intFromPayload(payload["content_runes"])
		thinkingRunes := intFromPayload(payload["thinking_runes"])
		s.Content = trimTrailingRunes(s.Content, contentRunes)
		s.Thinking = trimTrailingRunes(s.Thinking, thinkingRunes)
		s.Segments = trimRunSegments(s.Segments, contentRunes, thinkingRunes)
	case "tool_call_start":
		id, _ := payload["tool_call_id"].(string)
		if id == "" {
			return
		}
		name, _ := payload["tool_name"].(string)
		toolCall := map[string]interface{}{
			"id":     id,
			"name":   name,
			"status": "running",
		}
		s.ToolCalls = append(s.ToolCalls, toolCall)
		s.appendToolCallSegment(toolCall)
	case "tool_call_result":
		id, _ := payload["tool_call_id"].(string)
		if id == "" {
			return
		}
		for _, tc := range s.ToolCalls {
			if tc["id"] == id {
				tc["result"] = payload["result"]
				tc["status"] = "done"
				break
			}
		}
		s.updateSegmentToolCall(id, payload["result"])
	case "error":
		if text, ok := payload["error"].(string); ok {
			s.Error = text
		}
		if code, ok := payload["code"].(string); ok {
			s.ErrorCode = code
		}
		if requestID, ok := payload["request_id"].(string); ok {
			s.RequestID = requestID
		}
	}
}

func (s *runState) appendContentSegment(delta string) {
	if delta == "" {
		return
	}
	if len(s.Segments) > 0 {
		last := &s.Segments[len(s.Segments)-1]
		if last.Type == "content" && last.Thinking == "" {
			last.Content += delta
			return
		}
	}
	s.Segments = append(s.Segments, RunSegment{Type: "content", Content: delta})
}

func (s *runState) appendThinkingSegment(delta string) {
	if delta == "" {
		return
	}
	if len(s.Segments) > 0 {
		last := &s.Segments[len(s.Segments)-1]
		if last.Type == "content" && last.Content == "" {
			last.Thinking += delta
			return
		}
	}
	s.Segments = append(s.Segments, RunSegment{Type: "content", Thinking: delta})
}

func (s *runState) appendToolCallSegment(toolCall map[string]interface{}) {
	if len(s.Segments) > 0 {
		last := &s.Segments[len(s.Segments)-1]
		if last.Type == "tool" {
			last.ToolCalls = append(last.ToolCalls, cloneToolCall(toolCall))
			return
		}
	}
	s.Segments = append(s.Segments, RunSegment{Type: "tool", ToolCalls: []map[string]interface{}{cloneToolCall(toolCall)}})
}

func (s *runState) updateSegmentToolCall(id string, result interface{}) {
	for segmentIndex := range s.Segments {
		for toolIndex := range s.Segments[segmentIndex].ToolCalls {
			toolCall := s.Segments[segmentIndex].ToolCalls[toolIndex]
			if toolCall["id"] != id {
				continue
			}
			toolCall["result"] = result
			toolCall["status"] = "done"
			return
		}
	}
}

func trimRunSegments(segments []RunSegment, contentRunes, thinkingRunes int) []RunSegment {
	result := cloneRunSegments(segments)
	remainingContent := max(contentRunes, 0)
	remainingThinking := max(thinkingRunes, 0)
	for index := len(result) - 1; index >= 0 && (remainingContent > 0 || remainingThinking > 0); index-- {
		segment := &result[index]
		if segment.Type != "content" {
			continue
		}
		if remainingContent > 0 && segment.Content != "" {
			removed := min(len([]rune(segment.Content)), remainingContent)
			segment.Content = trimTrailingRunes(segment.Content, removed)
			remainingContent -= removed
		}
		if remainingThinking > 0 && segment.Thinking != "" {
			removed := min(len([]rune(segment.Thinking)), remainingThinking)
			segment.Thinking = trimTrailingRunes(segment.Thinking, removed)
			remainingThinking -= removed
		}
	}
	filtered := result[:0]
	for _, segment := range result {
		if segment.Type != "content" || segment.Content != "" || segment.Thinking != "" {
			filtered = append(filtered, segment)
		}
	}
	return filtered
}

func intFromPayload(value interface{}) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func trimTrailingRunes(value string, count int) string {
	if count <= 0 || value == "" {
		return value
	}
	runes := []rune(value)
	if count >= len(runes) {
		return ""
	}
	return string(runes[:len(runes)-count])
}

func (s *runState) trimLocked(maxBytes int) {
	if maxBytes <= 0 || s.bytes <= maxBytes/2 {
		return
	}
	if s.cacheBytes() <= maxBytes {
		return
	}

	// Leave headroom so ordinary deltas do not immediately trigger another full cache check.
	targetBytes := maxBytes / 3
	for s.bytes > targetBytes && len(s.events) > 0 {
		first := s.events[0]
		if b, err := json.Marshal(first); err == nil {
			s.bytes -= len(b)
		}
		if first.Event == "content_delta" || first.Event == "thinking_delta" || first.Event == "tool_call_start" || first.Event == "tool_call_result" {
			s.OutputTruncated = true
		}
		s.ReplayFrom = first.Cursor
		s.events = s.events[1:]
	}
	s.rebuildCachedOutputLocked()
	for s.cacheBytes() > maxBytes && len(s.events) > 0 {
		first := s.events[0]
		if b, err := json.Marshal(first); err == nil {
			s.bytes -= len(b)
		}
		if first.Event == "content_delta" || first.Event == "thinking_delta" || first.Event == "tool_call_start" || first.Event == "tool_call_result" {
			s.OutputTruncated = true
		}
		s.ReplayFrom = first.Cursor
		s.events = s.events[1:]
		s.rebuildCachedOutputLocked()
	}
}

func (s *runState) rebuildCachedOutputLocked() {
	s.Content = ""
	s.Thinking = ""
	s.ToolCalls = nil
	s.Segments = nil
	s.Error = ""
	s.ErrorCode = ""
	s.RequestID = ""
	for _, event := range s.events {
		s.applyEvent(event.Event, event.Data)
	}
}

func (s *runState) cacheBytes() int {
	payload := struct {
		Content   string                   `json:"content"`
		Thinking  string                   `json:"thinking"`
		ToolCalls []map[string]interface{} `json:"tool_calls"`
		Segments  []RunSegment             `json:"segments"`
		Error     string                   `json:"error"`
		ErrorCode string                   `json:"error_code"`
		RequestID string                   `json:"request_id"`
	}{
		Content:   s.Content,
		Thinking:  s.Thinking,
		ToolCalls: s.ToolCalls,
		Segments:  s.Segments,
		Error:     s.Error,
		ErrorCode: s.ErrorCode,
		RequestID: s.RequestID,
	}
	if raw, err := json.Marshal(payload); err == nil {
		return s.bytes + len(raw)
	}
	return s.bytes + len(s.Content) + len(s.Thinking) + len(s.Error) + len(s.ErrorCode) + len(s.RequestID)
}

func toMap(value interface{}) map[string]interface{} {
	if typed, ok := value.(map[string]interface{}); ok {
		return typed
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return map[string]interface{}{}
	}
	var out map[string]interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]interface{}{}
	}
	return out
}

func cloneStateSnapshot(state *runState) *RunSnapshot {
	if state == nil {
		return nil
	}
	state.eventMu.RLock()
	defer state.eventMu.RUnlock()
	return cloneSnapshot(&state.RunSnapshot)
}

func cloneSnapshot(snapshot *RunSnapshot) *RunSnapshot {
	if snapshot == nil {
		return nil
	}
	out := *snapshot
	out.RuntimeSnapshot = append(json.RawMessage(nil), snapshot.RuntimeSnapshot...)
	if snapshot.TerminalAt != nil {
		terminalAt := *snapshot.TerminalAt
		out.TerminalAt = &terminalAt
	}
	if snapshot.ToolCalls != nil {
		out.ToolCalls = make([]map[string]interface{}, len(snapshot.ToolCalls))
		for i, tc := range snapshot.ToolCalls {
			copied := make(map[string]interface{}, len(tc))
			for k, v := range tc {
				copied[k] = v
			}
			out.ToolCalls[i] = copied
		}
	}
	if snapshot.Segments != nil {
		out.Segments = cloneRunSegments(snapshot.Segments)
	}
	return &out
}

func cloneRunSegments(segments []RunSegment) []RunSegment {
	if segments == nil {
		return nil
	}
	result := make([]RunSegment, len(segments))
	for index, segment := range segments {
		result[index] = RunSegment{
			Type:      segment.Type,
			Content:   segment.Content,
			Thinking:  segment.Thinking,
			ToolCalls: make([]map[string]interface{}, len(segment.ToolCalls)),
		}
		for toolIndex, toolCall := range segment.ToolCalls {
			result[index].ToolCalls[toolIndex] = cloneToolCall(toolCall)
		}
	}
	return result
}

func cloneToolCall(toolCall map[string]interface{}) map[string]interface{} {
	copy := make(map[string]interface{}, len(toolCall))
	for key, value := range toolCall {
		copy[key] = value
	}
	return copy
}

func legacyRunIntent(kind string) RunIntent {
	operation := RunOperationSend
	if kind == RunKindCompaction {
		operation = RunOperationCompaction
	} else if kind == RunKindMemoryMaintenance {
		operation = RunOperationMemoryCompact
	}
	return RunIntent{Operation: operation}
}

func sameRunScope(state *runState, sessionID, userID int64, kind string, intent RunIntent) bool {
	if state.SessionID != sessionID || state.UserID != userID || state.Kind != kind {
		return false
	}
	return state.Operation == intent.Operation &&
		state.IntentVersion == intent.Version &&
		state.IntentHash == intent.Hash &&
		state.RetryTargetMessageID == intent.RetryTargetMessageID
}
