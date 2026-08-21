import type { ReactNode } from "react"
import { AlertTriangle, Check, FileText, Gauge, ImageIcon, Loader2, X } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import type { FileInfo } from "@/api/files"
import type { Message, Model, SkillDefinition } from "@/types"
import { useAuthedBlobUrl } from "@/hooks/useAuthedBlobUrl"
import { cn } from "@/lib/utils"
import { formatBytes, formatTokens } from "@/lib/format"
import { getCachedTokens, getCacheHitRate, getReasoningTokens } from "@/lib/usage"

export function ContextStatusButton({ messages, model }: { messages: Message[]; model?: Model }) {
  const latestUsage = [...messages]
    .reverse()
    .map((item) => item.message_data.response_meta?.usage)
    .find(Boolean)
  const latestRuntime = [...messages]
    .reverse()
    .map((item) => item.message_data.runtime)
    .find(Boolean)
  const totalTokens = latestUsage?.total_tokens || 0
  const cachedTokens = getCachedTokens(latestUsage)
  const reasoningTokens = getReasoningTokens(latestUsage)
  const cacheHitRate = getCacheHitRate(latestUsage)
  const contextWindow = model?.context_window || 0
  const remaining = contextWindow > 0 ? Math.max(contextWindow - totalTokens, 0) : 0

  if (!latestUsage && !contextWindow) return null

  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button
          size="icon"
          variant="ghost"
          className="h-11 w-11 shrink-0 rounded-[12px] text-muted-foreground transition-colors motion-control hover:bg-accent hover:text-foreground sm:h-9 sm:w-9"
          title="上下文与用量"
          aria-label="上下文与用量"
        >
          <Gauge className="h-3.5 w-3.5" aria-hidden="true" />
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" side="top" className="w-64 p-2 text-xs">
        <div className="grid gap-1.5">
          {contextWindow > 0 && (
            <ContextLine label="上下文" value={`${formatTokens(totalTokens)} / ${formatTokens(contextWindow)}`} />
          )}
          {contextWindow > 0 && <ContextLine label="剩余" value={formatTokens(remaining)} />}
          {latestUsage && (
            <>
              <ContextLine label="输入" value={formatTokens(latestUsage.prompt_tokens)} />
              <ContextLine label="输出" value={formatTokens(latestUsage.completion_tokens)} />
              <ContextLine label="总计" value={formatTokens(latestUsage.total_tokens)} />
              {cachedTokens > 0 && (
                <ContextLine label="缓存" value={`${formatTokens(cachedTokens)} · ${(cacheHitRate * 100).toFixed(0)}%`} />
              )}
              {reasoningTokens > 0 && (
                <ContextLine label="推理" value={formatTokens(reasoningTokens)} />
              )}
            </>
          )}
          {latestRuntime?.tokens_per_second ? (
            <ContextLine label="速度" value={`${latestRuntime.tokens_per_second.toFixed(1)} token/秒`} />
          ) : null}
        </div>
      </PopoverContent>
    </Popover>
  )
}

function ContextLine({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-3">
      <span className="text-muted-foreground">{label}</span>
      <span className="font-medium text-foreground">{value}</span>
    </div>
  )
}

export function MenuItem({
  icon,
  label,
  hint,
  active,
  disabled,
  trailing,
  onClick,
}: {
  icon: ReactNode
  label: string
  hint?: string
  active?: boolean
  disabled?: boolean
  trailing?: ReactNode
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      className={cn(
        "flex w-full items-center gap-2.5 rounded-[8px] px-2.5 py-2 text-left text-sm font-medium transition-[background-color,color,box-shadow] duration-200 motion-control",
        "hover:bg-foreground/5 hover:shadow-[0_1px_2px_rgba(0,0,0,0.02)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/50 disabled:pointer-events-none disabled:opacity-50",
        active ? "text-foreground" : "text-muted-foreground"
      )}
    >
      <span aria-hidden="true" className={cn("shrink-0", active && "text-primary")}>{icon}</span>
      <span className="flex-1 truncate text-foreground/90">{label}</span>
      {hint && <span className="text-xs text-muted-foreground/70">{hint}</span>}
      {trailing ? <span aria-hidden="true">{trailing}</span> : null}
    </button>
  )
}

export function SkillMenuItem({
  skill,
  active,
  onClick,
}: {
  skill: SkillDefinition
  active: boolean
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "flex min-h-10 w-full items-center gap-2.5 rounded-[8px] px-2.5 py-2 text-left text-sm font-medium transition-[background-color,color,box-shadow] duration-200 motion-control",
        "hover:bg-foreground/5 hover:shadow-[0_1px_2px_rgba(0,0,0,0.02)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/50",
        active ? "text-foreground" : "text-muted-foreground"
      )}
    >
      <span className={cn("flex h-4 w-4 shrink-0 items-center justify-center", active ? "text-primary" : "text-transparent")}>
        <Check className="h-4 w-4" />
      </span>
      <span className="min-w-0 flex-1 truncate font-medium">{skill.name}</span>
    </button>
  )
}

export function FileChip({ file, onRemove, onRetry }: { file: FileInfo; onRemove: () => void; onRetry?: () => void }) {
  const isImage = file.content_type.startsWith("image/")
  const { url } = useAuthedBlobUrl(isImage ? file.id : undefined)
  const Icon = isImage ? ImageIcon : FileText
  const tokenLabel = file.tokenEstimate && file.tokenEstimate > 0 ? formatTokens(file.tokenEstimate) : ""
  let statusLabel = ""
  if (!isImage && file.extractStatus === "ready") {
    statusLabel = `已解析${tokenLabel ? ` · ${tokenLabel}` : ""}`
  } else if (file.extractStatus === "pending") {
    statusLabel = "解析中"
  } else if (file.extractStatus === "ocr_pending" || file.extractStatus === "ocr_running") {
    const progress = file.ocrPageCount ? ` ${file.ocrProgressPages || 0}/${file.ocrPageCount}` : ""
    statusLabel = progress ? `OCR 中${progress}` : "OCR 排队中"
  } else if (file.extractStatus === "failed") {
    statusLabel = "解析失败"
  }

  return (
    <div
      className="group inline-flex h-9 max-w-full items-center gap-2 rounded-[10px] border border-border/80 bg-background/95 px-2 text-xs shadow-sm transition-shadow motion-control hover:shadow-md"
      title={file.filename}
    >
      {isImage && url ? (
        <img src={url} alt={file.filename} width={24} height={24} className="h-6 w-6 shrink-0 rounded object-cover" />
      ) : (
        <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-[8px] bg-muted/60 text-muted-foreground/80">
          <Icon className="h-3.5 w-3.5" />
        </span>
      )}
      <span className="min-w-0 max-w-[12rem] truncate font-medium text-foreground/85 sm:max-w-[18rem]">{file.filename}</span>
      {file.extractError ? (
        <>
          <span title={file.extractError} className="shrink-0">
            <AlertTriangle className="h-3.5 w-3.5 text-amber-500" />
          </span>
          {onRetry ? (
            <button
              type="button"
              onClick={onRetry}
              className="min-h-8 shrink-0 rounded-[6px] px-1.5 text-xs font-medium text-primary transition-colors motion-control hover:bg-primary/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
            >
              重试
            </button>
          ) : null}
        </>
      ) : file.extractStatus === "ocr_pending" || file.extractStatus === "ocr_running" ? (
        <span className="inline-flex shrink-0 items-center gap-1 text-xs text-sky-600">
          <Loader2 className="h-3.5 w-3.5 animate-spin" />
          {statusLabel}
        </span>
      ) : statusLabel ? (
        <span className="shrink-0 text-xs text-muted-foreground/70">{statusLabel}</span>
      ) : (
        <span className="shrink-0 text-xs text-muted-foreground/60">{formatBytes(file.size)}</span>
      )}
      <button
        type="button"
        onClick={onRemove}
        className="flex h-8 w-8 shrink-0 items-center justify-center rounded-[6px] text-muted-foreground transition-[background-color,color] duration-200 motion-control hover:bg-foreground/10 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
        aria-label={`移除附件：${file.filename}`}
      >
        <X className="h-3 w-3" aria-hidden="true" />
      </button>
    </div>
  )
}
