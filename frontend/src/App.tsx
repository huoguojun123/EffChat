import { lazy, useEffect } from "react"
import { WifiOff } from "lucide-react"
import { createBrowserRouter, Navigate } from "react-router"
import { RouterProvider } from "react-router/dom"
import { useAuthStore } from "@/stores/auth"
import { useSystemStore } from "@/stores/system"
import { LoginPage } from "@/components/auth/LoginPage"
import { RegisterPage } from "@/components/auth/RegisterPage"
import { Layout } from "@/components/Layout"
import { ErrorBoundary } from "@/components/ErrorBoundary"
import { PWAUpdatePrompt } from "@/components/PWAUpdatePrompt"
import { LazyBoundary } from "@/components/LazyBoundary"
import { LoadingIndicator } from "@/components/ui/loading-indicator"
import { Button } from "@/components/ui/button"

const AdminPage = lazy(() => import("@/components/admin/AdminPage").then((module) => ({ default: module.AdminPage })))

function ProtectedRoute({ children }: { children: React.ReactNode }) {
  const token = useAuthStore((s) => s.token)
  const hydrated = useAuthStore((s) => s.hydrated)
  if (token && !hydrated) return <AppBootShell />
  if (!token) return <Navigate to="/login" replace />
  return <>{children}</>
}

function GuestRoute({ children }: { children: React.ReactNode }) {
  const token = useAuthStore((s) => s.token)
  const hydrated = useAuthStore((s) => s.hydrated)
  if (token && !hydrated) return <AppBootShell />
  if (token) return <Navigate to="/" replace />
  return <>{children}</>
}

function AdminRoute({ children }: { children: React.ReactNode }) {
  const token = useAuthStore((s) => s.token)
  const user = useAuthStore((s) => s.user)
  const hydrated = useAuthStore((s) => s.hydrated)
  if (token && !hydrated) return <AppBootShell />
  if (!token || !user) return <Navigate to="/login" replace />
  if (user.role !== "admin") return <Navigate to="/" replace />
  return <>{children}</>
}

function AppBootShell() {
  const hydrationError = useAuthStore((s) => s.hydrationError)
  const hydrate = useAuthStore((s) => s.hydrate)
  return (
    <div className="flex h-dvh overflow-hidden bg-background" aria-busy={hydrationError ? undefined : true}>
      <div className="hidden w-[var(--desktop-sidebar-width)] shrink-0 border-r border-border/70 bg-sidebar md:block" />
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="h-12 shrink-0 border-b border-border/50" />
        {hydrationError ? (
          <div role="alert" className="flex flex-1 items-center justify-center px-5 text-center">
            <div className="max-w-sm">
              <WifiOff className="mx-auto h-5 w-5 text-muted-foreground" aria-hidden="true" />
              <h1 className="mt-3 text-sm font-semibold">暂时无法连接</h1>
              <p className="mt-1 text-sm text-muted-foreground">{hydrationError}</p>
              <Button type="button" variant="outline" className="mt-4 min-h-11" onClick={() => void hydrate()}>
                重新连接
              </Button>
            </div>
          </div>
        ) : <LoadingIndicator label="正在启动" className="flex-1" />}
      </div>
    </div>
  )
}

export default function App() {
  const hydrate = useAuthStore((s) => s.hydrate)
  const syncStoredToken = useAuthStore((s) => s.syncStoredToken)
  const loadSystem = useSystemStore((s) => s.load)

  useEffect(() => {
    hydrate()
    loadSystem()
    function handleStorage(event: StorageEvent) {
      if (event.storageArea === localStorage && event.key === "token") {
        void syncStoredToken(event.newValue)
      }
    }
    function handleOnline() {
      const auth = useAuthStore.getState()
      if (auth.token && !auth.hydrated) void auth.hydrate()
    }
    window.addEventListener("storage", handleStorage)
    window.addEventListener("online", handleOnline)
    return () => {
      window.removeEventListener("storage", handleStorage)
      window.removeEventListener("online", handleOnline)
    }
  }, [hydrate, loadSystem, syncStoredToken])

  return (
    <ErrorBoundary>
      <RouterProvider router={router} />
      <PWAUpdatePrompt />
    </ErrorBoundary>
  )
}

const router = createBrowserRouter([
  {
    path: "/login",
    element: <GuestRoute><LoginPage /></GuestRoute>,
  },
  {
    path: "/register",
    element: <GuestRoute><RegisterPage /></GuestRoute>,
  },
  {
    path: "/",
    element: <ProtectedRoute><Layout /></ProtectedRoute>,
  },
  {
    path: "/chat/:sessionId",
    element: <ProtectedRoute><Layout /></ProtectedRoute>,
  },
  {
    path: "/admin",
    element: <AdminRoute><Navigate to="/admin/models" replace /></AdminRoute>,
  },
  {
    path: "/admin/:section",
    element: <AdminRoute><LazyBoundary label="管理后台" variant="panel"><AdminPage /></LazyBoundary></AdminRoute>,
  },
  {
    path: "*",
    element: <Navigate to="/" replace />,
  },
])
