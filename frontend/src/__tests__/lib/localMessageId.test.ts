import { describe, expect, it } from "vitest"
import { createLocalMessageId } from "@/lib/localMessageId"

describe("local message ids", () => {
  it("returns unique negative numeric ids for optimistic messages", () => {
    const first = createLocalMessageId()
    const second = createLocalMessageId()

    expect(first).toBeLessThan(0)
    expect(second).toBeLessThan(0)
    expect(first).not.toBe(second)
  })
})
