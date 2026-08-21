import { useCallback, useEffect, useRef, useState, type KeyboardEvent, type PointerEvent } from "react"
import { useNavigate, useParams } from "react-router"
import { getSession } from "@/api/sessions"
import { useUIStore } from "@/stores/ui"
import { useChatStore } from "@/stores/chat"
import { useModelStore } from "@/stores/models"
import { Sidebar } from "@/components/sidebar/Sidebar"
import { SkipLink } from "@/components/ui/skip-link"
import { ChatArea } from "@/components/chat/ChatArea"
import {
  DESKTOP_SIDEBAR_MIN_WIDTH,
  DESKTOP_SIDEBAR_STEP,
  applyDesktopSidebarWidth,
  clampDesktopSidebarWidth,
  desktopSidebarMaxWidth,
} from "@/lib/sidebarWidth"

export function Layout() {
  const navigate = useNavigate()
  const { sessionId } = useParams()
  const sidebarOpen = useUIStore((s) => s.sidebarOpen)
  const sidebarWidth = useUIStore((s) => s.sidebarWidth)
  const setSidebarOpen = useUIStore((s) => s.setSidebarOpen)
  const setSidebarWidth = useUIStore((s) => s.setSidebarWidth)
  const loadSessions = useChatStore((s) => s.loadSessions)
  const loadSessionFolders = useChatStore((s) => s.loadSessionFolders)
  const loadSessionCreateReadiness = useChatStore((s) => s.loadSessionCreateReadiness)
  const setActiveSession = useChatStore((s) => s.setActiveSession)
  const activeSessionId = useChatStore((s) => s.activeSessionId)
  const sessions = useChatStore((s) => s.sessions)
  const isLoadingSessions = useChatStore((s) => s.isLoadingSessions)
  const loadModels = useModelStore((s) => s.loadModels)
  const [isMobile, setIsMobile] = useState(() => window.matchMedia("(max-width: 768px)").matches)
  const [sessionsLoaded, setSessionsLoaded] = useState(false)
  const [sidebarRenderedWidth, setSidebarRenderedWidth] = useState(DESKTOP_SIDEBAR_MIN_WIDTH)
  const [sidebarMaxWidth, setSidebarMaxWidth] = useState(() => desktopSidebarMaxWidth(window.innerWidth))
  const [resizingSidebar, setResizingSidebar] = useState(false)
  const routeLookupRef = useRef<number | null>(null)
  const sidebarRef = useRef<HTMLElement | null>(null)
  const resizePointerRef = useRef<number | null>(null)
  const resizeWidthRef = useRef(DESKTOP_SIDEBAR_MIN_WIDTH)
  const previousUserSelectRef = useRef("")

  useEffect(() => {
    let cancelled = false
    loadSessionFolders().catch(() => undefined)
    loadSessions()
      .catch(() => undefined)
      .finally(() => {
        if (!cancelled) setSessionsLoaded(true)
      })
    return () => {
      cancelled = true
    }
  }, [loadSessionFolders, loadSessions])

  useEffect(() => {
    loadModels().catch(() => undefined)
  }, [loadModels])

  useEffect(() => {
    loadSessionCreateReadiness().catch(() => undefined)
  }, [loadSessionCreateReadiness])

  useEffect(() => {
    if (sessionId) {
      const routedId = Number(sessionId)
      if (!/^[1-9]\d*$/.test(sessionId) || !Number.isSafeInteger(routedId)) {
        setActiveSession(null)
        navigate("/", { replace: true })
        return
      }
      if (!sessionsLoaded || isLoadingSessions) return
      if (!sessions.some((item) => item.id === routedId)) {
        if (routeLookupRef.current === routedId) return
        let cancelled = false
        routeLookupRef.current = routedId
        getSession(routedId)
          .then((session) => {
            if (cancelled) return
            useChatStore.setState((state) => ({
              sessions: state.sessions.some((item) => item.id === session.id)
                ? state.sessions.map((item) => (item.id === session.id ? session : item))
                : [session, ...state.sessions],
            }))
            if (useChatStore.getState().activeSessionId !== routedId) setActiveSession(routedId)
          })
          .catch(() => {
            if (cancelled) return
            setActiveSession(null)
            navigate("/", { replace: true })
          })
          .finally(() => {
            if (!cancelled && routeLookupRef.current === routedId) routeLookupRef.current = null
          })
        return () => {
          cancelled = true
          if (routeLookupRef.current === routedId) routeLookupRef.current = null
        }
      }
      if (activeSessionId !== routedId) setActiveSession(routedId)
      return
    }

    if (!sessionsLoaded || isLoadingSessions || activeSessionId || sessions.length === 0) return

    const lastId = Number(localStorage.getItem("active_session_id") || 0)
    if (lastId && sessions.some((item) => item.id === lastId)) {
      navigate(`/chat/${lastId}`, { replace: true })
    }
  }, [activeSessionId, isLoadingSessions, navigate, sessionId, sessions, sessionsLoaded, setActiveSession])

  useEffect(() => {
    const mq = window.matchMedia("(max-width: 768px)")
    if (mq.matches) setSidebarOpen(false, false)
    function handler(e: MediaQueryListEvent) {
      setIsMobile(e.matches)
      if (e.matches) setSidebarOpen(false, false)
    }
    mq.addEventListener("change", handler)
    return () => mq.removeEventListener("change", handler)
  }, [setSidebarOpen])

  useEffect(() => {
    const update = () => {
      applyDesktopSidebarWidth(sidebarWidth)
      setSidebarMaxWidth(desktopSidebarMaxWidth(window.innerWidth))
    }
    update()
    window.addEventListener("resize", update)
    return () => window.removeEventListener("resize", update)
  }, [sidebarWidth])

  useEffect(() => {
    const sidebar = sidebarRef.current
    if (!sidebar || typeof ResizeObserver === "undefined") return
    const update = () => {
      const width = Math.round(sidebar.getBoundingClientRect().width)
      if (width >= DESKTOP_SIDEBAR_MIN_WIDTH) {
        resizeWidthRef.current = width
        setSidebarRenderedWidth(width)
      }
    }
    update()
    const observer = new ResizeObserver(update)
    observer.observe(sidebar)
    return () => observer.disconnect()
  }, [])

  useEffect(() => () => {
    document.body.style.userSelect = previousUserSelectRef.current
    applyDesktopSidebarWidth(useUIStore.getState().sidebarWidth)
  }, [])

  const updateSidebarWidth = useCallback((requestedWidth: number) => {
    const width = clampDesktopSidebarWidth(requestedWidth, window.innerWidth)
    resizeWidthRef.current = width
    setSidebarRenderedWidth(width)
    applyDesktopSidebarWidth(width)
    return width
  }, [])

  function finishSidebarResize(pointerId: number) {
    if (resizePointerRef.current !== pointerId) return
    resizePointerRef.current = null
    document.body.style.userSelect = previousUserSelectRef.current
    setResizingSidebar(false)
    setSidebarWidth(resizeWidthRef.current)
  }

  function handleSidebarPointerDown(event: PointerEvent<HTMLDivElement>) {
    if (event.button !== 0) return
    resizePointerRef.current = event.pointerId
    previousUserSelectRef.current = document.body.style.userSelect
    document.body.style.userSelect = "none"
    event.currentTarget.setPointerCapture(event.pointerId)
    setResizingSidebar(true)
  }

  function handleSidebarPointerMove(event: PointerEvent<HTMLDivElement>) {
    if (resizePointerRef.current !== event.pointerId) return
    updateSidebarWidth(event.clientX)
  }

  function handleSidebarKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    let nextWidth: number | null = null
    if (event.key === "ArrowLeft") nextWidth = sidebarRenderedWidth - DESKTOP_SIDEBAR_STEP
    if (event.key === "ArrowRight") nextWidth = sidebarRenderedWidth + DESKTOP_SIDEBAR_STEP
    if (event.key === "Home") nextWidth = DESKTOP_SIDEBAR_MIN_WIDTH
    if (event.key === "End") nextWidth = sidebarMaxWidth
    if (nextWidth === null) return
    event.preventDefault()
    setSidebarWidth(updateSidebarWidth(nextWidth))
  }

  return (
    <div className="flex h-full overflow-hidden overscroll-none bg-background">
      <SkipLink />
      {/* Mobile overlay */}
      <div
        data-state={sidebarOpen && isMobile ? "open" : "closed"}
        className="
          fixed inset-0 z-40 bg-black/40 backdrop-blur-[2px]
          opacity-0 pointer-events-none transition-opacity motion-surface md:hidden
          data-[state=open]:pointer-events-auto data-[state=open]:opacity-100
        "
        aria-hidden="true"
        onClick={() => setSidebarOpen(false, false)}
      />

      {/* Sidebar */}
      <aside
        ref={sidebarRef}
        id="app-sidebar"
        data-state={sidebarOpen ? "open" : "closed"}
        aria-label="侧边栏"
        aria-hidden={!sidebarOpen}
        inert={!sidebarOpen ? true : undefined}
        className={`
          ${isMobile ? "fixed inset-y-0 left-0 z-50" : "relative"}
          ${isMobile ? "w-[min(84vw,300px)] transform-gpu transition-transform motion-surface" : `${sidebarOpen ? "w-[var(--desktop-sidebar-width)]" : "w-0"} ${resizingSidebar ? "transition-none" : "transition-[width,border-color] motion-panel"}`}
          shrink-0 overflow-hidden overscroll-none
          border-r will-change-transform
          ${isMobile ? (sidebarOpen ? "pointer-events-auto translate-x-0 border-border" : "pointer-events-none -translate-x-[calc(100%+1px)] border-transparent") : ""}
          ${!isMobile ? (sidebarOpen ? "border-border" : "border-transparent") : ""}
        `}
      >
        <Sidebar />
        {!isMobile && sidebarOpen && sidebarMaxWidth > DESKTOP_SIDEBAR_MIN_WIDTH ? (
          <div
            role="separator"
            aria-label="调整侧边栏宽度"
            aria-controls="app-sidebar"
            aria-orientation="vertical"
            aria-valuemin={DESKTOP_SIDEBAR_MIN_WIDTH}
            aria-valuemax={sidebarMaxWidth}
            aria-valuenow={sidebarRenderedWidth}
            tabIndex={0}
            title="拖动或使用方向键调整侧边栏宽度"
            className="group absolute inset-y-0 right-0 z-20 flex w-2 cursor-col-resize touch-none select-none items-stretch justify-center focus-visible:bg-sidebar-accent/70 focus-visible:outline-none"
            onPointerDown={handleSidebarPointerDown}
            onPointerMove={handleSidebarPointerMove}
            onPointerUp={(event) => finishSidebarResize(event.pointerId)}
            onPointerCancel={(event) => finishSidebarResize(event.pointerId)}
            onLostPointerCapture={(event) => finishSidebarResize(event.pointerId)}
            onKeyDown={handleSidebarKeyDown}
          >
            <span aria-hidden="true" className="w-px bg-transparent transition-colors motion-control group-hover:bg-sidebar-border group-focus-visible:bg-sidebar-ring" />
          </div>
        ) : null}
      </aside>

      {/* Main */}
      <main id="main-content" tabIndex={-1} className="flex min-w-0 flex-1 overflow-hidden overscroll-none focus:outline-none">
        <section className="flex h-full min-w-0 flex-1 flex-col overflow-hidden overscroll-none">
          <ChatArea
            sidebarOpen={sidebarOpen}
            onToggleSidebar={() => setSidebarOpen(!sidebarOpen, !isMobile)}
          />
        </section>
      </main>
    </div>
  )
}
