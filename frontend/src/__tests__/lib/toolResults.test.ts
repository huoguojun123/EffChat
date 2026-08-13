import { describe, expect, it } from "vitest"
import { cleanUrl, extractResultWarning, parseToolFailure } from "@/lib/toolResults"

describe("tool result helpers", () => {
  it("parses structured tool governance failures", () => {
    const failure = parseToolFailure(JSON.stringify({
      ok: false,
      tool: "web_search",
      error: "upstream unavailable",
      retryable: true,
      source: "tool_governance",
    }))

    expect(failure?.tool).toBe("web_search")
    expect(failure?.retryable).toBe(true)
  })

  it("normalizes web tool failures into the stable tool failure shape", () => {
    const failure = parseToolFailure(JSON.stringify({
      search_failed: true,
      error_code: "upstream_unavailable",
      error: "联网搜索服务暂时不可用",
      retryable: true,
    }))

    expect(failure).toMatchObject({
      ok: false,
      code: "upstream_unavailable",
      retryable: true,
    })
  })

  it("only keeps HTTP(S) links from tool results", () => {
    expect(cleanUrl("javascript:alert(1)")).toBe("")
    expect(cleanUrl("file:///etc/passwd")).toBe("")
    expect(cleanUrl("https://example.com/source")).toBe("https://example.com/source")
  })

  it.each([
    ["refinement_disabled", "未启用模型提炼，显示抓取原文"],
    ["refinement_unavailable", "提炼模型当前不可用，显示抓取原文"],
    ["refinement_cooldown", "提炼服务暂时冷却，显示抓取原文"],
    ["refinement_failed", "模型提炼未完成，显示抓取原文"],
    ["source_truncated", "网页原文过长，仅保留部分内容"],
  ])("maps %s to a stable user-facing warning", (reason, message) => {
    expect(extractResultWarning({ degraded: true, degradation_reason: reason })).toBe(message)
  })

  it("surfaces truncation without requiring a degraded flag", () => {
    expect(extractResultWarning({ truncated: true })).toBe("内容已截断，仅显示部分结果")
  })

  it("keeps clean and legacy results free of quality warnings", () => {
    expect(extractResultWarning({ summarized: true, content: "summary" })).toBe("")
    expect(extractResultWarning({ content: "legacy result" })).toBe("")
  })
})
