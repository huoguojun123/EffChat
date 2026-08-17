import type { ModelDraft } from "./AdminModelsPanel.types"

export const baseProviderOptions = ["openai", "anthropic", "google", "deepseek", "perplexity", "xai", "qwen", "dashscope", "mistral", "moonshot", "zhipu", "minimax", "volcengine"]

// These values are persisted and interpreted by the backend adapter layer, so labels can change but values must remain stable.
export const thinkingFormatOptions = [
  { value: "auto", label: "自动推荐", hint: "按模型名和渠道关键词匹配；GPT、Claude、Gemini、Qwen、DeepSeek、Grok、GLM、MiniMax、豆包会自动走对应格式。" },
  { value: "none", label: "关闭", hint: "适合普通非推理模型，或需要完全交给上游默认行为的模型。" },
  { value: "openai_reasoning_effort", label: "OpenAI reasoning", hint: "适用 GPT-5.5 及更早 GPT-5、gpt-oss、o 系列；下发三档 reasoning_effort。" },
  { value: "openai_gpt_5_6", label: "GPT-5.6 reasoning", hint: "适用 GPT-5.6 系列；下发独立的六档 reasoning_effort 和 max_completion_tokens。" },
  { value: "anthropic_budget", label: "Claude thinking", hint: "适用 Claude 4.5 及更早 manual thinking；下发 thinking.type=enabled + budget_tokens。" },
  { value: "anthropic_adaptive", label: "Claude adaptive thinking", hint: "适用 Claude 4.6+、Claude 5、Fable/Mythos；按型号下发 adaptive thinking、summarized display 与 effort。" },
  { value: "gemini_thinking", label: "Gemini thinking", hint: "Gemini 2.5 使用 thinkingBudget；Gemini 3.x 使用 thinkingLevel。" },
  { value: "dashscope_qwen", label: "Qwen thinking", hint: "适用 QwQ、Qwen3/3.5/3.6/3.7；下发 enable_thinking + thinking_budget。" },
  { value: "deepseek_v4", label: "DeepSeek thinking", hint: "适用 deepseek-v4 / deepseek_v4；下发 thinking.type=enabled + reasoning_effort=low/high/max。" },
  { value: "deepseek_v4_disabled", label: "DeepSeek 关闭思考", hint: "适用 DeepSeek V4 双模式模型；下发 thinking.type=disabled，不展示思考预算。" },
  { value: "xai_grok", label: "Grok reasoning", hint: "适用 Grok 标准推理模型；下发 low/medium/high，4.6 等已核验型号额外支持 xhigh。" },
  { value: "glm_thinking", label: "GLM thinking", hint: "适用 GLM 4.5+；下发 thinking.type=enabled 或 disabled。" },
  { value: "minimax_thinking", label: "MiniMax thinking", hint: "M3 支持 adaptive/disabled；M2 系列固定思考并分离 reasoning_content。" },
  { value: "volcengine_thinking", label: "Doubao / Ark thinking", hint: "适用豆包 Seed / 方舟推理模型；下发 thinking.type 与 reasoning_effort。" },
]

export const providerLabels: Record<string, string> = {
  openai: "OpenAI",
  anthropic: "Anthropic",
  google: "Google",
  deepseek: "DeepSeek",
  perplexity: "Perplexity",
  xai: "xAI",
  qwen: "Qwen",
  dashscope: "DashScope",
  mistral: "Mistral",
  moonshot: "Moonshot",
  zhipu: "Zhipu",
  minimax: "MiniMax",
  volcengine: "Volcengine",
}

export const providerDefaults: Record<string, Partial<ModelDraft>> = {
  openai: { vision: true, tool_use: true, reasoning: true, search_impl: "", context_window: 0, max_output: 0 },
  anthropic: { vision: true, tool_use: true, reasoning: true, search_impl: "tool", context_window: 1000000, max_output: 128000 },
  google: { vision: true, tool_use: true, reasoning: true, search_impl: "params", context_window: 1048576, max_output: 65536 },
  deepseek: { vision: false, tool_use: true, reasoning: true, search_impl: "", context_window: 1000000, max_output: 384000 },
  perplexity: { vision: false, tool_use: false, reasoning: false, search_impl: "internal", context_window: 0, max_output: 0 },
  xai: { vision: true, tool_use: true, reasoning: true, search_impl: "", context_window: 0, max_output: 0 },
  qwen: { vision: true, tool_use: true, reasoning: true, search_impl: "params", context_window: 0, max_output: 0 },
  dashscope: { vision: true, tool_use: true, reasoning: true, search_impl: "params", context_window: 0, max_output: 0 },
}
