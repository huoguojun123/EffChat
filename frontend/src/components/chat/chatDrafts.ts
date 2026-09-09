import type { AttachmentMeta } from "@/types"

const storageKey = "effchat:session-drafts"
export const chatDraftSessionRemovedEvent = "effchat:session-draft-removed"

export interface StoredSubmission {
  id: number
  sessionId: number
  clientRunId: string
  content: string
  attachments: AttachmentMeta[]
  attachmentIds: number[]
  thinkingEffort?: string
  status: "sending" | "failed"
}

export interface ChatDraftState {
  drafts: Record<number, string>
  submissions: Record<number, StoredSubmission>
}

export const emptyChatDraftState = (): ChatDraftState => ({ drafts: {}, submissions: {} })

export function loadChatDraftState(): ChatDraftState {
  try {
    if (typeof sessionStorage === "undefined") return emptyChatDraftState()
    return decodeChatDraftState(sessionStorage.getItem(storageKey))
  } catch {
    // Browser privacy settings can reject even a read. The editor must still
    // start with an in-memory state instead of failing the whole chat route.
    return emptyChatDraftState()
  }
}

export function saveChatDraftState(state: ChatDraftState): boolean {
  try {
    if (typeof sessionStorage === "undefined") return false
    if (Object.keys(state.drafts).length === 0 && Object.keys(state.submissions).length === 0) {
      sessionStorage.removeItem(storageKey)
    } else {
      sessionStorage.setItem(storageKey, JSON.stringify({ version: 2, ...state }))
    }
    return true
  } catch {
    // Storage may be disabled or full. The composer remains usable in memory;
    // callers must not treat a failed browser write as a successful handoff.
    return false
  }
}

export function loadChatDrafts(): Record<number, string> {
  return loadChatDraftState().drafts
}

export function saveChatDrafts(drafts: Record<number, string>) {
  saveChatDraftState({ drafts, submissions: {} })
}

/** Remove all composer state that belongs to a deleted session. */
export function removeChatDraftSession(sessionId: number): boolean {
  if (!Number.isSafeInteger(sessionId) || sessionId <= 0) return false
  const current = loadChatDraftState()
  if (current.drafts[sessionId] === undefined && current.submissions[sessionId] === undefined) return true
  const drafts = { ...current.drafts }
  const submissions = { ...current.submissions }
  delete drafts[sessionId]
  delete submissions[sessionId]
  const saved = saveChatDraftState({ drafts, submissions })
  if (typeof window !== "undefined") {
    window.dispatchEvent(new CustomEvent(chatDraftSessionRemovedEvent, { detail: { sessionId } }))
  }
  return saved
}

export function decodeChatDrafts(raw: string | null): Record<number, string> {
  return decodeChatDraftState(raw).drafts
}

export function decodeChatDraftState(raw: string | null): ChatDraftState {
  if (!raw) return emptyChatDraftState()
  try {
    const value = JSON.parse(raw) as Record<string, unknown>
    if (!value || typeof value !== "object" || Array.isArray(value)) return emptyChatDraftState()

    // Read the beta.9 flat shape without rewriting it until the next state change.
    const source = value.version === 2 && value.drafts && typeof value.drafts === "object"
      ? value.drafts as Record<string, unknown>
      : value
    const drafts: Record<number, string> = {}
    for (const [key, draft] of Object.entries(source)) {
      const sessionId = Number(key)
      if (Number.isSafeInteger(sessionId) && sessionId > 0 && typeof draft === "string" && draft !== "") {
        drafts[sessionId] = draft
      }
    }

    const submissions: Record<number, StoredSubmission> = {}
    if (value.version === 2 && value.submissions && typeof value.submissions === "object") {
      for (const [key, rawSubmission] of Object.entries(value.submissions as Record<string, unknown>)) {
        const sessionId = Number(key)
        const parsed = decodeStoredSubmission(rawSubmission)
        if (parsed && Number.isSafeInteger(sessionId) && sessionId > 0 && parsed.sessionId === sessionId) {
          submissions[sessionId] = parsed
        }
      }
    }
    return { drafts, submissions }
  } catch {
    return emptyChatDraftState()
  }
}

function decodeStoredSubmission(value: unknown): StoredSubmission | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null
  const record = value as Record<string, unknown>
  const id = Number(record.id)
  const sessionId = Number(record.sessionId)
  if (!Number.isSafeInteger(id) || id <= 0 || !Number.isSafeInteger(sessionId) || sessionId <= 0) return null
  if (typeof record.clientRunId !== "string" || !record.clientRunId.trim()) return null
  if (typeof record.content !== "string" || !record.content.trim()) return null
  if (!Array.isArray(record.attachments) || !Array.isArray(record.attachmentIds)) return null
  if (record.status !== "sending" && record.status !== "failed") return null
  return {
    id,
    sessionId,
    clientRunId: record.clientRunId,
    content: record.content,
    attachments: record.attachments as AttachmentMeta[],
    attachmentIds: record.attachmentIds.filter((item): item is number => Number.isSafeInteger(item) && item > 0),
    ...(typeof record.thinkingEffort === "string" && record.thinkingEffort ? { thinkingEffort: record.thinkingEffort } : {}),
    status: record.status,
  }
}
