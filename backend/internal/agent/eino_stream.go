package agent

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/EffChat/internal/modelstream"
	"github.com/huoguojun123/EffChat/pkg/streaming"
)

// consumeAssistantEvent 消费一个助手事件，逐帧发出 SSE delta，
// 返回拼接还原后的完整消息（含 tool_calls / reasoning / response_meta）。
// 流被重试错误中断时返回 nil（调用方丢弃）。
func (a *EinoAgent) consumeAssistantEvent(
	ctx context.Context,
	mv *adk.MessageVariant,
	emit func(string, interface{}) error,
) (*schema.Message, error) {
	var emittedContentRunes, emittedThinkingRunes int
	emitDelta := func(event string, data interface{}) error {
		if err := emit(event, data); err != nil {
			return err
		}
		switch value := data.(type) {
		case streaming.ContentDeltaEvent:
			emittedContentRunes += utf8.RuneCountInString(value.Delta)
		case streaming.ThinkingDeltaEvent:
			emittedThinkingRunes += utf8.RuneCountInString(value.Delta)
		}
		return nil
	}
	resetEmittedAttempt := func() error {
		if emittedContentRunes == 0 && emittedThinkingRunes == 0 {
			return nil
		}
		return emit(streaming.EventAttemptReset, streaming.AttemptResetEvent{
			ContentRunes:  emittedContentRunes,
			ThinkingRunes: emittedThinkingRunes,
		})
	}
	// 内联 <think> 切割器：老模型把思考写进正文流时，把 <think>...</think> 路由到
	// thinking 通道。对正文不以 <think> 开头的现代模型为纯透传，零影响。
	var splitter thinkSplitter
	emitContent := func(delta string) error {
		c, th := splitter.feed(delta)
		if th != "" {
			if err := emitDelta(streaming.EventThinkingDelta, streaming.ThinkingDeltaEvent{Delta: th}); err != nil {
				return err
			}
		}
		if c != "" {
			if err := emitDelta(streaming.EventContentDelta, streaming.ContentDeltaEvent{Delta: c}); err != nil {
				return err
			}
		}
		return nil
	}
	flushContent := func() error {
		c, th := splitter.flush()
		if th != "" {
			if err := emitDelta(streaming.EventThinkingDelta, streaming.ThinkingDeltaEvent{Delta: th}); err != nil {
				return err
			}
		}
		if c != "" {
			if err := emitDelta(streaming.EventContentDelta, streaming.ContentDeltaEvent{Delta: c}); err != nil {
				return err
			}
		}
		return nil
	}

	if !mv.IsStreaming {
		msg := mv.Message
		if msg == nil {
			return nil, nil
		}
		modelstream.ObserveMessage(ctx, msg)
		if msg.ReasoningContent != "" {
			if err := emitDelta(streaming.EventThinkingDelta, streaming.ThinkingDeltaEvent{Delta: msg.ReasoningContent}); err != nil {
				return nil, err
			}
		}
		if msg.Content != "" {
			if err := emitContent(msg.Content); err != nil {
				return nil, err
			}
			if err := flushContent(); err != nil {
				return nil, err
			}
		}
		stripInlineThink(msg)
		return msg, nil
	}

	stream := mv.MessageStream
	full, err := modelstream.Consume(ctx, stream, func(chunk *schema.Message) error {
		if chunk.ReasoningContent != "" {
			if err := emitDelta(streaming.EventThinkingDelta, streaming.ThinkingDeltaEvent{Delta: chunk.ReasoningContent}); err != nil {
				return err
			}
		}
		if chunk.Content != "" {
			if err := emitContent(chunk.Content); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		var willRetry *adk.WillRetryError
		if errors.As(err, &willRetry) {
			if resetErr := resetEmittedAttempt(); resetErr != nil {
				return nil, resetErr
			}
			return nil, nil // 重试中，丢弃这段部分流
		}
		if flushErr := flushContent(); flushErr != nil {
			return nil, flushErr
		}
		if full == nil {
			return nil, err
		}
		stripInlineThink(full)
		return full, fmt.Errorf("stream error: %w", err)
	}
	if err := flushContent(); err != nil {
		return nil, err
	}

	if full == nil {
		return nil, nil
	}
	stripInlineThink(full)
	return full, nil
}

// stripInlineThink 把内联 <think>...</think> 从 msg.Content 切出，正文留干净文本，
// 思考并入 ReasoningContent。保证落库的 content 不含 <think>、reasoning 不丢。
// 对不以 <think> 开头的正文为 no-op（与流式切割同一核心逻辑，口径一致）。
func stripInlineThink(msg *schema.Message) {
	if msg == nil || msg.Content == "" {
		return
	}
	content, thinking := splitThinkContent(msg.Content)
	if thinking == "" {
		return
	}
	msg.Content = content
	if msg.ReasoningContent == "" {
		msg.ReasoningContent = thinking
	} else {
		msg.ReasoningContent += thinking
	}
}
