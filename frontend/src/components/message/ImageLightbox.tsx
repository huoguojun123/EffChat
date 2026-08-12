import { useCallback, useEffect, useRef, useState, type PointerEvent as ReactPointerEvent } from "react"
import { Download, Minus, Plus, RotateCcw, X } from "lucide-react"
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { LatestOperationOwner } from "@/lib/latestOperation"

interface Props {
  open: boolean
  url: string
  filename: string
  onOpenChange: (open: boolean) => void
  onDownload?: (signal?: AbortSignal) => void | Promise<void>
}

const MIN_SCALE = 0.5
const MAX_SCALE = 4

export function ImageLightbox({ open, url, filename, onOpenChange, onDownload }: Props) {
  const [scale, setScale] = useState(1)
  const [offset, setOffset] = useState({ x: 0, y: 0 })
  const [dragging, setDragging] = useState(false)
  const [downloading, setDownloading] = useState(false)
  const [downloadError, setDownloadError] = useState<string | null>(null)
  const dragRef = useRef<{ pointerId: number; x: number; y: number; originX: number; originY: number } | null>(null)
  const downloadOwnerRef = useRef(new LatestOperationOwner())

  const reset = useCallback(() => {
    setScale(1)
    setOffset({ x: 0, y: 0 })
  }, [])

  const close = useCallback(() => {
    downloadOwnerRef.current.cancel()
    setDownloading(false)
    setDownloadError(null)
    reset()
    onOpenChange(false)
  }, [onOpenChange, reset])

  function setZoom(next: number) {
    const value = Math.min(MAX_SCALE, Math.max(MIN_SCALE, next))
    setScale(value)
    if (value <= 1) setOffset({ x: 0, y: 0 })
  }

  function handlePointerDown(event: ReactPointerEvent<HTMLImageElement>) {
    if (scale <= 1) return
    event.currentTarget.setPointerCapture(event.pointerId)
    dragRef.current = { pointerId: event.pointerId, x: event.clientX, y: event.clientY, originX: offset.x, originY: offset.y }
    setDragging(true)
  }

  function handlePointerMove(event: ReactPointerEvent<HTMLImageElement>) {
    const drag = dragRef.current
    if (!drag || drag.pointerId !== event.pointerId) return
    setOffset({ x: drag.originX + event.clientX - drag.x, y: drag.originY + event.clientY - drag.y })
  }

  function handlePointerUp(event: ReactPointerEvent<HTMLImageElement>) {
    if (dragRef.current?.pointerId === event.pointerId) {
      dragRef.current = null
      setDragging(false)
    }
  }

  async function download() {
    const operation = downloadOwnerRef.current.begin()
    setDownloading(true)
    setDownloadError(null)
    try {
      await (onDownload ? onDownload(operation.signal) : Promise.resolve(downloadUrl(url, filename)))
    } catch (err) {
      if (!operation.signal.aborted && downloadOwnerRef.current.owns(operation)) setDownloadError(err instanceof Error ? err.message : "下载失败，请稍后重试")
    } finally {
      if (downloadOwnerRef.current.release(operation)) {
        setDownloading(false)
      }
    }
  }

  useEffect(() => {
    if (!open) return
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "+" || event.key === "=") setZoom(scale + 0.25)
      if (event.key === "-") setZoom(scale - 0.25)
      if (event.key === "0") reset()
      if (event.key === "Escape") {
        close()
      }
    }
    window.addEventListener("keydown", handleKeyDown)
    return () => window.removeEventListener("keydown", handleKeyDown)
  }, [close, open, reset, scale])

  return (
    <Dialog open={open} onOpenChange={(nextOpen) => {
      if (!nextOpen) close()
      else onOpenChange(true)
    }}>
      <DialogContent
        showClose={false}
        className="!left-0 !top-0 h-[100dvh] w-screen !max-w-none !translate-x-0 !translate-y-0 overflow-hidden rounded-none border-0 bg-black/92 p-0 text-white shadow-none"
      >
        <DialogTitle className="sr-only">{filename}</DialogTitle>
        <DialogDescription className="sr-only">全屏查看图片，可缩放、拖拽、复位或下载。</DialogDescription>
        <div className="absolute inset-x-0 top-0 z-20 flex min-h-14 items-center gap-2 bg-gradient-to-b from-black/65 to-transparent px-[max(0.75rem,env(safe-area-inset-left))] pb-3 pt-[max(0.75rem,env(safe-area-inset-top))]">
          <div className="min-w-0 flex-1 truncate text-sm font-medium" title={filename}>{filename}</div>
          <div className="flex shrink-0 items-center gap-1">
            <LightboxButton label="缩小" onClick={() => setZoom(scale - 0.25)} disabled={scale <= MIN_SCALE}><Minus /></LightboxButton>
            <button type="button" className="h-9 min-w-14 rounded-md px-2 text-xs tabular-nums text-white/80 hover:bg-white/10" onClick={reset} aria-label="复位缩放">
              {Math.round(scale * 100)}%
            </button>
            <LightboxButton label="放大" onClick={() => setZoom(scale + 0.25)} disabled={scale >= MAX_SCALE}><Plus /></LightboxButton>
            <LightboxButton label="适应屏幕" onClick={reset}><RotateCcw /></LightboxButton>
            <LightboxButton label="下载图片" onClick={() => void download()} disabled={downloading}>{downloading ? <span className="h-4 w-4 animate-spin rounded-full border-2 border-current border-t-transparent" /> : <Download />}</LightboxButton>
            <LightboxButton label="关闭" onClick={() => {
              close()
            }}><X /></LightboxButton>
          </div>
        </div>
        {downloadError ? <div role="alert" className="absolute inset-x-3 top-16 z-20 mx-auto max-w-xl rounded-md bg-rose-950/90 px-3 py-2 text-center text-xs text-rose-100 shadow-lg">下载失败：{downloadError}</div> : null}

        <div className="flex h-full w-full items-center justify-center overflow-hidden px-3 pb-[max(0.75rem,env(safe-area-inset-bottom))] pt-16 sm:px-8">
          <img
            src={url}
            alt={filename}
            draggable={false}
            className="max-h-full max-w-full select-none object-contain transition-transform duration-150 ease-out motion-reduce:transition-none"
            style={{
              cursor: scale > 1 ? (dragging ? "grabbing" : "grab") : "zoom-in",
              transform: `translate3d(${offset.x}px, ${offset.y}px, 0) scale(${scale})`,
              touchAction: "none",
            }}
            onDoubleClick={() => scale === 1 ? setZoom(2) : reset()}
            onPointerDown={handlePointerDown}
            onPointerMove={handlePointerMove}
            onPointerUp={handlePointerUp}
            onPointerCancel={handlePointerUp}
          />
        </div>
      </DialogContent>
    </Dialog>
  )
}

function LightboxButton({ label, onClick, disabled, children }: { label: string; onClick: () => void; disabled?: boolean; children: React.ReactElement }) {
  return (
    <Button
      type="button"
      variant="ghost"
      size="icon"
      className="h-9 w-9 rounded-md text-white/85 hover:bg-white/10 hover:text-white disabled:text-white/30"
      aria-label={label}
      title={label}
      disabled={disabled}
      onClick={onClick}
    >
      <span className="[&>svg]:h-4 [&>svg]:w-4">{children}</span>
    </Button>
  )
}

function downloadUrl(url: string, filename: string) {
  const anchor = document.createElement("a")
  anchor.href = url
  anchor.download = filename
  anchor.click()
}
