import { useEffect, useRef, useState } from "react"
import { prefersReducedMotion } from "@/lib/motionPreference"

// useTypewriter 把「网络到达的文本」与「实际显示的文本」解耦：display 以恒定节奏
// 朝 target 追赶，落后越多追得越快（有下限保证不停顿），追平后空转。这样无论模型
// 一次吐一大段还是卡顿，观感都均匀 —— 解决「忽快忽慢」。
//
// active=false（流结束/未开始）时直接对齐到 target，避免收尾拖尾；尊重 reduced-motion。
export function useTypewriter(target: string, active: boolean): string {
  const prefersReduced = prefersReducedMotion()

  const [display, setDisplay] = useState(() => (active && !prefersReduced ? "" : target))
  const lenRef = useRef(display.length)
  const targetRef = useRef(target)
  const rafRef = useRef<number | null>(null)
  const lastPaintRef = useRef(0)

  // render 期间不写 ref；把 target 同步放进 effect（tick 读取最新值即可）。
  useEffect(() => {
    targetRef.current = target
  }, [target])

  useEffect(() => {
    if (!active || prefersReduced) {
      lenRef.current = targetRef.current.length
      setDisplay(targetRef.current)
      if (rafRef.current) cancelAnimationFrame(rafRef.current)
      rafRef.current = null
      return
    }

    function tick(now: number) {
      const t = targetRef.current
      const cur = lenRef.current
      const canPaint = now - lastPaintRef.current >= 33
      if (cur > t.length) {
        // target 被重置为更短（新一轮流）：直接对齐到当前 target。
        lenRef.current = t.length
        setDisplay(t)
        lastPaintRef.current = now
      } else if (cur < t.length && canPaint) {
        const backlog = t.length - cur
        const step = Math.max(1, Math.ceil(backlog / 8))
        const next = Math.min(t.length, cur + step)
        lenRef.current = next
        setDisplay(t.slice(0, next))
        lastPaintRef.current = now
      }
      rafRef.current = requestAnimationFrame(tick)
    }
    rafRef.current = requestAnimationFrame(tick)
    return () => {
      if (rafRef.current) cancelAnimationFrame(rafRef.current)
      rafRef.current = null
    }
  }, [active, prefersReduced])

  return display
}
