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
	if err != nil {
		m.record(ctx, input, nil, err, time.Since(startedAt))
		return nil, err
	}
	if reader == nil {
		err = modelstream.ErrNilReader
		m.record(ctx, input, nil, err, time.Since(startedAt))
		return nil, err
	}

	out, writer := schema.Pipe[*schema.Message](1)
	go func() {
		defer writer.Close()
		defer reader.Close()

		// 流式 usage 通常只出现在最后几个 chunk；这里透传每个 chunk，同时记住
		// 最后一条带 Usage 的消息。这样既不改变上游流行为，也能在 EOF 后记录
		// 完整 token 口径。若中途出错，则带上已观察到的 usage 和截断后的错误。
		var lastUsageMsg *schema.Message
		var streamErr error
		success := false

		for {
			chunk, recvErr := reader.Recv()
			if errors.Is(recvErr, io.EOF) {
				success = true
				break
			}
			if recvErr != nil {
				streamErr = recvErr
				writer.Send(nil, recvErr)
				break
			}
			if chunk != nil && chunk.ResponseMeta != nil && chunk.ResponseMeta.Usage != nil {
				lastUsageMsg = chunk
			}
			if closed := writer.Send(chunk, nil); closed {
				streamErr = context.Canceled
				break
			}
		}

		if success {
			m.record(ctx, input, lastUsageMsg, nil, time.Since(startedAt))
		} else {
			m.record(ctx, input, lastUsageMsg, streamErr, time.Since(startedAt))
		}
	}()

	return out, nil
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
	kind := inferKind(meta.Kind, input)
	event := Event{
		UserID:     meta.UserID,
		SessionID:  meta.SessionID,
		MessageID:  meta.MessageID,
		RunID:      meta.RunID,
		Kind:       kind,
		Provider:   meta.Provider,
		ModelID:    meta.ModelID,
		Success:    err == nil,
		DurationMs: duration.Milliseconds(),
	}
	if out != nil && out.ResponseMeta != nil && out.ResponseMeta.Usage != nil {
		u := out.ResponseMeta.Usage
		event.PromptTokens = u.PromptTokens
		event.CompletionTokens = u.CompletionTokens
		event.TotalTokens = u.TotalTokens
		event.CachedTokens = u.PromptTokenDetails.CachedTokens
		event.ReasoningTokens = u.CompletionTokensDetails.ReasoningTokens
	}
	if err != nil {
		event.ErrorType = ErrorType(err)
		event.ErrorMessage = err.Error()
	}
	m.recorder.Record(event)
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
