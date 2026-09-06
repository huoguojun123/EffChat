import { memo, useCallback, useLayoutEffect, useMemo, useRef, useState, type AnchorHTMLAttributes, type CSSProperties, type ImgHTMLAttributes, type InputHTMLAttributes } from "react"
import ReactMarkdown from "react-markdown"
import remarkGfm from "remark-gfm"
import remarkBreaks from "remark-breaks"
import remarkMath from "remark-math"
import rehypeKatex from "rehype-katex"
import { CodeBlock } from "./CodeBlock"
import { InlineCode } from "./InlineCode"
import { ImageLightbox } from "./ImageLightbox"
import { markdownHasDefaultInlinePreview } from "./previewArtifact"
import { LoadingIndicator } from "@/components/ui/loading-indicator"

interface Props {
  content: string
  streaming?: boolean
  reveal?: boolean
  ownerKey?: string
  allowArtifactPreviews?: boolean
  variant?: "chat" | "document" | "reasoning"
}

export const MarkdownContent = memo(function MarkdownContent({
  content,
  streaming = false,
  reveal = false,
  ownerKey = "markdown",
  allowArtifactPreviews = true,
  variant = "chat",
}: Props) {
  const blockIndexRef = useRef(0)
  blockIndexRef.current = 0
  const normalizedContent = useMemo(() => normalizeMarkdownContent(content), [content])
  const revealRuntime = useMemo(() => getRevealRuntime(ownerKey), [ownerKey])
  const previousRevealSource = revealRuntime.source
  const revealActive = reveal && variant !== "document"
  const canContinueReveal = revealActive && revealRuntime.initialized
    && normalizedContent.startsWith(previousRevealSource)
  // The first snapshot for the current owner gets one bounded reveal. Once a
  // live owner has measured visible text, the durable handoff starts at that
  // length and only a genuinely new suffix can animate.
  const revealStart = revealActive
    ? canContinueReveal ? revealRuntime.visibleLength : 0
    : -1
  if (revealActive) {
    revealRuntime.source = normalizedContent
    revealRuntime.initialized = true
  }
  const preparationIdentity = `${ownerKey}:${normalizedContent}`
  const [pendingState, setPendingState] = useState<{ identity: string; keys: Set<string> }>(() => ({
    identity: preparationIdentity,
    keys: new Set(),
  }))
  const [collectedIdentity, setCollectedIdentity] = useState(() => (
    typeof window === "undefined" ? preparationIdentity : ""
  ))
  const pendingKeys = pendingState.identity === preparationIdentity ? pendingState.keys : new Set<string>()
  const coordinatesPreviews = allowArtifactPreviews && !streaming && markdownHasDefaultInlinePreview(normalizedContent)
  const collecting = coordinatesPreviews && collectedIdentity !== preparationIdentity
  const preparing = collecting || pendingKeys.size > 0
  // A slow inline preview must not hide the already-readable chat prose. The
  // document reader keeps its existing coordinated concealment, while chat
  // leaves the Markdown body interactive and only shows the local indicator.
  const concealWhilePreparing = preparing && variant === "document"
  const markdownStyle = variant === "reasoning" ? {
    "--md-font-size": "12px",
    "--md-line-height": "1.45",
  } as CSSProperties : undefined

  useLayoutEffect(() => {
    if (!coordinatesPreviews || collectedIdentity === preparationIdentity) return
    const frame = window.requestAnimationFrame(() => setCollectedIdentity(preparationIdentity))
    return () => window.cancelAnimationFrame(frame)
  }, [collectedIdentity, coordinatesPreviews, preparationIdentity])

  const handlePreviewPendingChange = useCallback((blockKey: string, pending: boolean) => {
    setPendingState((previous) => {
      const keys = previous.identity === preparationIdentity ? new Set(previous.keys) : new Set<string>()
      if (pending) keys.add(blockKey)
      else keys.delete(blockKey)
      if (previous.identity === preparationIdentity && keys.size === previous.keys.size && keys.has(blockKey) === previous.keys.has(blockKey)) return previous
      return { identity: preparationIdentity, keys }
    })
  }, [preparationIdentity])

  const components = useMemo(() => ({
    pre({ children }: { children?: React.ReactNode }) {
      return <>{children}</>
    },
    input({ checked, ...props }: InputHTMLAttributes<HTMLInputElement>) {
      return <input {...props} checked={checked} aria-label={checked ? "已完成" : "未完成"} />
    },
    a({ href, children, ...props }: AnchorHTMLAttributes<HTMLAnchorElement>) {
      const external = isExternalMarkdownUrl(href)
      return (
        <a
          {...props}
          href={href}
          target={external ? "_blank" : props.target}
          rel={external ? "noreferrer" : props.rel}
        >
          {children}
        </a>
      )
    },
    img({ src, alt, title, ...props }: ImgHTMLAttributes<HTMLImageElement>) {
      return <MarkdownImage src={src} alt={alt} title={title} {...props} />
    },
    code({ className, children }: { className?: string; children?: React.ReactNode }) {
      const match = /language-([^\s]+)/.exec(className || "")
      const code = String(children ?? "").replace(/\n$/, "")
      if (match || code.includes("\n")) {
        const blockIndex = blockIndexRef.current++
        return (
          <CodeBlock
            code={code}
            language={match?.[1]}
            streaming={streaming}
            blockKey={`${ownerKey}:${match?.[1] || "text"}:${blockIndex}`}
            onPreviewPendingChange={handlePreviewPendingChange}
            allowPreview={allowArtifactPreviews}
          />
        )
      }
      return <InlineCode code={code} />
    },
    table({ children }: { children?: React.ReactNode }) {
      if (variant !== "document") return <table>{children}</table>
      return <div className="markdown-table-scroll"><table>{children}</table></div>
    },
  }), [allowArtifactPreviews, handlePreviewPendingChange, ownerKey, streaming, variant])

  return (
    <div className={`${variant === "document" ? "document-markdown" : ""} ${streaming ? "streaming-markdown" : ""} relative min-w-0 max-w-full`} data-markdown-preparing={preparing || undefined}>
      {preparing ? (
        <LoadingIndicator label="正在准备图表" className="pointer-events-none absolute inset-x-0 top-0 z-10 h-24" />
      ) : null}
      <div
        className={`markdown-body transition-opacity duration-300 ease-[cubic-bezier(0.16,1,0.3,1)] motion-reduce:transition-none ${variant === "reasoning" ? "text-muted-foreground" : ""} ${concealWhilePreparing ? "opacity-0" : "opacity-100"}`}
        style={markdownStyle}
        aria-busy={preparing}
        aria-hidden={concealWhilePreparing || undefined}
        inert={concealWhilePreparing || undefined}
      >
      <ReactMarkdown
        remarkPlugins={[remarkGfm, remarkBreaks, remarkMath]}
        rehypePlugins={revealStart >= 0 ? [rehypeKatex, [rehypeReveal, { start: revealStart, runtime: revealRuntime }]] : [rehypeKatex]}
        components={components}
      >
          {normalizedContent}
        </ReactMarkdown>
      </div>
    </div>
  )
})

interface RevealRuntime {
  source: string
  visibleLength: number
  initialized: boolean
}

const revealRuntimeRegistry = new Map<string, RevealRuntime>()

function getRevealRuntime(ownerKey: string): RevealRuntime {
  const existing = revealRuntimeRegistry.get(ownerKey)
  if (existing) return existing
  const runtime = { source: "", visibleLength: 0, initialized: false }
  revealRuntimeRegistry.set(ownerKey, runtime)
  if (revealRuntimeRegistry.size > 64) {
    const oldest = revealRuntimeRegistry.keys().next().value
    if (oldest) revealRuntimeRegistry.delete(oldest)
  }
  return runtime
}

interface RevealNode {
  type: string
  value?: string
  tagName?: string
  properties?: Record<string, unknown>
  children?: RevealNode[]
}

interface RevealOptions {
  start: number
  runtime: RevealRuntime
}

const STREAM_SENTENCE_STAGGER_MS = 72
const STREAM_SENTENCE_STAGGER_CAP_MS = 216

const sentenceSegmenter = typeof Intl !== "undefined" && "Segmenter" in Intl
  ? new Intl.Segmenter("zh", { granularity: "sentence" })
  : null

// Annotate only the newly visible suffix after Markdown has parsed the source.
// This keeps Markdown punctuation, entities and generated preview nodes out of
// the animation boundary calculation. New prose is grouped by sentence rather
// than by token/character so bursty SSE chunks still arrive as a calm sequence.
function rehypeReveal(options: RevealOptions) {
  return (tree: RevealNode) => {
    let visibleIndex = 0
    const walk = (node: RevealNode, excluded = false) => {
      const classes = node.properties?.className
      const isKatex = Array.isArray(classes) && classes.includes("katex")
      const nextExcluded = excluded || node.tagName === "pre" || node.tagName === "code" || node.tagName === "svg" || node.tagName === "math" || isKatex
      if (node.type === "text" && !excluded) {
        const chars = Array.from(node.value || "")
        const start = visibleIndex
        visibleIndex += chars.length
        if (options.start < visibleIndex && chars.length > 0) {
          const split = Math.max(0, options.start - start)
          const suffix = chars.slice(split).join("")
          // Markdown can expose paragraph separators as standalone text
          // nodes. Keep those nodes intact instead of animating invisible
          // whitespace spans.
          if (!suffix.trim()) return
          const children: RevealNode[] = []
          if (split > 0) children.push({ type: "text", value: chars.slice(0, split).join("") })
          let sentenceIndex = 0
          for (const sentence of splitRevealSentences(suffix)) {
            if (!sentence.trim()) {
              children.push({ type: "text", value: sentence })
              continue
            }
            const delay = Math.min(sentenceIndex * STREAM_SENTENCE_STAGGER_MS, STREAM_SENTENCE_STAGGER_CAP_MS)
            children.push({
              type: "element",
              tagName: "span",
              properties: {
                className: ["stream-reveal-text"],
                "data-reveal-unit": "sentence",
                style: `--stream-reveal-delay:${delay}ms`,
              },
              children: [{ type: "text", value: sentence }],
            })
            sentenceIndex++
          }
          Object.assign(node, { type: "root", children })
        }
        return
      }
      if (node.children && !nextExcluded) {
        const children: RevealNode[] = []
        for (const child of node.children) {
          walk(child, nextExcluded)
          if (child.type === "root" && child.children) children.push(...child.children)
          else children.push(child)
        }
        node.children = children
      }
    }
    walk(tree)
    options.runtime.visibleLength = visibleIndex
  }
}

function splitRevealSentences(value: string) {
  if (!value.trim()) return [value]
  if (sentenceSegmenter) {
    return Array.from(sentenceSegmenter.segment(value), ({ segment }) => segment)
  }

  // Older engines still get safe punctuation boundaries. A period only ends
  // a sentence before whitespace/end, so decimals and dotted identifiers stay
  // together instead of producing noisy micro-fades.
  const chars = Array.from(value)
  const segments: string[] = []
  let start = 0
  for (let index = 0; index < chars.length; index++) {
    const char = chars[index]
    const next = chars[index + 1] || ""
    const terminal = "。！？!?".includes(char)
      || char === "\n"
      || (char === "." && (!next || /\s/u.test(next)))
    if (!terminal) continue
    let end = index + 1
    while (end < chars.length && "”’》）)]}".includes(chars[end])) end++
    segments.push(chars.slice(start, end).join(""))
    start = end
    index = end - 1
  }
  if (start < chars.length) segments.push(chars.slice(start).join(""))
  return segments.length > 0 ? segments : [value]
}

function normalizeTexMathDelimiters(markdown: string) {
  return markdown
    .split(/(```[\s\S]*?```|~~~[\s\S]*?~~~)/g)
    .map((part) => {
      if (part.startsWith("```") || part.startsWith("~~~")) return part
      return normalizeInlineTexMath(part)
    })
    .join("")
}

function normalizeMarkdownContent(markdown: string) {
  return normalizeLegacyBreakTags(normalizeTexMathDelimiters(markdown))
}

// Older model replies sometimes use HTML line-break tags inside Markdown tables.
// Convert only the inert, attribute-free variants; raw HTML remains disabled and
// code spans/fences must retain their exact source text.
function normalizeLegacyBreakTags(markdown: string) {
  return markdown
    .split(/(```[\s\S]*?```|~~~[\s\S]*?~~~)/g)
    .map((part) => {
      if (part.startsWith("```") || part.startsWith("~~~")) return part
      const chunks = part.split(/(`+[^`]*`+)/g)
      return chunks.map((chunk) => {
        if (chunk.startsWith("`") && chunk.endsWith("`")) return chunk
        return chunk.replace(/<br\s*\/?>/gi, "&#10;")
      }).join("")
    })
    .join("")
}

const MarkdownImage = memo(function MarkdownImage({
  src,
  alt,
  title,
  ...props
}: ImgHTMLAttributes<HTMLImageElement>) {
  const [open, setOpen] = useState(false)
  const [failedSrc, setFailedSrc] = useState<string>()
  const label = alt?.trim() || "Markdown 图片"
  const filename = title?.trim() || label

  if (!src || failedSrc === src) {
    return (
      <span className="markdown-image-fallback" role="img" aria-label={`${label}加载失败`}>
        图片无法加载：{label}
      </span>
    )
  }

  return (
    <span className="markdown-image">
      <button
        type="button"
        className="markdown-image-trigger"
        aria-label={`放大图片：${label}`}
        title={`放大图片：${label}`}
        onClick={() => setOpen(true)}
      >
        <img
          {...props}
          src={src}
          alt={label}
          loading="lazy"
          onError={() => setFailedSrc(src)}
        />
      </button>
      {title ? <span className="markdown-image-caption">{title}</span> : null}
      <ImageLightbox open={open} url={src} filename={filename} onOpenChange={setOpen} />
    </span>
  )
})

function isExternalMarkdownUrl(href: string | undefined) {
  if (!href) return false
  try {
    const url = new URL(href, "https://effchat.invalid")
    return (url.protocol === "http:" || url.protocol === "https:") && url.host !== "effchat.invalid"
  } catch {
    return false
  }
}

function normalizeInlineTexMath(text: string) {
  let output = ""
  for (let i = 0; i < text.length; i++) {
    if (text[i] === "`") {
      const tickStart = i
      while (text[i + 1] === "`") i++
      const ticks = text.slice(tickStart, i + 1)
      const end = text.indexOf(ticks, i + 1)
      if (end === -1) {
        output += text.slice(tickStart)
        break
      }
      output += text.slice(tickStart, end + ticks.length)
      i = end + ticks.length - 1
      continue
    }

    if (text.startsWith("\\(", i)) {
      const end = text.indexOf("\\)", i + 2)
      if (end !== -1) {
        output += `$${text.slice(i + 2, end)}$`
        i = end + 1
        continue
      }
    }

    if (text.startsWith("\\[", i)) {
      const end = text.indexOf("\\]", i + 2)
      if (end !== -1) {
        output += `$$${text.slice(i + 2, end)}$$`
        i = end + 1
        continue
      }
    }

    output += text[i]
  }
  return output
}
