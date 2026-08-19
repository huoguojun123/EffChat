import { describe, expect, it } from "vitest"
import {
  DESKTOP_SIDEBAR_MAX_WIDTH,
  DESKTOP_SIDEBAR_MIN_WIDTH,
  clampDesktopSidebarWidth,
  desktopSidebarMaxWidth,
  normalizeDesktopSidebarPreference,
} from "@/lib/sidebarWidth"

describe("desktop sidebar width", () => {
  it("keeps the absolute width bounds on ordinary desktop viewports", () => {
    expect(clampDesktopSidebarWidth(180, 1280)).toBe(DESKTOP_SIDEBAR_MIN_WIDTH)
    expect(clampDesktopSidebarWidth(320, 1280)).toBe(320)
    expect(clampDesktopSidebarWidth(500, 1280)).toBe(DESKTOP_SIDEBAR_MAX_WIDTH)
  })

  it("reserves the main content width on narrow desktop viewports", () => {
    expect(desktopSidebarMaxWidth(880)).toBe(320)
    expect(desktopSidebarMaxWidth(769)).toBe(DESKTOP_SIDEBAR_MIN_WIDTH)
    expect(clampDesktopSidebarWidth(360, 880)).toBe(320)
  })

  it("rejects invalid persisted preferences", () => {
    expect(normalizeDesktopSidebarPreference(Number.NaN)).toBeNull()
    expect(normalizeDesktopSidebarPreference(239)).toBeNull()
    expect(normalizeDesktopSidebarPreference(361)).toBeNull()
    expect(normalizeDesktopSidebarPreference(301.6)).toBe(302)
  })
})
