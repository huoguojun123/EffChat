package modelbank

import (
	"strings"

	"github.com/huoguojun123/EffChat/internal/model"
)

// thinking format 是“运行时请求字段”的归一化层，不是 UI 标签层。
//
// 关键约束：provider 只说明请求走哪条 wire protocol，同一条 OpenAI-compatible
// 网关后面可能挂 DeepSeek、Qwen、Gemini-compatible 等完全不同模型族。因此解析
// 顺序必须是管理员显式配置 > model id 家族规则 > provider fallback，不能只按
// provider 猜。前端展示应消费这里解析后的结果，而不是另起一套能力判断。

// ThinkingFormat 是模型调用时使用的“官方思考参数格式”。
//
// 它和 provider 分开存储：provider 只表示请求走哪条通道，而同一个通道里可能
// 接入不同模型家族。例如 provider=openai 时，model_id 仍可能是 deepseek-v4，
// 这时应按 DeepSeek V4 的 OpenAI-compatible thinking 字段下发，而不是按
// OpenAI o 系列字段猜。
type ThinkingFormat string

const (
	ThinkingFormatAuto                  ThinkingFormat = "auto"
	ThinkingFormatNone                  ThinkingFormat = "none"
	ThinkingFormatOpenAIReasoningEffort ThinkingFormat = "openai_reasoning_effort"
	ThinkingFormatOpenAIGPT56           ThinkingFormat = "openai_gpt_5_6"
	ThinkingFormatDeepSeekV4            ThinkingFormat = "deepseek_v4"
	ThinkingFormatDeepSeekV4Disabled    ThinkingFormat = "deepseek_v4_disabled"
	ThinkingFormatDashScopeQwen         ThinkingFormat = "dashscope_qwen"
	ThinkingFormatAnthropicBudget       ThinkingFormat = "anthropic_budget"
	ThinkingFormatAnthropicAdaptive     ThinkingFormat = "anthropic_adaptive"
	ThinkingFormatGeminiThinking        ThinkingFormat = "gemini_thinking"
	ThinkingFormatXAIGrok               ThinkingFormat = "xai_grok"
	ThinkingFormatGLMThinking           ThinkingFormat = "glm_thinking"
	ThinkingFormatMiniMaxThinking       ThinkingFormat = "minimax_thinking"
	ThinkingFormatVolcengineThinking    ThinkingFormat = "volcengine_thinking"
)

// ThinkingEffort 是一次请求里用户选择的“思考投入”。
//
// 这里保留低/中/高/最大几个抽象档位，但它不直接暴露成统一产品语义；
// 真正下发前必须结合 ThinkingFormat 再转换成各家官方字段。
type ThinkingEffort string

const (
	ThinkingEffortAuto   ThinkingEffort = "auto"
	ThinkingEffortNone   ThinkingEffort = "none"
	ThinkingEffortLow    ThinkingEffort = "low"
	ThinkingEffortMedium ThinkingEffort = "medium"
	ThinkingEffortHigh   ThinkingEffort = "high"
	ThinkingEffortXHigh  ThinkingEffort = "xhigh"
	ThinkingEffortMax    ThinkingEffort = "max"
)

var validThinkingFormats = map[string]bool{
	string(ThinkingFormatAuto):                  true,
	string(ThinkingFormatNone):                  true,
	string(ThinkingFormatOpenAIReasoningEffort): true,
	string(ThinkingFormatOpenAIGPT56):           true,
	string(ThinkingFormatDeepSeekV4):            true,
	string(ThinkingFormatDeepSeekV4Disabled):    true,
	string(ThinkingFormatDashScopeQwen):         true,
	string(ThinkingFormatAnthropicBudget):       true,
	string(ThinkingFormatAnthropicAdaptive):     true,
	string(ThinkingFormatGeminiThinking):        true,
	string(ThinkingFormatXAIGrok):               true,
	string(ThinkingFormatGLMThinking):           true,
	string(ThinkingFormatMiniMaxThinking):       true,
	string(ThinkingFormatVolcengineThinking):    true,
}

var validThinkingEfforts = map[string]bool{
	string(ThinkingEffortAuto):   true,
	string(ThinkingEffortNone):   true,
	string(ThinkingEffortLow):    true,
	string(ThinkingEffortMedium): true,
	string(ThinkingEffortHigh):   true,
	string(ThinkingEffortXHigh):  true,
	string(ThinkingEffortMax):    true,
}

func IsValidThinkingFormat(format string) bool {
	return validThinkingFormats[NormalizeThinkingFormat(format)]
}

func IsValidThinkingEffort(effort string) bool {
	return validThinkingEfforts[NormalizeThinkingEffort(effort)]
}

func NormalizeThinkingFormat(format string) string {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		return string(ThinkingFormatAuto)
	}
	return format
}

func NormalizeThinkingEffort(effort string) string {
	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort == "" {
		return string(ThinkingEffortAuto)
	}
	return effort
}

// ResolveThinkingFormat 先尊重管理员显式选择；选择 auto 时再按 model_id 前缀/包含
// 规则推断。这里故意把 model_id 放在 provider 前面：OpenAI-compatible 网关里经常
// 把 DeepSeek、Qwen、xAI 等模型都挂在 provider=openai 下，单看 provider 会误判。
func ResolveThinkingFormat(provider, modelID, configured string, reasoning bool) ThinkingFormat {
	return ResolveThinkingFormatWithContext(provider, "", modelID, "", configured, reasoning)
}

// ResolveThinkingFormatWithContext 与 ResolveThinkingFormat 同源，但额外接收渠道 adapter
// 和显示名。管理员后台展示 runtime_profile 时必须使用这个入口：channel_key 可能是
// "my-gateway"，真正协议却是 google/anthropic；上游模型 ID 也可能很短，显示名才带
// Gemini/Claude/Qwen 等家族关键词。
func ResolveThinkingFormatWithContext(provider, adapter, modelID, displayName, configured string, reasoning bool) ThinkingFormat {
	normalizedConfigured := ThinkingFormat(NormalizeThinkingFormat(configured))
	if normalizedConfigured == ThinkingFormatNone {
		return normalizedConfigured
	}

	if normalizedConfigured != ThinkingFormatAuto && isThinkingFormatApplicableWithContext(provider, adapter, modelID, displayName, normalizedConfigured) {
		return normalizedConfigured
	}

	return inferThinkingFormatWithContext(provider, adapter, modelID, displayName, reasoning)
}

func inferThinkingFormatWithContext(provider, adapter, modelID, displayName string, reasoning bool) ThinkingFormat {
	p := normalizeProvider(provider)
	a := normalizeAdapter(adapter)
	id := normalizeModelID(strings.TrimSpace(modelID + " " + displayName))

	if isDeepSeekV4Model(id) {
		if reasoning {
			return ThinkingFormatDeepSeekV4
		}
		return ThinkingFormatDeepSeekV4Disabled
	}
	if isGeminiThinkingModel(id) || p == "google" || a == "google" {
		return ThinkingFormatGeminiThinking
	}
	if isAnthropicThinkingModel(p, id) || a == "anthropic" {
		if isAnthropicAdaptiveModel(id) {
			return ThinkingFormatAnthropicAdaptive
		}
		return ThinkingFormatAnthropicBudget
	}
	// These providers expose OpenAI-compatible wire APIs, but their thinking
	// controls are vendor-specific. Keep model-family detection ahead of the
	// generic OpenAI fallback so a gateway cannot silently receive GPT fields.
	if reasoning && isGrokReasoningModel(id) {
		return ThinkingFormatXAIGrok
	}
	if reasoning && isGLMThinkingModel(id) {
		return ThinkingFormatGLMThinking
	}
	if reasoning && isMiniMaxThinkingModel(id) {
		return ThinkingFormatMiniMaxThinking
	}
	if reasoning && isVolcengineThinkingModel(id) {
		return ThinkingFormatVolcengineThinking
	}
	if !reasoning {
		return ThinkingFormatNone
	}
	if isDashScopeQwenThinkingModel(id) {
		return ThinkingFormatDashScopeQwen
	}
	if isGPT56Model(id) {
		return ThinkingFormatOpenAIGPT56
	}
	if isOpenAIReasoningModel(p, id) {
		return ThinkingFormatOpenAIReasoningEffort
	}

	return ThinkingFormatNone
}

// ResolveThinkingEffort 将“本轮选择”归一化为当前 format 能接受的档位。
//
// 注意：这一步只处理选择合法性和默认值，不直接生成请求字段。不同 provider 的
// 参数形状在 agent/thinking_format.go 中处理，避免 modelbank 依赖 SDK 类型。
func ResolveThinkingEffort(format ThinkingFormat, requested string) ThinkingEffort {
	return ResolveThinkingEffortForModel(format, "", requested)
}

func ResolveThinkingEffortForModel(format ThinkingFormat, modelID, requested string) ThinkingEffort {
	effort := ThinkingEffort(NormalizeThinkingEffort(requested))
	if !validThinkingEfforts[string(effort)] {
		effort = ThinkingEffortAuto
	}

	switch format {
	case ThinkingFormatOpenAIGPT56:
		switch effort {
		case ThinkingEffortNone, ThinkingEffortLow, ThinkingEffortMedium, ThinkingEffortHigh, ThinkingEffortXHigh, ThinkingEffortMax:
			return effort
		default:
			return ThinkingEffortMedium
		}
	case ThinkingFormatOpenAIReasoningEffort:
		if effort == ThinkingEffortLow || effort == ThinkingEffortMedium || effort == ThinkingEffortHigh {
			return effort
		}
		return ThinkingEffortMedium
	case ThinkingFormatDeepSeekV4:
		if effort == ThinkingEffortMax {
			return ThinkingEffortMax
		}
		return ThinkingEffortHigh
	case ThinkingFormatDashScopeQwen, ThinkingFormatGeminiThinking, ThinkingFormatAnthropicBudget, ThinkingFormatAnthropicAdaptive:
		if effort == ThinkingEffortLow || effort == ThinkingEffortMedium || effort == ThinkingEffortHigh {
			return effort
		}
		return ThinkingEffortMedium
	case ThinkingFormatXAIGrok:
		if effort == ThinkingEffortLow || effort == ThinkingEffortMedium || effort == ThinkingEffortHigh {
			return effort
		}
		// Grok's ordinary reasoning models cannot disable reasoning; xAI defaults
		// these requests to high, so mirror that instead of inventing an off mode.
		return ThinkingEffortHigh
	case ThinkingFormatGLMThinking:
		if effort == ThinkingEffortNone {
			return ThinkingEffortNone
		}
		return ThinkingEffortHigh
	case ThinkingFormatMiniMaxThinking:
		if isMiniMaxM2Model(normalizeModelID(modelID)) {
			// MiniMax M2.x keeps thinking on. There is no valid user-controlled
			// budget, so callers receive no effort selector for this family.
			return ThinkingEffortAuto
		}
		if effort == ThinkingEffortNone {
			return ThinkingEffortNone
		}
		return ThinkingEffortMedium
	case ThinkingFormatVolcengineThinking:
		if effort == ThinkingEffortNone || effort == ThinkingEffortLow || effort == ThinkingEffortMedium || effort == ThinkingEffortHigh {
			return effort
		}
		return ThinkingEffortMedium
	default:
		return ThinkingEffortAuto
	}
}

func ApplyThinkingRuntimeMetadata(m *model.Model) *model.Model {
	return ApplyRuntimeProfile(m)
}

func ApplyThinkingRuntimeMetadataWithAdapter(m *model.Model, adapter string) *model.Model {
	return ApplyRuntimeProfileWithAdapter(m, adapter)
}

func ThinkingEffortOptions(format ThinkingFormat) []model.ThinkingEffortOption {
	return ThinkingEffortOptionsForModel(format, "")
}

func ThinkingEffortOptionsForModel(format ThinkingFormat, modelID string) []model.ThinkingEffortOption {
	switch format {
	case ThinkingFormatOpenAIGPT56:
		return []model.ThinkingEffortOption{
			{Value: string(ThinkingEffortNone), Label: "关闭", Description: "不使用额外推理，优先最低延迟。"},
			{Value: string(ThinkingEffortLow), Label: "低", Description: "适合快速问答、检索和轻量工具调用。"},
			{Value: string(ThinkingEffortMedium), Label: "中", Description: "GPT-5.6 默认档位，平衡质量与延迟。"},
			{Value: string(ThinkingEffortHigh), Label: "高", Description: "适合复杂分析、调试和多步任务。"},
			{Value: string(ThinkingEffortXHigh), Label: "极高", Description: "适合质量优先的深度研究和长任务。"},
			{Value: string(ThinkingEffortMax), Label: "最大", Description: "仅用于最困难、可接受高延迟的任务。"},
		}
	case ThinkingFormatOpenAIReasoningEffort:
		return []model.ThinkingEffortOption{
			{Value: string(ThinkingEffortLow), Label: "低", Description: "适用 gpt-5/gpt-5.x、gpt-oss、o 系列；下发 reasoning_effort=low，优先速度。"},
			{Value: string(ThinkingEffortMedium), Label: "中", Description: "适用 gpt-5/gpt-5.x、gpt-oss、o 系列；下发 reasoning_effort=medium，平衡质量与延迟。"},
			{Value: string(ThinkingEffortHigh), Label: "高", Description: "适用 gpt-5/gpt-5.x、gpt-oss、o 系列；下发 reasoning_effort=high，用于复杂推理。"},
		}
	case ThinkingFormatDeepSeekV4:
		return []model.ThinkingEffortOption{
			{Value: string(ThinkingEffortHigh), Label: "High", Description: "适用 deepseek-v4 / deepseek_v4；下发 thinking.type=enabled + reasoning_effort=high。"},
			{Value: string(ThinkingEffortMax), Label: "Max", Description: "适用 deepseek-v4 / deepseek_v4；下发 thinking.type=enabled + reasoning_effort=max。"},
		}
	case ThinkingFormatDashScopeQwen:
		return []model.ThinkingEffortOption{
			{Value: string(ThinkingEffortLow), Label: "短", Description: "适用 QwQ、Qwen3/3.5/3.6/3.7；下发 enable_thinking + thinking_budget=1024。"},
			{Value: string(ThinkingEffortMedium), Label: "中", Description: "适用 QwQ、Qwen3/3.5/3.6/3.7；下发 enable_thinking + thinking_budget=4096。"},
			{Value: string(ThinkingEffortHigh), Label: "长", Description: "适用 QwQ、Qwen3/3.5/3.6/3.7；下发 enable_thinking + thinking_budget=8192。"},
		}
	case ThinkingFormatGeminiThinking:
		return []model.ThinkingEffortOption{
			{Value: string(ThinkingEffortLow), Label: "短", Description: "适用 Gemini 2.5/3/3.1/3.5；当前下发 ThinkingConfig.thinkingBudget=1024，暂不迁移 thinkingLevel。"},
			{Value: string(ThinkingEffortMedium), Label: "中", Description: "适用 Gemini 2.5/3/3.1/3.5；当前下发 ThinkingConfig.thinkingBudget=4096，暂不迁移 thinkingLevel。"},
			{Value: string(ThinkingEffortHigh), Label: "长", Description: "适用 Gemini 2.5/3/3.1/3.5；当前下发 ThinkingConfig.thinkingBudget=8192，暂不迁移 thinkingLevel。"},
		}
	case ThinkingFormatAnthropicBudget:
		return []model.ThinkingEffortOption{
			{Value: string(ThinkingEffortLow), Label: "4k", Description: "适用 Claude 4.5 及更早 manual budget_tokens；设置 thinking.budget_tokens=4096。"},
			{Value: string(ThinkingEffortMedium), Label: "8k", Description: "适用 Claude 4.5 及更早 manual budget_tokens；设置 thinking.budget_tokens=8192。"},
			{Value: string(ThinkingEffortHigh), Label: "16k", Description: "适用 Claude 4.5 及更早 manual budget_tokens；设置 thinking.budget_tokens=16000。"},
		}
	case ThinkingFormatAnthropicAdaptive:
		return []model.ThinkingEffortOption{
			{Value: string(ThinkingEffortLow), Label: "低", Description: "适用 Claude 4.6+/5/Fable/Mythos；下发 thinking.type=adaptive + output_config.effort=low。"},
			{Value: string(ThinkingEffortMedium), Label: "中", Description: "适用 Claude 4.6+/5/Fable/Mythos；下发 thinking.type=adaptive + output_config.effort=medium。"},
			{Value: string(ThinkingEffortHigh), Label: "高", Description: "适用 Claude 4.6+/5/Fable/Mythos；下发 thinking.type=adaptive + output_config.effort=high。"},
		}
	case ThinkingFormatXAIGrok:
		return []model.ThinkingEffortOption{
			{Value: string(ThinkingEffortLow), Label: "低", Description: "适用 Grok 4.5 等标准推理模型；下发 reasoning_effort=low。"},
			{Value: string(ThinkingEffortMedium), Label: "中", Description: "适用 Grok 4.5 等标准推理模型；下发 reasoning_effort=medium。"},
			{Value: string(ThinkingEffortHigh), Label: "高", Description: "xAI 默认档位；下发 reasoning_effort=high。"},
		}
	case ThinkingFormatGLMThinking:
		return []model.ThinkingEffortOption{
			{Value: string(ThinkingEffortNone), Label: "关闭", Description: "适用 GLM 4.5+；下发 thinking.type=disabled。"},
			{Value: string(ThinkingEffortHigh), Label: "开启", Description: "适用 GLM 4.5+；下发 thinking.type=enabled。"},
		}
	case ThinkingFormatMiniMaxThinking:
		if isMiniMaxM2Model(normalizeModelID(modelID)) {
			return nil
		}
		return []model.ThinkingEffortOption{
			{Value: string(ThinkingEffortNone), Label: "关闭", Description: "适用 MiniMax M3；下发 thinking.type=disabled。"},
			{Value: string(ThinkingEffortMedium), Label: "自适应", Description: "适用 MiniMax M3；下发 thinking.type=adaptive，并分离 reasoning_content。"},
		}
	case ThinkingFormatVolcengineThinking:
		return []model.ThinkingEffortOption{
			{Value: string(ThinkingEffortNone), Label: "关闭", Description: "适用豆包 Seed / 方舟推理模型；下发 thinking.type=disabled。"},
			{Value: string(ThinkingEffortLow), Label: "低", Description: "适用豆包 Seed / 方舟推理模型；下发 thinking.type=enabled + reasoning_effort=low。"},
			{Value: string(ThinkingEffortMedium), Label: "中", Description: "适用豆包 Seed / 方舟推理模型；下发 thinking.type=enabled + reasoning_effort=medium。"},
			{Value: string(ThinkingEffortHigh), Label: "高", Description: "适用豆包 Seed / 方舟推理模型；下发 thinking.type=enabled + reasoning_effort=high。"},
		}
	default:
		return nil
	}
}

// isThinkingFormatApplicableWithContext 判断管理员手选的格式是否能用于当前模型。
//
// 选错格式时不报错也不伪装成功，而是让 ResolveThinkingFormat 回退到 auto 推断。
// 这里同时检查 provider 协议和 model_id 家族：OpenAI-compatible 通道能承载多家模型，
// 但把 DeepSeek V4 字段发给普通 GPT 模型仍会制造 400，因此也要看模型 ID。
func isThinkingFormatApplicableWithContext(provider, adapter, modelID, displayName string, format ThinkingFormat) bool {
	p := normalizeProvider(provider)
	a := normalizeAdapter(adapter)
	id := normalizeModelID(strings.TrimSpace(modelID + " " + displayName))
	switch format {
	case ThinkingFormatOpenAIGPT56:
		return a != "anthropic" && a != "google" && isGPT56Model(id)
	case ThinkingFormatOpenAIReasoningEffort:
		return a != "anthropic" && a != "google" && !isGPT56Model(id) && isOpenAIReasoningModel(p, id)
	case ThinkingFormatDeepSeekV4, ThinkingFormatDeepSeekV4Disabled:
		return isDeepSeekV4Model(id)
	case ThinkingFormatDashScopeQwen:
		return isDashScopeQwenThinkingModel(id)
	case ThinkingFormatAnthropicBudget, ThinkingFormatAnthropicAdaptive:
		return a == "anthropic" || isAnthropicThinkingModel(p, id)
	case ThinkingFormatGeminiThinking:
		return a == "google" || p == "google" || isGeminiThinkingModel(id)
	case ThinkingFormatXAIGrok:
		return isGrokReasoningModel(id)
	case ThinkingFormatGLMThinking:
		return isGLMThinkingModel(id)
	case ThinkingFormatMiniMaxThinking:
		return isMiniMaxThinkingModel(id)
	case ThinkingFormatVolcengineThinking:
		return isVolcengineThinkingModel(id)
	default:
		return false
	}
}

func IsOpenAICompatibleProvider(provider string) bool {
	switch normalizeProvider(provider) {
	case "openai", "perplexity", "deepseek", "xai", "qwen", "dashscope", "mistral", "moonshot", "zhipu", "minimax", "volcengine":
		return true
	default:
		return false
	}
}

func normalizeProvider(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if strings.Contains(provider, "gemini") || strings.Contains(provider, "google") {
		return "google"
	}
	if strings.Contains(provider, "claude") || strings.Contains(provider, "anthropic") {
		return "anthropic"
	}
	return provider
}

func normalizeAdapter(adapter string) string {
	adapter = strings.ToLower(strings.TrimSpace(adapter))
	switch adapter {
	case "google", "gemini":
		return "google"
	case "anthropic", "claude":
		return "anthropic"
	case "openai_compatible", "openai-compatible", "openai":
		return "openai_compatible"
	default:
		return adapter
	}
}

func normalizeModelID(modelID string) string {
	return strings.ToLower(strings.TrimSpace(modelID))
}

func isDeepSeekV4Model(id string) bool {
	return strings.Contains(id, "deepseek-v4") || strings.Contains(id, "deepseek_v4")
}

func isDashScopeQwenThinkingModel(id string) bool {
	return strings.Contains(id, "qwq") ||
		strings.Contains(id, "qwen3") ||
		strings.Contains(id, "qwen-3")
}

func isOpenAIReasoningModel(provider, id string) bool {
	if provider == "anthropic" || provider == "google" {
		return false
	}
	return isOpenAIReasoningModelID(id)
}

func isOpenAIReasoningModelID(id string) bool {
	return strings.HasPrefix(id, "o1") ||
		strings.HasPrefix(id, "o3") ||
		strings.HasPrefix(id, "o4") ||
		strings.Contains(id, "gpt-5") ||
		strings.Contains(id, "gpt-oss")
}

func isGPT56Model(id string) bool {
	return strings.Contains(id, "gpt-5.6")
}

func isGrokReasoningModel(id string) bool {
	// grok-4.20-multi-agent uses a different agent-count control. Treat it as
	// unsupported here rather than sending a standard reasoning budget to it.
	return strings.Contains(id, "grok-") && !strings.Contains(id, "multi-agent")
}

func isGLMThinkingModel(id string) bool {
	return strings.HasPrefix(id, "glm-") || strings.Contains(id, "/glm-") || strings.Contains(id, "zai/glm")
}

func isMiniMaxThinkingModel(id string) bool {
	return strings.Contains(id, "minimax-m3") || isMiniMaxM2Model(id)
}

func isMiniMaxM2Model(id string) bool {
	return strings.Contains(id, "minimax-m2")
}

func isVolcengineThinkingModel(id string) bool {
	return strings.Contains(id, "doubao") || strings.Contains(id, "doubao-seed") ||
		strings.Contains(id, "seed-1") || strings.Contains(id, "seed-2")
}

// IsKnownThinkingModel lets catalog import safely mark model families whose
// official APIs expose reasoning controls even when an upstream /models reply
// omits that capability bit.
func IsKnownThinkingModel(provider, modelID, displayName string) bool {
	p := normalizeProvider(provider)
	id := normalizeModelID(strings.TrimSpace(modelID + " " + displayName))
	return isDeepSeekV4Model(id) || isGeminiThinkingModel(id) ||
		isAnthropicThinkingModel(p, id) || isDashScopeQwenThinkingModel(id) ||
		isOpenAIReasoningModel(p, id) || isGrokReasoningModel(id) ||
		isGLMThinkingModel(id) || isMiniMaxThinkingModel(id) ||
		isVolcengineThinkingModel(id)
}

func isGeminiThinkingModel(id string) bool {
	return strings.Contains(id, "gemini")
}

func isAnthropicThinkingModel(provider, id string) bool {
	return provider == "anthropic" || strings.Contains(id, "claude")
}

func isAnthropicAdaptiveModel(id string) bool {
	return strings.Contains(id, "claude-4.6") ||
		strings.Contains(id, "claude4.6") ||
		strings.Contains(id, "claude-5") ||
		strings.Contains(id, "claude5") ||
		strings.Contains(id, "fable") ||
		strings.Contains(id, "mythos")
}
