package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/EffChat/internal/modelstream"
	modelusage "github.com/huoguojun123/EffChat/internal/usage"
)

type wrapperOrderModel struct{}

func (*wrapperOrderModel) Generate(context.Context, []*schema.Message, ...einoModel.Option) (*schema.Message, error) {
	return nil, errors.New("Generate must not be called")
}

func (*wrapperOrderModel) Stream(context.Context, []*schema.Message, ...einoModel.Option) (*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray([]*schema.Message{{
		Role:    schema.Assistant,
		Content: "provider output",
	}}), nil
}

func (m *wrapperOrderModel) WithTools([]*schema.ToolInfo) (einoModel.ToolCallingChatModel, error) {
	return m, nil
}

type wrapperOrderUsageStore struct {
	recorded chan struct{}
}

func (s *wrapperOrderUsageStore) Create(context.Context, *modelusage.Event) error {
	select {
	case s.recorded <- struct{}{}:
	default:
	}
	return nil
}

func (*wrapperOrderUsageStore) Aggregate(context.Context, time.Time, time.Time) (*modelusage.Summary, error) {
	return &modelusage.Summary{}, nil
}

func TestWrapUsageModelObservesRawProviderBeforeUsageForwarding(t *testing.T) {
	const firstOutputTimeout = 40 * time.Millisecond
	store := &wrapperOrderUsageStore{recorded: make(chan struct{}, 1)}
	usageService := modelusage.NewService(store)
	a := &EinoAgent{usageService: usageService}
	model := a.wrapUsageModel(&wrapperOrderModel{}, &ChatRequest{
		UserID:    11,
		SessionID: 22,
		Provider:  "openai",
		ModelID:   "gpt-test",
	})

	ctx, cancel, stop := modelstream.WithFirstOutputTimeout(
		t.Context(),
		firstOutputTimeout,
		modelstream.ErrFirstOutputTimeout,
	)
	defer func() {
		stop()
		cancel(nil)
	}()

	reader, err := model.Stream(ctx, nil)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer reader.Close()

	// The usage wrapper drains the raw provider even though this test does not
	// consume its outward-facing reader. The inner observer must therefore see
	// and account for the provider chunk before usage forwarding.
	select {
	case <-store.recorded:
	case <-time.After(time.Second):
		t.Fatal("usage wrapper did not finish draining the raw provider")
	}

	time.Sleep(3 * firstOutputTimeout)
	select {
	case <-ctx.Done():
		t.Fatalf("raw provider output did not disarm first-output guard: %v", context.Cause(ctx))
	default:
	}
}
