import { describe, expect, it, vi } from "vitest"
import { normalizeMermaidTypography, prepareDiagramSvg, readableDiagramScale } from "@/components/workspace/diagramSvg"

describe("prepareDiagramSvg", () => {
  it("removes fit-to-width attributes while preserving the SVG viewBox", () => {
    const result = prepareDiagramSvg('<svg width="100%" style="max-width: 8751.078125px;" viewBox="0 0 8751.078125 454"></svg>')

    expect(result.width).toBe(8751.078125)
    expect(result.height).toBe(454)
    expect(result.svg).toContain('width="8751.078125"')
    expect(result.svg).toContain('height="454"')
    expect(result.svg).not.toContain('width="100%"')
    expect(result.svg).not.toContain("max-width: 8751.078125px")
  })

  it("reads dimensions from the SVG root rather than nested diagram nodes", () => {
    const result = prepareDiagramSvg('<svg viewBox="0 0 540 450"><rect width="300" height="250" /></svg>')

    expect(result.width).toBe(540)
    expect(result.height).toBe(450)
  })

  it("converts physical SVG dimensions to CSS pixels when no viewBox exists", () => {
    const result = prepareDiagramSvg('<svg width="72pt" height="36pt"></svg>')

    expect(result.width).toBe(96)
    expect(result.height).toBe(48)
    expect(result.svg).toContain('width="96"')
  })

  it("finds the first Mermaid node so wide diagrams open near their root", () => {
    const result = prepareDiagramSvg('<svg viewBox="0 0 1000 500"><g class="node default" transform="translate(42, 318)"></g></svg>')

    expect(result.initialFocus).toEqual({ x: 42, y: 318 })
  })

  it("measures text in rendered coordinate space", () => {
    const result = prepareDiagramSvg('<svg width="72pt" height="36pt" viewBox="0 0 72 36"><text font-size="14">图表</text></svg>')

    expect(result.maxTextSize).toBeCloseTo(14 * (96 / 72))
  })

  it("scales the chart so its largest text matches the body", () => {
    expect(readableDiagramScale(16, 14)).toBeCloseTo(0.875)
    expect(readableDiagramScale(14, 14)).toBe(1)
  })

  it("keeps exceptional Mermaid titles aligned with the body text", () => {
    const originalDocument = globalThis.document
    const originalGetComputedStyle = globalThis.getComputedStyle
    const title = {
      textContent: "项目推进",
      getAttribute: () => "",
      setAttribute: vi.fn(),
      tagName: "text",
      parentElement: { tagName: "svg" },
      closest: vi.fn(),
    }
    const body = {
      textContent: "完成核心流程",
      getAttribute: () => "",
      setAttribute: vi.fn(),
      tagName: "text",
      parentElement: { tagName: "g" },
      closest: vi.fn(),
    }
    const svg = {
      outerHTML: "<svg />",
      querySelectorAll: vi.fn(() => [title]),
    }
    title.closest.mockReturnValue(svg)
    body.closest.mockReturnValue(svg)
    const createElement = (tagName: string) => {
      if (tagName === "div") {
        return {
          setAttribute: vi.fn(),
          style: { cssText: "" },
          innerHTML: "",
          remove: vi.fn(),
          querySelector: vi.fn(() => svg),
          querySelectorAll: vi.fn(() => [body, title]),
        }
      }
      return null
    }
    Object.defineProperty(globalThis, "document", {
      configurable: true,
      value: {
        createElement,
        body: { append: vi.fn() },
      },
    })
    Object.defineProperty(globalThis, "getComputedStyle", {
      configurable: true,
      value: (element: { textContent?: string }) => ({ fontSize: element.textContent === "项目推进" ? "33px" : "16px" }),
    })

    try {
      expect(normalizeMermaidTypography('<svg><text>完成核心流程</text><text>项目推进</text></svg>', '"EffChat Body", serif')).toBe("<svg />")
      expect(body.setAttribute).toHaveBeenCalledWith("style", "font-family:\"EffChat Body\", serif!important")
      expect(title.setAttribute).toHaveBeenCalledWith("style", "font-family:\"EffChat Body\", serif!important;font-size:16px!important")
    } finally {
      Object.defineProperty(globalThis, "document", { configurable: true, value: originalDocument })
      Object.defineProperty(globalThis, "getComputedStyle", { configurable: true, value: originalGetComputedStyle })
    }
  })

})
