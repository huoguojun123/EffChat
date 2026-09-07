import { useEffect, useRef, useState } from "react"
import { nextStreamPresentationUnit, presentationDelayMs } from "@/lib/streamPresentation"
import { prefersReducedMotion } from "@/lib/motionPreference"

const PARTIAL_RELEASE_DELAY_MS = 180

interface Runtime {
  ownerKey: string
  target: string
  displayedEnd: number
  nextBirthAt: number
  partialAfter: number
  frame: number
}

function createRuntime(ownerKey: string): Runtime {
  return { ownerKey, target: "", displayedEnd: 0, nextBirthAt: 0, partialAfter: 0, frame: 0 }
}

// Network reception remains eager in useSSE. This hook owns only the visual
// prefix, so hidden backlog cannot change layout or pull the viewport first.
export function useStreamPresentation(target: string, ownerKey: string, enabled: boolean) {
  const runtimeRef = useRef<Runtime>(createRuntime(ownerKey))
  const [displayedContent, setDisplayedContent] = useState(enabled ? "" : target)
  const [revision, setRevision] = useState(0)

  useEffect(() => {
    const runtime = runtimeRef.current
    const reducedMotion = prefersReducedMotion()
    if (!enabled || reducedMotion) {
      cancelAnimationFrame(runtime.frame)
      runtimeRef.current = { ...createRuntime(ownerKey), target, displayedEnd: target.length }
      runtimeRef.current.frame = requestAnimationFrame(() => setDisplayedContent(target))
      return
    }

    if (runtime.ownerKey !== ownerKey) {
      cancelAnimationFrame(runtime.frame)
      runtimeRef.current = createRuntime(ownerKey)
      runtimeRef.current.frame = requestAnimationFrame(() => setDisplayedContent(""))
    } else if (!target.startsWith(runtime.target)) {
      // Retry rollback is not append-only. Prefer correctness over replaying a
      // stale answer: show the authoritative replacement immediately, then
      // resume pacing for later append-only deltas.
      cancelAnimationFrame(runtime.frame)
      runtime.target = target
      runtime.displayedEnd = target.length
      runtime.nextBirthAt = 0
      runtime.partialAfter = 0
      runtime.frame = requestAnimationFrame(() => setDisplayedContent(target))
      return
    }

    const current = runtimeRef.current
    current.target = target
    if (current.displayedEnd >= target.length) return

    const tick = (now: number) => {
      if (document.visibilityState === "hidden") return
      const active = runtimeRef.current
      if (active.ownerKey !== ownerKey || active.displayedEnd >= active.target.length) return
      const allowPartial = active.partialAfter > 0 && now >= active.partialAfter
      const unit = nextStreamPresentationUnit(active.target, active.displayedEnd, allowPartial)
      if (!unit) {
        active.partialAfter ||= now + PARTIAL_RELEASE_DELAY_MS
        active.frame = requestAnimationFrame(tick)
        return
      }
      if (now < active.nextBirthAt) {
        active.frame = requestAnimationFrame(tick)
        return
      }
      active.displayedEnd = unit.end
      active.nextBirthAt = now + presentationDelayMs(unit.visualCost)
      active.partialAfter = 0
      setDisplayedContent(active.target.slice(0, unit.end))
      active.frame = requestAnimationFrame(tick)
    }

    current.frame = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(current.frame)
  }, [enabled, ownerKey, revision, target])

  useEffect(() => {
    const resume = () => {
      const runtime = runtimeRef.current
      if (document.visibilityState !== "visible" || !enabled || runtime.displayedEnd >= runtime.target.length) return
      runtime.nextBirthAt = Math.min(runtime.nextBirthAt, performance.now())
      // Restart the same timing owner after the tab becomes visible. A new
      // revision only schedules work; it never changes the displayed prefix.
      setRevision((current) => current + 1)
    }
    document.addEventListener("visibilitychange", resume)
    return () => document.removeEventListener("visibilitychange", resume)
  }, [enabled])

  useEffect(() => () => cancelAnimationFrame(runtimeRef.current.frame), [])

  return displayedContent
}
