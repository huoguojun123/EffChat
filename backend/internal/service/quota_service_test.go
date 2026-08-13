package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
)

type fakeQuotaStore struct {
	limits         repository.UserQuotaLimits
	usage          repository.QuotaUsage
	err            error
	reservationErr error
	runRecord      repository.ChatRunRecord
	runErr         error
}

type blockingQuotaStore struct {
	fakeQuotaStore
	operation string
	started   chan struct{}
}

func (f blockingQuotaStore) LimitsForUser(ctx context.Context, userID int64) (repository.UserQuotaLimits, error) {
	if f.operation != "limits" {
		return f.fakeQuotaStore.LimitsForUser(ctx, userID)
	}
	close(f.started)
	<-ctx.Done()
	return repository.UserQuotaLimits{}, ctx.Err()
}

func (f blockingQuotaStore) AdmitChatMessage(ctx context.Context, input repository.ChatRunReservationInput, message *model.Message) (repository.ChatRunAdmission, error) {
	if f.operation != "admission" {
		return f.fakeQuotaStore.AdmitChatMessage(ctx, input, message)
	}
	close(f.started)
	<-ctx.Done()
	return repository.ChatRunAdmission{}, ctx.Err()
}

func (f fakeQuotaStore) LimitsForUser(ctx context.Context, userID int64) (repository.UserQuotaLimits, error) {
	if f.err != nil {
		return repository.UserQuotaLimits{}, f.err
	}
	return f.limits, nil
}

func (f fakeQuotaStore) UsageForToday(ctx context.Context, userID int64) (repository.QuotaUsage, error) {
	if f.err != nil {
		return repository.QuotaUsage{}, f.err
	}
	return f.usage, nil
}

func (f fakeQuotaStore) ReserveToolCall(ctx context.Context, input repository.ToolCallReservationInput) (repository.ToolCallReservation, error) {
	if f.reservationErr != nil {
		return repository.ToolCallReservation{}, f.reservationErr
	}
	return repository.ToolCallReservation{ID: 42, CreatedAt: time.Now()}, nil
}

func (f fakeQuotaStore) ReserveChatRun(ctx context.Context, input repository.ChatRunReservationInput) (repository.ChatRunReservation, error) {
	return repository.ChatRunReservation{RunID: input.RunID, Record: repository.ChatRunRecord{RunID: input.RunID}}, f.reservationErr
}

func (f fakeQuotaStore) AdmitChatMessage(_ context.Context, input repository.ChatRunReservationInput, message *model.Message) (repository.ChatRunAdmission, error) {
	return repository.ChatRunAdmission{Record: repository.ChatRunRecord{RunID: input.RunID}, Message: message}, f.reservationErr
}

func (f fakeQuotaStore) AdmitRetryChatRun(_ context.Context, input repository.ChatRunReservationInput, _ int64) (repository.ChatRunAdmission, error) {
	return repository.ChatRunAdmission{Record: repository.ChatRunRecord{RunID: input.RunID}}, f.reservationErr
}

func (f fakeQuotaStore) AdmitEditedRetryChatRun(_ context.Context, input repository.ChatRunReservationInput, _ int64, message *model.Message) (repository.ChatRunAdmission, error) {
	return repository.ChatRunAdmission{Record: repository.ChatRunRecord{RunID: input.RunID}, Message: message}, f.reservationErr
}

func (f fakeQuotaStore) GetChatRun(context.Context, string) (repository.ChatRunRecord, error) {
	if f.runErr != nil {
		return repository.ChatRunRecord{}, f.runErr
	}
	if f.runRecord.RunID == "" {
		return repository.ChatRunRecord{}, repository.ErrNotFound
	}
	return f.runRecord, nil
}

func (f fakeQuotaStore) ReserveOCRSubmission(context.Context, int64, int64, int64, int) (bool, error) {
	return true, f.reservationErr
}

func TestQuotaService_CheckBeforeRunBlocksDailyTokens(t *testing.T) {
	svc := NewQuotaService(fakeQuotaStore{
		limits: repository.UserQuotaLimits{DailyTokenLimit: 1000},
		usage:  repository.QuotaUsage{DailyTokens: 1000, ResetAt: time.Now().Add(time.Hour)},
	})

	err := svc.CheckBeforeRun(context.Background(), 1, QuotaCheck{})
	var quotaErr *QuotaError
	if !errors.As(err, &quotaErr) {
		t.Fatalf("expected QuotaError, got %v", err)
	}
	if quotaErr.Code != "daily_token_limit_exceeded" {
		t.Fatalf("code = %q", quotaErr.Code)
	}
}

func TestQuotaService_CheckOCRPagesDoesNotRecheckFileLimit(t *testing.T) {
	reset := time.Now().Add(time.Hour)
	svc := NewQuotaService(fakeQuotaStore{
		limits: repository.UserQuotaLimits{DailyOCRFileLimit: 1, DailyOCRPageLimit: 10},
		usage:  repository.QuotaUsage{DailyOCRFiles: 1, DailyOCRPages: 3, ResetAt: reset},
	})

	if err := svc.CheckOCRPages(context.Background(), 1, 3); err != nil {
		t.Fatalf("CheckOCRPages() error = %v, want nil for the file already queued today", err)
	}

	err := svc.CheckOCRPages(context.Background(), 1, 8)
	var quotaErr *QuotaError
	if !errors.As(err, &quotaErr) {
		t.Fatalf("expected page QuotaError, got %v", err)
	}
	if quotaErr.Code != "daily_ocr_page_limit_exceeded" {
		t.Fatalf("code = %q", quotaErr.Code)
	}
}

func TestQuotaService_CheckOCRFileBlocksExistingUsage(t *testing.T) {
	svc := NewQuotaService(fakeQuotaStore{
		limits: repository.UserQuotaLimits{DailyOCRFileLimit: 1},
		usage:  repository.QuotaUsage{DailyOCRFiles: 1, ResetAt: time.Now().Add(time.Hour)},
	})

	err := svc.CheckOCRFile(context.Background(), 1)
	var quotaErr *QuotaError
	if !errors.As(err, &quotaErr) {
		t.Fatalf("expected file QuotaError, got %v", err)
	}
	if quotaErr.Code != "daily_ocr_file_limit_exceeded" {
		t.Fatalf("code = %q", quotaErr.Code)
	}
}

func TestQuotaService_ReserveToolCallMapsRepositoryQuotaError(t *testing.T) {
	svc := NewQuotaService(fakeQuotaStore{
		reservationErr: &repository.ToolQuotaExceeded{
			Code:    "daily_web_search_limit_exceeded",
			Limit:   1,
			Used:    1,
			ResetAt: time.Now().Add(time.Hour),
		},
	})

	_, err := svc.ReserveToolCall(context.Background(), ToolCallQuotaInput{
		UserID:   1,
		ToolName: "web_search",
	})
	var quotaErr *QuotaError
	if !errors.As(err, &quotaErr) {
		t.Fatalf("expected QuotaError, got %v", err)
	}
	if quotaErr.Code != "daily_web_search_limit_exceeded" || quotaErr.Limit != 1 || quotaErr.Used != 1 {
		t.Fatalf("code = %q", quotaErr.Code)
	}
}

func TestQuotaServiceMatchChatRunUsesVersionedIntent(t *testing.T) {
	intent := BuildRetryRunIntent(42)
	record := repository.ChatRunRecord{
		RunID: "run", UserID: 1, SessionID: 2, Kind: RunKindChat,
		Operation: intent.Operation, IntentVersion: intent.Version, IntentHash: intent.Hash, RetryTargetMessageID: 42,
	}
	svc := NewQuotaService(fakeQuotaStore{runRecord: record})
	input := ChatRunQuotaInput{UserID: 1, SessionID: 2, RunID: "run", Kind: RunKindChat, Intent: intent}
	if _, err := svc.MatchChatRun(context.Background(), input); err != nil {
		t.Fatalf("matching intent: %v", err)
	}
	input.Intent = BuildRetryRunIntent(43)
	if _, err := svc.MatchChatRun(context.Background(), input); !errors.Is(err, ErrRunIDConflict) {
		t.Fatalf("changed intent error = %v", err)
	}

	legacy := record
	legacy.Operation = RunOperationSend
	legacy.IntentVersion = 0
	legacy.IntentHash = ""
	legacy.RetryTargetMessageID = 0
	svc = NewQuotaService(fakeQuotaStore{runRecord: legacy})
	if _, err := svc.MatchChatRun(context.Background(), ChatRunQuotaInput{UserID: 1, SessionID: 2, RunID: "run", Kind: RunKindChat, Intent: intent}); !errors.Is(err, ErrRunIDConflict) {
		t.Fatalf("legacy cross-operation error = %v", err)
	}
	if _, err := svc.MatchChatRun(context.Background(), ChatRunQuotaInput{
		UserID: 1, SessionID: 2, RunID: "run", Kind: RunKindChat,
		Intent: RunIntent{Operation: RunOperationSend, Version: ChatRunIntentVersion, Hash: "v1:new-send"},
	}); err != nil {
		t.Fatalf("legacy same-operation compatibility: %v", err)
	}
}

func TestQuotaServiceGetChatRunForSessionScopesTheDurableRecord(t *testing.T) {
	record := repository.ChatRunRecord{RunID: "run-status", UserID: 1, SessionID: 2, Status: "failed"}
	svc := NewQuotaService(fakeQuotaStore{runRecord: record})

	got, err := svc.GetChatRunForSession(context.Background(), 1, 2, "run-status")
	if err != nil || got.RunID != record.RunID {
		t.Fatalf("scoped record = %+v err=%v", got, err)
	}
	if _, err := svc.GetChatRunForSession(context.Background(), 2, 2, "run-status"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("wrong user error = %v", err)
	}
	if _, err := svc.GetChatRunForSession(context.Background(), 1, 3, "run-status"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("wrong session error = %v", err)
	}
}

func TestQuotaServiceBoundsControlPlaneReadsIndependently(t *testing.T) {
	started := make(chan struct{})
	svc := newQuotaService(blockingQuotaStore{
		operation: "limits",
		started:   started,
	}, 20*time.Millisecond)

	start := time.Now()
	err := svc.CheckBeforeRun(context.Background(), 1, QuotaCheck{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("CheckBeforeRun() error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("control-plane read took %s, want bounded exit", elapsed)
	}
	select {
	case <-started:
	default:
		t.Fatal("quota store was not called")
	}
}

func TestQuotaServicePreservesShorterCallerDeadline(t *testing.T) {
	started := make(chan struct{})
	svc := newQuotaService(blockingQuotaStore{
		operation: "limits",
		started:   started,
	}, time.Second)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := svc.CheckBeforeRun(ctx, 1, QuotaCheck{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("CheckBeforeRun() error = %v, want caller deadline", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("quota operation ignored shorter caller deadline: %s", elapsed)
	}
	select {
	case <-started:
	default:
		t.Fatal("quota store was not called")
	}
}

func TestQuotaServiceBoundsChatAdmissionIndependently(t *testing.T) {
	started := make(chan struct{})
	svc := newQuotaService(blockingQuotaStore{
		operation: "admission",
		started:   started,
	}, 20*time.Millisecond)

	start := time.Now()
	_, err := svc.AdmitChatMessage(context.Background(), ChatRunQuotaInput{
		UserID: 1, SessionID: 2, RunID: "bounded-admission",
	}, &model.Message{SessionID: 2})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("AdmitChatMessage() error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("chat admission took %s, want bounded exit", elapsed)
	}
	select {
	case <-started:
	default:
		t.Fatal("admission store was not called")
	}
}
