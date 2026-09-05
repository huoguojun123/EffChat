import type { ChangeEvent, ClipboardEvent, KeyboardEvent, RefObject } from "react"
import { AlertTriangle, LoaderCircle, Paperclip, Send, Square, X } from "lucide-react"
import type { FileInfo } from "@/api/files"
import type { Message, Model, StreamLifecycleState } from "@/types"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"
import { ContextStatusButton } from "./ChatInputParts"
import type { UploadTask } from "./useAttachmentUploadQueue"
import { summarizeUploadTasks } from "./uploadTaskPresentation"

interface ComposerBoxProps {
  input: string
  setInput: (value: string) => void
  textareaRef: RefObject<HTMLTextAreaElement | null>
  attachments: FileInfo[]
  stagedCount: number
  uploadError: string | null
  uploadTasks: UploadTask[]
  imageUnsupported?: boolean
  streamingStatus: StreamLifecycleState
  notice: string | null
  noticeAction?: { label: string; onClick: () => void } | null
  attachmentNotice: string | null
  messages: Message[]
  currentModel?: Model
  isStreaming: boolean
  isAbortable: boolean
  preparingSend: boolean
  canSend: boolean
  onKeyDown: (event: KeyboardEvent<HTMLTextAreaElement>) => void
  onPaste: (event: ClipboardEvent<HTMLTextAreaElement>) => void
  onCompositionStart: () => void
  onCompositionEnd: () => void
  onOpenStaging: () => void
  onCancelUploads: () => void
  onAbort: () => void
  onSubmit: () => void
}

export function ComposerBox({
  input,
  setInput,
  textareaRef,
  attachments,
  stagedCount,
  uploadError,
  uploadTasks,
  imageUnsupported,
  streamingStatus,
  notice,
  noticeAction,
  attachmentNotice,
  messages,
  currentModel,
  isStreaming,
  isAbortable,
  preparingSend,
  canSend,
  onKeyDown,
  onPaste,
  onCompositionStart,
  onCompositionEnd,
  onOpenStaging,
  onCancelUploads,
  onAbort,
  onSubmit,
}: ComposerBoxProps) {
  const uploadSummary = summarizeUploadTasks(uploadTasks)
  const showStatus = Boolean(preparingSend || streamingStatus === "sending" || uploadSummary || attachments.length > 0 || uploadError || imageUnsupported || attachmentNotice || streamingStatus === "failed_local" || notice)
  const submitLabel = preparingSend
    ? "正在准备消息…"
    : streamingStatus === "sending"
      ? "取消发送"
      : isAbortable
        ? "停止生成"
        : isStreaming
          ? "正在同步结果…"
          : "发送消息"

  function handleChange(event: ChangeEvent<HTMLTextAreaElement>) {
    setInput(event.target.value)
  }

  return (
    <div className="min-w-0">
      <div
        data-testid="composer-surface"
        className={cn(
          "relative rounded-[18px] border border-border/80 bg-popover/96 px-3 py-1 shadow-[0_4px_14px_-10px_rgba(0,0,0,0.22)] transition-[background-color,border-color,box-shadow] motion-control focus-within:border-ring/55 focus-within:bg-popover focus-within:ring-3 focus-within:ring-ring/12"
        )}
      >
        {showStatus && (
          <div role="status" aria-live="polite" className="mb-1 space-y-1.5 pt-1.5 animate-msg-in">
            {preparingSend && <div className="text-xs text-muted-foreground">正在准备消息…</div>}
            {streamingStatus === "sending" && <div className="text-xs text-muted-foreground">正在发送…</div>}
            {uploadSummary && (
              <div className="flex min-h-8 min-w-0 items-center gap-2 rounded-md bg-muted/45 px-2 text-xs text-muted-foreground">
                <LoaderCircle className={`h-3.5 w-3.5 shrink-0 ${uploadSummary.active ? "animate-spin motion-reduce:animate-none" : ""}`} aria-hidden="true" />
                <button type="button" onClick={onOpenStaging} className="min-w-0 flex-1 truncate text-left transition-colors motion-control hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50">
                  {uploadSummary.label}
                </button>
                {uploadSummary.active ? (
                  <button type="button" onClick={onCancelUploads} className="inline-flex h-11 w-11 shrink-0 items-center justify-center rounded-md transition-colors motion-control hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50 min-[769px]:h-7 min-[769px]:w-7" aria-label="取消正在上传的文件">
                    <X className="h-3.5 w-3.5" aria-hidden="true" />
                  </button>
                ) : null}
              </div>
            )}
            {stagedCount > 0 && <button type="button" onClick={onOpenStaging} className="flex min-h-8 items-center gap-1.5 rounded-md px-1 text-xs text-muted-foreground transition-colors motion-control hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50"><Paperclip className="h-3.5 w-3.5" aria-hidden="true" />本次已选 {attachments.length} 个附件，暂存 {stagedCount} 个</button>}
            {imageUnsupported && (
              <div className="flex items-center gap-1.5 text-xs text-amber-700 dark:text-amber-300">
                <AlertTriangle className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
                <span>当前模型不支持图片输入；发送后会作为附件说明处理，不会读图。</span>
              </div>
            )}
            {uploadError && <p className="text-xs text-destructive-foreground">{uploadError}</p>}
            {attachmentNotice && (
              <div className="flex items-center gap-1.5 text-xs text-amber-700 dark:text-amber-300">
                <AlertTriangle className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
                <span>{attachmentNotice}</span>
              </div>
            )}
            {streamingStatus === "failed_local" && (
              <div className="text-xs text-rose-600 dark:text-rose-400">
                本地流已中断，历史会以后端真实落库结果为准。
              </div>
            )}
            {notice && (
              <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                <span className="min-w-0 break-words">{notice}</span>
                {noticeAction && (
                  <Button
                    type="button"
                    variant="secondary"
                    size="sm"
                    className="h-8 rounded-[8px] px-2 text-xs"
                    onClick={noticeAction.onClick}
                  >
                    {noticeAction.label}
                  </Button>
                )}
              </div>
            )}
          </div>
        )}

        <textarea
          ref={textareaRef}
          value={input}
          onChange={handleChange}
          onKeyDown={onKeyDown}
          onPaste={onPaste}
          onCompositionStart={onCompositionStart}
          onCompositionEnd={onCompositionEnd}
          name="message"
          autoComplete="off"
          aria-label="消息输入"
          placeholder="输入消息…"
          className="block box-border h-[var(--chat-composer-height)] w-full resize-none overflow-y-hidden bg-transparent py-[var(--chat-composer-padding-y)] pr-[6.25rem] text-[15px] leading-6 outline-none [font-family:var(--chat-font-family,var(--font-sans))] placeholder:text-muted-foreground/50 sm:pr-[5.6rem]"
          data-testid="chat-input"
        />

        <div className="absolute bottom-1.5 right-2 flex items-center gap-1.5 sm:bottom-2.5">
          <ContextStatusButton messages={messages} model={currentModel} />
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                size="icon"
                variant={isAbortable ? "ghost" : "default"}
                className={cn(
                  "!min-h-0 !min-w-0 h-8 w-8 shrink-0 rounded-[12px] shadow-sm transition-[background-color,color,box-shadow] motion-control",
                  isAbortable && "text-destructive-foreground hover:bg-destructive/10 hover:text-destructive-foreground",
                  isStreaming && !isAbortable && "cursor-wait"
                )}
                disabled={preparingSend || (isStreaming ? !isAbortable : !canSend)}
                onClick={isAbortable ? onAbort : onSubmit}
                data-testid={isAbortable ? "stop-button" : "send-button"}
                aria-label={submitLabel}
              >
                {(preparingSend || (isStreaming && !isAbortable)) ? (
                  <LoaderCircle className="h-4 w-4 animate-spin motion-reduce:animate-none" aria-hidden="true" />
                ) : (
                  <span className="relative block h-4 w-4" aria-hidden="true">
              <Send
                className={cn(
                  "absolute inset-0 h-4 w-4 transition-[opacity,transform] motion-surface",
                  isAbortable ? "scale-75 rotate-45 opacity-0" : "scale-100 rotate-0 opacity-100"
                )}
              />
              <Square
                className={cn(
                  "absolute inset-0 h-4 w-4 fill-current transition-[opacity,transform] motion-surface",
                  isAbortable ? "scale-100 rotate-0 opacity-100" : "scale-75 -rotate-45 opacity-0"
                )}
              />
                  </span>
                )}
              </Button>
            </TooltipTrigger>
            <TooltipContent>{submitLabel}</TooltipContent>
          </Tooltip>
        </div>
      </div>
    </div>
  )
}
