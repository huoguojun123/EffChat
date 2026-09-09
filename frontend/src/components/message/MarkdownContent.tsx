import { memo, useCallback, useLayoutEffect, useMemo, useRef, useState, type AnchorHTMLAttributes, type ComponentPropsWithoutRef, type CSSProperties, type ImgHTMLAttributes, type InputHTMLAttributes } from "react"
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
  const revealActive = reveal && variant !== "document"
  const revealOptions = prepareReveal(revealRuntime, normalizedContent, revealActive)
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
    span({ node, ...props }: ComponentPropsWithoutRef<"span"> & { node?: unknown; "data-reveal-sequence"?: string }) {
      void node
      const sequence = props["data-reveal-sequence"]
      // hast-util-to-jsx-runtime keys same-tag siblings by their ordinal, so a
      // later tail otherwise reuses an already-finished DOM animation. The
      // inner key is the owner timeline identity and only remounts reveal spans.
      return <span key={typeof sequence === "string" ? sequence : undefined} {...props} />
    },
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
        rehypePlugins={revealOptions ? [rehypeKatex, [rehypeReveal, revealOptions]] : [rehypeKatex]}
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
  ranges: RevealRange[]
  nextSequence: number
  pendingSource: string | null
  pendingStart: number
}

interface RevealRange {
  start: number
  end: number
  bornAt: number
  sequence: number
}

const revealRuntimeRegistry = new Map<string, RevealRuntime>()

function getRevealRuntime(ownerKey: string): RevealRuntime {
  const existing = revealRuntimeRegistry.get(ownerKey)
  if (existing) return existing
  const runtime = {
    source: "",
    visibleLength: 0,
    initialized: false,
    ranges: [],
    nextSequence: 0,
    pendingSource: null,
    pendingStart: 0,
  }
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
  append: boolean
  now: number
  runtime: RevealRuntime
}

const STREAM_REVEAL_DURATION_MS = 1500

function prepareReveal(runtime: RevealRuntime, source: string, active: boolean): RevealOptions | null {
  if (!active) return null

  const previousSource = runtime.source
  const continuing = runtime.initialized && source.startsWith(previousSource)
  if (!continuing) {
    runtime.visibleLength = 0
    runtime.ranges = []
    runtime.nextSequence = 0
    runtime.pendingSource = null
    runtime.pendingStart = 0
  }

  const sourceChanged = !runtime.initialized || !continuing || source.length > previousSource.length
  if (sourceChanged && source.length > 0) {
    runtime.pendingSource = source
    runtime.pendingStart = continuing ? runtime.visibleLength : 0
  }
  runtime.source = source
  runtime.initialized = true
  const append = runtime.pendingSource === source
  return {
    start: append ? runtime.pendingStart : runtime.visibleLength,
    append,
    now: revealNow(),
    runtime,
  }
}

function revealNow() {
  return typeof performance === "undefined" ? Date.now() : performance.now()
}

// The presentation queue decides when a safe prose unit enters layout. This
// pass records that unit once, then rehydrates every still-fading range with
// its elapsed time on later Markdown renders. A new AST must not cut short a
// previously born opacity animation.
function rehypeReveal(options: RevealOptions) {
  return (tree: RevealNode) => {
    const visibleLength = countRevealVisibleText(tree)
    const ranges = reconcileRevealRanges(options, visibleLength)
    let visibleIndex = 0
    const walk = (node: RevealNode, excluded = false) => {
      const nextExcluded = excluded || isRevealExcluded(node)
      if (node.type === "text" && !excluded) {
        const chars = Array.from(node.value || "")
        const start = visibleIndex
        visibleIndex += chars.length
        const children = renderRevealText(chars, start, ranges, options.now)
        if (children) Object.assign(node, { type: "root", children })
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

function countRevealVisibleText(tree: RevealNode) {
  let length = 0
  const walk = (node: RevealNode, excluded = false) => {
    const nextExcluded = excluded || isRevealExcluded(node)
    if (node.type === "text" && !excluded) {
      length += Array.from(node.value || "").length
      return
    }
    if (node.children) node.children.forEach((child) => walk(child, nextExcluded))
  }
  walk(tree)
  return length
}

function reconcileRevealRanges(options: RevealOptions, visibleLength: number) {
  const cutoff = options.now - STREAM_REVEAL_DURATION_MS
  const ranges = options.runtime.ranges.filter((range) => range.end <= visibleLength && range.bornAt > cutoff)
  if (options.append && visibleLength > options.start && !ranges.some((range) => (
    range.start === options.start && range.end === visibleLength
  ))) {
    ranges.push({
      start: options.start,
      end: visibleLength,
      bornAt: options.now,
      sequence: ++options.runtime.nextSequence,
    })
  }
  if (options.append && options.runtime.pendingSource === options.runtime.source) {
    options.runtime.pendingSource = null
    options.runtime.pendingStart = visibleLength
  }
  options.runtime.ranges = ranges
  return ranges
}

function renderRevealText(chars: string[], start: number, ranges: RevealRange[], now: number) {
  const end = start + chars.length
  const boundaries = new Set([0, chars.length])
  let intersects = false
  for (const range of ranges) {
    if (range.end <= start || range.start >= end) continue
    intersects = true
    boundaries.add(Math.max(0, range.start - start))
    boundaries.add(Math.min(chars.length, range.end - start))
  }
  if (!intersects) return null

  const offsets = [...boundaries].sort((left, right) => left - right)
  const children: RevealNode[] = []
  for (let index = 1; index < offsets.length; index++) {
    const from = offsets[index - 1]
    const to = offsets[index]
    const value = chars.slice(from, to).join("")
    const range = ranges.find((candidate) => start + from >= candidate.start && start + to <= candidate.end)
    if (!range || !value.trim()) {
      children.push({ type: "text", value })
      continue
    }
    children.push({
      type: "element",
      tagName: "span",
      properties: {
        className: ["stream-reveal-text"],
        "data-reveal-unit": "prose",
        "data-reveal-sequence": String(range.sequence),
        style: `--stream-reveal-elapsed:${Math.max(0, Math.floor(now - range.bornAt))}ms`,
      },
      children: [{ type: "text", value }],
    })
  }
  return children
}

function isRevealExcluded(node: RevealNode) {
  const classes = node.properties?.className
  const isKatex = Array.isArray(classes) && classes.includes("katex")
  return node.tagName === "pre" || node.tagName === "code" || node.tagName === "svg" || node.tagName === "math" || isKatex
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
