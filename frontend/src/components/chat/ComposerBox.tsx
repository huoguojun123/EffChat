import type { ChangeEvent, ClipboardEvent, KeyboardEvent, RefObject } from "react"
import { AlertTriangle, LoaderCircle, Paperclip, Send, Square } from "lucide-react"
import type { FileInfo } from "@/api/files"
import type { Message, Model, StreamLifecycleState } from "@/types"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"
import { ContextStatusButton } from "./ChatInputParts"

interface ComposerBoxProps {
  input: string
  setInput: (value: string) => void
  textareaRef: RefObject<HTMLTextAreaElement | null>
  attachments: FileInfo[]
  stagedCount: number
  uploadError: string | null
  imageUnsupported?: boolean
  streamingStatus: StreamLifecycleState
  notice: string | null
  noticeAction?: { label: string; onClick: () => void } | null
  attachmentNotice: string | null
  messages: Message[]
  currentModel?: Model
  isStreaming: boolean
  isAbortable: boolean
  canSend: boolean
  onKeyDown: (event: KeyboardEvent<HTMLTextAreaElement>) => void
  onPaste: (event: ClipboardEvent<HTMLTextAreaElement>) => void
  onCompositionStart: () => void
  onCompositionEnd: () => void
  onOpenStaging: () => void
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
  imageUnsupported,
  streamingStatus,
  notice,
  noticeAction,
  attachmentNotice,
  messages,
  currentModel,
  isStreaming,
  isAbortable,
  canSend,
  onKeyDown,
  onPaste,
  onCompositionStart,
  onCompositionEnd,
  onOpenStaging,
  onAbort,
  onSubmit,
}: ComposerBoxProps) {
  const showStatus = attachments.length > 0 || uploadError || imageUnsupported || attachmentNotice || streamingStatus === "recovering" || streamingStatus === "failed_local" || notice

  function handleChange(event: ChangeEvent<HTMLTextAreaElement>) {
    setInput(event.target.value)
  }

  return (
    <div className="min-w-0">
      <div
        data-testid="composer-surface"
        className={cn(
          "relative rounded-[18px] border border-border/80 bg-popover/96 px-3 py-1 shadow-[0_12px_32px_-20px_rgba(0,0,0,0.32),0_2px_8px_rgba(0,0,0,0.06)] transition-[background-color,border-color,box-shadow] motion-control focus-within:border-ring/55 focus-within:bg-popover focus-within:ring-3 focus-within:ring-ring/12"
        )}
      >
        {showStatus && (
          <div role="status" aria-live="polite" className="mb-1 space-y-1.5 pt-1.5 animate-msg-in">
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
            {streamingStatus === "recovering" && (
              <div className="text-xs text-muted-foreground">连接暂时中断，正在确认回答。</div>
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
          className="block box-border h-[var(--chat-composer-height)] w-full resize-none overflow-y-hidden bg-transparent py-[var(--chat-composer-padding-y)] pr-[6.25rem] text-[15px] leading-6 outline-none [font-family:var(--chat-font-family,var(--font-serif))] placeholder:text-muted-foreground/50 sm:pr-[5.6rem]"
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
                  "h-11 w-11 shrink-0 rounded-[12px] shadow-sm transition-[background-color,color,box-shadow] motion-control sm:h-9 sm:w-9",
                  isAbortable && "text-destructive-foreground hover:bg-destructive/10 hover:text-destructive-foreground",
                  isStreaming && !isAbortable && "cursor-wait"
                )}
                disabled={isStreaming ? !isAbortable : !canSend}
                onClick={isAbortable ? onAbort : onSubmit}
                data-testid={isAbortable ? "stop-button" : "send-button"}
                aria-label={isAbortable ? "停止生成" : isStreaming ? "正在确认结果" : "发送消息"}
              >
                {isStreaming && !isAbortable ? (
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
            <TooltipContent>{isAbortable ? "停止生成" : isStreaming ? "正在确认结果" : "发送消息"}</TooltipContent>
          </Tooltip>
        </div>
      </div>
    </div>
  )
}
