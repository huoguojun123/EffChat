package agent

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/tool"
)

func (a *EinoAgent) buildUtilityModelWithInfo(ctx context.Context, modelID string) (einoModel.ToolCallingChatModel, *modelbank.ModelInfo, *model.AIChannel, error) {
	info, channel, err := a.resolveUtilityModelInfo(ctx, modelID)
	if err != nil {
		return nil, nil, nil, err
	}
	cm, err := a.buildResolvedUtilityModel(ctx, info, channel)
	if err != nil {
		return nil, info, channel, err
	}
	return cm, info, channel, nil
}

func (a *EinoAgent) buildResolvedUtilityModel(ctx context.Context, info *modelbank.ModelInfo, channel *model.AIChannel) (einoModel.ToolCallingChatModel, error) {
	if info == nil || channel == nil {
		return nil, fmt.Errorf("resolved utility model is unavailable")
	}
	channelCopy := *channel
	modelReq := taskModelRequest(&ChatRequest{
		ModelID:          info.ID,
		Provider:         info.Provider,
		RuntimeResolved:  true,
		RuntimeChannel:   &channelCopy,
		ModelMaxOutput:   info.Capabilities.MaxOutput,
		Vision:           info.Capabilities.Vision,
		ToolUse:          info.Capabilities.ToolUse,
		Reasoning:        info.Capabilities.Reasoning,
		ThinkingFormat:   info.ThinkingFormat,
		SuppressThinking: true,
		SearchImpl:       info.Capabilities.SearchImpl,
	}, extractSummaryMaxTokens)
	cm, err := a.buildChatModel(ctx, modelReq, modelbank.SearchDecision{})
	if err != nil {
		return nil, err
	}
	return cm, nil
}

func (a *EinoAgent) resolveUtilityModelInfo(ctx context.Context, preferredModelID string) (*modelbank.ModelInfo, *model.AIChannel, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	channelAvailability := make(map[string]*model.AIChannel)
	unavailableChannels := make(map[string]bool)
	resolveChannel := func(channelKey string) (*model.AIChannel, error) {
		channelKey = strings.TrimSpace(channelKey)
		if channelKey == "" {
			return nil, nil
		}
		if channel, ok := channelAvailability[channelKey]; ok {
			return channel, nil
		}
		if unavailableChannels[channelKey] {
			return nil, nil
		}
		channel, err := a.resolveUtilityChannelContext(ctx, channelKey)
		if err != nil {
			return nil, err
		}
		if channel == nil {
			unavailableChannels[channelKey] = true
			return nil, nil
		}
		channelAvailability[channelKey] = channel
		return channel, nil
	}

	preferredModelID = strings.TrimSpace(preferredModelID)
	if preferredModelID != "" {
		if info := modelbank.Get(preferredModelID); info != nil && info.Enabled {
			channel, err := resolveChannel(info.Provider)
			if err != nil {
				return nil, nil, err
			}
			if channel != nil {
				return info, channel, nil
			}
		}
	}

	candidates := modelbank.List()
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Provider == candidates[j].Provider {
			return candidates[i].ID < candidates[j].ID
		}
		return candidates[i].Provider < candidates[j].Provider
	})
	for _, info := range candidates {
		if info == nil || !info.Enabled {
			continue
		}
		channel, err := resolveChannel(info.Provider)
		if err != nil {
			return nil, nil, err
		}
		if channel != nil {
			return info, channel, nil
		}
	}
	if preferredModelID == "" {
		return nil, nil, fmt.Errorf("no enabled utility model has an available channel")
	}
	return nil, nil, fmt.Errorf("utility model %q is unavailable and no fallback model has an available channel", preferredModelID)
}

func (a *EinoAgent) resolveUtilityChannelContext(ctx context.Context, channelKey string) (*model.AIChannel, error) {
	if a == nil || a.channelService == nil || strings.TrimSpace(channelKey) == "" {
		return nil, nil
	}
	channel, err := a.channelService.ResolveAIChannelContext(ctx, channelKey)
	if err != nil {
		if ctxErr := runtimeContextError(ctx, err); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, nil
	}
	return channel, nil
}

// buildExtractSummarizer constructs the per-run web refinement dependency.
// Accepted runs use their in-memory model/channel copies; live configuration is
// resolved only for compatibility callers that have not passed admission.
func (a *EinoAgent) buildExtractSummarizer(ctx context.Context, req *ChatRequest) (tool.Summarizer, bool, error) {
	setupCtx, setupCancel := extractSummarySetupContext(ctx)
	defer setupCancel()

	enabled, modelID, err := a.resolveExtractSummaryRuntimeConfig(setupCtx, req)
	if err != nil {
		return nil, false, err
	}
	if !enabled {
		return nil, false, nil
	}

	var (
		cm      einoModel.ToolCallingChatModel
		info    *modelbank.ModelInfo
		channel *model.AIChannel
	)
	if req != nil && req.RuntimeResolved {
		info = req.RuntimeExtractSummaryModelInfo
		channel = req.RuntimeExtractSummaryChannel
		if info == nil || channel == nil {
			return nil, false, nil
		}
		cm, err = a.buildResolvedUtilityModel(setupCtx, info, channel)
	} else {
		cm, info, channel, err = a.buildUtilityModelWithInfo(setupCtx, modelID)
	}
	if err != nil {
		if ctxErr := runtimeContextError(setupCtx, err); ctxErr != nil {
			return nil, false, ctxErr
		}
		log.Printf("[web_extract] 提炼小模型构造失败，降级到截断: model=%s err=%v", modelID, err)
		return nil, false, nil
	}
	return &extractSummarizer{
		chatModel: cm, taskRuns: a.taskRunRepo, provider: info.Provider, modelID: info.ID,
		runtimeVersion: a.extractSummarizerRuntimeVersion(modelID, info, channel),
	}, true, nil
}

func (a *EinoAgent) resolveExtractSummaryRuntimeConfig(ctx context.Context, req *ChatRequest) (bool, string, error) {
	enabled := true
	modelID := "claude-haiku-4-5"
	if req != nil && req.RuntimeResolved {
		return req.RuntimeExtractSummaryEnabled, strings.TrimSpace(req.RuntimeExtractSummaryModel), nil
	}
	if a != nil && a.configRepo != nil {
		var err error
		enabled, err = a.configRepo.GetBoolContext(ctx, "extract_summary_enabled", enabled)
		if err != nil {
			return false, "", err
		}
		modelID, err = a.configRepo.GetStringContext(ctx, "extract_summary_model", modelID)
		if err != nil {
			return false, "", err
		}
	}
	return enabled, strings.TrimSpace(modelID), nil
}

func (a *EinoAgent) extractSummarizerRuntimeVersion(configuredModelID string, info *modelbank.ModelInfo, channel *model.AIChannel) string {
	material := struct {
		ConfiguredModelID string
		Model             runtimeModelMaterial
		Channel           runtimeChannelMaterial
	}{
		ConfiguredModelID: strings.TrimSpace(configuredModelID),
		Model:             runtimeModelInfoMaterial(info),
		Channel:           runtimeAIChannelMaterial(channel),
	}
	return checksumValue("extract-refinement-runtime", material)
}

const extractSummarySetupTimeout = 10 * time.Second

// extractSummarySetupContext gives accepted refinement model/channel setup a
// small control-plane budget. The returned ChatModel does not retain this child;
// actual streaming remains owned by the Tool call context and its first-output
// guard.
func extractSummarySetupContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, extractSummarySetupTimeout)
}
