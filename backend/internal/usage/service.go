package usage

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/huoguojun123/EffChat/internal/modelstream"
)

const maxErrorMessageRunes = 500

type Store interface {
	Create(ctx context.Context, event *Event) error
	Aggregate(ctx context.Context, start, end time.Time) (*Summary, error)
}

type ToolStore interface {
	CreateToolEvent(ctx context.Context, event *ToolEvent) error
	UpdateToolEvent(ctx context.Context, event *ToolEvent) error
	AggregateToolUsage(ctx context.Context, start, end time.Time) (ToolTotals, []ByTool, error)
	QuotaUsersForToday(ctx context.Context) ([]QuotaUserUsage, error)
}

type Service struct {
	store     Store
	toolStore ToolStore
	mu        sync.Mutex
	draining  bool
	tasks     sync.WaitGroup
}

func NewService(store Store) *Service {
	toolStore, _ := store.(ToolStore)
	return &Service{store: store, toolStore: toolStore}
}

// Record 尽力记录一次模型调用。
//
// 统计是旁路能力：它不能改变聊天成功/失败的主语义，也不能因为数据库短暂异常让用户
// 本来已经生成成功的回复变成失败。因此这里统一短超时、只打日志。
func (s *Service) Record(event Event) {
	if s == nil || s.store == nil {
		return
	}
	normalizeEvent(&event)
	if !s.startTask() {
		return
	}
	go func() {
		defer s.tasks.Done()
		s.recordSync(event)
	}()
}

func (s *Service) recordSync(event Event) {
	if s == nil || s.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.store.Create(ctx, &event); err != nil {
		log.Printf("[usage] record failed: kind=%s provider=%s model=%s err=%v", event.Kind, event.Provider, event.ModelID, err)
	}
}

func (s *Service) RecordTool(event ToolEvent) {
	if s == nil || s.toolStore == nil {
		return
	}
	normalizeToolEvent(&event)
	if !s.startTask() {
		return
	}
	go func() {
		defer s.tasks.Done()
		s.recordToolSync(event)
	}()
}

func (s *Service) startTask() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.draining {
		return false
	}
	s.tasks.Add(1)
	return true
}

func (s *Service) Drain(ctx context.Context) bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	s.draining = true
	s.mu.Unlock()
	done := make(chan struct{})
	go func() {
		s.tasks.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *Service) RecordToolSync(event ToolEvent) {
	if s == nil || s.toolStore == nil {
		return
	}
	normalizeToolEvent(&event)
	s.recordToolSync(event)
}

func (s *Service) FinishToolSync(event ToolEvent) {
	if s == nil || s.toolStore == nil {
		return
	}
	normalizeToolEvent(&event)
	if event.ID <= 0 {
		s.recordToolSync(event)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.toolStore.UpdateToolEvent(ctx, &event); err != nil {
		log.Printf("[usage] update tool failed: id=%d tool=%s user=%d err=%v", event.ID, event.ToolKey, event.UserID, err)
	}
}

func (s *Service) recordToolSync(event ToolEvent) {
	if s == nil || s.toolStore == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.toolStore.CreateToolEvent(ctx, &event); err != nil {
		log.Printf("[usage] record tool failed: tool=%s user=%d err=%v", event.ToolKey, event.UserID, err)
	}
}

func (s *Service) Summary(ctx context.Context, rangeValue string) (*Summary, error) {
	now := time.Now()
	return s.SummaryBetween(ctx, rangeStart(now, rangeValue), now)
}

func (s *Service) SummaryBetween(ctx context.Context, start, end time.Time) (*Summary, error) {
	if s == nil || s.store == nil {
		return &Summary{}, nil
	}
	summary, err := s.store.Aggregate(ctx, start, end)
	if err != nil {
		return nil, err
	}
	if summary == nil {
		summary = &Summary{}
	}
	if s.toolStore == nil {
		return summary, nil
	}
	toolTotals, byTool, err := s.toolStore.AggregateToolUsage(ctx, start, end)
	if err != nil {
		return nil, err
	}
	quotaUsers, err := s.toolStore.QuotaUsersForToday(ctx)
	if err != nil {
		return nil, err
	}
	summary.ToolTotals = toolTotals
	summary.ByTool = byTool
	summary.QuotaUsers = quotaUsers
	summary.Totals.ToolCalls = toolTotals.Calls
	summary.Totals.WebSearchCalls = toolTotals.WebSearchCalls
	summary.Totals.WebExtractCalls = toolTotals.WebExtractCalls
	summary.Totals.ToolContextTokens = toolTotals.ContextTokens
	return summary, nil
}

func normalizeEvent(event *Event) {
	event.Kind = normalizeKind(event.Kind)
	event.Provider = strings.TrimSpace(event.Provider)
	event.ModelID = strings.TrimSpace(event.ModelID)
	event.RunID = strings.TrimSpace(event.RunID)
	event.ErrorType = strings.TrimSpace(event.ErrorType)
	event.ErrorMessage = truncateRunes(strings.TrimSpace(event.ErrorMessage), maxErrorMessageRunes)
	if event.Provider == "" {
		event.Provider = "unknown"
	}
	if event.ModelID == "" {
		event.ModelID = "unknown"
	}
	if event.PromptTokens < 0 {
		event.PromptTokens = 0
	}
	if event.CompletionTokens < 0 {
		event.CompletionTokens = 0
	}
	if event.TotalTokens < 0 {
		event.TotalTokens = 0
	}
	if event.CachedTokens < 0 {
		event.CachedTokens = 0
	}
	if event.ReasoningTokens < 0 {
		event.ReasoningTokens = 0
	}
	if event.DurationMs < 0 {
		event.DurationMs = 0
	}
}

func normalizeToolEvent(event *ToolEvent) {
	event.ToolKey = strings.TrimSpace(event.ToolKey)
	event.RunID = strings.TrimSpace(event.RunID)
	event.CallID = strings.TrimSpace(event.CallID)
	event.ErrorType = strings.TrimSpace(event.ErrorType)
	event.ErrorMessage = truncateRunes(strings.TrimSpace(event.ErrorMessage), maxErrorMessageRunes)
	if event.ToolKey == "" {
		event.ToolKey = "unknown"
	}
	if event.ContextTokens < 0 {
		event.ContextTokens = 0
	}
	if event.DurationMs < 0 {
		event.DurationMs = 0
	}
}

func EstimateTextTokens(value string) int {
	if len(value) <= 0 {
		return 0
	}
	return (len(value) + 2) / 3
}

func normalizeKind(kind string) string {
	switch strings.TrimSpace(kind) {
	case KindRetry:
		return KindRetry
	case KindTitle:
		return KindTitle
	case KindCompression:
		return KindCompression
	case KindToolChain:
		return KindToolChain
	case KindMemoryMaintenance:
		return KindMemoryMaintenance
	default:
		return KindChat
	}
}

func rangeStart(now time.Time, rangeValue string) time.Time {
	switch strings.TrimSpace(rangeValue) {
	case "today":
		y, m, d := now.Date()
		return time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	case "30d":
		return now.AddDate(0, 0, -30)
	default:
		return now.AddDate(0, 0, -7)
	}
}

func ErrorType(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, modelstream.ErrFirstOutputTimeout) {
		return "first_output_timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "model_error"
}

func truncateRunes(value string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}
