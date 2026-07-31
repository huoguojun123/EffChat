package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
)

const defaultQuotaOperationTimeout = 5 * time.Second

type QuotaService struct {
	repo             quotaStore
	operationTimeout time.Duration
}

type quotaStore interface {
	LimitsForUser(ctx context.Context, userID int64) (repository.UserQuotaLimits, error)
	UsageForToday(ctx context.Context, userID int64) (repository.QuotaUsage, error)
	ReserveToolCall(ctx context.Context, input repository.ToolCallReservationInput) (repository.ToolCallReservation, error)
	ReserveChatRun(ctx context.Context, input repository.ChatRunReservationInput) (repository.ChatRunReservation, error)
	AdmitChatMessage(ctx context.Context, input repository.ChatRunReservationInput, message *model.Message) (repository.ChatRunAdmission, error)
	AdmitRetryChatRun(ctx context.Context, input repository.ChatRunReservationInput, targetMessageID int64) (repository.ChatRunAdmission, error)
	AdmitEditedRetryChatRun(ctx context.Context, input repository.ChatRunReservationInput, targetMessageID int64, replacement *model.Message) (repository.ChatRunAdmission, error)
	GetChatRun(ctx context.Context, runID string) (repository.ChatRunRecord, error)
	ReserveOCRSubmission(ctx context.Context, fileID, userID int64, pageCount int) (bool, error)
}

func NewQuotaService(repo quotaStore) *QuotaService {
	return newQuotaService(repo, defaultQuotaOperationTimeout)
}

func newQuotaService(repo quotaStore, operationTimeout time.Duration) *QuotaService {
	if operationTimeout <= 0 {
		operationTimeout = defaultQuotaOperationTimeout
	}
	return &QuotaService{repo: repo, operationTimeout: operationTimeout}
}

// operationContext bounds one quota control-plane operation. It is deliberately
// unrelated to model execution or run liveness: an admitted run remains owned
// by its durable status until terminal transition, while a blocked quota query
// or transaction must return control to the caller promptly.
func (s *QuotaService) operationContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, s.operationTimeout)
}

type QuotaCheck struct {
}

type QuotaError struct {
	Code    string    `json:"code"`
	Message string    `json:"message"`
	Limit   int64     `json:"limit"`
	Used    int64     `json:"used"`
	ResetAt time.Time `json:"reset_at,omitempty"`
}

type ToolCallQuotaInput struct {
	UserID    int64
	SessionID int64
	RunID     string
	CallID    string
	ToolName  string
}

type ToolCallQuotaReservation struct {
	ID        int64
	CreatedAt time.Time
}

type ChatRunQuotaInput struct {
	UserID          int64
	AuthVersion     int
	SessionID       int64
	RunID           string
	Kind            string
	Intent          RunIntent
	RuntimeSnapshot json.RawMessage
	ReserveMessage  bool
	AcceptedAt      time.Time
	// ExpiresAt is terminal replay/retention metadata. A running reservation
	// owns quota by status until an atomic completed/failed/canceled transition;
	// admission must never treat this timestamp as a run liveness lease.
	ExpiresAt time.Time
}

type ChatRunAdmission struct {
	Record   repository.ChatRunRecord
	Message  *model.Message
	Existing bool
}

func (e *QuotaError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (s *QuotaService) CheckBeforeRun(ctx context.Context, userID int64, check QuotaCheck) error {
	if s == nil || s.repo == nil {
		return nil
	}
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	limits, err := s.repo.LimitsForUser(ctx, userID)
	if err != nil {
		return err
	}
	if limits.DailyTokenLimit <= 0 {
		return nil
	}
	usage, err := s.repo.UsageForToday(ctx, userID)
	if err != nil {
		return err
	}
	if limits.DailyTokenLimit > 0 && usage.DailyTokens >= int64(limits.DailyTokenLimit) {
		return &QuotaError{
			Code:    "daily_token_limit_exceeded",
			Message: fmt.Sprintf("今日 token 用量已达近似上限（%d）", limits.DailyTokenLimit),
			Limit:   int64(limits.DailyTokenLimit),
			Used:    usage.DailyTokens,
			ResetAt: usage.ResetAt,
		}
	}
	return nil
}

func (s *QuotaService) CheckBeforeOCR(ctx context.Context, userID int64, pages int) error {
	return s.checkOCRQuota(ctx, userID, pages, true)
}

func (s *QuotaService) CheckOCRFile(ctx context.Context, userID int64) error {
	return s.checkOCRQuota(ctx, userID, 0, true)
}

func (s *QuotaService) CheckOCRPages(ctx context.Context, userID int64, pages int) error {
	return s.checkOCRQuota(ctx, userID, pages, false)
}

func (s *QuotaService) checkOCRQuota(ctx context.Context, userID int64, pages int, checkFileLimit bool) error {
	if s == nil || s.repo == nil {
		return nil
	}
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if pages < 0 {
		pages = 0
	}
	limits, err := s.repo.LimitsForUser(ctx, userID)
	if err != nil {
		return err
	}
	if limits.DailyOCRFileLimit <= 0 && limits.DailyOCRPageLimit <= 0 {
		return nil
	}
	usage, err := s.repo.UsageForToday(ctx, userID)
	if err != nil {
		return err
	}
	if checkFileLimit && limits.DailyOCRFileLimit > 0 && usage.DailyOCRFiles >= int64(limits.DailyOCRFileLimit) {
		return &QuotaError{
			Code:    "daily_ocr_file_limit_exceeded",
			Message: fmt.Sprintf("今日 OCR 文件数已达上限（%d）", limits.DailyOCRFileLimit),
			Limit:   int64(limits.DailyOCRFileLimit),
			Used:    usage.DailyOCRFiles,
			ResetAt: usage.ResetAt,
		}
	}
	if limits.DailyOCRPageLimit > 0 && usage.DailyOCRPages+int64(pages) > int64(limits.DailyOCRPageLimit) {
		return &QuotaError{
			Code:    "daily_ocr_page_limit_exceeded",
			Message: fmt.Sprintf("今日 OCR 页数将超过上限（%d）", limits.DailyOCRPageLimit),
			Limit:   int64(limits.DailyOCRPageLimit),
			Used:    usage.DailyOCRPages,
			ResetAt: usage.ResetAt,
		}
	}
	return nil
}

func (s *QuotaService) ReserveToolCall(ctx context.Context, input ToolCallQuotaInput) (ToolCallQuotaReservation, error) {
	if s == nil || s.repo == nil || input.UserID <= 0 {
		return ToolCallQuotaReservation{}, nil
	}
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	reservation, err := s.repo.ReserveToolCall(ctx, repository.ToolCallReservationInput{
		UserID:    input.UserID,
		SessionID: input.SessionID,
		RunID:     input.RunID,
		CallID:    input.CallID,
		ToolName:  input.ToolName,
	})
	if err != nil {
		return ToolCallQuotaReservation{}, mapRepositoryQuotaError(err)
	}
	return ToolCallQuotaReservation{ID: reservation.ID, CreatedAt: reservation.CreatedAt}, nil
}

func (s *QuotaService) ReserveChatRun(ctx context.Context, input ChatRunQuotaInput) (repository.ChatRunRecord, error) {
	if s == nil || s.repo == nil {
		return repository.ChatRunRecord{}, nil
	}
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	reservation, err := s.repo.ReserveChatRun(ctx, repositoryChatRunInput(input))
	return reservation.Record, mapRepositoryQuotaError(err)
}

func (s *QuotaService) AdmitChatMessage(ctx context.Context, input ChatRunQuotaInput, message *model.Message) (ChatRunAdmission, error) {
	if s == nil || s.repo == nil {
		return ChatRunAdmission{}, fmt.Errorf("chat run admission is unavailable")
	}
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	admission, err := s.repo.AdmitChatMessage(ctx, repositoryChatRunInput(input), message)
	if err != nil {
		return ChatRunAdmission{}, mapRepositoryQuotaError(err)
	}
	return ChatRunAdmission{Record: admission.Record, Message: admission.Message, Existing: admission.Existing}, nil
}

func (s *QuotaService) AdmitRetryChatRun(ctx context.Context, input ChatRunQuotaInput, targetMessageID int64) (ChatRunAdmission, error) {
	if s == nil || s.repo == nil {
		return ChatRunAdmission{}, fmt.Errorf("retry admission is unavailable")
	}
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	admission, err := s.repo.AdmitRetryChatRun(ctx, repositoryChatRunInput(input), targetMessageID)
	if err != nil {
		return ChatRunAdmission{}, mapRepositoryQuotaError(err)
	}
	return ChatRunAdmission{Record: admission.Record, Message: admission.Message, Existing: admission.Existing}, nil
}

func (s *QuotaService) AdmitEditedRetryChatRun(ctx context.Context, input ChatRunQuotaInput, targetMessageID int64, replacement *model.Message) (ChatRunAdmission, error) {
	if s == nil || s.repo == nil {
		return ChatRunAdmission{}, fmt.Errorf("edited retry admission is unavailable")
	}
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	admission, err := s.repo.AdmitEditedRetryChatRun(ctx, repositoryChatRunInput(input), targetMessageID, replacement)
	if err != nil {
		return ChatRunAdmission{}, mapRepositoryQuotaError(err)
	}
	return ChatRunAdmission{Record: admission.Record, Message: admission.Message, Existing: admission.Existing}, nil
}

func (s *QuotaService) MatchChatRun(ctx context.Context, input ChatRunQuotaInput) (repository.ChatRunRecord, error) {
	if s == nil || s.repo == nil || input.RunID == "" {
		return repository.ChatRunRecord{}, repository.ErrNotFound
	}
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	record, err := s.repo.GetChatRun(ctx, input.RunID)
	if err != nil {
		return repository.ChatRunRecord{}, err
	}
	if record.UserID != input.UserID || record.SessionID != input.SessionID || record.Kind != input.Kind {
		return repository.ChatRunRecord{}, ErrRunIDConflict
	}
	if record.IntentVersion == 0 && record.Operation != input.Intent.Operation {
		return repository.ChatRunRecord{}, ErrRunIDConflict
	}
	if record.IntentVersion > 0 && (record.Operation != input.Intent.Operation ||
		record.IntentVersion != input.Intent.Version ||
		record.IntentHash != input.Intent.Hash ||
		record.RetryTargetMessageID != input.Intent.RetryTargetMessageID) {
		return repository.ChatRunRecord{}, ErrRunIDConflict
	}
	return record, nil
}

func (s *QuotaService) GetChatRunForSession(ctx context.Context, userID, sessionID int64, runID string) (repository.ChatRunRecord, error) {
	if s == nil || s.repo == nil || runID == "" {
		return repository.ChatRunRecord{}, repository.ErrNotFound
	}
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	record, err := s.repo.GetChatRun(ctx, runID)
	if err != nil {
		return repository.ChatRunRecord{}, err
	}
	if record.UserID != userID || record.SessionID != sessionID {
		return repository.ChatRunRecord{}, repository.ErrNotFound
	}
	return record, nil
}

func repositoryChatRunInput(input ChatRunQuotaInput) repository.ChatRunReservationInput {
	return repository.ChatRunReservationInput{
		UserID:               input.UserID,
		AuthVersion:          input.AuthVersion,
		SessionID:            input.SessionID,
		RunID:                input.RunID,
		Kind:                 input.Kind,
		Operation:            input.Intent.Operation,
		IntentVersion:        input.Intent.Version,
		IntentHash:           input.Intent.Hash,
		RetryTargetMessageID: input.Intent.RetryTargetMessageID,
		RuntimeSnapshot:      input.RuntimeSnapshot,
		ReserveMessage:       input.ReserveMessage,
		AcceptedAt:           input.AcceptedAt,
		ExpiresAt:            input.ExpiresAt,
	}
}

func (s *QuotaService) ReserveOCRSubmission(ctx context.Context, fileID, userID int64, pageCount int) (bool, error) {
	if s == nil || s.repo == nil {
		return true, nil
	}
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	reserved, err := s.repo.ReserveOCRSubmission(ctx, fileID, userID, pageCount)
	return reserved, mapRepositoryQuotaError(err)
}

func mapRepositoryQuotaError(err error) error {
	if err == nil {
		return nil
	}
	var quotaErr *repository.ToolQuotaExceeded
	if errors.As(err, &quotaErr) {
		return &QuotaError{
			Code:    quotaErr.Code,
			Message: quotaMessage(quotaErr.Code, quotaErr.Limit),
			Limit:   quotaErr.Limit,
			Used:    quotaErr.Used,
			ResetAt: quotaErr.ResetAt,
		}
	}
	if errors.Is(err, repository.ErrAccountStateChanged) {
		return ErrAuthenticationUnavailable
	}
	if errors.Is(err, repository.ErrChatRunIntentConflict) {
		return ErrRunIDConflict
	}
	if errors.Is(err, repository.ErrChatRunTerminal) {
		return ErrRunTerminal
	}
	return err
}

func quotaMessage(code string, limit int64) string {
	switch code {
	case "daily_tool_call_limit_exceeded":
		return fmt.Sprintf("今日工具调用次数已达上限（%d）", limit)
	case "daily_web_search_limit_exceeded":
		return fmt.Sprintf("今日联网搜索次数已达上限（%d）", limit)
	case "daily_web_extract_limit_exceeded":
		return fmt.Sprintf("今日网页提取次数已达上限（%d）", limit)
	case "concurrent_run_limit_exceeded":
		return fmt.Sprintf("并发运行数已达上限（%d）", limit)
	case "daily_message_limit_exceeded":
		return fmt.Sprintf("今日消息数已达上限（%d）", limit)
	case "daily_ocr_file_limit_exceeded":
		return fmt.Sprintf("今日 OCR 文件数已达上限（%d）", limit)
	case "daily_ocr_page_limit_exceeded":
		return fmt.Sprintf("今日 OCR 页数将超过上限（%d）", limit)
	default:
		return "今日工具调用次数已达上限"
	}
}
