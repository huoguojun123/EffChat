import { api } from "./client"
import type { ActiveRunSnapshot, DurableRunStatus } from "@/types"

export function getActiveRun(sessionId: number) {
  return api.get<{ run: ActiveRunSnapshot | null }>(`/sessions/${sessionId}/runs/active`)
}

export function getRunStatus(sessionId: number, runId: string, timeoutMs?: number) {
  return api.get<{ run: DurableRunStatus }>(
    `/sessions/${sessionId}/runs/${encodeURIComponent(runId)}`,
    timeoutMs ? { timeoutMs } : undefined
  )
}
