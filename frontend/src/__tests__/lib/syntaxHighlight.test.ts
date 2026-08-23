import { describe, expect, it } from "vitest"
import { escapeHtml } from "@/lib/htmlText"
import { highlightCodeToHtml, highlightInlineCodeToHtml } from "@/lib/syntaxHighlight"

describe("syntax highlight HTML safety", () => {
  it("escapes unsupported code blocks before using innerHTML", async () => {
    const html = await highlightCodeToHtml('<img src=x onerror="alert(1)">', "unknown-language")

    expect(html).toContain("&lt;img")
    expect(html).not.toContain("<img")
  })

  it("keeps supported HTML code as escaped text in Shiki output", async () => {
    const html = await highlightCodeToHtml("<script>alert(1)</script>", "html")

    expect(html).toMatch(/(&lt;|&#x3C;)/)
    expect(html).not.toContain("<script>")
  })

  it("loads the selected light and dark syntax themes", async () => {
    const html = await highlightCodeToHtml("const answer = 42", "typescript", "catppuccin-latte", "catppuccin-mocha")

    expect(html).toContain("--shiki-light")
    expect(html).toContain("--shiki-dark")
  })

  it.each([
    ["rb", "ruby"],
    ["cmd", "bat"],
    ["c++", "cpp"],
    ["cs", "csharp"],
    ["ps1", "powershell"],
    ["kt", "kotlin"],
    ["swift", "swift"],
    ["vue", "vue"],
  ])("highlights the supported %s alias as %s", async (alias) => {
    const html = await highlightCodeToHtml("const answer = 42", alias)

    expect(html).toContain("--shiki-light")
    expect(html).toContain("--shiki-dark")
  })

  it("escapes inline HTML snippets before rendering inline code", async () => {
    const html = await highlightInlineCodeToHtml("<b onclick='alert(1)'>x</b>")

    expect(html).toMatch(/(&lt;|&#x3C;)/)
    expect(html).not.toContain("<b")
  })

  it("escapes quotes and apostrophes in fallback text", () => {
    expect(escapeHtml(`'"<>`)).toBe("&#39;&quot;&lt;&gt;")
  })
})
