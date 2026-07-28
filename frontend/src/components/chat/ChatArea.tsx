import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react"
import { useChatStore } from "@/stores/chat"
import { useLocation, useNavigate, useParams } from "react-router"
import { ChatInput, type ChatInputHandle } from "./ChatInput"
import { SessionFilesDrawer } from "./SessionFilesDrawer"
import { SessionExportDialog } from "./SessionExportDialog"
import { MessageList } from "@/components/message/MessageList"
import { Button } from "@/components/ui/button"
import { LoadingIndicator } from "@/components/ui/loading-indicator"
import { AppLogo } from "@/components/AppLogo"
import { ModelSelector } from "./ModelSelector"
import { useSSE } from "@/hooks/useSSE"
import { useModelStore } from "@/stores/models"
import { useAuthStore } from "@/stores/auth"
import { isStreamingDisplayActive } from "@/lib/streamingStatus"
import { navigateWithFade } from "@/lib/navigation"
import { pickEmptyQuote } from "@/lib/emptyQuotes"
import { prefersReducedMotion } from "@/lib/motionPreference"
import { Files, PanelLeft, PanelLeftClose, Settings2, UploadCloud } from "lucide-react"

export function ChatArea({
  sidebarOpen,
  onToggleSidebar,
}: {
  sidebarOpen: boolean
  onToggleSidebar: () => void
}) {
  const navigate = useNavigate()
  const { sessionId } = useParams()
  const activeSessionId = useChatStore((s) => s.activeSessionId)
  const messages = useChatStore((s) => s.messages)
  const sessions = useChatStore((s) => s.sessions)
  const isLoadingMessages = useChatStore((s) => s.isLoadingMessages)
  const messageLoadError = useChatStore((s) => s.messageLoadError)
  const loadMessages = useChatStore((s) => s.loadMessages)
  const createSession = useChatStore((s) => s.createSession)
  const streamingStatus = useChatStore((s) => s.streaming.status)
  const models = useModelStore((s) => s.models)
  const modelsLoaded = useModelStore((s) => s.loaded)
  const user = useAuthStore((s) => s.user)
  const location = useLocation()
  const { resumeActiveRun, disconnectActiveStream } = useSSE()

  const chatInputRef = useRef<ChatInputHandle>(null)
  const chatAreaRef = useRef<HTMLDivElement>(null)
  const composerDockRef = useRef<HTMLDivElement>(null)
  const [dragging, setDragging] = useState(false)
  const [filesOpen, setFilesOpen] = useState(false)
  // dragenter/leave 会在子元素间反复触发，用计数器判定真正离开整个区域。
  const dragDepth = useRef(0)
  const routedSessionId = sessionId ? Number(sessionId) : null
  const isRouteSessionPending = Boolean(routedSessionId && activeSessionId !== routedSessionId)
  const isSessionTransitioning = isRouteSessionPending || (isLoadingMessages && messages.length === 0)
  const showSessionLoading = useDelayedVisible(isSessionTransitioning, 120)
  const activeSession = sessions.find((item) => item.id === activeSessionId)

  useLayoutEffect(() => {
    const root = chatAreaRef.current
    const dock = composerDockRef.current
    if (!root || !dock) return

    const updateInset = () => {
      const scroller = root.querySelector<HTMLElement>("[data-chat-scroll-container]")
      const keepBottomAnchored = scroller
        ? scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 120
        : false
      const inset = `${Math.ceil(dock.getBoundingClientRect().height)}px`
      root.style.setProperty("--chat-composer-inset", inset)
      document.documentElement.style.setProperty("--chat-composer-viewport-inset", inset)
      if (scroller && keepBottomAnchored) scroller.scrollTop = scroller.scrollHeight
    }
    updateInset()

    const observer = new ResizeObserver(updateInset)
    observer.observe(dock)
    return () => {
      observer.disconnect()
      root.style.removeProperty("--chat-composer-inset")
      document.documentElement.style.removeProperty("--chat-composer-viewport-inset")
    }
  }, [activeSessionId])

  function hasFiles(e: React.DragEvent) {
    return Array.from(e.dataTransfer?.types || []).includes("Files")
  }

  function handleDragEnter(e: React.DragEvent<HTMLDivElement>) {
    if (!hasFiles(e)) return
    e.preventDefault()
    dragDepth.current += 1
    if (chatInputRef.current?.canUpload) setDragging(true)
  }

  function handleDragOver(e: React.DragEvent<HTMLDivElement>) {
    if (!hasFiles(e)) return
    e.preventDefault()
  }

  function handleDragLeave(e: React.DragEvent<HTMLDivElement>) {
    if (!hasFiles(e)) return
    dragDepth.current = Math.max(0, dragDepth.current - 1)
    if (dragDepth.current === 0) setDragging(false)
  }

  async function handleDrop(e: React.DragEvent<HTMLDivElement>) {
    dragDepth.current = 0
    if (!hasFiles(e)) return
    e.preventDefault()
    setDragging(false)
    const files = Array.from(e.dataTransfer.files || [])
    await chatInputRef.current?.uploadFiles(files)
  }

  useEffect(() => {
    if (!activeSessionId || isRouteSessionPending || isLoadingMessages || streamingStatus !== "idle") return
    resumeActiveRun(activeSessionId).catch(() => {})
  }, [activeSessionId, isLoadingMessages, isRouteSessionPending, resumeActiveRun, streamingStatus])

  useEffect(() => {
    function reconcileAfterReturn() {
      if (!activeSessionId || isRouteSessionPending || isLoadingMessages || document.visibilityState !== "visible") return
      void resumeActiveRun(activeSessionId).catch(() => {})
    }
    function handleVisibilityChange() {
      if (document.visibilityState === "visible") reconcileAfterReturn()
    }

    window.addEventListener("online", reconcileAfterReturn)
    window.addEventListener("pageshow", reconcileAfterReturn)
    document.addEventListener("visibilitychange", handleVisibilityChange)
    return () => {
      window.removeEventListener("online", reconcileAfterReturn)
      window.removeEventListener("pageshow", reconcileAfterReturn)
      document.removeEventListener("visibilitychange", handleVisibilityChange)
    }
  }, [activeSessionId, isLoadingMessages, isRouteSessionPending, resumeActiveRun])

  useEffect(() => {
    return () => {
      if (activeSessionId) disconnectActiveStream(activeSessionId)
    }
  }, [activeSessionId, disconnectActiveStream])

  async function handleCreateSession() {
    const session = await createSession()
    navigate(`/chat/${session.id}`)
  }

  function handleRetryLoadMessages() {
    if (activeSessionId) void loadMessages(activeSessionId).catch(() => undefined)
  }

  const showBlockingMessageLoadError = Boolean(messageLoadError && messages.length === 0 && !isStreamingDisplayActive(streamingStatus))
  if (!activeSessionId && !isRouteSessionPending) {
    const noEnabledModels = modelsLoaded && !models.some((model) => model.enabled)
    if (noEnabledModels) {
      return (
        <div className="flex min-h-0 flex-1 flex-col items-center justify-center overflow-hidden overscroll-none px-5 text-center">
          <Settings2 className="h-7 w-7 text-muted-foreground" />
          <h1 className="mt-3 text-base font-semibold">还没有可用模型</h1>
          <p className="mt-1 max-w-sm text-sm leading-6 text-muted-foreground">
            {user?.role === "admin"
              ? "先配置渠道并启用一个模型，之后就可以创建第一场对话。"
              : "管理员尚未完成模型配置，请联系管理员后再试。"}
          </p>
          {user?.role === "admin" ? (
            <Button variant="outline" size="sm" className="mt-4" onClick={() => navigateWithFade(navigate, "/admin/models", { state: { from: `${location.pathname}${location.search}` } })}>
              <Settings2 className="h-3.5 w-3.5" />
              配置模型
            </Button>
          ) : null}
        </div>
      )
    }
    return (
      <EmptyGreeting showLogo onCreateSession={handleCreateSession} />
    )
  }

  return (
    <div
      ref={chatAreaRef}
      className="relative flex min-h-0 flex-1 flex-col overflow-hidden overscroll-none"
      aria-label="聊天区域，支持拖放文件上传"
      onDragEnter={handleDragEnter}
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
    >
      <header className="pointer-events-none absolute inset-x-0 top-0 z-30 flex h-12 items-center gap-1.5 bg-gradient-to-b from-background/68 via-background/32 to-transparent px-2 sm:gap-2 sm:px-3">
        <Button
          variant="ghost"
          size="icon"
          onClick={onToggleSidebar}
          className="pointer-events-auto h-8 w-8 shrink-0 rounded-[10px] bg-popover/30 backdrop-blur-md hover:bg-popover/58"
          aria-label={sidebarOpen ? "收起侧边栏" : "打开侧边栏"}
          aria-expanded={sidebarOpen}
          aria-controls="app-sidebar"
        >
          {sidebarOpen ? <PanelLeftClose className="h-4 w-4" /> : <PanelLeft className="h-4 w-4" />}
        </Button>

        {activeSession ? (
          <h1 className="min-w-0 flex-1 truncate px-1 text-sm font-medium text-foreground/80">
            {activeSession.title || "新对话"}
          </h1>
        ) : (
          <div className="flex-1" />
        )}

        {!isSessionTransitioning && activeSessionId ? (
          <div className="pointer-events-none flex shrink-0 items-center gap-1">
          <Button
            variant="outline"
            size="sm"
            className="pointer-events-auto h-8 w-8 gap-1.5 rounded-[10px] border-border/45 bg-popover/42 px-0 text-xs shadow-sm backdrop-blur-md motion-control hover:bg-popover/68 sm:w-auto sm:px-2.5"
            onClick={() => setFilesOpen(true)}
            aria-expanded={filesOpen}
            aria-label="文件"
          >
            <Files className="h-3.5 w-3.5" />
            <span className="hidden sm:inline">文件</span>
          </Button>
          <SessionExportDialog key={activeSessionId} sessionId={activeSessionId} />
          </div>
        ) : null}
        <div className="pointer-events-auto min-w-0 shrink-0">
          <ModelSelector />
        </div>
      </header>
      <div className="flex min-h-0 flex-1 flex-col">
        {isSessionTransitioning ? (
          <div className="flex flex-1 items-center justify-center">
            {showSessionLoading ? <LoadingIndicator label="正在加载会话" /> : null}
          </div>
        ) : showBlockingMessageLoadError ? (
          <div className="flex flex-1 items-center justify-center px-4">
            <div className="max-w-sm text-center">
              <p className="text-sm font-medium text-foreground">消息加载失败</p>
              <p className="mt-1 text-sm text-muted-foreground">{messageLoadError}</p>
              <Button variant="outline" size="sm" className="mt-3" onClick={handleRetryLoadMessages}>
                重新加载
              </Button>
            </div>
          </div>
        ) : messages.length === 0 ? (
          <EmptyGreeting key={activeSessionId} withComposerInset />
        ) : (
          <MessageList />
        )}
      </div>
      <div ref={composerDockRef} className="pointer-events-none absolute bottom-0 left-0 right-0 z-20 bg-gradient-to-t from-background/68 via-background/32 to-transparent pt-10 pb-[env(safe-area-inset-bottom)] sm:pb-6">
        <div className="pointer-events-auto">
          <ChatInput ref={chatInputRef} />
        </div>
      </div>
      <SessionFilesDrawer sessionId={activeSessionId} open={filesOpen} onOpenChange={setFilesOpen} />

      {dragging && (
        <div className="pointer-events-none absolute inset-0 z-30 flex items-center justify-center bg-background/80 backdrop-blur-sm animate-in fade-in-0 motion-surface">
          <div className="m-4 flex h-[calc(100%-2rem)] w-[calc(100%-2rem)] flex-col items-center justify-center gap-3 rounded-2xl border-2 border-dashed border-primary/50 bg-primary/5">
            <UploadCloud className="h-10 w-10 text-primary/70" />
            <p className="text-sm font-medium text-foreground">拖放文件到此处上传</p>
            <p className="text-xs text-muted-foreground">支持图片、PDF、Word、Excel、文本等</p>
          </div>
        </div>
      )}
    </div>
  )
}

function useDelayedVisible(active: boolean, delayMs: number) {
  const [visible, setVisible] = useState(false)

  useEffect(() => {
    const timer = window.setTimeout(() => setVisible(active), active ? delayMs : 0)
    return () => window.clearTimeout(timer)
  }, [active, delayMs])

  return active && visible
}

function EmptyGreeting({
  showLogo = false,
  withComposerInset = false,
  onCreateSession,
}: {
  showLogo?: boolean
  withComposerInset?: boolean
  onCreateSession?: () => void
}) {
  const hour = new Date().getHours()
  const greeting = hour < 5 ? "夜深了" : hour < 12 ? "早上好" : hour < 18 ? "下午好" : "晚上好"
  const [quote] = useState(pickEmptyQuote)
  const { text, complete } = useQuoteTypewriter(quote.text)

  return (
    <div
      className="flex flex-1 items-center justify-center px-6 text-center"
      style={withComposerInset ? { paddingBottom: "calc(var(--chat-composer-inset, 5rem) + 1rem)" } : { paddingBottom: "4rem" }}
    >
      <div className="empty-greeting flex max-w-2xl flex-col items-center">
        {showLogo ? <AppLogo className="mb-6 h-10 w-10 opacity-70" /> : null}
        <h2 className="text-sm font-medium text-muted-foreground/75">{greeting}</h2>
        <blockquote
          aria-label={quote.text}
          className="relative mt-4 text-balance font-serif text-xl leading-9 text-foreground/82 sm:text-[1.35rem]"
        >
          <span aria-hidden="true" className="invisible block">{quote.text}</span>
          <span aria-hidden="true" className="absolute inset-0 block">
            <span className="quote-typewriter" data-complete={complete || undefined}>{text}</span>
          </span>
        </blockquote>
        <p
          aria-hidden={!complete}
          className="quote-source mt-3 text-xs text-muted-foreground/60"
          data-visible={complete || undefined}
        >
          {quote.source}
        </p>
        {onCreateSession ? (
          <Button variant="outline" size="sm" className="mt-7" onClick={onCreateSession}>
            新建对话
          </Button>
        ) : null}
      </div>
    </div>
  )
}

function useQuoteTypewriter(value: string) {
  const reduced = prefersReducedMotion()
  const characters = useMemo(() => Array.from(value), [value])
  const [length, setLength] = useState(() => reduced ? Array.from(value).length : 0)

  useEffect(() => {
    if (reduced) return

    let index = 0
    let timer = window.setTimeout(tick, 420)

    function tick() {
      index += 1
      setLength(index)
      if (index < characters.length) {
        timer = window.setTimeout(tick, quoteCharacterDelay(characters[index - 1]))
      }
    }

    return () => window.clearTimeout(timer)
  }, [characters, reduced])

  return {
    text: characters.slice(0, length).join(""),
    complete: length >= characters.length,
  }
}

function quoteCharacterDelay(character: string) {
  if (/[。！？.!?]/u.test(character)) return 220
  if (/[，、；：,;:]/u.test(character)) return 105
  if (/\s/u.test(character)) return 18
  return character.charCodeAt(0) <= 0xff ? 30 : 48
}
