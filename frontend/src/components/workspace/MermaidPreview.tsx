import { useEffect, useId, useState } from "react"
import { DiagramViewport } from "@/components/workspace/DiagramViewport"
import { readDiagramRenderCache, writeDiagramRenderCache } from "@/components/workspace/diagramRenderCache"
import { measureRenderedDiagramTextSize, normalizeMermaidTypography, prepareDiagramSvg, readDiagramFontFamily, readResolvedThemeColor } from "@/components/workspace/diagramSvg"
import { mermaidRenderConfig } from "@/components/workspace/mermaidConfig"
import { useUIStore } from "@/stores/ui"
import { useDeferredDiagramTheme } from "@/hooks/useDeferredDiagramTheme"

interface Props {
  code: string
  className?: string
  fill?: boolean
  onReady?: () => void
  onError?: (message: string) => void
}

export function MermaidPreview({ code, className, fill = false, onReady, onError }: Props) {
  const id = useId().replace(/:/g, "")
  const chatFontScale = useUIStore((state) => state.chatFontScale)
  const [theme, themeHostRef] = useDeferredDiagramTheme(readTheme, 320 + (code.length % 5) * 80)
  const renderKey = `${code}:${theme.isDark}:${theme.background}:${theme.foreground}:${theme.accent}:${theme.fontFamily}`
  const [result, setResult] = useState<{
    key: string
    diagram: { svg: string; width: number; height: number; maxTextSize: number; initialFocus?: { x: number; y: number } }
    error: string
  }>(() => {
    const diagram = readDiagramRenderCache("mermaid", renderKey)
    return diagram
      ? { key: renderKey, diagram, error: "" }
      : { key: "", diagram: { svg: "", width: 800, height: 450, maxTextSize: 14 }, error: "" }
  })
  const cachedDiagram = readDiagramRenderCache("mermaid", renderKey)
  const diagram = result.key === renderKey
    ? result.diagram
    : cachedDiagram || { svg: "", width: 800, height: 450, maxTextSize: 14 }
  const error = result.key === renderKey ? result.error : ""

  useEffect(() => {
    let cancelled = false
    if (readDiagramRenderCache("mermaid", renderKey)) return

    import("mermaid").then(({ default: mermaid }) => {
      mermaid.initialize(mermaidRenderConfig(theme.isDark, theme))

      return mermaid.render(`mermaid-${id}`, code)
    }).then((result) => {
      if (cancelled) return
      const diagram = prepareDiagramSvg(normalizeMermaidTypography(result.svg, theme.fontFamily))
      const preparedDiagram = { ...diagram, maxTextSize: measureRenderedDiagramTextSize(diagram.svg, diagram.maxTextSize) }
      writeDiagramRenderCache("mermaid", renderKey, preparedDiagram)
      setResult({
        key: renderKey,
        diagram: preparedDiagram,
        error: "",
      })
    }).catch((err) => {
      if (cancelled) return
      setResult({
        key: renderKey,
        diagram: { svg: "", width: 800, height: 450, maxTextSize: 14 },
        error: err instanceof Error ? err.message : "图表渲染失败",
      })
    })

    return () => {
      cancelled = true
    }
  }, [code, id, renderKey, theme])

  useEffect(() => {
    if (error) onError?.(error)
    else if (diagram.svg) onReady?.()
  }, [diagram.svg, error, onError, onReady])

  if (error) {
    return (
      <pre className={className || "max-h-[360px] overflow-auto bg-background p-4 text-sm leading-6 text-foreground"}>
        {code}
      </pre>
    )
  }

  if (!diagram.svg) {
    return <SourceFallback code={code} className={className} />
  }

  return (
    <DiagramViewport
      key={`${id}:${renderKey}`}
      naturalWidth={diagram.width}
      naturalHeight={diagram.height}
      className={className}
      fill={fill}
      maxTextSize={diagram.maxTextSize}
      targetTextSize={14 * chatFontScale}
      initialFocus={diagram.initialFocus}
    >
      <div
        ref={themeHostRef}
        className="h-full w-full"
        // Mermaid is the only HTML producer for this sink. Keep securityLevel strict
        // in mermaidRenderConfig so user diagram text cannot opt into scripts or raw HTML.
        dangerouslySetInnerHTML={{ __html: diagram.svg }}
      />
    </DiagramViewport>
  )
}

function SourceFallback({ code, className }: { code: string; className?: string }) {
  return (
    <pre className={`m-0 whitespace-pre-wrap overflow-auto bg-background p-4 font-mono text-sm leading-6 text-foreground ${className || "max-h-[360px]"}`}>
      {code}
    </pre>
  )
}

function readTheme() {
  if (typeof document === "undefined") return { isDark: false, background: "#ffffff", foreground: "#171717", accent: "#2563eb", fontFamily: "" }
  const root = document.documentElement
  return {
    isDark: root.classList.contains("dark"),
    background: readResolvedThemeColor("--bg", "#ffffff"),
    foreground: readResolvedThemeColor("--fg", "#171717"),
    accent: readResolvedThemeColor("--primary", "#2563eb"),
    fontFamily: readDiagramFontFamily(),
  }
}
