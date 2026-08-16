import { Minus, Plus, RotateCcw, Scan } from "lucide-react"
import { type PointerEvent, type ReactNode, useEffect, useMemo, useRef, useState } from "react"
import { readableDiagramScale } from "@/components/workspace/diagramSvg"
import { cn } from "@/lib/utils"

interface Props {
  naturalWidth: number
  naturalHeight: number
  children: ReactNode
  className?: string
  fill?: boolean
  maxTextSize?: number
  targetTextSize?: number
  initialFocus?: { x: number; y: number }
}

type ViewMode = "readable" | "fit" | "custom"

const padding = 24
const minScale = 0.05
const maxScale = 4
const inlineMinHeight = 240
const inlineMaxHeight = 480

export function DiagramViewport({
  naturalWidth,
  naturalHeight,
  children,
  className,
  fill = false,
  maxTextSize = 0,
  targetTextSize = 14,
  initialFocus,
}: Props) {
  const rootRef = useRef<HTMLDivElement>(null)
  const scrollRef = useRef<HTMLDivElement>(null)
  const initialScrollAppliedRef = useRef(false)
  const dragRef = useRef({ active: false, x: 0, y: 0, left: 0, top: 0 })
  const [size, setSize] = useState({ width: 0, height: 0 })
  const [mode, setMode] = useState<ViewMode>("readable")
  const [customScale, setCustomScale] = useState(1)
  const [dragging, setDragging] = useState(false)

  const readableScale = useMemo(() => {
    return clamp(readableDiagramScale(maxTextSize, targetTextSize), minScale, maxScale)
  }, [maxTextSize, targetTextSize])

  const inlineHeight = useMemo(() => {
    const viewportMax = typeof window === "undefined" ? inlineMaxHeight : Math.max(inlineMinHeight, Math.min(inlineMaxHeight, window.innerHeight * 0.56))
    return Math.min(viewportMax, Math.max(inlineMinHeight, naturalHeight * readableScale + padding * 2))
  }, [naturalHeight, readableScale])

  const viewportHeight = fill ? size.height : inlineHeight
  const fitScale = useMemo(() => {
    if (size.width <= 0 || viewportHeight <= 0) return readableScale
    return clamp(Math.min(
      (size.width - padding * 2) / naturalWidth,
      (viewportHeight - padding * 2) / naturalHeight,
    ), minScale, maxScale)
  }, [naturalHeight, naturalWidth, readableScale, size.width, viewportHeight])

  const scale = mode === "fit" ? fitScale : mode === "readable" ? readableScale : customScale
  const scaledWidth = naturalWidth * scale
  const scaledHeight = naturalHeight * scale
  const canvasWidth = Math.max(size.width, scaledWidth + padding * 2)
  const canvasHeight = Math.max(viewportHeight, scaledHeight + padding * 2)
  const contentLeft = Math.max(padding, (canvasWidth - scaledWidth) / 2)
  const contentTop = Math.max(padding, (canvasHeight - scaledHeight) / 2)

  useEffect(() => {
    const root = rootRef.current
    if (!root || typeof ResizeObserver === "undefined") return
    const observer = new ResizeObserver(([entry]) => {
      setSize({ width: entry.contentRect.width, height: entry.contentRect.height })
    })
    observer.observe(root)
    setSize({ width: root.clientWidth, height: root.clientHeight })
    return () => observer.disconnect()
  }, [])

  useEffect(() => {
    const scroller = scrollRef.current
    if (!scroller || initialScrollAppliedRef.current || size.width <= 0 || viewportHeight <= 0) return
    initialScrollAppliedRef.current = true
    requestAnimationFrame(() => {
      if (initialFocus) {
        scroller.scrollLeft = Math.max(0, contentLeft + initialFocus.x * scale - 80)
        scroller.scrollTop = Math.max(0, contentTop + initialFocus.y * scale - scroller.clientHeight / 2)
      } else {
        scroller.scrollLeft = 0
        scroller.scrollTop = Math.max(0, (scroller.scrollHeight - scroller.clientHeight) / 2)
      }
    })
  }, [contentLeft, contentTop, initialFocus, scale, size.width, viewportHeight])

  function changeScale(nextScale: number) {
    const scroller = scrollRef.current
    const next = clamp(nextScale, minScale, maxScale)
    if (scroller) {
      const centerX = scroller.scrollLeft + scroller.clientWidth / 2
      const centerY = scroller.scrollTop + scroller.clientHeight / 2
      const ratio = next / scale
      requestAnimationFrame(() => {
        scroller.scrollLeft = centerX * ratio - scroller.clientWidth / 2
        scroller.scrollTop = centerY * ratio - scroller.clientHeight / 2
      })
    }
    setCustomScale(next)
    setMode("custom")
  }

  function handlePointerDown(event: PointerEvent<HTMLDivElement>) {
    if (event.pointerType !== "mouse" || event.button !== 0 || (event.target as HTMLElement).closest("button")) return
    const scroller = scrollRef.current
    if (!scroller || (scroller.scrollWidth <= scroller.clientWidth && scroller.scrollHeight <= scroller.clientHeight)) return
    dragRef.current = {
      active: true,
      x: event.clientX,
      y: event.clientY,
      left: scroller.scrollLeft,
      top: scroller.scrollTop,
    }
    setDragging(true)
    event.currentTarget.setPointerCapture(event.pointerId)
  }

  function handlePointerMove(event: PointerEvent<HTMLDivElement>) {
    const drag = dragRef.current
    const scroller = scrollRef.current
    if (!drag.active || !scroller) return
    scroller.scrollLeft = drag.left - (event.clientX - drag.x)
    scroller.scrollTop = drag.top - (event.clientY - drag.y)
  }

  function stopDragging(event: PointerEvent<HTMLDivElement>) {
    dragRef.current.active = false
    setDragging(false)
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId)
  }

  return (
    <div
      ref={rootRef}
      className={cn("relative w-full overflow-hidden bg-background", fill ? "h-full min-h-0" : "min-h-[240px]", className)}
      style={fill ? undefined : { height: `${inlineHeight}px` }}
    >
      <div className="diagram-viewport-toolbar absolute right-2 top-2 z-10 flex items-center rounded-md border border-border/55 bg-popover/70 p-0.5 shadow-[0_4px_14px_rgba(0,0,0,0.07)] backdrop-blur-xl">
        <ViewportButton label="缩小" onClick={() => changeScale(scale / 1.2)} disabled={scale <= minScale}>
          <Minus className="h-4 w-4" />
        </ViewportButton>
        <span className="w-10 text-center text-xs tabular-nums text-muted-foreground sm:w-11" aria-live="polite">
          {Math.round(scale * 100)}%
        </span>
        <ViewportButton label="放大" onClick={() => changeScale(scale * 1.2)} disabled={scale >= maxScale}>
          <Plus className="h-4 w-4" />
        </ViewportButton>
        <ViewportButton label="适应窗口" onClick={() => setMode("fit")} active={mode === "fit"}>
          <Scan className="h-4 w-4" />
        </ViewportButton>
        <ViewportButton label="恢复可读比例" onClick={() => setMode("readable")} active={mode === "readable"}>
          <RotateCcw className="h-4 w-4" />
        </ViewportButton>
      </div>

      <div
        ref={scrollRef}
        className={cn("h-full w-full overflow-auto overscroll-auto", dragging ? "cursor-grabbing select-none" : "cursor-grab")}
        onDoubleClick={() => setMode((current) => current === "fit" ? "readable" : "fit")}
        onPointerDown={handlePointerDown}
        onPointerMove={handlePointerMove}
        onPointerUp={stopDragging}
        onPointerCancel={stopDragging}
      >
        <div className="relative" style={{ width: `${canvasWidth}px`, height: `${canvasHeight}px` }}>
          <div
            className="absolute origin-top-left"
            style={{
              left: `${contentLeft}px`,
              top: `${contentTop}px`,
              width: `${naturalWidth}px`,
              height: `${naturalHeight}px`,
              transform: `scale(${scale})`,
            }}
          >
            {children}
          </div>
        </div>
      </div>
    </div>
  )
}

export function DiagramLoadingPlaceholder({ className, label }: { className?: string; label: string }) {
  return <div className={cn("h-[min(56dvh,480px)] min-h-[240px] w-full bg-muted/20", className)} aria-busy="true" aria-label={label} />
}

function ViewportButton({
  label,
  onClick,
  children,
  active = false,
  disabled = false,
}: {
  label: string
  onClick: () => void
  children: ReactNode
  active?: boolean
  disabled?: boolean
}) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      aria-pressed={active || undefined}
      disabled={disabled}
      onClick={onClick}
      className={cn(
        "flex h-10 w-10 items-center justify-center rounded text-muted-foreground transition-colors motion-control hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-35 sm:h-8 sm:w-8",
        active && "bg-accent text-foreground",
      )}
    >
      {children}
    </button>
  )
}

function clamp(value: number, min: number, max: number) {
  return Math.min(max, Math.max(min, value))
}
