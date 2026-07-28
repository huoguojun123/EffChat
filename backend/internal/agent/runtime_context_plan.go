package agent

import (
	"github.com/huoguojun123/effchat/internal/modelbank"
	"github.com/huoguojun123/effchat/internal/service"
)

type runtimeContextPlan struct {
	searchDecision  modelbank.SearchDecision
	toolRuntime     service.ToolRuntimeConfigSet
	searchRuntime   service.SearchRuntimeConfig
	mountedTools    map[string]bool
	schemaTokenCost int
}

func (a *EinoAgent) buildRuntimeContextPlan(req *ChatRequest) runtimeContextPlan {
	runtime := a.resolveToolRuntimeConfig()
	if req != nil && req.RuntimeResolved && req.RuntimeToolConfig != nil {
		runtime = req.RuntimeToolConfig
	}
	searchMode := req.SearchMode
	if searchMode == "" {
		searchMode = modelbank.SearchModeAuto
	}
	decision := modelbank.ResolveSearchConfig(req.ModelID, req.Provider, searchMode, req.PreferModelNativeSearch)
	if req.RuntimeResolved {
		decision = modelbank.ResolveSearchConfigForCapabilities(req.SearchImpl, req.ToolUse, searchMode, req.PreferModelNativeSearch)
	}
	searchRuntime := a.resolveSearchRuntimeConfig()
	if req.RuntimeResolved {
		searchRuntime = req.RuntimeSearchConfig
	}
	webToolsEnabled := (runtime.IsEnabled("web_search") && hasSearchProvider(searchRuntime)) || runtime.IsEnabled("web_extract")
	if decision.UseApplicationTool && !webToolsEnabled {
		decision.UseApplicationTool = false
	}

	mounted := make(map[string]bool)
	if req.MemoryEnabled && a.memoryRepo != nil && req.SessionID > 0 && runtime.IsEnabled("memory") && runtimeMemoryAvailable(req.RuntimeMemoryState) {
		mounted["memory"] = true
	}
	if a.fileRepo != nil && req.UserID > 0 && req.SessionID > 0 {
		for _, name := range []string{"file_list", "file_search", "file_read"} {
			if runtime.IsEnabled(name) {
				mounted[name] = true
			}
		}
	}
	if len(req.EnabledSkills) > 0 {
		for _, name := range []string{"skill_list", "skill_search", "skill_read"} {
			if runtime.IsEnabled(name) {
				mounted[name] = true
			}
		}
	}
	if decision.UseApplicationTool {
		if runtime.IsEnabled("web_search") && hasSearchProvider(searchRuntime) {
			mounted["web_search"] = true
		}
		if runtime.IsEnabled("web_extract") {
			mounted["web_extract"] = true
		}
	}

	return runtimeContextPlan{
		searchDecision:  decision,
		toolRuntime:     runtime,
		searchRuntime:   searchRuntime,
		mountedTools:    mounted,
		schemaTokenCost: estimateMountedToolSchemaTokens(mounted),
	}
}

func runtimeMemoryAvailable(state service.RuntimeConfigState) bool {
	return state.State != service.RuntimeStateUnavailable
}

func estimateMountedToolSchemaTokens(mounted map[string]bool) int {
	budgets := map[string]int{
		"memory":       700,
		"file_list":    300,
		"file_search":  500,
		"file_read":    450,
		"skill_list":   250,
		"skill_search": 450,
		"skill_read":   450,
		"web_search":   450,
		"web_extract":  500,
	}
	total := 0
	for name, enabled := range mounted {
		if enabled {
			total += budgets[name]
		}
	}
	return total
}
