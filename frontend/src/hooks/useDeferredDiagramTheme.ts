import { useCallback, useEffect, useRef, useState } from "react"

export function useDeferredDiagramTheme<T>(readTheme: () => T, delayMs: number) {
  const [theme, setTheme] = useState(readTheme)
  const visibleRef = useRef(false)
  const pendingRef = useRef<T | null>(null)
  const timerRef = useRef<number | null>(null)
  const intersectionRef = useRef<IntersectionObserver | null>(null)
  const flush = useCallback((delay = 0) => {
    if (timerRef.current != null) window.clearTimeout(timerRef.current)
    timerRef.current = window.setTimeout(() => {
      timerRef.current = null
      if (!visibleRef.current || pendingRef.current == null) return
      const next = pendingRef.current
      pendingRef.current = null
      setTheme(next)
    }, delay)
  }, [])

  const hostRef = useCallback((node: HTMLElement | null) => {
    intersectionRef.current?.disconnect()
    intersectionRef.current = null
    if (!node) {
      visibleRef.current = false
      return
    }
    if (typeof IntersectionObserver === "undefined") {
      visibleRef.current = true
      flush()
      return
    }
    const observer = new IntersectionObserver(([entry]) => {
      visibleRef.current = entry.isIntersecting
      if (entry.isIntersecting) flush(40)
    }, { rootMargin: "240px 0px" })
    intersectionRef.current = observer
    observer.observe(node)
  }, [flush])

  useEffect(() => {
    if (typeof MutationObserver === "undefined") return
    const observer = new MutationObserver(() => {
      pendingRef.current = readTheme()
      flush(delayMs)
    })
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["class", "data-color-theme", "data-accent", "style"],
    })
    return () => observer.disconnect()
  }, [delayMs, flush, readTheme])

  useEffect(() => () => {
    if (timerRef.current != null) window.clearTimeout(timerRef.current)
    intersectionRef.current?.disconnect()
  }, [])

  return [theme, hostRef] as const
}
