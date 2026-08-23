import { describe, expect, it, vi } from "vitest"
import { renderToStaticMarkup } from "react-dom/server"
import { MarkdownContent } from "@/components/message/MarkdownContent"
import { markdownHasDefaultInlinePreview } from "@/components/message/previewArtifact"

vi.mock("@/components/message/CodeBlock", () => ({
  CodeBlock: ({ code, allowPreview }: { code: string; allowPreview?: boolean }) => <pre className="mock-code-block" data-allow-preview={String(allowPreview)}>{code}</pre>,
}))

describe("MarkdownContent rendering", () => {
  it("renders inline and block math as KaTeX markup", () => {
    const html = renderToStaticMarkup(
      <MarkdownContent content={"行内公式 $E=mc^2$\n\n$$\\int_0^1 x^2 dx$$"} />
    )

    expect(html.match(/class="katex/g)?.length).toBeGreaterThanOrEqual(2)
    expect(html).toContain("katex-mathml")
  })

  it("renders common TeX delimiters in normal chat markdown", () => {
    const html = renderToStaticMarkup(
      <MarkdownContent content={"行内公式 \\(E=mc^2\\)\n\n行间公式：\n\n\\[\\boxed{\\pi(x) \\sim \\frac{x}{\\ln x}}\\]\n\n| 项 | 公式 |\n|---|---|\n| Prime | $\\pi(x) \\sim x/\\ln x$ |"} />
    )

    expect(html.match(/class="katex/g)?.length).toBeGreaterThanOrEqual(3)
    expect(html).not.toContain("\\(")
    expect(html).not.toContain("\\[")
  })

  it("does not parse dollar signs inside fenced code as math", () => {
    const html = renderToStaticMarkup(
      <MarkdownContent content={"```text\nconst price = \"$5\"\n```"} />
    )

    expect(html).toContain("mock-code-block")
    expect(html).not.toContain("katex")
  })

  it("does not normalize TeX delimiters inside code", () => {
    const html = renderToStaticMarkup(
      <MarkdownContent content={"行内代码 `\\(raw\\)`\n\n```text\n\\[raw\\]\n```"} />
    )

    expect(html).toContain("\\(raw\\)")
    expect(html).toContain("\\[raw\\]")
    expect(html).not.toContain("katex")
  })

  it("keeps emoji as native text without external image requests", () => {
    const html = renderToStaticMarkup(<MarkdownContent content="状态正常 ✅" />)

    expect(html).toContain("✅")
    expect(html).not.toContain('class="emoji"')
    expect(html).not.toContain("cdn.jsdelivr.net")
  })

  it("renders reasoning as compact markdown without artifact previews", () => {
    const html = renderToStaticMarkup(
      <MarkdownContent
        content={"## 判断\n\n- **证据一**\n- 证据二"}
        variant="reasoning"
        allowArtifactPreviews={false}
      />
    )

    expect(html).toContain("<h2>判断</h2>")
    expect(html).toContain("<strong>证据一</strong>")
    expect(html).toContain("<ul>")
    expect(html).toContain("--md-font-size:12px")
    expect(html).toContain("text-muted-foreground")
  })

  it("disables artifact previews for parsed documents", () => {
    const html = renderToStaticMarkup(
      <MarkdownContent content={"```mermaid\ngraph TD; A-->B\n```"} allowArtifactPreviews={false} />
    )

    expect(html).toContain('data-allow-preview="false"')
  })

  it("does not stringify a missing fenced-code child", () => {
    const html = renderToStaticMarkup(<MarkdownContent content={"```mermaid\n"} allowArtifactPreviews={false} />)

    expect(html).not.toContain("undefined")
  })

  it("coordinates only code fences that open in a default diagram mode", () => {
    expect(markdownHasDefaultInlinePreview("普通正文\n\n```ts\nconst ready = true\n```")).toBe(false)
    expect(markdownHasDefaultInlinePreview("```html\n<p>source first</p>\n```")).toBe(false)
    expect(markdownHasDefaultInlinePreview("```mermaid\ngraph TD; A-->B\n```")).toBe(true)
    expect(markdownHasDefaultInlinePreview("~~~graphviz-neato\ngraph { A -- B }\n~~~")).toBe(true)
  })

  it("keeps extracted GFM table cells structurally intact", () => {
    const content = [
      String.raw`| head\|er | literal\\\|pipe | multi&#10;line | &lt;tag&gt; | 中文 |`,
      "| --- | --- | --- | --- | --- |",
      "| A | B&#10;C | D&#10;E |  |  |",
      "|  |  | tail | &amp;entity; | 🙂 |",
    ].join("\n")
    const html = renderToStaticMarkup(<MarkdownContent content={content} variant="document" />)

    expect(html).toContain("<table>")
    expect(html.match(/<th>/g)).toHaveLength(5)
    expect(html.match(/<td>/g)).toHaveLength(10)
    expect(html).toContain("<th>head|er</th>")
    expect(html).toContain(String.raw`<th>literal\|pipe</th>`)
    expect(html).toContain("<th>multi<br/>\nline</th>")
    expect(html).toContain("<th>&lt;tag&gt;</th>")
    expect(html).not.toContain("<tag>")
  })

  it("renders read-only task lists with accessible state labels", () => {
    const html = renderToStaticMarkup(
      <MarkdownContent content={"- [x] 已完成\n- [ ] 待处理"} />
    )

    expect(html).toContain('class="contains-task-list"')
    expect(html).toContain('class="task-list-item"')
    expect(html).toContain('type="checkbox"')
    expect(html).toContain('aria-label="已完成"')
    expect(html).toContain('aria-label="未完成"')
    expect(html).toContain('disabled=""')
  })

  it("renders GFM footnotes with a return link", () => {
    const html = renderToStaticMarkup(
      <MarkdownContent content={"说明[^1]\n\n[^1]: 补充说明"} />
    )

    expect(html).toContain('data-footnote-ref="true"')
    expect(html).toContain('data-footnote-backref=""')
    expect(html).toContain('class="footnotes"')
  })
})
