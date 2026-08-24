import { api } from "./client"
import type { Message } from "@/types"

// 游标分页：beforeId 省略/为 0 取最新一页；传入则取 id 小于它的更早一页。
export function listMessages(sessionId: number, limit = 30, beforeId = 0) {
  const params = new URLSearchParams({ limit: String(limit) })
  if (beforeId > 0) params.set("before_id", String(beforeId))
  return api.get<{ messages: Message[]; has_more: boolean }>(
    `/sessions/${sessionId}/messages?${params.toString()}`
  )
}

export interface ConversationTurnIndex {
  id: number
  sequence: number
  user_message_id: number
  user_preview: string
  created_at: string
}

export interface ConversationTurnPage {
  turns: ConversationTurnIndex[]
  total: number
  has_more: boolean
  next_before_turn_id?: number | null
}

export interface MessageWindowResponse {
  messages: Message[]
  first_turn_id: number
  last_turn_id: number
  has_older: boolean
  has_newer: boolean
}

export interface SessionMessageCursor {
  latest_message_id: number
  session_updated_at: string
}

export function getSessionMessageCursor(sessionId: number, signal?: AbortSignal) {
  return api.get<SessionMessageCursor>(`/sessions/${sessionId}/message-cursor`, { timeoutMs: 5000, signal })
}

export function listConversationTurns(sessionId: number, limit = 500, beforeTurnId = 0) {
  const params = new URLSearchParams({ limit: String(limit) })
  if (beforeTurnId > 0) params.set("before_turn_id", String(beforeTurnId))
  return api.get<ConversationTurnPage>(`/sessions/${sessionId}/turns?${params.toString()}`)
}

export function listMessageWindow(
  sessionId: number,
  options: {
    latest?: true
    beforeTurnId?: number
    afterTurnId?: number
    aroundTurnId?: number
    turnLimit?: number
  } = {},
) {
  const params = new URLSearchParams({ turn_limit: String(options.turnLimit ?? 16) })
  if (options.beforeTurnId) params.set("before_turn_id", String(options.beforeTurnId))
  else if (options.afterTurnId) params.set("after_turn_id", String(options.afterTurnId))
  else if (options.aroundTurnId) params.set("around_turn_id", String(options.aroundTurnId))
  else params.set("latest", "true")
  return api.get<MessageWindowResponse>(`/sessions/${sessionId}/message-window?${params.toString()}`)
}

export interface SelectAnswerAttemptResponse {
  attempt_id: number
  selected: boolean
  selection_changed: boolean
  answer_selection_revision: number
  memory_reconciliation_started: boolean
}

export function selectAnswerAttempt(sessionId: number, attemptId: number) {
  return api.post<SelectAnswerAttemptResponse>(`/sessions/${sessionId}/answer-attempts/${attemptId}/select`, {})
}

export interface DeleteAnswerAttemptResponse {
  deleted_attempt_id: number
  selected_attempt_id: number
  selection_changed: boolean
  answer_selection_revision: number
  memory_reconciliation_started: boolean
}

export function deleteAnswerAttempt(sessionId: number, attemptId: number) {
  return api.delete<DeleteAnswerAttemptResponse>(`/sessions/${sessionId}/answer-attempts/${attemptId}`)
}
