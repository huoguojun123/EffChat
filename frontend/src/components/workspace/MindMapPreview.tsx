import { useEffect, useMemo } from "react"
import { DiagramViewport } from "@/components/workspace/DiagramViewport"
import { layoutMindMap, parseMindMap } from "@/components/workspace/mindmap"
import { useUIStore } from "@/stores/ui"

interface Props {
  code: string
  className?: string
  fill?: boolean
  onReady?: () => void
  onError?: (message: string) => void
}

export function MindMapPreview({ code, className, fill = false, onReady, onError }: Props) {
  const chatFontScale = useUIStore((state) => state.chatFontScale)
  const result = useMemo(() => {
    try {
      return { layout: layoutMindMap(parseMindMap(code)), error: "" }
    } catch (error) {
      return { layout: null, error: error instanceof Error ? error.message : "思维导图渲染失败" }
    }
  }, [code])

  useEffect(() => {
    if (result.error) onError?.(result.error)
    else if (result.layout) onReady?.()
  }, [onError, onReady, result.error, result.layout])

  if (!result.layout) {
    return (
      <div className={className || "max-h-[360px] overflow-auto bg-background p-4"}>
        <div role="alert" className="mb-3 text-sm text-destructive">{result.error}</div>
        <pre className="whitespace-pre-wrap text-sm leading-6 text-foreground">{code}</pre>
      </div>
    )
  }

  const { layout } = result
  const rootNode = layout.nodes.find((node) => node.depth === 0)
  return (
    <DiagramViewport
      key={code}
      naturalWidth={layout.width}
      naturalHeight={layout.height}
      className={className}
      fill={fill}
      maxTextSize={14}
      targetTextSize={14 * chatFontScale}
      initialFocus={{
        x: rootNode?.x || 0,
        y: (rootNode?.y || 0) + (rootNode?.height || 0) / 2,
      }}
    >
      <svg
        width={layout.width}
        height={layout.height}
        viewBox={`0 0 ${layout.width} ${layout.height}`}
        className="block h-full w-full"
        role="img"
        aria-label="思维导图"
        style={{ fontFamily: "var(--chat-font-family, var(--font-serif))" }}
      >
        <g fill="none" stroke="var(--border)" strokeWidth="1.5">
          {layout.connections.map(({ from, to }) => {
            const startX = from.x + from.width
            const startY = from.y + from.height / 2
            const endX = to.x
            const endY = to.y + to.height / 2
            const bend = Math.max(28, (endX - startX) * 0.48)
            return (
              <path
                key={`${from.id}-${to.id}`}
                d={`M ${startX} ${startY} C ${startX + bend} ${startY}, ${endX - bend} ${endY}, ${endX} ${endY}`}
                vectorEffect="non-scaling-stroke"
              />
            )
          })}
        </g>
        {layout.nodes.map((node) => (
          <g key={node.id} className="mindmap-node" transform={`translate(${node.x} ${node.y})`}>
            <rect
              width={node.width}
              height={node.height}
              rx="7"
              fill={node.depth === 0 ? "var(--accent)" : "var(--popover)"}
              stroke={node.depth === 0 ? "var(--primary)" : "var(--border)"}
              strokeWidth={node.depth === 0 ? 1.5 : 1}
              vectorEffect="non-scaling-stroke"
            />
            <text
              x="12"
              y={(node.height - node.lines.length * 19) / 2 + 14}
              fill="var(--fg)"
              fontSize="14"
              fontWeight={node.depth === 0 ? 600 : 450}
            >
              {node.lines.map((line, index) => (
                <tspan key={`${node.id}-${index}`} x="12" dy={index === 0 ? 0 : 19}>{line}</tspan>
              ))}
            </text>
          </g>
        ))}
      </svg>
    </DiagramViewport>
  )
}
