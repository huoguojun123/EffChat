import { create } from "zustand"
import type { CodeBlockMode, CodeBlockViewState, ReasoningStateSource, ReasoningViewState } from "@/types"
import { nextAutoCollapseState, nextReasoningState } from "./reasoningState"
import {
  normalizeAccent,
  normalizeColorTheme,
  type AccentId,
  type AppearanceMode,
  type ColorThemeId,
} from "@/lib/themes"

// 字号档位：85%–125%，步长 5%，共 9 档。滑块连续拖动但吸附到这些档位。
// 必须声明在 create() 之前：store 工厂在模块加载时立即执行并读取这些常量。
export const CHAT_FONT_STEPS = [0.85, 0.9, 0.95, 1, 1.05, 1.1, 1.15, 1.2, 1.25]
const DEFAULT_FONT_SCALE = 1
const SIDEBAR_OPEN_KEY = "sidebar_open"

// 把任意数值吸附到最近的档位。
function snapFontScale(scale: number): number {
  return CHAT_FONT_STEPS.reduce((best, step) =>
    Math.abs(step - scale) < Math.abs(best - scale) ? step : best
  , CHAT_FONT_STEPS[0])
}

// 读取初始字号：优先新键 chat_font_scale；否则迁移旧的 small/medium/large。
function readInitialFontScale(): number {
  const raw = storageGet("chat_font_scale")
  if (raw !== null) {
    const n = parseFloat(raw)
    if (!Number.isNaN(n)) return snapFontScale(n)
  }
  const legacy = storageGet("chat_font_size")
  const legacyMap: Record<string, number> = { small: 0.9, medium: 1, large: 1.15 }
  if (legacy && legacy in legacyMap) return legacyMap[legacy]
  return DEFAULT_FONT_SCALE
}

function readInitialSidebarOpen(): boolean {
  if (typeof window !== "undefined" && window.matchMedia("(max-width: 768px)").matches) return false
  const raw = storageGet(SIDEBAR_OPEN_KEY)
  if (raw === "false") return false
  if (raw === "true") return true
  return true
}

function writeSidebarOpen(open: boolean) {
  storageSet(SIDEBAR_OPEN_KEY, String(open))
}

interface UIState {
  sidebarOpen: boolean
  codeBlockStates: Record<string, CodeBlockViewState>
  reasoningOpenStates: Record<string, ReasoningViewState>
  theme: AppearanceMode
  lightColorTheme: ColorThemeId
  darkColorTheme: ColorThemeId
  accentColor: AccentId
  chatFontScale: number
  toggleSidebar: () => void
  setSidebarOpen: (open: boolean, persist?: boolean) => void
  setCodeBlockMode: (key: string, mode: CodeBlockMode) => void
  setCodeBlockExpanded: (key: string, expanded: boolean) => void
  resetCodeBlockStatesForSession: (sessionId: number) => void
  setReasoningOpen: (key: string, open: boolean, source?: ReasoningStateSource) => void
  markReasoningAutoCollapse: (key: string) => void
  clearReasoningState: (prefix: string) => void
  resetAccountState: () => void
  setTheme: (theme: AppearanceMode) => void
  setLightColorTheme: (theme: ColorThemeId) => void
  setDarkColorTheme: (theme: ColorThemeId) => void
  setAccentColor: (accent: AccentId) => void
  setChatFontScale: (scale: number) => void
}

export const useUIStore = create<UIState>()((set, get) => ({
  sidebarOpen: readInitialSidebarOpen(),
  codeBlockStates: {},
  reasoningOpenStates: {},
  theme: readAppearanceMode(),
  lightColorTheme: normalizeColorTheme(storageGet("light_color_theme")),
  darkColorTheme: normalizeColorTheme(storageGet("dark_color_theme")),
  accentColor: normalizeAccent(storageGet("accent_color")),
  chatFontScale: readInitialFontScale(),

  toggleSidebar: () => set((s) => {
    const next = !s.sidebarOpen
    writeSidebarOpen(next)
    return { sidebarOpen: next }
  }),
  setSidebarOpen: (open: boolean, persist = true) => {
    if (persist) writeSidebarOpen(open)
    set({ sidebarOpen: open })
  },
  setCodeBlockMode: (key: string, mode: CodeBlockMode) => set((s) => ({
    codeBlockStates: {
      ...s.codeBlockStates,
      [key]: {
        mode,
        expanded: s.codeBlockStates[key]?.expanded ?? false,
      },
    },
  })),
  setCodeBlockExpanded: (key: string, expanded: boolean) => set((s) => ({
    codeBlockStates: {
      ...s.codeBlockStates,
      [key]: {
        mode: s.codeBlockStates[key]?.mode ?? "source",
        expanded,
      },
    },
  })),
  resetCodeBlockStatesForSession: (sessionId: number) => set((s) => ({
    codeBlockStates: Object.fromEntries(
      Object.entries(s.codeBlockStates).filter(([key]) => !key.startsWith(`${sessionId}:`))
    ),
  })),
  setReasoningOpen: (key: string, open: boolean, source: ReasoningStateSource = "user") => set((s) => ({
    reasoningOpenStates: {
      ...s.reasoningOpenStates,
      [key]: nextReasoningState(s.reasoningOpenStates[key], open, source),
    },
  })),
  markReasoningAutoCollapse: (key: string) => set((s) => {
    const current = s.reasoningOpenStates[key]
    const next = nextAutoCollapseState(current)
    if (!next) return s
    return {
      reasoningOpenStates: {
        ...s.reasoningOpenStates,
        [key]: next,
      },
    }
  }),
  clearReasoningState: (prefix: string) => set((s) => ({
    reasoningOpenStates: Object.fromEntries(
      Object.entries(s.reasoningOpenStates).filter(([key]) => !key.startsWith(prefix))
    ),
  })),
  resetAccountState: () => set({
    codeBlockStates: {},
    reasoningOpenStates: {},
  }),
  setTheme: (theme: AppearanceMode) => {
    storageSet("theme", theme)
    set({ theme })
    applyAppearance(theme, get().lightColorTheme, get().darkColorTheme, get().accentColor, true)
  },
  setLightColorTheme: (lightColorTheme: ColorThemeId) => {
    storageSet("light_color_theme", lightColorTheme)
    set({ lightColorTheme })
    applyAppearance(get().theme, lightColorTheme, get().darkColorTheme, get().accentColor, true)
  },
  setDarkColorTheme: (darkColorTheme: ColorThemeId) => {
    storageSet("dark_color_theme", darkColorTheme)
    set({ darkColorTheme })
    applyAppearance(get().theme, get().lightColorTheme, darkColorTheme, get().accentColor, true)
  },
  setAccentColor: (accentColor: AccentId) => {
    storageSet("accent_color", accentColor)
    set({ accentColor })
    applyAppearance(get().theme, get().lightColorTheme, get().darkColorTheme, accentColor, true)
  },
  setChatFontScale: (scale: number) => {
    const snapped = snapFontScale(scale)
    storageSet("chat_font_scale", String(snapped))
    set({ chatFontScale: snapped })
    applyChatFontScale(snapped)
  },
}))

function applyChatFontScale(scale: number) {
  if (typeof document === "undefined") return
  document.documentElement.style.setProperty("--chat-font-scale", String(scale))
}

function readAppearanceMode(): AppearanceMode {
  const value = storageGet("theme")
  return value === "light" || value === "dark" || value === "system" ? value : "system"
}

let appearanceTransition: ViewTransition | null = null

function applyAppearance(theme: AppearanceMode, lightTheme: ColorThemeId, darkTheme: ColorThemeId, accent: AccentId, animate = false) {
  if (typeof document === "undefined") return
  const root = document.documentElement
  const dark = theme === "dark" || (theme === "system" && systemPrefersDark())

  const update = () => {
    root.classList.toggle("dark", dark)
    root.dataset.colorTheme = dark ? darkTheme : lightTheme
    root.dataset.accent = accent
    root.style.colorScheme = dark ? "dark" : "light"
    const updateThemeColor = () => {
      document.querySelector('meta[name="theme-color"]')?.setAttribute("content", getComputedStyle(root).getPropertyValue("--bg").trim())
    }
    updateThemeColor()
    if (typeof requestAnimationFrame === "function") requestAnimationFrame(updateThemeColor)
  }

  const reducedMotion = typeof window !== "undefined" && window.matchMedia?.("(prefers-reduced-motion: reduce)").matches
  // Chromium root view transitions can temporarily detach Radix dialog portals
  // from the interactive tree. Apply tokens directly while a modal is open so
  // appearance controls do not dismiss the dialog they are rendered inside.
  const hasOpenDialog = document.querySelector('[role="dialog"][data-state="open"]') !== null
  if (!animate || reducedMotion || hasOpenDialog || typeof document.startViewTransition !== "function") {
    update()
    return
  }

  appearanceTransition?.skipTransition()
  root.classList.add("theme-view-transition")
  const transition = document.startViewTransition(update)
  appearanceTransition = transition
  void transition.finished.finally(() => {
    if (appearanceTransition !== transition) return
    appearanceTransition = null
    root.classList.remove("theme-view-transition")
  })
}

function systemPrefersDark() {
  return typeof window !== "undefined" &&
    typeof window.matchMedia === "function" &&
    window.matchMedia("(prefers-color-scheme: dark)").matches
}

function watchSystemTheme() {
  if (typeof window === "undefined" || typeof window.matchMedia !== "function") return
  const media = window.matchMedia("(prefers-color-scheme: dark)")
  const update = () => {
    const state = useUIStore.getState()
    if (state.theme === "system") applyAppearance(state.theme, state.lightColorTheme, state.darkColorTheme, state.accentColor, true)
  }
  if (typeof media.addEventListener === "function") {
    media.addEventListener("change", update)
  } else {
    media.addListener?.(update)
  }
}

function storageGet(key: string) {
  return typeof localStorage === "undefined" ? null : localStorage.getItem(key)
}

function storageSet(key: string, value: string) {
  if (typeof localStorage !== "undefined") localStorage.setItem(key, value)
}

const initialUIState = useUIStore.getState()
applyAppearance(initialUIState.theme, initialUIState.lightColorTheme, initialUIState.darkColorTheme, initialUIState.accentColor)
applyChatFontScale(useUIStore.getState().chatFontScale)
watchSystemTheme()
