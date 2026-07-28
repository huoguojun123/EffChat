import type { AssistantSegment } from "@/types"

export interface ReasoningGroup {
  segments: AssistantSegment[]
}

export interface AssistantMessageRow {
  reasoning?: ReasoningGroup
  content?: string
}

export function groupAssistantSegments(segments: AssistantSegment[]) {
  const rows: AssistantMessageRow[] = []
  let pending: AssistantSegment[] = []

  function flushReasoning() {
    if (pending.length === 0) return
    rows.push({ reasoning: { segments: pending } })
    pending = []
  }

  for (const segment of segments) {
    if (segment.thinking?.trim() || segment.tool_calls?.length) {
      pending.push({
        thinking: segment.thinking,
        tool_calls: segment.tool_calls,
      })
    }
    if (segment.content?.trim()) {
      flushReasoning()
      rows.push({ content: segment.content })
    }
  }
  flushReasoning()

  return rows
}
