package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/effchat/internal/modelbank"
)

// CompactConversation 手动压缩：对当前会话历史做一次性总结，返回压缩检查点。
//
// 与自动压缩（summarization 中间件，按阈值在对话轮中触发）不同，这是用户主动
// 通过 /compact 指令触发的一次性操作：不产生新的助手回复，只生成摘要并标记边界。
// 摘要消息格式与中间件产出保持一致（role=user，extra._eino_summarization_content_type=summary），
// 因此加载、标题统计等逻辑无需区分两种来源。
//
// 返回 nil checkpoint 表示无需压缩（历史为空或不足以压缩）。
func (a *EinoAgent) CompactConversation(ctx context.Context, req *ChatRequest) (*CompressionCheckpoint, error) {
	if len(req.Messages) == 0 {
		return nil, nil
	}

	chatModel, err := a.buildChatModel(ctx, req, modelbank.SearchDecision{})
	if err != nil {
		return nil, err
	}
	compressionProfile := a.buildCompressionModel(ctx, chatModel, req.Provider, req.ModelID)

	// 压缩仅做文本摘要，图片退化为文字提示即可，无需读盘转 base64。
	history, err := convertToEinoMessages(req.Messages, false)
	if err != nil {
		return nil, err
	}

	input := make([]*schema.Message, 0, len(history)+2)
	input = append(input, schema.SystemMessage("You are an assistant responsible for summarizing conversations into durable continuation context."))
	input = append(input, history...)
	input = append(input, schema.UserMessage(compactionInstruction))

	result, err := compressionProfile.Model.Generate(ctx, input)
	if err != nil {
		return nil, sanitizeModelRuntimeError(compressionProfile.Provider, compressionProfile.ModelID, err)
	}
	if err := validateCompactionCompletion(compressionProfile.Provider, compressionProfile.ModelID, result); err != nil {
		return nil, err
	}
	if result == nil || result.Content == "" {
		return nil, fmt.Errorf("compaction produced empty summary")
	}
	summaryMsg := buildCompactionSummaryMessage(result.Content, req.Messages)
	summaryData, err := json.Marshal(summaryMsg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal summary message: %w", err)
	}

	boundary := req.Messages[len(req.Messages)-1].ID + 1
	return &CompressionCheckpoint{
		SummaryData:    summaryData,
		CompressBefore: boundary,
		Provider:       compressionProfile.Provider,
		ModelID:        compressionProfile.ModelID,
	}, nil
}
