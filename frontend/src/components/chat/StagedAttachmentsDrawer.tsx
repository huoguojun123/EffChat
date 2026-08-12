import { useRef, useState } from "react"
import * as DialogPrimitive from "@radix-ui/react-dialog"
import { FileText, ImageIcon, Loader2, Plus, RefreshCw, Trash2, X } from "lucide-react"
import { canRetryOCR, filesApi, type FileInfo, isFileSendBlocked } from "@/api/files"
import { Button } from "@/components/ui/button"
import { useAuthedBlobUrl } from "@/hooks/useAuthedBlobUrl"
import { formatBytes } from "@/lib/format"
import { cn } from "@/lib/utils"
import { ImageLightbox } from "@/components/message/ImageLightbox"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { DocumentPreview } from "@/components/files/DocumentPreview"
import { WorkspaceWindow } from "@/components/ui/workspace-window"

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  files: FileInfo[]
  selected: FileInfo[]
  uploading: boolean
  onUpload: () => void
  onToggle: (id: number) => void
  onDelete: (id: number) => void
  onRetry: (id: number) => Promise<void>
  onRefresh: () => Promise<void>
}

export function StagedAttachmentsDrawer({ open, onOpenChange, files, selected, uploading, onUpload, onToggle, onDelete, onRetry, onRefresh }: Props) {
  const selectedOrder = new Map(selected.map((file, index) => [file.id, index + 1]))
  const [previewFile, setPreviewFile] = useState<FileInfo | null>(null)
  const [pendingDelete, setPendingDelete] = useState<FileInfo | null>(null)
  const returnFocusRef = useRef<HTMLElement | null>(null)

  return (
    <>
      <DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
        <DialogPrimitive.Portal>
          <DialogPrimitive.Overlay className="fixed inset-0 z-50 bg-background/55 backdrop-blur-[2px] data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0" />
          <DialogPrimitive.Content
            aria-describedby={undefined}
            onOpenAutoFocus={() => {
              const active = document.activeElement
              returnFocusRef.current = active instanceof HTMLElement && active !== document.body ? active : null
            }}
            onCloseAutoFocus={(event) => {
              const returnTarget = returnFocusRef.current
              returnFocusRef.current = null
              if (!returnTarget?.isConnected) return

              // The drawer has multiple external openers, so a static DialogTrigger cannot
              // own focus restoration. Return focus to the element that actually opened it.
              event.preventDefault()
              returnTarget.focus()
            }}
            className="fixed inset-y-0 right-0 z-50 flex h-[100dvh] w-full flex-col border border-border/80 bg-background shadow-2xl outline-none data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:slide-out-to-bottom-4 data-[state=open]:slide-in-from-bottom-4 sm:w-[min(34rem,calc(100vw-3rem))] sm:data-[state=closed]:slide-out-to-right-6 sm:data-[state=closed]:slide-out-to-bottom-0 sm:data-[state=open]:slide-in-from-right-6 sm:data-[state=open]:slide-in-from-bottom-0 sm:rounded-l-md"
          >
            <header className="flex min-h-12 items-center gap-2 border-b border-border/70 px-3 pt-[env(safe-area-inset-top)]">
              <DialogPrimitive.Title className="min-w-0 flex-1 text-sm font-medium">暂存附件</DialogPrimitive.Title>
              <Button variant="ghost" size="sm" className="h-11 gap-1.5 px-2 sm:h-8" onClick={onUpload} disabled={uploading}>
                {uploading ? <Loader2 className="h-4 w-4 animate-spin" /> : <Plus className="h-4 w-4" />}
                上传
              </Button>
              <Button variant="ghost" size="icon" className="h-11 w-11 rounded-md sm:h-8 sm:w-8" onClick={() => void onRefresh()} aria-label="刷新暂存附件"><RefreshCw className="h-4 w-4" /></Button>
              <DialogPrimitive.Close asChild>
                <Button variant="ghost" size="icon" className="h-11 w-11 rounded-md sm:h-8 sm:w-8" aria-label="关闭暂存附件"><X className="h-4 w-4" /></Button>
              </DialogPrimitive.Close>
            </header>
            <div className="min-h-0 flex-1 overflow-y-auto p-3 scrollbar-thin">
              {files.length === 0 ? <div className="py-16 text-center text-sm text-muted-foreground">上传的文件会暂存在这里</div> : null}
              <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
                {files.map((file) => <StagedFile key={file.id} file={file} selectedOrder={selectedOrder.get(file.id)} onToggle={() => onToggle(file.id)} onDelete={() => setPendingDelete(file)} onRetry={() => onRetry(file.id)} onPreview={() => setPreviewFile(file)} />)}
              </div>
            </div>
            {files.length > 0 ? <footer className="flex min-h-12 items-center border-t border-border/70 px-3 pb-[env(safe-area-inset-bottom)] text-xs text-muted-foreground">本次已选 {selected.length}/10 个附件 · {selected.filter((file) => file.content_type.startsWith("image/")).length}/4 张图片</footer> : null}
          </DialogPrimitive.Content>
        </DialogPrimitive.Portal>
      </DialogPrimitive.Root>
      <StagedFilePreview file={previewFile} onOpenChange={(nextOpen) => !nextOpen && setPreviewFile(null)} />
      <Dialog open={!!pendingDelete} onOpenChange={(nextOpen) => !nextOpen && setPendingDelete(null)}>
        <DialogContent className="max-w-[calc(100vw-1.5rem)] sm:max-w-sm">
          <DialogHeader>
            <DialogTitle>删除暂存附件？</DialogTitle>
            <DialogDescription>“{pendingDelete?.filename}”会被永久删除，无法恢复。</DialogDescription>
          </DialogHeader>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="secondary" onClick={() => setPendingDelete(null)}>取消</Button>
            <Button type="button" variant="destructive" onClick={() => {
              if (pendingDelete) onDelete(pendingDelete.id)
              setPendingDelete(null)
            }}>删除</Button>
          </div>
        </DialogContent>
      </Dialog>
    </>
  )
}

function StagedFile({ file, selectedOrder, onToggle, onDelete, onRetry, onPreview }: { file: FileInfo; selectedOrder?: number; onToggle: () => void; onDelete: () => void; onRetry: () => Promise<void>; onPreview: () => void }) {
  const blocked = isFileSendBlocked(file)
  const image = file.content_type.startsWith("image/")
  const { url, loading } = useAuthedBlobUrl(image ? file.id : undefined)
  const [retrying, setRetrying] = useState(false)
  const status = file.extractStatus === "ocr_pending" || file.extractStatus === "ocr_running" ? "OCR 解析中" : file.extractStatus === "failed" ? "解析失败" : ""

  return (
    <div className={cn("group relative min-w-0 overflow-hidden rounded-lg border shadow-sm transition-[border-color,box-shadow,opacity] motion-control hover:shadow-md", selectedOrder ? "border-primary/60 bg-primary/5" : "border-border/70 bg-muted/15 hover:border-border", blocked && "opacity-60")}>
      <button type="button" disabled={blocked} onClick={onToggle} className="block w-full text-left outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring disabled:cursor-not-allowed" aria-label={`${selectedOrder ? "取消选择" : "选择"}附件：${file.filename}`}>
        <div className="flex aspect-[4/3] items-center justify-center bg-muted/35 p-2 transition-colors group-hover:bg-muted/50">
          {image && url ? <img src={url} alt="" className="h-full w-full object-contain" /> : image && loading ? <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" /> : image ? <ImageIcon className="h-6 w-6 text-muted-foreground" /> : <FileText className="h-6 w-6 text-muted-foreground" />}
        </div>
        <div className="min-w-0 px-2 py-1.5"><div className="truncate text-xs font-medium">{file.filename}</div><div className={cn("truncate text-[11px] text-muted-foreground", blocked && "text-amber-600")}>{status || formatBytes(file.size)}</div></div>
      </button>
      <button type="button" onClick={onPreview} className="absolute inset-x-1 bottom-[3.4rem] flex h-7 items-center justify-center rounded-md bg-background/80 text-[11px] text-foreground opacity-100 shadow-sm backdrop-blur-sm transition-[background-color,opacity] motion-control hover:bg-background sm:opacity-0 sm:focus:opacity-100 sm:group-hover:opacity-100" aria-label={`预览附件：${file.filename}`}>预览</button>
      {selectedOrder ? <span className="absolute left-1.5 top-1.5 flex h-5 min-w-[20px] items-center justify-center rounded-full bg-primary px-1.5 text-[11px] font-medium text-primary-foreground shadow-sm">{selectedOrder}</span> : null}
      {canRetryOCR(file) ? <button type="button" disabled={retrying} onClick={async () => { setRetrying(true); try { await onRetry() } finally { setRetrying(false) } }} className="absolute bottom-0 right-0 flex h-11 w-11 items-center justify-center rounded-md text-muted-foreground hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50" aria-label={`重试解析：${file.filename}`}><span className="flex h-6 w-6 items-center justify-center rounded-md bg-background/85 shadow-sm"><RefreshCw className={cn("h-3.5 w-3.5", retrying && "animate-spin")} /></span></button> : null}
      <button type="button" onClick={onDelete} className="absolute right-0 top-0 flex h-11 w-11 items-center justify-center rounded-md text-muted-foreground hover:text-rose-600 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50" aria-label={`删除暂存附件：${file.filename}`} title="删除附件"><span className="flex h-6 w-6 items-center justify-center rounded-md bg-background/85 shadow-sm"><Trash2 className="h-3.5 w-3.5" /></span></button>
    </div>
  )
}

function StagedFilePreview({ file, onOpenChange }: { file: FileInfo | null; onOpenChange: (open: boolean) => void }) {
  const image = !!file?.content_type.startsWith("image/")
  const { url, loading, error } = useAuthedBlobUrl(image ? file?.id : undefined)

  if (!file) return null
  if (image && url) return <ImageLightbox open url={url} filename={file.filename} onOpenChange={onOpenChange} onDownload={(signal) => filesApi.downloadBlob(file.id, file.filename, signal)} />
  if (!image) {
    return (
      <WorkspaceWindow
        open
        onOpenChange={onOpenChange}
        title={file.filename}
        defaultWidth={1080}
        defaultHeight={760}
      >
        <div className="h-full overflow-auto bg-muted/10 p-3 sm:p-6 scrollbar-thin">
          <DocumentPreview fileId={file.id} className="mx-auto w-full max-w-[1040px]" />
        </div>
      </WorkspaceWindow>
    )
  }

  return (
    <WorkspaceWindow
      open
      onOpenChange={onOpenChange}
      title={file.filename}
      defaultWidth={900}
      defaultHeight={680}
    >
        <div className="flex h-full items-center justify-center overflow-auto bg-muted/10 p-4 text-sm leading-6">
          {loading ? <div className="flex h-full min-h-[180px] items-center justify-center text-muted-foreground"><Loader2 className="mr-2 h-4 w-4 animate-spin" />读取预览</div> : error ? <div className="text-muted-foreground">附件已删除</div> : <div className="text-muted-foreground">图片预览不可用</div>}
        </div>
    </WorkspaceWindow>
  )
}
