import { describe, expect, it } from "vitest"
import { UsageQueryOwnership } from "@/components/admin/usageQueryOwnership"

describe("UsageQueryOwnership", () => {
  it("rejects a late response after refresh A, range B, and range A", () => {
    const owner = new UsageQueryOwnership()
    const refreshA = owner.begin("range:today")
    owner.begin("range:7d")
    owner.begin("range:today")

    expect(owner.owns(refreshA)).toBe(false)
  })

  it("accepts only the last same-query refresh", () => {
    const owner = new UsageQueryOwnership()
    const first = owner.begin("range:today")
    const last = owner.begin("range:today")

    expect(owner.owns(first)).toBe(false)
    expect(owner.owns(last)).toBe(true)
  })

  it("invalidates custom-date responses when the selected dates change", () => {
    const owner = new UsageQueryOwnership()
    const oldDates = owner.begin("custom:2026-08-01:2026-08-07")
    owner.activate("custom:2026-08-02:2026-08-07")

    expect(owner.owns(oldDates)).toBe(false)
  })
})
