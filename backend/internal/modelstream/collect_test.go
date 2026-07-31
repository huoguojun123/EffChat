package modelstream

import (
	"context"
	"errors"
	"testing"
	"time"

	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type streamOnlyModel struct {
	stream         func(context.Context) (*schema.StreamReader[*schema.Message], error)
	generateCalled bool
}

func (m *streamOnlyModel) Generate(context.Context, []*schema.Message, ...einoModel.Option) (*schema.Message, error) {
	m.generateCalled = true
	return nil, errors.New("Generate must not be called")
}

func (m *streamOnlyModel) Stream(ctx context.Context, _ []*schema.Message, _ ...einoModel.Option) (*schema.StreamReader[*schema.Message], error) {
	return m.stream(ctx)
}

func (m *streamOnlyModel) WithTools(_ []*schema.ToolInfo) (einoModel.ToolCallingChatModel, error) {
	return m, nil
}

func TestObserveChatModelRejectsNilReaderBeforeOuterWrappers(t *testing.T) {
	model := &streamOnlyModel{
		stream: func(context.Context) (*schema.StreamReader[*schema.Message], error) {
			return nil, nil
		},
	}

	reader, err := ObserveChatModel(model).Stream(t.Context(), nil)
	if reader != nil || !errors.Is(err, ErrNilReader) {
		t.Fatalf("Stream() = reader:%v err:%v, want nil ErrNilReader", reader, err)
	}
}

func TestCollectUsesStreamAndConcatenatesCompleteResponse(t *testing.T) {
	model := &streamOnlyModel{
		stream: func(context.Context) (*schema.StreamReader[*schema.Message], error) {
			return schema.StreamReaderFromArray([]*schema.Message{
				{Role: schema.Assistant, ReasoningContent: "先分析"},
				{Role: schema.Assistant, Content: "最终"},
				{Role: schema.Assistant, Content: "答案"},
			}), nil
		},
	}

	got, err := Collect(t.Context(), model, nil, time.Second)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if model.generateCalled {
		t.Fatal("Collect must use Stream, not Generate")
	}
	if got.Content != "最终答案" || got.ReasoningContent != "先分析" {
		t.Fatalf("collected message = %#v", got)
	}
}

func TestCollectFirstOutputTimeoutIgnoresMetadataOnlyChunks(t *testing.T) {
	const timeout = 25 * time.Millisecond
	model := &streamOnlyModel{
		stream: func(ctx context.Context) (*schema.StreamReader[*schema.Message], error) {
			reader, writer := schema.Pipe[*schema.Message](1)
			go func() {
				defer writer.Close()
				writer.Send(&schema.Message{
					Role: schema.Assistant,
					ResponseMeta: &schema.ResponseMeta{
						FinishReason: "stop",
					},
				}, nil)
				<-ctx.Done()
				writer.Send(nil, context.Cause(ctx))
			}()
			return reader, nil
		},
	}

	started := time.Now()
	_, err := Collect(t.Context(), model, nil, timeout)
	if !errors.Is(err, ErrFirstOutputTimeout) {
		t.Fatalf("Collect() error = %v, want ErrFirstOutputTimeout", err)
	}
	if elapsed := time.Since(started); elapsed > 10*timeout {
		t.Fatalf("first-output timeout took %s, want close to %s", elapsed, timeout)
	}
}

func TestCollectDisarmsFixedTimeoutAfterFirstMeaningfulOutput(t *testing.T) {
	const timeout = 20 * time.Millisecond
	model := &streamOnlyModel{
		stream: func(context.Context) (*schema.StreamReader[*schema.Message], error) {
			reader, writer := schema.Pipe[*schema.Message](1)
			go func() {
				defer writer.Close()
				writer.Send(&schema.Message{Role: schema.Assistant, Content: "已开始"}, nil)
				time.Sleep(4 * timeout)
				writer.Send(&schema.Message{Role: schema.Assistant, Content: "并完成"}, nil)
			}()
			return reader, nil
		},
	}

	got, err := Collect(t.Context(), model, nil, timeout)
	if err != nil {
		t.Fatalf("Collect() error after valid first output = %v", err)
	}
	if got.Content != "已开始并完成" {
		t.Fatalf("content = %q", got.Content)
	}
}

func TestCollectDisarmsEnclosingGateWhenLocalTimeoutIsDisabled(t *testing.T) {
	const timeout = 20 * time.Millisecond
	timeoutCause := errors.New("enclosing first output timeout")
	parent, cancelParent, stopParent := WithDeferredFirstOutputTimeout(t.Context(), timeout, timeoutCause)
	defer func() {
		stopParent()
		cancelParent(nil)
	}()

	model := &streamOnlyModel{
		stream: func(context.Context) (*schema.StreamReader[*schema.Message], error) {
			reader, writer := schema.Pipe[*schema.Message](1)
			go func() {
				defer writer.Close()
				writer.Send(&schema.Message{Role: schema.Assistant, Content: "已开始"}, nil)
				time.Sleep(4 * timeout)
				writer.Send(&schema.Message{Role: schema.Assistant, Content: "并完成"}, nil)
			}()
			return reader, nil
		},
	}

	got, err := Collect(parent, model, nil, 0)
	if err != nil {
		t.Fatalf("Collect() error after inherited first output = %v", err)
	}
	if got.Content != "已开始并完成" {
		t.Fatalf("content = %q", got.Content)
	}
}

func TestCollectPrefersFirstOutputCauseWhenTransportClosesOnCancellation(t *testing.T) {
	const timeout = 20 * time.Millisecond
	model := &streamOnlyModel{
		stream: func(ctx context.Context) (*schema.StreamReader[*schema.Message], error) {
			<-ctx.Done()
			return nil, errors.New("transport closed")
		},
	}

	_, err := Collect(t.Context(), model, nil, timeout)
	if !errors.Is(err, ErrFirstOutputTimeout) {
		t.Fatalf("Collect() error = %v, want ErrFirstOutputTimeout", err)
	}
}

func TestCollectStillHonorsParentCancellationAfterOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	firstChunkSent := make(chan struct{})
	model := &streamOnlyModel{
		stream: func(ctx context.Context) (*schema.StreamReader[*schema.Message], error) {
			reader, writer := schema.Pipe[*schema.Message](1)
			go func() {
				defer writer.Close()
				writer.Send(&schema.Message{Role: schema.Assistant, Content: "partial"}, nil)
				close(firstChunkSent)
				<-ctx.Done()
				writer.Send(nil, context.Cause(ctx))
			}()
			return reader, nil
		},
	}

	result := make(chan error, 1)
	go func() {
		_, err := Collect(ctx, model, nil, time.Second)
		result <- err
	}()
	<-firstChunkSent
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Collect() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Collect did not stop after parent cancellation")
	}
}

func TestCollectRejectsStreamWithoutMeaningfulOutput(t *testing.T) {
	model := &streamOnlyModel{
		stream: func(context.Context) (*schema.StreamReader[*schema.Message], error) {
			return schema.StreamReaderFromArray([]*schema.Message{
				{Role: schema.Assistant},
				{Role: schema.Assistant, Content: " \n\t"},
			}), nil
		},
	}

	_, err := Collect(t.Context(), model, nil, time.Second)
	if !errors.Is(err, ErrEmptyOutput) {
		t.Fatalf("Collect() error = %v, want ErrEmptyOutput", err)
	}
}

func TestHasMeaningfulOutputRequiresTextReasoningOrUsableToolCall(t *testing.T) {
	index := 0
	tests := []struct {
		name string
		msg  *schema.Message
		want bool
	}{
		{name: "nil", msg: nil},
		{name: "role only", msg: &schema.Message{Role: schema.Assistant}},
		{name: "whitespace", msg: &schema.Message{Role: schema.Assistant, Content: " \n"}},
		{name: "finish metadata", msg: &schema.Message{Role: schema.Assistant, ResponseMeta: &schema.ResponseMeta{FinishReason: "stop"}}},
		{name: "content", msg: &schema.Message{Role: schema.Assistant, Content: "answer"}, want: true},
		{name: "reasoning", msg: &schema.Message{Role: schema.Assistant, ReasoningContent: "thinking"}, want: true},
		{name: "tool index only", msg: &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{Index: &index}}}},
		{name: "tool id only", msg: &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "call_1"}}}},
		{name: "tool name", msg: &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{Function: schema.FunctionCall{Name: "web_search"}}}}, want: true},
		{name: "tool arguments without name", msg: &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{Function: schema.FunctionCall{Arguments: `{"q":"EffChat"}`}}}}},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if got := HasMeaningfulOutput(testCase.msg); got != testCase.want {
				t.Fatalf("HasMeaningfulOutput(%#v) = %t, want %t", testCase.msg, got, testCase.want)
			}
		})
	}
}

func TestConsumeReturnsConcatenatedPartialMessageOnStreamError(t *testing.T) {
	stream, writer := schema.Pipe[*schema.Message](1)
	streamErr := errors.New("transport interrupted")
	go func() {
		defer writer.Close()
		writer.Send(&schema.Message{Role: schema.Assistant, ReasoningContent: "先分析"}, nil)
		writer.Send(&schema.Message{Role: schema.Assistant, Content: "部分回答"}, nil)
		writer.Send(nil, streamErr)
	}()

	got, err := Consume(t.Context(), stream, nil)
	if !errors.Is(err, streamErr) {
		t.Fatalf("Consume() error = %v, want %v", err, streamErr)
	}
	if got == nil || got.Content != "部分回答" || got.ReasoningContent != "先分析" {
		t.Fatalf("partial message = %#v", got)
	}
}

func TestLateChildOutputDoesNotDisarmParentAfterChildTimeout(t *testing.T) {
	parentCause := errors.New("parent first output timeout")
	parent, cancelParent, stopParent := WithDeferredFirstOutputTimeout(t.Context(), 60*time.Millisecond, parentCause)
	defer func() {
		stopParent()
		cancelParent(nil)
	}()
	childCause := errors.New("child first output timeout")
	child, cancelChild, stopChild := WithDeferredFirstOutputTimeout(parent, 15*time.Millisecond, childCause)
	defer func() {
		stopChild()
		cancelChild(nil)
	}()

	ArmFirstOutputTimeout(child)
	select {
	case <-child.Done():
	case <-time.After(time.Second):
		t.Fatal("child first-output timeout did not fire")
	}
	if !errors.Is(context.Cause(child), childCause) {
		t.Fatalf("child cause = %v, want %v", context.Cause(child), childCause)
	}

	ObserveMessage(child, &schema.Message{Role: schema.Assistant, Content: "late output"})
	select {
	case <-parent.Done():
	case <-time.After(time.Second):
		t.Fatal("late child output incorrectly disarmed the parent gate")
	}
	if !errors.Is(context.Cause(parent), parentCause) {
		t.Fatalf("parent cause = %v, want %v", context.Cause(parent), parentCause)
	}
}

func TestObserveChatModelDisarmsGateWhenRetryLayerReadsRawChunk(t *testing.T) {
	const timeout = 20 * time.Millisecond
	rawChunkSent := make(chan struct{})
	model := &streamOnlyModel{
		stream: func(context.Context) (*schema.StreamReader[*schema.Message], error) {
			reader, writer := schema.Pipe[*schema.Message](1)
			go func() {
				defer writer.Close()
				writer.Send(&schema.Message{Role: schema.Assistant, Content: "raw output"}, nil)
				close(rawChunkSent)
			}()
			return reader, nil
		},
	}
	ctx, cancel, stop := WithFirstOutputTimeout(t.Context(), timeout, ErrFirstOutputTimeout)
	defer func() {
		stop()
		cancel(nil)
	}()

	reader, err := ObserveChatModel(model).Stream(ctx, nil)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer reader.Close()
	<-rawChunkSent
	if _, err := reader.Recv(); err != nil {
		t.Fatalf("Recv() error = %v", err)
	}
	time.Sleep(4 * timeout)
	select {
	case <-ctx.Done():
		t.Fatalf("raw model output did not disarm gate: %v", context.Cause(ctx))
	default:
	}
}
