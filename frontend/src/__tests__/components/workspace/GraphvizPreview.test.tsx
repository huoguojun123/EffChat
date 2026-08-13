import { renderToStaticMarkup } from "react-dom/server"
import { describe, expect, it } from "vitest"
import { GraphvizPreview } from "@/components/workspace/GraphvizPreview"
import { buildGraphvizSandboxSrcDoc } from "@/components/workspace/graphvizSandbox"

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
      { background: "#fff", foreground: "#111", fontFamily: '"EffChat Body", serif' },
    )

    expect(srcDoc).toContain("pointer-events: none")
    expect(srcDoc).toContain("javascript:alert(1)")
    expect(srcDoc).toContain('font-family: "EffChat Body", serif !important')
  })
})
