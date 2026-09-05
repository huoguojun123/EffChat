import { create } from "zustand"
import { systemApi, type ChatFonts } from "@/api/system"
import type { FontAsset } from "@/types"

const DEFAULT_SYSTEM_NAME = "EffChat"
const DEFAULT_SYSTEM_VERSION = "0.4.1-beta.9"
const CHAT_FONT_STYLE_ID = "effchat-chat-font-face"
// Font-family order alone cannot route glyphs because most CJK fonts also
// contain ASCII. Disjoint ranges make the visible Chinese and Latin slots own
// their scripts; the code slot stays unrestricted on its separate CSS variable.
const CHAT_FONT_UNICODE_RANGES = {
  chinese: "U+1100-11FF, U+2E80-2FFF, U+3000-303F, U+3040-30FF, U+3100-318F, U+3190-319F, U+31C0-31FF, U+3200-33FF, U+3400-4DBF, U+4E00-9FFF, U+AC00-D7AF, U+F900-FAFF, U+FF00-FFEF, U+20000-323AF",
  latin: "U+0000-024F, U+1E00-1EFF, U+2C60-2C7F, U+A720-A7FF",
} as const

interface SystemState {
  systemName: string
  systemVersion: string
  chatFont: FontAsset | null
  chatFonts: ChatFonts
  load: () => Promise<void>
}

// 系统名称和版本来自后端公开端点，供 sidebar、标签页和菜单展示消费。
// 公开端点无需登录，App 启动即拉取；失败时保留默认名（首屏不阻塞）。
export const useSystemStore = create<SystemState>((set) => ({
  systemName: DEFAULT_SYSTEM_NAME,
  systemVersion: DEFAULT_SYSTEM_VERSION,
  chatFont: null,
  chatFonts: {},
  load: async () => {
    try {
      const info = await systemApi.getInfo()
      const name = info.system_name?.trim() || DEFAULT_SYSTEM_NAME
      const version = info.version?.trim() || DEFAULT_SYSTEM_VERSION
      const chatFont = info.chat_font || null
      const chatFonts = normalizeChatFonts(info.chat_fonts, chatFont)
      set({ systemName: name, systemVersion: version, chatFont, chatFonts })
      document.title = name
      applyChatFonts(chatFonts)
    } catch {
      set({ chatFont: null, chatFonts: {} })
      applyChatFonts({})
    }
  },
}))

export function normalizeChatFonts(fonts: ChatFonts | undefined, legacyFont: FontAsset | null): ChatFonts {
  return {
    chinese: fonts?.chinese === undefined ? legacyFont : fonts.chinese,
    latin: fonts?.latin === undefined ? legacyFont : fonts.latin,
    code: fonts?.code === undefined ? legacyFont : fonts.code,
  }
}

function applyChatFonts(fonts: ChatFonts) {
  const root = document.documentElement
  const existing = document.getElementById(CHAT_FONT_STYLE_ID)
  const entries = [
    { slot: "chinese", font: fonts.chinese || null },
    { slot: "latin", font: fonts.latin || null },
    { slot: "code", font: fonts.code || null },
  ] as const
  const active = entries.filter((entry) => entry.font?.file_url)

  if (active.length === 0) {
    existing?.remove()
    root.style.removeProperty("--chat-font-family")
    root.style.removeProperty("--chat-code-font-family")
    return
  }

  const style = existing || document.createElement("style")
  style.id = CHAT_FONT_STYLE_ID
  style.textContent = active.map(({ slot, font }) => font ? chatFontFaceRule(slot, font) : "").join("\n")
  if (!existing) document.head.appendChild(style)

  const chineseFamily = fonts.chinese?.file_url ? `"${fontFamilyName("chinese", fonts.chinese)}"` : ""
  const latinFamily = fonts.latin?.file_url ? `"${fontFamilyName("latin", fonts.latin)}"` : ""
  if (chineseFamily || latinFamily) {
    const bodyFamilies = [chineseFamily, latinFamily, "var(--font-sans)"].filter(Boolean).join(", ")
    root.style.setProperty("--chat-font-family", bodyFamilies)
  } else {
    root.style.setProperty("--chat-font-family", "var(--font-sans)")
  }

  if (fonts.code?.file_url) {
    root.style.setProperty("--chat-code-font-family", `"${fontFamilyName("code", fonts.code)}", var(--font-mono)`)
  } else {
    root.style.removeProperty("--chat-code-font-family")
  }
}

export function chatFontFaceRule(slot: "chinese" | "latin" | "code", font: FontAsset) {
  const unicodeRange = slot === "code" ? "" : `\n  unicode-range: ${CHAT_FONT_UNICODE_RANGES[slot]};`
  return `
@font-face {
  font-family: "${fontFamilyName(slot, font)}";
  src: url("${font.file_url}") format("${fontFormat(font)}");
  font-weight: ${font.weight || 400};
  font-style: ${font.style || "normal"};
  font-display: swap;${unicodeRange}
}`
}

function fontFamilyName(slot: "chinese" | "latin" | "code", font: FontAsset) {
  return `EffChatFont-${slot}-${font.id}`
}

function fontFormat(font: FontAsset) {
  if (font.mime_type === "font/woff2" || font.file_name.endsWith(".woff2")) return "woff2"
  if (font.mime_type === "font/woff" || font.file_name.endsWith(".woff")) return "woff"
  if (font.mime_type === "font/otf" || font.file_name.endsWith(".otf")) return "opentype"
  return "truetype"
}
