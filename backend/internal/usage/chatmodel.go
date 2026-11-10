package usage

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/EffChat/internal/modelstream"
)

// 本包把“模型调用用量”定义为上游 LLM API 审计日志，而不是业务统计报表。
//
// 设计上只记录真正穿过 ChatModel 的调用：聊天、重试、标题生成、压缩摘要以及
// 工具内部的小模型调用都会自然经过这里；Tavily/Firecrawl/Python extractor 等
// 非模型服务不进入这个口径。后续如果要做成本或额度，应在 usage event 之外
// 增加价格快照，而不要用当前价格回算历史事件。

// WrapChatModel 在 Eino ChatModel 外面包一层用量记录器。
//
// 这是最小耦合点：所有真正访问上游 LLM API 的路径最终都会调用 ChatModel.Generate
// 或 ChatModel.Stream。把统计放在这里，可以自然覆盖普通聊天、重试、标题生成、压缩、
// web_extract 提炼小模型，而不需要把“写用量”的代码散落到每个业务 handler。
func WrapChatModel(base einoModel.ToolCallingChatModel, recorder *Service, defaults Meta) einoModel.ToolCallingChatModel {
	if base == nil || recorder == nil {
		return base
	}
	return &recordingChatModel{base: base, recorder: recorder, defaults: defaults}
}

// WrapAgenticModel records the official Responses AgenticModel at the same raw
// LLM boundary as classic chat models. Each ReAct model turn produces one
// audit event; local Tool execution remains accounted by tool governance.
func WrapAgenticModel(base einoModel.AgenticModel, recorder *Service, defaults Meta) einoModel.AgenticModel {
	if base == nil || recorder == nil {
		return base
	}
	return &recordingAgenticModel{base: base, recorder: recorder, defaults: defaults}
}

type recordingAgenticModel struct {
	base     einoModel.AgenticModel
	recorder *Service
	defaults Meta
}

func (m *recordingAgenticModel) Generate(ctx context.Context, input []*schema.AgenticMessage, opts ...einoModel.Option) (*schema.AgenticMessage, error) {
	startedAt := time.Now()
	out, err := m.base.Generate(ctx, input, opts...)
	m.record(ctx, out, err, time.Since(startedAt))
	return out, err
}

func (m *recordingAgenticModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...einoModel.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	startedAt := time.Now()
	reader, err := m.base.Stream(ctx, input, opts...)
	if err == nil && reader == nil {
		err = modelstream.ErrNilReader
	}
	if err != nil {
		m.record(ctx, nil, err, time.Since(startedAt))
		return nil, err
	}
	return observeUsageStream(reader, func(chunk *schema.AgenticMessage) bool {
		return chunk != nil && chunk.ResponseMeta != nil && chunk.ResponseMeta.TokenUsage != nil
	}, func(last *schema.AgenticMessage, err error) {
		m.record(ctx, last, err, time.Since(startedAt))
	}), nil
}

func (m *recordingAgenticModel) record(ctx context.Context, out *schema.AgenticMessage, err error, duration time.Duration) {
	if m == nil || m.recorder == nil {
		return
	}
	meta := mergeMeta(m.defaults, MetaFromContext(ctx))
	var usage *schema.TokenUsage
	if out != nil && out.ResponseMeta != nil {
		usage = out.ResponseMeta.TokenUsage
	}
	m.recorder.Record(modelUsageEvent(meta, meta.Kind, usage, err, duration))
}

type recordingChatModel struct {
	base     einoModel.ToolCallingChatModel
	recorder *Service
	defaults Meta
}

func (m *recordingChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...einoModel.Option) (*schema.Message, error) {
	startedAt := time.Now()
	out, err := m.base.Generate(ctx, input, opts...)
	m.record(ctx, input, out, err, time.Since(startedAt))
	return out, err
}

func (m *recordingChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...einoModel.Option) (*schema.StreamReader[*schema.Message], error) {
	startedAt := time.Now()
	reader, err := m.base.Stream(ctx, input, opts...)
	if err == nil && reader == nil {
		err = modelstream.ErrNilReader
	}
	if err != nil {
		m.record(ctx, input, nil, err, time.Since(startedAt))
		return nil, err
	}
	return observeUsageStream(reader, func(chunk *schema.Message) bool {
		return chunk != nil && chunk.ResponseMeta != nil && chunk.ResponseMeta.Usage != nil
	}, func(last *schema.Message, err error) {
		m.record(ctx, input, last, err, time.Since(startedAt))
	}), nil
}

func (m *recordingChatModel) WithTools(tools []*schema.ToolInfo) (einoModel.ToolCallingChatModel, error) {
	next, err := m.base.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &recordingChatModel{base: next, recorder: m.recorder, defaults: m.defaults}, nil
}

func (m *recordingChatModel) record(ctx context.Context, input []*schema.Message, out *schema.Message, err error, duration time.Duration) {
	if m == nil || m.recorder == nil {
		return
	}
	meta := mergeMeta(m.defaults, MetaFromContext(ctx))
	var usage *schema.TokenUsage
	if out != nil && out.ResponseMeta != nil {
		usage = out.ResponseMeta.Usage
	}
	// Classic callers retain legacy compression-instruction inference. Typed
	// callers use their explicit Meta; sharing projection must not change Kind.
	m.recorder.Record(modelUsageEvent(meta, inferKind(meta.Kind, input), usage, err, duration))
}

// observeUsageStream owns both stream ends after setup succeeds. It forwards
// chunks unchanged and records once, before closing the output. Usage frames
// are cumulative snapshots: retain the last reported one, including on error,
// rather than summing frames or dropping it when a later frame has no usage.
// The caller still owns cancellation of a provider blocked inside Recv;
// closing the returned reader is observed when forwarding its next chunk.
func observeUsageStream[T any](reader *schema.StreamReader[T], hasUsage func(T) bool, record func(T, error)) *schema.StreamReader[T] {
	out, writer := schema.Pipe[T](1)
	go func() {
		defer writer.Close()
		defer reader.Close()
		var last T
		var streamErr error
		for {
			chunk, err := reader.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				streamErr = err
				var zero T
				writer.Send(zero, err)
				break
			}
			if hasUsage(chunk) {
				last = chunk
			}
			if closed := writer.Send(chunk, nil); closed {
				streamErr = context.Canceled
				break
			}
		}
		record(last, streamErr)
	}()
	return out
}

// modelUsageEvent projects only the existing product audit fields. A nil usage
// remains the legacy zero-valued event, not proof of a measured zero; preserving
// field presence/cache-write requires its own adapter and storage contract.
func modelUsageEvent(meta Meta, kind string, usage *schema.TokenUsage, err error, duration time.Duration) Event {
	if kind == "" {
		kind = KindChat
	}
	event := Event{
		UserID: meta.UserID, SessionID: meta.SessionID, MessageID: meta.MessageID, RunID: meta.RunID,
		Kind: kind, Provider: meta.Provider, ModelID: meta.ModelID,
		Success: err == nil, DurationMs: duration.Milliseconds(),
	}
	if usage != nil {
		event.PromptTokens = usage.PromptTokens
		event.CompletionTokens = usage.CompletionTokens
		event.TotalTokens = usage.TotalTokens
		event.CachedTokens = usage.PromptTokenDetails.CachedTokens
		event.ReasoningTokens = usage.CompletionTokensDetails.ReasoningTokens
	}
	if err != nil {
		event.ErrorType = ErrorType(err)
		event.ErrorMessage = err.Error()
	}
	return event
}

func inferKind(defaultKind string, input []*schema.Message) string {
	if defaultKind == "" {
		defaultKind = KindChat
	}
	if defaultKind != KindChat && defaultKind != KindRetry {
		return defaultKind
	}
	for _, msg := range input {
		if msg == nil {
			continue
		}
		content := msg.Content
		if strings.Contains(content, "<summary>") &&
			(strings.Contains(content, "Create a detailed continuation summary") ||
				strings.Contains(content, "你的任务是为这段对话生成一份详细总结")) {
			return KindCompression
		}
	}
	return defaultKind
}
