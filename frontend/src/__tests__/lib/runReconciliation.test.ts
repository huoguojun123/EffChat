import { beforeEach, describe, expect, it, vi } from "vitest"

const mocks = vi.hoisted(() => ({ getRunStatus: vi.fn() }))

vi.mock("@/api/runs", () => ({ getRunStatus: mocks.getRunStatus }))

import { isUnrecoverableRunStatusError, waitForDelay, waitForRunAppearance, waitForRunSettlement } from "@/lib/runReconciliation"

describe("run reconciliation helpers", () => {
  beforeEach(() => {
    mocks.getRunStatus.mockReset()
  })

  it("recognizes terminal runs for acceptance and stop reconciliation", async () => {
    const run = { run_id: "run-1", session_id: 7, status: "completed" }
    mocks.getRunStatus.mockResolvedValue({ run })

    await expect(waitForRunAppearance(7, "run-1", new AbortController().signal)).resolves.toEqual(run)
    await expect(waitForRunSettlement(7, "run-1")).resolves.toEqual(run)
  })

  it("classifies only authentication and missing-run statuses as unrecoverable", () => {
    expect(isUnrecoverableRunStatusError({ status: 401 })).toBe(true)
    expect(isUnrecoverableRunStatusError({ status: 404 })).toBe(true)
    expect(isUnrecoverableRunStatusError({ status: 500 })).toBe(false)
    expect(isUnrecoverableRunStatusError(new Error("network"))).toBe(false)
  })

  it("releases an aborted delay immediately", async () => {
    const controller = new AbortController()
    const pending = waitForDelay(60_000, controller.signal)
    controller.abort()
    await expect(pending).resolves.toBeUndefined()
  })
})
