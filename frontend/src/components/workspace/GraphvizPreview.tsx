import { useEffect, useMemo, useState } from "react"
import { DiagramViewport } from "@/components/workspace/DiagramViewport"
import { readDiagramRenderCache, writeDiagramRenderCache } from "@/components/workspace/diagramRenderCache"
import { prepareDiagramSvg, readDiagramFontFamily, readResolvedThemeColor } from "@/components/workspace/diagramSvg"
import { buildGraphvizSandboxSrcDoc } from "./graphvizSandbox"
import { useUIStore } from "@/stores/ui"
import { useDeferredDiagramTheme } from "@/hooks/useDeferredDiagramTheme"

interface Props {
  code: string
  engine?: string
  className?: string
  fill?: boolean
  onReady?: () => void
  onError?: (message: string) => void
}

const graphvizEngines = new Set(["dot", "neato", "fdp", "sfdp", "circo", "twopi", "osage", "patchwork"])

type GraphvizModule = {
  Graphviz: {
    load: () => Promise<{
      layout: (code: string, format?: string, engine?: string) => string
    }>
  }
}

export function GraphvizPreview({ code, engine = "dot", className, fill = false, onReady, onError }: Props) {
  const chatFontScale = useUIStore((state) => state.chatFontScale)
  const [theme, themeHostRef] = useDeferredDiagramTheme(readTheme, 320 + (code.length % 5) * 80)
  const layoutEngine = normalizeGraphvizEngine(engine)
  const renderKey = `${layoutEngine}:${code}`
  const [result, setResult] = useState(() => {
    const diagram = readDiagramRenderCache("graphviz", renderKey)
    return { key: diagram ? renderKey : "", diagram, error: "" }
  })
  const cachedDiagram = readDiagramRenderCache("graphviz", renderKey)
  const diagram = result.key === renderKey ? result.diagram : cachedDiagram
  const error = result.key === renderKey ? result.error : ""
  const srcDoc = useMemo(() => buildGraphvizSandboxSrcDoc(diagram?.svg || "", theme.isDark, theme), [diagram?.svg, theme])
  const frameKey = useMemo(() => `${layoutEngine}:${theme.key}:${hashString(diagram?.svg || "")}`, [diagram?.svg, layoutEngine, theme.key])

  useEffect(() => {
    let cancelled = false
    if (readDiagramRenderCache("graphviz", renderKey)) return

    import("@hpcc-js/wasm/graphviz").then((mod) => (
      (mod as unknown as GraphvizModule).Graphviz.load()
    )).then((graphviz) => {
      const diagram = prepareDiagramSvg(graphviz.layout(code, "svg", layoutEngine))
      if (cancelled) return
      writeDiagramRenderCache("graphviz", renderKey, diagram)
      setResult({ key: renderKey, diagram, error: "" })
    }).catch((err) => {
      if (cancelled) return
      setResult({ key: renderKey, diagram: null, error: err instanceof Error ? err.message : "Graphviz 渲染失败" })
    })

    return () => {
      cancelled = true
    }
  }, [code, layoutEngine, renderKey])

  useEffect(() => {
    if (error) onError?.(error)
  }, [error, onError])

  if (error) {
    return (
      <pre className={className || "max-h-[360px] overflow-auto bg-background p-4 text-sm leading-6 text-foreground"}>
        {code}
      </pre>
    )
  }

  return (
    diagram ? (
      <DiagramViewport
        key={frameKey}
        naturalWidth={diagram.width}
        naturalHeight={diagram.height}
        className={className}
        fill={fill}
        maxTextSize={diagram.maxTextSize}
        targetTextSize={14 * chatFontScale}
        initialFocus={diagram.initialFocus}
      >
        <iframe
          ref={themeHostRef}
          key={frameKey}
          title="Graphviz preview"
          sandbox=""
          srcDoc={srcDoc}
          onLoad={onReady}
          className="pointer-events-none block h-full w-full border-0 bg-transparent"
        />
      </DiagramViewport>
    ) : <SourceFallback code={code} className={className} />
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
  if (typeof document === "undefined") return { key: "light:default", isDark: false, background: "#ffffff", foreground: "#171717", fontFamily: "" }
  const root = document.documentElement
  const isDark = root.classList.contains("dark")
  const colorTheme = root.dataset.colorTheme || "codex"
  const accent = root.dataset.accent || "default"
  return {
    key: `${isDark ? "dark" : "light"}:${colorTheme}:${accent}:${readDiagramFontFamily()}`,
    isDark,
    background: readResolvedThemeColor("--bg", isDark ? "#111827" : "#ffffff"),
    foreground: readResolvedThemeColor("--fg", isDark ? "#e5e7eb" : "#171717"),
    fontFamily: readDiagramFontFamily(),
  }
}

function normalizeGraphvizEngine(engine: string) {
  const normalized = engine.toLowerCase().replace(/^graphviz-/, "")
  return graphvizEngines.has(normalized) ? normalized : "dot"
}

function hashString(value: string) {
  let hash = 5381
  for (let i = 0; i < value.length; i++) {
    hash = ((hash << 5) + hash) ^ value.charCodeAt(i)
  }
  return (hash >>> 0).toString(36)
}
