import { describe, expect, it } from "vitest"
import { isPreviewableArtifact, previewArtifactFromCode } from "@/components/message/previewArtifact"

describe("preview artifacts", () => {
  it("maps supported fenced blocks to dialog-ready artifacts", () => {
    expect(previewArtifactFromCode("graph TD; A-->B", "mermaid")).toMatchObject({ type: "mermaid", title: "Mermaid 图表" })
    expect(previewArtifactFromCode("digraph { a -> b }", "dot")).toMatchObject({ type: "graphviz", language: "dot" })
    expect(previewArtifactFromCode("- 中心\n  - 分支", "mindmap")).toMatchObject({ type: "mindmap", title: "思维导图" })
  })

  it("keeps plain source blocks outside the dialog preview path", () => {
    expect(isPreviewableArtifact(previewArtifactFromCode("const answer = 42", "typescript"))).toBe(false)
    expect(isPreviewableArtifact(previewArtifactFromCode("# 标题", "markdown"))).toBe(false)
  })
})
