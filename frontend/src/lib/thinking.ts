import type { Model, ThinkingEffortOption } from "@/types"

export type ThinkingEffort = "auto" | "none" | "low" | "medium" | "high" | "xhigh" | "max"

const VALID_EFFORTS = new Set(["auto", "none", "low", "medium", "high", "xhigh", "max"])

export function resolvedThinkingFormat(model?: Model | null): string {
  return model?.runtime_profile?.thinking_format || model?.resolved_thinking_format || "none"
}

export function thinkingEffortOptions(model?: Model | null): ThinkingEffortOption[] {
  return model?.runtime_profile?.thinking_effort_options || model?.thinking_effort_options || []
}

export function defaultThinkingEffort(model?: Model | null): ThinkingEffort {
  const options = thinkingEffortOptions(model)
  if (options.length === 0) return "auto"
  const backendDefault = normalizeEffortValue(model?.runtime_profile?.default_thinking_effort || model?.default_thinking_effort)
  if (options.some((option) => option.value === backendDefault)) return backendDefault
  const first = normalizeEffortValue(options[0]?.value)
  return first === "auto" ? "medium" : first
}

export function normalizeThinkingEffort(model: Model | null | undefined, effort: string | null | undefined): ThinkingEffort {
  const options = thinkingEffortOptions(model)
  if (options.length === 0) return "auto"
  const value = normalizeEffortValue(effort)
  if (options.some((option) => option.value === value)) return value
  return defaultThinkingEffort(model)
}

function normalizeEffortValue(effort: string | null | undefined): ThinkingEffort {
  const value = (effort || "").toLowerCase().trim()
  return VALID_EFFORTS.has(value) ? (value as ThinkingEffort) : "auto"
}
