import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

// crypto.randomUUID 仅在安全上下文（https / localhost）可用；通过局域网 IP + http 访问时
// 它会抛错，导致依赖它的 send/retry 直接静默失败。这里降级到 RFC4122 v4 兼容实现。
export function safeUUID(): string {
  try {
    if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
      return crypto.randomUUID()
    }
  } catch {
    // fall through to manual implementation
  }
  return "10000000-1000-4000-8000-100000000000".replace(/[018]/g, (c) => {
    const n = Number(c)
    const r = (typeof crypto !== "undefined" && crypto.getRandomValues
      ? crypto.getRandomValues(new Uint8Array(1))[0]
      : Math.floor(Math.random() * 256)) & 15
    return (n ^ (r >> (n / 4))).toString(16)
  })
}
