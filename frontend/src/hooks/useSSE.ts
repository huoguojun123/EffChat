import { useCallback, useEffect, useRef } from "react"
import { useChatStore } from "@/stores/chat"
import type { ActiveRunSnapshot, AttachmentMeta, Message, MessageData, StreamingSegment, ToolCall } from "@/types"
import { listMessages } from "@/api/messages"
import { getActiveRun, getRunStatus } from "@/api/runs"
import { compactSessionUrl, cancelRun, preflightSessionMessage } from "@/api/sessions"
import { handleAuthExpired } from "@/api/client"
import { cloneToolCall, editableTailUserMessageId } from "@/lib/chatMessages"
import { createLocalMessageId } from "@/lib/localMessageId"
import { isUnrecoverableRunStatusError, waitForDelay, waitForRunAppearance, waitForRunSettlement } from "@/lib/runReconciliation"
import { consumeCompactionSSE, createStreamHTTPError, formatErrorDiagnostic, formatErrorPayload, isCompactionRequired, parseUsage, readSSEEvents, readStreamHTTPError, trimTrailingCodePoints } from "@/lib/sseProtocol"
import { safeUUID } from "@/lib/utils"

type StreamRequest = RequestInit & { url: string; authToken: string | null }
type StreamSeed = { content: string; thinking: string; toolCalls: ToolCall[] }
interface SendMessageOptions {
  thinkingEffort?: string
  onAccepted?: () => void
}

const recoveryInitialDelayMs = 400
const recoveryMaxDelayMs = 5000
const recoveryStatusRequestTimeoutMs = 3000

interface ClientStream {
  controller: AbortController
  sessionId: number
  requestId: string
  sessionGeneration: number
}

interface ReconciliationTask {
  sessionId: number
  runId: string
  sessionGeneration: number
  cancelled: boolean
  controller: AbortController
  promise: Promise<void>
}

let activeStream: ClientStream | null = null
const reconciliationGenerations = new Map<number, number>()
const reconciliationTasks = new Map<number, ReconciliationTask>()

function beginStream(sessionId: number, requestId: string): ClientStream {
  activeStream?.controller.abort()
  cancelReconciliation(sessionId)
  const stream: ClientStream = {
    controller: new AbortController(),
    sessionId,
    requestId,
    sessionGeneration: useChatStore.getState().activeSessionGeneration,
  }
  activeStream = stream
  return stream
}

function isCurrentClientStream(stream: ClientStream) {
  const state = useChatStore.getState()
  return activeStream === stream
    && state.activeSessionId === stream.sessionId
    && state.activeSessionGeneration === stream.sessionGeneration
    && state.streaming.requestId === stream.requestId
}

function isActiveClientStream(stream: ClientStream) {
  const state = useChatStore.getState()
  return activeStream === stream
    && state.activeSessionId === stream.sessionId
    && state.activeSessionGeneration === stream.sessionGeneration
}

function cancelReconciliation(sessionId: number, keepRunId?: string) {
  const task = reconciliationTasks.get(sessionId)
  if (!task || task.runId === keepRunId) return
  task.cancelled = true
  task.controller.abort()
  reconciliationTasks.delete(sessionId)
}

function nextReconciliationGeneration(sessionId: number) {
  const generation = (reconciliationGenerations.get(sessionId) || 0) + 1
  reconciliationGenerations.set(sessionId, generation)
  return generation
}

export function useSSE() {
  const addMessage = useChatStore((s) => s.addMessage)
  const syncMessages = useChatStore((s) => s.syncMessages)
  const updateMessagesByRequest = useChatStore((s) => s.updateMessagesByRequest)
  const beginEditRetry = useChatStore((s) => s.beginEditRetry)
  const confirmUserMessageForRequest = useChatStore((s) => s.confirmUserMessageForRequest)
  const updateStreaming = useChatStore((s) => s.updateStreaming)
  const resetStreaming = useChatStore((s) => s.resetStreaming)
  const appendStreamContent = useChatStore((s) => s.appendStreamContent)
  const appendStreamThinking = useChatStore((s) => s.appendStreamThinking)
  const rollbackStreamAttempt = useChatStore((s) => s.rollbackStreamAttempt)
  const addStreamToolCall = useChatStore((s) => s.addStreamToolCall)
  const updateStreamToolCall = useChatStore((s) => s.updateStreamToolCall)
  const commitStreamingMessage = useChatStore((s) => s.commitStreamingMessage)
  const resumeActiveRunRef = useRef<((sessionId: number) => Promise<void>) | null>(null)

  const isCurrentSession = useCallback((sessionId: number) => {
    return useChatStore.getState().activeSessionId === sessionId
  }, [])

  const syncSessionMessages = useCallback(async (
    sessionId: number,
    expectedRequestId?: string,
  ) => {
    const generation = nextReconciliationGeneration(sessionId)
    const initialState = useChatStore.getState()
    const activeSessionGeneration = initialState.activeSessionGeneration
    const messageWindowGeneration = initialState.messageWindowGeneration
    const res = await listMessages(sessionId)
    const state = useChatStore.getState()
    if (
      state.activeSessionId !== sessionId
      || state.activeSessionGeneration !== activeSessionGeneration
      || state.messageWindowGeneration !== messageWindowGeneration
      || reconciliationGenerations.get(sessionId) !== generation
      || (expectedRequestId !== undefined && state.streaming.requestId !== expectedRequestId)
    ) return false
    const incoming = res.messages || []
    if (state.hasNewerMessages) return true
    const oldestIncomingID = incoming.reduce<number | null>((oldest, message) => (
      oldest === null || message.id < oldest ? message.id : oldest
    ), null)
    const hasLoadedOlderPage = oldestIncomingID !== null
      && state.messages.some((message) => !message.is_local && message.id < oldestIncomingID)
    if (!syncMessages(incoming, messageWindowGeneration)) return false
    const current = useChatStore.getState()
    if (current.messageWindowGeneration !== messageWindowGeneration) return false
    useChatStore.setState({
      hasMoreMessages: hasLoadedOlderPage ? state.hasMoreMessages : !!res.has_more,
      isLoadingOlder: false,
    })
    return true
  }, [syncMessages])

  const ownsReconciliationTask = useCallback((task: ReconciliationTask) => {
    const state = useChatStore.getState()
    return !task.cancelled
      && state.activeSessionId === task.sessionId
      && state.activeSessionGeneration === task.sessionGeneration
      && state.streaming.requestId === task.runId
  }, [])

  const scheduleRunReconciliation = useCallback((sessionId: number, runId: string) => {
    const existing = reconciliationTasks.get(sessionId)
    if (existing && !existing.cancelled && existing.runId === runId) return existing.promise
    cancelReconciliation(sessionId)

    const task: ReconciliationTask = {
      sessionId,
      runId,
      sessionGeneration: useChatStore.getState().activeSessionGeneration,
      cancelled: false,
      controller: new AbortController(),
      promise: Promise.resolve(),
    }
    reconciliationTasks.set(sessionId, task)

    task.promise = (async () => {
      let delayMs = 0
      while (ownsReconciliationTask(task)) {
        if (delayMs > 0) await waitForDelay(delayMs, task.controller.signal)
        if (!ownsReconciliationTask(task)) return

        try {
          const { run } = await getRunStatus(sessionId, runId, recoveryStatusRequestTimeoutMs)
          if (!ownsReconciliationTask(task) || run.run_id !== runId) return
          if (run.status !== "running") {
            const synced = await syncSessionMessages(sessionId, runId)
            if (!synced || !ownsReconciliationTask(task)) return
            if (!run.terminal_message_id) {
              updateMessagesByRequest(runId, {
                local_state: "failed_local",
                local_error: run.error || (run.status === "canceled" ? "已停止生成" : "本次回复未能完成"),
              })
            }
            resetStreaming()
            return
          }
          updateStreaming({ status: "recovering", requestId: runId, error: "连接暂时中断，正在确认回答" })
          const resume = resumeActiveRunRef.current
          if (resume) void resume(sessionId).catch(() => undefined)
        } catch (error) {
          if (!ownsReconciliationTask(task)) return
          if (isUnrecoverableRunStatusError(error)) {
            updateMessagesByRequest(runId, {
              local_state: "failed_local",
              local_error: "本次回复状态已不可恢复，请重试最后一条消息",
            })
            resetStreaming()
            return
          }
          updateStreaming({ status: "recovering", requestId: runId, error: "连接暂时中断，正在确认回答" })
        }

        delayMs = delayMs === 0
          ? recoveryInitialDelayMs
          : Math.min(delayMs * 2, recoveryMaxDelayMs)
      }
    })().finally(() => {
      if (reconciliationTasks.get(sessionId) === task) reconciliationTasks.delete(sessionId)
    })

    return task.promise
  }, [ownsReconciliationTask, resetStreaming, syncSessionMessages, updateMessagesByRequest, updateStreaming])

  // 消费一条 SSE 流：解析事件、累积内容、提交消息。正常发送与断线续传共用，
  // 通过 seed 注入续传时已有的快照内容（resume 流仅回放游标之后的增量）。
  const consumeSSE = useCallback(async (
    sessionId: number,
    response: Response,
    requestId: string,
    stream: ClientStream,
    seed?: StreamSeed,
    onReplayGapSnapshot?: (snapshot: ActiveRunSnapshot, current: StreamSeed) => StreamSeed
  ) => {
    let sawTerminalError = false
    let sawMessageComplete = false
    let terminalMessageId: number | undefined
    let terminalError = ""
    let terminalIncomplete = false
    let sawReplayGap = false

    let accContent = seed?.content ?? ""
    let accThinking = seed?.thinking ?? ""
    let accToolCalls: ToolCall[] = seed?.toolCalls ? seed.toolCalls.map(cloneToolCall) : []
    let retryTraceVisible = false
    let pendingDeltas: Array<{ type: "content" | "thinking"; delta: string }> = []
    let framePending = false
    const canMutate = () => isCurrentClientStream(stream)
    const flushPendingDeltas = () => {
      framePending = false
      if (!canMutate()) {
        pendingDeltas = []
        return
      }
      const deltas = pendingDeltas
      pendingDeltas = []
      for (const item of deltas) {
        if (item.type === "content") appendStreamContent(item.delta)
        else appendStreamThinking(item.delta)
      }
    }
    const scheduleDeltaFlush = () => {
      if (framePending) return
      framePending = true
      if (typeof requestAnimationFrame === "function") {
        requestAnimationFrame(flushPendingDeltas)
        return
      }
      queueMicrotask(flushPendingDeltas)
    }
    const enqueueDelta = (type: "content" | "thinking", delta: string) => {
      const last = pendingDeltas[pendingDeltas.length - 1]
      if (last?.type === type) last.delta += delta
      else pendingDeltas.push({ type, delta })
      scheduleDeltaFlush()
    }
    const clearRetryTrace = () => {
      if (!retryTraceVisible) return
      retryTraceVisible = false
      if (canMutate()) updateStreaming({ retryTrace: null })
    }

    const handleEvent = async (evt: string, raw: string) => {
      if (!raw || raw === "[DONE]") return

      let parsed: Record<string, unknown>
      try {
        parsed = JSON.parse(raw)
      } catch {
        return
      }

      switch (evt) {
        case "message_start":
          if (canMutate() && typeof parsed.user_message_id === "number" && parsed.user_message_id > 0) {
            confirmUserMessageForRequest(requestId, parsed.user_message_id)
          }
          break
        case "replay_gap":
          sawReplayGap = true
          if (canMutate()) {
            updateStreaming({
              status: "recovering",
              requestId,
              replayGap: true,
              error: "较早输出正在从服务端恢复",
            })
          }
          break
        case "run_snapshot":
          if (sawReplayGap && onReplayGapSnapshot) {
            const next = onReplayGapSnapshot(parsed as unknown as ActiveRunSnapshot, {
              content: accContent,
              thinking: accThinking,
              toolCalls: accToolCalls.map(cloneToolCall),
            })
            accContent = next.content
            accThinking = next.thinking
            accToolCalls = next.toolCalls.map(cloneToolCall)
          }
          break
        case "content_delta":
          if (typeof parsed.delta === "string") {
            clearRetryTrace()
            accContent += parsed.delta
            if (canMutate()) {
              enqueueDelta("content", parsed.delta)
            }
          }
          break
        case "thinking_delta":
          if (typeof parsed.delta === "string") {
            clearRetryTrace()
            accThinking += parsed.delta
            if (canMutate()) {
              enqueueDelta("thinking", parsed.delta)
            }
          }
          break
        case "assistant_attempt_reset": {
          const contentRunes = Math.max(0, Number(parsed.content_runes || 0))
          const thinkingRunes = Math.max(0, Number(parsed.thinking_runes || 0))
          accContent = trimTrailingCodePoints(accContent, contentRunes)
          accThinking = trimTrailingCodePoints(accThinking, thinkingRunes)
          if (canMutate()) {
            flushPendingDeltas()
            rollbackStreamAttempt(contentRunes, thinkingRunes)
          }
          break
        }
        case "model_retry": {
          const attempt = Number(parsed.attempt)
          const maxAttempts = Number(parsed.max_attempts)
          const delayMs = Number(parsed.delay_ms)
          if (
            canMutate()
            && Number.isInteger(attempt) && attempt > 0
            && Number.isInteger(maxAttempts) && maxAttempts > attempt
            && Number.isFinite(delayMs) && delayMs >= 0 && delayMs <= 30_000
          ) {
            retryTraceVisible = true
            updateStreaming({
              retryTrace: {
                attempt,
                maxAttempts,
                delayMs,
                category: typeof parsed.category === "string" ? parsed.category : "transient",
              },
            })
          }
          break
        }
        case "tool_call_start": {
          const tc: ToolCall = {
            id: String(parsed.tool_call_id || ""),
            name: String(parsed.tool_name || parsed.name || ""),
            arguments: typeof parsed.arguments === "string" ? parsed.arguments : "",
            status: "running",
          }
          if (!tc.id) break
          clearRetryTrace()
          accToolCalls.push(cloneToolCall(tc))
          if (canMutate()) {
            addStreamToolCall(tc)
          }
          break
        }
        case "tool_call_result": {
          const tcId = String(parsed.tool_call_id || "")
          if (!tcId) break
          const result = typeof parsed.result === "string" ? parsed.result : JSON.stringify(parsed.result ?? "")
          const resolved = findToolCall(accToolCalls, tcId)
          if (resolved) {
            resolved.result = result
            resolved.status = "done"
          }
          if (canMutate()) {
            updateStreamToolCall(tcId, { result, status: "done" })
          }
          break
        }
        case "message_complete": {
          clearRetryTrace()
          sawMessageComplete = true
          terminalMessageId = typeof parsed.message_id === "number" ? parsed.message_id : undefined
          terminalIncomplete = parsed.incomplete === true
          const ownsView = canMutate()
          if (ownsView) flushPendingDeltas()
          if (ownsView && hasStreamOutput(accContent, accThinking, accToolCalls)) {
            const msgData: MessageData = {
              role: "assistant",
              content: accContent,
              thinking: accThinking || undefined,
              tool_calls: accToolCalls.length > 0 ? accToolCalls : undefined,
              response_meta: {
                finish_reason: typeof parsed.finish_reason === "string" ? parsed.finish_reason : undefined,
                usage: parseUsage(parsed.usage),
              },
              runtime: {
                duration_ms: typeof parsed.duration_ms === "number" ? parsed.duration_ms : undefined,
                tokens_per_second: typeof parsed.tokens_per_second === "number" ? parsed.tokens_per_second : undefined,
              },
              metadata: parsed.incomplete === true ? { incomplete: true } : undefined,
            }
            commitStreamingMessage(msgData)
          }
          if (ownsView && isActiveClientStream(stream)) {
            updateStreaming({ status: "syncing", requestId })
            updateMessagesByRequest(requestId, { local_state: "syncing" })
          }
          break
        }
        case "error":
          if (typeof parsed.error === "string" && parsed.error) {
            clearRetryTrace()
            sawTerminalError = true
            const ownsView = canMutate()
            if (ownsView) flushPendingDeltas()
            const errorText = formatErrorPayload(parsed, "请求失败")
            const errorDiagnostic = formatErrorDiagnostic(parsed)
            terminalMessageId = typeof parsed.message_id === "number" ? parsed.message_id : undefined
            terminalError = [errorText, errorDiagnostic].filter(Boolean).join("；")
            const persistenceFailed = parsed.code === "message_persist_failed"
            const hasGeneratedOutput = hasStreamOutput(accContent, accThinking, accToolCalls)
            if (persistenceFailed && hasGeneratedOutput && ownsView) {
              commitStreamingMessage({
                role: "assistant",
                content: accContent,
                thinking: accThinking || undefined,
                tool_calls: accToolCalls.length > 0 ? accToolCalls : undefined,
                metadata: { unsaved: true },
              })
              updateMessagesByRequest(requestId, { local_state: "failed_local", local_error: errorText })
            } else {
              if (typeof parsed.message_id !== "number" && ownsView) {
                const errorMsg: Message = {
                  id: createLocalMessageId(),
                  session_id: sessionId,
                  schema_version: "v2",
                  message_data: {
                    role: "assistant",
                    content: "",
                    metadata: {
                      error: true,
                      error_detail: errorText,
                      error_code: typeof parsed.code === "string" ? parsed.code : undefined,
                      error_diagnostic: errorDiagnostic || undefined,
                    },
                  },
                  role: "assistant",
                  has_tool_calls: false,
                  has_reasoning: false,
                  created_at: new Date().toISOString(),
                  local_state: "failed_local",
                  is_local: true,
                  local_request_id: requestId,
                  local_error: errorText,
                }
                updateMessagesByRequest(requestId, { local_state: "failed_local", local_error: errorText })
                addMessage(errorMsg)
              }
            }
            if (isActiveClientStream(stream)) {
              updateStreaming({
                status: typeof parsed.message_id === "number" ? "syncing" : "failed_local",
                error: errorText,
                requestId,
              })
            }
          }
          break
      }
    }

    await readSSEEvents(response, async (event, data) => {
      if (event !== "ping") {
        await handleEvent(event, data)
      }
    })

    flushPendingDeltas()

    return { sawMessageComplete, sawTerminalError, terminalMessageId, terminalError, terminalIncomplete }
  }, [addMessage, appendStreamContent, appendStreamThinking, addStreamToolCall, commitStreamingMessage, confirmUserMessageForRequest, rollbackStreamAttempt, updateMessagesByRequest, updateStreaming, updateStreamToolCall])

  const runStream = useCallback(async (sessionId: number, request: StreamRequest, requestId: string, onAccepted?: () => void) => {
    const stream = beginStream(sessionId, requestId)
    let accepted = false
    let receivedResponse = false
    let needsReconciliation = false
    let hiddenTerminalError = ""
    updateStreaming({ status: "sending", requestId, error: null, content: "", thinking: "", toolCalls: [], segments: [], replayGap: false, retryTrace: null })

    try {
      const { url, authToken, ...requestInit } = request
      const res = await fetch(url, { ...requestInit, signal: stream.controller.signal })
      receivedResponse = true
      if (!res.ok) throw await createStreamHTTPError(res, authToken)
      if (!res.body) throw new Error("流式响应为空")

      accepted = true
      onAccepted?.()
      if (isCurrentClientStream(stream)) {
        updateStreaming({ status: "streaming" })
        updateMessagesByRequest(requestId, { local_state: "streaming" })
      }

      const result = await consumeSSE(sessionId, res, requestId, stream)

      if (result.sawMessageComplete || result.sawTerminalError) {
        if (isCurrentClientStream(stream)) {
          const synced = await syncSessionMessages(sessionId, requestId)
          if (
            synced
            && result.terminalMessageId
            && !useChatStore.getState().messages.some((message) => message.id === result.terminalMessageId)
          ) {
            hiddenTerminalError = result.terminalError
              || (result.terminalIncomplete ? "本次重试未完成，已保留原回答" : "本次重试未能更新回答，已保留原回答")
          }
        }
      } else if (!result.sawTerminalError) {
        if (isCurrentClientStream(stream)) {
          needsReconciliation = true
          updateStreaming({ status: "recovering", error: "连接暂时中断，正在确认回答", requestId })
          void scheduleRunReconciliation(sessionId, requestId)
        }
      }
    } catch (err) {
      if (!accepted && !receivedResponse && (err as Error).name !== "AbortError" && isCurrentClientStream(stream)) {
        const run = await waitForRunAppearance(sessionId, requestId, stream.controller.signal)
        if (!run || !isCurrentClientStream(stream)) throw err
        onAccepted?.()
        updateMessagesByRequest(requestId, { local_state: "streaming" })
        needsReconciliation = true
        updateStreaming({ status: "recovering", error: "连接暂时中断，正在确认回答", requestId })
        void scheduleRunReconciliation(sessionId, requestId)
        return
      }
      if (!accepted) throw err
      if ((err as Error).name !== "AbortError" && isCurrentClientStream(stream)) {
        console.error("SSE error:", err)
        needsReconciliation = true
        updateStreaming({ status: "recovering", error: "连接暂时中断，正在确认回答", requestId })
        void scheduleRunReconciliation(sessionId, requestId)
      }
    } finally {
      useChatStore.getState().finishCompaction(sessionId, requestId)
      if (activeStream === stream) {
        if (isCurrentClientStream(stream) && useChatStore.getState().streaming.status === "syncing") {
          updateMessagesByRequest(requestId, { local_state: "persisted", is_local: false, local_request_id: undefined, local_error: undefined })
        }
        if (!needsReconciliation && useChatStore.getState().streaming.requestId === requestId) resetStreaming()
        activeStream = null
      }
    }
    if (hiddenTerminalError) throw new Error(hiddenTerminalError)
  }, [consumeSSE, resetStreaming, scheduleRunReconciliation, syncSessionMessages, updateMessagesByRequest, updateStreaming])

  const startCompactionInternal = useCallback(async (sessionId: number, source: "auto" | "manual" = "manual", thinkingEffort?: string, notice = "", preserveMessageId?: number) => {
    if (useChatStore.getState().compactionOwners[sessionId]) return
    const clientRunId = safeUUID()
    useChatStore.getState().beginCompaction(
      sessionId,
      clientRunId,
      notice || (source === "auto" ? "发送前需要先整理上下文" : "")
    )
    try {
      const token = localStorage.getItem("token")
      const res = await fetch(compactSessionUrl(sessionId, clientRunId, source, thinkingEffort, preserveMessageId), {
        method: "POST",
        headers: { Authorization: `Bearer ${token}` },
      })
      if (!res.ok) throw new Error(await readStreamHTTPError(res, token))
      if (!res.body) throw new Error("流式响应为空")
      const outcome = await consumeCompactionSSE(res)
      if (outcome === "complete") {
        await syncSessionMessages(sessionId)
      }
      return outcome
    } finally {
      useChatStore.getState().finishCompaction(sessionId, clientRunId)
    }
  }, [syncSessionMessages])

  const sendMessage = useCallback(async (sessionId: number, content: string, attachments?: AttachmentMeta[], options?: SendMessageOptions) => {
    const requestId = safeUUID()
    const sessionGeneration = useChatStore.getState().activeSessionGeneration
    const isCurrentSessionView = () => {
      const state = useChatStore.getState()
      return state.activeSessionId === sessionId && state.activeSessionGeneration === sessionGeneration
    }
    const ensureCurrentSession = () => {
      if (!isCurrentSessionView()) throw new Error("会话已切换，消息未发送")
    }
    ensureCurrentSession()
    if (useChatStore.getState().hasNewerMessages) {
      const loaded = await useChatStore.getState().loadMessages(sessionId)
      ensureCurrentSession()
      if (!loaded || useChatStore.getState().hasNewerMessages) {
        throw new Error("当前消息窗口已变化，请重试发送")
      }
    }
    const attachmentIds = attachments && attachments.length > 0 ? attachments.map((a) => a.file_id) : undefined
    const preflight = await preflightSessionMessage(sessionId, {
      content,
      client_run_id: requestId,
      ...(attachmentIds ? { attachments: attachmentIds } : {}),
      ...(options?.thinkingEffort ? { thinking_effort: options.thinkingEffort } : {}),
    })
    ensureCurrentSession()
    if (preflight.needs_compaction) {
      const outcome = await startCompactionInternal(
        sessionId,
        "auto",
        options?.thinkingEffort,
        preflight.message || "发送前需要先整理上下文"
      )
      if (outcome !== "complete" && outcome !== "skip") {
        throw new Error("压缩失败，请联系管理员")
      }
      ensureCurrentSession()
    }

    const token = localStorage.getItem("token")
    const request: StreamRequest = {
      url: `/api/v1/sessions/${sessionId}/messages/stream`,
      authToken: token,
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${token}`,
      },
      body: JSON.stringify({
        content,
        client_run_id: requestId,
        ...(attachments && attachments.length > 0 ? { attachments: attachments.map((a) => a.file_id) } : {}),
        ...(options?.thinkingEffort ? { thinking_effort: options.thinkingEffort } : {}),
      }),
    }
    let accepted = false
    const acceptMessage = () => {
      if (accepted) return
      accepted = true
      if (isCurrentSessionView()) {
        const userMsg: Message = {
          id: createLocalMessageId(),
          session_id: sessionId,
          schema_version: "v2",
          message_data: {
            role: "user",
            content,
            ...(attachments && attachments.length > 0 ? { attachments } : {}),
            ...(options?.thinkingEffort ? { metadata: { thinking_effort: options.thinkingEffort } } : {}),
          },
          role: "user",
          has_tool_calls: false,
          has_reasoning: false,
          created_at: new Date().toISOString(),
          local_state: "pending",
          is_local: true,
          local_request_id: requestId,
        }
        addMessage(userMsg)
      }
      options?.onAccepted?.()
    }

    try {
      await runStream(sessionId, request, requestId, acceptMessage)
    } catch (err) {
      if (!isCompactionRequired(err)) throw err
      ensureCurrentSession()

      const outcome = await startCompactionInternal(sessionId, "auto", options?.thinkingEffort, "发送前需要先整理上下文")
      if (outcome !== "complete" && outcome !== "skip") {
        throw new Error("压缩失败，请联系管理员", { cause: err })
      }
      ensureCurrentSession()
      await runStream(sessionId, request, requestId, acceptMessage)
    }
  }, [addMessage, runStream, startCompactionInternal])

  const retryMessage = useCallback(async (sessionId: number, messageId: number) => {
    const requestId = safeUUID()
    const sessionGeneration = useChatStore.getState().activeSessionGeneration
    let synchronized: boolean
    if (useChatStore.getState().hasNewerMessages) {
      synchronized = await useChatStore.getState().loadMessages(sessionId)
    } else {
      synchronized = await syncSessionMessages(sessionId)
    }
    const synchronizedState = useChatStore.getState()
    if (
      !synchronized
      || synchronizedState.activeSessionId !== sessionId
      || synchronizedState.activeSessionGeneration !== sessionGeneration
      || synchronizedState.hasNewerMessages
    ) throw new Error("会话或消息窗口已变化，请重试")
    const currentMessages = useChatStore.getState().messages
    // 重试目标为最后一条可见消息，无论 user 还是 assistant：助手回复落库失败时，
    // 最后一条恰好是 user 消息，仍需可重试。后端 PrepareRetry 已支持两种 role。
    const visibleIds = currentMessages
      .filter((msg) => !msg.is_local && msg.role !== "tool" && msg.message_data.role !== "tool")
      .map((msg) => msg.id)
    if (!visibleIds.length || visibleIds[visibleIds.length - 1] !== messageId) {
      throw new Error("请先等待当前会话同步完成，再重试最后一条消息")
    }

    const token = localStorage.getItem("token")
    const request: StreamRequest = {
      url: `/api/v1/sessions/${sessionId}/messages/${messageId}/retry?client_run_id=${encodeURIComponent(requestId)}`,
      authToken: token,
      method: "POST",
      headers: {
        Authorization: `Bearer ${token}`,
      },
    }
    try {
      await runStream(sessionId, request, requestId)
    } catch (err) {
      if (!isCompactionRequired(err)) throw err
      if (!isCurrentSession(sessionId)) throw new Error("会话已切换，消息未发送", { cause: err })
      const preserveMessageId = err.preserveMessageId
      if (!preserveMessageId || preserveMessageId <= 0) {
        throw new Error("重试上下文已变化，请刷新后重试", { cause: err })
      }
      const outcome = await startCompactionInternal(
        sessionId,
        "auto",
        undefined,
        err.message || "重新生成前需要先整理上下文",
        preserveMessageId
      )
      if (outcome !== "complete") {
        throw new Error("当前上下文无法再缩短，无法重新生成。请新建会话或缩短本轮内容", { cause: err })
      }
      if (!isCurrentSession(sessionId)) throw new Error("会话已切换，消息未发送", { cause: err })
      await runStream(sessionId, request, requestId)
    }
  }, [isCurrentSession, runStream, startCompactionInternal, syncSessionMessages])

  const editRetryMessage = useCallback(async (sessionId: number, messageId: number, content: string) => {
    const requestId = safeUUID()
    const sessionGeneration = useChatStore.getState().activeSessionGeneration
    let synchronized: boolean
    if (useChatStore.getState().hasNewerMessages) {
      synchronized = await useChatStore.getState().loadMessages(sessionId)
    } else {
      synchronized = await syncSessionMessages(sessionId)
    }
    const synchronizedState = useChatStore.getState()
    if (
      !synchronized
      || synchronizedState.activeSessionId !== sessionId
      || synchronizedState.activeSessionGeneration !== sessionGeneration
      || synchronizedState.hasNewerMessages
    ) throw new Error("会话或消息窗口已变化，请重试")
    if (editableTailUserMessageId(useChatStore.getState().messages) !== messageId) {
      throw new Error("这条消息已不能修改，请刷新后重试")
    }

    const token = localStorage.getItem("token")
    const request: StreamRequest = {
      url: `/api/v1/sessions/${sessionId}/messages/${messageId}/edit-retry`,
      authToken: token,
      method: "POST",
      headers: {
        Authorization: `Bearer ${token}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ content, client_run_id: requestId }),
    }
    const acceptEdit = () => {
      beginEditRetry(messageId, content, requestId)
    }

    try {
      await runStream(sessionId, request, requestId, acceptEdit)
    } catch (err) {
      if (!isCompactionRequired(err)) throw err
      if (!isCurrentSession(sessionId)) throw new Error("会话已切换，消息未发送", { cause: err })
      const preserveMessageId = err.preserveMessageId
      if (!preserveMessageId || preserveMessageId !== messageId) {
        throw new Error("编辑目标已变化，请刷新后重试", { cause: err })
      }
      const outcome = await startCompactionInternal(
        sessionId,
        "auto",
        undefined,
        err.message || "重新生成前需要先整理上下文",
        preserveMessageId
      )
      if (outcome !== "complete") {
        throw new Error("当前上下文无法再缩短，无法重新生成。请新建会话或缩短本轮内容", { cause: err })
      }
      if (!isCurrentSession(sessionId)) throw new Error("会话已切换，消息未发送", { cause: err })
      await runStream(sessionId, request, requestId, acceptEdit)
    }
  }, [beginEditRetry, isCurrentSession, runStream, startCompactionInternal, syncSessionMessages])

  // 进入/切回会话时，若后端仍有运行中的 run（如刷新页面、网络抖动断流），
  // 用快照恢复流式视图并从游标之后续传，无需用户重发。
  const resumeActiveRun = useCallback(async (sessionId: number) => {
    const currentState = useChatStore.getState()
    const recoveringRunID = currentState.streaming.status === "recovering"
      ? currentState.streaming.requestId
      : null
    if (activeStream || (currentState.streaming.status !== "idle" && !recoveringRunID)) return
    const { run } = await getActiveRun(sessionId)
    if (!isCurrentSession(sessionId)) return
    if (!run || run.status !== "running") return
    if (recoveringRunID && recoveringRunID !== run.run_id) return

    // Memory maintenance has its own dialog-level observer. It must never be
    // interpreted as a chat stream; closing the dialog simply leaves RunHub to
    // finish and the next dialog open reloads the durable memory state.
    if (run.kind === "memory_maintenance") return

    // 压缩 run 走独立分支：不进 streaming buffer，仅恢复“正在压缩”态，完成时刷新。
    if (run.kind === "compaction") {
      if (useChatStore.getState().compactionOwners[sessionId]) return
      useChatStore.getState().beginCompaction(sessionId, run.run_id, "正在整理会话上下文")
      try {
        const token = localStorage.getItem("token")
        const res = await fetch(`/api/v1/sessions/${sessionId}/runs/${run.run_id}/resume?cursor=${run.cursor}`, {
          method: "GET",
          headers: { Authorization: `Bearer ${token}` },
        })
        if (!res.ok) {
          if (res.status === 401) handleAuthExpired(token)
          return
        }
        if (!res.body) return
        const outcome = await consumeCompactionSSE(res)
        if (outcome === "complete") await syncSessionMessages(sessionId)
      } catch (err) {
        console.error("resume compaction failed", err)
      } finally {
        useChatStore.getState().finishCompaction(sessionId, run.run_id)
      }
      return
    }

    // 拉取快照是异步的，期间用户可能已发起 send/retry。这里同步复检：若已有活跃流或
    // 状态不再 idle，放弃恢复——恢复永远让位于用户主动发起的流，绝不抢占。
    let latestState = useChatStore.getState()
    if (
      activeStream
      || (latestState.streaming.status !== "idle" && latestState.streaming.requestId !== recoveringRunID)
    ) return

    // A second tab can finish its initial history request just before the first tab
    // durably accepts this user turn. The resume stream starts at the current cursor,
    // so it will not replay message_start to repair that race. Reconcile the accepted
    // user message before exposing the assistant stream to keep the visible turn ordered.
    if (
      run.user_message_id > 0
      && !latestState.messages.some((message) => message.id === run.user_message_id && message.role === "user" && !message.is_local)
    ) {
      const synced = await syncSessionMessages(sessionId)
      if (!synced || !isCurrentSession(sessionId)) return
      latestState = useChatStore.getState()
      if (
        activeStream
        || (latestState.streaming.status !== "idle" && latestState.streaming.requestId !== recoveringRunID)
      ) return
    }

    const requestId = run.run_id
    const snapshotIsPartial = run.output_truncated === true
    const hasCurrentOutput = recoveringRunID === requestId
      && hasStreamOutput(latestState.streaming.content, latestState.streaming.thinking, latestState.streaming.toolCalls)
    const preserveCurrent = hasCurrentOutput
      && snapshotIsPartial
    const seedContent = preserveCurrent ? latestState.streaming.content : (snapshotIsPartial ? "" : run.content || "")
    const seedThinking = preserveCurrent ? latestState.streaming.thinking : (snapshotIsPartial ? "" : run.thinking || "")
    const seedToolCalls = preserveCurrent
      ? latestState.streaming.toolCalls.map(cloneToolCall)
      : (snapshotIsPartial ? [] : snapshotToolCalls(run))
    const seedSegments = preserveCurrent
      ? latestState.streaming.segments.map((segment) => ({
        ...segment,
        tool_calls: segment.tool_calls?.map(cloneToolCall),
      }))
      : buildSeedSegments(snapshotIsPartial ? { ...run, content: "", thinking: "", segments: undefined } : run, seedToolCalls)

    if (!isCurrentSession(sessionId)) return
    const stream = beginStream(sessionId, requestId)
    let needsReconciliation = false
    updateStreaming({
      status: "streaming",
      requestId,
      error: null,
      replayGap: snapshotIsPartial && !preserveCurrent,
      retryTrace: null,
      content: seedContent,
      thinking: seedThinking,
      toolCalls: seedToolCalls,
      segments: seedSegments,
    })

    try {
      const token = localStorage.getItem("token")
      const resumeCursor = snapshotIsPartial && !preserveCurrent ? (run.replay_from || 0) : run.cursor
      const res = await fetch(`/api/v1/sessions/${sessionId}/runs/${run.run_id}/resume?cursor=${resumeCursor}`, {
        method: "GET",
        headers: { Authorization: `Bearer ${token}` },
        signal: stream.controller.signal,
      })
      if (!res.ok) throw new Error(await readStreamHTTPError(res, token))
      if (!res.body) throw new Error("流式响应为空")

      const result = await consumeSSE(sessionId, res, requestId, stream, {
        content: seedContent,
        thinking: seedThinking,
        toolCalls: seedToolCalls,
      }, (snapshot) => {
        const visibleStreaming = useChatStore.getState().streaming
        const keepVisiblePrefix = snapshot.output_truncated === true
          && isCurrentClientStream(stream)
          && hasStreamOutput(visibleStreaming.content, visibleStreaming.thinking, visibleStreaming.toolCalls)
        const next: StreamSeed = keepVisiblePrefix
          ? {
              content: visibleStreaming.content,
              thinking: visibleStreaming.thinking,
              toolCalls: visibleStreaming.toolCalls.map(cloneToolCall),
            }
          : snapshot.output_truncated === true
            ? { content: "", thinking: "", toolCalls: [] }
            : {
                content: snapshot.content || "",
                thinking: snapshot.thinking || "",
                toolCalls: snapshotToolCalls(snapshot),
              }
        if (isCurrentClientStream(stream)) {
          updateStreaming({
            status: "recovering",
            requestId,
            replayGap: true,
            error: "较早输出正在从服务端恢复",
            content: next.content,
            thinking: next.thinking,
            toolCalls: next.toolCalls,
            segments: keepVisiblePrefix
              ? visibleStreaming.segments.map((segment) => ({
                  ...segment,
                  tool_calls: segment.tool_calls?.map(cloneToolCall),
                }))
              : buildSeedSegments(snapshot.output_truncated === true ? { ...snapshot, content: "", thinking: "", segments: undefined } : snapshot, next.toolCalls),
          })
        }
        return next
      })

      if (result.sawMessageComplete && isCurrentClientStream(stream)) {
        updateMessagesByRequest(requestId, { local_state: "persisted", is_local: false, local_request_id: undefined, local_error: undefined })
      }
      if ((result.sawMessageComplete || result.sawTerminalError) && isCurrentClientStream(stream)) {
        await syncSessionMessages(sessionId, requestId)
      } else if (isCurrentClientStream(stream)) {
        needsReconciliation = true
        updateStreaming({ status: "recovering", requestId, error: "连接暂时中断，正在确认回答" })
        void scheduleRunReconciliation(sessionId, requestId)
      }
    } catch (err) {
      if ((err as Error).name !== "AbortError" && isCurrentClientStream(stream)) {
        needsReconciliation = true
        updateStreaming({ status: "recovering", requestId, error: "连接暂时中断，正在确认回答" })
        void scheduleRunReconciliation(sessionId, requestId)
      }
    } finally {
      if (activeStream === stream) {
        if (!needsReconciliation && useChatStore.getState().streaming.requestId === requestId) resetStreaming()
        activeStream = null
      }
    }
  }, [consumeSSE, isCurrentSession, resetStreaming, scheduleRunReconciliation, syncSessionMessages, updateMessagesByRequest, updateStreaming])

  useEffect(() => {
    resumeActiveRunRef.current = resumeActiveRun
  }, [resumeActiveRun])

  const startCompaction = useCallback((sessionId: number, thinkingEffort?: string) => {
    return startCompactionInternal(sessionId, "manual", thinkingEffort)
  }, [startCompactionInternal])

  // 停止生成：后端取消语义是「保留已生成的部分内容、落库、再把 run 记为 canceled」，
  // 且**先落库再改状态**。所以本地不能只 abort+reset（那样用户会以为内容丢了，要手动刷新
  // 才看得到后端保留的 partial）。这里 abort 后进入 syncing 态保留已显示内容，有界轮询
  // 等到 run 离开 running（⇒部分内容已落库），再从 DB 同步真实结果，最后收尾。
  const abort = useCallback(async () => {
    const currentState = useChatStore.getState()
    const { streaming } = currentState
    const requestId = streaming.requestId
    const sessionId = currentState.activeSessionId
    const sessionGeneration = currentState.activeSessionGeneration
    let needsReconciliation = false
    if (!requestId || !sessionId) {
      resetStreaming()
      return
    }
    if (streaming.status === "syncing") return

    const stream = activeStream
    if (stream?.sessionId === sessionId && stream.requestId === requestId) {
      stream.controller.abort()
      if (activeStream === stream) activeStream = null
    }
    const ownsStreaming = () => {
      const state = useChatStore.getState()
      return state.activeSessionId === sessionId
        && state.activeSessionGeneration === sessionGeneration
        && state.streaming.requestId === requestId
    }

    if (hasStreamOutput(streaming.content, streaming.thinking, streaming.toolCalls)) {
      commitStreamingMessage({
        role: "assistant",
        content: streaming.content,
        thinking: streaming.thinking || undefined,
        tool_calls: streaming.toolCalls.length > 0 ? streaming.toolCalls : undefined,
        metadata: { incomplete: true },
      })
    }

    // 进入同步态，保留已显示的 partial（不立即标 failed_local）。
    updateStreaming({ status: "syncing", requestId })

    try {
      void cancelRun(sessionId, requestId).catch(() => {})

      const terminal = await waitForRunSettlement(sessionId, requestId)
      if (terminal?.terminal_message_id) {
        await syncSessionMessages(sessionId, requestId)
        cancelReconciliation(sessionId)
      } else if (terminal) {
        if (ownsStreaming()) {
          updateMessagesByRequest(requestId, {
            local_state: "failed_local",
            local_error: terminal.error || "回复未保存到服务端；刷新前请复制或重试",
          })
        }
        if (terminal.user_message_id) {
          await syncSessionMessages(sessionId, requestId)
        }
        cancelReconciliation(sessionId)
      } else if (ownsStreaming()) {
        needsReconciliation = true
        updateStreaming({ status: "recovering", requestId, error: "停止请求仍在确认" })
        void scheduleRunReconciliation(sessionId, requestId)
      }
    } catch {
      if (ownsStreaming()) {
        needsReconciliation = true
        updateStreaming({ status: "recovering", requestId, error: "停止请求仍在确认" })
        void scheduleRunReconciliation(sessionId, requestId)
      }
    } finally {
      if (!needsReconciliation && ownsStreaming()) {
        resetStreaming()
      }
    }
  }, [commitStreamingMessage, resetStreaming, scheduleRunReconciliation, syncSessionMessages, updateMessagesByRequest, updateStreaming])

  const disconnectActiveStream = useCallback((sessionId?: number) => {
    if (sessionId !== undefined) cancelReconciliation(sessionId)
    const stream = activeStream
    const state = useChatStore.getState()
    const ownsCurrentView = sessionId === undefined || state.activeSessionId === sessionId
    if (!stream || (sessionId !== undefined && stream.sessionId !== sessionId)) {
      if (ownsCurrentView) resetStreaming()
      return
    }
    stream.controller.abort()
    if (activeStream === stream) activeStream = null
    if (ownsCurrentView && useChatStore.getState().streaming.requestId === stream.requestId) resetStreaming()
  }, [resetStreaming])

  return { sendMessage, retryMessage, editRetryMessage, resumeActiveRun, startCompaction, abort, disconnectActiveStream }
}

function snapshotToolCalls(run: ActiveRunSnapshot): ToolCall[] {
  return (run.tool_calls || []).map(cloneToolCall)
}

// 新版 RunHub 快照保留原始事件顺序；旧运行记录仍使用扁平字段兜底。
function buildSeedSegments(run: ActiveRunSnapshot, toolCalls: ToolCall[]): StreamingSegment[] {
  if (run.segments?.length) {
    return run.segments.map((segment) => ({
      ...segment,
      tool_calls: segment.tool_calls?.map(cloneToolCall),
    }))
  }
  const segments: StreamingSegment[] = []
  if (toolCalls.length) segments.push({ type: "tool", tool_calls: toolCalls })
  if ((run.thinking || "").trim()) segments.push({ type: "content", thinking: run.thinking })
  if ((run.content || "").trim()) segments.push({ type: "content", content: run.content })
  return segments
}

function findToolCall(toolCalls: ToolCall[], id: string, seen = new Set<ToolCall>()): ToolCall | null {
  for (const toolCall of toolCalls) {
    if (seen.has(toolCall)) continue
    seen.add(toolCall)
    if (toolCall.id === id) return toolCall
    if (toolCall.children?.length) {
      const nested = findToolCall(toolCall.children, id, seen)
      if (nested) return nested
    }
  }
  return null
}

function hasStreamOutput(content: string, thinking: string, toolCalls: ToolCall[]) {
  return content.trim().length > 0 || thinking.trim().length > 0 || toolCalls.length > 0
}
