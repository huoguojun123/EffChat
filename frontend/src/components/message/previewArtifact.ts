export type PreviewArtifactType = "html" | "svg" | "mermaid" | "mindmap" | "graphviz" | "markdown" | "code"

export interface PreviewArtifact {
  id: string
  type: PreviewArtifactType
  title: string
  language: string
  content: string
}

export function previewArtifactFromCode(code: string, language?: string): PreviewArtifact {
  const normalized = (language || "text").toLowerCase()
  const type = previewArtifactType(normalized, code)
  return {
    id: `${type}-${hashCode(`${normalized}:${code}`)}`,
    type,
    title: previewTitle(type, normalized),
    language: normalized,
    content: code,
  }
}

export function isPreviewableArtifact(artifact: PreviewArtifact) {
  return artifact.type !== "code" && artifact.type !== "markdown"
}

export function isDefaultInlinePreviewLanguage(language: string) {
  return isDefaultInlinePreviewType(previewArtifactType(language.toLowerCase(), ""))
}

export function isDefaultInlinePreviewType(type: PreviewArtifactType) {
  return type === "mermaid" || type === "mindmap" || type === "graphviz"
}

export function markdownHasDefaultInlinePreview(markdown: string) {
  const fencePattern = /(?:^|\n)[ \t]{0,3}(?:`{3,}|~{3,})[ \t]*([^\s`~]+)/g
  for (const match of markdown.matchAll(fencePattern)) {
    if (isDefaultInlinePreviewLanguage(match[1])) return true
  }
  return false
}

export function preloadPreviewArtifact(artifact: PreviewArtifact) {
  if (artifact.type === "mermaid") return import("mermaid").then(() => undefined)
  if (artifact.type === "graphviz") return import("@hpcc-js/wasm/graphviz").then(() => undefined)
  return Promise.resolve()
}

function previewArtifactType(language: string, code: string): PreviewArtifactType {
  if (language === "html") return "html"
  if (language === "svg" || (language === "xml" && /<svg[\s>]/i.test(code))) return "svg"
  if (language === "mindmap") return "mindmap"
  if (language === "mermaid" || language === "mmd") return "mermaid"
  if (language.startsWith("graphviz") || ["dot", "gv", "neato", "fdp", "sfdp", "circo", "twopi", "osage", "patchwork"].includes(language)) return "graphviz"
  if (language === "markdown" || language === "md") return "markdown"
  return "code"
}

function previewTitle(type: PreviewArtifactType, language: string) {
  if (type === "html") return "网页预览"
  if (type === "svg") return "SVG 预览"
  if (type === "mermaid") return "Mermaid 图表"
  if (type === "mindmap") return "思维导图"
  if (type === "graphviz") return "Graphviz 图表"
  if (type === "markdown") return "Markdown 文档"
  return language ? `${language} 代码` : "代码片段"
}

function hashCode(value: string) {
  let hash = 0
  for (let i = 0; i < value.length; i++) {
    hash = ((hash << 5) - hash + value.charCodeAt(i)) | 0
  }
  return Math.abs(hash).toString(36)
}
