// Package modelstream owns the common contract for consuming Eino model
// streams outside the full chat Agent runtime.
package modelstream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

var (
	// ErrFirstOutputTimeout is returned only when a model has not produced
	// meaningful output before the startup budget expires. It unwraps to
	// context.DeadlineExceeded so existing timeout classification still works.
	ErrFirstOutputTimeout = &firstOutputTimeoutError{}
	ErrEmptyOutput        = errors.New("model stream completed without meaningful output")
	ErrNilReader          = errors.New("model stream reader is nil")
)

type firstOutputTimeoutError struct{}

func (*firstOutputTimeoutError) Error() string {
	return "model stream first output timed out"
}

func (*firstOutputTimeoutError) Unwrap() error {
	return context.DeadlineExceeded
}

type outputGateContextKey struct{}

// outputGate is deliberately independent from context deadlines. A regular
// context.WithTimeout keeps canceling Recv after output has started, which
// turns a startup guard into an accidental total-generation limit.
//
// Gates form a linked list through parent. This matters when a nested model
// call is the work that owns its enclosing run: one meaningful chunk must
// disarm every guard in that ownership chain. Sibling/background work that
// does not prove progress for its parent must use IsolateFirstOutputTimeout.
type outputGate struct {
	mu           sync.Mutex
	cancel       context.CancelCauseFunc
	timeoutCause error
	timer        *time.Timer
	parent       *outputGate
	timeout      time.Duration
	armed        bool
	observed     bool
	fired        bool
	stopped      bool
}

type isolatedOutputGateContext struct {
	context.Context
}

func (ctx isolatedOutputGateContext) Value(key interface{}) interface{} {
	if _, ok := key.(outputGateContextKey); ok {
		return nil
	}
	return ctx.Context.Value(key)
}

// IsolateFirstOutputTimeout preserves cancellation, deadlines, and ordinary
// context values while hiding the current output gate from nested collectors.
// Use it for sibling model work whose output must not disarm the parent task's
// startup guard, such as memory maintenance running beside compression.
func IsolateFirstOutputTimeout(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return isolatedOutputGateContext{Context: ctx}
}

// WithFirstOutputTimeout creates a cancelable context whose fixed timer is
// disarmed by MarkOutput or ObserveMessage. The returned stop function only
// stops this startup timer; it does not cancel the operation.
//
// Callers still own cancel and must invoke it when the operation finishes.
// Parent cancellation remains live after output starts, preserving user stop,
// server drain, account/session invalidation, and transport cancellation.
func WithFirstOutputTimeout(parent context.Context, timeout time.Duration, timeoutCause error) (context.Context, context.CancelCauseFunc, func()) {
	ctx, cancel, stop := WithDeferredFirstOutputTimeout(parent, timeout, timeoutCause)
	ArmFirstOutputTimeout(ctx)
	return ctx, cancel, stop
}

// WithDeferredFirstOutputTimeout attaches an unarmed guard to parent. The raw
// ChatModel wrapper calls ArmFirstOutputTimeout immediately before invoking
// Stream, so database work, prompt construction, Tool assembly, and ADK setup
// cannot consume the model's startup budget.
func WithDeferredFirstOutputTimeout(parent context.Context, timeout time.Duration, timeoutCause error) (context.Context, context.CancelCauseFunc, func()) {
	if parent == nil {
		parent = context.Background()
	}
	if timeoutCause == nil {
		timeoutCause = ErrFirstOutputTimeout
	}

	ctx, cancel := context.WithCancelCause(parent)
	gate := &outputGate{
		cancel:       cancel,
		timeoutCause: timeoutCause,
		parent:       outputGateFromContext(parent),
		timeout:      timeout,
	}
	ctx = context.WithValue(ctx, outputGateContextKey{}, gate)
	return ctx, cancel, gate.stop
}

// ArmFirstOutputTimeout starts every unarmed guard carried by ctx. It is
// idempotent across ReAct rounds and retries: the whole logical run gets one
// startup budget rather than a fresh timeout for every upstream attempt.
func ArmFirstOutputTimeout(ctx context.Context) {
	for current := outputGateFromContext(ctx); current != nil; current = current.parent {
		current.arm()
	}
}

func (g *outputGate) arm() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.armed || g.observed || g.fired || g.stopped {
		return
	}
	g.armed = true
	if g.timeout > 0 {
		g.timer = time.AfterFunc(g.timeout, g.fire)
	}
}

func (g *outputGate) fire() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.armed || g.observed || g.stopped || g.fired {
		return
	}
	g.fired = true
	// Cancel while holding the state lock so a chunk racing with the timer
	// cannot be accepted between marking the timeout and publishing it.
	g.cancel(g.timeoutCause)
}

func (g *outputGate) mark() {
	for current := g; current != nil; current = current.parent {
		current.mu.Lock()
		if current.fired || current.stopped {
			current.mu.Unlock()
			// A child guard that has already timed out or been stopped no
			// longer owns valid progress. A late provider chunk must not walk
			// past that boundary and disarm an enclosing run guard.
			return
		}
		if !current.observed {
			current.observed = true
			if current.timer != nil {
				current.timer.Stop()
			}
		}
		current.mu.Unlock()
	}
}

func (g *outputGate) stop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped {
		return
	}
	g.stopped = true
	if g.timer != nil {
		g.timer.Stop()
	}
}

func outputGateFromContext(ctx context.Context) *outputGate {
	if ctx == nil {
		return nil
	}
	gate, _ := ctx.Value(outputGateContextKey{}).(*outputGate)
	return gate
}

// MarkOutput disarms every first-output guard carried by ctx.
func MarkOutput(ctx context.Context) {
	if gate := outputGateFromContext(ctx); gate != nil {
		gate.mark()
	}
}

// ObserveMessage marks model progress only for output that a caller can
// actually use. Role, usage, finish metadata, and empty tool-call shells do
// not prove that the upstream has started producing an answer.
func ObserveMessage(ctx context.Context, message *schema.Message) bool {
	if !HasMeaningfulOutput(message) {
		return false
	}
	MarkOutput(ctx)
	return true
}

// HasMeaningfulOutput implements the shared first-output definition used by
// chat, background model tasks, and tool refinement.
func HasMeaningfulOutput(message *schema.Message) bool {
	if message == nil {
		return false
	}
	if strings.TrimSpace(message.Content) != "" || strings.TrimSpace(message.ReasoningContent) != "" {
		return true
	}
	for _, call := range message.ToolCalls {
		// IDs and argument fragments can arrive before a provider has emitted
		// a callable function name. Only a named call is useful enough to
		// disarm the startup guard.
		if strings.TrimSpace(call.Function.Name) != "" {
			return true
		}
	}
	return false
}

// ChunkHandler observes a provider chunk after first-output accounting and
// before the shared consumer advances to the next frame.
type ChunkHandler func(*schema.Message) error

// Consume fully drains and closes one Eino message stream, preserving a
// concatenated partial message when Recv or the caller's chunk handler fails.
//
// This is the common reader contract used by background model tasks and the
// ADK assistant event adapter. Business-specific behavior such as SSE deltas,
// attempt resets, and retry decisions stays in the caller, while EOF, nil
// chunks, context causes, partial concatenation, and reader ownership remain
// identical everywhere.
func Consume(
	ctx context.Context,
	stream *schema.StreamReader[*schema.Message],
	onChunk ChunkHandler,
) (*schema.Message, error) {
	if stream == nil {
		return nil, ErrNilReader
	}
	defer stream.Close()

	chunks := make([]*schema.Message, 0, 16)
	for {
		chunk, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			partial, concatErr := concatChunks(chunks)
			if concatErr != nil {
				return nil, concatErr
			}
			return partial, streamError(ctx, recvErr)
		}
		if chunk == nil {
			continue
		}
		chunks = append(chunks, chunk)
		ObserveMessage(ctx, chunk)
		if cause := context.Cause(ctx); cause != nil {
			partial, concatErr := concatChunks(chunks)
			if concatErr != nil {
				return nil, concatErr
			}
			return partial, cause
		}
		if onChunk != nil {
			if err := onChunk(chunk); err != nil {
				partial, concatErr := concatChunks(chunks)
				if concatErr != nil {
					return nil, concatErr
				}
				return partial, err
			}
		}
	}

	result, err := concatChunks(chunks)
	if err != nil {
		return nil, err
	}
	if cause := context.Cause(ctx); cause != nil {
		return result, cause
	}
	return result, nil
}

func concatChunks(chunks []*schema.Message) (*schema.Message, error) {
	if len(chunks) == 0 {
		return nil, nil
	}
	result, err := schema.ConcatMessages(chunks)
	if err != nil {
		return nil, fmt.Errorf("concat model stream: %w", err)
	}
	return result, nil
}

// Collect opens and fully consumes one Eino stream. It never calls Generate:
// providers that only support streaming therefore follow the same transport
// path as the main chat. firstOutputTimeout guards only startup; after the
// first meaningful chunk, collection continues until EOF, semantic
// cancellation, or a real upstream/transport error.
func Collect(
	ctx context.Context,
	chatModel einoModel.BaseChatModel,
	messages []*schema.Message,
	firstOutputTimeout time.Duration,
	options ...einoModel.Option,
) (*schema.Message, error) {
	if chatModel == nil {
		return nil, errors.New("model stream chat model is nil")
	}

	streamCtx, cancel, stopFirstOutputTimer := WithFirstOutputTimeout(ctx, firstOutputTimeout, ErrFirstOutputTimeout)
	defer func() {
		stopFirstOutputTimer()
		cancel(nil)
	}()

	stream, err := chatModel.Stream(streamCtx, messages, options...)
	if err != nil {
		return nil, fmt.Errorf("open model stream: %w", streamError(streamCtx, err))
	}
	if stream == nil {
		return nil, ErrNilReader
	}

	result, err := Consume(streamCtx, stream, nil)
	if err != nil {
		return nil, fmt.Errorf("receive model stream: %w", err)
	}
	if result == nil || !HasMeaningfulOutput(result) {
		return nil, ErrEmptyOutput
	}
	return result, nil
}

func streamError(ctx context.Context, fallback error) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	return fallback
}
