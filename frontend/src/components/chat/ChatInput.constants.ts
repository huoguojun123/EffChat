import type { CSSProperties } from "react"
import type { UploadLimits } from "@/api/files"

export type SearchMode = "off" | "auto"

export const SEARCH_MODE_LABEL: Record<SearchMode, string> = {
  off: "联网：关闭",
  auto: "联网：开启",
}

const COMPOSER_TEXTAREA_FALLBACK_MIN_HEIGHT = 54
export const COMPOSER_TEXTAREA_MOBILE_MAX_HEIGHT = 132
export const COMPOSER_TEXTAREA_DESKTOP_MAX_HEIGHT = 180

export function getComposerTextareaMinHeight(ownerDocument?: Document) {
  const raw = ownerDocument?.defaultView
    ?.getComputedStyle(ownerDocument.documentElement)
    .getPropertyValue("--chat-composer-height")
  const value = Number.parseFloat(raw || "")
  return Number.isFinite(value) && value > 0 ? value : COMPOSER_TEXTAREA_FALLBACK_MIN_HEIGHT
}

export function getComposerTextareaMaxHeight(viewportWidth: number) {
  if (viewportWidth >= 640) return COMPOSER_TEXTAREA_DESKTOP_MAX_HEIGHT
  return COMPOSER_TEXTAREA_MOBILE_MAX_HEIGHT
}

export const composerIconButtonClass = "relative h-10 w-9 shrink-0 border-0 bg-transparent p-0 text-muted-foreground shadow-none transition-colors motion-control before:absolute before:inset-x-0.5 before:inset-y-1 before:rounded-[7px] before:border before:border-white/35 before:bg-popover/32 before:shadow-[inset_0_1px_0_rgba(255,255,255,0.24),0_4px_12px_rgba(0,0,0,0.07)] before:backdrop-blur-xl before:backdrop-saturate-150 before:transition-[background-color,box-shadow] hover:bg-transparent hover:text-foreground hover:before:bg-popover/58 hover:before:shadow-[inset_0_1px_0_rgba(255,255,255,0.32),0_5px_15px_rgba(0,0,0,0.1)] active:bg-transparent dark:before:border-white/10 [&>svg]:relative [&>svg]:z-10 sm:h-8 sm:w-8 sm:before:inset-0"

export function motionIndex(index: number): CSSProperties {
  return { "--motion-index": index } as CSSProperties
}

export const DEFAULT_UPLOAD_LIMITS: UploadLimits = {
  maxFileSizeMb: 20,
  maxSessionFiles: 50,
  allowedTypes: [
    "image/png",
    "image/jpeg",
    "image/gif",
    "image/webp",
    "application/pdf",
    "text/*",
    "application/json",
    "application/xml",
    "application/vnd.ms-excel",
    "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
    "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
    "application/vnd.openxmlformats-officedocument.presentationml.presentation",
  ],
}

export const ACCEPT_ATTR =
  "image/png,image/jpeg,image/gif,image/webp,application/pdf,text/*,application/json,application/xml,.md,.markdown,.csv,.tsv,.json,.docx,.xlsx,.pptx,.txt,.log,.yaml,.yml,.xml"
