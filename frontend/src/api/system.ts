import { api } from "./client"
import type { FontAsset } from "@/types"

export interface ChatFonts {
  chinese?: FontAsset | null
  latin?: FontAsset | null
  code?: FontAsset | null
}

export interface SystemInfo {
  system_name: string
  version: string
  chat_font?: FontAsset | null
  chat_fonts?: ChatFonts
}

export const systemApi = {
  getInfo() {
    return api.get<SystemInfo>("/system/info")
  },
}
