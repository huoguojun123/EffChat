import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react"
import { AlertTriangle, Download, FileText, ImageIcon, Loader2, RefreshCw, Search, Trash2 } from "lucide-react"
import { Button } from "@/components/ui/button"
import { canRetryOCR, filesApi, isOCRPending, isOCRRefreshable, type FileInfo } from "@/api/files"
import { cn } from "@/lib/utils"
import { formatBytes, formatTokens } from "@/lib/format"
import { useAuthedBlobUrl } from "@/hooks/useAuthedBlobUrl"
import { ImageLightbox } from "@/components/message/ImageLightbox"
import { DocumentPreview } from "@/components/files/DocumentPreview"
import { WorkspaceWindow } from "@/components/ui/workspace-window"

interface Props {
  sessionId: number | null
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function SessionFilesDrawer({ sessionId, open, onOpenChange }: Props) {
  const [files, setFiles] = useState<FileInfo[]>([])
  const [selectedId, setSelectedId] = useState<number | null>(null)
  const [mobilePreviewOpen, setMobilePreviewOpen] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [retryingID, setRetryingID] = useState<number | null>(null)
  const [query, setQuery] = useState("")
  const ocrRefreshInFlightRef = useRef<Set<number>>(new Set())
  const sessionRef = useRef(sessionId)
  const loadedSessionRef = useRef<number | null>(null)
  const requestRef = useRef(0)
  const selectionRef = useRef<number | null>(null)

  useLayoutEffect(() => {
    sessionRef.current = sessionId
    loadedSessionRef.current = null
    requestRef.current += 1
    queueMicrotask(() => {
      if (sessionRef.current !== sessionId) return
      setFiles([])
      setSelectedId(null)
      selectionRef.current = null
      setQuery("")
      setError(null)
      setRetryingID(null)
    })
  }, [sessionId])

  const filteredFiles = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return files
    return files.filter((file) => file.filename.toLowerCase().includes(q))
  }, [files, query])

  const loadFiles = useCallback(async () => {
    if (!sessionId) return
    const request = ++requestRef.current
    setLoading(true)
    setError(null)
    try {
      const res = await filesApi.list({ sessionId, limit: 100, offset: 0, referenced: true })
      if (request !== requestRef.current || sessionRef.current !== sessionId) return
      loadedSessionRef.current = sessionId
      setFiles(res.files)
      const nextSelected = selectionRef.current && res.files.some((file) => file.id === selectionRef.current)
        ? selectionRef.current
        : res.files[0]?.id ?? null
      setSelectedId(nextSelected)
      selectionRef.current = nextSelected
    } catch (err) {
      if (request !== requestRef.current || sessionRef.current !== sessionId) return
      setError(err instanceof Error ? err.message : "加载文件失败")
    } finally {
      if (request === requestRef.current && sessionRef.current === sessionId) setLoading(false)
    }
  }, [sessionId])

  async function deleteFile(file: FileInfo) {
    if (!sessionId || loadedSessionRef.current !== sessionId || !files.some((item) => item.id === file.id)) return
    const requestedSession = sessionId
    const requestedSelection = selectedId
    try {
      await filesApi.delete(file.id)
      if (sessionRef.current !== requestedSession || loadedSessionRef.current !== requestedSession) return
      setFiles((prev) => prev.filter((item) => item.id !== file.id))
      if (requestedSelection === file.id && selectionRef.current === file.id) {
        const next = files.find((item) => item.id !== file.id)
        setSelectedId(next?.id ?? null)
        selectionRef.current = next?.id ?? null
        setMobilePreviewOpen(false)
      }
    } catch (err) {
      if (sessionRef.current !== requestedSession || loadedSessionRef.current !== requestedSession) return
      setError(err instanceof Error ? err.message : "删除文件失败")
    }
  }

  async function retryOCR(file: FileInfo) {
    if (!sessionId || loadedSessionRef.current !== sessionId || !files.some((item) => item.id === file.id)) return
    const requestedSession = sessionId
    setRetryingID(file.id)
    setError(null)
    try {
      const next = await filesApi.retryOCR(file.id)
      if (sessionRef.current !== requestedSession || loadedSessionRef.current !== requestedSession) return
      setFiles((prev) => prev.map((item) => (item.id === next.id ? next : item)))
    } catch (err) {
      if (sessionRef.current !== requestedSession || loadedSessionRef.current !== requestedSession) return
      setError(err instanceof Error ? err.message : "OCR 重试失败")
    } finally {
      if (sessionRef.current === requestedSession && loadedSessionRef.current === requestedSession) setRetryingID(null)
    }
  }

  useEffect(() => {
    if (!open) return
    queueMicrotask(() => {
      setMobilePreviewOpen(false)
      void loadFiles()
    })
  }, [open, loadFiles])

  useEffect(() => {
    if (!open || !files.some(isOCRRefreshable)) return
    const timer = window.setInterval(() => {
      const requestedSession = sessionId
      for (const file of files.filter(isOCRRefreshable)) {
        if (ocrRefreshInFlightRef.current.has(file.id)) continue
        ocrRefreshInFlightRef.current.add(file.id)
        void filesApi.refreshOCR(file.id).then((next) => {
          if (sessionRef.current !== requestedSession || loadedSessionRef.current !== requestedSession) return
          setFiles((prev) => prev.map((item) => (item.id === next.id ? next : item)))
        }).catch((err) => {
          if (sessionRef.current !== requestedSession || loadedSessionRef.current !== requestedSession) return
          setError(err instanceof Error ? err.message : "OCR 状态刷新失败")
        }).finally(() => {
          ocrRefreshInFlightRef.current.delete(file.id)
        })
      }
    }, 2500)
    return () => window.clearInterval(timer)
  }, [files, open, sessionId])

  const selected = files.find((file) => file.id === selectedId) || null

  return (
    <WorkspaceWindow
      open={open}
      onOpenChange={onOpenChange}
      title="对话附件"
      defaultWidth={1240}
      defaultHeight={820}
      toolbar={(
        <Button variant="ghost" size="icon" className="h-8 w-8 rounded-md" onClick={loadFiles} disabled={loading} aria-label="刷新文件列表">
          {loading ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />}
        </Button>
      )}
    >
      <div role="region" aria-label="会话文件" className="grid h-full min-h-0 grid-cols-1 md:grid-cols-[15rem_minmax(0,1fr)]">
          <aside className={cn("min-h-0 flex-col border-b border-border/70 md:flex md:border-b-0 md:border-r", mobilePreviewOpen ? "hidden" : "flex")}>
            <div className="p-2">
              <label className="flex h-9 items-center gap-2 rounded-md border border-input bg-background pl-3 pr-2 text-sm transition-[border-color,box-shadow] motion-control focus-within:ring-2 focus-within:ring-ring/50">
                <Search className="h-4 w-4 text-muted-foreground" />
                <input
                  value={query}
                  onChange={(event) => setQuery(event.target.value)}
                  placeholder="筛选文件"
                  aria-label="筛选文件"
                  className="min-w-0 flex-1 bg-transparent outline-none placeholder:text-muted-foreground/50"
                />
              </label>
            </div>
            <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-2 scrollbar-thin">
              {error ? <div className="px-2 py-2 text-xs text-rose-500">{error}</div> : null}
              {loading && files.length === 0 ? (
                <div className="flex h-24 items-center justify-center text-xs text-muted-foreground">
                  <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                  加载中
                </div>
              ) : null}
              {!loading && filteredFiles.length === 0 ? (
                <div className="px-2 py-8 text-center text-xs text-muted-foreground">当前会话还没有已发送附件</div>
              ) : null}
              <div className="space-y-1" role="listbox" aria-label="会话文件列表">
                {filteredFiles.map((file) => (
                  <button
                    key={file.id}
                    onClick={() => {
                      setSelectedId(file.id)
                      selectionRef.current = file.id
                      setMobilePreviewOpen(true)
                    }}
                    role="option"
                    aria-selected={selectedId === file.id}
                    className={cn(
                      "flex w-full items-center gap-2 rounded-md px-2.5 py-2 text-left outline-none transition-[background-color,color,box-shadow] motion-control focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/50",
                      selectedId === file.id
                        ? "bg-accent/80 text-accent-foreground"
                        : "hover:bg-muted/60"
                    )}
                  >
                    <FileIcon file={file} />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-sm font-medium">{file.filename}</span>
                      <span className="block truncate text-[11px] text-muted-foreground">
                        {fileStatusLabel(file)}
                        {file.tokenEstimate ? ` · ${formatTokens(file.tokenEstimate)}` : ""}
                        {" · "}{formatBytes(file.size)}
                      </span>
                      {file.extractError ? (
                        <span className="block truncate text-[11px] text-amber-500" title={file.extractError}>
                          解析失败：{file.extractError}
                        </span>
                      ) : null}
                    </span>
                  </button>
                ))}
              </div>
            </div>
          </aside>

          <main className={cn("min-h-0 flex-col md:flex", mobilePreviewOpen ? "flex" : "hidden")}>
            {selected ? (
              <>
                <div className="flex min-h-12 min-w-0 items-center gap-2 border-b border-border/70 px-3">
                  <Button variant="ghost" size="sm" className="h-8 shrink-0 px-2 md:hidden" onClick={() => setMobilePreviewOpen(false)} aria-label="返回文件列表">
                    文件
                  </Button>
                  <span className="shrink-0"><FileIcon file={selected} /></span>
                  <div className="min-w-0 flex-1 overflow-hidden">
                    <div className="truncate text-sm font-medium">{selected.filename}</div>
                    <div className="truncate text-[11px] text-muted-foreground">
                      {selected.content_type} · {formatBytes(selected.size)}
                      {selected.tokenEstimate ? ` · ${formatTokens(selected.tokenEstimate)}` : ""}
                    </div>
                  </div>
                  <Button variant="ghost" size="icon" className="h-11 w-11 shrink-0 rounded-md sm:h-8 sm:w-8" onClick={() => filesApi.downloadBlob(selected.id, selected.filename)} aria-label={`下载文件：${selected.filename}`}>
                    <Download className="h-4 w-4" />
                  </Button>
                  <Button variant="ghost" size="icon" className="h-11 w-11 shrink-0 rounded-md text-rose-500 hover:text-rose-600 sm:h-8 sm:w-8" onClick={() => {
                    if (window.confirm("删除后，历史消息中的预览、下载和重新发送将不可用。确定删除吗？")) void deleteFile(selected)
                  }} aria-label={`删除文件：${selected.filename}`}>
                    <Trash2 className="h-4 w-4" />
                  </Button>
                </div>
                <div className="min-h-0 flex-1 overflow-auto bg-muted/10 p-3 sm:p-5 scrollbar-thin">
                  {selected.extractError ? (
                    <div className="mb-3 flex items-start gap-2 rounded-md border border-amber-500/30 bg-amber-500/10 p-3 text-xs leading-5 text-amber-700 dark:text-amber-300">
                      <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
                      <span className="min-w-0 flex-1 break-words">解析失败：{selected.extractError}</span>
                      {canRetryOCR(selected) ? (
                        <Button type="button" size="sm" variant="outline" className="h-7 shrink-0 px-2 text-xs" disabled={retryingID === selected.id} onClick={() => void retryOCR(selected)}>
                          {retryingID === selected.id ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : "重试"}
                        </Button>
                      ) : null}
                    </div>
                  ) : null}
                  {isOCRPending(selected) ? (
                    <div className="flex h-32 items-center justify-center rounded-md border border-sky-500/25 bg-sky-500/10 text-sm text-sky-700 dark:text-sky-300">
                      <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                      OCR 解析中，完成后会自动显示文本
                    </div>
                  ) : selected.content_type.startsWith("image/") ? (
                    <FileDrawerImage file={selected} />
                  ) : (
                    <DocumentPreview key={`${selected.id}:${selected.extractStatus || ""}`} fileId={selected.id} className="mx-auto w-full max-w-[1100px]" />
                  )}
                </div>
              </>
            ) : (
              <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">选择一个文件查看预览</div>
            )}
          </main>
      </div>
    </WorkspaceWindow>
  )
}

function FileDrawerImage({ file }: { file: FileInfo }) {
  const { url, loading, error } = useAuthedBlobUrl(file.id)
  const [open, setOpen] = useState(false)

  if (loading) {
    return <div className="h-64 w-full animate-pulse rounded-md bg-muted" aria-label="图片加载中" />
  }
  if (error || !url) {
    return <div className="py-12 text-center text-sm text-muted-foreground">图片预览加载失败</div>
  }

  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="flex min-h-64 w-full items-center justify-center rounded-lg bg-muted/35 p-3 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        aria-label={`查看图片：${file.filename}`}
      >
        <img src={url} alt={file.filename} className="max-h-[calc(82dvh-9rem)] max-w-full object-contain" />
      </button>
      <ImageLightbox
        open={open}
        url={url}
        filename={file.filename}
        onOpenChange={setOpen}
        onDownload={() => filesApi.downloadBlob(file.id, file.filename)}
      />
    </>
  )
}

function FileIcon({ file }: { file: FileInfo }) {
  const isImage = file.content_type.startsWith("image/")
  const Icon = isImage ? ImageIcon : FileText
  return (
    <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground">
      <Icon className="h-4 w-4" />
    </span>
  )
}

function fileStatusLabel(file: FileInfo): string {
  if (file.content_type.startsWith("image/")) return "图片"
  if (file.extractStatus === "ready" && file.tokenEstimate === 0) return "无文本"
  if (file.extractStatus === "ready") return "已解析"
  if (file.extractStatus === "ocr_pending" || file.extractStatus === "ocr_running") {
    return file.ocrPageCount ? `OCR ${file.ocrProgressPages || 0}/${file.ocrPageCount}` : "OCR 解析中"
  }
  if (file.extractStatus === "pending") return "解析中"
  if (file.extractStatus === "failed") return "解析失败"
  return file.extractStatus || "待解析"
}
