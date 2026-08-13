import { describe, expect, it } from "vitest"
import { renderToStaticMarkup } from "react-dom/server"
import { mermaidRenderConfig } from "@/components/workspace/mermaidConfig"
import { MermaidPreview } from "@/components/workspace/MermaidPreview"

describe("MermaidPreview security config", () => {
  it("keeps Mermaid in strict security mode", () => {
    expect(mermaidRenderConfig(false)).toMatchObject({
      startOnLoad: false,
      securityLevel: "strict",
      theme: "default",
    })
    expect(mermaidRenderConfig(true).theme).toBe("dark")
  })

  it("passes active UI colors into Mermaid without weakening security", () => {
    const config = mermaidRenderConfig(false, { background: "#fff", foreground: "#111", accent: "#06c", fontFamily: '"EffChat Body", serif' })

    expect(config.themeVariables).toMatchObject({
      background: "#fff",
      primaryTextColor: "#111",
      primaryBorderColor: "#06c",
      fontFamily: '"EffChat Body", serif',
    })
    expect(config.themeCSS).toContain('font-family: "EffChat Body", serif !important')
    expect(config.securityLevel).toBe("strict")
  })

  it("keeps wide diagrams at their natural width", () => {
    expect(mermaidRenderConfig(false).flowchart).toEqual({ useMaxWidth: false })
    expect(mermaidRenderConfig(false).sequence).toEqual({ useMaxWidth: false })
    expect(mermaidRenderConfig(false).gantt).toMatchObject({
      useMaxWidth: false,
      fontSize: 14,
      sectionFontSize: 14,
      barHeight: 24,
    })
    expect(mermaidRenderConfig(false).journey).toMatchObject({
      useMaxWidth: false,
      taskFontSize: 14,
      titleFontSize: "14px",
    })
    expect(mermaidRenderConfig(false).timeline).toMatchObject({
      useMaxWidth: false,
      taskFontSize: 14,
    })
    expect(mermaidRenderConfig(false).themeVariables).toMatchObject({
      pieTitleTextSize: "14px",
      pieSectionTextSize: "14px",
      pieLegendTextSize: "14px",
    })
    expect(mermaidRenderConfig(false).themeCSS).toContain(".titleText")
  })

  it("shows source instead of an empty inline placeholder before the renderer is ready", () => {
    const html = renderToStaticMarkup(
      <MermaidPreview code="graph TD; A-->B" className="diagram-inline-preview" />
    )

    expect(html).toContain("graph TD; A--&gt;B")
    expect(html).not.toContain('aria-label="Mermaid rendering"')
  })
})
