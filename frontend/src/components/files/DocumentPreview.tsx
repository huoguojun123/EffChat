import { useCallback, useEffect, useRef, useState } from "react"
import { Loader2, RotateCw } from "lucide-react"
import { ApiError } from "@/api/client"
import { filesApi } from "@/api/files"
import { MarkdownContent } from "@/components/message/MarkdownContent"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

interface Props {
  fileId: number
  className?: string
}

export function DocumentPreview({ fileId, className }: Props) {
  const [content, setContent] = useState("")
  const [cursor, setCursor] = useState("")
  const [hasMore, setHasMore] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [canRetry, setCanRetry] = useState(false)
  const requestRef = useRef(0)

  const loadChunk = useCallback(async (nextCursor: string) => {
    const request = ++requestRef.current
    setLoading(true)
    setError("")
    setCanRetry(false)
    try {
      const preview = await filesApi.preview(fileId, 16000, nextCursor)
      if (request !== requestRef.current) return
      setContent((previous) => nextCursor ? previous + preview.content : preview.content)
      setCursor(preview.nextCursor)
      setHasMore(preview.hasMore)
      const responseError = preview.code === "file_text_unavailable" ? "暂无可预览内容" : preview.error
      setError(responseError || (!nextCursor && !preview.content ? "暂无可预览内容" : ""))
      setCanRetry(Boolean(responseError) && preview.retryable === true)
    } catch (err) {
      if (request !== requestRef.current) return
      const message = err instanceof Error ? err.message : "读取预览失败"
      setError(err instanceof ApiError && err.code === "file_not_found" ? "附件已删除" : message)
      setCanRetry(err instanceof ApiError ? err.retryable !== false : true)
    } finally {
      if (request === requestRef.current) setLoading(false)
    }
  }, [fileId])

  useEffect(() => {
    const generation = ++requestRef.current
    queueMicrotask(() => {
      if (generation !== requestRef.current) return
      setContent("")
      setCursor("")
      setHasMore(false)
      setError("")
      setCanRetry(false)
      void loadChunk("")
    })
    return () => {
      requestRef.current += 1
    }
  }, [fileId, loadChunk])

  return (
    <div className={cn("min-w-0", className)}>
      {content ? (
        <MarkdownContent content={content} ownerKey={`document-preview-${fileId}`} allowArtifactPreviews={false} variant="document" />
      ) : loading ? (
        <div className="flex min-h-40 items-center justify-center text-sm text-muted-foreground">
          <Loader2 className="mr-2 h-4 w-4 animate-spin motion-reduce:animate-none" />
          读取解析文本
        </div>
      ) : null}

      {error ? (
        <div role="status" className={cn("mt-3 flex items-center justify-between gap-3 rounded-md border border-border/70 bg-muted/25 px-3 py-2 text-xs text-muted-foreground", !content && "mt-0 min-h-24")}>
          <span className="min-w-0 break-words">{error}</span>
          {!loading && canRetry ? (
            <Button type="button" size="sm" variant="ghost" className="h-7 shrink-0 px-2 text-xs" onClick={() => void loadChunk(cursor)}>
              <RotateCw className="mr-1.5 h-3.5 w-3.5" />
              重试
            </Button>
          ) : null}
        </div>
      ) : null}

      {!error && (hasMore || (loading && content)) ? (
        <div className="flex justify-center pt-4">
          <Button type="button" size="sm" variant="outline" className="h-8 px-3 text-xs" disabled={loading} onClick={() => void loadChunk(cursor)}>
            {loading ? <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin motion-reduce:animate-none" /> : null}
            继续加载
          </Button>
        </div>
      ) : null}
    </div>
  )
}
