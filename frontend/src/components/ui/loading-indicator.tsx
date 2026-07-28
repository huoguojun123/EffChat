import { cn } from "@/lib/utils"

export function LoadingIndicator({ label = "正在加载", className }: { label?: string; className?: string }) {
  return (
    <div role="status" aria-live="polite" className={cn("flex items-center justify-center", className)}>
      <span className="relative h-1 w-14 overflow-hidden rounded-full bg-muted/70">
        <span className="loading-indicator-bar absolute inset-y-0 left-0 w-5 rounded-full bg-primary/70" />
      </span>
      <span className="sr-only">{label}</span>
    </div>
  )
}
