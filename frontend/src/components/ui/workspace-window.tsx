import * as DialogPrimitive from "@radix-ui/react-dialog"
import { Maximize2, Minimize2, X } from "lucide-react"
import { useEffect, useState, type CSSProperties, type ReactNode } from "react"
import { cn } from "@/lib/utils"

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: ReactNode
  toolbar?: ReactNode
  children: ReactNode
  className?: string
  contentClassName?: string
  defaultWidth?: number
  defaultHeight?: number
}

const DESKTOP_BREAKPOINT = 768
const VIEWPORT_MARGIN = 16

export function WorkspaceWindow({
  open,
  onOpenChange,
  title,
  toolbar,
  children,
  className,
  contentClassName,
  defaultWidth = 1120,
  defaultHeight = 760,
}: Props) {
  const [mobile, setMobile] = useState(() => typeof window !== "undefined" && window.innerWidth < DESKTOP_BREAKPOINT)
  const [fullscreen, setFullscreen] = useState(false)

  useEffect(() => {
    const handleResize = () => {
      const nextMobile = window.innerWidth < DESKTOP_BREAKPOINT
      setMobile(nextMobile)
      if (nextMobile) setFullscreen(false)
    }
    window.addEventListener("resize", handleResize)
    return () => window.removeEventListener("resize", handleResize)
  }, [])

  const expanded = mobile || fullscreen
  const style: CSSProperties = expanded
    ? { width: "100vw", height: "100dvh" }
    : {
        width: `min(${defaultWidth}px, calc(100vw - ${VIEWPORT_MARGIN * 2}px))`,
        height: `min(${defaultHeight}px, calc(100dvh - ${VIEWPORT_MARGIN * 2}px))`,
      }

  return (
    <DialogPrimitive.Root
      open={open}
      onOpenChange={(nextOpen) => {
        if (!nextOpen) setFullscreen(false)
        onOpenChange(nextOpen)
      }}
    >
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="fixed inset-0 z-[60] bg-black/42 backdrop-blur-[2px] data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 motion-surface" />
        <DialogPrimitive.Content
          style={style}
          data-fullscreen={expanded}
          className={cn(
            "workspace-window-content fixed left-1/2 top-1/2 z-[61] flex -translate-x-1/2 -translate-y-1/2 flex-col overflow-hidden bg-background text-foreground outline-none",
            expanded
              ? "rounded-none border-0"
              : "rounded-lg border border-border/70 shadow-[0_24px_80px_rgba(0,0,0,0.28)]",
            className
          )}
        >
          <header
            data-testid="workspace-titlebar"
            className="flex h-11 shrink-0 select-none items-center gap-2 border-b border-border/70 bg-background/96 px-3"
            onDoubleClick={(event) => {
              if (!mobile && !(event.target as HTMLElement).closest("button")) setFullscreen((value) => !value)
            }}
          >
            <DialogPrimitive.Title className="min-w-0 flex-1 truncate text-sm font-medium leading-5">
              {title}
            </DialogPrimitive.Title>
            <DialogPrimitive.Description className="sr-only">工作窗口</DialogPrimitive.Description>
            {toolbar}
            {!mobile ? (
              <button
                type="button"
                className="window-control"
                onClick={() => setFullscreen((value) => !value)}
                aria-label={fullscreen ? "退出全屏" : "全屏显示"}
                title={fullscreen ? "退出全屏" : "全屏显示"}
              >
                {fullscreen ? <Minimize2 className="h-4 w-4" /> : <Maximize2 className="h-4 w-4" />}
              </button>
            ) : null}
            <DialogPrimitive.Close className="window-control" aria-label="关闭窗口" title="关闭">
              <X className="h-4 w-4" />
            </DialogPrimitive.Close>
          </header>
          <div className={cn("min-h-0 flex-1 overflow-hidden", contentClassName)}>
            {children}
          </div>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  )
}
