import { useLayoutEffect, useRef, useState, type ReactNode } from "react"
import { cn } from "@/lib/utils"

type DrillView = "main" | "detail"
type ViewDirection = "forward" | "back"

export function MotionDrill({
  view,
  main,
  detail,
  className,
}: {
  view: DrillView
  main: ReactNode
  detail: ReactNode
  className?: string
}) {
  const mainContentRef = useRef<HTMLDivElement>(null)
  const detailContentRef = useRef<HTMLDivElement>(null)
  const [height, setHeight] = useState<number>()

  useLayoutEffect(() => {
    const active = view === "main" ? mainContentRef.current : detailContentRef.current
    if (!active) return

    let frame = 0
    const update = () => {
      cancelAnimationFrame(frame)
      frame = requestAnimationFrame(() => setHeight(active.getBoundingClientRect().height))
    }

    update()

    if (typeof ResizeObserver !== "undefined") {
      const observer = new ResizeObserver(update)
      observer.observe(active)
      return () => {
        cancelAnimationFrame(frame)
        observer.disconnect()
      }
    }

    window.addEventListener("resize", update)
    return () => {
      cancelAnimationFrame(frame)
      window.removeEventListener("resize", update)
    }
  }, [detail, main, view])

  return (
    <div className={cn("motion-drill-frame", className)} data-view={view} style={height ? { height } : undefined}>
      <div className="motion-drill-layer motion-drill-main" aria-hidden={view !== "main"} inert={view !== "main" ? true : undefined}>
        <div ref={mainContentRef} className="motion-drill-content">{main}</div>
      </div>
      <div className="motion-drill-layer motion-drill-detail" aria-hidden={view !== "detail"} inert={view !== "detail" ? true : undefined}>
        <div ref={detailContentRef} className="motion-drill-content">{detail}</div>
      </div>
    </div>
  )
}

export function MotionView({
  viewKey,
  direction = "forward",
  children,
  className,
}: {
  viewKey: string | number
  direction?: ViewDirection
  children: ReactNode
  className?: string
}) {
  return (
    <div key={`${viewKey}:${direction}`} className={cn("motion-view", direction === "back" ? "motion-view-back" : "motion-view-forward", className)}>
      {children}
    </div>
  )
}
