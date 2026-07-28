import { renderToStaticMarkup } from "react-dom/server"
import { describe, expect, it } from "vitest"
import { DiagramViewport } from "@/components/workspace/DiagramViewport"

describe("DiagramViewport", () => {
  it("caps a tall inline diagram at the compact preview height", () => {
    const html = renderToStaticMarkup(
      <DiagramViewport naturalWidth={800} naturalHeight={1600}>
        <svg />
      </DiagramViewport>
    )

    expect(html).toContain("height:480px")
  })

  it("allows vertical scroll to chain to the conversation at diagram edges", () => {
    const html = renderToStaticMarkup(
      <DiagramViewport naturalWidth={800} naturalHeight={1600}>
        <svg />
      </DiagramViewport>
    )

    expect(html).toContain("overscroll-auto")
    expect(html).not.toContain("overscroll-contain")
  })
})
