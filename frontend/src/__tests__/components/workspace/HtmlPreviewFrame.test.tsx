import { renderToStaticMarkup } from "react-dom/server"
import { describe, expect, it } from "vitest"
import { HtmlPreviewFrame } from "@/components/workspace/HtmlPreviewFrame"

describe("HtmlPreviewFrame sandbox", () => {
  it("renders HTML previews without script permissions", () => {
    const html = renderToStaticMarkup(
      <HtmlPreviewFrame id="html-preview" type="html" content="<script>alert(1)</script>" />
    )

    expect(html).toContain("<iframe")
    expect(html).toContain('sandbox="allow-same-origin"')
    expect(html).not.toContain("allow-scripts")
  })

  it("adds the conversation font as an overridable HTML default", () => {
    const html = renderToStaticMarkup(
      <HtmlPreviewFrame
        id="html-font-preview"
        type="html"
        content="<html><head><style>body{font-family:monospace}</style></head><body>preview</body></html>"
      />
    )

    expect(html).toContain("data-effchat-preview-font")
    expect(html.indexOf("data-effchat-preview-font")).toBeLessThan(html.indexOf("font-family:monospace"))
  })

  it("renders SVG previews through the same sandboxed frame", () => {
    const html = renderToStaticMarkup(
      <HtmlPreviewFrame id="svg-preview" type="svg" content='<svg onload="alert(1)"></svg>' />
    )

    expect(html).toContain("<iframe")
    expect(html).toContain('sandbox="allow-same-origin"')
    expect(html).not.toContain("allow-scripts")
    expect(html).toContain("background:transparent")
    expect(html).not.toContain("bg-white")
  })

  it("reserves inline preview height before its document has loaded", () => {
    const html = renderToStaticMarkup(
      <HtmlPreviewFrame id="html-preview" type="html" content="<main>preview</main>" staticPreview />
    )

    expect(html).toContain("height:240px")
  })
})
