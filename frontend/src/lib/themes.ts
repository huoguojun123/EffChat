export type AppearanceMode = "light" | "dark" | "system"
export type ColorThemeId = "codex" | "parchment" | "github" | "catppuccin" | "everforest" | "gruvbox" | "one"
export type AccentId = "default" | "blue" | "purple" | "green" | "amber" | "rose"

export interface ColorThemeDefinition {
  id: ColorThemeId
  label: string
  lightSwatches: [string, string, string]
  darkSwatches: [string, string, string]
  shikiLight: ShikiThemeId
  shikiDark: ShikiThemeId
}

export interface AccentDefinition {
  id: AccentId
  label: string
  color: string
}

export type ShikiThemeId =
  | "github-light"
  | "github-dark"
  | "catppuccin-latte"
  | "catppuccin-mocha"
  | "everforest-light"
  | "everforest-dark"
  | "gruvbox-light-medium"
  | "gruvbox-dark-medium"
  | "one-light"
  | "one-dark-pro"

export const COLOR_THEMES: ColorThemeDefinition[] = [
  { id: "codex", label: "默认", lightSwatches: ["#f4f8fc", "#e3ebf4", "#0672ce"], darkSwatches: ["#10151b", "#1d2935", "#4ea1ff"], shikiLight: "github-light", shikiDark: "github-dark" },
  { id: "parchment", label: "Parchment", lightSwatches: ["#f4ebdc", "#e9dcc7", "#c64f2f"], darkSwatches: ["#211914", "#3a2b22", "#f08a5b"], shikiLight: "one-light", shikiDark: "one-dark-pro" },
  { id: "github", label: "GitHub", lightSwatches: ["#f0f6fc", "#dbe7f3", "#0969da"], darkSwatches: ["#080c12", "#1b2633", "#58a6ff"], shikiLight: "github-light", shikiDark: "github-dark" },
  { id: "catppuccin", label: "Catppuccin", lightSwatches: ["#e7e9f5", "#cdd2e8", "#7c3aed"], darkSwatches: ["#11111b", "#313244", "#cba6f7"], shikiLight: "catppuccin-latte", shikiDark: "catppuccin-mocha" },
  { id: "everforest", label: "Everforest", lightSwatches: ["#f1ebcf", "#d4dfc0", "#58740f"], darkSwatches: ["#222b28", "#3a493f", "#b5cc69"], shikiLight: "everforest-light", shikiDark: "everforest-dark" },
  { id: "gruvbox", label: "Gruvbox", lightSwatches: ["#f9e4ad", "#e5c984", "#076678"], darkSwatches: ["#1d2021", "#3c3836", "#83a598"], shikiLight: "gruvbox-light-medium", shikiDark: "gruvbox-dark-medium" },
  { id: "one", label: "One", lightSwatches: ["#f3f5fa", "#dfe4ee", "#3b5bdb"], darkSwatches: ["#1e222a", "#303640", "#61afef"], shikiLight: "one-light", shikiDark: "one-dark-pro" },
]

export const ACCENTS: AccentDefinition[] = [
  { id: "default", label: "主题默认", color: "linear-gradient(135deg,#6b7280 0 50%,#d1d5db 50%)" },
  { id: "blue", label: "蓝色", color: "#1685f8" },
  { id: "purple", label: "紫色", color: "#8b5cf6" },
  { id: "green", label: "绿色", color: "#2f9e62" },
  { id: "amber", label: "琥珀色", color: "#d28a12" },
  { id: "rose", label: "玫红色", color: "#e0527d" },
]

export const DEFAULT_LIGHT_THEME: ColorThemeId = "codex"
export const DEFAULT_DARK_THEME: ColorThemeId = "codex"
export const DEFAULT_ACCENT: AccentId = "default"

const colorThemeIds = new Set(COLOR_THEMES.map((item) => item.id))
const accentIds = new Set(ACCENTS.map((item) => item.id))

export function normalizeColorTheme(value: string | null): ColorThemeId {
  return colorThemeIds.has(value as ColorThemeId) ? value as ColorThemeId : DEFAULT_LIGHT_THEME
}

export function normalizeAccent(value: string | null): AccentId {
  return accentIds.has(value as AccentId) ? value as AccentId : DEFAULT_ACCENT
}

export function colorTheme(id: ColorThemeId) {
  return COLOR_THEMES.find((item) => item.id === id) || COLOR_THEMES[0]
}

export function activeColorTheme(light: ColorThemeId, dark: ColorThemeId, isDark: boolean) {
  return colorTheme(isDark ? dark : light)
}
