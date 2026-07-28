import { Suspense, type ReactNode } from "react"
import { RefreshCw } from "lucide-react"
import { ErrorBoundary } from "@/components/ErrorBoundary"
import { Button } from "@/components/ui/button"
import { LoadingIndicator } from "@/components/ui/loading-indicator"
import { cn } from "@/lib/utils"

interface LazyBoundaryProps {
  children: ReactNode
  label: string
  variant: "panel" | "dialog"
}

export function LazyBoundary({ children, label, variant }: LazyBoundaryProps) {
  return (
    <ErrorBoundary fallback={() => <LazyFailure label={label} variant={variant} />}>
      <Suspense fallback={<LazyLoading label={label} variant={variant} />}>
        {children}
      </Suspense>
    </ErrorBoundary>
  )
}

function LazyLoading({ label, variant }: Omit<LazyBoundaryProps, "children">) {
  return (
    <div
      className={cn(
        "flex items-center justify-center",
        variant === "dialog" ? "fixed inset-0 z-[100] bg-background/72 backdrop-blur-sm" : "h-full min-h-40"
      )}
    >
      <LoadingIndicator label={`正在加载${label}`} />
    </div>
  )
}

function LazyFailure({ label, variant }: Omit<LazyBoundaryProps, "children">) {
  return (
    <div
      role="alert"
      className={cn(
        "flex flex-col items-center justify-center gap-3 px-5 text-center",
        variant === "dialog" ? "fixed inset-0 z-[100] bg-background/92 backdrop-blur-sm" : "h-full min-h-40"
      )}
    >
      <div>
        <div className="text-sm font-medium text-foreground">{label}加载失败</div>
        <div className="mt-1 text-xs text-muted-foreground">资源可能已更新，刷新后即可继续。</div>
      </div>
      <Button type="button" size="sm" variant="outline" onClick={() => window.location.reload()}>
        <RefreshCw className="h-3.5 w-3.5" />
        刷新重试
      </Button>
    </div>
  )
}
