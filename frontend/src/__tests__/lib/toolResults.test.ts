import { describe, expect, it } from "vitest"
import { cleanUrl, parseToolFailure } from "@/lib/toolResults"

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
})
