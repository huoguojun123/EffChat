import { beforeEach, describe, expect, it, vi } from "vitest"
import type { Message, Session, SessionFolder } from "@/types"
import { assistantErrorDetail, assistantErrorDiagnostic, editableTailUserMessageId, isErrorAssistant, normalizeMessages } from "@/lib/chatMessages"
import { useChatStore } from "@/stores/chat"
import * as messagesApi from "@/api/messages"
import * as sessionsApi from "@/api/sessions"

vi.mock("@/api/messages", () => ({
  listMessages: vi.fn(),
  listConversationTurns: vi.fn(),
  listMessageWindow: vi.fn(),
}))

vi.mock("@/api/sessions", () => ({
  listSessions: vi.fn(),
  listSessionFolders: vi.fn(),
  createSessionFolder: vi.fn(),
  updateSessionFolder: vi.fn(),
  deleteSessionFolder: vi.fn(),
  getSessionCreateReadiness: vi.fn(),
  createSession: vi.fn(),
  updateSession: vi.fn(),
  deleteSession: vi.fn(),
}))

const localStorageData = new Map<string, string>()
vi.stubGlobal("localStorage", {
  getItem: vi.fn((key: string) => localStorageData.get(key) ?? null),
  setItem: vi.fn((key: string, value: string) => {
    localStorageData.set(key, String(value))
  }),
  removeItem: vi.fn((key: string) => {
    localStorageData.delete(key)
  }),
  clear: vi.fn(() => {
    localStorageData.clear()
  }),
})

const listMessagesMock = vi.mocked(messagesApi.listMessages)
const listConversationTurnsMock = vi.mocked(messagesApi.listConversationTurns)
const listMessageWindowMock = vi.mocked(messagesApi.listMessageWindow)
const listSessionsMock = vi.mocked(sessionsApi.listSessions)
const listSessionFoldersMock = vi.mocked(sessionsApi.listSessionFolders)
const updateSessionFolderMock = vi.mocked(sessionsApi.updateSessionFolder)
const getSessionCreateReadinessMock = vi.mocked(sessionsApi.getSessionCreateReadiness)
const createSessionMock = vi.mocked(sessionsApi.createSession)
const updateSessionMock = vi.mocked(sessionsApi.updateSession)
const deleteSessionMock = vi.mocked(sessionsApi.deleteSession)

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
  listConversationTurnsMock.mockResolvedValue({ turns: [], total: 0, has_more: false, next_before_turn_id: null })
  getSessionCreateReadinessMock.mockResolvedValue({ ready: true, retryable: false })
  useChatStore.setState({
    sessions: [],
    sessionFolders: [],
    activeFolderId: "all",
    activeSessionId: null,
    activeSessionGeneration: 0,
    messageWindowGeneration: 0,
    messages: [],
    conversationTurns: [],
    totalConversationTurns: 0,
    streaming: {
      status: "idle",
      content: "",
      thinking: "",
      toolCalls: [],
      segments: [],
      requestId: null,
      error: null,
    },
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
    sessionCreateReadiness: { ready: true, retryable: false },
    isLoadingSessionCreateReadiness: false,
    sessionCreateReadinessError: null,
    isCreatingSession: false,
    sessionCreateError: null,
  })
})

function assistant(id: number, content: string, extra: Partial<Message> = {}): Message {
  return {
    id,
    session_id: 1,
    schema_version: "v2",
    role: "assistant",
    has_tool_calls: false,
    has_reasoning: false,
    created_at: `2026-06-15T13:2${id}:00Z`,
    message_data: {
      role: "assistant",
      content,
    },
    ...extra,
  }
}

function session(id: number, title: string, folderId: number | null = null): Session {
  return {
    id,
    user_id: 1,
    folder_id: folderId,
    title,
    title_generated: false,
    model_id: "gpt-4o-mini",
    provider: "openai",
    created_at: `2026-06-15T13:${String(id).padStart(2, "0")}:00Z`,
    updated_at: `2026-06-15T13:${String(id).padStart(2, "0")}:00Z`,
  }
}

function folder(id: number, name: string, pinnedAt: string | null = null): SessionFolder {
  return {
    id,
    user_id: 1,
    name,
    pinned_at: pinnedAt,
    created_at: `2026-06-15T12:${String(id).padStart(2, "0")}:00Z`,
    updated_at: `2026-06-15T12:${String(id).padStart(2, "0")}:00Z`,
  }
}

describe("sidebar pin mutation ownership", () => {
  it("does not let a failed session pin replace another session's successful pin", async () => {
    const first = deferred<Session>()
    const second = deferred<Session>()
    updateSessionMock.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)
    useChatStore.setState({ sessions: [session(1, "first"), session(2, "second")] })

    const failed = useChatStore.getState().setSessionPinned(1, true)
    const failedResult = expect(failed).rejects.toThrow("pin failed")
    const succeeded = useChatStore.getState().setSessionPinned(2, true)
    second.resolve({ ...session(2, "second"), pinned_at: "2026-08-06T01:00:00Z" })
    await succeeded
    first.reject(new Error("pin failed"))
    await failedResult

    expect(useChatStore.getState().sessions.find((item) => item.id === 2)?.pinned_at).toBeTruthy()
  })

  it("does not let a failed folder pin replace another folder's canonical success", async () => {
    const first = deferred<SessionFolder>()
    const second = deferred<SessionFolder>()
    updateSessionFolderMock.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)
    useChatStore.setState({ sessionFolders: [folder(1, "first"), folder(2, "second")] })

    const succeeded = useChatStore.getState().setSessionFolderPinned(1, true)
    const failed = useChatStore.getState().setSessionFolderPinned(2, true)
    const failedResult = expect(failed).rejects.toThrow("pin failed")
    first.resolve(folder(1, "server first", "2026-08-06T01:00:00Z"))
    await succeeded
    second.reject(new Error("pin failed"))
    await failedResult

    expect(useChatStore.getState().sessionFolders.find((item) => item.id === 1)?.pinned_at).toBe("2026-08-06T01:00:00Z")
  })

  it("ignores an older folder pin success after a newer unpin succeeds", async () => {
    const older = deferred<SessionFolder>()
    const newer = deferred<SessionFolder>()
    updateSessionFolderMock.mockReturnValueOnce(older.promise).mockReturnValueOnce(newer.promise)
    useChatStore.setState({ sessionFolders: [folder(1, "folder")] })

    const pin = useChatStore.getState().setSessionFolderPinned(1, true)
    const unpin = useChatStore.getState().setSessionFolderPinned(1, false)
    newer.resolve(folder(1, "folder", null))
    await unpin
    older.resolve(folder(1, "folder", "2026-08-06T01:00:00Z"))
    await pin

    expect(useChatStore.getState().sessionFolders[0]?.pinned_at).toBeNull()
  })

  it("ignores an older session pin failure after a newer intent and local update", async () => {
    const older = deferred<Session>()
    const newer = deferred<Session>()
    updateSessionMock.mockReturnValueOnce(older.promise).mockReturnValueOnce(newer.promise)
    useChatStore.setState({ sessions: [session(1, "original")] })

    const pin = useChatStore.getState().setSessionPinned(1, true)
    const pinResult = expect(pin).rejects.toThrow("pin failed")
    const unpin = useChatStore.getState().setSessionPinned(1, false)
    newer.resolve({ ...session(1, "original"), pinned_at: null })
    await unpin
    useChatStore.getState().updateSessionLocal(1, { title: "new title" })
    older.reject(new Error("pin failed"))
    await pinResult

    expect(useChatStore.getState().sessions[0]).toMatchObject({ title: "new title", pinned_at: null })
  })

  it("preserves an optimistic session pin while a stale list reload completes", async () => {
    const pin = deferred<Session>()
    const reload = deferred<{ sessions: Session[]; has_more: boolean; next_offset: number }>()
    updateSessionMock.mockReturnValue(pin.promise)
    listSessionsMock.mockReturnValue(reload.promise)
    useChatStore.setState({ sessions: [session(1, "session")] })

    const pendingPin = useChatStore.getState().setSessionPinned(1, true)
    const pendingReload = useChatStore.getState().loadSessions()
    reload.resolve({ sessions: [session(1, "session")], has_more: false, next_offset: 0 })
    await pendingReload

    expect(useChatStore.getState().sessions[0]?.pinned_at).toBeTruthy()
    pin.resolve({ ...session(1, "session"), pinned_at: "2026-08-06T01:00:00Z" })
    await pendingPin
  })

  it("does not let a list response started before a folder pin overwrite its success", async () => {
    const reload = deferred<{ folders: SessionFolder[] }>()
    listSessionFoldersMock.mockReturnValue(reload.promise)
    updateSessionFolderMock.mockResolvedValue(folder(1, "folder", "2026-08-06T01:00:00Z"))
    useChatStore.setState({ sessionFolders: [folder(1, "folder")] })

    const pendingReload = useChatStore.getState().loadSessionFolders()
    await useChatStore.getState().setSessionFolderPinned(1, true)
    reload.resolve({ folders: [folder(1, "folder")] })
    await pendingReload

    expect(useChatStore.getState().sessionFolders[0]?.pinned_at).toBe("2026-08-06T01:00:00Z")
  })

  it("does not restore sessions when an old pin fails after account reset", async () => {
    const stale = deferred<Session>()
    updateSessionMock.mockReturnValue(stale.promise)
    useChatStore.setState({ sessions: [session(1, "old account")] })

    const pending = useChatStore.getState().setSessionPinned(1, true)
    const pendingResult = expect(pending).rejects.toThrow("pin failed")
    useChatStore.getState().resetAccountState()
    stale.reject(new Error("pin failed"))
    await pendingResult

    expect(useChatStore.getState().sessions).toEqual([])
  })

  it("does not restore folders when an old pin fails after account reset", async () => {
    const stale = deferred<SessionFolder>()
    updateSessionFolderMock.mockReturnValue(stale.promise)
    useChatStore.setState({ sessionFolders: [folder(1, "old account")] })

    const pending = useChatStore.getState().setSessionFolderPinned(1, true)
    const pendingResult = expect(pending).rejects.toThrow("pin failed")
    useChatStore.getState().resetAccountState()
    stale.reject(new Error("pin failed"))
    await pendingResult

    expect(useChatStore.getState().sessionFolders).toEqual([])
  })
})

describe("account-scoped requests", () => {
  it("ignores a session folder response from before account reset", async () => {
    const stale = deferred<{ folders: Array<{ id: number; user_id: number; name: string; pinned_at: null; created_at: string; updated_at: string }> }>()
    listSessionFoldersMock.mockReturnValue(stale.promise)

    const pending = useChatStore.getState().loadSessionFolders()
    useChatStore.getState().resetAccountState()
    stale.resolve({ folders: [{ id: 1, user_id: 1, name: "old account", pinned_at: null, created_at: "", updated_at: "" }] })
    await pending

    expect(useChatStore.getState().sessionFolders).toEqual([])
  })

  it("ignores session readiness returned after account reset", async () => {
    const stale = deferred<sessionsApi.SessionCreateReadiness>()
    getSessionCreateReadinessMock.mockReturnValue(stale.promise)

    const pending = useChatStore.getState().loadSessionCreateReadiness(true)
    useChatStore.getState().resetAccountState()
    stale.resolve({ ready: true, retryable: false })
    await pending

    expect(useChatStore.getState().sessionCreateReadiness).toBeNull()
  })
})

describe("session view transitions", () => {
  it("resets the previous stream and message load state when creating a session", async () => {
    const created = session(2, "new session")
    createSessionMock.mockResolvedValue(created)
    useChatStore.setState({
      activeSessionId: 1,
      messages: [assistant(1, "partial")],
      streaming: {
        status: "streaming",
        content: "partial",
        thinking: "working",
        toolCalls: [],
        segments: [],
        requestId: "old-run",
        error: null,
      },
      isLoadingMessages: true,
      messageLoadError: "old error",
      hasMoreMessages: true,
      isLoadingOlder: true,
    })

    await useChatStore.getState().createSession()

    expect(useChatStore.getState()).toMatchObject({
      activeSessionId: 2,
      messages: [],
      streaming: { status: "idle", requestId: null, content: "", thinking: "" },
      isLoadingMessages: false,
      messageLoadError: null,
      hasMoreMessages: false,
      isLoadingOlder: false,
    })
  })

  it("does not send an empty create request while the default model is not ready", async () => {
    useChatStore.setState({
      sessionCreateReadiness: { ready: false, retryable: false, code: "default_model_not_configured" },
    })

    await expect(useChatStore.getState().createSession()).rejects.toThrow("尚未配置默认模型")

    expect(createSessionMock).not.toHaveBeenCalled()
    expect(useChatStore.getState()).toMatchObject({ isCreatingSession: false, sessionCreateError: "尚未配置默认模型" })
  })

  it("shares one busy owner across duplicate create attempts", async () => {
    const pending = deferred<Session>()
    createSessionMock.mockReturnValue(pending.promise)

    const first = useChatStore.getState().createSession()
    await expect(useChatStore.getState().createSession()).rejects.toThrow("正在创建对话")
    expect(createSessionMock).toHaveBeenCalledTimes(1)

    pending.resolve(session(2, "new session"))
    await first
    expect(useChatStore.getState().isCreatingSession).toBe(false)
  })
})

function user(id: number, content: string, extra: Partial<Message> = {}): Message {
  return {
    id,
    session_id: 1,
    schema_version: "v2",
    role: "user",
    has_tool_calls: false,
    has_reasoning: false,
    created_at: `2026-06-15T13:1${id}:00Z`,
    message_data: {
      role: "user",
      content,
    },
    ...extra,
  }
}

describe("normalizeMessages", () => {
  it("不会把失败助手消息合并进正常回答", () => {
    const messages = normalizeMessages([
      user(1, "你好"),
      assistant(2, "正常回答"),
      user(3, "再找找"),
      assistant(4, "请求失败：Maximum call stack size exceeded", {
        message_data: {
          role: "assistant",
          content: "请求失败：Maximum call stack size exceeded",
          metadata: { ephemeral_error: true },
        },
      }),
      assistant(5, "后端最终真实回答"),
    ])

    expect(messages).toHaveLength(5)
    expect(messages[1].message_data.content).toBe("正常回答")
    expect(messages[3].message_data.content).toContain("请求失败")
    expect(messages[4].message_data.content).toBe("后端最终真实回答")
    expect(isErrorAssistant(messages[3])).toBe(true)
    expect(assistantErrorDetail(messages[3])).toBe("Maximum call stack size exceeded")
  })

  it("错误助手识别不再依赖中文内容前缀", () => {
    const msg = assistant(10, "请求失败：plain text without metadata")
    expect(isErrorAssistant(msg)).toBe(false)
    expect(assistantErrorDetail(msg)).toBe("plain text without metadata")
  })

  it("刷新后保留无旧前缀的安全模型错误", () => {
    const msg = assistant(11, "模型渠道鉴权或访问权限无效，请联系管理员检查配置", {
      message_data: {
        role: "assistant",
        content: "模型渠道鉴权或访问权限无效，请联系管理员检查配置",
        metadata: { ephemeral_error: true },
      },
    })
    expect(assistantErrorDetail(msg)).toBe("模型渠道鉴权或访问权限无效，请联系管理员检查配置")
  })

  it("刷新后保留安全的上游诊断摘要", () => {
    const msg = assistant(12, "上游模型额度不足，请在模型网关补充额度后重试", {
      message_data: {
        role: "assistant",
        content: "上游模型额度不足，请在模型网关补充额度后重试",
        metadata: {
          ephemeral_error: true,
          error_diagnostic: "HTTP 403 · 上游额度不足 · 请求 ID req-123456",
        },
      },
    })
    expect(assistantErrorDiagnostic(msg)).toBe("HTTP 403 · 上游额度不足 · 请求 ID req-123456")
  })

  it("会把连续正常助手消息重建为一个展示消息", () => {
    const messages = normalizeMessages([
      user(1, "查一下"),
      assistant(2, "先给你搜一下"),
      assistant(3, "这是最终结果"),
    ])

    expect(messages).toHaveLength(2)
    expect(messages[1].message_data.content).toContain("先给你搜一下")
    expect(messages[1].message_data.content).toContain("这是最终结果")
    expect(messages[1].message_data.segments).toHaveLength(2)
  })

  it("只合并同一个 run_id 的新助手分段", () => {
    const messages = normalizeMessages([
      user(1, "查一下"),
      assistant(2, "第一轮", {
        message_data: { role: "assistant", content: "第一轮", metadata: { run_id: "run-a" } },
      }),
      assistant(3, "第二轮", {
        message_data: { role: "assistant", content: "第二轮", metadata: { run_id: "run-b" } },
      }),
    ])

    expect(messages).toHaveLength(3)
    expect(messages[1].message_data.content).toBe("第一轮")
    expect(messages[2].message_data.content).toBe("第二轮")
  })

  it("会合并同一个 run_id 的 produced assistant 分段", () => {
    const messages = normalizeMessages([
      user(1, "查一下"),
      assistant(2, "工具调用前", {
        message_data: { role: "assistant", content: "工具调用前", metadata: { run_id: "run-a" } },
      }),
      assistant(3, "最终回答", {
        message_data: { role: "assistant", content: "最终回答", metadata: { run_id: "run-a" } },
      }),
    ])

    expect(messages).toHaveLength(2)
    expect(messages[1].message_data.content).toContain("工具调用前")
    expect(messages[1].message_data.content).toContain("最终回答")
    expect(messages[1].message_data.segments).toHaveLength(2)
  })

  it("保留终态回答上的答案切换导航", () => {
    const messages = normalizeMessages([
      user(1, "查一下"),
      assistant(2, "工具调用前", {
        answer_attempt_id: 41,
        message_data: { role: "assistant", content: "工具调用前", metadata: { run_id: "run-a" } },
      }),
      assistant(3, "最终回答", {
        answer_attempt_id: 41,
        answer_navigation: {
          attempt_id: 41,
          attempt_number: 2,
          attempt_count: 3,
          previous_attempt_id: 40,
          next_attempt_id: 42,
          can_switch: true,
        },
        message_data: { role: "assistant", content: "最终回答", metadata: { run_id: "run-a" } },
      }),
    ])

    expect(messages).toHaveLength(2)
    expect(messages[1].answer_navigation).toMatchObject({
      attempt_id: 41,
      attempt_number: 2,
      attempt_count: 3,
      previous_attempt_id: 40,
      next_attempt_id: 42,
      can_switch: true,
    })
  })

  it("归一化后服务端消息应为 persisted 状态", () => {
    const messages = normalizeMessages([
      user(1, "你好"),
      assistant(2, "你好"),
    ])

    expect(messages.every((msg) => msg.local_state === "persisted")).toBe(true)
    expect(messages.every((msg) => msg.is_local === false)).toBe(true)
  })

  it("会把服务端 reasoning_content 归一化为 thinking，避免思维链同步后消失", () => {
    const messages = normalizeMessages([
      user(1, "你好"),
      assistant(2, "答案", {
        has_reasoning: true,
        message_data: {
          role: "assistant",
          content: "答案",
          reasoning_content: "我先分析一下",
        },
      }),
    ])

    expect(messages[1].message_data.thinking).toBe("我先分析一下")
    expect(messages[1].has_reasoning).toBe(true)
  })
})

describe("resetAccountState", () => {
  it("clears active account conversations and streaming state", () => {
    localStorage.setItem("active_session_id", "7")
    useChatStore.setState({
      sessions: [session(7, "Private session")],
      sessionFolders: [{ id: 3, user_id: 1, name: "Private", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" }],
      activeSessionId: 7,
      messages: [user(1, "private message")],
      streaming: { status: "streaming", content: "private answer", thinking: "", toolCalls: [], segments: [], requestId: "run-1", error: null },
      compactionOwners: { 7: { sessionId: 7, operationId: "compact-1", notice: "" } },
    })

    useChatStore.getState().resetAccountState()

    const state = useChatStore.getState()
    expect(state.sessions).toEqual([])
    expect(state.sessionFolders).toEqual([])
    expect(state.activeSessionId).toBeNull()
    expect(state.messages).toEqual([])
    expect(state.streaming.status).toBe("idle")
    expect(state.compactionOwners).toEqual({})
    expect(localStorage.getItem("active_session_id")).toBeNull()
  })
})

describe("active session transitions", () => {
  it("clears a recovering stream before loading the newly selected session", async () => {
    listMessagesMock.mockResolvedValue({ messages: [], has_more: false })
    useChatStore.setState({
      activeSessionId: 11,
      streaming: {
        status: "recovering",
        content: "old session output",
        thinking: "old session reasoning",
        toolCalls: [],
        segments: [{ type: "content", content: "old session output" }],
        requestId: "run-old-session",
        error: "连接暂时中断，正在确认回答",
        replayGap: true,
      },
    })

    useChatStore.getState().setActiveSession(12)

    expect(useChatStore.getState().activeSessionId).toBe(12)
    expect(useChatStore.getState().streaming).toEqual({
      status: "idle",
      content: "",
      thinking: "",
      toolCalls: [],
      segments: [],
      requestId: null,
      error: null,
      replayGap: false,
      retryTrace: null,
    })
    await vi.waitFor(() => expect(useChatStore.getState().isLoadingMessages).toBe(false))
  })
})

describe("syncMessages", () => {
  it("确认已接受的用户消息时保留同 run 的未保存助手副本", () => {
    useChatStore.setState({
      messages: [
        user(999, "local pending", {
          is_local: true,
          local_state: "streaming",
          local_request_id: "run-partial",
        }),
        assistant(1000, "generated but unsaved", {
          is_local: true,
          local_state: "failed_local",
          local_request_id: "run-partial",
          message_data: { role: "assistant", content: "generated but unsaved", metadata: { unsaved: true } },
        }),
      ],
    })

    useChatStore.getState().confirmUserMessageForRequest("run-partial", 42)

    const messages = useChatStore.getState().messages
    expect(messages[0]).toMatchObject({ id: 42, is_local: false, local_state: "persisted" })
    expect(messages[0].local_request_id).toBeUndefined()
    expect(messages[1]).toMatchObject({ id: 1000, is_local: true, local_state: "failed_local", local_request_id: "run-partial" })
  })

  it("仅在同 run 的 assistant 已落库后才替换未保存助手副本", () => {
    useChatStore.setState({
      messages: [
        user(42, "persisted user", {
          is_local: false,
          local_state: "persisted",
          message_data: { role: "user", content: "persisted user", metadata: { run_id: "run-partial" } },
        }),
        assistant(1000, "generated but unsaved", {
          is_local: true,
          local_state: "failed_local",
          local_request_id: "run-partial",
          message_data: { role: "assistant", content: "generated but unsaved", metadata: { unsaved: true } },
        }),
      ],
    })

    useChatStore.getState().syncMessages([
      user(42, "persisted user", {
        message_data: { role: "user", content: "persisted user", metadata: { run_id: "run-partial" } },
      }),
    ])

    expect(useChatStore.getState().messages).toHaveLength(2)
    expect(useChatStore.getState().messages[1]).toMatchObject({ is_local: true, local_state: "failed_local" })

    useChatStore.getState().syncMessages([
      user(42, "persisted user", {
        message_data: { role: "user", content: "persisted user", metadata: { run_id: "run-partial" } },
      }),
      assistant(43, "durably saved partial", {
        message_data: { role: "assistant", content: "durably saved partial", metadata: { run_id: "run-partial", incomplete: true } },
      }),
    ])

    const messages = useChatStore.getState().messages
    expect(messages).toHaveLength(2)
    expect(messages.some((msg) => msg.is_local)).toBe(false)
    expect(messages[1].message_data.content).toBe("durably saved partial")
  })

  it("服务端暂时只有同 run 用户消息时保留对账中的 incomplete assistant", () => {
    useChatStore.setState({
      messages: [
        user(42, "persisted user", {
          is_local: false,
          local_state: "persisted",
          message_data: { role: "user", content: "persisted user", metadata: { run_id: "run-stop" } },
        }),
        assistant(1000, "visible partial", {
          is_local: true,
          local_state: "finalizing",
          local_request_id: "run-stop",
          message_data: { role: "assistant", content: "visible partial", metadata: { incomplete: true } },
        }),
      ],
    })

    useChatStore.getState().syncMessages([
      user(42, "persisted user", {
        message_data: { role: "user", content: "persisted user", metadata: { run_id: "run-stop" } },
      }),
    ])

    expect(useChatStore.getState().messages).toHaveLength(2)
    expect(useChatStore.getState().messages[1]).toMatchObject({
      is_local: true,
      local_state: "finalizing",
      message_data: { content: "visible partial", metadata: { incomplete: true } },
    })

    useChatStore.getState().syncMessages([
      user(42, "persisted user", {
        message_data: { role: "user", content: "persisted user", metadata: { run_id: "run-stop" } },
      }),
      assistant(43, "durably saved partial", {
        message_data: { role: "assistant", content: "durably saved partial", metadata: { run_id: "run-stop", incomplete: true } },
      }),
    ])

    const messages = useChatStore.getState().messages
    expect(messages).toHaveLength(2)
    expect(messages.some((message) => message.is_local)).toBe(false)
    expect(messages[1].message_data.content).toBe("durably saved partial")
  })

  it("同 run 的正式用户消息会清除普通本地错误卡", () => {
    useChatStore.setState({
      messages: [
        user(42, "persisted user", {
          is_local: false,
          local_state: "persisted",
          message_data: { role: "user", content: "persisted user", metadata: { run_id: "run-error" } },
        }),
        assistant(1000, "", {
          is_local: true,
          local_state: "failed_local",
          local_request_id: "run-error",
          message_data: { role: "assistant", content: "", metadata: { error: true, error_detail: "temporary failure" } },
        }),
      ],
    })

    useChatStore.getState().syncMessages([
      user(42, "persisted user", {
        message_data: { role: "user", content: "persisted user", metadata: { run_id: "run-error" } },
      }),
    ])

    expect(useChatStore.getState().messages).toHaveLength(1)
    expect(useChatStore.getState().messages[0].id).toBe(42)
  })

  it("服务端暂缺本地消息时保留 pending 用户消息", () => {
    useChatStore.setState({
      messages: [
        user(999, "local pending", {
          is_local: true,
          local_state: "pending",
          local_request_id: "run-local",
          created_at: "2026-06-15T13:59:00Z",
        }),
      ],
    })

    useChatStore.getState().syncMessages([user(1, "persisted old")])

    const messages = useChatStore.getState().messages
    expect(messages).toHaveLength(2)
    expect(messages[0].message_data.content).toBe("persisted old")
    expect(messages[1].message_data.content).toBe("local pending")
    expect(messages[1].local_state).toBe("pending")
    expect(messages[1].is_local).toBe(true)
  })

  it("服务端已有同 run_id 消息时丢弃本地副本", () => {
    useChatStore.setState({
      messages: [
        user(999, "local pending", {
          is_local: true,
          local_state: "pending",
          local_request_id: "run-42",
        }),
      ],
    })

    useChatStore.getState().syncMessages([
      user(10, "persisted user", {
        message_data: { role: "user", content: "persisted user", metadata: { run_id: "run-42" } },
      }),
      assistant(11, "persisted assistant", {
        message_data: { role: "assistant", content: "persisted assistant", metadata: { run_id: "run-42" } },
      }),
    ])

    const messages = useChatStore.getState().messages
    expect(messages).toHaveLength(2)
    expect(messages.some((msg) => msg.is_local)).toBe(false)
    expect(messages.map((msg) => msg.message_data.content)).toEqual(["persisted user", "persisted assistant"])
  })

  it("历史 assistant 到达时替换已提前标记为 persisted 的恢复副本", () => {
    useChatStore.setState({
      messages: [
        user(1, "persisted user", {
          is_local: false,
          local_state: "persisted",
          message_data: { role: "user", content: "persisted user", metadata: { run_id: "run-recovery" } },
        }),
        assistant(-1, "recovered local answer", {
          is_local: false,
          local_state: "persisted",
          message_data: { role: "assistant", content: "recovered local answer", metadata: { run_id: "run-recovery" } },
        }),
      ],
    })

    useChatStore.getState().syncMessages([
      user(1, "persisted user", {
        message_data: { role: "user", content: "persisted user", metadata: { run_id: "run-recovery" } },
      }),
      assistant(2, "durable answer", {
        message_data: { role: "assistant", content: "durable answer", metadata: { run_id: "run-recovery" } },
      }),
    ])

    const messages = useChatStore.getState().messages
    expect(messages).toHaveLength(2)
    expect(messages.map((message) => message.id)).toEqual([1, 2])
    expect(messages[1].message_data.content).toBe("durable answer")
  })

  it("对账最新窗口时保留已经向上加载的历史页", () => {
    useChatStore.setState({
      messages: [
        user(1, "older user", { is_local: false, local_state: "persisted" }),
        assistant(2, "older answer", { is_local: false, local_state: "persisted" }),
        user(9, "latest user", { is_local: false, local_state: "persisted" }),
      ],
    })

    useChatStore.getState().syncMessages([
      user(9, "latest user updated", { is_local: false, local_state: "persisted" }),
      assistant(10, "latest answer", { is_local: false, local_state: "persisted" }),
    ])

    expect(useChatStore.getState().messages.map((message) => message.id)).toEqual([1, 2, 9, 10])
    expect(useChatStore.getState().messages[2].message_data.content).toBe("latest user updated")
  })

  it("对账最新窗口时替换已被重试软删除的 durable 后缀", () => {
    useChatStore.setState({
      messages: [
        user(1, "older user", { is_local: false, local_state: "persisted" }),
        assistant(2, "older answer", { is_local: false, local_state: "persisted" }),
        user(9, "retry target", { is_local: false, local_state: "persisted" }),
        assistant(10, "stale first answer", { is_local: false, local_state: "persisted" }),
        user(11, "stale follow-up", { is_local: false, local_state: "persisted" }),
      ],
    })

    useChatStore.getState().syncMessages([
      user(9, "retry target", { is_local: false, local_state: "persisted" }),
      assistant(12, "replacement answer", { is_local: false, local_state: "persisted" }),
    ])

    expect(useChatStore.getState().messages.map((message) => message.id)).toEqual([1, 2, 9, 12])
    expect(useChatStore.getState().messages.some((message) => message.id === 10 || message.id === 11)).toBe(false)
  })

  it("对账最新窗口时保留并按锚点排列受保护压缩摘要", () => {
    const summary = user(100, "older context summary", {
      is_local: false,
      local_state: "persisted",
      message_data: {
        role: "user",
        content: "older context summary",
        metadata: {
          compaction_summary: true,
          compaction_before_message_id: 9,
        },
      },
    })
    useChatStore.setState({
      messages: [
        user(1, "older user", { is_local: false, local_state: "persisted" }),
        summary,
        user(9, "protected user", { is_local: false, local_state: "persisted" }),
        assistant(10, "stale answer", { is_local: false, local_state: "persisted" }),
      ],
    })

    useChatStore.getState().syncMessages([
      user(9, "protected user", { is_local: false, local_state: "persisted" }),
      assistant(12, "replacement answer", { is_local: false, local_state: "persisted" }),
    ])

    expect(useChatStore.getState().messages.map((message) => message.id)).toEqual([1, 100, 9, 12])
  })

  it("不会在权威最新窗口为空时保留已删除的 durable 消息", () => {
    useChatStore.setState({
      messages: [
        user(1, "deleted user", { is_local: false, local_state: "persisted" }),
        assistant(2, "deleted answer", { is_local: false, local_state: "persisted" }),
      ],
    })

    useChatStore.getState().syncMessages([])

    expect(useChatStore.getState().messages).toEqual([])
  })

  it("按 durable run ID 标记已经确认的用户消息", () => {
    useChatStore.setState({
      messages: [user(9, "persisted user", {
        is_local: false,
        local_state: "persisted",
        message_data: { role: "user", content: "persisted user", metadata: { run_id: "run-terminal-failed" } },
      })],
    })

    useChatStore.getState().updateMessagesByRequest("run-terminal-failed", {
      local_state: "failed_local",
      local_error: "本次回复未能完成",
    })

    expect(useChatStore.getState().messages[0]).toMatchObject({
      local_state: "failed_local",
      local_error: "本次回复未能完成",
    })
  })
})

describe("commitStreamingMessage", () => {
  it("同一个 requestId 不重复插入 finalizing assistant", () => {
    useChatStore.setState({
      activeSessionId: 1,
      messages: [],
      streaming: {
        status: "streaming",
        content: "hello",
        thinking: "",
        toolCalls: [],
        segments: [{ type: "content", content: "hello" }],
        requestId: "run-1",
        error: null,
      },
    })

    const first = useChatStore.getState().commitStreamingMessage({ role: "assistant", content: "hello" })
    useChatStore.setState((state) => ({
      streaming: { ...state.streaming, requestId: "run-1", segments: [{ type: "content", content: "hello" }] },
    }))
    const second = useChatStore.getState().commitStreamingMessage({ role: "assistant", content: "hello" })

    expect(first?.id).toBe(second?.id)
    expect(first?.message_data.metadata).toMatchObject({ run_id: "run-1" })
    expect(useChatStore.getState().messages.filter((msg) => msg.role === "assistant")).toHaveLength(1)
  })
})

describe("streaming segment ordering", () => {
  it("does not merge reasoning and answer content across event boundaries", () => {
    const store = useChatStore.getState()

    store.appendStreamThinking("先分析")
    store.appendStreamContent("正文开始")
    store.appendStreamThinking("补充判断")

    expect(useChatStore.getState().streaming.segments).toEqual([
      { type: "content", thinking: "先分析" },
      { type: "content", content: "正文开始" },
      { type: "content", thinking: "补充判断" },
    ])
  })
})

describe("session folder and pagination state", () => {
  it("默认加载 100 条会话并保存下一页游标", async () => {
    listSessionsMock.mockResolvedValue({
      sessions: [session(1, "first")],
      has_more: true,
      next_offset: 100,
    })

    await useChatStore.getState().loadSessions()

    expect(listSessionsMock).toHaveBeenCalledWith({ limit: 100, offset: 0, folderId: "all" })
    expect(useChatStore.getState().sessions).toHaveLength(1)
    expect(useChatStore.getState().hasMoreSessions).toBe(true)
    expect(useChatStore.getState().sessionNextOffset).toBe(100)
  })

  it("触底加载下一页并过滤重复会话", async () => {
    useChatStore.setState({
      sessions: [session(1, "first")],
      activeFolderId: "all",
      hasMoreSessions: true,
      sessionNextOffset: 100,
    })
    listSessionsMock.mockResolvedValue({
      sessions: [session(1, "first"), session(2, "second")],
      has_more: false,
      next_offset: 100,
    })

    await useChatStore.getState().loadMoreSessions()

    expect(listSessionsMock).toHaveBeenCalledWith({ limit: 100, offset: 100, folderId: "all" })
    expect(useChatStore.getState().sessions.map((item) => item.id)).toEqual([1, 2])
    expect(useChatStore.getState().hasMoreSessions).toBe(false)
  })

  it("切换文件夹时保留活动会话并按文件夹重新加载侧栏", async () => {
    const pending = deferred<{ sessions: Session[]; has_more: boolean; next_offset: number }>()
    listSessionsMock.mockReturnValue(pending.promise)
    useChatStore.setState({ sessions: [session(1, "old")], activeFolderId: "all", activeSessionId: 1 })

    const loading = useChatStore.getState().loadSessions({ folderId: 7, reset: true })

    expect(useChatStore.getState().activeFolderId).toBe(7)
    expect(useChatStore.getState().sessions.map((item) => item.id)).toEqual([1])
    pending.resolve({ sessions: [session(7, "foldered", 7)], has_more: false, next_offset: 0 })
    await loading

    expect(listSessionsMock).toHaveBeenCalledWith({ limit: 100, offset: 0, folderId: 7 })
    expect(useChatStore.getState().sessions.map((item) => item.id)).toEqual([7, 1])
  })

  it("移入文件夹后从未分组视图移除该话题", async () => {
    updateSessionMock.mockResolvedValue({ ...session(1, "unfiled", 9), folder_id: 9 })
    useChatStore.setState({
      activeFolderId: "unfiled",
      sessions: [session(1, "unfiled"), session(2, "stay")],
    })

    await useChatStore.getState().moveSessionToFolder(1, 9)

    expect(updateSessionMock).toHaveBeenCalledWith(1, { folder_id: 9 })
    expect(useChatStore.getState().sessions.map((item) => item.id)).toEqual([2])
  })

  it("删除当前会话时清空本地消息、流式状态和历史激活 ID", async () => {
    deleteSessionMock.mockResolvedValue({ message: "session deleted" })
    localStorage.setItem("active_session_id", "7")
    useChatStore.setState({
      sessions: [session(7, "empty draft"), session(8, "keep")],
      activeSessionId: 7,
      messages: [user(701, "local pending", { session_id: 7, is_local: true, local_state: "pending" })],
      streaming: {
        status: "streaming",
        content: "partial",
        thinking: "thinking",
        toolCalls: [{ id: "tool-1", name: "web_search", status: "running" }],
        segments: [{ type: "content", content: "partial" }],
        requestId: "run-7",
        error: "stale error",
      },
      isLoadingMessages: true,
      hasMoreMessages: true,
      isLoadingOlder: true,
      compactionOwners: { 7: { sessionId: 7, operationId: "compact-7", notice: "working" } },
    })

    await useChatStore.getState().deleteSession(7)

    const state = useChatStore.getState()
    expect(deleteSessionMock).toHaveBeenCalledWith(7)
    expect(state.sessions.map((item) => item.id)).toEqual([8])
    expect(state.activeSessionId).toBeNull()
    expect(state.messages).toEqual([])
    expect(state.streaming).toEqual({
      status: "idle",
      content: "",
      thinking: "",
      toolCalls: [],
      segments: [],
      requestId: null,
      error: null,
      replayGap: false,
      retryTrace: null,
    })
    expect(state.isLoadingMessages).toBe(false)
    expect(state.hasMoreMessages).toBe(false)
    expect(state.isLoadingOlder).toBe(false)
    expect(state.compactionOwners).toEqual({})
    expect(localStorage.getItem("active_session_id")).toBeNull()
  })
})

describe("chat message loading guards", () => {
  it("历史请求迟到时不覆盖恢复中的本地回答副本", async () => {
    listMessageWindowMock.mockResolvedValue({
      messages: [
        user(1, "question", {
          is_local: false,
          local_state: "persisted",
          message_data: { role: "user", content: "question", metadata: { run_id: "run-late-history" } },
        }),
        assistant(2, "durable answer", {
          is_local: false,
          local_state: "persisted",
          message_data: { role: "assistant", content: "durable answer", metadata: { run_id: "run-late-history" } },
        }),
      ],
      first_turn_id: 1,
      last_turn_id: 1,
      has_older: false,
      has_newer: false,
    })

    useChatStore.setState({
      activeSessionId: 1,
      messages: [
        assistant(-1, "recovered local answer", {
          is_local: true,
          local_state: "syncing",
          local_request_id: "run-late-history",
          message_data: { role: "assistant", content: "recovered local answer", metadata: { run_id: "run-late-history" } },
        }),
      ],
    })

    await useChatStore.getState().loadMessages(1)

    const messages = useChatStore.getState().messages
    expect(messages).toHaveLength(2)
    expect(messages.map((message) => message.id)).toEqual([1, 2])
    expect(messages[1].message_data.content).toBe("durable answer")
  })

  it("忽略切换会话后返回的旧消息列表", async () => {
    const stale = deferred<messagesApi.MessageWindowResponse>()
    const current = deferred<messagesApi.MessageWindowResponse>()

    listMessageWindowMock.mockImplementation((sessionId: number) => {
      if (sessionId === 19) return stale.promise
      if (sessionId === 21) return current.promise
      throw new Error(`unexpected session ${sessionId}`)
    })

    useChatStore.setState({ activeSessionId: 19 })
    const staleLoad = useChatStore.getState().loadMessages(19)

    useChatStore.setState({ activeSessionId: 21 })
    const currentLoad = useChatStore.getState().loadMessages(21)

    current.resolve({ messages: [user(210, "current session", { session_id: 21 })], first_turn_id: 210, last_turn_id: 210, has_older: false, has_newer: false })
    await currentLoad

    stale.resolve({ messages: [user(190, "stale session", { session_id: 19 })], first_turn_id: 190, last_turn_id: 190, has_older: true, has_newer: false })
    await staleLoad

    expect(useChatStore.getState().activeSessionId).toBe(21)
    expect(useChatStore.getState().messages).toHaveLength(1)
    expect(useChatStore.getState().messages[0].session_id).toBe(21)
    expect(useChatStore.getState().messages[0].message_data.content).toBe("current session")
    expect(useChatStore.getState().hasMoreMessages).toBe(false)
    expect(useChatStore.getState().isLoadingMessages).toBe(false)
  })

  it("消息加载失败时保留可展示错误并结束 loading", async () => {
    listMessageWindowMock.mockRejectedValue(new Error("backend unavailable"))

    useChatStore.setState({ activeSessionId: 31 })
    await expect(useChatStore.getState().loadMessages(31)).rejects.toThrow("backend unavailable")

    expect(useChatStore.getState().messages).toHaveLength(0)
    expect(useChatStore.getState().messageLoadError).toBe("backend unavailable")
    expect(useChatStore.getState().isLoadingMessages).toBe(false)
  })

  it("切换会话后丢弃旧会话的向上分页结果", async () => {
    const older = deferred<messagesApi.MessageWindowResponse>()
    listMessageWindowMock.mockReturnValue(older.promise)

    useChatStore.setState({
      activeSessionId: 19,
      messages: [user(100, "newest in old session", { session_id: 19 })],
      hasMoreMessages: true,
      isLoadingOlder: false,
      firstLoadedTurnId: 100,
    })

    const loadOlder = useChatStore.getState().loadOlderMessages()
    expect(listMessageWindowMock).toHaveBeenCalledWith(19, { beforeTurnId: 100, turnLimit: 12 })

    useChatStore.setState({
      activeSessionId: 21,
      messages: [user(210, "current session", { session_id: 21 })],
      hasMoreMessages: false,
      isLoadingOlder: false,
    })

    older.resolve({ messages: [user(90, "older stale session", { session_id: 19 })], first_turn_id: 90, last_turn_id: 90, has_older: false, has_newer: true })
    await expect(loadOlder).resolves.toBe(0)

    expect(useChatStore.getState().messages).toHaveLength(1)
    expect(useChatStore.getState().messages[0].session_id).toBe(21)
    expect(useChatStore.getState().messages[0].message_data.content).toBe("current session")
    expect(useChatStore.getState().isLoadingOlder).toBe(false)
  })

  it("向上分页在途时串行拒绝向下分页并在下一次调用恢复", async () => {
    const older = deferred<messagesApi.MessageWindowResponse>()
    listMessageWindowMock
      .mockImplementationOnce(() => older.promise)
      .mockResolvedValueOnce({
        messages: [user(110, "newer")],
        first_turn_id: 110,
        last_turn_id: 110,
        has_older: true,
        has_newer: false,
      })
    useChatStore.setState({
      activeSessionId: 1,
      messages: [user(100, "middle")],
      hasMoreMessages: true,
      hasNewerMessages: true,
      firstLoadedTurnId: 100,
      lastLoadedTurnId: 100,
    })

    const loadingOlder = useChatStore.getState().loadOlderMessages()
    await expect(useChatStore.getState().loadNewerMessages()).resolves.toBe(0)
    expect(listMessageWindowMock).toHaveBeenCalledTimes(1)

    older.resolve({ messages: [user(90, "older")], first_turn_id: 90, last_turn_id: 90, has_older: false, has_newer: true })
    await expect(loadingOlder).resolves.toBe(1)
    expect(useChatStore.getState()).toMatchObject({ isLoadingOlder: false, isLoadingNewer: false })

    await expect(useChatStore.getState().loadNewerMessages()).resolves.toBe(1)
    expect(listMessageWindowMock).toHaveBeenCalledTimes(2)
  })

  it("向下分页在途时串行拒绝向上分页并在下一次调用恢复", async () => {
    const newer = deferred<messagesApi.MessageWindowResponse>()
    listMessageWindowMock
      .mockImplementationOnce(() => newer.promise)
      .mockResolvedValueOnce({
        messages: [user(90, "older")],
        first_turn_id: 90,
        last_turn_id: 90,
        has_older: false,
        has_newer: true,
      })
    useChatStore.setState({
      activeSessionId: 1,
      messages: [user(100, "middle")],
      hasMoreMessages: true,
      hasNewerMessages: true,
      firstLoadedTurnId: 100,
      lastLoadedTurnId: 100,
    })

    const loadingNewer = useChatStore.getState().loadNewerMessages()
    await expect(useChatStore.getState().loadOlderMessages()).resolves.toBe(0)
    expect(listMessageWindowMock).toHaveBeenCalledTimes(1)

    newer.resolve({ messages: [user(110, "newer")], first_turn_id: 110, last_turn_id: 110, has_older: true, has_newer: false })
    await expect(loadingNewer).resolves.toBe(1)
    expect(useChatStore.getState()).toMatchObject({ isLoadingOlder: false, isLoadingNewer: false })

    await expect(useChatStore.getState().loadOlderMessages()).resolves.toBe(1)
    expect(listMessageWindowMock).toHaveBeenCalledTimes(2)
  })

  it("around 替换窗口后丢弃旧向上分页响应并主动释放 loading", async () => {
    const older = deferred<messagesApi.MessageWindowResponse>()
    listMessageWindowMock.mockImplementation((_sessionId, options) => {
      if (options?.beforeTurnId) return older.promise
      if (options?.aroundTurnId === 500) return Promise.resolve({
        messages: [user(500, "target")],
        first_turn_id: 500,
        last_turn_id: 500,
        has_older: true,
        has_newer: true,
      })
      throw new Error(`unexpected options ${JSON.stringify(options)}`)
    })
    useChatStore.setState({
      activeSessionId: 1,
      messages: [user(100, "old window")],
      hasMoreMessages: true,
      firstLoadedTurnId: 100,
      lastLoadedTurnId: 100,
    })

    const loadingOlder = useChatStore.getState().loadOlderMessages()
    await expect(useChatStore.getState().loadMessageWindowAround(500)).resolves.toBe(true)
    expect(useChatStore.getState().isLoadingOlder).toBe(false)

    older.resolve({ messages: [user(90, "stale older")], first_turn_id: 90, last_turn_id: 90, has_older: false, has_newer: true })
    await expect(loadingOlder).resolves.toBe(0)
    expect(useChatStore.getState().messages.map((message) => message.id)).toEqual([500])
    expect(useChatStore.getState()).toMatchObject({ firstLoadedTurnId: 500, lastLoadedTurnId: 500 })
  })

  it("full reload 替换窗口后丢弃旧向下分页响应", async () => {
    const newer = deferred<messagesApi.MessageWindowResponse>()
    listMessageWindowMock.mockImplementation((_sessionId, options) => {
      if (options?.afterTurnId) return newer.promise
      if (options?.latest) return Promise.resolve({
        messages: [user(500, "latest")],
        first_turn_id: 500,
        last_turn_id: 500,
        has_older: true,
        has_newer: false,
      })
      throw new Error(`unexpected options ${JSON.stringify(options)}`)
    })
    useChatStore.setState({
      activeSessionId: 1,
      messages: [user(100, "old window")],
      hasNewerMessages: true,
      firstLoadedTurnId: 100,
      lastLoadedTurnId: 100,
    })

    const loadingNewer = useChatStore.getState().loadNewerMessages()
    await expect(useChatStore.getState().loadMessages(1)).resolves.toBe(true)

    newer.resolve({ messages: [user(110, "stale newer")], first_turn_id: 110, last_turn_id: 110, has_older: true, has_newer: true })
    await expect(loadingNewer).resolves.toBe(0)
    expect(useChatStore.getState().messages.map((message) => message.id)).toEqual([500])
    expect(useChatStore.getState()).toMatchObject({ firstLoadedTurnId: 500, lastLoadedTurnId: 500, isLoadingNewer: false })
  })

  it("向上合并后不会把接口页的新端边界误当成本地窗口缺口", async () => {
    listMessageWindowMock.mockResolvedValue({
      messages: [user(90, "older")],
      first_turn_id: 90,
      last_turn_id: 90,
      has_older: true,
      has_newer: true,
    })
    useChatStore.setState({
      activeSessionId: 1,
      messages: [user(100, "latest")],
      hasMoreMessages: true,
      hasNewerMessages: false,
      isLoadingOlder: false,
      firstLoadedTurnId: 100,
      lastLoadedTurnId: 100,
    })

    await useChatStore.getState().loadOlderMessages()

    expect(useChatStore.getState()).toMatchObject({
      firstLoadedTurnId: 90,
      lastLoadedTurnId: 100,
      hasMoreMessages: true,
      hasNewerMessages: false,
    })
  })

  it("向下合并后不会把接口页的旧端边界误当成本地窗口缺口", async () => {
    listMessageWindowMock.mockResolvedValue({
      messages: [user(110, "newer")],
      first_turn_id: 110,
      last_turn_id: 110,
      has_older: true,
      has_newer: true,
    })
    useChatStore.setState({
      activeSessionId: 1,
      messages: [user(100, "oldest")],
      hasMoreMessages: false,
      hasNewerMessages: true,
      isLoadingNewer: false,
      firstLoadedTurnId: 100,
      lastLoadedTurnId: 100,
    })

    await useChatStore.getState().loadNewerMessages()

    expect(useChatStore.getState()).toMatchObject({
      firstLoadedTurnId: 100,
      lastLoadedTurnId: 110,
      hasMoreMessages: false,
      hasNewerMessages: true,
    })
  })

  it("按当前阅读方向裁剪正文窗口并保留双向边界", () => {
    const messages = Array.from({ length: 20 }, (_, index) => user(index + 1, `turn ${index + 1}`))
    useChatStore.setState({
      messages,
      firstLoadedTurnId: 1,
      lastLoadedTurnId: 20,
      hasMoreMessages: false,
      hasNewerMessages: false,
    })

    useChatStore.getState().trimLoadedMessageWindow(16, "end")
    expect(useChatStore.getState()).toMatchObject({
      firstLoadedTurnId: 5,
      lastLoadedTurnId: 20,
      hasMoreMessages: true,
      hasNewerMessages: false,
    })

    useChatStore.setState({
      messages,
      firstLoadedTurnId: 1,
      lastLoadedTurnId: 20,
      hasMoreMessages: false,
      hasNewerMessages: false,
    })
    useChatStore.getState().trimLoadedMessageWindow(16, "start")
    expect(useChatStore.getState()).toMatchObject({
      firstLoadedTurnId: 1,
      lastLoadedTurnId: 16,
      hasMoreMessages: false,
      hasNewerMessages: true,
    })
  })
})

describe("compaction ownership", () => {
  it("keeps compaction ownership independent for concurrent sessions", () => {
    const store = useChatStore.getState()
    store.beginCompaction(1, "compact-old", "old")
    store.beginCompaction(2, "compact-new", "new")

    const owners = () => (useChatStore.getState() as unknown as {
      compactionOwners?: Record<number, { sessionId: number; operationId: string; notice: string }>
    }).compactionOwners
    expect(owners()).toEqual({
      1: { sessionId: 1, operationId: "compact-old", notice: "old" },
      2: { sessionId: 2, operationId: "compact-new", notice: "new" },
    })

    store.finishCompaction(1, "compact-old")
    expect(owners()).toEqual({
      2: { sessionId: 2, operationId: "compact-new", notice: "new" },
    })

    store.finishCompaction(2, "compact-new")
    expect(owners()).toEqual({})
  })
})

describe("stream attempt rollback", () => {
  it("removes only the failed attempt suffix from text and segments", () => {
    useChatStore.setState({
      streaming: {
        status: "streaming",
        content: "stable坏答案",
        thinking: "plan错误思路",
        toolCalls: [],
        segments: [{ type: "content", content: "stable坏答案", thinking: "plan错误思路" }],
        requestId: "run-retry",
        error: null,
      },
    })

    useChatStore.getState().rollbackStreamAttempt(3, 4)

    expect(useChatStore.getState().streaming).toEqual(expect.objectContaining({
      content: "stable",
      thinking: "plan",
      segments: [{ type: "content", content: "stable", thinking: "plan" }],
    }))
  })
})

describe("editable tail replacement", () => {
  const userMessage = {
    id: 10,
    session_id: 1,
    schema_version: "v2",
    role: "user",
    has_tool_calls: false,
    has_reasoning: false,
    created_at: "2026-07-28T00:00:00Z",
    message_data: { role: "user", content: "original" },
  } satisfies Message

  it("allows error-only tails and rejects any assistant or tool output", () => {
    const errorMessage = {
      ...userMessage,
      id: 11,
      role: "assistant",
      message_data: {
        role: "assistant",
        content: "请求失败",
        metadata: { ephemeral_error: true },
      },
    } satisfies Message
    expect(editableTailUserMessageId([userMessage, errorMessage])).toBe(10)

    expect(editableTailUserMessageId([
      userMessage,
      { ...errorMessage, message_data: { role: "assistant", content: "", thinking: "started" } },
    ])).toBeNull()
    expect(editableTailUserMessageId([
      userMessage,
      { ...errorMessage, message_data: { role: "assistant", content: "", tool_calls: [{ id: "tool-1" }] } },
    ])).toBeNull()
    expect(editableTailUserMessageId([
      userMessage,
      { ...errorMessage, role: "tool", message_data: { role: "tool", content: "result" } },
    ])).toBeNull()
  })

  it("replaces the visible tail and preserves the logical turn count after confirmation", () => {
    useChatStore.setState({
      messages: [
        userMessage,
        {
          ...userMessage,
          id: 11,
          role: "assistant",
          message_data: { role: "assistant", content: "", metadata: { ephemeral_error: true } },
        },
      ],
      conversationTurns: [{
        id: 10,
        sequence: 1,
        user_message_id: 10,
        user_preview: "original",
        created_at: userMessage.created_at,
      }],
      totalConversationTurns: 1,
    })

    expect(useChatStore.getState().beginEditRetry(10, "edited", "edit-run")).toBe(true)
    expect(useChatStore.getState().messages).toHaveLength(1)
    expect(useChatStore.getState().messages[0]).toMatchObject({
      id: 10,
      is_local: true,
      local_request_id: "edit-run",
      message_data: { content: "edited" },
    })

    useChatStore.getState().confirmUserMessageForRequest("edit-run", 20)
    expect(useChatStore.getState().messages[0]).toMatchObject({
      id: 20,
      is_local: false,
      local_state: "persisted",
    })
    expect(useChatStore.getState().conversationTurns).toEqual([expect.objectContaining({
      id: 20,
      user_message_id: 20,
      user_preview: "edited",
    })])
    expect(useChatStore.getState().totalConversationTurns).toBe(1)
  })
})
