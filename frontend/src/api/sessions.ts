import { api } from "./client"
import type { Session, SessionFolder } from "@/types"
import type { Model } from "@/types"

export type SessionFolderScope = "all" | "unfiled" | number

export interface ConversationSearchResult {
  kind: "session" | "message"
  session_id: number
  session_title: string
  folder_id?: number | null
  message_id?: number
  turn_id?: number
  role?: "user" | "assistant"
  snippet: string
  created_at: string
}

export interface ListSessionsOptions {
  limit?: number
  offset?: number
  folderId?: SessionFolderScope
}

export interface ListSessionsResponse {
  sessions: Session[]
  has_more: boolean
  next_offset: number
}

export function listSessions(options: ListSessionsOptions = {}) {
  const limit = options.limit ?? 100
  const offset = options.offset ?? 0
  const folderId = options.folderId ?? "all"
  const params = new URLSearchParams({
    limit: String(limit),
    offset: String(offset),
  })
  if (folderId !== "all") params.set("folder_id", String(folderId))
  return api.get<ListSessionsResponse>(`/sessions?${params.toString()}`)
}

export function getSession(id: number) {
  return api.get<Session>(`/sessions/${id}`)
}

export function searchConversations(query: string, folderId: SessionFolderScope, searchAll = false, limit = 30) {
  const params = new URLSearchParams({ q: query, limit: String(limit) })
  if (searchAll || folderId === "all") {
    params.set("scope", "all")
  } else if (folderId === "unfiled") {
    params.set("scope", "unfiled")
  } else {
    params.set("scope", "folder")
    params.set("folder_id", String(folderId))
  }
  return api.get<{ results: ConversationSearchResult[] }>(`/search/conversations?${params.toString()}`)
}

export function createSession(data: { model_id?: string; provider?: Model["provider"]; folder_id?: number; system_prompt?: string }) {
  return api.post<Session>("/sessions", data)
}

export function updateSession(id: number, data: Partial<Pick<Session, "title" | "model_id" | "provider" | "folder_id" | "system_prompt" | "temperature" | "max_tokens" | "search_mode" | "memory_enabled">> & { pinned?: boolean }) {
  return api.patch<{ message: string }>(`/sessions/${id}`, data)
}

export async function exportSessionMarkdown(id: number, includeTools = false) {
  const params = new URLSearchParams({ include_tools: String(includeTools) })
  const response = await api.download(`/sessions/${id}/export.md?${params.toString()}`)
  const blob = await response.blob()
  const filename = downloadFilename(response.headers.get("Content-Disposition")) || `conversation-${id}.md`
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement("a")
  anchor.href = url
  anchor.download = filename
  anchor.hidden = true
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  window.setTimeout(() => URL.revokeObjectURL(url), 1000)
}

export function downloadFilename(disposition: string | null) {
  if (!disposition) return ""
  const encoded = /filename\*=UTF-8''([^;]+)/i.exec(disposition)?.[1]
  if (encoded) {
    try {
      return decodeURIComponent(encoded)
    } catch {
      return ""
    }
  }
  return /filename="([^"]+)"/i.exec(disposition)?.[1] || /filename=([^;]+)/i.exec(disposition)?.[1]?.trim() || ""
}

export interface SessionMemorySection {
  key: string
  title: string
  items: string[]
}

export interface SessionMemoryStats {
  chars: number
  max_chars: number
  soft_max_chars: number
  near_limit: boolean
  hard_limited: boolean
  item_count: number
}

export interface SessionMemoryChange {
  id: number
  session_id: number
  user_id: number
  source: "auto" | "manual" | "tool" | "compact" | "undo"
  action: "update" | "compact" | "clear" | "undo"
  summary: string
  created_at: string
  undone_at?: string | null
}

export interface ModelTaskRun {
  id: number
  task_key: "title_generation" | "compression" | "tool_extract_summary" | "memory_maintenance"
  source: "auto" | "manual" | "tool" | "system"
  status: "success" | "failed" | "skipped"
  provider?: string
  model_id?: string
  target_type?: string
  target_id?: string
  error_type?: string
  error_message?: string
  retry_after?: string | null
  started_at: string
  finished_at: string
  duration_ms: number
}

export interface SessionMemoryResponse {
  enabled: boolean
  content: string
  sections: SessionMemorySection[]
  stats: SessionMemoryStats
  changes: SessionMemoryChange[]
  last_auto_updated_at?: string
  last_task_run?: ModelTaskRun
  task_runs?: ModelTaskRun[]
  updated_at?: string
}

export function getSessionMemory(id: number) {
  return api.get<SessionMemoryResponse>(`/sessions/${id}/memory`)
}

export function saveSessionMemory(id: number, data: { enabled?: boolean; sections?: SessionMemorySection[]; content?: string; expected_updated_at?: string }) {
  return api.put<SessionMemoryResponse>(`/sessions/${id}/memory`, data)
}

export function memoryMaintenanceUrl(id: number, operation: "compact" | "retry", clientRunId: string) {
  return `/api/v1/sessions/${id}/memory/${operation}?client_run_id=${encodeURIComponent(clientRunId)}`
}

export function undoSessionMemoryChange(sessionId: number, changeId: number) {
  return api.post<SessionMemoryResponse>(`/sessions/${sessionId}/memory/changes/${changeId}/undo`, {})
}

export interface MessagePreflightPayload {
  content: string
  client_run_id?: string
  attachments?: number[]
  thinking_effort?: string
}

export interface MessagePreflightResponse {
  status: "ok" | "needs_compaction"
  needs_compaction: boolean
  retryable?: boolean
  message?: string
  tokens?: number
  threshold?: number
  last_task_run?: ModelTaskRun
}

export function preflightSessionMessage(id: number, data: MessagePreflightPayload) {
  return api.post<MessagePreflightResponse>(`/sessions/${id}/messages/preflight`, data)
}

export function deleteSession(id: number) {
  return api.delete<{ message: string }>(`/sessions/${id}`)
}

// 压缩走 SSE（复用 RunHub 续传通道），不再是同步 REST。
// 实际的 fetch + 流消费在 useSSE.startCompaction 中进行，这里仅给出端点。
export function compactSessionUrl(id: number, clientRunId: string, source: "auto" | "manual" = "manual", thinkingEffort?: string, preserveMessageId?: number) {
  const params = new URLSearchParams({ client_run_id: clientRunId, source })
  if (thinkingEffort) params.set("thinking_effort", thinkingEffort)
  if (preserveMessageId && preserveMessageId > 0) params.set("preserve_message_id", String(preserveMessageId))
  return `/api/v1/sessions/${id}/compact?${params.toString()}`
}

// 撤销最近一次压缩检查点：恢复被压消息、删除摘要。
export function undoCompaction(id: number) {
  return api.post<{ restored: number }>(`/sessions/${id}/compact/undo`, {})
}

// 主动中止进行中的 run（停止生成 / 停止压缩）。best-effort。
export function cancelRun(sessionId: number, runId: string) {
  return api.delete<{ canceled: boolean }>(`/sessions/${sessionId}/runs/${encodeURIComponent(runId)}`)
}

export function listSessionFolders() {
  return api.get<{ folders: SessionFolder[] }>("/session-folders")
}

export function createSessionFolder(name: string) {
  return api.post<SessionFolder>("/session-folders", { name })
}

export function updateSessionFolder(id: number, data: { name?: string; pinned?: boolean }) {
  return api.patch<SessionFolder>(`/session-folders/${id}`, data)
}

export function deleteSessionFolder(id: number) {
  return api.delete<{ message: string }>(`/session-folders/${id}`)
}
