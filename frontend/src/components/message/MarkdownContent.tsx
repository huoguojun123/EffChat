import { memo, useCallback, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type InputHTMLAttributes } from "react"
import ReactMarkdown from "react-markdown"
import remarkGfm from "remark-gfm"
import remarkBreaks from "remark-breaks"
import remarkMath from "remark-math"
import rehypeKatex from "rehype-katex"
import { CodeBlock } from "./CodeBlock"
import { InlineCode } from "./InlineCode"
import { markdownHasDefaultInlinePreview } from "./previewArtifact"
import { LoadingIndicator } from "@/components/ui/loading-indicator"

interface Props {
  content: string
  streaming?: boolean
  ownerKey?: string
  allowArtifactPreviews?: boolean
  variant?: "chat" | "document" | "reasoning"
}

export const MarkdownContent = memo(function MarkdownContent({
  content,
  streaming = false,
  ownerKey = "markdown",
  allowArtifactPreviews = true,
  variant = "chat",
}: Props) {
  const blockIndexRef = useRef(0)
  blockIndexRef.current = 0
  const normalizedContent = useMemo(() => normalizeTexMathDelimiters(content), [content])
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
    <div className={variant === "document" ? "document-markdown relative" : "relative"} data-markdown-preparing={preparing || undefined}>
      {preparing ? (
        <LoadingIndicator label="正在准备图表" className="pointer-events-none absolute inset-x-0 top-0 z-10 h-24" />
      ) : null}
      <div
        className={`markdown-body transition-opacity duration-300 ease-[cubic-bezier(0.16,1,0.3,1)] motion-reduce:transition-none ${variant === "reasoning" ? "text-muted-foreground" : ""} ${preparing ? "opacity-0" : "opacity-100"}`}
        style={markdownStyle}
        aria-busy={preparing}
        aria-hidden={preparing || undefined}
        inert={preparing || undefined}
      >
        <ReactMarkdown remarkPlugins={[remarkGfm, remarkBreaks, remarkMath]} rehypePlugins={[rehypeKatex]} components={components}>
          {normalizedContent}
        </ReactMarkdown>
      </div>
    </div>
  )
})

function normalizeTexMathDelimiters(markdown: string) {
  return markdown
    .split(/(```[\s\S]*?```|~~~[\s\S]*?~~~)/g)
    .map((part) => {
      if (part.startsWith("```") || part.startsWith("~~~")) return part
      return normalizeInlineTexMath(part)
    })
    .join("")
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
