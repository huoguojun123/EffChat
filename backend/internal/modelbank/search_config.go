package modelbank

// SearchMode 搜索模式（对齐 LobeHub SearchMode）
type SearchMode string

const (
	// SearchModeOff 关闭搜索
	SearchModeOff SearchMode = "off"
	// SearchModeAuto 自适应：有原生搜索优先用原生，否则挂工具
	SearchModeAuto SearchMode = "auto"
	// SearchModeOn 强制开启搜索
	SearchModeOn SearchMode = "on"
)

// SearchDecision 搜索决策结果
type SearchDecision struct {
	// EnabledSearch 是否开启了搜索（searchMode != off）
	EnabledSearch bool
	// UseModelNativeSearch 是否使用模型原生搜索
	UseModelNativeSearch bool
	// UseApplicationTool 是否挂载应用内置搜索工具（SearXNG / 网页提取）
	// 对支持工具调用的模型，即使开启了原生搜索，也保留应用工具作为兜底。
	UseApplicationTool bool
	// SearchImpl 模型的原生搜索实现类型（用于决定如何传参）
	SearchImpl SearchImpl
}

// ResolveSearchConfig 自适应搜索决策（移植自 LobeHub getSearchConfig）
//
// 决策逻辑：
//  1. searchMode == off          → 完全不搜索
//  2. 模型 internal 型            → 透明搜索，优先使用原生
//  3. 模型 params/tool 型 + 偏好  → 开启模型原生搜索（若当前 adapter 支持）
//  4. 只要搜索开启且模型支持 function calling，就同时挂载应用搜索工具兜底
//  5. searchMode == on（强制开启） → 只用系统应用搜索工具，关闭模型原生搜索
//     （internal 型原生透明不可关者除外）
//
// preferModelNativeSearch: 用户是否偏好使用模型自带搜索（对齐 useModelBuiltinSearch），
// 默认 true —— 即"有原生搜索就先用原生，不够时再让模型改用应用搜索工具"。
func ResolveSearchConfig(modelID, provider string, mode SearchMode, preferModelNativeSearch bool) SearchDecision {
	model := GetOrDefault(modelID, provider)
	return ResolveSearchConfigForCapabilities(
		model.Capabilities.SearchImpl,
		model.Capabilities.ToolUse,
		mode,
		preferModelNativeSearch,
	)
}

func ResolveSearchConfigForCapabilities(searchImpl SearchImpl, toolUse bool, mode SearchMode, preferModelNativeSearch bool) SearchDecision {
	enabledSearch := mode != SearchModeOff
	if !enabledSearch {
		return SearchDecision{
			EnabledSearch: false,
			SearchImpl:    searchImpl,
		}
	}

	// 计算是否使用模型原生搜索
	useModelNativeSearch := false
	switch searchImpl {
	case SearchImplInternal:
		// internal 型强制使用原生（透明，无法关闭）
		useModelNativeSearch = true
	case SearchImplParams, SearchImplTool:
		// params/tool 型：尊重用户偏好
		useModelNativeSearch = preferModelNativeSearch
	default:
		// 不支持原生搜索
		useModelNativeSearch = false
	}

	// on（强制开启）= 只用系统应用搜索工具，关闭模型原生搜索。
	// internal 型原生搜索透明不可关，无法满足"纯工具"，此时仍保留原生（兜底由工具补足）。
	if mode == SearchModeOn && searchImpl != SearchImplInternal {
		useModelNativeSearch = false
	}

	// 搜索开启时，只要模型支持工具调用，就保留应用搜索工具兜底。
	// 这样 params/internal 型模型可以先用原生；若结果不足，仍可转而调用 web_search / web_extract。
	useApplicationTool := enabledSearch && toolUse

	return SearchDecision{
		EnabledSearch:        enabledSearch,
		UseModelNativeSearch: useModelNativeSearch,
		UseApplicationTool:   useApplicationTool,
		SearchImpl:           searchImpl,
	}
}
