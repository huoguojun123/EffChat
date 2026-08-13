import { describe, expect, it } from "vitest"
import { LatestOperationOwner } from "@/lib/latestOperation"

describe("LatestOperationOwner", () => {
  it("prevents an older failure or finally from owning newer state", () => {
    const owner = new LatestOperationOwner()
    const older = owner.begin()
    const current = owner.begin()

    expect(older.signal.aborted).toBe(true)
    expect(owner.owns(older)).toBe(false)
    expect(owner.release(older)).toBe(false)
    expect(owner.owns(current)).toBe(true)
    expect(owner.release(current)).toBe(true)
  })

  it("invalidates the active operation when its surface closes", () => {
    const owner = new LatestOperationOwner()
    const operation = owner.begin()

    owner.cancel()

    expect(operation.signal.aborted).toBe(true)
    expect(owner.owns(operation)).toBe(false)
    expect(owner.release(operation)).toBe(false)
  })
})
