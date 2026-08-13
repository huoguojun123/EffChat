import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react"
import { readDiagramFontFamily, sanitizeFontFamily } from "@/components/workspace/diagramSvg"

interface Props {
  id: string
  type: "html" | "svg"
  content: string
  staticPreview?: boolean
  onReady?: () => void
  onError?: (message: string) => void
}

export const HtmlPreviewFrame = memo(function HtmlPreviewFrame({ id, type, content, staticPreview = false, onReady, onError }: Props) {
  const srcDoc = useMemo(() => wrapPreview(type, content, readDiagramFontFamily()), [type, content])
  const frameRef = useRef<HTMLIFrameElement>(null)
  // 静态预览（消息内联）：让 iframe 长到内容实际高度，使外层 CodeBlock 成为唯一滚动容器，
  // 避免 iframe 自身再出一条（因 pointerEvents:none 而无法操作的）多余滚动条。
  const [autoHeight, setAutoHeight] = useState<number | null>(null)
  const [loaded, setLoaded] = useState(false)

  useEffect(() => {
    setAutoHeight(null)
    setLoaded(false)
  }, [id])

  const handleLoad = useCallback(() => {
    if (staticPreview) {
      const doc = frameRef.current?.contentDocument
      if (doc) {
        const h = Math.max(doc.documentElement.scrollHeight, doc.body?.scrollHeight ?? 0)
        if (h > 0) setAutoHeight(h)
      }
    }
    setLoaded(true)
    onReady?.()
  }, [onReady, staticPreview])

  const frame = <iframe
      ref={frameRef}
      key={id}
      title={id}
      // HTML/SVG preview is intentionally isolated in srcDoc. We allow same-origin
      // only so static previews can measure height; scripts, forms and popups stay disabled.
      sandbox="allow-same-origin"
      srcDoc={srcDoc}
      loading="eager"
      onLoad={handleLoad}
      onError={() => onError?.("预览内容无法加载")}
      className={staticPreview
        ? `block w-full min-w-0 border-0 bg-transparent transition-opacity duration-150 ease-out motion-control ${loaded ? "relative opacity-100" : "absolute inset-0 opacity-0"}`
        : "block w-full min-w-0 border-0 bg-transparent"}
      style={
        staticPreview
          ? { pointerEvents: "none", height: autoHeight ? `${autoHeight}px` : "240px" }
          : { height: "100%" }
      }
    />

  if (!staticPreview) return frame

  return (
    <div className="relative min-h-[240px]">
      {!loaded ? <pre className="max-h-[min(56dvh,30rem)] overflow-auto p-4 text-sm leading-6 text-foreground">{content}</pre> : null}
      {frame}
    </div>
  )
})

function wrapPreview(type: "html" | "svg", content: string, fontFamily: string) {
  const safeFontFamily = sanitizeFontFamily(fontFamily)
  if (type === "svg") {
    return `<!doctype html>
    <html lang="zh-CN">
	  <head><style>html,body{margin:0;min-height:100%;background:transparent;}body{display:flex;align-items:center;justify-content:center;}svg text,svg tspan{font-family:${safeFontFamily}!important;}</style></head>
  <body>
    ${content}
  </body>
</html>`
  }

  const baseFontStyle = `<style data-effchat-preview-font>html,body{font-family:${safeFontFamily};}</style>`
  const headTag = content.match(/<head\b[^>]*>/i)?.[0]
  if (headTag) return content.replace(headTag, `${headTag}${baseFontStyle}`)

  const htmlTag = content.match(/<html\b[^>]*>/i)?.[0]
  if (htmlTag) return content.replace(htmlTag, `${htmlTag}<head>${baseFontStyle}</head>`)

  return `<!doctype html><html lang="zh-CN"><head>${baseFontStyle}</head><body>${content}</body></html>`
}
