import { memo, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react"
import ReactMarkdown from "react-markdown"
import remarkBreaks from "remark-breaks"
import remarkGfm from "remark-gfm"
import { Check, ChevronDown, ChevronUp, Code2, Copy, ExternalLink, Eye, LoaderCircle } from "lucide-react"
import { useUIStore } from "@/stores/ui"
import { MermaidPreview } from "@/components/workspace/MermaidPreview"
import { MindMapPreview } from "@/components/workspace/MindMapPreview"
import { GraphvizPreview } from "@/components/workspace/GraphvizPreview"
import { HtmlPreviewFrame } from "@/components/workspace/HtmlPreviewFrame"
import { compactCodeForDisplay, escapeHtml } from "@/lib/htmlText"
import { colorTheme, type ColorThemeId } from "@/lib/themes"
import { PreviewDialog, type PreviewDialogPhase } from "./PreviewDialog"
import { isDefaultInlinePreviewType, isPreviewableArtifact, preloadPreviewArtifact, previewArtifactFromCode, type PreviewArtifact, type PreviewArtifactType } from "./previewArtifact"

const COLLAPSED_HEIGHT_PX = 360
const toolbarButtonClass = "flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground transition-[background-color,color,opacity] motion-control hover:bg-background/70 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-35"
let syntaxHighlighterPromise: Promise<typeof import("@/lib/syntaxHighlight")> | null = null

function loadSyntaxHighlighter() {
  syntaxHighlighterPromise ??= import("@/lib/syntaxHighlight")
  return syntaxHighlighterPromise
}

async function highlightCode(displayCode: string, language: string | undefined, lightThemeId: ColorThemeId, darkThemeId: ColorThemeId) {
  const { highlightCodeToHtml } = await loadSyntaxHighlighter()
  return highlightCodeToHtml(
    displayCode,
    language,
    colorTheme(lightThemeId).shikiLight,
    colorTheme(darkThemeId).shikiDark
  )
}

interface Props {
  code: string
  language?: string
  streaming?: boolean
  blockKey: string
  onPreviewPendingChange?: (blockKey: string, pending: boolean) => void
  allowPreview?: boolean
}

export const CodeBlock = memo(function CodeBlock({ code, language, streaming = false, blockKey, onPreviewPendingChange, allowPreview = true }: Props) {
  const [copied, setCopied] = useState(false)
  const lightColorTheme = useUIStore((s) => s.lightColorTheme)
  const darkColorTheme = useUIStore((s) => s.darkColorTheme)
  const displayCode = useMemo(() => compactCodeForDisplay(code), [code])
  const highlightIdentity = `${lightColorTheme}:${darkColorTheme}:${language || ""}:${displayCode}`
  const [canExpand, setCanExpand] = useState(false)
  const [bodyHeight, setBodyHeight] = useState(COLLAPSED_HEIGHT_PX)
  const [highlightState, setHighlightState] = useState(() => ({
    identity: highlightIdentity,
    html: fallbackCodeHtml(displayCode),
  }))
  const [previewDialogPhase, setPreviewDialogPhase] = useState<PreviewDialogPhase>("idle")
  const [previewDialogError, setPreviewDialogError] = useState("")
  const [inlinePreviewError, setInlinePreviewError] = useState("")
  const [readyPreviewId, setReadyPreviewId] = useState("")
  const sourceRef = useRef<HTMLDivElement>(null)
  const codeBlockState = useUIStore((s) => s.codeBlockStates[blockKey])
  const setCodeBlockMode = useUIStore((s) => s.setCodeBlockMode)
  const setCodeBlockExpanded = useUIStore((s) => s.setCodeBlockExpanded)
  const previewArtifact = useMemo(() => previewArtifactFromCode(code, language), [code, language])
  const previewable = allowPreview && isPreviewableArtifact(previewArtifact)
  const mode = codeBlockState?.mode ?? defaultCodeBlockMode(previewArtifact.type, streaming)
  const expanded = streaming ? true : (codeBlockState?.expanded ?? false)
  const previewReady = readyPreviewId === previewArtifact.id
  const requestedPreview = previewable && mode === "preview" && !streaming
  const showPreview = requestedPreview && previewReady
  const highlightedHtml = streaming
    ? fallbackCodeHtml(displayCode)
    : highlightState.identity === highlightIdentity
      ? highlightState.html
      : fallbackCodeHtml(displayCode)

  function handleCopy() {
    navigator.clipboard.writeText(code)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  function handleOpenPreview() {
    setPreviewDialogError("")
    setPreviewDialogPhase("preparing")
  }

  function handleToggleMode() {
    setCodeBlockMode(blockKey, mode === "preview" ? "source" : "preview")
  }

  const handleInlinePreviewReady = useCallback(() => {
    setReadyPreviewId(previewArtifact.id)
  }, [previewArtifact.id])

  const handleInlinePreviewError = useCallback(() => {
    setReadyPreviewId("")
    setInlinePreviewError("预览渲染失败，已保留源码。")
    setCodeBlockMode(blockKey, "source")
  }, [blockKey, setCodeBlockMode])

  function updateExpandState() {
    const el = sourceRef.current
    if (!el) return
    const nextHeight = el.scrollHeight
    setBodyHeight(nextHeight)
    setCanExpand(nextHeight > COLLAPSED_HEIGHT_PX + 8)
  }

  useEffect(() => {
    updateExpandState()
  }, [code, language, mode, expanded, streaming, highlightedHtml])

  useEffect(() => {
    let canceled = false
    if (streaming) return
    const identity = highlightIdentity
    void highlightCode(displayCode, language, lightColorTheme, darkColorTheme)
      .then((html) => {
        if (!canceled) setHighlightState({ identity, html })
      })
      .catch(() => {
        if (!canceled) setHighlightState({ identity, html: fallbackCodeHtml(displayCode) })
      })
    return () => {
      canceled = true
    }
  }, [darkColorTheme, displayCode, highlightIdentity, language, lightColorTheme, streaming])

  useEffect(() => {
    setPreviewDialogPhase("idle")
    setPreviewDialogError("")
    setInlinePreviewError("")
  }, [previewArtifact.id])

  useLayoutEffect(() => {
    const pending = requestedPreview && !previewReady && !inlinePreviewError
    onPreviewPendingChange?.(blockKey, pending)
    return () => onPreviewPendingChange?.(blockKey, false)
  }, [blockKey, inlinePreviewError, onPreviewPendingChange, previewReady, requestedPreview])

  const bodyMaxHeight = !showPreview && !streaming && canExpand ? `${expanded ? bodyHeight : COLLAPSED_HEIGHT_PX}px` : undefined

  return (
    <>
      <div className="code-block-shell group relative my-3 overflow-hidden rounded-lg border border-border/70 bg-background/70">
        <span
          className="pointer-events-none absolute left-2.5 top-2 z-20 rounded px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground/75 backdrop-blur-sm"
          aria-label={`代码语言：${language || "text"}`}
        >
          {language || "text"}
        </span>
        <div className="code-block-toolbar absolute right-2 top-2 z-20 flex items-center gap-0.5 rounded-lg border border-border/45 bg-popover/55 p-0.5 shadow-[0_5px_18px_rgba(0,0,0,0.08)] backdrop-blur-xl transition-[background-color,border-color,opacity,box-shadow] motion-surface sm:opacity-75 sm:group-hover:opacity-100 sm:focus-within:opacity-100">
          {previewable ? (
            <button
              type="button"
              onClick={handleToggleMode}
              disabled={streaming || !!inlinePreviewError}
              aria-pressed={mode === "preview"}
              aria-label={streaming ? "图表生成中" : mode === "preview" ? "查看源码" : "查看预览"}
              title={streaming ? "图表生成中" : mode === "preview" ? "查看源码" : "查看预览"}
              className={toolbarButtonClass}
            >
              {mode === "preview" ? <Code2 className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
            </button>
          ) : null}
          {previewable ? (
            <button
              type="button"
              onClick={handleOpenPreview}
              onPointerEnter={() => void preloadPreviewArtifact(previewArtifact)}
              onFocus={() => void preloadPreviewArtifact(previewArtifact)}
              disabled={streaming || previewDialogPhase === "preparing"}
              aria-busy={previewDialogPhase === "preparing"}
              aria-label={previewDialogPhase === "preparing" ? "正在准备弹窗预览" : "在弹窗中查看预览"}
              className={toolbarButtonClass}
              title="在弹窗中查看预览"
            >
              {previewDialogPhase === "preparing" ? <LoaderCircle className="h-3.5 w-3.5 animate-spin motion-reduce:animate-none" /> : <ExternalLink className="h-3.5 w-3.5" />}
            </button>
          ) : null}
          <button
            type="button"
            onClick={handleCopy}
            aria-label={copied ? "已复制代码" : "复制代码"}
            title={copied ? "已复制" : "复制代码"}
            className={toolbarButtonClass}
          >
            {copied ? <Check className="h-3.5 w-3.5 text-primary" /> : <Copy className="h-3.5 w-3.5" />}
          </button>
          {!showPreview && !streaming && canExpand ? (
            <button
              type="button"
              onClick={() => setCodeBlockExpanded(blockKey, !expanded)}
              aria-expanded={expanded}
              aria-label={expanded ? "收起代码" : "展开代码"}
              title={expanded ? "收起代码" : "展开代码"}
              className={toolbarButtonClass}
            >
              {expanded ? <ChevronUp className="h-3.5 w-3.5" /> : <ChevronDown className="h-3.5 w-3.5" />}
            </button>
          ) : null}
        </div>

        <div
          data-inline-view={showPreview ? "preview" : "source"}
          className="code-block-body relative overflow-hidden transition-[max-height] motion-panel"
          style={bodyMaxHeight ? { maxHeight: bodyMaxHeight } : undefined}
        >
          <div className="relative">
            <div
              ref={sourceRef}
              aria-hidden={showPreview}
              inert={showPreview}
              className={showPreview
                ? "pointer-events-none absolute inset-x-0 top-0 opacity-0"
                : "relative opacity-100"}
            >
              <div className={expanded ? "code-scrollbar overflow-x-auto" : "code-scrollbar max-h-full overflow-auto"}>
                <div className="shiki-code-block" dangerouslySetInnerHTML={{ __html: highlightedHtml }} />
              </div>
            </div>
            {previewable && !streaming ? (
              <div
                aria-hidden={!showPreview}
                inert={!showPreview}
                className={showPreview
                  ? "relative opacity-100"
                  : "pointer-events-none absolute inset-x-0 top-0 opacity-0"}
              >
                <InlinePreview
                  artifact={previewArtifact}
                  onReady={handleInlinePreviewReady}
                  onError={handleInlinePreviewError}
                />
              </div>
            ) : null}
          </div>
          {!showPreview && !streaming && !expanded && canExpand ? (
            <div className="pointer-events-none absolute inset-x-0 bottom-0 h-12 bg-gradient-to-t from-background to-transparent" />
          ) : null}
          {previewDialogError || inlinePreviewError ? <div role="status" className="border-t border-border/60 px-3 py-2 text-xs text-destructive">{previewDialogError || inlinePreviewError}</div> : null}
        </div>
      </div>
      <PreviewDialog
        artifact={previewArtifact}
        phase={previewDialogPhase}
        onReady={() => setPreviewDialogPhase("open")}
        onError={() => {
          setPreviewDialogPhase("idle")
          setPreviewDialogError("预览未能打开，可在消息中查看源码。")
        }}
        onClose={() => setPreviewDialogPhase("idle")}
      />
    </>
  )
})

const InlinePreview = memo(function InlinePreview({ artifact, onReady, onError }: { artifact: PreviewArtifact; onReady: () => void; onError: () => void }) {
  useEffect(() => {
    if (artifact.type === "markdown") onReady()
  }, [artifact.type, onReady])

  if (artifact.type === "markdown") {
    return (
      <div className="markdown-body overflow-auto px-4 py-3 text-[15px] leading-[22.5px]">
        <ReactMarkdown remarkPlugins={[remarkGfm, remarkBreaks]}>{artifact.content}</ReactMarkdown>
      </div>
    )
  }

  if (artifact.type === "mermaid") {
    return <MermaidPreview code={artifact.content} className="diagram-inline-preview" onReady={onReady} onError={onError} />
  }

  if (artifact.type === "mindmap") {
    return <MindMapPreview code={artifact.content} className="diagram-inline-preview" onReady={onReady} onError={onError} />
  }

  if (artifact.type === "graphviz") {
    return <GraphvizPreview code={artifact.content} engine={artifact.language} className="diagram-inline-preview" onReady={onReady} onError={onError} />
  }

  if (artifact.type === "html" || artifact.type === "svg") {
    return (
      <div className="max-h-[min(56dvh,30rem)] min-h-[240px] overflow-auto bg-background">
        <HtmlPreviewFrame id={artifact.id} type={artifact.type} content={artifact.content} staticPreview onReady={onReady} onError={onError} />
      </div>
    )
  }

  return (
    <HighlightedSource code={artifact.content} language={artifact.language} />
  )
})

const HighlightedSource = memo(function HighlightedSource({ code, language }: { code: string; language?: string }) {
  const lightColorTheme = useUIStore((s) => s.lightColorTheme)
  const darkColorTheme = useUIStore((s) => s.darkColorTheme)
  const displayCode = useMemo(() => compactCodeForDisplay(code), [code])
  const highlightIdentity = `${lightColorTheme}:${darkColorTheme}:${language || ""}:${displayCode}`
  const [highlightState, setHighlightState] = useState(() => ({
    identity: highlightIdentity,
    html: fallbackCodeHtml(displayCode),
  }))

  useEffect(() => {
    let canceled = false
    const identity = highlightIdentity
    void highlightCode(displayCode, language, lightColorTheme, darkColorTheme)
      .then((nextHtml) => {
        if (!canceled) setHighlightState({ identity, html: nextHtml })
      })
      .catch(() => {
        if (!canceled) setHighlightState({ identity, html: fallbackCodeHtml(displayCode) })
      })
    return () => {
      canceled = true
    }
  }, [darkColorTheme, displayCode, highlightIdentity, language, lightColorTheme])

  const html = highlightState.identity === highlightIdentity
    ? highlightState.html
    : fallbackCodeHtml(displayCode)
  // html comes from Shiki or fallbackCodeHtml, both of which escape user code.
  return <div className="shiki-code-block" dangerouslySetInnerHTML={{ __html: html }} />
})

function defaultCodeBlockMode(type: PreviewArtifactType, streaming: boolean) {
  if (streaming) return "source"
  return isDefaultInlinePreviewType(type) ? "preview" : "source"
}

function fallbackCodeHtml(code: string) {
  return `<pre class="shiki"><code>${escapeHtml(code)}</code></pre>`
}
