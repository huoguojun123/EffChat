import type { NavigateFunction, NavigateOptions, To } from "react-router"

let fallbackTimer: number | null = null

export function navigateWithFade(navigate: NavigateFunction, to: To, options: NavigateOptions = {}) {
  const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches
  if (reducedMotion || typeof document.startViewTransition === "function") {
    navigate(to, { ...options, viewTransition: !reducedMotion })
    return
  }

  if (fallbackTimer != null) window.clearTimeout(fallbackTimer)
  document.documentElement.classList.add("app-route-leaving")
  fallbackTimer = window.setTimeout(() => {
    navigate(to, options)
    requestAnimationFrame(() => {
      requestAnimationFrame(() => document.documentElement.classList.remove("app-route-leaving"))
    })
    fallbackTimer = null
  }, 140)
}
