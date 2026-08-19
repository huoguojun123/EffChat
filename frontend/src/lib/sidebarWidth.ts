export const DESKTOP_SIDEBAR_MIN_WIDTH = 240
export const DESKTOP_SIDEBAR_MAX_WIDTH = 360
export const DESKTOP_MAIN_MIN_WIDTH = 560
export const DESKTOP_SIDEBAR_STEP = 16

export function desktopSidebarMaxWidth(viewportWidth: number) {
  if (!Number.isFinite(viewportWidth)) return DESKTOP_SIDEBAR_MAX_WIDTH
  return Math.max(
    DESKTOP_SIDEBAR_MIN_WIDTH,
    Math.min(DESKTOP_SIDEBAR_MAX_WIDTH, Math.floor(viewportWidth - DESKTOP_MAIN_MIN_WIDTH))
  )
}

export function clampDesktopSidebarWidth(width: number, viewportWidth: number) {
  const normalized = Number.isFinite(width) ? Math.round(width) : DESKTOP_SIDEBAR_MIN_WIDTH
  return Math.min(
    Math.max(normalized, DESKTOP_SIDEBAR_MIN_WIDTH),
    desktopSidebarMaxWidth(viewportWidth)
  )
}

export function normalizeDesktopSidebarPreference(width: number) {
  if (!Number.isFinite(width) || width < DESKTOP_SIDEBAR_MIN_WIDTH || width > DESKTOP_SIDEBAR_MAX_WIDTH) return null
  return Math.round(width)
}

export function applyDesktopSidebarWidth(width: number | null) {
  if (typeof document === "undefined") return
  if (width === null) {
    document.documentElement.style.removeProperty("--desktop-sidebar-width")
    return
  }
  const viewportWidth = typeof window === "undefined" ? Number.POSITIVE_INFINITY : window.innerWidth
  document.documentElement.style.setProperty("--desktop-sidebar-width", `${clampDesktopSidebarWidth(width, viewportWidth)}px`)
}
