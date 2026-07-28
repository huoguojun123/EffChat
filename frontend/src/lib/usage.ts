import type { Usage } from "@/types"

export function getCachedTokens(usage?: Usage) {
  if (!usage) return 0
  return usage.cached_tokens ?? usage.prompt_token_details?.cached_tokens ?? 0
}

export function getReasoningTokens(usage?: Usage) {
  if (!usage) return 0
  return usage.reasoning_tokens ?? usage.completion_token_details?.reasoning_tokens ?? 0
}

export function getCacheHitRate(usage?: Usage) {
  const promptTokens = usage?.prompt_tokens || 0
  if (promptTokens <= 0) return 0
  return getCachedTokens(usage) / promptTokens
}
