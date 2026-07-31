package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/modelstream"
)

const compactionMaxOutputTokens = 8192

// PreparedCompactionRun is the immutable boundary between bounded compaction
// setup and durable execution. It owns the resolved model and converted input,
// but never stores the setup context that created them.
type PreparedCompactionRun struct {
	profile        compressionModelProfile
	input          []*schema.Message
	sourceMessages []*model.Message
	boundary       int64
}

// CompactConversation 手动压缩：对当前会话历史做一次性总结，返回压缩检查点。
//
// 与自动压缩（summarization 中间件，按阈值在对话轮中触发）不同，这是用户主动
// 通过 /compact 指令触发的一次性操作：不产生新的助手回复，只生成摘要并标记边界。
// 摘要消息格式与中间件产出保持一致（role=user，extra._eino_summarization_content_type=summary），
// 因此加载、标题统计等逻辑无需区分两种来源。
//
// 返回 nil checkpoint 表示无需压缩（历史为空或不足以压缩）。
func (a *EinoAgent) CompactConversation(ctx context.Context, req *ChatRequest) (*CompressionCheckpoint, error) {
	prepared, err := a.PrepareCompaction(ctx, req)
	if err != nil || prepared == nil {
		return nil, err
	}
	return a.RunPreparedCompaction(ctx, prepared)
}

// PrepareCompaction resolves the provider and converts the source history
// while the caller still owns a bounded setup context.
func (a *EinoAgent) PrepareCompaction(setupCtx context.Context, req *ChatRequest) (*PreparedCompactionRun, error) {
	if setupCtx == nil {
		return nil, fmt.Errorf("compaction setup context is required")
	}
	if req == nil {
		return nil, fmt.Errorf("compaction request is required")
	}
	if len(req.Messages) == 0 {
		return nil, nil
	}

	compressionReq := taskModelRequest(req, compactionMaxOutputTokens)
	chatModel, err := a.buildChatModel(setupCtx, compressionReq, modelbank.SearchDecision{})
	if err != nil {
		return nil, err
	}
	compressionProfile := a.buildCompressionModel(setupCtx, chatModel, compressionReq.Provider, compressionReq.ModelID)

	// 压缩仅做文本摘要，图片退化为文字提示即可，无需读盘转 base64。
	history, err := convertToEinoMessages(req.Messages, false)
	if err != nil {
		return nil, err
	}

	input := make([]*schema.Message, 0, len(history)+2)
	input = append(input, schema.SystemMessage("You are an assistant responsible for summarizing conversations into durable continuation context."))
	input = append(input, history...)
	input = append(input, schema.UserMessage(compactionInstruction))

	return &PreparedCompactionRun{
		profile:        compressionProfile,
		input:          input,
		sourceMessages: append([]*model.Message(nil), req.Messages...),
		boundary:       req.Messages[len(req.Messages)-1].ID + 1,
	}, nil
}

// RunPreparedCompaction executes the model stream with the durable run
// context. The setup child may already be canceled when this method starts.
func (a *EinoAgent) RunPreparedCompaction(runCtx context.Context, prepared *PreparedCompactionRun) (*CompressionCheckpoint, error) {
	if runCtx == nil {
		return nil, fmt.Errorf("durable compaction context is required")
	}
	if prepared == nil || prepared.profile.Model == nil {
		return nil, fmt.Errorf("prepared compaction run is required")
	}

	// The enclosing RunHub owns the configurable compaction first-output gate.
	// A zero local timeout still lets Collect arm and observe that parent gate,
	// without imposing a second hard-coded deadline that would shadow config.
	result, err := modelstream.Collect(runCtx, prepared.profile.Model, prepared.input, 0)
	if err != nil {
		return nil, sanitizeModelRuntimeError(prepared.profile.Provider, prepared.profile.ModelID, err)
	}
	if err := validateCompactionCompletion(prepared.profile.Provider, prepared.profile.ModelID, result); err != nil {
		return nil, err
	}
	if result == nil || result.Content == "" {
		return nil, fmt.Errorf("compaction produced empty summary")
	}
	summaryMsg := buildCompactionSummaryMessage(result.Content, prepared.sourceMessages)
	summaryData, err := json.Marshal(summaryMsg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal summary message: %w", err)
	}

	return &CompressionCheckpoint{
		SummaryData:    summaryData,
		CompressBefore: prepared.boundary,
		Provider:       prepared.profile.Provider,
		ModelID:        prepared.profile.ModelID,
	}, nil
}
