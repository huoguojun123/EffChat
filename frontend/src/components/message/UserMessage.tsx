import { useCallback, useLayoutEffect, useRef, useState, type KeyboardEvent } from "react"
import { RotateCcw, ChevronDown, ChevronUp, Pencil } from "lucide-react"
import type { Message } from "@/types"
import { useSSE } from "@/hooks/useSSE"
import { useChatStore } from "@/stores/chat"
import { parseAttachments } from "@/lib/attachments"
import { cn } from "@/lib/utils"
import { AttachmentCard } from "@/components/message/AttachmentCard"
import { isStreamingInteractionBusy } from "@/lib/streamingStatus"
import { prefersReducedMotion } from "@/lib/motionPreference"
import { editableTailUserMessageId } from "@/lib/chatMessages"
import { getComposerTextareaMinHeight, getComposerTextareaMaxHeight } from "@/components/chat/ChatInput.constants"

interface Props {
  message: Message
  isLastUserRetryable?: boolean
  isEditableTail?: boolean
  stopsZeroOutputRun?: boolean
}

// 折叠态的最大高度（px）。超过此高度的用户消息默认折叠，避免长粘贴占满整屏。
const COLLAPSED_MAX_PX = 336
// 收起时把这条消息顶部锚到视口顶部下方的间距（px），让焦点落在消息开头而非飘走。
const COLLAPSE_TOP_GAP = 16

export function UserMessage({
  message,
  isLastUserRetryable = false,
  isEditableTail = false,
  stopsZeroOutputRun = false,
}: Props) {
  const { retryMessage, editRetryMessage, abort } = useSSE()
  const streamingStatus = useChatStore((s) => s.streaming.status)
  const isStreaming = isStreamingInteractionBusy(streamingStatus)
  const [retryError, setRetryError] = useState<string | null>(null)
  const [retrying, setRetrying] = useState(false)
  const [editing, setEditing] = useState(false)
  const [editContent, setEditContent] = useState(message.message_data.content)
  const [editError, setEditError] = useState<string | null>(null)
  const [savingEdit, setSavingEdit] = useState(false)
  const editRef = useRef<HTMLTextAreaElement>(null)

  const { files, text } = parseAttachments(message.message_data.content, message.message_data.attachments)

  async function handleRetry() {
    if (isStreaming || retrying) return
    setRetryError(null)
    setRetrying(true)
    try {
      await retryMessage(message.session_id, message.id)
    } catch (err) {
      setRetryError(err instanceof Error ? err.message : "重试失败")
    } finally {
      setRetrying(false)
    }
  }

  const resizeEditTextarea = useCallback(() => {
    const element = editRef.current
    if (!element) return
    element.style.height = "0px"
    const contentHeight = element.scrollHeight
    const minHeight = getComposerTextareaMinHeight(element.ownerDocument)
    const maxHeight = getComposerTextareaMaxHeight(window.innerWidth)
    element.style.height = `${Math.min(Math.max(contentHeight, minHeight), maxHeight)}px`
    element.style.overflowY = contentHeight > maxHeight ? "auto" : "hidden"
  }, [])

  useLayoutEffect(() => {
    if (!editing) return
    resizeEditTextarea()
    editRef.current?.focus()
  }, [editContent, editing, resizeEditTextarea])

  async function handleBeginEdit() {
    if (!isEditableTail || savingEdit) return
    setEditError(null)
    if (stopsZeroOutputRun) {
      await abort()
      if (editableTailUserMessageId(useChatStore.getState().messages) !== message.id) return
    }
    setEditContent(message.message_data.content)
    setEditing(true)
  }

  async function handleSaveEdit() {
    if (savingEdit || editContent === message.message_data.content) return
    if (!editContent.trim() && files.length === 0) return
    setEditError(null)
    setSavingEdit(true)
    try {
      await editRetryMessage(message.session_id, message.id, editContent)
      setEditing(false)
    } catch (err) {
      setEditError(err instanceof Error ? err.message : "保存失败")
    } finally {
      setSavingEdit(false)
    }
  }

  function handleEditKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.nativeEvent.isComposing) return
    if (event.key === "Escape") {
      event.preventDefault()
      setEditing(false)
      setEditError(null)
      return
    }
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault()
      void handleSaveEdit()
    }
  }

  const unchangedEdit = editContent === message.message_data.content
  const emptyEdit = !editContent.trim() && files.length === 0

  return (
    <div className="flex justify-end py-3 pl-10">
      <div className="max-w-[85%] space-y-1.5">
        {files.length > 0 && (
          <div className="flex flex-wrap justify-end gap-1.5">
            {files.map((f, i) => (
              <AttachmentCard key={f.file_id || i} att={f} />
            ))}
          </div>
        )}
        {editing ? (
          <div className="flex min-w-[min(72vw,38rem)] flex-col items-end gap-2">
            <textarea
              ref={editRef}
              value={editContent}
              onChange={(event) => setEditContent(event.target.value)}
              onKeyDown={handleEditKeyDown}
              aria-label="编辑最后一条消息"
              className="w-full resize-none rounded-[18px] border border-border/60 bg-popover px-4 py-2 text-[15px] leading-relaxed shadow-sm outline-none transition-[border-color,box-shadow] motion-control focus:border-primary/45 focus:ring-2 focus:ring-primary/10"
            />
            <div className="flex items-center gap-1.5">
              <button
                type="button"
                onClick={() => {
                  setEditing(false)
                  setEditError(null)
                }}
                disabled={savingEdit}
                className="h-8 rounded-md px-2.5 text-xs font-medium text-muted-foreground transition-colors motion-control hover:bg-muted/50 hover:text-foreground disabled:opacity-40"
              >
                取消
              </button>
              <button
                type="button"
                onClick={() => void handleSaveEdit()}
                disabled={savingEdit || unchangedEdit || emptyEdit}
                className="h-8 rounded-md bg-foreground px-3 text-xs font-medium text-background transition-opacity motion-control hover:opacity-85 disabled:pointer-events-none disabled:opacity-35"
              >
                {savingEdit ? "保存中" : "保存并重新生成"}
              </button>
            </div>
            {editError && <span role="alert" aria-live="polite" className="text-xs text-rose-600 dark:text-rose-400">{editError}</span>}
          </div>
        ) : text ? <UserText text={text} /> : null}
        {!editing && (isLastUserRetryable || isEditableTail) ? (
          <div className="flex flex-col items-end gap-0.5">
            <div className="flex items-center">
              {isEditableTail ? (
                <button
                  type="button"
                  onClick={() => void handleBeginEdit()}
                  disabled={savingEdit}
                  className="inline-flex h-8 items-center gap-1.5 rounded-md px-2 text-xs font-medium text-muted-foreground transition-colors motion-control hover:bg-muted/50 hover:text-foreground disabled:pointer-events-none disabled:opacity-40"
                >
                  <Pencil className="h-3.5 w-3.5" />
                  <span>编辑</span>
                </button>
              ) : null}
              {isLastUserRetryable ? (
                <button
                  type="button"
                  onClick={handleRetry}
                  disabled={isStreaming || retrying}
                  className="inline-flex h-8 items-center gap-1.5 rounded-md px-2 text-xs font-medium text-muted-foreground transition-colors motion-control hover:bg-muted/50 hover:text-foreground disabled:pointer-events-none disabled:opacity-40"
                >
                  <RotateCcw className={`h-3.5 w-3.5 ${retrying ? "animate-spin motion-reduce:animate-none" : ""}`} />
                  <span>{retrying ? "重试中" : "重试"}</span>
                </button>
              ) : null}
            </div>
            {retryError && <span role="alert" aria-live="polite" className="text-xs text-rose-600 dark:text-rose-400">{retryError}</span>}
          </div>
        ) : null}
        {message.local_state === "pending" || message.local_state === "streaming" || message.local_state === "finalizing" ? (
          <div className="text-right text-xs text-muted-foreground">等待服务端确认</div>
        ) : null}
        {message.local_state === "failed_local" ? (
          <div className="text-right text-xs text-rose-600 dark:text-rose-400">
            {message.local_error || "本次回复未能完成，可重试最后一条消息"}
          </div>
        ) : null}
      </div>
    </div>
  )
}

// UserText 渲染用户气泡正文：emoji 走 Twemoji；超长时默认折叠（lobehub 风格）。
// 折叠/展开用 max-height 过渡 + 底部渐变遮罩 + 居中按钮，动画时序与思考过程一致
// 先测量真实高度，仅当确实超出折叠高度时才显示按钮。
function UserText({ text }: { text: string }) {
  const rootRef = useRef<HTMLDivElement>(null)
  const innerRef = useRef<HTMLDivElement>(null)
  const [fullHeight, setFullHeight] = useState(0)
  const [collapsed, setCollapsed] = useState(true)

  useLayoutEffect(() => {
    if (innerRef.current) setFullHeight(innerRef.current.scrollHeight)
  }, [text])

  const isLong = fullHeight > COLLAPSED_MAX_PX
  const showCollapsed = isLong && collapsed
  // 已测量时用精确高度做过渡终点；未测量（首帧）放开高度避免裁切。
  const maxHeight = !isLong ? "none" : collapsed ? `${COLLAPSED_MAX_PX}px` : `${fullHeight}px`

  // 收起时若消息顶部已滚出视口上方，平滑把它锚回视口顶部，
  // 避免高度骤减导致下方内容上移、焦点跳到收起后的奇怪位置。
  const handleToggle = () => {
    setCollapsed((prev) => {
      const next = !prev
      if (next && rootRef.current) {
        const container = rootRef.current.closest<HTMLElement>("[data-chat-scroll-container]")
        const rootTop = rootRef.current.getBoundingClientRect().top
        const containerTop = container?.getBoundingClientRect().top ?? 0
        if (container && rootTop < containerTop) {
          const target = container.scrollTop + (rootTop - containerTop) - COLLAPSE_TOP_GAP
          requestAnimationFrame(() => container.scrollTo({ top: target, behavior: prefersReducedMotion() ? "auto" : "smooth" }))
        }
      }
      return next
    })
  }

  return (
    <div ref={rootRef} className="space-y-1">
      <div className="relative">
        <div
          className="overflow-hidden transition-[max-height] motion-panel"
          style={{ maxHeight }}
        >
          <div
            ref={innerRef}
            className="rounded-2xl border border-border/40 bg-popover px-5 py-3 text-[15px] leading-relaxed shadow-sm whitespace-pre-wrap"
          >
            {text}
          </div>
        </div>
        <div
          className={cn(
            "pointer-events-none absolute inset-x-0 bottom-0 h-12 rounded-b-2xl bg-gradient-to-t from-popover to-transparent transition-opacity motion-panel",
            showCollapsed ? "opacity-100" : "opacity-0"
          )}
        />
      </div>
      {isLong && (
        <div className="flex justify-center">
          <button
            onClick={handleToggle}
            className="inline-flex items-center gap-1 px-2 py-0.5 text-xs text-muted-foreground transition-colors motion-control hover:text-foreground"
          >
            {collapsed ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronUp className="h-3.5 w-3.5" />}
            <span>{collapsed ? "展开" : "收起"}</span>
          </button>
        </div>
      )}
    </div>
  )
}
