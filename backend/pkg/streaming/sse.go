package streaming

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
)

// SSEWriter SSE 流式写入器。
//
// 写入受 mu 保护：主流式循环与心跳 goroutine 会并发调用 WriteEvent/WritePing，
// 而 http.ResponseWriter 本身非并发安全，必须串行化。
type SSEWriter struct {
	mu      sync.Mutex
	writer  http.ResponseWriter
	flusher http.Flusher
	hook    func(event string, data interface{}) bool
}

type EventWriter interface {
	WriteEvent(event string, data interface{}) error
}

func NewSSEWriter(c *gin.Context) (*SSEWriter, error) {
	w := c.Writer
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("streaming not supported")
	}

	// 设置 SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.Header().Set("X-Accel-Buffering", "no") // Nginx 禁用缓冲

	return &SSEWriter{
		writer:  w,
		flusher: flusher,
	}, nil
}

func (s *SSEWriter) SetEventHook(hook func(event string, data interface{}) bool) {
	s.hook = hook
}

func (s *SSEWriter) RecordEvent(event string, data interface{}) bool {
	if s.hook != nil {
		return s.hook(event, data)
	}
	return true
}

// WriteEvent 写入 SSE 事件
func (s *SSEWriter) WriteEvent(event string, data interface{}) error {
	return s.writeEvent(event, data, true)
}

func (s *SSEWriter) WriteEventWithoutRecord(event string, data interface{}) error {
	return s.writeEvent(event, data, false)
}

func (s *SSEWriter) writeEvent(event string, data interface{}, record bool) error {
	if record {
		if !s.RecordEvent(event, data) {
			return nil
		}
	}

	// 序列化数据
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal data: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 写入事件
	if event != "" {
		if _, err := fmt.Fprintf(s.writer, "event: %s\n", event); err != nil {
			return err
		}
	}

	// 写入数据（dataJSON 为单行 JSON，SSE data 字段无需拆分）
	if _, err := fmt.Fprintf(s.writer, "data: %s\n\n", string(dataJSON)); err != nil {
		return err
	}

	// 立即刷新
	s.flusher.Flush()
	return nil
}

// WritePing 写入心跳事件，保持长时间空闲（如超长工具调用）期间连接存活。
//
// 心跳不经过 RecordEvent，避免污染 RunHub 的可回放事件序列与游标。
func (s *SSEWriter) WritePing() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := fmt.Fprint(s.writer, "event: ping\ndata: {}\n\n"); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

// WriteError 写入错误事件
func (s *SSEWriter) WriteError(errMsg string, fields ...map[string]interface{}) error {
	payload := map[string]interface{}{"error": errMsg}
	for _, item := range fields {
		for key, value := range item {
			if key == "" {
				continue
			}
			payload[key] = value
		}
	}
	if _, ok := payload["error"]; !ok {
		payload["error"] = errMsg
	}
	return s.WriteEvent(EventError, payload)
}

// Close 关闭流
func (s *SSEWriter) Close() error {
	// SSE 流通过客户端断开或写入完成自然关闭
	return nil
}

// SSE 事件类型定义
const (
	EventMessageStart    = "message_start"
	EventContentDelta    = "content_delta"
	EventThinkingDelta   = "thinking_delta"
	EventAttemptReset    = "assistant_attempt_reset"
	EventModelRetry      = "model_retry"
	EventToolCallStart   = "tool_call_start"
	EventToolCallResult  = "tool_call_result"
	EventError           = "error"
	EventMessageComplete = "message_complete"
	EventPing            = "ping"

	// 压缩（/compact）专用事件：复用 SSE + RunHub 通道实现断点续传。
	EventCompactionStart    = "compaction_start"
	EventCompactionComplete = "compaction_complete"
	EventCompactionSkip     = "compaction_skip"
)

// 事件数据结构
type MessageStartEvent struct {
	MessageID     int64  `json:"message_id"`
	RunID         string `json:"run_id,omitempty"`
	UserMessageID int64  `json:"user_message_id,omitempty"`
}

type ContentDeltaEvent struct {
	Delta string `json:"delta"`
}

type ThinkingDeltaEvent struct {
	Delta string `json:"delta"`
}

type AttemptResetEvent struct {
	ContentRunes  int `json:"content_runes"`
	ThinkingRunes int `json:"thinking_runes"`
}

type ModelRetryEvent struct {
	Attempt     int    `json:"attempt"`
	MaxAttempts int    `json:"max_attempts"`
	DelayMs     int64  `json:"delay_ms"`
	Category    string `json:"category"`
}

type ToolCallStartEvent struct {
	ToolName   string `json:"tool_name"`
	ToolCallID string `json:"tool_call_id"`
}

type ToolCallResultEvent struct {
	ToolCallID string      `json:"tool_call_id"`
	Result     interface{} `json:"result"`
}

type MessageCompleteEvent struct {
	MessageID       int64       `json:"message_id"`
	FinishReason    string      `json:"finish_reason"`
	Incomplete      bool        `json:"incomplete,omitempty"`
	Usage           *UsageEvent `json:"usage,omitempty"`
	DurationMs      int64       `json:"duration_ms,omitempty"`
	TokensPerSecond float64     `json:"tokens_per_second,omitempty"`
}

type UsageEvent struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	CachedTokens     int `json:"cached_tokens,omitempty"`
	ReasoningTokens  int `json:"reasoning_tokens,omitempty"`
}
