import { renderToStaticMarkup } from "react-dom/server"
import { describe, expect, it } from "vitest"
import { ToolCallTree } from "@/components/message/ToolCallTree"
import type { ToolCall } from "@/types"

function renderExtract(result: Record<string, unknown>, status: ToolCall["status"] = "done") {
  return renderToStaticMarkup(
    <ToolCallTree
      toolCalls={[{
        id: "extract-1",
        name: "web_extract",
        status,
        result: JSON.stringify({
          ok: true,
          title: "Example page",
          url: "https://example.com/source",
          content: "Fallback content remains visible.",
          source: "crawler",
          ...result,
        }),
      }]}
    />
  )
}

describe("ToolCallTree web extraction quality", () => {
  it("renders degraded output as a warning while preserving content and source", () => {
    const html = renderExtract({
      degraded: true,
      degradation_reason: "refinement_failed",
      refinement_attempted: true,
    })

    expect(html).toContain("内容受限")
    expect(html).toContain("模型提炼未完成，显示抓取原文")
    expect(html).toContain("Fallback content remains visible.")
    expect(html).toContain('href="https://example.com/source"')
    expect(html).toContain('aria-label="打开来源：Example page"')
    expect(html).toContain("status-warning-solid")
    expect(html).not.toContain("status-success-solid")
  })

  it("renders source-only truncation as a warning", () => {
    const html = renderExtract({ truncated: true })

    expect(html).toContain("内容受限")
    expect(html).toContain("内容已截断，仅显示部分结果")
    expect(html).not.toContain("text-emerald-600")
  })

  it("keeps clean and legacy output in the normal success state", () => {
    expect(renderExtract({ summarized: true })).toContain("status-success-solid")
    expect(renderExtract({})).toContain("status-success-solid")
  })

  it("keeps hard failures in the error state", () => {
    const html = renderExtract({ ok: false, error: "upstream unavailable", degraded: true })

    expect(html).toContain("status-error-solid")
    expect(html).toContain("工具调用失败")
    expect(html).not.toContain("内容受限")
  })
})
