import type { ReasoningStateSource, ReasoningViewState } from "@/types"

export function nextReasoningState(
  current: ReasoningViewState | undefined,
  open: boolean,
  source: ReasoningStateSource
): ReasoningViewState {
  return {
    open,
    touchedByUser: source === "user" ? true : (current?.touchedByUser ?? false),
    autoCollapsing: false,
  }
}

export function nextAutoCollapseState(current: ReasoningViewState | undefined) {
  if (!current || current.touchedByUser) return current
  return {
    ...current,
    autoCollapsing: true,
  }
}
