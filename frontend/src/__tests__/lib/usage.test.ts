import { describe, expect, it } from "vitest"
import { getCachedTokens, getCacheHitRate, getReasoningTokens } from "@/lib/usage"

describe("usage helpers", () => {
  it("reads flat streaming usage cache details", () => {
    const usage = {
      prompt_tokens: 100,
      completion_tokens: 20,
      total_tokens: 120,
      cached_tokens: 80,
      reasoning_tokens: 10,
    }

    expect(getCachedTokens(usage)).toBe(80)
    expect(getCacheHitRate(usage)).toBe(0.8)
    expect(getReasoningTokens(usage)).toBe(10)
  })

  it("reads nested persisted Eino usage cache details", () => {
    const usage = {
      prompt_tokens: 200,
      completion_tokens: 30,
      total_tokens: 230,
      prompt_token_details: { cached_tokens: 150 },
      completion_token_details: { reasoning_tokens: 12 },
    }

    expect(getCachedTokens(usage)).toBe(150)
    expect(getCacheHitRate(usage)).toBe(0.75)
    expect(getReasoningTokens(usage)).toBe(12)
  })
})
