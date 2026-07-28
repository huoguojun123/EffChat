import { renderToStaticMarkup } from "react-dom/server"
import { describe, expect, it } from "vitest"
import { GraphvizPreview } from "@/components/workspace/GraphvizPreview"
import { buildGraphvizSandboxSrcDoc } from "@/components/workspace/graphvizSandbox"
import { parseGraphvizSvgMetrics } from "@/components/workspace/graphvizMetrics"

describe("GraphvizPreview sandbox", () => {
  it("does not mount an empty sandbox before Graphviz output is ready", () => {
    const html = renderToStaticMarkup(<GraphvizPreview code="digraph { a -> b }" />)

    expect(html).not.toContain("<iframe")
    expect(html).not.toContain("dangerouslySetInnerHTML")
    expect(html).toContain("digraph { a -&gt; b }")
    expect(html).not.toContain('aria-label="Graphviz rendering"')
  })

  it("keeps SVG links inert through iframe sandbox and pointer blocking", () => {
    const srcDoc = buildGraphvizSandboxSrcDoc(
      '<svg><a xlink:href="javascript:alert(1)"><text>bad</text></a></svg>',
      false,
      { background: "#fff", foreground: "#111", fontFamily: '"FChat Body", serif' },
    )

    expect(srcDoc).toContain("pointer-events: none")
    expect(srcDoc).toContain("javascript:alert(1)")
    expect(srcDoc).toContain('font-family: "FChat Body", serif !important')
  })

  it("prefers Graphviz physical SVG dimensions without inserting SVG into the DOM", () => {
    expect(parseGraphvizSvgMetrics('<svg width="62pt" height="116pt" viewBox="0.00 0.00 62.00 116.00"></svg>')).toEqual({
      width: 62 * (96 / 72),
      height: 116 * (96 / 72),
    })
    expect(parseGraphvizSvgMetrics('<svg width="72pt" height="36pt"></svg>')).toEqual({
      width: 96,
      height: 48,
    })
  })
})
