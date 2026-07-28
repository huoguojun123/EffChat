import { getRunStatus } from "@/api/runs"
import type { DurableRunStatus } from "@/types"

const stopReconciliationDelayMs = 300
const stopReconciliationWindowMs = 7200
const stopStatusRequestTimeoutMs = 1000
const initialAcceptanceWindowMs = 2000
const initialAcceptanceDelayMs = 200
const initialAcceptanceRequestTimeoutMs = 750

export function waitForDelay(delayMs: number, signal: AbortSignal) {
  if (signal.aborted) return Promise.resolve()
  return new Promise<void>((resolve) => {
    const timer = setTimeout(done, delayMs)
    function done() {
      clearTimeout(timer)
      signal.removeEventListener("abort", done)
      resolve()
    }
    signal.addEventListener("abort", done, { once: true })
  })
}

export function isUnrecoverableRunStatusError(error: unknown) {
  if (!error || typeof error !== "object") return false
  const status = (error as { status?: unknown }).status
  return status === 401 || status === 403 || status === 404
}

export async function waitForRunSettlement(sessionId: number, runId: string): Promise<DurableRunStatus | null> {
  const deadline = Date.now() + stopReconciliationWindowMs
  while (Date.now() < deadline) {
    try {
      const remaining = deadline - Date.now()
      const { run } = await getRunStatus(sessionId, runId, Math.min(remaining, stopStatusRequestTimeoutMs))
      if (run.run_id === runId && run.status !== "running") return run
    } catch {
      // A failed status probe cannot establish that the terminal transaction committed.
    }
    const delay = Math.min(stopReconciliationDelayMs, deadline - Date.now())
    if (delay > 0) await new Promise((resolve) => setTimeout(resolve, delay))
  }
  return null
}

export async function waitForRunAppearance(sessionId: number, runId: string, signal: AbortSignal): Promise<DurableRunStatus | null> {
  const deadline = Date.now() + initialAcceptanceWindowMs
  while (!signal.aborted && Date.now() < deadline) {
    try {
      const remaining = deadline - Date.now()
      const { run } = await getRunStatus(sessionId, runId, Math.min(remaining, initialAcceptanceRequestTimeoutMs))
      if (run.run_id === runId) return run
    } catch {
      // The request may have reached the server before its durable run record becomes visible.
    }
    const delay = Math.min(initialAcceptanceDelayMs, deadline - Date.now())
    if (delay > 0) await waitForDelay(delay, signal)
  }
  return null
}
