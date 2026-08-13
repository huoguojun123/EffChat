package agent

import (
	"context"
	"errors"
	"log"

	sessionmemory "github.com/huoguojun123/EffChat/internal/memory"
	"github.com/huoguojun123/EffChat/internal/service"
)

func runtimeContextError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}

func (a *EinoAgent) resolveToolRuntimeConfigWithStateContext(ctx context.Context) (service.ToolRuntimeConfigSet, service.RuntimeConfigState, error) {
	var (
		runtime service.ToolRuntimeConfigSet
		state   service.RuntimeConfigState
		err     error
	)
	if a == nil || a.toolService == nil {
		runtime, state, err = service.NewToolConfigService(nil).ResolveRuntimeConfigContext(ctx)
	} else {
		runtime, state, err = a.toolService.ResolveRuntimeConfigContext(ctx)
	}
	if ctxErr := runtimeContextError(ctx, err); ctxErr != nil {
		return nil, service.RuntimeConfigState{}, ctxErr
	}
	return runtime, state, err
}

func (a *EinoAgent) resolveSearchRuntimeConfigWithStateContext(ctx context.Context) (service.SearchRuntimeConfig, service.SearchRuntimeConfigState, error) {
	var (
		runtime service.SearchRuntimeConfig
		state   service.SearchRuntimeConfigState
		err     error
	)
	if a == nil || a.channelService == nil {
		runtime, state, err = service.NewChannelService(nil).ResolveSearchRuntimeConfigWithStateContext(ctx)
	} else {
		runtime, state, err = a.channelService.ResolveSearchRuntimeConfigWithStateContext(ctx)
	}
	if ctxErr := runtimeContextError(ctx, err); ctxErr != nil {
		return service.SearchRuntimeConfig{}, service.SearchRuntimeConfigState{}, ctxErr
	}
	return runtime, state, err
}

func (a *EinoAgent) runtimeConfigMaterialContext(ctx context.Context) (runtimeConfigMaterial, error) {
	if cause := context.Cause(ctx); cause != nil {
		return runtimeConfigMaterial{}, cause
	}
	material := runtimeConfigMaterial{
		ExtractSummaryEnabled: true,
		ExtractSummaryModel:   "claude-haiku-4-5",
		MemoryMaxChars:        sessionmemory.DefaultLimits().MaxChars,
	}
	material.ExtractSummaryState = extractSummaryConfigState(true, service.RuntimeStateDefault, "", material.ExtractSummaryModel)
	if a == nil {
		return material, nil
	}
	material.CompressionContextThreshold = a.compressMaxTokens
	if a.configRepo == nil {
		return material, nil
	}

	var err error
	material.CompressionContextThreshold, err = a.configRepo.GetIntContext(ctx, "compression_context_threshold", material.CompressionContextThreshold)
	if err != nil {
		if ctxErr := runtimeContextError(ctx, err); ctxErr != nil {
			return runtimeConfigMaterial{}, ctxErr
		}
		return runtimeConfigMaterial{}, err
	}
	limits, err := a.configRepo.GetMemoryLimitsContext(ctx)
	if err != nil {
		if ctxErr := runtimeContextError(ctx, err); ctxErr != nil {
			return runtimeConfigMaterial{}, ctxErr
		}
		return runtimeConfigMaterial{}, err
	}
	material.MemoryMaxChars = limits.MaxChars
	var policyDegraded bool
	material.ExtractSummaryEnabled, policyDegraded, err = a.configRepo.GetPolicyBoolContext(ctx, "extract_summary_enabled", material.ExtractSummaryEnabled)
	if err != nil {
		if ctxErr := runtimeContextError(ctx, err); ctxErr != nil {
			return runtimeConfigMaterial{}, ctxErr
		}
		log.Printf("[web_extract] refinement policy unavailable during runtime admission: err=%v", err)
		material.ExtractSummaryEnabled = false
		material.ExtractSummaryState = extractSummaryConfigState(false, service.RuntimeStateUnavailable, "config_unavailable", material.ExtractSummaryModel)
		return material, nil
	}
	var modelDegraded bool
	material.ExtractSummaryModel, modelDegraded, err = a.configRepo.GetPolicyStringContext(ctx, "extract_summary_model", material.ExtractSummaryModel)
	if err != nil {
		if ctxErr := runtimeContextError(ctx, err); ctxErr != nil {
			return runtimeConfigMaterial{}, ctxErr
		}
		log.Printf("[web_extract] refinement model policy unavailable during runtime admission: err=%v", err)
		material.ExtractSummaryEnabled = false
		material.ExtractSummaryState = extractSummaryConfigState(false, service.RuntimeStateUnavailable, "config_unavailable", material.ExtractSummaryModel)
		return material, nil
	}
	switch {
	case policyDegraded || modelDegraded:
		material.ExtractSummaryState = extractSummaryConfigState(material.ExtractSummaryEnabled, service.RuntimeStateUnavailable, "last_known_good", material.ExtractSummaryModel)
	case !material.ExtractSummaryEnabled:
		material.ExtractSummaryState = extractSummaryConfigState(false, service.RuntimeStateDisabled, "", material.ExtractSummaryModel)
	default:
		material.ExtractSummaryState = extractSummaryConfigState(true, service.RuntimeStateReady, "", material.ExtractSummaryModel)
	}
	return material, nil
}

func extractSummaryConfigState(enabled bool, state, cause, modelID string) service.RuntimeConfigState {
	version := checksumValue("extract-summary-policy", struct {
		Enabled bool
		ModelID string
		State   string
		Cause   string
	}{Enabled: enabled, ModelID: modelID, State: state, Cause: cause})
	return service.RuntimeConfigState{State: state, Cause: cause, Version: version}
}
