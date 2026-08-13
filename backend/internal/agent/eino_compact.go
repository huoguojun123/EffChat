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
	// Compaction is a bounded utility call whose content is persisted as future
	// conversation context. Optional provider thinking must not consume the
	// summary allowance or become durable user-visible checkpoint text.
	compressionReq.SuppressThinking = true
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
	// The detailed compaction contract belongs to the system layer. Keeping it
	// out of source user messages prevents the model from recording EffChat's
	// own output protocol as a user request. A short final marker still gives
	// every provider a user turn to answer after histories that end in assistant
	// output; the system instruction explicitly excludes that marker from the
	// conversation being summarized.
	input = append(input, schema.SystemMessage(compactionSystemInstruction))
	input = append(input, history...)
	input = append(input, schema.UserMessage(compactionRequestMarker))

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
	// Some OpenAI-compatible gateways can still return legacy <think> blocks as
	// ordinary content even when the request disables thinking. Separate that
	// material before the checkpoint consumer extracts and persists the summary.
	stripInlineThink(result)
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
