package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelstream"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/testutil"
	internaltool "github.com/huoguojun123/EffChat/internal/tool"
	modelusage "github.com/huoguojun123/EffChat/internal/usage"
)

type coordinatedSummaryChatModel struct {
	calls         atomic.Int32
	started       chan int32
	failFirst     chan struct{}
	releaseSecond chan struct{}
}

func (m *coordinatedSummaryChatModel) Generate(context.Context, []*schema.Message, ...einoModel.Option) (*schema.Message, error) {
	return nil, fmt.Errorf("Generate must not be called")
}

func (m *coordinatedSummaryChatModel) Stream(context.Context, []*schema.Message, ...einoModel.Option) (*schema.StreamReader[*schema.Message], error) {
	call := m.calls.Add(1)
	m.started <- call
	switch call {
	case 1:
		<-m.failFirst
		return nil, fmt.Errorf("upstream unavailable")
	case 2:
		<-m.releaseSecond
		return schema.StreamReaderFromArray([]*schema.Message{{Role: schema.Assistant, Content: "in-flight summary"}}), nil
	default:
		return nil, fmt.Errorf("unexpected provider call %d after breaker opened", call)
	}
}

func (m *coordinatedSummaryChatModel) WithTools(_ []*schema.ToolInfo) (einoModel.ToolCallingChatModel, error) {
	return m, nil
}

type cancelThenSucceedSummaryChatModel struct {
	calls   atomic.Int32
	started chan struct{}
}

func (m *cancelThenSucceedSummaryChatModel) Generate(context.Context, []*schema.Message, ...einoModel.Option) (*schema.Message, error) {
	return nil, fmt.Errorf("Generate must not be called")
}

func (m *cancelThenSucceedSummaryChatModel) Stream(ctx context.Context, _ []*schema.Message, _ ...einoModel.Option) (*schema.StreamReader[*schema.Message], error) {
	if m.calls.Add(1) == 1 {
		close(m.started)
		<-ctx.Done()
		return nil, context.Cause(ctx)
	}
	return schema.StreamReaderFromArray([]*schema.Message{{Role: schema.Assistant, Content: "second attempt"}}), nil
}

func (m *cancelThenSucceedSummaryChatModel) WithTools(_ []*schema.ToolInfo) (einoModel.ToolCallingChatModel, error) {
	return m, nil
}

type cancelAfterOutputThenSucceedSummaryChatModel struct {
	calls       atomic.Int32
	firstOutput chan struct{}
}

func (m *cancelAfterOutputThenSucceedSummaryChatModel) Generate(context.Context, []*schema.Message, ...einoModel.Option) (*schema.Message, error) {
	return nil, fmt.Errorf("Generate must not be called")
}

func (m *cancelAfterOutputThenSucceedSummaryChatModel) Stream(ctx context.Context, _ []*schema.Message, _ ...einoModel.Option) (*schema.StreamReader[*schema.Message], error) {
	if m.calls.Add(1) != 1 {
		return schema.StreamReaderFromArray([]*schema.Message{{Role: schema.Assistant, Content: "second attempt"}}), nil
	}
	reader, writer := schema.Pipe[*schema.Message](1)
	go func() {
		defer writer.Close()
		writer.Send(&schema.Message{Role: schema.Assistant, Content: "partial"}, nil)
		// The raw provider wrapper normally marks this chunk while Recv returns.
		// Marking here as well makes the test's "after output" boundary explicit
		// before the caller publishes cancellation.
		modelstream.MarkOutput(ctx)
		close(m.firstOutput)
		<-ctx.Done()
		writer.Send(nil, context.Cause(ctx))
	}()
	return reader, nil
}

func (m *cancelAfterOutputThenSucceedSummaryChatModel) WithTools(_ []*schema.ToolInfo) (einoModel.ToolCallingChatModel, error) {
	return m, nil
}

type serializedSuccessSummaryChatModel struct {
	calls     atomic.Int32
	active    atomic.Int32
	maxActive atomic.Int32
	started   chan int32
	release   chan struct{}
}

func (m *serializedSuccessSummaryChatModel) Generate(context.Context, []*schema.Message, ...einoModel.Option) (*schema.Message, error) {
	return nil, fmt.Errorf("Generate must not be called")
}

func (m *serializedSuccessSummaryChatModel) Stream(ctx context.Context, _ []*schema.Message, _ ...einoModel.Option) (*schema.StreamReader[*schema.Message], error) {
	call := m.calls.Add(1)
	active := m.active.Add(1)
	for {
		current := m.maxActive.Load()
		if active <= current || m.maxActive.CompareAndSwap(current, active) {
			break
		}
	}
	m.started <- call
	select {
	case <-ctx.Done():
		m.active.Add(-1)
		return nil, context.Cause(ctx)
	case <-m.release:
		m.active.Add(-1)
		return schema.StreamReaderFromArray([]*schema.Message{{Role: schema.Assistant, Content: fmt.Sprintf("summary-%d", call)}}), nil
	}
}

func (m *serializedSuccessSummaryChatModel) WithTools(_ []*schema.ToolInfo) (einoModel.ToolCallingChatModel, error) {
	return m, nil
}

type extractSummaryDBFixture struct {
	db        *sql.DB
	taskRuns  *repository.ModelTaskRunRepository
	userID    int64
	sessionID int64
}

func newExtractSummaryDBFixture(t *testing.T, purpose string) extractSummaryDBFixture {
	t.Helper()
	db := testutil.OpenPostgresTestDB(t)
	user := &model.User{
		Username:     fmt.Sprintf("extract-%s-%d", purpose, time.Now().UnixNano()),
		PasswordHash: "test",
		Role:         "user",
		IsActive:     true,
		Permissions:  []byte(`{}`),
		Preferences:  []byte(`{}`),
	}
	if err := repository.NewUserRepository(db).Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	session := &model.Session{
		UserID:        user.ID,
		Title:         "extract " + purpose,
		ModelID:       "gpt-4o",
		Provider:      "openai",
		MessageFormat: "v1",
		Metadata:      []byte(`{}`),
	}
	if err := repository.NewSessionRepository(db).Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return extractSummaryDBFixture{
		db:        db,
		taskRuns:  repository.NewModelTaskRunRepository(db),
		userID:    user.ID,
		sessionID: session.ID,
	}
}

func TestExtractSummarizerStopsQueuedBatchAfterFirstModelFailure(t *testing.T) {
	chatModel := &coordinatedSummaryChatModel{
		started:       make(chan int32, 2),
		failFirst:     make(chan struct{}),
		releaseSecond: make(chan struct{}),
	}
	summarizer := &extractSummarizer{chatModel: chatModel}
	const callers = 8
	start := make(chan struct{})
	results := make(chan struct {
		summary string
		err     error
	}, callers)
	var ready sync.WaitGroup
	ready.Add(callers)

	for i := range callers {
		go func(index int) {
			ready.Done()
			<-start
			summary, err := summarizer.Summarize(t.Context(), "goal", fmt.Sprintf("page-%d", index), "content", "summary")
			results <- struct {
				summary string
				err     error
			}{summary: summary, err: err}
		}(i)
	}
	ready.Wait()
	close(start)

	started := map[int32]bool{
		<-chatModel.started: true,
		<-chatModel.started: true,
	}
	if !started[1] || !started[2] {
		t.Fatalf("initial provider calls = %#v, want calls 1 and 2 in flight", started)
	}
	close(chatModel.failFirst)

	var providerFailures, cooldowns int
	for range callers - 1 {
		select {
		case result := <-results:
			if result.err == nil {
				t.Fatalf("queued Summarize() unexpectedly succeeded with %q", result.summary)
			}
			if internaltool.IsRefinementReason(result.err, internaltool.RefinementCooldown) {
				cooldowns++
			} else {
				providerFailures++
			}
		case <-time.After(2 * time.Second):
			t.Fatal("queued refinement did not observe the opened breaker")
		}
	}
	if got := chatModel.calls.Load(); got != extractSummaryMaxConcurrency {
		t.Fatalf("model Stream calls = %d, want only the %d accepted calls", got, extractSummaryMaxConcurrency)
	}
	if providerFailures != 1 || cooldowns != callers-extractSummaryMaxConcurrency {
		t.Fatalf("failed batch outcomes = provider:%d cooldown:%d", providerFailures, cooldowns)
	}

	// The call accepted before the breaker opened remains independently owned
	// and must be allowed to finish rather than being canceled as collateral.
	close(chatModel.releaseSecond)
	select {
	case result := <-results:
		if result.err != nil || result.summary != "in-flight summary" {
			t.Fatalf("in-flight result = (%q, %v)", result.summary, result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("accepted in-flight refinement did not finish")
	}
}

func TestExtractSummarizerCancellationDoesNotTripBatchBreaker(t *testing.T) {
	serviceCanceled := errors.New("server draining")
	tests := []struct {
		name       string
		newContext func() (context.Context, func())
		want       error
	}{
		{
			name: "user stop",
			newContext: func() (context.Context, func()) {
				return context.WithCancel(t.Context())
			},
			want: context.Canceled,
		},
		{
			name: "service stop",
			newContext: func() (context.Context, func()) {
				ctx, cancel := context.WithCancelCause(t.Context())
				return ctx, func() { cancel(serviceCanceled) }
			},
			want: serviceCanceled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chatModel := &cancelThenSucceedSummaryChatModel{started: make(chan struct{})}
			summarizer := &extractSummarizer{chatModel: chatModel}
			firstCtx, cancel := tt.newContext()
			firstErr := make(chan error, 1)
			go func() {
				_, err := summarizer.Summarize(firstCtx, "goal", "first", "content", "summary")
				firstErr <- err
			}()
			<-chatModel.started
			cancel()
			if err := <-firstErr; !errors.Is(err, tt.want) {
				t.Fatalf("first Summarize() error = %v, want %v", err, tt.want)
			}

			got, err := summarizer.Summarize(t.Context(), "goal", "second", "content", "summary")
			if err != nil {
				t.Fatalf("second Summarize() error = %v", err)
			}
			if got != "second attempt" {
				t.Fatalf("second summary = %q", got)
			}
			if calls := chatModel.calls.Load(); calls != 2 {
				t.Fatalf("model Stream calls = %d, want retry after caller cancellation", calls)
			}
		})
	}
}

func TestExtractSummarizerCancellationAfterFirstOutputDoesNotTripBatchBreaker(t *testing.T) {
	drainCause := errors.New("server draining after refinement output")
	chatModel := &cancelAfterOutputThenSucceedSummaryChatModel{firstOutput: make(chan struct{})}
	summarizer := &extractSummarizer{chatModel: chatModel}
	ctx, cancel := context.WithCancelCause(t.Context())
	firstErr := make(chan error, 1)
	go func() {
		_, err := summarizer.Summarize(ctx, "goal", "first", "content", "summary")
		firstErr <- err
	}()
	<-chatModel.firstOutput
	cancel(drainCause)

	if err := <-firstErr; !errors.Is(err, drainCause) {
		t.Fatalf("first Summarize() error = %v, want %v", err, drainCause)
	}
	got, err := summarizer.Summarize(t.Context(), "goal", "second", "content", "summary")
	if err != nil {
		t.Fatalf("second Summarize() error = %v", err)
	}
	if got != "second attempt" {
		t.Fatalf("second summary = %q", got)
	}
	if calls := chatModel.calls.Load(); calls != 2 {
		t.Fatalf("model Stream calls = %d, want retry after post-output cancellation", calls)
	}
}

func TestExtractSummarizerCancellationDoesNotCreatePersistentCooldown(t *testing.T) {
	db := testutil.OpenPostgresTestDB(t)
	user := &model.User{
		Username:     fmt.Sprintf("extract-cancel-%d", time.Now().UnixNano()),
		PasswordHash: "test",
		Role:         "user",
		IsActive:     true,
		Permissions:  []byte(`{}`),
		Preferences:  []byte(`{}`),
	}
	if err := repository.NewUserRepository(db).Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	session := &model.Session{
		UserID:        user.ID,
		Title:         "extract cancellation",
		ModelID:       "gpt-4o",
		Provider:      "openai",
		MessageFormat: "v1",
		Metadata:      []byte(`{}`),
	}
	if err := repository.NewSessionRepository(db).Create(session); err != nil {
		t.Fatalf("create session: %v", err)
	}

	taskRuns := repository.NewModelTaskRunRepository(db)
	chatModel := &cancelThenSucceedSummaryChatModel{started: make(chan struct{})}
	summarizer := &extractSummarizer{
		chatModel:      chatModel,
		taskRuns:       taskRuns,
		provider:       "openai",
		modelID:        "gpt-4o-mini",
		runtimeVersion: "runtime-v1",
	}
	baseCtx, cancel := context.WithCancel(t.Context())
	ctx := modelusage.WithMeta(baseCtx, modelusage.Meta{
		UserID:    user.ID,
		SessionID: session.ID,
		RunID:     "run-canceled-refinement",
	})
	result := make(chan error, 1)
	go func() {
		_, err := summarizer.Summarize(ctx, "goal", "title", "content", "summary")
		result <- err
	}()
	<-chatModel.started
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Summarize() error = %v, want cancellation", err)
	}

	cooling, err := taskRuns.LatestCooldown(
		t.Context(),
		session.ID,
		user.ID,
		repository.ModelTaskToolExtractSummary,
		repository.ModelTaskSourceTool,
		time.Now(),
	)
	if err != nil {
		t.Fatalf("LatestCooldown() error: %v", err)
	}
	if cooling != nil {
		t.Fatalf("canceled refinement created cooldown: %#v", cooling)
	}
	latest, err := taskRuns.LatestForSession(t.Context(), session.ID, user.ID, repository.ModelTaskToolExtractSummary)
	if err != nil {
		t.Fatalf("LatestForSession() error: %v", err)
	}
	if latest == nil || latest.Status != repository.ModelTaskStatusFailed || latest.ErrorType != "canceled" || latest.RetryAfter != nil {
		t.Fatalf("canceled task run = %#v, want failed/canceled record without retry_after", latest)
	}
}

func TestExtractSummarizerPersistsFirstOutputTimeoutCategory(t *testing.T) {
	fixture := newExtractSummaryDBFixture(t, "first-output-timeout")
	summarizer := &extractSummarizer{
		taskRuns:       fixture.taskRuns,
		provider:       "openai",
		modelID:        "gpt-4o-mini",
		runtimeVersion: "runtime-v1",
	}
	ctx := modelusage.WithMeta(t.Context(), modelusage.Meta{
		UserID:    fixture.userID,
		SessionID: fixture.sessionID,
		RunID:     "run-first-output-timeout",
	})
	summarizer.recordTaskRun(
		ctx,
		time.Now().Add(-time.Second),
		repository.ModelTaskStatusFailed,
		fmt.Errorf("extract summary stream failed: %w", modelstream.ErrFirstOutputTimeout),
	)

	latest, err := fixture.taskRuns.LatestForSession(
		t.Context(),
		fixture.sessionID,
		fixture.userID,
		repository.ModelTaskToolExtractSummary,
	)
	if err != nil {
		t.Fatalf("LatestForSession() error: %v", err)
	}
	if latest == nil ||
		latest.ErrorType != "first_output_timeout" ||
		latest.ErrorMessage != "网页内容提炼等待首个输出超时" ||
		latest.RetryAfter == nil {
		t.Fatalf("first-output timeout task run = %#v", latest)
	}
}

func TestExtractSummaryControlContextPreservesShorterParentDeadline(t *testing.T) {
	parent, cancelParent := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancelParent()
	parentDeadline, _ := parent.Deadline()
	child, cancelChild := extractSummaryControlContext(parent)
	defer cancelChild()
	childDeadline, ok := child.Deadline()
	if !ok || childDeadline.After(parentDeadline) {
		t.Fatalf("control deadline = %v, parent deadline = %v", childDeadline, parentDeadline)
	}
}

func TestExtractSummarizerReleasesSlotsBeforeTaskRunPersistence(t *testing.T) {
	fixture := newExtractSummaryDBFixture(t, "blocked-record")
	chatModel := &serializedSuccessSummaryChatModel{
		started: make(chan int32, extractSummaryMaxConcurrency+1),
		release: make(chan struct{}, extractSummaryMaxConcurrency+1),
	}
	summarizer := &extractSummarizer{
		chatModel:      chatModel,
		taskRuns:       fixture.taskRuns,
		provider:       "openai",
		modelID:        "gpt-4o-mini",
		runtimeVersion: "runtime-v1",
	}
	type callResult struct {
		summary string
		err     error
	}
	results := make(chan callResult, extractSummaryMaxConcurrency+1)
	run := func(ctx context.Context, title string) {
		summary, err := summarizer.Summarize(ctx, "goal", title, "content", "summary")
		results <- callResult{summary: summary, err: err}
	}
	recordingCtx := modelusage.WithMeta(t.Context(), modelusage.Meta{
		UserID:    fixture.userID,
		SessionID: fixture.sessionID,
		RunID:     "run-blocked-record",
	})
	for index := range extractSummaryMaxConcurrency {
		go run(recordingCtx, fmt.Sprintf("page-%d", index))
	}
	for range extractSummaryMaxConcurrency {
		select {
		case <-chatModel.started:
		case <-time.After(2 * time.Second):
			t.Fatal("initial provider calls did not start")
		}
	}

	lockTx, err := fixture.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("begin table lock: %v", err)
	}
	locked := true
	defer func() {
		if locked {
			_ = lockTx.Rollback()
		}
	}()
	if _, err := lockTx.ExecContext(t.Context(), "LOCK TABLE model_task_runs IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatalf("lock model_task_runs: %v", err)
	}
	for range extractSummaryMaxConcurrency {
		chatModel.release <- struct{}{}
	}
	deadline := time.Now().Add(2 * time.Second)
	for chatModel.active.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if active := chatModel.active.Load(); active != 0 {
		t.Fatalf("provider calls remained active: %d", active)
	}

	// This call intentionally has no usage metadata, so it skips the locked
	// cooldown lookup. It can reach the provider only if both prior calls
	// released their slots before their task-run INSERTs blocked.
	go run(t.Context(), "third page")
	select {
	case call := <-chatModel.started:
		if call != extractSummaryMaxConcurrency+1 {
			t.Fatalf("third provider call = %d", call)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("task-run persistence retained both provider slots")
	}
	chatModel.release <- struct{}{}

	if err := lockTx.Rollback(); err != nil {
		t.Fatalf("release table lock: %v", err)
	}
	locked = false
	for range extractSummaryMaxConcurrency + 1 {
		select {
		case result := <-results:
			if result.err != nil || !strings.HasPrefix(result.summary, "summary-") {
				t.Fatalf("Summarize() = (%q, %v)", result.summary, result.err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Summarize() did not finish after persistence lock release")
		}
	}
}

func TestExtractSummarizerBoundsSuccessfulRefinementConcurrency(t *testing.T) {
	chatModel := &serializedSuccessSummaryChatModel{
		started: make(chan int32, extractSummaryMaxConcurrency+1),
		release: make(chan struct{}, extractSummaryMaxConcurrency+1),
	}
	summarizer := &extractSummarizer{chatModel: chatModel}
	const callers = extractSummaryMaxConcurrency + 1
	results := make(chan string, callers)
	errs := make(chan error, callers)
	start := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(callers)

	for i := range callers {
		go func(index int) {
			ready.Done()
			<-start
			got, err := summarizer.Summarize(t.Context(), "goal", fmt.Sprintf("page-%d", index), "content", "summary")
			results <- got
			errs <- err
		}(i)
	}
	ready.Wait()
	close(start)

	started := map[int32]bool{}
	for range extractSummaryMaxConcurrency {
		started[<-chatModel.started] = true
	}
	if len(started) != extractSummaryMaxConcurrency {
		t.Fatalf("initial model calls = %#v", started)
	}
	select {
	case call := <-chatModel.started:
		t.Fatalf("model call %d exceeded concurrency cap before a slot was released", call)
	case <-time.After(25 * time.Millisecond):
	}
	chatModel.release <- struct{}{}
	if call := <-chatModel.started; call != callers {
		t.Fatalf("queued model call = %d, want %d", call, callers)
	}
	for range extractSummaryMaxConcurrency {
		chatModel.release <- struct{}{}
	}

	for range callers {
		if err := <-errs; err != nil {
			t.Fatalf("Summarize() error = %v", err)
		}
		if got := <-results; !strings.HasPrefix(got, "summary-") {
			t.Fatalf("summary = %q", got)
		}
	}
	if calls := chatModel.calls.Load(); calls != callers {
		t.Fatalf("model Stream calls = %d, want %d successful refinements", calls, callers)
	}
	if maxActive := chatModel.maxActive.Load(); maxActive != extractSummaryMaxConcurrency {
		t.Fatalf("max concurrent model Stream calls = %d, want %d", maxActive, extractSummaryMaxConcurrency)
	}
}
