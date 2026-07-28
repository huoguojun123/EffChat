import { useState } from "react"
import type { Model } from "@/types"
import { normalizeThinkingEffort, thinkingEffortOptions, type ThinkingEffort } from "@/lib/thinking"

const THINKING_EFFORT_STORAGE_PREFIX = "fchat_thinking_effort:"

export function useThinkingEffortSelection(currentModel?: Model) {
  const [thinkingEffortByModel, setThinkingEffortByModel] = useState<Record<string, ThinkingEffort>>({})
  const options = thinkingEffortOptions(currentModel)
  const savedEffort = currentModel ? thinkingEffortByModel[currentModel.id] ?? localStorage.getItem(`${THINKING_EFFORT_STORAGE_PREFIX}${currentModel.id}`) : undefined
  const effort = normalizeThinkingEffort(currentModel, savedEffort)
  const active = options.length > 0
  const label = options.find((item) => item.value === effort)?.label || ""

  function setEffort(next: string) {
    if (!currentModel) return
    const normalized = normalizeThinkingEffort(currentModel, next)
    localStorage.setItem(`${THINKING_EFFORT_STORAGE_PREFIX}${currentModel.id}`, normalized)
    setThinkingEffortByModel((prev) => ({ ...prev, [currentModel.id]: normalized }))
  }

  return {
    thinkingOptions: options,
    thinkingEffort: effort,
    thinkingActive: active,
    thinkingLabel: label,
    setThinkingEffort: setEffort,
  }
}
