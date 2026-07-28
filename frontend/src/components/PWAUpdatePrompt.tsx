import { RefreshCw } from "lucide-react"
import { useEffect, useRef, useState } from "react"
import { registerSW } from "virtual:pwa-register"

const periodicCheckMs = 30 * 60 * 1000
const foregroundCheckThrottleMs = 60 * 1000

export function PWAUpdatePrompt() {
  const updateRef = useRef<((reloadPage?: boolean) => Promise<void>) | null>(null)
  const updateTimeoutRef = useRef<number | null>(null)
  const [needRefresh, setNeedRefresh] = useState(false)
  const [updating, setUpdating] = useState(false)

  useEffect(() => {
    if (!("serviceWorker" in navigator)) return
    let intervalId: number | undefined
    let lastCheckAt = 0
    let disposed = false
    let registration: ServiceWorkerRegistration | undefined

    async function checkForUpdate(force = false) {
      if (disposed || !registration || !navigator.onLine || registration.installing) return
      const now = Date.now()
      if (!force && now - lastCheckAt < foregroundCheckThrottleMs) return
      lastCheckAt = now
      try {
        await registration.update()
      } catch {
        // Offline and transient network failures are retried on the next foreground or periodic check.
      }
    }

    updateRef.current = registerSW({
      immediate: true,
      onNeedReload() {
        window.location.reload()
      },
      onNeedRefresh() {
        if (disposed) return
        setNeedRefresh(true)
      },
      onRegisteredSW(_swUrl, nextRegistration) {
        if (disposed) return
        registration = nextRegistration
        void checkForUpdate(true)
        if (intervalId) window.clearInterval(intervalId)
        intervalId = window.setInterval(() => void checkForUpdate(true), periodicCheckMs)
      },
    })

    function handleForeground() {
      if (document.visibilityState === "visible") void checkForUpdate()
    }

    window.addEventListener("focus", handleForeground)
    window.addEventListener("online", handleForeground)
    document.addEventListener("visibilitychange", handleForeground)
    return () => {
      disposed = true
      if (intervalId) window.clearInterval(intervalId)
      if (updateTimeoutRef.current) window.clearTimeout(updateTimeoutRef.current)
      window.removeEventListener("focus", handleForeground)
      window.removeEventListener("online", handleForeground)
      document.removeEventListener("visibilitychange", handleForeground)
      updateRef.current = null
    }
  }, [])

  async function applyUpdate() {
    if (!updateRef.current || updating) return
    setUpdating(true)
    if (updateTimeoutRef.current) window.clearTimeout(updateTimeoutRef.current)
    updateTimeoutRef.current = window.setTimeout(() => {
      updateTimeoutRef.current = null
      setUpdating(false)
    }, 4000)
    try {
      await updateRef.current()
    } catch {
      if (updateTimeoutRef.current) window.clearTimeout(updateTimeoutRef.current)
      updateTimeoutRef.current = null
      setUpdating(false)
    }
  }

  if (!needRefresh) return null
  return (
    <div
      role="status"
      aria-live="polite"
      className="fixed inset-x-3 bottom-[calc(var(--chat-composer-viewport-inset,0px)_+_max(0.75rem,env(safe-area-inset-bottom)))] z-[100] mx-auto flex max-w-sm items-center gap-3 rounded-lg border border-white/35 bg-popover/72 px-3 py-2.5 text-sm text-popover-foreground shadow-lg backdrop-blur-xl backdrop-saturate-150 transition-[bottom,background-color] motion-panel dark:border-white/10 sm:bottom-4 sm:left-auto sm:right-4 sm:mx-0"
    >
      <RefreshCw className={updating ? "h-4 w-4 shrink-0 animate-spin text-primary" : "h-4 w-4 shrink-0 text-primary"} />
      <span className="min-w-0 flex-1">新版本已准备好</span>
      <button
        type="button"
        onClick={applyUpdate}
        disabled={updating}
        className="h-8 shrink-0 rounded-md bg-primary px-3 text-sm font-medium text-primary-foreground transition-opacity hover:opacity-90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-60"
      >
        {updating ? "更新中" : "更新"}
      </button>
    </div>
  )
}
