import { useEffect, useRef, useState } from "react"
import { useNavigate, useParams } from "react-router"
import { getSession } from "@/api/sessions"
import { useUIStore } from "@/stores/ui"
import { useChatStore } from "@/stores/chat"
import { useModelStore } from "@/stores/models"
import { Sidebar } from "@/components/sidebar/Sidebar"
import { ChatArea } from "@/components/chat/ChatArea"

export function Layout() {
  const navigate = useNavigate()
  const { sessionId } = useParams()
  const sidebarOpen = useUIStore((s) => s.sidebarOpen)
  const setSidebarOpen = useUIStore((s) => s.setSidebarOpen)
  const loadSessions = useChatStore((s) => s.loadSessions)
  const loadSessionFolders = useChatStore((s) => s.loadSessionFolders)
  const setActiveSession = useChatStore((s) => s.setActiveSession)
  const activeSessionId = useChatStore((s) => s.activeSessionId)
  const sessions = useChatStore((s) => s.sessions)
  const isLoadingSessions = useChatStore((s) => s.isLoadingSessions)
  const loadModels = useModelStore((s) => s.loadModels)
  const [isMobile, setIsMobile] = useState(() => window.matchMedia("(max-width: 768px)").matches)
  const [sessionsLoaded, setSessionsLoaded] = useState(false)
  const routeLookupRef = useRef<number | null>(null)

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
    const routedId = sessionId ? Number(sessionId) : null
    if (routedId) {
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

  return (
    <div className="flex h-full overflow-hidden overscroll-none bg-background">
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
        id="app-sidebar"
        data-state={sidebarOpen ? "open" : "closed"}
        aria-label="侧边栏"
        aria-hidden={!sidebarOpen}
        inert={!sidebarOpen ? true : undefined}
        className={`
          ${isMobile ? "fixed inset-y-0 left-0 z-50" : "relative"}
          ${isMobile ? "w-[min(84vw,300px)] transform-gpu transition-transform motion-surface" : `${sidebarOpen ? "w-[280px]" : "w-0"} transition-[width,border-color] motion-panel`}
          shrink-0 overflow-hidden overscroll-none
          border-r will-change-transform
          ${isMobile ? (sidebarOpen ? "pointer-events-auto translate-x-0 border-border" : "pointer-events-none -translate-x-[calc(100%+1px)] border-transparent") : ""}
          ${!isMobile ? (sidebarOpen ? "border-border" : "border-transparent") : ""}
        `}
      >
        <Sidebar />
      </aside>

      {/* Main */}
      <main className="flex min-w-0 flex-1 overflow-hidden overscroll-none">
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
