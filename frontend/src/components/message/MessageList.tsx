import { useRef, useEffect, useState, useCallback, useLayoutEffect, useMemo } from "react"
import type { Message } from "@/types"
import { useChatStore } from "@/stores/chat"
import { useUIStore } from "@/stores/ui"
import { UserMessage } from "./UserMessage"
import { AssistantMessage } from "./AssistantMessage"
import { StreamingMessage } from "./StreamingMessage"
import { editableTailUserMessageId, isCompactionSummary, compactionKind, messageRunId } from "@/lib/chatMessages"
import { undoCompaction } from "@/api/sessions"
import { MarkdownContent } from "./MarkdownContent"
import { isStreamingDisplayActive } from "@/lib/streamingStatus"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogFooter,
  DialogTitle,
  DialogDescription,
  DialogClose,
} from "@/components/ui/dialog"
import { ChevronDown, ChevronRight, Loader2, Undo2 } from "lucide-react"
import { prefersReducedMotion } from "@/lib/motionPreference"
import { buildConversationTurns, conversationTurnMarkerRange, conversationTurnRailMode, type ConversationTurn } from "@/lib/conversationTurns"
import { useSearchParams } from "react-router"

const INITIAL_BOTTOM_LOCK_MS = 1400

export function MessageList() {
  const [searchParams, setSearchParams] = useSearchParams()
  const activeSessionId = useChatStore((s) => s.activeSessionId)
  const messageWindowGeneration = useChatStore((s) => s.messageWindowGeneration)
  const messages = useChatStore((s) => s.messages)
  const streamingStatus = useChatStore((s) => s.streaming.status)
  const streamingRequestId = useChatStore((s) => s.streaming.requestId)
  const streamingContentLength = useChatStore((s) => s.streaming.content.length)
  const streamingThinkingLength = useChatStore((s) => s.streaming.thinking.length)
  const streamingToolCount = useChatStore((s) => s.streaming.toolCalls.length)
  const clearReasoningState = useUIStore((s) => s.clearReasoningState)
  const isLoadingOlder = useChatStore((s) => s.isLoadingOlder)
  const isLoadingNewer = useChatStore((s) => s.isLoadingNewer)
  const hasMoreMessages = useChatStore((s) => s.hasMoreMessages)
  const hasNewerMessages = useChatStore((s) => s.hasNewerMessages)
  const conversationTurnIndex = useChatStore((s) => s.conversationTurns)
  const loadOlderMessages = useChatStore((s) => s.loadOlderMessages)
  const loadNewerMessages = useChatStore((s) => s.loadNewerMessages)
  const loadMessageWindowAround = useChatStore((s) => s.loadMessageWindowAround)
  const trimLoadedMessageWindow = useChatStore((s) => s.trimLoadedMessageWindow)
  const compactionOwner = useChatStore((s) => activeSessionId ? s.compactionOwners[activeSessionId] : undefined)
  const loadMessages = useChatStore((s) => s.loadMessages)
  const [undoing, setUndoing] = useState(false)
  const scrollRef = useRef<HTMLDivElement>(null)
  const listRef = useRef<HTMLDivElement>(null)
  const wasNearBottomRef = useRef(true)
  const previousMessageCountRef = useRef(0)
  const initialScrolledSessionRef = useRef<number | null>(null)
  const bottomLockSessionRef = useRef<number | null>(null)
  const bottomLockUntilRef = useRef(0)
  const bottomLockTimerRef = useRef(0)
  const userCancelledBottomLockRef = useRef(false)
  const userPausedAutoFollowRef = useRef(false)
  const suppressOlderLoadUntilRef = useRef(0)
  const rafRef = useRef(0)
  const turnRafRef = useRef(0)
  const trimRafRef = useRef(0)
  const turnAnchorsRef = useRef(new Map<number, HTMLDivElement>())
  // 向上加载时锚定视口：prepend 前记录距底距离，prepend 后据此恢复 scrollTop。
  const pendingAnchorRef = useRef<{ id: number; top: number; until: number; windowGeneration: number } | null>(null)
  const pendingAnchorTimerRef = useRef(0)
  const windowSwitchingRef = useRef(false)
  const windowSwitchTokenRef = useRef(0)
  const windowSwitchReleaseTimerRef = useRef(0)
  const [showBack, setShowBack] = useState(false)
  const [activeTurnId, setActiveTurnId] = useState<number | null>(null)
  const [windowSwitching, setWindowSwitching] = useState(false)
  const visibleMessages = useMemo(
    () => messages.filter((msg) => msg.role !== "tool" && msg.message_data.role !== "tool"),
    [messages]
  )
  const loadedTurns = useMemo(() => buildConversationTurns(visibleMessages), [visibleMessages])
  const turns = useMemo(() => buildConversationTurns(visibleMessages, conversationTurnIndex), [conversationTurnIndex, visibleMessages])
  const displayedActiveTurnId = activeTurnId && turns.some((turn) => turn.id === activeTurnId)
    ? activeTurnId
    : turns.at(-1)?.id || null
  // 本地未保存的助手回复不能遮住其前一条已确认 user turn 的重试入口。
  const lastVisible = [...visibleMessages].reverse().find((message) => !message.is_local)
  const lastVisibleId = hasNewerMessages ? undefined : lastVisible?.id
  const lastAssistantId = !hasNewerMessages && lastVisible?.role === "assistant" ? lastVisible.id : undefined
  // 最后一条压缩摘要：仅它可撤销（撤销更早的会破坏后续检查点边界）。
  const lastSummaryId = useMemo(() => {
    for (let i = visibleMessages.length - 1; i >= 0; i--) {
      if (isCompactionSummary(visibleMessages[i])) return visibleMessages[i].id
    }
    return undefined
  }, [visibleMessages])
  const isStreaming = isStreamingDisplayActive(streamingStatus)
  const streamingHasOutput = streamingContentLength > 0 || streamingThinkingLength > 0 || streamingToolCount > 0
  const editLifecycleReady = streamingStatus === "idle"
    || streamingStatus === "failed_local"
    || streamingStatus === "streaming"
    || streamingStatus === "recovering"
  const editableUserMessageId = !hasNewerMessages && editLifecycleReady && !streamingHasOutput
    ? editableTailUserMessageId(visibleMessages)
    : null
  const durableStreamMessageExists = Boolean(
    isStreaming
      && streamingRequestId
      && visibleMessages.some((message) => (
        !message.is_local
        && message.role === "assistant"
        && messageRunId(message) === streamingRequestId
      )),
  )
  const compacting = Boolean(compactionOwner)
  const compactionNotice = compactionOwner?.notice ?? ""

  // 合并到下一帧执行，避免每个 token delta 触发一次同步重排。
  const scrollToBottom = useCallback((smooth = false) => {
    const container = scrollRef.current
    if (!container) return
    cancelAnimationFrame(rafRef.current)
    rafRef.current = requestAnimationFrame(() => {
      container.scrollTo({ top: container.scrollHeight, behavior: smooth && !prefersReducedMotion() ? "smooth" : "auto" })
    })
  }, [])

  const clearInitialBottomLock = useCallback(() => {
    bottomLockSessionRef.current = null
    userCancelledBottomLockRef.current = false
    window.clearTimeout(bottomLockTimerRef.current)
  }, [])

  const clearPendingAnchor = useCallback(() => {
    window.clearTimeout(pendingAnchorTimerRef.current)
    pendingAnchorRef.current = null
  }, [])

  const capturePendingAnchor = useCallback((element: HTMLElement) => {
    pendingAnchorRef.current = {
      id: Number(element.dataset.messageId),
      top: element.getBoundingClientRect().top,
      until: Date.now() + 1500,
      windowGeneration: messageWindowGeneration,
    }
    window.clearTimeout(pendingAnchorTimerRef.current)
    pendingAnchorTimerRef.current = window.setTimeout(clearPendingAnchor, 1500)
  }, [clearPendingAnchor, messageWindowGeneration])

  const keepInitialBottomLocked = useCallback(() => {
    const el = scrollRef.current
    if (!el || !activeSessionId) return
    if (bottomLockSessionRef.current !== activeSessionId) return
    if (pendingAnchorRef.current != null || userCancelledBottomLockRef.current || Date.now() > bottomLockUntilRef.current) {
      clearInitialBottomLock()
      return
    }
    cancelAnimationFrame(rafRef.current)
    rafRef.current = requestAnimationFrame(() => {
      const current = scrollRef.current
      if (!current || bottomLockSessionRef.current !== activeSessionId || userCancelledBottomLockRef.current) return
      current.scrollTop = current.scrollHeight
      wasNearBottomRef.current = true
      userPausedAutoFollowRef.current = false
      setShowBack(false)
    })
  }, [activeSessionId, clearInitialBottomLock])

  const triggerLoadOlder = useCallback(async () => {
    const el = scrollRef.current
    if (!el || pendingAnchorRef.current || windowSwitchingRef.current) return
    const anchors = [...el.querySelectorAll<HTMLElement>("[data-message-id]")]
    const firstVisible = anchors.find((element) => element.getBoundingClientRect().bottom >= el.getBoundingClientRect().top)
    if (!firstVisible) return
    capturePendingAnchor(firstVisible)
    const added = await loadOlderMessages()
    if (added === 0) clearPendingAnchor()
  }, [capturePendingAnchor, clearPendingAnchor, loadOlderMessages])

  const enforceMessageWindowHeight = useCallback(() => {
    const container = scrollRef.current
    if (!container || windowSwitchingRef.current || loadedTurns.length <= 16 || container.clientHeight <= 0) return
    const maxHeight = container.clientHeight * 28
    if (container.scrollHeight <= maxHeight) return
    const limit = Math.max(16, Math.min(72, Math.floor(loadedTurns.length * maxHeight / container.scrollHeight)))
    if (limit >= loadedTurns.length) return
    const maxScroll = Math.max(0, container.scrollHeight - container.clientHeight)
    const keep = container.scrollTop <= maxScroll / 2 ? "start" : "end"
    if (keep === "end") {
      const firstVisible = [...container.querySelectorAll<HTMLElement>("[data-message-id]")]
        .find((element) => element.getBoundingClientRect().bottom >= container.getBoundingClientRect().top)
      if (firstVisible) capturePendingAnchor(firstVisible)
    }
    trimLoadedMessageWindow(limit, keep)
  }, [capturePendingAnchor, loadedTurns.length, trimLoadedMessageWindow])

  // 撤销最近一次压缩：调用后端恢复历史，再重新拉取完整消息列表。
  const handleUndoCompaction = useCallback(async () => {
    if (!activeSessionId || undoing) return
    setUndoing(true)
    try {
      await undoCompaction(activeSessionId)
      await loadMessages(activeSessionId)
    } catch (e) {
      console.error("undo compaction failed", e)
    } finally {
      setUndoing(false)
    }
  }, [activeSessionId, undoing, loadMessages])

  useLayoutEffect(() => {
    const el = scrollRef.current
    const pending = pendingAnchorRef.current
    if (!el || !pending) return
    if (pending.windowGeneration !== messageWindowGeneration) {
      clearPendingAnchor()
      return
    }
    const anchor = el.querySelector<HTMLElement>(`[data-message-id="${pending.id}"]`)
    if (!anchor) return
    el.scrollTop += anchor.getBoundingClientRect().top - pending.top
  }, [clearPendingAnchor, messageWindowGeneration, messages.length])

  useEffect(() => {
    clearPendingAnchor()
  }, [clearPendingAnchor, messageWindowGeneration])

  useEffect(() => {
    const list = listRef.current
    const container = scrollRef.current
    if (!list || !container || typeof ResizeObserver === "undefined") return
    const observer = new ResizeObserver(() => {
      const pending = pendingAnchorRef.current
      if (pending && (pending.windowGeneration !== messageWindowGeneration || Date.now() > pending.until)) {
        clearPendingAnchor()
      } else if (pending) {
        const anchor = container.querySelector<HTMLElement>(`[data-message-id="${pending.id}"]`)
        if (anchor) container.scrollTop += anchor.getBoundingClientRect().top - pending.top
      }
      cancelAnimationFrame(trimRafRef.current)
      trimRafRef.current = requestAnimationFrame(enforceMessageWindowHeight)
    })
    observer.observe(list)
    trimRafRef.current = requestAnimationFrame(enforceMessageWindowHeight)
    return () => {
      cancelAnimationFrame(trimRafRef.current)
      observer.disconnect()
    }
  }, [clearPendingAnchor, enforceMessageWindowHeight, messageWindowGeneration])

  useLayoutEffect(() => {
    initialScrolledSessionRef.current = null
    clearInitialBottomLock()
  }, [activeSessionId, clearInitialBottomLock])

  useLayoutEffect(() => {
    const el = scrollRef.current
    if (!el || !activeSessionId || messages.length === 0 || pendingAnchorRef.current != null) return
    if (initialScrolledSessionRef.current === activeSessionId) return
    cancelAnimationFrame(rafRef.current)
    el.scrollTop = el.scrollHeight
    previousMessageCountRef.current = messages.length
    wasNearBottomRef.current = true
    initialScrolledSessionRef.current = activeSessionId
    bottomLockSessionRef.current = activeSessionId
    bottomLockUntilRef.current = Date.now() + INITIAL_BOTTOM_LOCK_MS
    userCancelledBottomLockRef.current = false
    userPausedAutoFollowRef.current = false
    window.clearTimeout(bottomLockTimerRef.current)
    bottomLockTimerRef.current = window.setTimeout(() => {
      if (bottomLockSessionRef.current === activeSessionId) clearInitialBottomLock()
    }, INITIAL_BOTTOM_LOCK_MS)
  }, [activeSessionId, clearInitialBottomLock, messages.length])

  // 切换会话时重置基线，并清理上一会话残留的流式推理展开态。
  useEffect(() => {
    wasNearBottomRef.current = true
    userPausedAutoFollowRef.current = false
    if (initialScrolledSessionRef.current !== activeSessionId) previousMessageCountRef.current = 0
    clearReasoningState("stream:")
  }, [activeSessionId, clearReasoningState])

  // 新增消息时，仅当用户原本贴在底部才自动跟随。
  useEffect(() => {
    if (messages.length <= previousMessageCountRef.current) {
      previousMessageCountRef.current = messages.length
      return
    }
    previousMessageCountRef.current = messages.length
    if (wasNearBottomRef.current && !userPausedAutoFollowRef.current) scrollToBottom(false)
  }, [messages.length, scrollToBottom])

  // 流式增量时跟随底部；原生 overflow-anchor 负责非底部时的视口稳定。
  useEffect(() => {
    if (!isStreaming || !wasNearBottomRef.current || userPausedAutoFollowRef.current) return
    scrollToBottom(false)
  }, [isStreaming, streamingContentLength, streamingThinkingLength, streamingToolCount, scrollToBottom])

  useEffect(() => {
    const target = listRef.current
    if (!target || typeof ResizeObserver === "undefined") return
    const observer = new ResizeObserver(() => keepInitialBottomLocked())
    observer.observe(target)
    return () => observer.disconnect()
  }, [keepInitialBottomLocked])

  useEffect(() => {
    const el = scrollRef.current
    if (!el) return
    function cancelLockFromUserInput() {
      clearPendingAnchor()
      userCancelledBottomLockRef.current = true
      clearInitialBottomLock()
      if (isStreaming) {
        userPausedAutoFollowRef.current = true
        wasNearBottomRef.current = false
        cancelAnimationFrame(rafRef.current)
      }
    }
    el.addEventListener("wheel", cancelLockFromUserInput, { passive: true })
    el.addEventListener("touchstart", cancelLockFromUserInput, { passive: true })
    el.addEventListener("touchmove", cancelLockFromUserInput, { passive: true })
    el.addEventListener("pointerdown", cancelLockFromUserInput, { passive: true })
    return () => {
      el.removeEventListener("wheel", cancelLockFromUserInput)
      el.removeEventListener("touchstart", cancelLockFromUserInput)
      el.removeEventListener("touchmove", cancelLockFromUserInput)
      el.removeEventListener("pointerdown", cancelLockFromUserInput)
    }
  }, [clearInitialBottomLock, clearPendingAnchor, isStreaming])

  useEffect(() => () => {
    cancelAnimationFrame(rafRef.current)
    cancelAnimationFrame(trimRafRef.current)
    window.clearTimeout(bottomLockTimerRef.current)
    clearPendingAnchor()
    window.clearTimeout(windowSwitchReleaseTimerRef.current)
  }, [clearPendingAnchor])

  useEffect(() => {
    const el = scrollRef.current
    if (!el) return
    function onScroll() {
      if (!el) return
      const distance = el.scrollHeight - el.scrollTop - el.clientHeight
      const nearBottom = distance < 120
      wasNearBottomRef.current = nearBottom
      if (nearBottom) userPausedAutoFollowRef.current = false
      setShowBack(distance > 180)
      if (!windowSwitchingRef.current && el.scrollTop < el.clientHeight * 8 && Date.now() >= suppressOlderLoadUntilRef.current) triggerLoadOlder()
      if (!windowSwitchingRef.current && distance < el.clientHeight * 8 && hasNewerMessages) void loadNewerMessages()
    }
    el.addEventListener("scroll", onScroll, { passive: true })
    requestAnimationFrame(onScroll)
    return () => el.removeEventListener("scroll", onScroll)
  }, [hasNewerMessages, loadNewerMessages, triggerLoadOlder])

  useEffect(() => {
    const el = scrollRef.current
    if (!el || windowSwitchingRef.current || !hasMoreMessages || isLoadingOlder || loadedTurns.length >= 48) return
    if (el.scrollHeight < el.clientHeight * 14) void triggerLoadOlder()
  }, [hasMoreMessages, isLoadingOlder, loadedTurns.length, messages.length, triggerLoadOlder])

  const handleBackToBottom = useCallback(() => {
    userPausedAutoFollowRef.current = false
    wasNearBottomRef.current = true
    setShowBack(false)
    scrollToBottom(true)
  }, [scrollToBottom])

  const updateActiveTurn = useCallback(() => {
    cancelAnimationFrame(turnRafRef.current)
    turnRafRef.current = requestAnimationFrame(() => {
      const container = scrollRef.current
      if (!container || loadedTurns.length === 0) return
      const threshold = container.getBoundingClientRect().top + Math.min(180, container.clientHeight * 0.28)
      let nextId = loadedTurns[0].id
      for (const turn of loadedTurns) {
        const anchor = turnAnchorsRef.current.get(turn.userMessageId)
        if (!anchor || anchor.getBoundingClientRect().top > threshold) break
        nextId = turn.id
      }
      setActiveTurnId((current) => current === nextId ? current : nextId)
    })
  }, [loadedTurns])

  useEffect(() => {
    updateActiveTurn()
  }, [updateActiveTurn])

  useEffect(() => {
    const container = scrollRef.current
    if (!container) return
    container.addEventListener("scroll", updateActiveTurn, { passive: true })
    window.addEventListener("resize", updateActiveTurn)
    return () => {
      container.removeEventListener("scroll", updateActiveTurn)
      window.removeEventListener("resize", updateActiveTurn)
    }
  }, [updateActiveTurn])

  const handleTurnSelect = useCallback(async (turn: ConversationTurn) => {
    const container = scrollRef.current
    if (!container) return
    let anchor = turnAnchorsRef.current.get(turn.userMessageId)
    if (!anchor) {
      const switchToken = ++windowSwitchTokenRef.current
      window.clearTimeout(windowSwitchReleaseTimerRef.current)
      windowSwitchingRef.current = true
      clearPendingAnchor()
      clearInitialBottomLock()
      try {
        await loadMessageWindowAround(turn.id)
        setWindowSwitching(true)
        await new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve())))
        anchor = turnAnchorsRef.current.get(turn.userMessageId)
        if (!anchor || switchToken !== windowSwitchTokenRef.current) return
        suppressOlderLoadUntilRef.current = Date.now() + 900
        const top = anchor.getBoundingClientRect().top - container.getBoundingClientRect().top + container.scrollTop - 24
        container.scrollTop = top
        setActiveTurnId(turn.id)
      } finally {
        if (switchToken === windowSwitchTokenRef.current) {
          requestAnimationFrame(() => setWindowSwitching(false))
          windowSwitchReleaseTimerRef.current = window.setTimeout(() => {
            if (switchToken === windowSwitchTokenRef.current) windowSwitchingRef.current = false
          }, 180)
        }
      }
      return
    }
    clearInitialBottomLock()
    userPausedAutoFollowRef.current = isStreaming
    suppressOlderLoadUntilRef.current = Date.now() + 900
    const top = anchor.getBoundingClientRect().top - container.getBoundingClientRect().top + container.scrollTop - 24
    container.scrollTo({ top, behavior: prefersReducedMotion() ? "auto" : "smooth" })
    setActiveTurnId(turn.id)
  }, [clearInitialBottomLock, clearPendingAnchor, isStreaming, loadMessageWindowAround])

  const requestedTurnRef = useRef(0)
  useEffect(() => {
    const requestedTurn = Number(searchParams.get("turn"))
    if (!Number.isSafeInteger(requestedTurn) || requestedTurn <= 0) {
      requestedTurnRef.current = 0
      return
    }
    if (requestedTurnRef.current === requestedTurn) return
    const turn = turns.find((item) => item.id === requestedTurn)
    if (!turn) return
    requestedTurnRef.current = requestedTurn
    void handleTurnSelect(turn).finally(() => {
      const next = new URLSearchParams(searchParams)
      next.delete("turn")
      setSearchParams(next, { replace: true })
    })
  }, [handleTurnSelect, searchParams, setSearchParams, turns])

  return (
    <div className="relative min-h-0 flex-1">
      <div
        ref={scrollRef}
        data-chat-scroll-container="true"
        className="absolute inset-0 overflow-y-auto overscroll-contain scrollbar-thin"
      >
        <div
          ref={listRef}
          className={`mx-auto w-full max-w-[1060px] px-4 pt-16 transition-opacity duration-[160ms] ease-out motion-reduce:transition-none ${windowSwitching ? "opacity-0" : "opacity-100"}`}
          style={{ paddingBottom: "var(--chat-composer-inset, 1rem)" }}
          data-testid="message-list"
        >
          {visibleMessages.map((msg) => (
            <div
              key={msg.id}
              ref={msg.message_data.role === "user" ? (element) => {
                if (element) turnAnchorsRef.current.set(msg.id, element)
                else turnAnchorsRef.current.delete(msg.id)
              } : undefined}
              data-testid="message-item"
              data-message-id={msg.id}
              data-role={msg.message_data.role}
              data-turn-anchor={msg.message_data.role === "user" ? msg.id : undefined}
            >
              {isCompactionSummary(msg) ? (
                <CompactionDivider
                  message={msg}
                  canUndo={msg.id === lastSummaryId && msg.id === lastVisibleId && compactionKind(msg) !== "auto" && !isStreaming && !compacting}
                  undoing={undoing}
                  onUndo={handleUndoCompaction}
                />
              ) : msg.role === "user" ? (
                <UserMessage
                  message={msg}
                  isLastUserRetryable={msg.id === lastVisibleId && !isStreaming}
                  isEditableTail={msg.id === editableUserMessageId}
                  stopsZeroOutputRun={msg.id === editableUserMessageId && (streamingStatus === "streaming" || streamingStatus === "recovering")}
                />
              ) : (
                <AssistantMessage message={msg} isLastAssistant={msg.id === lastAssistantId && !isStreaming} />
              )}
            </div>
          ))}
          {isStreaming && !durableStreamMessageExists && (
            <div className="animate-msg-in">
              <StreamingMessage />
            </div>
          )}
          {compacting && <CompactionIndicator notice={compactionNotice} />}
        </div>

        {showBack && (
          <div
            className="sticky z-10 flex justify-center pointer-events-none animate-in fade-in-0 zoom-in-95 motion-surface"
            style={{ bottom: "calc(var(--chat-composer-inset, 0px) + 0.75rem)" }}
          >
            <button
              onClick={handleBackToBottom}
              aria-label="回到最新消息"
              className="pointer-events-auto flex h-8 w-8 items-center justify-center rounded-full border border-border bg-background/95 shadow-md backdrop-blur transition-[background-color,border-color,box-shadow,color] motion-control hover:shadow-lg"
            >
              <ChevronDown className="h-4 w-4 text-muted-foreground" aria-hidden="true" />
            </button>
          </div>
        )}
      </div>
      {isLoadingOlder && <div className="pointer-events-none absolute left-1/2 top-2 z-20 -translate-x-1/2 rounded-full bg-background/70 px-2 py-1 backdrop-blur"><Loader2 className="h-3.5 w-3.5 animate-spin text-muted-foreground" /></div>}
      {isLoadingNewer && <div className="pointer-events-none absolute bottom-2 left-1/2 z-20 -translate-x-1/2 rounded-full bg-background/70 px-2 py-1 backdrop-blur"><Loader2 className="h-3.5 w-3.5 animate-spin text-muted-foreground" /></div>}
      <ConversationTurnRail turns={turns} activeTurnId={displayedActiveTurnId} onSelect={handleTurnSelect} />
    </div>
  )
}

function ConversationTurnRail({
  turns,
  activeTurnId,
  onSelect,
}: {
  turns: ConversationTurn[]
  activeTurnId: number | null
  onSelect: (turn: ConversationTurn) => void | Promise<void>
}) {
  const viewportRef = useRef<HTMLDivElement>(null)
  const followPausedUntilRef = useRef(0)
  const followResumeTimerRef = useRef(0)
  const [scrollTop, setScrollTop] = useState(0)
  const [followRevision, setFollowRevision] = useState(0)
  const [interactionIndex, setInteractionIndex] = useState<number | null>(null)
  const activeIndex = Math.max(0, turns.findIndex((turn) => turn.id === activeTurnId))
  const { scrollable, virtual } = conversationTurnRailMode(turns.length)
  const rowHeight = 10
  const viewportHeight = 520
  const { start, end } = conversationTurnMarkerRange(turns.length, scrollTop, viewportHeight, rowHeight)

  useEffect(() => {
    const viewport = viewportRef.current
    if (!viewport || !scrollable || Date.now() < followPausedUntilRef.current) return
    const markerTop = activeIndex * rowHeight
    if (markerTop < viewport.scrollTop + viewport.clientHeight / 3 || markerTop > viewport.scrollTop + viewport.clientHeight * 2 / 3) {
      viewport.scrollTo({ top: Math.max(0, markerTop - viewport.clientHeight / 2), behavior: prefersReducedMotion() ? "auto" : "smooth" })
    }
  }, [activeIndex, followRevision, scrollable])

  useEffect(() => () => window.clearTimeout(followResumeTimerRef.current), [])

  if (turns.length < 2) return null

  function handleKeyDown(event: React.KeyboardEvent<HTMLDivElement>) {
    let next = activeIndex
    if (event.key === "ArrowUp") next -= 1
    else if (event.key === "ArrowDown") next += 1
    else if (event.key === "PageUp") next -= 10
    else if (event.key === "PageDown") next += 10
    else if (event.key === "Home") next = 0
    else if (event.key === "End") next = turns.length - 1
    else if (event.key === "Enter") void onSelect(turns[activeIndex])
    else return
    event.preventDefault()
    if (event.key !== "Enter") void onSelect(turns[Math.max(0, Math.min(turns.length - 1, next))])
  }

  return (
    <nav
      aria-label="对话轮次"
      onPointerLeave={() => setInteractionIndex(null)}
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget)) setInteractionIndex(null)
      }}
      className="pointer-events-none absolute left-2 top-1/2 z-20 hidden -translate-y-1/2 lg:flex"
    >
      <div className="relative">
      <div
        ref={viewportRef}
        tabIndex={0}
        onKeyDown={handleKeyDown}
        onWheel={() => {
          followPausedUntilRef.current = Date.now() + 1200
          window.clearTimeout(followResumeTimerRef.current)
          followResumeTimerRef.current = window.setTimeout(() => setFollowRevision((value) => value + 1), 1200)
        }}
        onScroll={(event) => { if (virtual) setScrollTop(event.currentTarget.scrollTop) }}
        className={`pointer-events-auto outline-none ${scrollable ? "overflow-y-auto overscroll-contain [scrollbar-width:none]" : "flex flex-col justify-center"}`}
        style={{ height: scrollable ? "min(60vh, 520px)" : "auto" }}
      >
        <div className={scrollable ? "relative" : "flex flex-col justify-center gap-[3px]"} style={virtual ? { height: `${turns.length * rowHeight}px` } : undefined}>
        {turns.slice(start, end).map((turn, visibleIndex) => {
          const index = start + visibleIndex
          const distance = Math.abs(index - activeIndex)
          const active = distance === 0
          return (
            <button
              key={turn.id}
              type="button"
              aria-label={`跳转到对话：${turn.title}`}
              aria-current={active ? "step" : undefined}
              onPointerEnter={() => setInteractionIndex(index)}
              onFocus={() => setInteractionIndex(index)}
              onClick={() => void onSelect(turn)}
              className={`group relative flex items-center outline-none ${scrollable ? "h-2.5" : "h-1.5"} ${virtual ? "absolute left-0" : ""}`}
              style={virtual ? { top: `${index * rowHeight}px` } : undefined}
            >
              <span
                className={`block h-0.5 rounded-full transition-[width,background-color,opacity] duration-[240ms] ease-out ${
                  interactionIndex === index ? "w-6 bg-foreground/85"
                    : interactionIndex != null && Math.abs(index - interactionIndex) === 1 ? "w-[18px] bg-foreground/55"
                    : interactionIndex != null && Math.abs(index - interactionIndex) === 2 ? "w-3.5 bg-foreground/38"
                    : active ? "w-2.5 bg-foreground/68"
                    : "w-2.5 bg-muted-foreground/28"
                }`}
              />
              <span className="conversation-turn-preview pointer-events-none absolute left-7 top-1/2 w-72 rounded-lg border border-border/55 bg-popover/88 px-3 py-2.5 text-left text-popover-foreground shadow-[0_12px_36px_rgba(0,0,0,0.14)] backdrop-blur-xl xl:w-80">
                <span className="block truncate text-sm font-medium">{turn.title}</span>
                {turn.assistantPreview ? (
                  <span className="mt-1.5 line-clamp-3 block text-xs leading-5 text-muted-foreground">{turn.assistantPreview}</span>
                ) : null}
              </span>
            </button>
          )
        })}
        </div>
      </div>
      {scrollable ? <><div className="pointer-events-none absolute inset-x-0 top-0 h-8 bg-gradient-to-b from-background to-transparent" /><div className="pointer-events-none absolute inset-x-0 bottom-0 h-8 bg-gradient-to-t from-background to-transparent" /></> : null}
      </div>
    </nav>
  )
}

// CompactionDivider 压缩检查点分割线：摘要消息渲染为一条居中细线，
// 标示此线以上的历史已被压缩为摘要（参考 Claude Code 的分隔样式）。
// 仅最近一条检查点可撤销（canUndo），hover 时显示撤销按钮。
function CompactionDivider({
  message,
  canUndo,
  undoing,
  onUndo,
}: {
  message: Message
  canUndo?: boolean
  undoing?: boolean
  onUndo?: () => void
}) {
  const [expanded, setExpanded] = useState(false)
  const [confirmOpen, setConfirmOpen] = useState(false)
  const summary = message.message_data.content || ""

  return (
    <div className="py-5" data-testid="compaction-divider">
      <div className="flex items-center gap-3 text-muted-foreground">
        <div className="h-px flex-1 bg-border" />
        <div className="flex items-center gap-2 text-xs tracking-wide">
          <button
            onClick={() => setExpanded((v) => !v)}
            className="inline-flex items-center gap-1 rounded px-1.5 py-0.5 transition-colors motion-control hover:text-foreground"
            title={expanded ? "收起摘要" : "展开查看压缩摘要"}
          >
            {expanded ? <ChevronDown className="h-3 w-3" /> : <ChevronRight className="h-3 w-3" />}
            以上对话已压缩
          </button>
          {canUndo && (
            <button
              onClick={() => setConfirmOpen(true)}
              disabled={undoing}
              className="inline-flex items-center gap-1 rounded border border-border px-1.5 py-0.5 text-muted-foreground transition-colors motion-control hover:border-foreground/30 hover:text-foreground disabled:opacity-50"
              title="撤销压缩，恢复完整历史"
              data-testid="undo-compaction"
            >
              {undoing ? <Loader2 className="h-3 w-3 animate-spin" /> : <Undo2 className="h-3 w-3" />}
              撤销
            </button>
          )}
        </div>
        <div className="h-px flex-1 bg-border" />
      </div>

      <div className="grid transition-[grid-template-rows] motion-panel" style={{ gridTemplateRows: expanded ? "1fr" : "0fr" }}>
        <div className="min-h-0 overflow-hidden">
          <div className={`mx-auto mt-3 max-w-[900px] rounded-lg border border-border bg-muted/30 p-4 text-sm transition-opacity motion-panel ${expanded ? "opacity-100" : "opacity-0"}`}>
            <MarkdownContent content={summary} ownerKey={`compaction-${message.id}`} />
          </div>
        </div>
      </div>

      <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>撤销压缩？</DialogTitle>
            <DialogDescription>
              将恢复被压缩的完整历史消息。若上下文仍超过阈值，下次发送可能会再次触发压缩。
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <DialogClose className="rounded-md border border-input px-3 py-1.5 text-sm hover:bg-accent">
              取消
            </DialogClose>
            <button
              onClick={() => {
                setConfirmOpen(false)
                onUndo?.()
              }}
              className="rounded-md bg-primary px-3 py-1.5 text-sm text-primary-foreground hover:bg-primary/90"
              data-testid="undo-compaction-confirm"
            >
              确定撤销
            </button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}

// CompactionIndicator 压缩进行中的居中提示（Claude Code 风格：细线 + 转圈 + 文案）。
// notice 为后端下发的友好说明（自动压缩时有），缺省回退到手动压缩的通用文案。
function CompactionIndicator({ notice }: { notice?: string }) {
  return (
    <div className="flex items-center gap-3 py-5 text-muted-foreground animate-msg-in">
      <div className="h-px flex-1 bg-border" />
      <span className="flex items-center gap-1.5 text-xs tracking-wide">
        <Loader2 className="h-3 w-3 animate-spin" />
        {notice || "正在压缩上下文"}
      </span>
      <div className="h-px flex-1 bg-border" />
    </div>
  )
}
