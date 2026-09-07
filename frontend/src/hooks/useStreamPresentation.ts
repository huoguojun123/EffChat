import { useEffect, useState } from "react"
import { nextStreamPresentationUnit, presentationDelayMs } from "@/lib/streamPresentation"
import { prefersReducedMotion } from "@/lib/motionPreference"

const PARTIAL_RELEASE_DELAY_MS = 180
const MAX_PRESENTATION_OWNERS = 64

interface Runtime {
  target: string
  displayedEnd: number
  nextBirthAt: number
  partialAfter: number
  frame: number
  controller: symbol | null
}

const runtimes = new Map<string, Runtime>()

function createRuntime(): Runtime {
  return { target: "", displayedEnd: 0, nextBirthAt: 0, partialAfter: 0, frame: 0, controller: null }
}

function getRuntime(ownerKey: string) {
  const existing = runtimes.get(ownerKey)
  if (existing) return existing
  const runtime = createRuntime()
  runtimes.set(ownerKey, runtime)
  if (runtimes.size > MAX_PRESENTATION_OWNERS) {
    const oldest = runtimes.entries().next().value as [string, Runtime] | undefined
    if (oldest) {
      cancelAnimationFrame(oldest[1].frame)
      runtimes.delete(oldest[0])
    }
  }
  return runtime
}

export function shouldContinueStreamPresentation(ownerKey: string, target: string) {
  const runtime = runtimes.get(ownerKey)
  if (!runtime) return false
  return runtime.displayedEnd < runtime.target.length
    || (runtime.target.length > 0 && target.length > runtime.target.length && target.startsWith(runtime.target))
}

// Network reception remains eager in useSSE. The owner runtime survives the
// live-to-durable component handoff, so only the released prefix can affect
// layout and the terminal message cannot flush a hidden burst all at once.
export function useStreamPresentation(target: string, ownerKey: string, enabled: boolean) {
  const runtime = runtimes.get(ownerKey)
  const [displayedContent, setDisplayedContent] = useState(() => (
    enabled ? target.slice(0, Math.min(runtime?.displayedEnd ?? 0, target.length)) : target
  ))
  const [revision, setRevision] = useState(0)

  useEffect(() => {
    const reducedMotion = prefersReducedMotion()
    if (!enabled || reducedMotion) {
      const current = runtimes.get(ownerKey)
      if (!current) {
        const frame = requestAnimationFrame(() => setDisplayedContent(target))
        return () => cancelAnimationFrame(frame)
      }
      cancelAnimationFrame(current.frame)
      current.target = target
      current.displayedEnd = target.length
      current.nextBirthAt = 0
      current.partialAfter = 0
      current.controller = null
      current.frame = requestAnimationFrame(() => setDisplayedContent(target))
      return () => cancelAnimationFrame(current.frame)
    }

    const current = getRuntime(ownerKey)
    const controller = Symbol(ownerKey)
    current.controller = controller

    if (!target.startsWith(current.target)) {
      // Retry rollback is not append-only. Prefer correctness over replaying a
      // stale answer: show the authoritative replacement immediately, then
      // resume pacing for later append-only deltas.
      cancelAnimationFrame(current.frame)
      current.target = target
      current.displayedEnd = target.length
      current.nextBirthAt = 0
      current.partialAfter = 0
      current.frame = requestAnimationFrame(() => setDisplayedContent(target))
      return () => {
        if (runtimes.get(ownerKey)?.controller === controller) cancelAnimationFrame(current.frame)
      }
    }

    current.target = target
    if (current.displayedEnd >= target.length) return

    const tick = (now: number) => {
      if (document.visibilityState === "hidden") return
      const active = runtimes.get(ownerKey)
      if (!active || active.controller !== controller || active.displayedEnd >= active.target.length) return
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
    return () => {
      const active = runtimes.get(ownerKey)
      if (active?.controller === controller) {
        cancelAnimationFrame(active.frame)
        active.frame = 0
        active.controller = null
      }
    }
  }, [enabled, ownerKey, revision, target])

  useEffect(() => {
    const resume = () => {
      const current = runtimes.get(ownerKey)
      if (!current) return
      if (document.visibilityState !== "visible" || !enabled || current.displayedEnd >= current.target.length) return
      current.nextBirthAt = Math.min(current.nextBirthAt, performance.now())
      // A revision only re-enters the same owner scheduler; it does not create
      // another copy of the displayed prefix or a competing timing loop.
      setRevision((value) => value + 1)
    }
    document.addEventListener("visibilitychange", resume)
    return () => document.removeEventListener("visibilitychange", resume)
  }, [enabled, ownerKey])

  return displayedContent
}
