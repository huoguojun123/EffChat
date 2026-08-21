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

export const composerIconButtonClass = "h-11 w-11 shrink-0 rounded-[9px] border-0 bg-transparent p-0 text-muted-foreground shadow-none transition-[background-color,color] motion-control hover:bg-accent/80 hover:text-foreground active:bg-accent focus-visible:ring-2 focus-visible:ring-ring/50 sm:h-8 sm:w-8"

// Chat chrome uses one quiet raised surface so adjacent controls do not look like
// unrelated floating layers across light and dark themes.
export const chatSurfaceControlClass = "rounded-[10px] border border-border/45 bg-popover/42 shadow-sm backdrop-blur-md transition-[background-color,border-color,color,box-shadow] motion-control hover:bg-popover/68"

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
