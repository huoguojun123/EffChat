import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

const mocks = vi.hoisted(() => {
  const emptyStreaming = {
    status: "idle",
    content: "",
    thinking: "",
    toolCalls: [],
    segments: [],
    requestId: null,
    error: null,
    retryTrace: null,
  }

  let store: Record<string, unknown>
  const resetStore = () => {
    store = {
      activeSessionId: 1,
      activeSessionGeneration: 0,
      messages: [],
      hasNewerMessages: false,
      hasMoreMessages: false,
      isLoadingOlder: false,
      compactionOwners: {},
      streaming: { ...emptyStreaming },
      addMessage: vi.fn(),
      setMessages: vi.fn((messages) => {
        store.messages = messages
      }),
      syncMessages: vi.fn((messages) => {
        store.messages = messages
      }),
      loadMessages: vi.fn(),
      beginEditRetry: vi.fn(() => true),
      confirmUserMessageForRequest: vi.fn(),
      updateMessagesByRequest: vi.fn(),
      updateStreaming: vi.fn((partial) => {
        store.streaming = { ...(store.streaming as Record<string, unknown>), ...(partial as Record<string, unknown>) }
      }),
      resetStreaming: vi.fn(() => {
        store.streaming = { ...emptyStreaming }
      }),
      appendStreamContent: vi.fn((delta) => {
        const streaming = store.streaming as { content: string }
        streaming.content += delta
      }),
      appendStreamThinking: vi.fn((delta) => {
        const streaming = store.streaming as { thinking: string }
        streaming.thinking += delta
      }),
      rollbackStreamAttempt: vi.fn((contentRunes, thinkingRunes) => {
        const streaming = store.streaming as { content: string; thinking: string }
        streaming.content = Array.from(streaming.content).slice(0, -contentRunes || undefined).join("")
        streaming.thinking = Array.from(streaming.thinking).slice(0, -thinkingRunes || undefined).join("")
      }),
      addStreamToolCall: vi.fn(),
      updateStreamToolCall: vi.fn(),
      commitStreamingMessage: vi.fn(() => {
        store.streaming = { ...emptyStreaming }
        return null
      }),
      beginCompaction: vi.fn((sessionId, operationId, notice = "") => {
        store.compactionOwners = {
          ...(store.compactionOwners as Record<number, unknown>),
          [sessionId]: { sessionId, operationId, notice },
        }
      }),
      finishCompaction: vi.fn((sessionId, operationId) => {
        const owners = store.compactionOwners as Record<number, { operationId: string }>
        if (owners[sessionId]?.operationId === operationId) {
          const next = { ...owners }
          delete next[sessionId]
          store.compactionOwners = next
        }
      }),
    }
  }
  resetStore()

  const useChatStore = Object.assign(vi.fn((selector) => selector(store)), {
    getState: () => store,
    setState: (partial: Record<string, unknown>) => {
      Object.assign(store, partial)
    },
  })

  return {
    get store() {
      return store
    },
    resetStore,
    useChatStore,
    getActiveRun: vi.fn(),
    getRunStatus: vi.fn(),
    cancelRun: vi.fn(),
    listMessages: vi.fn(),
    preflightSessionMessage: vi.fn(),
    handleAuthExpired: vi.fn(),
  }
})

vi.mock("react", () => ({
  useCallback: (fn: unknown) => fn,
  useEffect: (callback: () => void) => callback(),
  useRef: <T,>(value: T) => ({ current: value }),
}))

vi.mock("@/stores/chat", () => ({
  useChatStore: mocks.useChatStore,
}))

vi.mock("@/api/runs", () => ({
  getActiveRun: mocks.getActiveRun,
  getRunStatus: mocks.getRunStatus,
}))

vi.mock("@/api/messages", () => ({
  listMessages: mocks.listMessages,
}))

vi.mock("@/api/sessions", () => ({
  cancelRun: mocks.cancelRun,
  compactSessionUrl: vi.fn((sessionId: number, runId: string, source?: string, thinkingEffort?: string, preserveMessageId?: number) => {
    const params = new URLSearchParams({ client_run_id: runId, source: source || "manual" })
    if (thinkingEffort) params.set("thinking_effort", thinkingEffort)
    if (preserveMessageId) params.set("preserve_message_id", String(preserveMessageId))
    return `/sessions/${sessionId}/compact?${params.toString()}`
  }),
  preflightSessionMessage: mocks.preflightSessionMessage,
}))

vi.mock("@/api/client", () => ({
  handleAuthExpired: mocks.handleAuthExpired,
}))

import { useSSE } from "@/hooks/useSSE"

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

describe("useSSE", () => {
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  beforeEach(() => {
    mocks.resetStore()
    mocks.useChatStore.mockClear()
    mocks.getActiveRun.mockReset()
    mocks.getRunStatus.mockReset()
    mocks.cancelRun.mockReset()
    mocks.cancelRun.mockResolvedValue(undefined)
    mocks.listMessages.mockReset()
    mocks.listMessages.mockResolvedValue({ messages: [], has_more: false })
    const loadMessages = mocks.store.loadMessages as ReturnType<typeof vi.fn>
    loadMessages.mockReset()
    mocks.preflightSessionMessage.mockReset()
    mocks.preflightSessionMessage.mockResolvedValue({ status: "ok", needs_compaction: false })
    mocks.handleAuthExpired.mockReset()
    vi.stubGlobal("fetch", vi.fn())
    vi.stubGlobal("localStorage", { getItem: vi.fn(() => "test-token"), setItem: vi.fn(), removeItem: vi.fn() })
  })

  it("does not restore a stale run after the active session changes", async () => {
    const activeRun = deferred<{
      run: {
        run_id: string
        session_id: number
        user_message_id: number
        status: "running"
        cursor: number
        content: string
      }
    }>()
    mocks.getActiveRun.mockReturnValue(activeRun.promise)

    const { resumeActiveRun } = useSSE()
    const pending = resumeActiveRun(1)

    mocks.store.activeSessionId = 2
    activeRun.resolve({
      run: {
        run_id: "run-1",
        session_id: 1,
        user_message_id: 10,
        status: "running",
        cursor: 0,
        content: "stale",
      },
    })

    await pending

    expect((mocks.store.streaming as { status: string }).status).toBe("idle")
    expect(mocks.store.compactionOwners).toEqual({})
    expect(mocks.store.updateStreaming).not.toHaveBeenCalled()
    expect(globalThis.fetch).not.toHaveBeenCalled()
  })

  it("merges a resumed compaction result without replacing loaded history", async () => {
    mocks.store.messages = [{
      id: 1,
      session_id: 1,
      schema_version: "v2",
      message_data: { role: "user", content: "older history" },
      role: "user",
      has_tool_calls: false,
      has_reasoning: false,
      created_at: "2026-07-23T00:00:00Z",
      is_local: false,
      local_state: "persisted",
    }]
    mocks.getActiveRun.mockResolvedValue({
      run: {
        run_id: "compact-resume",
        session_id: 1,
        kind: "compaction",
        status: "running",
        cursor: 0,
      },
    })
    vi.mocked(globalThis.fetch).mockResolvedValue(new Response(
      'event: compaction_complete\ndata: {"checkpoint_id":1}\n\n',
      { status: 200, headers: { "Content-Type": "text/event-stream" } }
    ))

    const { resumeActiveRun } = useSSE()
    await resumeActiveRun(1)

    expect(mocks.getActiveRun).toHaveBeenCalledWith(1)
    expect(mocks.store.beginCompaction).toHaveBeenCalledWith(1, "compact-resume", "正在整理会话上下文")
    expect(mocks.listMessages).toHaveBeenCalledWith(1)
    expect(mocks.store.setMessages).not.toHaveBeenCalled()
    expect(mocks.store.syncMessages).toHaveBeenCalledWith([])
  })

  it("leaves memory maintenance recovery to the memory dialog", async () => {
    mocks.getActiveRun.mockResolvedValue({
      run: {
        run_id: "memory-resume",
        session_id: 1,
        kind: "memory_maintenance",
        status: "running",
        cursor: 3,
      },
    })

    const { resumeActiveRun } = useSSE()
    await resumeActiveRun(1)

    expect(mocks.getActiveRun).toHaveBeenCalledWith(1)
    expect(globalThis.fetch).not.toHaveBeenCalled()
    expect(mocks.store.beginCompaction).not.toHaveBeenCalled()
    expect(mocks.store.updateStreaming).not.toHaveBeenCalled()
  })

  it("reconnects a recovering run with the current durable snapshot", async () => {
    mocks.store.streaming = {
      status: "recovering",
      requestId: "run-reconnect",
      content: "locally visible",
      thinking: "",
      toolCalls: [],
      segments: [{ type: "content", content: "locally visible" }],
      error: "连接暂时中断，正在确认回答",
    }
    mocks.getActiveRun.mockResolvedValue({
      run: {
        run_id: "run-reconnect",
        session_id: 1,
        kind: "chat",
        status: "running",
        cursor: 8,
        content: "durable snapshot",
      },
    })
    vi.mocked(globalThis.fetch).mockResolvedValue(new Response(
      'event: message_complete\ndata: {"finish_reason":"stop"}\n\n',
      { status: 200, headers: { "Content-Type": "text/event-stream" } }
    ))

    const { resumeActiveRun } = useSSE()
    await resumeActiveRun(1)

    expect(mocks.getActiveRun).toHaveBeenCalledWith(1)
    expect(globalThis.fetch).toHaveBeenCalledWith(
      "/api/v1/sessions/1/runs/run-reconnect/resume?cursor=8",
      expect.any(Object)
    )
    expect(mocks.store.updateStreaming).toHaveBeenCalledWith(expect.objectContaining({
      status: "streaming",
      requestId: "run-reconnect",
      content: "durable snapshot",
    }))
  })

  it("keeps already rendered output when a truncated run reconnects", async () => {
    mocks.store.streaming = {
      status: "recovering",
      requestId: "run-truncated-reconnect",
      content: "visible prefix",
      thinking: "visible reasoning",
      toolCalls: [],
      segments: [{ type: "content", content: "visible prefix", thinking: "visible reasoning" }],
      error: "连接暂时中断，正在确认回答",
    }
    mocks.getActiveRun.mockResolvedValue({
      run: {
        run_id: "run-truncated-reconnect",
        session_id: 1,
        kind: "chat",
        status: "running",
        cursor: 12,
        replay_from: 6,
        output_truncated: true,
        content: "retained tail only",
      },
    })
    vi.mocked(globalThis.fetch).mockResolvedValue(new Response(
      'event: message_complete\ndata: {"finish_reason":"stop"}\n\n',
      { status: 200, headers: { "Content-Type": "text/event-stream" } }
    ))

    const { resumeActiveRun } = useSSE()
    await resumeActiveRun(1)

    expect(globalThis.fetch).toHaveBeenCalledWith(
      "/api/v1/sessions/1/runs/run-truncated-reconnect/resume?cursor=12",
      expect.any(Object)
    )
    expect(mocks.store.updateStreaming).toHaveBeenCalledWith(expect.objectContaining({
      content: "visible prefix",
      thinking: "visible reasoning",
    }))
  })

  it("restores a resumed run with the original reasoning and tool order", async () => {
    mocks.getActiveRun.mockResolvedValue({
      run: {
        run_id: "run-ordered",
        session_id: 1,
        user_message_id: 10,
        status: "running",
        cursor: 5,
        content: "最终答案",
        thinking: "先检索再核对",
        tool_calls: [{ id: "tool-1", name: "web_search", status: "done", result: "完成" }],
        segments: [
          { type: "content", thinking: "先检索" },
          { type: "tool", tool_calls: [{ id: "tool-1", name: "web_search", status: "done", result: "完成" }] },
          { type: "content", thinking: "再核对" },
          { type: "content", content: "最终答案" },
        ],
      },
    })
    vi.mocked(globalThis.fetch).mockResolvedValue(new Response(
      "event: message_complete\ndata: {\"finish_reason\":\"stop\"}\n\n",
      { status: 200, headers: { "Content-Type": "text/event-stream" } }
    ))

    const { resumeActiveRun } = useSSE()
    await resumeActiveRun(1)

    expect(mocks.store.updateStreaming).toHaveBeenCalledWith(expect.objectContaining({
      segments: [
        { type: "content", thinking: "先检索" },
        { type: "tool", tool_calls: [{ id: "tool-1", name: "web_search", status: "done", result: "完成" }] },
        { type: "content", thinking: "再核对" },
        { type: "content", content: "最终答案" },
      ],
    }))
  })

  it("syncs the accepted user turn before showing a resumed assistant stream", async () => {
    const acceptedUserMessage = {
      id: 10,
      session_id: 1,
      schema_version: "v2",
      role: "user",
      message_data: { role: "user", content: "另一页刚刚发送的消息" },
      has_tool_calls: false,
      has_reasoning: false,
      created_at: "2026-07-25T00:00:00Z",
      is_local: false,
      local_state: "persisted",
    }
    mocks.getActiveRun.mockResolvedValue({
      run: {
        run_id: "run-other-tab",
        session_id: 1,
        user_message_id: 10,
        status: "running",
        cursor: 1,
        content: "",
      },
    })
    mocks.listMessages.mockResolvedValue({ messages: [acceptedUserMessage], has_more: false })
    vi.mocked(globalThis.fetch).mockResolvedValue(new Response(
      'event: message_complete\ndata: {"finish_reason":"stop"}\n\n',
      { status: 200, headers: { "Content-Type": "text/event-stream" } }
    ))

    const { resumeActiveRun } = useSSE()
    await resumeActiveRun(1)

    const syncMessages = mocks.store.syncMessages as ReturnType<typeof vi.fn>
    const updateStreaming = mocks.store.updateStreaming as ReturnType<typeof vi.fn>
    const streamingCall = updateStreaming.mock.calls.findIndex(([partial]) => partial.status === "streaming")

    expect(syncMessages).toHaveBeenCalledWith([acceptedUserMessage])
    expect(streamingCall).toBeGreaterThanOrEqual(0)
    expect(syncMessages.mock.invocationCallOrder[0]).toBeLessThan(updateStreaming.mock.invocationCallOrder[streamingCall])
  })

  it("replays a truncated run without rendering its partial snapshot segments", async () => {
    mocks.getActiveRun.mockResolvedValue({
      run: {
        run_id: "run-truncated",
        session_id: 1,
        user_message_id: 10,
        status: "running",
        cursor: 8,
        replay_from: 4,
        output_truncated: true,
        content: "partial",
        segments: [{ type: "content", content: "partial" }],
      },
    })
    vi.mocked(globalThis.fetch).mockResolvedValue(new Response(
      "event: message_complete\ndata: {\"finish_reason\":\"stop\"}\n\n",
      { status: 200, headers: { "Content-Type": "text/event-stream" } }
    ))

    const { resumeActiveRun } = useSSE()
    await resumeActiveRun(1)

    expect(mocks.store.updateStreaming).toHaveBeenCalledWith(expect.objectContaining({
      content: "",
      thinking: "",
      toolCalls: [],
      segments: [],
    }))
    expect(globalThis.fetch).toHaveBeenCalledWith(
      "/api/v1/sessions/1/runs/run-truncated/resume?cursor=4",
      expect.any(Object)
    )
  })

  it("marks a resume replay gap instead of presenting retained tail output as continuous", async () => {
    mocks.getActiveRun.mockResolvedValue({
      run: {
        run_id: "run-gap",
        session_id: 1,
        user_message_id: 10,
        status: "running",
        cursor: 8,
        replay_from: 4,
        output_truncated: true,
        content: "retained tail",
      },
    })
    vi.mocked(globalThis.fetch).mockResolvedValue(new Response(
      [
        'event: replay_gap\ndata: {"requested_cursor":0,"replay_from":4}\n\n',
        'event: run_snapshot\ndata: {"run_id":"run-gap","session_id":1,"status":"running","cursor":8,"replay_from":4,"output_truncated":true,"content":"retained tail"}\n\n',
        'event: content_delta\ndata: {"delta":"future output"}\n\n',
        'event: message_complete\ndata: {"finish_reason":"stop"}\n\n',
      ].join(""),
      { status: 200, headers: { "Content-Type": "text/event-stream" } }
    ))

    const { resumeActiveRun } = useSSE()
    await resumeActiveRun(1)

    expect(mocks.store.updateStreaming).toHaveBeenCalledWith(expect.objectContaining({
      status: "recovering",
      replayGap: true,
      content: "",
    }))
    expect(mocks.store.appendStreamContent).toHaveBeenCalledWith("future output")
  })

  it("keeps the active snapshot prefix when replay truncates after the snapshot request", async () => {
    mocks.getActiveRun.mockResolvedValue({
      run: {
        run_id: "run-gap-after-snapshot",
        session_id: 1,
        user_message_id: 10,
        status: "running",
        cursor: 8,
        content: "durable prefix",
      },
    })
    vi.mocked(globalThis.fetch).mockResolvedValue(new Response(
      [
        'event: replay_gap\ndata: {"requested_cursor":8,"replay_from":9}\n\n',
        'event: run_snapshot\ndata: {"run_id":"run-gap-after-snapshot","session_id":1,"status":"running","cursor":12,"replay_from":9,"output_truncated":true,"content":"retained tail"}\n\n',
        'event: content_delta\ndata: {"delta":"future output"}\n\n',
        'event: message_complete\ndata: {"finish_reason":"stop"}\n\n',
      ].join(""),
      { status: 200, headers: { "Content-Type": "text/event-stream" } }
    ))

    const { resumeActiveRun } = useSSE()
    await resumeActiveRun(1)

    expect(mocks.store.updateStreaming).toHaveBeenCalledWith(expect.objectContaining({
      status: "recovering",
      replayGap: true,
      content: "durable prefix",
      segments: [{ type: "content", content: "durable prefix" }],
    }))
  })

  it("keeps newly rendered segments when a replay gap arrives after the active snapshot", async () => {
    mocks.getActiveRun.mockResolvedValue({
      run: {
        run_id: "run-live-prefix",
        session_id: 1,
        user_message_id: 10,
        status: "running",
        cursor: 8,
        content: "initial snapshot",
      },
    })
    mocks.listMessages.mockResolvedValue({ messages: [], has_more: false })
    let controller!: ReadableStreamDefaultController<Uint8Array>
    vi.mocked(globalThis.fetch).mockResolvedValue(new Response(new ReadableStream({
      start(nextController) {
        controller = nextController
        controller.enqueue(new TextEncoder().encode(
          'event: replay_gap\ndata: {"requested_cursor":8,"replay_from":9}\n\n'
        ))
      },
    }), { status: 200, headers: { "Content-Type": "text/event-stream" } }))

    const { resumeActiveRun } = useSSE()
    const pending = resumeActiveRun(1)
    await vi.waitFor(() => expect(mocks.store.updateStreaming).toHaveBeenCalledWith(expect.objectContaining({
      replayGap: true,
      status: "recovering",
    })))

    mocks.store.streaming = {
      status: "streaming",
      requestId: "run-live-prefix",
      content: "visible after request",
      thinking: "fresh reasoning",
      toolCalls: [],
      segments: [{ type: "content", content: "visible after request", thinking: "fresh reasoning" }],
      error: null,
    }
    controller.enqueue(new TextEncoder().encode(
      'event: run_snapshot\ndata: {"run_id":"run-live-prefix","session_id":1,"status":"running","cursor":12,"replay_from":9,"output_truncated":true,"content":"retained tail"}\n\n'
    ))
    controller.enqueue(new TextEncoder().encode(
      'event: message_complete\ndata: {"finish_reason":"stop"}\n\n'
    ))
    controller.close()
    await pending

    expect(mocks.store.updateStreaming).toHaveBeenCalledWith(expect.objectContaining({
      replayGap: true,
      content: "visible after request",
      thinking: "fresh reasoning",
      segments: [{ type: "content", content: "visible after request", thinking: "fresh reasoning" }],
    }))
  })

  it("syncs a resumed durable error exactly once", async () => {
    mocks.store.messages = [{
      id: 10,
      session_id: 1,
      schema_version: "v2",
      role: "user",
      message_data: { role: "user", content: "already visible" },
      has_tool_calls: false,
      has_reasoning: false,
      created_at: "2026-07-25T00:00:00Z",
      is_local: false,
      local_state: "persisted",
    }]
    mocks.getActiveRun.mockResolvedValue({
      run: {
        run_id: "run-terminal-error",
        session_id: 1,
        user_message_id: 10,
        status: "running",
        cursor: 4,
      },
    })
    vi.mocked(globalThis.fetch).mockResolvedValue(new Response(
      'event: error\ndata: {"error":"模型请求失败，请稍后重试","message_id":42}\n\n',
      { status: 200, headers: { "Content-Type": "text/event-stream" } }
    ))

    const { resumeActiveRun } = useSSE()
    await resumeActiveRun(1)

    expect(mocks.listMessages).toHaveBeenCalledTimes(1)
    expect(mocks.store.updateStreaming).toHaveBeenCalledWith(expect.objectContaining({ status: "syncing" }))
    expect(mocks.store.updateStreaming).not.toHaveBeenCalledWith(expect.objectContaining({ status: "failed_local" }))
  })

  it("consumes a CRLF final SSE frame without a trailing separator", async () => {
    mocks.getActiveRun.mockResolvedValue({
      run: {
        run_id: "run-crlf",
        session_id: 1,
        user_message_id: 10,
        status: "running",
        cursor: 2,
      },
    })
    vi.mocked(globalThis.fetch).mockResolvedValue(new Response(
      'event: message_complete\r\ndata: {"finish_reason":"stop"}',
      { status: 200, headers: { "Content-Type": "text/event-stream" } }
    ))

    const { resumeActiveRun } = useSSE()
    await resumeActiveRun(1)

    expect(mocks.store.updateStreaming).toHaveBeenCalledWith(expect.objectContaining({ status: "syncing" }))
  })

  it("shows structured quota errors from stream start", async () => {
    vi.mocked(globalThis.fetch).mockResolvedValue({
      ok: false,
      status: 429,
      json: async () => ({
        error: "今日消息数已达上限（3）",
        code: "daily_message_limit_exceeded",
        used: 3,
        limit: 3,
        reset_at: "2026-07-04T00:00:00Z",
      }),
    } as Response)

    const onAccepted = vi.fn()
    const { sendMessage } = useSSE()
    await expect(sendMessage(1, "hello", undefined, { onAccepted })).rejects.toThrow("今日消息数已达上限")

    expect(onAccepted).not.toHaveBeenCalled()
    expect(mocks.store.addMessage).not.toHaveBeenCalled()
    expect(mocks.store.updateStreaming).not.toHaveBeenCalledWith(expect.objectContaining({ status: "failed_local" }))
  })

  it("reconciles an uncertain send delivery before response headers", async () => {
    const onAccepted = vi.fn()
    vi.mocked(globalThis.fetch).mockRejectedValue(new TypeError("Failed to fetch"))
    mocks.getRunStatus.mockImplementation(async (_sessionId: number, runId: string) => ({
      run: {
        run_id: runId,
        session_id: 1,
        kind: "chat",
        status: "completed",
        terminal_message_id: 42,
      },
    }))

    const { sendMessage } = useSSE()
    await expect(sendMessage(1, "question", undefined, { onAccepted })).resolves.toBeUndefined()

    await vi.waitFor(() => expect(mocks.getRunStatus).toHaveBeenCalledWith(1, expect.any(String), expect.any(Number)))
    await vi.waitFor(() => expect((mocks.store.streaming as { status: string }).status).toBe("idle"))
    expect(onAccepted).toHaveBeenCalledTimes(1)
    expect(mocks.store.addMessage).toHaveBeenCalledTimes(1)
    expect(mocks.listMessages).toHaveBeenCalledWith(1)
  })

  it("waits through an initial missing run before accepting an uncertain delivery", async () => {
    vi.useFakeTimers()
    const onAccepted = vi.fn()
    vi.mocked(globalThis.fetch).mockRejectedValue(new TypeError("Failed to fetch"))
    mocks.getRunStatus
      .mockRejectedValueOnce(Object.assign(new Error("run not found"), { status: 404 }))
      .mockImplementationOnce(async (_sessionId: number, runId: string) => ({
        run: { run_id: runId, session_id: 1, kind: "chat", status: "running" },
      }))

    const { sendMessage, disconnectActiveStream } = useSSE()
    const pending = sendMessage(1, "question", undefined, { onAccepted })
    await vi.advanceTimersByTimeAsync(201)
    await pending

    expect(mocks.getRunStatus.mock.calls.length).toBeGreaterThanOrEqual(2)
    expect(onAccepted).toHaveBeenCalledTimes(1)
    disconnectActiveStream(1)
  })

  it("keeps an uncertain draft unaccepted when no durable run appears", async () => {
    vi.useFakeTimers()
    const onAccepted = vi.fn()
    vi.mocked(globalThis.fetch).mockRejectedValue(new TypeError("Failed to fetch"))
    mocks.getRunStatus.mockRejectedValue(Object.assign(new Error("run not found"), { status: 404 }))

    const { sendMessage } = useSSE()
    const pending = expect(sendMessage(1, "question", undefined, { onAccepted })).rejects.toThrow("Failed to fetch")
    await vi.advanceTimersByTimeAsync(2100)
    await pending

    expect(onAccepted).not.toHaveBeenCalled()
    expect(mocks.store.addMessage).not.toHaveBeenCalled()
    expect((mocks.store.streaming as { status: string }).status).toBe("idle")
  })

  it("passes the request token when a stream start receives 401", async () => {
    vi.mocked(globalThis.fetch).mockResolvedValue({
      ok: false,
      status: 401,
      statusText: "Unauthorized",
      json: async () => ({ error: "unauthorized" }),
    } as Response)

    const { sendMessage } = useSSE()
    await expect(sendMessage(1, "question")).rejects.toThrow("unauthorized")

    expect(mocks.handleAuthExpired).toHaveBeenCalledWith("test-token")
  })

  it("shows model switch guidance for unavailable session model", async () => {
    vi.mocked(globalThis.fetch).mockResolvedValue({
      ok: false,
      status: 400,
      json: async () => ({
        error: "渠道 \"google\" 未配置，请切换模型",
        code: "channel_not_configured",
        provider: "google",
        model_id: "gemini-3.5-flash",
      }),
    } as Response)

    const onAccepted = vi.fn()
    const { sendMessage } = useSSE()
    await expect(sendMessage(1, "hello", undefined, { onAccepted })).rejects.toThrow("请在上方模型切换器选择一个可用模型")

    expect(onAccepted).not.toHaveBeenCalled()
    expect(mocks.store.addMessage).not.toHaveBeenCalled()
  })

  it("does not create a local user message when preflight compaction fails", async () => {
    mocks.preflightSessionMessage.mockResolvedValue({
      status: "needs_compaction",
      needs_compaction: true,
      retryable: true,
      message: "发送前需要先整理上下文",
    })
    vi.mocked(globalThis.fetch).mockResolvedValue(new Response(
      'event: error\ndata: {"message":"压缩失败，请联系管理员","error_code":"compaction_failed","retryable":true}\n\n',
      { status: 200, headers: { "Content-Type": "text/event-stream" } }
    ))

    const { sendMessage } = useSSE()
    await expect(sendMessage(1, "hello")).rejects.toThrow("压缩失败，请联系管理员")

    expect(mocks.store.addMessage).not.toHaveBeenCalled()
    expect(mocks.store.compactionOwners).toEqual({})
  })

  it("does not treat a compaction stream without a terminal event as skip", async () => {
    vi.mocked(globalThis.fetch).mockResolvedValue(new Response(
      'event: ping\ndata: {}\n\n',
      { status: 200, headers: { "Content-Type": "text/event-stream" } }
    ))

    const { startCompaction } = useSSE()
    await expect(startCompaction(1)).rejects.toThrow("压缩结果未确认")

    expect(mocks.store.compactionOwners).toEqual({})
  })

  it("cancels a compaction stream when its terminal error is handled", async () => {
    const cancel = vi.fn()
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(new TextEncoder().encode(
          'event: error\ndata: {"error":"压缩失败，请联系管理员"}\n\n'
        ))
      },
      cancel,
    })
    vi.mocked(globalThis.fetch).mockResolvedValue(new Response(body, {
      status: 200,
      headers: { "Content-Type": "text/event-stream" },
    }))

    const { startCompaction } = useSSE()
    await expect(startCompaction(1)).rejects.toThrow("压缩失败，请联系管理员")

    expect(cancel).toHaveBeenCalledTimes(1)
  })

  it("compacts and retries once when the send endpoint enforces the final gate", async () => {
    const onAccepted = vi.fn()
    vi.mocked(globalThis.fetch)
      .mockResolvedValueOnce({
        ok: false,
        status: 409,
        json: async () => ({
          error: "发送前需要先整理上下文",
          code: "compaction_required",
          retryable: true,
        }),
      } as Response)
      .mockResolvedValueOnce(new Response(
        'event: compaction_complete\ndata: {"checkpoint_id":1}\n\n',
        { status: 200, headers: { "Content-Type": "text/event-stream" } }
      ))
      .mockResolvedValueOnce(new Response(
        'event: message_complete\ndata: {"finish_reason":"stop"}\n\n',
        { status: 200, headers: { "Content-Type": "text/event-stream" } }
      ))

    const { sendMessage } = useSSE()
    await sendMessage(1, "hello", undefined, { onAccepted })

    expect(globalThis.fetch).toHaveBeenCalledTimes(3)
    expect(onAccepted).toHaveBeenCalledTimes(1)
    expect(mocks.store.addMessage).toHaveBeenCalledTimes(1)
    expect(mocks.store.beginCompaction).toHaveBeenCalledWith(1, expect.any(String), "发送前需要先整理上下文")
    expect(mocks.store.compactionOwners).toEqual({})
  })

  it("protects the original user turn when retry requires compaction", async () => {
    mocks.listMessages.mockResolvedValue({
      messages: [
        { id: 9, session_id: 1, role: "user", message_data: { role: "user", content: "retry user" } },
        { id: 10, session_id: 1, role: "assistant", message_data: { role: "assistant", content: "old answer" } },
      ],
      has_more: false,
    })
    vi.mocked(globalThis.fetch)
      .mockResolvedValueOnce({
        ok: false,
        status: 409,
        json: async () => ({
          error: "重新生成前需要先整理上下文",
          code: "compaction_required",
          retryable: true,
          preserve_message_id: 9,
        }),
      } as Response)
      .mockResolvedValueOnce(new Response(
        'event: compaction_complete\ndata: {"checkpoint_id":1}\n\n',
        { status: 200, headers: { "Content-Type": "text/event-stream" } }
      ))
      .mockResolvedValueOnce(new Response(
        'event: message_complete\ndata: {"finish_reason":"stop"}\n\n',
        { status: 200, headers: { "Content-Type": "text/event-stream" } }
      ))

    const { retryMessage } = useSSE()
    await expect(retryMessage(1, 10)).resolves.toBeUndefined()

    const calls = vi.mocked(globalThis.fetch).mock.calls
    expect(calls).toHaveLength(3)
    expect(String(calls[0][0])).toContain("/messages/10/retry?client_run_id=")
    expect(String(calls[1][0])).toContain("preserve_message_id=9")
    expect(calls[2][0]).toBe(calls[0][0])
    expect(mocks.store.beginCompaction).toHaveBeenCalledWith(1, expect.any(String), "重新生成前需要先整理上下文")
    expect(mocks.store.compactionOwners).toEqual({})
  })

  it("replaces an unanswered tail only after edited retry admission", async () => {
    const original = {
      id: 9,
      session_id: 1,
      role: "user",
      has_tool_calls: false,
      has_reasoning: false,
      message_data: { role: "user", content: "original" },
    }
    mocks.listMessages
      .mockResolvedValueOnce({ messages: [original], has_more: false })
      .mockResolvedValueOnce({ messages: [{ ...original, id: 42, message_data: { role: "user", content: "edited" } }], has_more: false })
    vi.mocked(globalThis.fetch).mockResolvedValue(new Response(
      [
        'event: message_start\ndata: {"user_message_id":42}\n\n',
        'event: message_complete\ndata: {"finish_reason":"stop"}\n\n',
      ].join(""),
      { status: 200, headers: { "Content-Type": "text/event-stream" } }
    ))

    const { editRetryMessage } = useSSE()
    await expect(editRetryMessage(1, 9, "edited")).resolves.toBeUndefined()

    const fetchCall = vi.mocked(globalThis.fetch).mock.calls[0]
    expect(String(fetchCall[0])).toContain("/messages/9/edit-retry")
    expect(JSON.parse(String((fetchCall[1] as RequestInit).body))).toEqual({
      content: "edited",
      client_run_id: expect.any(String),
    })
    expect(mocks.store.beginEditRetry).toHaveBeenCalledWith(9, "edited", expect.any(String))
    expect(mocks.store.confirmUserMessageForRequest).toHaveBeenCalledWith(expect.any(String), 42)
  })

  it("keeps the original tail untouched when edited retry admission fails", async () => {
    const original = {
      id: 9,
      session_id: 1,
      role: "user",
      has_tool_calls: false,
      has_reasoning: false,
      message_data: { role: "user", content: "original" },
    }
    mocks.listMessages.mockResolvedValue({ messages: [original], has_more: false })
    vi.mocked(globalThis.fetch).mockResolvedValue(new Response(
      JSON.stringify({
        error: "助手已开始输出，不能再修改这条消息",
        code: "message_already_answered",
        retryable: false,
      }),
      { status: 409, headers: { "Content-Type": "application/json" } }
    ))

    const { editRetryMessage } = useSSE()
    await expect(editRetryMessage(1, 9, "edited")).rejects.toThrow("助手已开始输出")
    expect(mocks.store.beginEditRetry).not.toHaveBeenCalled()
  })

  it("returns to the latest window before retrying from history", async () => {
    mocks.store.hasNewerMessages = true
    const loadMessages = mocks.store.loadMessages as ReturnType<typeof vi.fn>
    loadMessages.mockImplementation(async () => {
      mocks.store.hasNewerMessages = false
      mocks.store.messages = [
        { id: 19, session_id: 1, role: "user", message_data: { role: "user", content: "latest user" } },
        { id: 20, session_id: 1, role: "assistant", message_data: { role: "assistant", content: "latest failed answer" } },
      ]
    })
    vi.mocked(globalThis.fetch).mockResolvedValue(new Response(
      'event: message_complete\ndata: {"finish_reason":"stop"}\n\n',
      { status: 200, headers: { "Content-Type": "text/event-stream" } }
    ))

    const { retryMessage } = useSSE()
    await expect(retryMessage(1, 20)).resolves.toBeUndefined()

    const fetchMock = vi.mocked(globalThis.fetch)
    expect(loadMessages).toHaveBeenCalledWith(1)
    expect(loadMessages.mock.invocationCallOrder[0]).toBeLessThan(fetchMock.mock.invocationCallOrder[0])
    expect(String(fetchMock.mock.calls[0][0])).toContain("/messages/20/retry?client_run_id=")
  })

  it("reports a durable failed retry that keeps the previous answer selected", async () => {
    const selectedMessages = [
      { id: 9, session_id: 1, role: "user", message_data: { role: "user", content: "retry user" } },
      { id: 10, session_id: 1, role: "assistant", message_data: { role: "assistant", content: "previous answer" } },
    ]
    mocks.listMessages
      .mockResolvedValueOnce({ messages: selectedMessages, has_more: false })
      .mockResolvedValueOnce({ messages: selectedMessages, has_more: false })
    vi.mocked(globalThis.fetch).mockResolvedValue(new Response(
      'event: error\ndata: {"error":"模型服务暂时不可用，请稍后重试","code":"model_upstream_unavailable","message_id":99}\n\n',
      { status: 200, headers: { "Content-Type": "text/event-stream" } }
    ))

    const { retryMessage } = useSSE()
    await expect(retryMessage(1, 10)).rejects.toThrow("模型服务暂时不可用，请稍后重试")

    expect(mocks.store.messages).toEqual(selectedMessages)
    expect((mocks.store.streaming as { status: string }).status).toBe("idle")
  })

  it("does not retry after protected compaction cannot shrink the context", async () => {
    mocks.listMessages.mockResolvedValue({
      messages: [
        { id: 9, session_id: 1, role: "user", message_data: { role: "user", content: "retry user" } },
        { id: 10, session_id: 1, role: "assistant", message_data: { role: "assistant", content: "old answer" } },
      ],
      has_more: false,
    })
    vi.mocked(globalThis.fetch)
      .mockResolvedValueOnce({
        ok: false,
        status: 409,
        json: async () => ({
          error: "重新生成前需要先整理上下文",
          code: "compaction_required",
          retryable: true,
          preserve_message_id: 9,
        }),
      } as Response)
      .mockResolvedValueOnce(new Response(
        'event: compaction_skip\ndata: {"reason":"no history to compact"}\n\n',
        { status: 200, headers: { "Content-Type": "text/event-stream" } }
      ))

    const { retryMessage } = useSSE()
    await expect(retryMessage(1, 10)).rejects.toThrow("当前上下文无法再缩短")

    expect(globalThis.fetch).toHaveBeenCalledTimes(2)
  })

  it("does not send or accept a draft after the active session changes during preflight", async () => {
    const preflight = deferred<{ status: "ok"; needs_compaction: false }>()
    const onAccepted = vi.fn()
    mocks.preflightSessionMessage.mockReturnValue(preflight.promise)

    const { sendMessage } = useSSE()
    const pending = sendMessage(1, "draft for session one", undefined, { onAccepted })
    mocks.store.activeSessionId = 2
    preflight.resolve({ status: "ok", needs_compaction: false })

    await expect(pending).rejects.toThrow("会话已切换")
    expect(globalThis.fetch).not.toHaveBeenCalled()
    expect(onAccepted).not.toHaveBeenCalled()
    expect(mocks.store.addMessage).not.toHaveBeenCalled()
  })

  it("does not send a stale draft after returning to the same session during preflight", async () => {
    const preflight = deferred<{ status: "ok"; needs_compaction: false }>()
    const onAccepted = vi.fn()
    mocks.preflightSessionMessage.mockReturnValue(preflight.promise)

    const { sendMessage } = useSSE()
    const pending = sendMessage(1, "stale draft", undefined, { onAccepted })
    mocks.store.activeSessionId = 2
    mocks.store.activeSessionGeneration = 1
    mocks.store.activeSessionId = 1
    mocks.store.activeSessionGeneration = 2
    preflight.resolve({ status: "ok", needs_compaction: false })

    await expect(pending).rejects.toThrow("会话已切换")
    expect(globalThis.fetch).not.toHaveBeenCalled()
    expect(onAccepted).not.toHaveBeenCalled()
    expect(mocks.store.addMessage).not.toHaveBeenCalled()
  })

  it("reopens an interrupted stream while the browser remains online", async () => {
    mocks.getRunStatus.mockImplementation(async (_sessionId: number, runId: string) => ({
      run: { run_id: runId, session_id: 1, kind: "chat", status: "running" },
    }))
    mocks.getActiveRun.mockImplementation(async () => ({
      run: {
        run_id: (mocks.store.streaming as { requestId: string }).requestId,
        session_id: 1,
        user_message_id: 10,
        kind: "chat",
        status: "running",
        cursor: 0,
        content: "",
      },
    }))
    vi.mocked(globalThis.fetch)
      .mockResolvedValueOnce(new Response("", { status: 200, headers: { "Content-Type": "text/event-stream" } }))
      .mockResolvedValueOnce(new Response([
        'event: content_delta\ndata: {"delta":"recovered"}\n\n',
        'event: message_complete\ndata: {"finish_reason":"stop"}\n\n',
      ].join(""), { status: 200, headers: { "Content-Type": "text/event-stream" } }))

    const { sendMessage } = useSSE()
    await sendMessage(1, "continue this run")

    await vi.waitFor(() => expect(globalThis.fetch).toHaveBeenCalledTimes(2))
    await vi.waitFor(() => expect(mocks.store.appendStreamContent).toHaveBeenCalledWith("recovered"))
    expect(String(vi.mocked(globalThis.fetch).mock.calls[1]?.[0])).toContain("/resume?cursor=0")
  })

  it("acknowledges an accepted send without mutating the newly selected session", async () => {
    const response = deferred<Response>()
    vi.mocked(globalThis.fetch).mockReturnValue(response.promise)
    const onAccepted = vi.fn()

    const { sendMessage } = useSSE()
    const pending = sendMessage(1, "session one draft", undefined, { onAccepted })
    await vi.waitFor(() => expect(globalThis.fetch).toHaveBeenCalledTimes(1))
    mocks.store.activeSessionId = 2
    response.resolve(new Response(
      'event: message_complete\ndata: {"finish_reason":"stop"}\n\n',
      { status: 200, headers: { "Content-Type": "text/event-stream" } }
    ))

    await pending

    expect(onAccepted).toHaveBeenCalledTimes(1)
    expect(mocks.store.addMessage).not.toHaveBeenCalled()
    expect(mocks.store.updateStreaming).not.toHaveBeenCalledWith(expect.objectContaining({ status: "streaming" }))
  })

  it("does not let a superseded same-session stream append stale output", async () => {
    let firstBodyController!: ReadableStreamDefaultController<Uint8Array>
    const firstBody = new ReadableStream<Uint8Array>({
      start(controller) {
        firstBodyController = controller
      },
    })
    vi.mocked(globalThis.fetch)
      .mockResolvedValueOnce(new Response(firstBody, {
        status: 200,
        headers: { "Content-Type": "text/event-stream" },
      }))
      .mockResolvedValueOnce(new Response([
        'event: content_delta\ndata: {"delta":"fresh"}\n\n',
        'event: message_complete\ndata: {"finish_reason":"stop"}\n\n',
      ].join(""), { status: 200, headers: { "Content-Type": "text/event-stream" } }))

    const { sendMessage } = useSSE()
    const first = sendMessage(1, "first draft")
    await vi.waitFor(() => expect(globalThis.fetch).toHaveBeenCalledTimes(1))

    await sendMessage(1, "replacement draft")

    firstBodyController.enqueue(new TextEncoder().encode([
      'event: content_delta\ndata: {"delta":"stale"}\n\n',
      'event: message_complete\ndata: {"finish_reason":"stop"}\n\n',
    ].join("")))
    firstBodyController.close()
    await first

    expect(mocks.store.appendStreamContent).toHaveBeenCalledWith("fresh")
    expect(mocks.store.appendStreamContent).not.toHaveBeenCalledWith("stale")
  })

  it("does not let a superseded terminal invalidate the current stream reconciliation", async () => {
    let firstBodyController!: ReadableStreamDefaultController<Uint8Array>
    const firstBody = new ReadableStream<Uint8Array>({
      start(controller) {
        firstBodyController = controller
      },
    })
    const currentReconciliation = deferred<{ messages: never[]; has_more: boolean }>()
    mocks.listMessages.mockReturnValueOnce(currentReconciliation.promise)
    vi.mocked(globalThis.fetch)
      .mockResolvedValueOnce(new Response(firstBody, {
        status: 200,
        headers: { "Content-Type": "text/event-stream" },
      }))
      .mockResolvedValueOnce(new Response(
        'event: message_complete\ndata: {"finish_reason":"stop"}\n\n',
        { status: 200, headers: { "Content-Type": "text/event-stream" } }
      ))

    const { sendMessage } = useSSE()
    const first = sendMessage(1, "first draft")
    await vi.waitFor(() => expect(globalThis.fetch).toHaveBeenCalledTimes(1))

    const current = sendMessage(1, "replacement draft")
    await vi.waitFor(() => expect(mocks.listMessages).toHaveBeenCalledTimes(1))

    firstBodyController.enqueue(new TextEncoder().encode(
      'event: message_complete\\ndata: {"finish_reason":"stop"}\\n\\n'
    ))
    firstBodyController.close()

    currentReconciliation.resolve({ messages: [], has_more: false })
    await Promise.all([first, current])

    expect(mocks.listMessages).toHaveBeenCalledTimes(1)
    expect(mocks.store.syncMessages).toHaveBeenCalledWith([])
  })

  it("does not abort a newer session stream while cleaning up the previous session", async () => {
    const signals: AbortSignal[] = []
    vi.mocked(globalThis.fetch).mockImplementation((_input, init) => new Promise<Response>((_resolve, reject) => {
      const signal = (init as RequestInit | undefined)?.signal
      if (!signal) throw new Error("expected an abort signal")
      signals.push(signal)
      signal.addEventListener("abort", () => reject(new DOMException("Aborted", "AbortError")), { once: true })
    }))

    const { sendMessage, disconnectActiveStream } = useSSE()
    const first = sendMessage(1, "session one").catch(() => undefined)
    await vi.waitFor(() => expect(signals).toHaveLength(1))

    mocks.store.activeSessionId = 2
    const second = sendMessage(2, "session two").catch(() => undefined)
    await vi.waitFor(() => expect(signals).toHaveLength(2))

    ;(disconnectActiveStream as (sessionId?: number) => void)(1)
    expect(signals[1].aborted).toBe(false)

    ;(disconnectActiveStream as (sessionId?: number) => void)(2)
    await Promise.all([first, second])
  })

  it("clears a recovered stream when its session view disconnects after the reader has closed", () => {
    const { disconnectActiveStream } = useSSE()
    disconnectActiveStream()
    const resetStreaming = mocks.store.resetStreaming as ReturnType<typeof vi.fn>
    resetStreaming.mockClear()
    mocks.store.streaming = {
      status: "recovering",
      requestId: "run-closed-reader",
      content: "old output",
      thinking: "",
      toolCalls: [],
      segments: [{ type: "content", content: "old output" }],
      error: "连接暂时中断，正在确认回答",
    }

    disconnectActiveStream(1)

    expect(resetStreaming).toHaveBeenCalledTimes(1)
    expect((mocks.store.streaming as { status: string }).status).toBe("idle")
  })

  it("does not apply an old reconciliation after returning to the same session", async () => {
    const reconciliation = deferred<{ messages: never[]; has_more: boolean }>()
    mocks.listMessages.mockReturnValue(reconciliation.promise)
    vi.mocked(globalThis.fetch).mockResolvedValue(new Response(
      'event: message_complete\ndata: {"finish_reason":"stop"}\n\n',
      { status: 200, headers: { "Content-Type": "text/event-stream" } }
    ))

    const { sendMessage } = useSSE()
    const pending = sendMessage(1, "reconcile me")
    await vi.waitFor(() => expect(mocks.listMessages).toHaveBeenCalledWith(1))

    mocks.store.activeSessionId = 2
    mocks.store.activeSessionGeneration = 1
    mocks.store.activeSessionId = 1
    mocks.store.activeSessionGeneration = 2
    reconciliation.resolve({ messages: [], has_more: false })
    await pending

    expect(mocks.store.syncMessages).not.toHaveBeenCalled()
  })

  it("rolls back a failed provider attempt before committing the retry", async () => {
    vi.mocked(globalThis.fetch).mockResolvedValue(new Response([
      'event: content_delta\ndata: {"delta":"坏答案"}\n\n',
      'event: thinking_delta\ndata: {"delta":"错误思路"}\n\n',
      'event: assistant_attempt_reset\ndata: {"content_runes":3,"thinking_runes":4}\n\n',
      'event: content_delta\ndata: {"delta":"正确答案"}\n\n',
      'event: message_complete\ndata: {"finish_reason":"stop"}\n\n',
    ].join(""), { status: 200, headers: { "Content-Type": "text/event-stream" } }))

    const { sendMessage } = useSSE()
    await sendMessage(1, "question")

    expect(mocks.store.rollbackStreamAttempt).toHaveBeenCalledWith(3, 4)
    expect(mocks.store.commitStreamingMessage).toHaveBeenCalledWith(expect.objectContaining({
      content: "正确答案",
      thinking: undefined,
    }))
  })

  it("batches streamed deltas into one visual update before completion", async () => {
    const callbacks: FrameRequestCallback[] = []
    vi.stubGlobal("requestAnimationFrame", vi.fn((callback: FrameRequestCallback) => {
      callbacks.push(callback)
      return callbacks.length
    }))
    vi.mocked(globalThis.fetch).mockResolvedValue(new Response([
      'event: content_delta\ndata: {"delta":"one"}\n\n',
      'event: content_delta\ndata: {"delta":" two"}\n\n',
      'event: content_delta\ndata: {"delta":" three"}\n\n',
      'event: message_complete\ndata: {"finish_reason":"stop"}\n\n',
    ].join(""), { status: 200, headers: { "Content-Type": "text/event-stream" } }))

    const { sendMessage } = useSSE()
    await sendMessage(1, "question")

    expect(callbacks).toHaveLength(1)
    expect(mocks.store.appendStreamContent).toHaveBeenCalledTimes(1)
    expect(mocks.store.appendStreamContent).toHaveBeenCalledWith("one two three")
  })

  it("preserves interleaved reasoning and content order while batching", async () => {
    const updates: string[] = []
    vi.mocked(mocks.store.appendStreamThinking as (delta: string) => void).mockImplementation((delta) => { updates.push(`thinking:${delta}`) })
    vi.mocked(mocks.store.appendStreamContent as (delta: string) => void).mockImplementation((delta) => { updates.push(`content:${delta}`) })
    vi.stubGlobal("requestAnimationFrame", vi.fn(() => 1))
    vi.mocked(globalThis.fetch).mockResolvedValue(new Response([
      'event: thinking_delta\ndata: {"delta":"plan"}\n\n',
      'event: content_delta\ndata: {"delta":"answer"}\n\n',
      'event: thinking_delta\ndata: {"delta":"check"}\n\n',
      'event: message_complete\ndata: {"finish_reason":"stop"}\n\n',
    ].join(""), { status: 200, headers: { "Content-Type": "text/event-stream" } }))

    const { sendMessage } = useSSE()
    await sendMessage(1, "question")

    expect(updates).toEqual(["thinking:plan", "content:answer", "thinking:check"])
  })

  it("shows a bounded retry trace until the next provider attempt produces output", async () => {
    vi.mocked(globalThis.fetch).mockResolvedValue(new Response([
      'event: model_retry\ndata: {"attempt":1,"max_attempts":2,"delay_ms":1500,"category":"transient"}\n\n',
      'event: content_delta\ndata: {"delta":"恢复后的回答"}\n\n',
      'event: message_complete\ndata: {"finish_reason":"stop"}\n\n',
    ].join(""), { status: 200, headers: { "Content-Type": "text/event-stream" } }))

    const { sendMessage } = useSSE()
    await sendMessage(1, "question")

    expect(mocks.store.updateStreaming).toHaveBeenCalledWith(expect.objectContaining({
      retryTrace: {
        attempt: 1,
        maxAttempts: 2,
        delayMs: 1500,
        category: "transient",
      },
    }))
    expect(mocks.store.updateStreaming).toHaveBeenCalledWith({ retryTrace: null })
    expect(mocks.store.commitStreamingMessage).toHaveBeenCalledWith(expect.objectContaining({
      content: "恢复后的回答",
    }))
  })

  it("marks a terminal partial response incomplete without adding an error card", async () => {
    vi.mocked(globalThis.fetch).mockResolvedValue(new Response([
      'event: content_delta\ndata: {"delta":"已经生成的回答"}\n\n',
      'event: message_complete\ndata: {"finish_reason":"canceled","incomplete":true}\n\n',
    ].join(""), { status: 200, headers: { "Content-Type": "text/event-stream" } }))

    const { sendMessage } = useSSE()
    await sendMessage(1, "question")

    expect(mocks.store.appendStreamContent).toHaveBeenCalledWith("已经生成的回答")
    expect(mocks.store.commitStreamingMessage).toHaveBeenCalledWith(expect.objectContaining({
      content: "已经生成的回答",
      metadata: expect.objectContaining({ incomplete: true }),
    }))
    expect(mocks.store.addMessage).toHaveBeenCalledTimes(1)
  })

  it("keeps output-limit content and marks it incomplete", async () => {
    vi.mocked(globalThis.fetch).mockResolvedValue(new Response([
      'event: content_delta\ndata: {"delta":"达到上限前的正文"}\n\n',
      'event: message_complete\ndata: {"finish_reason":"output_limit","incomplete":true}\n\n',
    ].join(""), { status: 200, headers: { "Content-Type": "text/event-stream" } }))

    const { sendMessage } = useSSE()
    await sendMessage(1, "question")

    expect(mocks.store.commitStreamingMessage).toHaveBeenCalledWith(expect.objectContaining({
      content: "达到上限前的正文",
      response_meta: expect.objectContaining({ finish_reason: "output_limit" }),
      metadata: expect.objectContaining({ incomplete: true }),
    }))
  })

  it("reconciles durable history after committing the local terminal reply", async () => {
    vi.mocked(globalThis.fetch).mockResolvedValue(new Response([
      'event: content_delta\ndata: {"delta":"completed reply"}\n\n',
      'event: message_complete\ndata: {"finish_reason":"stop"}\n\n',
    ].join(""), { status: 200, headers: { "Content-Type": "text/event-stream" } }))

    const { sendMessage } = useSSE()
    await sendMessage(1, "question")

    expect(mocks.store.syncMessages).toHaveBeenCalledWith([])
    expect((mocks.store.streaming as { status: string }).status).toBe("idle")
  })

  it("keeps an accepted run in recovery while durable status is still running", async () => {
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(new TextEncoder().encode('event: content_delta\\ndata: {"delta":"already visible"}\\n\\n'))
        controller.error(new Error("network lost"))
      },
    })
    vi.mocked(globalThis.fetch).mockResolvedValue(new Response(body, {
      status: 200,
      headers: { "Content-Type": "text/event-stream" },
    }))
    mocks.getRunStatus.mockImplementation(async (_sessionId: number, runId: string) => ({
      run: { run_id: runId, session_id: 1, kind: "chat", status: "running" },
    }))

    const { sendMessage, disconnectActiveStream } = useSSE()
    await sendMessage(1, "question")

    expect(mocks.getRunStatus).toHaveBeenCalled()
    expect(mocks.store.updateStreaming).toHaveBeenCalledWith(expect.objectContaining({ status: "recovering" }))
    expect((mocks.store.streaming as { status: string }).status).toBe("recovering")

    disconnectActiveStream(1)
  })

  it("syncs a disconnected run after its durable terminal arrives", async () => {
    vi.useFakeTimers()
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.error(new Error("network lost"))
      },
    })
    vi.mocked(globalThis.fetch).mockResolvedValue(new Response(body, {
      status: 200,
      headers: { "Content-Type": "text/event-stream" },
    }))
    mocks.getRunStatus
      .mockImplementationOnce(async (_sessionId: number, runId: string) => ({
        run: { run_id: runId, session_id: 1, kind: "chat", status: "running" },
      }))
      .mockImplementationOnce(async (_sessionId: number, runId: string) => ({
        run: { run_id: runId, session_id: 1, kind: "chat", status: "completed", terminal_message_id: 77 },
      }))

    const { sendMessage } = useSSE()
    await sendMessage(1, "question")
    await vi.advanceTimersByTimeAsync(400)

    expect(mocks.getRunStatus).toHaveBeenCalledTimes(2)
    expect(mocks.listMessages).toHaveBeenCalledWith(1)
    expect((mocks.store.streaming as { status: string }).status).toBe("idle")
  })

  it("releases recovery when durable run lookup is no longer possible", async () => {
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.error(new Error("network lost"))
      },
    })
    vi.mocked(globalThis.fetch).mockResolvedValue(new Response(body, {
      status: 200,
      headers: { "Content-Type": "text/event-stream" },
    }))
    mocks.getRunStatus.mockRejectedValue(Object.assign(new Error("run not found"), { status: 404 }))

    const { sendMessage, disconnectActiveStream } = useSSE()
    try {
      await sendMessage(1, "question")
      await vi.waitFor(() => expect((mocks.store.streaming as { status: string }).status).toBe("idle"))
      expect(mocks.store.updateMessagesByRequest).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({
        local_state: "failed_local",
      }))
    } finally {
      disconnectActiveStream(1)
    }
  })

  it("keeps a generated reply locally when persistence fails without adding an error card", async () => {
    vi.mocked(globalThis.fetch).mockResolvedValue(new Response([
      'event: message_start\ndata: {"user_message_id":42}\n\n',
      'event: thinking_delta\ndata: {"delta":"先分析"}\n\n',
      'event: content_delta\ndata: {"delta":"已经生成的回答"}\n\n',
      'event: tool_call_start\ndata: {"tool_call_id":"tool-1","tool_name":"web_search"}\n\n',
      'event: tool_call_result\ndata: {"tool_call_id":"tool-1","result":"完成"}\n\n',
      'event: error\ndata: {"error":"回复已生成但保存失败，请重试最后一条消息","code":"message_persist_failed","retryable":true}\n\n',
    ].join(""), { status: 200, headers: { "Content-Type": "text/event-stream" } }))

    const { sendMessage } = useSSE()
    await sendMessage(1, "question")

    expect(mocks.store.appendStreamContent).toHaveBeenCalledWith("已经生成的回答")
    expect(mocks.store.commitStreamingMessage).toHaveBeenCalledWith(expect.objectContaining({
      content: "已经生成的回答",
      thinking: "先分析",
      metadata: expect.objectContaining({ unsaved: true }),
    }))
    expect(mocks.store.updateMessagesByRequest).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({
      local_state: "failed_local",
      local_error: "回复已生成但保存失败，请重试最后一条消息",
    }))
    expect(mocks.store.confirmUserMessageForRequest).toHaveBeenCalledWith(expect.any(String), 42)
    expect(mocks.store.addMessage).toHaveBeenCalledTimes(1)
  })

  it("waits for the stop terminal commit before replacing a provisional partial reply", async () => {
    vi.useFakeTimers()
    mocks.store.streaming = {
      status: "streaming",
      content: "already visible",
      thinking: "",
      toolCalls: [],
      segments: [{ type: "content", content: "already visible" }],
      requestId: "run-stop",
      error: null,
    }
    const running = {
      run_id: "run-stop",
      session_id: 1,
      kind: "chat",
      user_message_id: 42,
      status: "running",
    }
    for (let i = 0; i < 10; i++) {
      mocks.getRunStatus.mockResolvedValueOnce({ run: running })
    }
    mocks.getRunStatus.mockResolvedValueOnce({
      run: { ...running, status: "canceled", terminal_message_id: 43 },
    })

    const { abort } = useSSE()
    const pending = abort()
    await vi.advanceTimersByTimeAsync(3300)
    await pending

    expect(mocks.cancelRun).toHaveBeenCalledWith(1, "run-stop")
    expect(mocks.getRunStatus).toHaveBeenCalledTimes(11)
    expect(mocks.listMessages).toHaveBeenCalledWith(1)
    expect(mocks.store.commitStreamingMessage).toHaveBeenCalledWith(expect.objectContaining({
      content: "already visible",
      metadata: expect.objectContaining({ incomplete: true }),
    }))
  })

  it("does not read partial history while a stopped run is still running", async () => {
    vi.useFakeTimers()
    mocks.store.streaming = {
      status: "streaming",
      content: "already visible",
      thinking: "",
      toolCalls: [],
      segments: [{ type: "content", content: "already visible" }],
      requestId: "run-still-stopping",
      error: null,
    }
    mocks.getRunStatus.mockResolvedValue({
      run: {
        run_id: "run-still-stopping",
        session_id: 1,
        kind: "chat",
        user_message_id: 42,
        status: "running",
      },
    })

    const { abort, disconnectActiveStream } = useSSE()
    const pending = abort()
    await vi.advanceTimersByTimeAsync(7200)
    await pending

    expect(mocks.listMessages).not.toHaveBeenCalled()
    expect(mocks.store.updateStreaming).toHaveBeenCalledWith(expect.objectContaining({
      status: "recovering",
      requestId: "run-still-stopping",
    }))

    disconnectActiveStream(1)
  })

  it("leaves a natural terminal sync alone when the user taps stop", async () => {
    let closeStream: (() => void) | undefined
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(new TextEncoder().encode('event: message_complete\ndata: {"finish_reason":"stop"}\n\n'))
        closeStream = () => controller.close()
      },
    })
    vi.mocked(globalThis.fetch).mockResolvedValue(new Response(body, {
      status: 200,
      headers: { "Content-Type": "text/event-stream" },
    }))

    const { sendMessage, abort } = useSSE()
    const pending = sendMessage(1, "question")
    await vi.waitFor(() => expect((mocks.store.streaming as { status: string }).status).toBe("syncing"))

    await abort()
    expect(mocks.cancelRun).not.toHaveBeenCalled()

    closeStream?.()
    await pending
    expect((mocks.store.streaming as { status: string }).status).toBe("idle")
  })

  it("syncs a durably accepted user when stop finishes before an assistant is saved", async () => {
    vi.useFakeTimers()
    const cancellation = deferred<void>()
    mocks.store.streaming = {
      status: "streaming",
      content: "already visible",
      thinking: "",
      toolCalls: [],
      segments: [{ type: "content", content: "already visible" }],
      requestId: "run-terminal-failed",
      error: null,
    }
    mocks.cancelRun.mockReturnValue(cancellation.promise)
    mocks.getRunStatus.mockResolvedValue({
      run: {
        run_id: "run-terminal-failed",
        session_id: 1,
        kind: "chat",
        status: "failed",
        user_message_id: 42,
        terminal_message_id: 0,
        error_code: "message_persist_failed",
        error: "回复已生成但保存失败，请重试最后一条消息",
      },
    })

    const { abort } = useSSE()
    const pending = abort()
    try {
      await vi.advanceTimersByTimeAsync(0)
      expect(mocks.getRunStatus).toHaveBeenCalledWith(1, "run-terminal-failed", expect.any(Number))
      expect(mocks.listMessages).toHaveBeenCalledWith(1)
      expect(mocks.store.updateMessagesByRequest).toHaveBeenCalledWith("run-terminal-failed", expect.objectContaining({
        local_state: "failed_local",
        local_error: "回复已生成但保存失败，请重试最后一条消息",
      }))
    } finally {
      cancellation.resolve()
      await vi.advanceTimersByTimeAsync(7200)
      await pending
    }
  })
})
