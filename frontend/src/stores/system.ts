import { create } from "zustand"
import { systemApi, type ChatFonts } from "@/api/system"
import type { FontAsset } from "@/types"

const DEFAULT_SYSTEM_NAME = "EffChat"
const DEFAULT_SYSTEM_VERSION = "pre-release 0.3.4"
const CHAT_FONT_STYLE_ID = "fchat-chat-font-face"

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

function normalizeChatFonts(fonts: ChatFonts | undefined, legacyFont: FontAsset | null): ChatFonts {
  return {
    chinese: fonts?.chinese || legacyFont || null,
    latin: fonts?.latin || legacyFont || null,
    code: fonts?.code || legacyFont || null,
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
  style.textContent = active.map(({ slot, font }) => {
    if (!font) return ""
    const family = fontFamilyName(slot, font)
    return `
@font-face {
  font-family: "${family}";
  src: url("${font.file_url}") format("${fontFormat(font)}");
  font-weight: ${font.weight || 400};
  font-style: ${font.style || "normal"};
  font-display: swap;
}`
  }).join("\n")
  if (!existing) document.head.appendChild(style)

  const chineseFamily = fonts.chinese?.file_url ? `"${fontFamilyName("chinese", fonts.chinese)}"` : ""
  const latinFamily = fonts.latin?.file_url ? `"${fontFamilyName("latin", fonts.latin)}"` : ""
  if (chineseFamily || latinFamily) {
    const bodyFamilies = [chineseFamily, latinFamily, "var(--font-serif)"].filter(Boolean).join(", ")
    root.style.setProperty("--chat-font-family", bodyFamilies)
  } else {
    root.style.setProperty("--chat-font-family", "var(--font-serif)")
  }

  if (fonts.code?.file_url) {
    root.style.setProperty("--chat-code-font-family", `"${fontFamilyName("code", fonts.code)}", var(--font-mono)`)
  } else {
    root.style.removeProperty("--chat-code-font-family")
  }
}

function fontFamilyName(slot: "chinese" | "latin" | "code", font: FontAsset) {
  return `FChatFont-${slot}-${font.id}`
}

function fontFormat(font: FontAsset) {
  if (font.mime_type === "font/woff2" || font.file_name.endsWith(".woff2")) return "woff2"
  if (font.mime_type === "font/woff" || font.file_name.endsWith(".woff")) return "woff"
  if (font.mime_type === "font/otf" || font.file_name.endsWith(".otf")) return "opentype"
  return "truetype"
}
