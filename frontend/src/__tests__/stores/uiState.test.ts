import { describe, expect, it } from "vitest"
import { nextAutoCollapseState, nextReasoningState } from "@/stores/reasoningState"

describe("reasoning state helpers", () => {
  it("system 更新不应覆盖用户已触碰状态", () => {
    const userTouched = nextReasoningState(undefined, true, "user")
    const next = nextReasoningState(userTouched, false, "system")

    expect(next.open).toBe(false)
    expect(next.touchedByUser).toBe(true)
    expect(next.autoCollapsing).toBe(false)
  })

  it("自动收起只标记未被用户触碰的块", () => {
    const systemOpened = nextReasoningState(undefined, true, "system")
    const userOpened = nextReasoningState(undefined, true, "user")

    expect(nextAutoCollapseState(systemOpened)?.autoCollapsing).toBe(true)
    expect(nextAutoCollapseState(userOpened)?.autoCollapsing).toBe(false)
  })
})
