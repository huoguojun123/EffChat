export type AppearanceMode = "light" | "dark" | "system"
export type ColorThemeId = "codex" | "parchment" | "github" | "catppuccin" | "everforest" | "gruvbox" | "one"
export type AccentId = "default" | "blue" | "purple" | "green" | "amber" | "rose"

export interface ColorThemeDefinition {
  id: ColorThemeId
  label: string
  shikiLight: ShikiThemeId
  shikiDark: ShikiThemeId
}

export interface AccentDefinition {
  id: AccentId
  label: string
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
  { id: "codex", label: "默认", shikiLight: "github-light", shikiDark: "github-dark" },
  { id: "parchment", label: "Parchment", shikiLight: "one-light", shikiDark: "one-dark-pro" },
  { id: "github", label: "GitHub", shikiLight: "github-light", shikiDark: "github-dark" },
  { id: "catppuccin", label: "Catppuccin", shikiLight: "catppuccin-latte", shikiDark: "catppuccin-mocha" },
  { id: "everforest", label: "Everforest", shikiLight: "everforest-light", shikiDark: "everforest-dark" },
  { id: "gruvbox", label: "Gruvbox", shikiLight: "gruvbox-light-medium", shikiDark: "gruvbox-dark-medium" },
  { id: "one", label: "One", shikiLight: "one-light", shikiDark: "one-dark-pro" },
]

export const ACCENTS: AccentDefinition[] = [
  { id: "default", label: "主题默认" },
  { id: "blue", label: "蓝色" },
  { id: "purple", label: "紫色" },
  { id: "green", label: "绿色" },
  { id: "amber", label: "琥珀色" },
  { id: "rose", label: "玫红色" },
]

// Preview colors name semantic theme roles rather than copying palette hex values.
// The settings UI resolves these variables inside the selected theme scope, so
// preview cards cannot drift from the CSS tokens that render the application.
export const THEME_PREVIEW_COLORS = [
  "var(--theme-bg)",
  "var(--theme-surface)",
  "var(--theme-default-accent)",
] as const

export function accentPreviewColor(id: AccentId) {
  return id === "default" ? "var(--theme-default-accent)" : `var(--accent-${id})`
}

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
