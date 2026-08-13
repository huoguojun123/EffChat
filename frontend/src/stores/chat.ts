import { create } from "zustand"
import type { Session, SessionFolder, Message, MessageData, ToolCall, Model, StreamingSegment, StreamLifecycleState, LocalMessageState, ModelRetryTrace } from "@/types"
import * as sessionsApi from "@/api/sessions"
import type { SessionFolderScope } from "@/api/sessions"
import { ApiError } from "@/api/client"
import * as messagesApi from "@/api/messages"
import { cloneToolCall, messageRunId, normalizeMessages } from "@/lib/chatMessages"
import { createLocalMessageId } from "@/lib/localMessageId"

interface StreamingState {
  status: StreamLifecycleState
  content: string
  thinking: string
  toolCalls: ToolCall[]
  segments: StreamingSegment[]
  requestId: string | null
  error: string | null
  replayGap?: boolean
  retryTrace?: ModelRetryTrace | null
}

interface CompactionOwner {
  sessionId: number
  operationId: string
  notice: string
}

interface ChatState {
  sessions: Session[]
  sessionFolders: SessionFolder[]
  activeFolderId: SessionFolderScope
  activeSessionId: number | null
  activeSessionGeneration: number
  messageWindowGeneration: number
  messages: Message[]
  conversationTurns: messagesApi.ConversationTurnIndex[]
  totalConversationTurns: number
  streaming: StreamingState
  isLoadingSessions: boolean
  isLoadingMoreSessions: boolean
  hasMoreSessions: boolean
  sessionNextOffset: number
  isLoadingMessages: boolean
  messageLoadError: string | null
  hasMoreMessages: boolean
  hasNewerMessages: boolean
  isLoadingOlder: boolean
  isLoadingNewer: boolean
  firstLoadedTurnId: number | null
  lastLoadedTurnId: number | null
  compactionOwners: Record<number, CompactionOwner>
  sessionCreateReadiness: sessionsApi.SessionCreateReadiness | null
  isLoadingSessionCreateReadiness: boolean
  sessionCreateReadinessError: string | null
  isCreatingSession: boolean
  sessionCreateError: string | null

  loadSessionCreateReadiness: (force?: boolean) => Promise<void>
  loadSessionFolders: () => Promise<void>
  loadSessions: (options?: { folderId?: SessionFolderScope; reset?: boolean }) => Promise<void>
  loadMoreSessions: () => Promise<void>
  setActiveFolder: (folderId: SessionFolderScope) => void
  createSessionFolder: (name: string) => Promise<SessionFolder>
  renameSessionFolder: (id: number, name: string) => Promise<void>
  setSessionFolderPinned: (id: number, pinned: boolean) => Promise<void>
  deleteSessionFolder: (id: number) => Promise<void>
  setActiveSession: (id: number | null) => void
  createSession: (modelId?: string, provider?: Model["provider"], systemPrompt?: string) => Promise<Session>
  deleteSession: (id: number) => Promise<void>
  renameSession: (id: number, title: string) => Promise<void>
  moveSessionToFolder: (id: number, folderId: number | null) => Promise<void>
  setSessionPinned: (id: number, pinned: boolean) => Promise<void>
  updateSessionLocal: (id: number, patch: Partial<Session>) => void
  loadMessages: (sessionId: number) => Promise<boolean>
  loadOlderMessages: () => Promise<number>
  loadNewerMessages: () => Promise<number>
  loadMessageWindowAround: (turnId: number) => Promise<boolean>
  trimLoadedMessageWindow: (limit: number, keep: "start" | "end") => void
  beginCompaction: (sessionId: number, operationId: string, notice?: string) => void
  finishCompaction: (sessionId: number, operationId: string) => void
  setMessages: (messages: Message[]) => void
  syncMessages: (messages: Message[], expectedWindowGeneration?: number) => boolean
  addMessage: (msg: Message) => void
  updateMessage: (id: number, patch: Partial<Message>) => void
  updateMessagesByRequest: (requestId: string, patch: Partial<Message>) => void
  beginEditRetry: (messageId: number, content: string, requestId: string) => boolean
  confirmUserMessageForRequest: (requestId: string, messageId: number) => void
  removeLocalMessagesByRequest: (requestId: string) => void
  trimMessagesForRetry: (assistantMessageId: number) => void
  updateStreaming: (partial: Partial<StreamingState>) => void
  resetStreaming: () => void
  appendStreamContent: (delta: string) => void
  appendStreamThinking: (delta: string) => void
  rollbackStreamAttempt: (contentRunes: number, thinkingRunes: number) => void
  addStreamToolCall: (tc: ToolCall) => void
  updateStreamToolCall: (id: string, update: Partial<ToolCall>) => void
  commitStreamingMessage: (messageData: MessageData) => Message | null
  resetAccountState: () => void
}

const emptyStreaming: StreamingState = {
  status: "idle",
  content: "",
  thinking: "",
  toolCalls: [],
  segments: [],
  requestId: null,
  error: null,
  replayGap: false,
  retryTrace: null,
}

let latestMessagesRequest = 0
let latestPaginationRequest = 0
let latestSessionsRequest = 0
let latestSessionFoldersRequest = 0
let latestSessionCreateReadinessRequest = 0
let latestSessionPinOperation = 0
let latestFolderPinOperation = 0
const sessionPinOperations = new Map<number, number>()
const folderPinOperations = new Map<number, number>()
const pendingSessionPins = new Set<number>()
const pendingFolderPins = new Set<number>()
const sessionPinQueues = new Map<number, Promise<void>>()
const folderPinQueues = new Map<number, Promise<void>>()
const SESSION_PAGE_SIZE = 100

export const useChatStore = create<ChatState>()((set, get) => ({
  sessions: [],
  sessionFolders: [],
  activeFolderId: "all",
  activeSessionId: null,
  activeSessionGeneration: 0,
  messageWindowGeneration: 0,
  messages: [],
  conversationTurns: [],
  totalConversationTurns: 0,
  streaming: { ...emptyStreaming },
  isLoadingSessions: false,
  isLoadingMoreSessions: false,
  hasMoreSessions: false,
  sessionNextOffset: 0,
  isLoadingMessages: false,
  messageLoadError: null,
  hasMoreMessages: false,
  hasNewerMessages: false,
  isLoadingOlder: false,
  isLoadingNewer: false,
  firstLoadedTurnId: null,
  lastLoadedTurnId: null,
  compactionOwners: {},
  sessionCreateReadiness: null,
  isLoadingSessionCreateReadiness: true,
  sessionCreateReadinessError: null,
  isCreatingSession: false,
  sessionCreateError: null,

  loadSessionCreateReadiness: async (force = false) => {
    const current = get()
    if (!force && (current.sessionCreateReadiness || current.isLoadingSessionCreateReadiness && latestSessionCreateReadinessRequest > 0)) return
    const requestId = ++latestSessionCreateReadinessRequest
    set({ isLoadingSessionCreateReadiness: true, sessionCreateReadinessError: null })
    try {
      const readiness = await sessionsApi.getSessionCreateReadiness()
      if (requestId !== latestSessionCreateReadinessRequest) return
      set({ sessionCreateReadiness: readiness, sessionCreateError: null })
    } catch (err) {
      if (requestId !== latestSessionCreateReadinessRequest) return
      set({ sessionCreateReadiness: null, sessionCreateReadinessError: sessionCreationErrorMessage(err, "无法检查聊天配置，请重试") })
    } finally {
      if (requestId === latestSessionCreateReadinessRequest) set({ isLoadingSessionCreateReadiness: false })
    }
  },

  loadSessionFolders: async () => {
    const requestId = ++latestSessionFoldersRequest
    const pinOperation = latestFolderPinOperation
    const res = await sessionsApi.listSessionFolders()
    if (requestId !== latestSessionFoldersRequest) return
    set((state) => ({
      sessionFolders: sortFolders(preserveNewerPins(
        res.folders || [],
        state.sessionFolders,
        pinOperation,
        folderPinOperations,
        pendingFolderPins,
      )),
    }))
  },

  loadSessions: async (options = {}) => {
    const folderId = options.folderId ?? get().activeFolderId
    const reset = options.reset ?? true
    const requestId = ++latestSessionsRequest
    const pinOperation = latestSessionPinOperation
    set({
      activeFolderId: folderId,
      isLoadingSessions: true,
      isLoadingMoreSessions: false,
      hasMoreSessions: reset ? false : get().hasMoreSessions,
      sessionNextOffset: reset ? 0 : get().sessionNextOffset,
    })
    try {
      const res = await sessionsApi.listSessions({ limit: SESSION_PAGE_SIZE, offset: 0, folderId })
      if (get().activeFolderId !== folderId || requestId !== latestSessionsRequest) return
      set((state) => {
        const nextSessions = preserveNewerPins(
          res.sessions || [],
          state.sessions,
          pinOperation,
          sessionPinOperations,
          pendingSessionPins,
        )
        const activeSession = state.sessions.find((session) => session.id === state.activeSessionId)
        return {
          sessions: activeSession && !nextSessions.some((session) => session.id === activeSession.id)
            ? sortSessions([...nextSessions, activeSession])
            : sortSessions(nextSessions),
          hasMoreSessions: !!res.has_more,
          sessionNextOffset: res.next_offset || 0,
        }
      })
    } finally {
      if (get().activeFolderId === folderId && requestId === latestSessionsRequest) {
        set({ isLoadingSessions: false })
      }
    }
  },

  loadMoreSessions: async () => {
    const { activeFolderId, hasMoreSessions, isLoadingMoreSessions, isLoadingSessions, sessionNextOffset } = get()
    if (!hasMoreSessions || isLoadingMoreSessions || isLoadingSessions) return
    const requestId = ++latestSessionsRequest
    set({ isLoadingMoreSessions: true })
    try {
      const res = await sessionsApi.listSessions({
        limit: SESSION_PAGE_SIZE,
        offset: sessionNextOffset,
        folderId: activeFolderId,
      })
      if (get().activeFolderId !== activeFolderId || requestId !== latestSessionsRequest) return
      const existingIds = new Set(get().sessions.map((session) => session.id))
      const fresh = (res.sessions || []).filter((session) => !existingIds.has(session.id))
      set((s) => ({
        sessions: [...s.sessions, ...fresh],
        hasMoreSessions: !!res.has_more,
        sessionNextOffset: res.next_offset || s.sessionNextOffset,
      }))
    } finally {
      if (get().activeFolderId === activeFolderId && requestId === latestSessionsRequest) {
        set({ isLoadingMoreSessions: false })
      }
    }
  },

  setActiveFolder: (folderId: SessionFolderScope) => {
    if (get().activeFolderId === folderId && get().sessions.length > 0) return
    get().loadSessions({ folderId, reset: true })
  },

  createSessionFolder: async (name: string) => {
    const folder = await sessionsApi.createSessionFolder(name)
    set((s) => ({ sessionFolders: sortFolders([...s.sessionFolders, folder]) }))
    return folder
  },

  renameSessionFolder: async (id: number, name: string) => {
    const folder = await sessionsApi.updateSessionFolder(id, { name })
    set((s) => ({
      sessionFolders: sortFolders(s.sessionFolders.map((item) => (item.id === id ? folder : item))),
    }))
  },

  setSessionFolderPinned: async (id: number, pinned: boolean) => {
    const operationId = ++latestFolderPinOperation
    const previousPinnedAt = get().sessionFolders.find((folder) => folder.id === id)?.pinned_at ?? null
    const pinnedAt = pinned ? new Date().toISOString() : null
    folderPinOperations.set(id, operationId)
    pendingFolderPins.add(id)
    set((s) => ({ sessionFolders: sortFolders(s.sessionFolders.map((folder) => folder.id === id ? { ...folder, pinned_at: pinnedAt } : folder)) }))
    const previousOperation = folderPinQueues.get(id) ?? Promise.resolve()
    const operation = previousOperation.catch(() => undefined).then(async () => {
      if (folderPinOperations.get(id) !== operationId) return
      try {
        const folder = await sessionsApi.updateSessionFolder(id, { pinned })
        if (folderPinOperations.get(id) !== operationId) return
        set((s) => ({
          sessionFolders: sortFolders(s.sessionFolders.map((item) => (
            item.id === id ? { ...item, pinned_at: folder.pinned_at ?? null } : item
          ))),
        }))
      } catch (err) {
        if (folderPinOperations.get(id) !== operationId) return
        set((s) => ({
          sessionFolders: sortFolders(s.sessionFolders.map((item) => (
            item.id === id && (item.pinned_at ?? null) === pinnedAt
              ? { ...item, pinned_at: previousPinnedAt }
              : item
          ))),
        }))
        throw err
      } finally {
        if (folderPinOperations.get(id) === operationId) pendingFolderPins.delete(id)
      }
    })
    // Same-object requests must reach PostgreSQL in user-intent order; otherwise
    // an older PATCH can finish last and become durable even if its UI response is ignored.
    folderPinQueues.set(id, operation)
    try {
      await operation
    } finally {
      if (folderPinQueues.get(id) === operation) folderPinQueues.delete(id)
    }
  },

  deleteSessionFolder: async (id: number) => {
    await sessionsApi.deleteSessionFolder(id)
    const shouldLeaveFolder = get().activeFolderId === id
    set((s) => ({
      sessionFolders: s.sessionFolders.filter((folder) => folder.id !== id),
      sessions: s.sessions.map((session) => (
        session.folder_id === id ? { ...session, folder_id: null } : session
      )),
    }))
    if (shouldLeaveFolder) get().loadSessions({ folderId: "all", reset: true })
  },

  setActiveSession: (id: number | null) => {
    latestMessagesRequest += 1
    latestPaginationRequest += 1
    if (id) localStorage.setItem("active_session_id", String(id))
    else localStorage.removeItem("active_session_id")
    set((s) => ({
      activeSessionId: id,
      activeSessionGeneration: s.activeSessionGeneration + 1,
      messageWindowGeneration: s.messageWindowGeneration + 1,
      messages: [],
      conversationTurns: [],
      totalConversationTurns: 0,
      streaming: { ...emptyStreaming },
      messageLoadError: null,
      hasMoreMessages: false,
      hasNewerMessages: false,
      isLoadingOlder: false,
      isLoadingNewer: false,
      firstLoadedTurnId: null,
      lastLoadedTurnId: null,
    }))
    if (id) get().loadMessages(id).catch(() => undefined)
  },

  createSession: async (modelId?: string, provider?: Model["provider"], systemPrompt?: string) => {
    if (get().isCreatingSession) throw new Error("正在创建对话，请稍候")
    if (!modelId && !get().sessionCreateReadiness?.ready) {
      const message = get().sessionCreateReadinessError || readinessMessage(get().sessionCreateReadiness)
      set({ sessionCreateError: message })
      throw new Error(message)
    }
    const activeFolderId = get().activeFolderId
    const folderId = typeof activeFolderId === "number" ? activeFolderId : undefined
    set({ isCreatingSession: true, sessionCreateError: null })
    let session: Session
    try {
      session = await sessionsApi.createSession({ model_id: modelId, provider, system_prompt: systemPrompt, folder_id: folderId })
    } catch (err) {
      const message = sessionCreationErrorMessage(err, "创建对话失败，请重试")
      const patch: Partial<ChatState> = { sessionCreateError: message }
      if (err instanceof ApiError && isDefaultReadinessCode(err.code)) {
        patch.sessionCreateReadiness = { ready: false, code: err.code, message, retryable: Boolean(err.retryable) }
      }
      set(patch)
      throw err
    } finally {
      set({ isCreatingSession: false })
    }
    latestMessagesRequest += 1
    latestPaginationRequest += 1
    localStorage.setItem("active_session_id", String(session.id))
    set((s) => ({
      sessions: sessionBelongsToScope(session, s.activeFolderId) ? [session, ...s.sessions] : s.sessions,
      activeSessionId: session.id,
      activeSessionGeneration: s.activeSessionGeneration + 1,
      messageWindowGeneration: s.messageWindowGeneration + 1,
      messages: [],
      conversationTurns: [],
      totalConversationTurns: 0,
      streaming: { ...emptyStreaming },
      isLoadingMessages: false,
      messageLoadError: null,
      hasMoreMessages: false,
      hasNewerMessages: false,
      isLoadingOlder: false,
      isLoadingNewer: false,
      firstLoadedTurnId: null,
      lastLoadedTurnId: null,
    }))
    return session
  },

  deleteSession: async (id: number) => {
    await sessionsApi.deleteSession(id)
    const deletingActive = get().activeSessionId === id
    if (deletingActive) {
      latestMessagesRequest += 1
      latestPaginationRequest += 1
      localStorage.removeItem("active_session_id")
    }
    set((s) => {
      const compactionOwners = { ...s.compactionOwners }
      delete compactionOwners[id]
      return {
        sessions: s.sessions.filter((x) => x.id !== id),
        activeSessionId: deletingActive ? null : s.activeSessionId,
        activeSessionGeneration: deletingActive ? s.activeSessionGeneration + 1 : s.activeSessionGeneration,
        messageWindowGeneration: deletingActive ? s.messageWindowGeneration + 1 : s.messageWindowGeneration,
        messages: deletingActive ? [] : s.messages,
        conversationTurns: deletingActive ? [] : s.conversationTurns,
        totalConversationTurns: deletingActive ? 0 : s.totalConversationTurns,
        messageLoadError: deletingActive ? null : s.messageLoadError,
        streaming: deletingActive ? { ...emptyStreaming } : s.streaming,
        isLoadingMessages: deletingActive ? false : s.isLoadingMessages,
        hasMoreMessages: deletingActive ? false : s.hasMoreMessages,
        hasNewerMessages: deletingActive ? false : s.hasNewerMessages,
        isLoadingOlder: deletingActive ? false : s.isLoadingOlder,
        isLoadingNewer: deletingActive ? false : s.isLoadingNewer,
        firstLoadedTurnId: deletingActive ? null : s.firstLoadedTurnId,
        lastLoadedTurnId: deletingActive ? null : s.lastLoadedTurnId,
        compactionOwners,
      }
    })
  },

  renameSession: async (id: number, title: string) => {
    await sessionsApi.updateSession(id, { title })
    set((s) => ({
      sessions: s.sessions.map((x) => (x.id === id ? { ...x, title } : x)),
    }))
  },

  moveSessionToFolder: async (id: number, folderId: number | null) => {
    await sessionsApi.updateSession(id, { folder_id: folderId })
    set((s) => {
      const updated = s.sessions.map((session) => (
        session.id === id ? { ...session, folder_id: folderId } : session
      ))
      return {
        sessions: updated.filter((session) => sessionBelongsToScope(session, s.activeFolderId)),
      }
    })
  },

  setSessionPinned: async (id: number, pinned: boolean) => {
    const operationId = ++latestSessionPinOperation
    const previousPinnedAt = get().sessions.find((session) => session.id === id)?.pinned_at ?? null
    const pinnedAt = pinned ? new Date().toISOString() : null
    sessionPinOperations.set(id, operationId)
    pendingSessionPins.add(id)
    set((s) => ({ sessions: sortSessions(s.sessions.map((session) => session.id === id ? { ...session, pinned_at: pinnedAt } : session)) }))
    const previousOperation = sessionPinQueues.get(id) ?? Promise.resolve()
    const operation = previousOperation.catch(() => undefined).then(async () => {
      if (sessionPinOperations.get(id) !== operationId) return
      try {
        const session = await sessionsApi.updateSession(id, { pinned })
        if (sessionPinOperations.get(id) !== operationId) return
        set((s) => ({
          sessions: sortSessions(s.sessions.map((item) => (
            item.id === id ? { ...item, pinned_at: session.pinned_at ?? null } : item
          ))),
        }))
      } catch (err) {
        if (sessionPinOperations.get(id) !== operationId) return
        set((s) => ({
          sessions: sortSessions(s.sessions.map((item) => (
            item.id === id && (item.pinned_at ?? null) === pinnedAt
              ? { ...item, pinned_at: previousPinnedAt }
              : item
          ))),
        }))
        throw err
      } finally {
        if (sessionPinOperations.get(id) === operationId) pendingSessionPins.delete(id)
      }
    })
    sessionPinQueues.set(id, operation)
    try {
      await operation
    } finally {
      if (sessionPinQueues.get(id) === operation) sessionPinQueues.delete(id)
    }
  },

  updateSessionLocal: (id: number, patch: Partial<Session>) =>
    set((s) => ({
      sessions: s.sessions.map((x) => (x.id === id ? { ...x, ...patch } : x)),
    })),

  loadMessages: async (sessionId: number) => {
    const requestId = ++latestMessagesRequest
    const windowGeneration = get().messageWindowGeneration + 1
    latestPaginationRequest += 1
    set({
      messageWindowGeneration: windowGeneration,
      isLoadingMessages: true,
      isLoadingOlder: false,
      isLoadingNewer: false,
      messageLoadError: null,
    })
    void loadConversationTurnIndex(sessionId, requestId, get, set).catch(() => undefined)
    try {
      const res = await messagesApi.listMessageWindow(sessionId, { latest: true, turnLimit: 16 })
      if (get().activeSessionId !== sessionId || requestId !== latestMessagesRequest || get().messageWindowGeneration !== windowGeneration) return false
      // Full/latest loads replace the durable window. Only optimistic local rows may
      // survive long enough to be reconciled against the incoming persisted batch.
      const localMessages = get().messages.filter((message) => message.is_local)
      set({
        messages: mergeSyncedMessages(localMessages, res.messages || []),
        hasMoreMessages: !!res.has_older,
        hasNewerMessages: !!res.has_newer,
        firstLoadedTurnId: res.first_turn_id || null,
        lastLoadedTurnId: res.last_turn_id || null,
        isLoadingOlder: false,
        isLoadingNewer: false,
        messageLoadError: null,
      })
      return true
    } catch (err) {
      const message = formatStoreError(err, "加载消息失败")
      if (get().activeSessionId === sessionId && requestId === latestMessagesRequest) {
        set({ messageLoadError: message })
      }
      throw new Error(message, { cause: err })
    } finally {
      if (get().activeSessionId === sessionId && requestId === latestMessagesRequest) {
        set({ isLoadingMessages: false })
      }
    }
  },

  // 向上回溯：以当前最旧消息 id 作游标拉取更早一页，prepend 到头部。
  // 返回本次实际新增的消息条数，供视图做滚动锚定。并发/到头时直接返回 0。
  loadOlderMessages: async () => {
    const { activeSessionId, hasMoreMessages, isLoadingOlder, isLoadingNewer, firstLoadedTurnId, messageWindowGeneration } = get()
    if (!activeSessionId || !hasMoreMessages || isLoadingOlder || isLoadingNewer) return 0
    if (!firstLoadedTurnId) return 0

    const requestId = ++latestPaginationRequest
    const sessionId = activeSessionId
    const cursor = firstLoadedTurnId
    set({ isLoadingOlder: true })
    try {
      const res = await messagesApi.listMessageWindow(sessionId, { beforeTurnId: cursor, turnLimit: 12 })
      const current = get()
      if (
        current.activeSessionId !== sessionId
        || current.messageWindowGeneration !== messageWindowGeneration
        || requestId !== latestPaginationRequest
        || current.firstLoadedTurnId !== cursor
      ) return 0
      const older = normalizeMessages(res.messages || [])
      const existingIds = new Set(get().messages.map((m) => m.id))
      const fresh = older.filter((m) => !existingIds.has(m.id))
      set((s) => {
        const messages = trimMessageTurns([...fresh, ...s.messages], 72, "start")
        const bounds = messageTurnBounds(messages)
        return {
          messages,
          hasMoreMessages: !!res.has_older,
          hasNewerMessages: s.hasNewerMessages || bounds.last !== s.lastLoadedTurnId,
          firstLoadedTurnId: bounds.first,
          lastLoadedTurnId: bounds.last,
          isLoadingOlder: false,
        }
      })
      return fresh.length
    } catch {
      return 0
    } finally {
      if (
        get().activeSessionId === sessionId
        && get().messageWindowGeneration === messageWindowGeneration
        && requestId === latestPaginationRequest
      ) {
        set({ isLoadingOlder: false })
      }
    }
  },

  loadNewerMessages: async () => {
    const { activeSessionId, hasNewerMessages, isLoadingOlder, isLoadingNewer, lastLoadedTurnId, messageWindowGeneration } = get()
    if (!activeSessionId || !hasNewerMessages || isLoadingOlder || isLoadingNewer || !lastLoadedTurnId) return 0
    const requestId = ++latestPaginationRequest
    const sessionId = activeSessionId
    const cursor = lastLoadedTurnId
    set({ isLoadingNewer: true })
    try {
      const res = await messagesApi.listMessageWindow(sessionId, { afterTurnId: cursor, turnLimit: 12 })
      const current = get()
      if (
        current.activeSessionId !== sessionId
        || current.messageWindowGeneration !== messageWindowGeneration
        || requestId !== latestPaginationRequest
        || current.lastLoadedTurnId !== cursor
      ) return 0
      const existingIds = new Set(get().messages.map((message) => message.id))
      const fresh = normalizeMessages(res.messages || []).filter((message) => !existingIds.has(message.id))
      set((state) => {
        const messages = trimMessageTurns([...state.messages, ...fresh], 72, "end")
        const bounds = messageTurnBounds(messages)
        return {
          messages,
          hasMoreMessages: state.hasMoreMessages || bounds.first !== state.firstLoadedTurnId,
          hasNewerMessages: !!res.has_newer,
          firstLoadedTurnId: bounds.first,
          lastLoadedTurnId: bounds.last,
          isLoadingNewer: false,
        }
      })
      return fresh.length
    } catch {
      return 0
    } finally {
      if (
        get().activeSessionId === sessionId
        && get().messageWindowGeneration === messageWindowGeneration
        && requestId === latestPaginationRequest
      ) set({ isLoadingNewer: false })
    }
  },

  loadMessageWindowAround: async (turnId: number) => {
    const sessionId = get().activeSessionId
    if (!sessionId || turnId <= 0) return false
    const requestId = ++latestMessagesRequest
    const windowGeneration = get().messageWindowGeneration + 1
    latestPaginationRequest += 1
    set({
      messageWindowGeneration: windowGeneration,
      isLoadingOlder: false,
      isLoadingNewer: false,
    })
    const res = await messagesApi.listMessageWindow(sessionId, { aroundTurnId: turnId, turnLimit: 16 })
    if (get().activeSessionId !== sessionId || requestId !== latestMessagesRequest || get().messageWindowGeneration !== windowGeneration) return false
    set({
      messages: normalizeMessages(res.messages || []),
      hasMoreMessages: !!res.has_older,
      hasNewerMessages: !!res.has_newer,
      firstLoadedTurnId: res.first_turn_id || null,
      lastLoadedTurnId: res.last_turn_id || null,
      messageLoadError: null,
    })
    return true
  },

  trimLoadedMessageWindow: (limit: number, keep: "start" | "end") => set((state) => {
    const messages = trimMessageTurns(state.messages, limit, keep)
    if (messages.length === state.messages.length) return state
    const bounds = messageTurnBounds(messages)
    return {
      messages,
      hasMoreMessages: state.hasMoreMessages || bounds.first !== state.firstLoadedTurnId,
      hasNewerMessages: state.hasNewerMessages || bounds.last !== state.lastLoadedTurnId,
      firstLoadedTurnId: bounds.first,
      lastLoadedTurnId: bounds.last,
    }
  }),

  setMessages: (messages: Message[]) => {
    latestMessagesRequest += 1
    latestPaginationRequest += 1
    set((state) => ({
      messages: normalizeMessages(messages),
      messageWindowGeneration: state.messageWindowGeneration + 1,
      isLoadingMessages: false,
      isLoadingOlder: false,
      isLoadingNewer: false,
      messageLoadError: null,
    }))
  },
  syncMessages: (messages: Message[], expectedWindowGeneration = get().messageWindowGeneration) => {
    let committed = false
    set((state) => {
      if (state.messageWindowGeneration !== expectedWindowGeneration || state.hasNewerMessages) return state
      const merged = mergeSyncedMessages(state.messages, messages)
      const bounds = messageTurnBounds(merged)
      committed = true
      return {
        messages: merged,
        firstLoadedTurnId: bounds.first,
        lastLoadedTurnId: bounds.last,
        messageLoadError: null,
      }
    })
    return committed
  },

  beginCompaction: (sessionId: number, operationId: string, notice = "") =>
    set((s) => ({
      compactionOwners: {
        ...s.compactionOwners,
        [sessionId]: { sessionId, operationId, notice },
      },
    })),

  finishCompaction: (sessionId: number, operationId: string) =>
    set((s) => {
      if (s.compactionOwners[sessionId]?.operationId !== operationId) return {}
      const compactionOwners = { ...s.compactionOwners }
      delete compactionOwners[sessionId]
      return { compactionOwners }
    }),

  addMessage: (msg: Message) => set((s) => ({ messages: [...s.messages, normalizeMessage(msg)] })),

  updateMessage: (id: number, patch: Partial<Message>) =>
    set((s) => ({
      messages: s.messages.map((msg) => (msg.id === id ? normalizeMessage({ ...msg, ...patch }) : msg)),
    })),

  updateMessagesByRequest: (requestId: string, patch: Partial<Message>) =>
    set((s) => ({
      messages: s.messages.map((msg) => (
        msg.local_request_id === requestId || messageRunId(msg) === requestId
          ? normalizeMessage({ ...msg, ...patch })
          : msg
      )),
    })),

  beginEditRetry: (messageId: number, content: string, requestId: string) => {
    const state = get()
    const targetIndex = state.messages.findIndex((message) => (
      message.id === messageId
      && !message.is_local
      && message.role === "user"
      && message.message_data.role === "user"
    ))
    if (targetIndex < 0) return false
    const source = state.messages[targetIndex]
    set({
      messages: [
        ...state.messages.slice(0, targetIndex),
        normalizeMessage({
          ...source,
          message_data: {
            ...source.message_data,
            content,
            metadata: {
              ...(source.message_data.metadata || {}),
              run_id: requestId,
            },
          },
          local_state: "pending",
          local_request_id: requestId,
          local_error: undefined,
          is_local: true,
        }),
      ],
    })
    return true
  },

  confirmUserMessageForRequest: (requestId: string, messageId: number) => {
    if (!requestId || messageId <= 0) return
    set((s) => {
      const pendingUser = s.messages.find((msg) => (
        msg.role === "user" && msg.is_local && msg.local_request_id === requestId
      ))
      const alreadyPresent = s.messages.some((msg) => msg.id === messageId && !msg.is_local)
      const messages = s.messages
          .filter((msg) => !(alreadyPresent && msg.role === "user" && msg.is_local && msg.local_request_id === requestId))
          .map((msg) => (
            msg.role === "user" && msg.is_local && msg.local_request_id === requestId
              ? normalizeMessage({ ...msg, id: messageId, is_local: false, local_state: "persisted", local_request_id: undefined, local_error: undefined })
              : msg
          ))
      const confirmed = messages.find((message) => message.id === messageId && message.role === "user")
      const alreadyIndexed = s.conversationTurns.some((turn) => turn.id === messageId)
      const preview = confirmed?.message_data.content.replace(/\s+/g, " ").trim().slice(0, 110) || "空消息"
      const replacesIndexedTurn = Boolean(
        pendingUser && s.conversationTurns.some((turn) => turn.id === pendingUser.id)
      )
      const conversationTurns = replacesIndexedTurn
        ? s.conversationTurns.map((turn) => (
            turn.id === pendingUser?.id
              ? { ...turn, id: messageId, user_message_id: messageId, user_preview: preview }
              : turn
          ))
        : confirmed && !alreadyIndexed
          ? [...s.conversationTurns, {
              id: messageId,
              sequence: s.totalConversationTurns + 1,
              user_message_id: messageId,
              user_preview: preview,
              created_at: confirmed.created_at,
            }]
          : s.conversationTurns
      return {
        messages,
        conversationTurns,
        totalConversationTurns: confirmed && !alreadyIndexed && !replacesIndexedTurn
          ? s.totalConversationTurns + 1
          : s.totalConversationTurns,
      }
    })
  },

  removeLocalMessagesByRequest: (requestId: string) =>
    set((s) => ({
      messages: s.messages.filter((msg) => !(msg.is_local && msg.local_request_id === requestId)),
    })),

  trimMessagesForRetry: (assistantMessageId: number) =>
    set((s) => {
      const targetIndex = s.messages.findIndex((msg) => msg.id === assistantMessageId)
      if (targetIndex < 0) return s

      for (let i = targetIndex - 1; i >= 0; i--) {
        if (s.messages[i].role === "user") {
          return { messages: s.messages.slice(0, i + 1) }
        }
      }

      return s
    }),

  updateStreaming: (partial: Partial<StreamingState>) =>
    set((s) => ({ streaming: { ...s.streaming, ...partial } })),

  resetStreaming: () => set({ streaming: { ...emptyStreaming } }),

  appendStreamContent: (delta: string) =>
    set((s) => ({
      streaming: {
        ...s.streaming,
        content: s.streaming.content + delta,
        segments: appendSegmentContent(s.streaming.segments, delta),
      },
    })),

  appendStreamThinking: (delta: string) =>
    set((s) => ({
      streaming: {
        ...s.streaming,
        thinking: s.streaming.thinking + delta,
        segments: appendSegmentThinking(s.streaming.segments, delta),
      },
    })),

  rollbackStreamAttempt: (contentRunes: number, thinkingRunes: number) =>
    set((s) => ({
      streaming: {
        ...s.streaming,
        content: trimTrailingCodePoints(s.streaming.content, contentRunes),
        thinking: trimTrailingCodePoints(s.streaming.thinking, thinkingRunes),
        segments: trimStreamingSegments(s.streaming.segments, contentRunes, thinkingRunes),
      },
    })),

  addStreamToolCall: (tc: ToolCall) =>
    set((s) => ({
      streaming: {
        ...s.streaming,
        toolCalls: appendToolCallTree(s.streaming.toolCalls, tc),
        segments: appendSegmentToolCall(s.streaming.segments, tc),
      },
    })),

  updateStreamToolCall: (id: string, update: Partial<ToolCall>) =>
    set((s) => ({
      streaming: {
        ...s.streaming,
        toolCalls: updateToolCallTree(s.streaming.toolCalls, id, update),
        segments: updateSegmentToolCall(s.streaming.segments, id, update),
      },
    })),

  commitStreamingMessage: (messageData: MessageData) => {
    const { activeSessionId } = get()
    if (!activeSessionId) return null
    const requestId = get().streaming.requestId || undefined
    if (requestId) {
      const existing = get().messages.find((msg) => (
        msg.role === "assistant" &&
        msg.is_local &&
        msg.local_request_id === requestId &&
        (msg.local_state === "finalizing" || msg.local_state === "syncing" || (
          msg.local_state === "failed_local" && msg.message_data.metadata?.unsaved === true
        ))
      ))
      if (existing) return existing
    }
    const streamingSegments = get().streaming.segments.length ? get().streaming.segments : messageData.segments
    const hasReasoning = Boolean(messageData.thinking?.trim()) || Boolean(streamingSegments?.some((segment) => segment.thinking?.trim()))
    const hasToolCalls = (messageData.tool_calls?.length ?? 0) > 0 || Boolean(streamingSegments?.some((segment) => segment.tool_calls?.length))
    const msg: Message = {
      id: createLocalMessageId(),
      session_id: activeSessionId,
      schema_version: "v2",
      message_data: {
        ...messageData,
        metadata: requestId
          ? { ...(messageData.metadata || {}), run_id: requestId }
          : messageData.metadata,
        segments: streamingSegments,
      },
      role: "assistant",
      has_tool_calls: hasToolCalls,
      has_reasoning: hasReasoning,
      created_at: new Date().toISOString(),
      local_state: "finalizing",
      is_local: true,
      local_request_id: requestId,
    }
    set((s) => ({ messages: [...s.messages, normalizeMessage(msg)], streaming: { ...emptyStreaming } }))
    return msg
  },

  resetAccountState: () => {
    latestMessagesRequest += 1
    latestPaginationRequest += 1
    latestSessionsRequest += 1
    latestSessionFoldersRequest += 1
    latestSessionCreateReadinessRequest += 1
    sessionPinOperations.clear()
    folderPinOperations.clear()
    pendingSessionPins.clear()
    pendingFolderPins.clear()
    sessionPinQueues.clear()
    folderPinQueues.clear()
    localStorage.removeItem("active_session_id")
    set({
      sessions: [],
      sessionFolders: [],
      activeFolderId: "all",
      activeSessionId: null,
      activeSessionGeneration: get().activeSessionGeneration + 1,
      messageWindowGeneration: get().messageWindowGeneration + 1,
      messages: [],
      conversationTurns: [],
      totalConversationTurns: 0,
      streaming: { ...emptyStreaming },
      isLoadingSessions: false,
      isLoadingMoreSessions: false,
      hasMoreSessions: false,
      sessionNextOffset: 0,
      isLoadingMessages: false,
      messageLoadError: null,
      hasMoreMessages: false,
      hasNewerMessages: false,
      isLoadingOlder: false,
      isLoadingNewer: false,
      firstLoadedTurnId: null,
      lastLoadedTurnId: null,
      compactionOwners: {},
      sessionCreateReadiness: null,
      isLoadingSessionCreateReadiness: true,
      sessionCreateReadinessError: null,
      isCreatingSession: false,
      sessionCreateError: null,
    })
  },
}))

function readinessMessage(readiness: sessionsApi.SessionCreateReadiness | null) {
  if (!readiness) return "正在检查聊天配置，请稍候"
  if (readiness.code === "default_model_not_configured") return "尚未配置默认模型"
  return readiness.message || "默认模型暂不可用"
}

function sessionCreationErrorMessage(err: unknown, fallback: string) {
  if (err instanceof ApiError) {
    if (err.code === "default_model_not_configured") return "尚未配置默认模型"
    return err.message || fallback
  }
  return err instanceof Error && err.message ? err.message : fallback
}

function isDefaultReadinessCode(code?: string) {
  return code === "default_model_not_configured" || code === "model_runtime_unavailable" || code?.startsWith("session_model_") || code?.startsWith("channel_")
}

async function loadConversationTurnIndex(
  sessionId: number,
  requestId: number,
  get: () => ChatState,
  set: (partial: Partial<ChatState> | ((state: ChatState) => Partial<ChatState>)) => void,
) {
  let beforeTurnId = 0
  let collected: messagesApi.ConversationTurnIndex[] = []
  let hasMore = true
  while (hasMore) {
    const page = await messagesApi.listConversationTurns(sessionId, 500, beforeTurnId)
    if (get().activeSessionId !== sessionId || requestId !== latestMessagesRequest) return
    collected = [...page.turns, ...collected]
    set({ conversationTurns: collected, totalConversationTurns: page.total })
    beforeTurnId = page.next_before_turn_id || 0
    hasMore = page.has_more && beforeTurnId > 0
    if (!hasMore) return
    await new Promise<void>((resolve) => window.setTimeout(resolve, 0))
  }
}

function trimMessageTurns(messages: Message[], limit: number, keep: "start" | "end") {
  const turnIndexes: number[] = []
  messages.forEach((message, index) => {
    if (message.role === "user" && message.message_data.role === "user" && message.message_data.metadata?.compaction_summary !== true) {
      turnIndexes.push(index)
    }
  })
  if (turnIndexes.length <= limit) return messages
  if (keep === "start") return messages.slice(0, turnIndexes[limit])
  return messages.slice(turnIndexes[turnIndexes.length - limit])
}

function messageTurnBounds(messages: Message[]) {
  const turns = messages.filter((message) => message.role === "user" && message.message_data.role === "user" && message.message_data.metadata?.compaction_summary !== true)
  return { first: turns[0]?.id || null, last: turns.at(-1)?.id || null }
}

function normalizeMessage(message: Message): Message {
  return {
    ...message,
    local_state: message.local_state || inferLocalState(message),
    is_local: message.is_local ?? message.local_state !== "persisted",
  }
}

function mergeSyncedMessages(current: Message[], incoming: Message[]): Message[] {
  const synced = normalizeMessages(incoming)
  const syncedIDs = new Set(synced.map((message) => message.id))
  const syncedRunIds = new Set(synced.map(messageRunId).filter(Boolean))
  const syncedAssistantRunIds = new Set(synced
    .filter((message) => message.role === "assistant" && message.message_data.role === "assistant")
    .map(messageRunId)
    .filter(Boolean))
  const localOnly = current.filter((msg) => {
    if (!msg.is_local) return false
    const runId = msg.local_request_id?.trim() || messageRunId(msg)
    if (msg.role === "assistant") {
      const preserveVisibleCopy = msg.message_data.metadata?.incomplete === true || msg.message_data.metadata?.unsaved === true
      if (preserveVisibleCopy) return !runId || !syncedAssistantRunIds.has(runId)
      return !runId || !syncedRunIds.has(runId)
    }
    if (runId && syncedRunIds.has(runId)) return false
    return true
  })
  const durableCurrent = current.filter((message) => {
    if (syncedIDs.has(message.id)) return false
    const runId = messageRunId(message)
    return !runId || !syncedRunIds.has(runId)
  })
  const durableByID = new Map<number, Message>()
  const oldestSynced = synced.reduce<Message | null>((oldest, message) => (
    oldest === null || compareMessageLogicalOrder(message, oldest) < 0 ? message : oldest
  ), null)
  if (oldestSynced !== null) {
    for (const message of durableCurrent) {
      if (!message.is_local && compareMessageLogicalOrder(message, oldestSynced) < 0) {
        durableByID.set(message.id, normalizeMessage(message))
      }
    }
  }
  for (const message of synced) durableByID.set(message.id, message)
  const durable = [...durableByID.values()].sort(compareMessageLogicalOrder)
  return [...durable, ...localOnly.map(normalizeMessage)]
}

function compareMessageLogicalOrder(left: Message, right: Message) {
  const leftPosition = messageLogicalPosition(left)
  const rightPosition = messageLogicalPosition(right)
  for (let index = 0; index < leftPosition.length; index += 1) {
    const difference = leftPosition[index] - rightPosition[index]
    if (difference !== 0) return difference
  }
  return 0
}

function messageLogicalPosition(message: Message): [number, number, number] {
  const metadata = message.message_data.metadata
  const anchor = metadata?.compaction_before_message_id
  if (metadata?.compaction_summary === true && typeof anchor === "number" && Number.isSafeInteger(anchor) && anchor > 0) {
    return [anchor, 0, message.id]
  }
  return [message.id, 1, message.id]
}

function inferLocalState(message: Message): LocalMessageState {
  return message.is_local ? "pending" : "persisted"
}

function appendSegmentContent(segments: StreamingSegment[], delta: string): StreamingSegment[] {
  const next = cloneSegments(segments)
  const last = next[next.length - 1]
  if (last?.type === "content" && !last.thinking) {
    last.content = (last.content || "") + delta
    return next
  }
  next.push({ type: "content", content: delta })
  return next
}

function appendSegmentThinking(segments: StreamingSegment[], delta: string): StreamingSegment[] {
  const next = cloneSegments(segments)
  const last = next[next.length - 1]
  if (last?.type === "content" && !last.content) {
    last.thinking = (last.thinking || "") + delta
    return next
  }
  next.push({ type: "content", thinking: delta })
  return next
}

function trimStreamingSegments(segments: StreamingSegment[], contentRunes: number, thinkingRunes: number): StreamingSegment[] {
  const next = cloneSegments(segments)
  let remainingContent = Math.max(0, contentRunes)
  let remainingThinking = Math.max(0, thinkingRunes)
  for (let i = next.length - 1; i >= 0 && (remainingContent > 0 || remainingThinking > 0); i--) {
    const segment = next[i]
    if (segment.type !== "content") continue
    if (remainingContent > 0 && segment.content) {
      const size = Array.from(segment.content).length
      const removed = Math.min(size, remainingContent)
      segment.content = trimTrailingCodePoints(segment.content, removed)
      remainingContent -= removed
    }
    if (remainingThinking > 0 && segment.thinking) {
      const size = Array.from(segment.thinking).length
      const removed = Math.min(size, remainingThinking)
      segment.thinking = trimTrailingCodePoints(segment.thinking, removed)
      remainingThinking -= removed
    }
  }
  return next.filter((segment) => segment.type !== "content" || Boolean(segment.content || segment.thinking))
}

function trimTrailingCodePoints(value: string, count: number) {
  if (count <= 0 || !value) return value
  const points = Array.from(value)
  return points.slice(0, Math.max(0, points.length - count)).join("")
}

function appendSegmentToolCall(segments: StreamingSegment[], toolCall: ToolCall): StreamingSegment[] {
  const next = cloneSegments(segments)
  const last = next[next.length - 1]
  // 连续的工具调用（中间没有正文/思考）并入同一个 tool segment，使流式阶段
  // 就把同一轮的多次调用（如双语各搜一次）合并展示，与历史渲染口径一致，
  // 不再等输出完成后才合并。出现 content/thinking 会另起 content segment 自然隔断。
  if (last?.type === "tool") {
    last.tool_calls = [...(last.tool_calls || []), cloneToolCall(toolCall)]
    return next
  }
  next.push({ type: "tool", tool_calls: [cloneToolCall(toolCall)] })
  return next
}

function updateSegmentToolCall(segments: StreamingSegment[], id: string, update: Partial<ToolCall>): StreamingSegment[] {
  return cloneSegments(segments).map((segment) => ({
    ...segment,
    tool_calls: segment.tool_calls ? updateToolCallTree(segment.tool_calls, id, update) : undefined,
  }))
}

function cloneSegments(segments: StreamingSegment[]): StreamingSegment[] {
  return segments.map((segment) => ({
    ...segment,
    tool_calls: segment.tool_calls?.map(cloneToolCall),
  }))
}

function updateToolCallTree(toolCalls: ToolCall[], id: string, update: Partial<ToolCall>): ToolCall[] {
  return toolCalls.map((tc) => {
    if (tc.id === id) {
      return { ...tc, ...update }
    }
    if (tc.children?.length) {
      return { ...tc, children: updateToolCallTree(tc.children, id, update) }
    }
    return tc
  })
}

function appendToolCallTree(toolCalls: ToolCall[], next: ToolCall): ToolCall[] {
  return [...toolCalls.map(cloneToolCall), cloneToolCall(next)]
}

function formatStoreError(err: unknown, fallback: string) {
  return err instanceof Error && err.message ? err.message : fallback
}

const folderNameCollator = new Intl.Collator(undefined, { numeric: true, sensitivity: "base" })

function preserveNewerPins<T extends { id: number; pinned_at?: string | null }>(
  incoming: T[],
  current: T[],
  requestPinOperation: number,
  operations: Map<number, number>,
  pending: Set<number>,
): T[] {
  const currentById = new Map(current.map((item) => [item.id, item]))
  return incoming.map((item) => {
    const operation = operations.get(item.id) ?? 0
    if (!pending.has(item.id) && operation <= requestPinOperation) return item
    const local = currentById.get(item.id)
    // A list response cannot know about a pin request that was pending or started
    // after it. Preserve only that field; all other server fields remain canonical.
    return local ? { ...item, pinned_at: local.pinned_at ?? null } : item
  })
}

function sortFolders(folders: SessionFolder[]): SessionFolder[] {
  return [...folders].sort((a, b) => Number(Boolean(b.pinned_at)) - Number(Boolean(a.pinned_at)) || (b.pinned_at || "").localeCompare(a.pinned_at || "") || folderNameCollator.compare(a.name, b.name) || a.id - b.id)
}

function sortSessions(sessions: Session[]): Session[] {
  return [...sessions].sort((a, b) => Number(Boolean(b.pinned_at)) - Number(Boolean(a.pinned_at)) || (b.pinned_at || "").localeCompare(a.pinned_at || "") || b.updated_at.localeCompare(a.updated_at) || b.id - a.id)
}

function sessionBelongsToScope(session: Session, folderId: SessionFolderScope): boolean {
  if (folderId === "all") return true
  if (folderId === "unfiled") return session.folder_id == null
  return session.folder_id === folderId
}
