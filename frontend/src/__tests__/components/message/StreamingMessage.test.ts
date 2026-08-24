import { describe, expect, it } from "vitest"
import { formatRetryDelay } from "@/lib/streamingRetry"

describe("formatRetryDelay", () => {
  it("shows a live retry state at zero", () => {
    expect(formatRetryDelay(0)).toBe("正在重新请求")
  })

  it("keeps sub-second values readable", () => {
    expect(formatRetryDelay(450)).toBe("0.5 秒")
  })

  it("keeps fractional seconds visible while waiting", () => {
    expect(formatRetryDelay(1500)).toBe("1.5 秒")
  })
})
