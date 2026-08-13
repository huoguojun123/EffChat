const storageKey = "effchat:session-drafts"

export function loadChatDrafts(): Record<number, string> {
  if (typeof sessionStorage === "undefined") return {}
  return decodeChatDrafts(sessionStorage.getItem(storageKey))
}

export function saveChatDrafts(drafts: Record<number, string>) {
  if (typeof sessionStorage === "undefined") return
  if (Object.keys(drafts).length === 0) {
    sessionStorage.removeItem(storageKey)
    return
  }
  sessionStorage.setItem(storageKey, JSON.stringify(drafts))
}

export function decodeChatDrafts(raw: string | null): Record<number, string> {
  if (!raw) return {}
  try {
    const value = JSON.parse(raw) as Record<string, unknown>
    if (!value || typeof value !== "object" || Array.isArray(value)) return {}
    const drafts: Record<number, string> = {}
    for (const [key, draft] of Object.entries(value)) {
      const sessionId = Number(key)
      if (Number.isSafeInteger(sessionId) && sessionId > 0 && typeof draft === "string" && draft !== "") {
        drafts[sessionId] = draft
      }
    }
    return drafts
  } catch {
    return {}
  }
}
