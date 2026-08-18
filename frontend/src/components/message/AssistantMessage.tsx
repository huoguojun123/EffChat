import { memo, useMemo, useState } from "react"
import type { AnswerAttemptNavigation, AssistantSegment, Message } from "@/types"
import { AlertTriangle, Check, ChevronDown, ChevronLeft, ChevronRight, Copy, Loader2, RotateCcw, Trash2 } from "lucide-react"
import { AppLogo } from "@/components/AppLogo"
import { deleteAnswerAttempt, selectAnswerAttempt } from "@/api/messages"
import { MarkdownContent } from "./MarkdownContent"
import { useSSE } from "@/hooks/useSSE"
import { useChatStore } from "@/stores/chat"
import { ToolCallTree } from "./ToolCallTree"
import { ReasoningPanel } from "./ReasoningPanel"
import { assistantErrorDetail, assistantErrorDiagnostic, isErrorAssistant } from "@/lib/chatMessages"
import { isStreamingInteractionBusy } from "@/lib/streamingStatus"
import { getCachedTokens, getCacheHitRate, getReasoningTokens } from "@/lib/usage"
import { formatTokens } from "@/lib/format"
import { groupAssistantSegments } from "./assistantSegments"

interface Props {
  message: Message
  isLastAssistant?: boolean
}

export const AssistantMessage = memo(function AssistantMessage({ message, isLastAssistant = false }: Props) {
  const { content, thinking, tool_calls } = message.message_data
  const [copied, setCopied] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)
  const [retrying, setRetrying] = useState(false)
  const [switchingAttempt, setSwitchingAttempt] = useState<number | null>(null)
  const [deletingAttempt, setDeletingAttempt] = useState(false)
  const { retryMessage } = useSSE()
  const streamingStatus = useChatStore((s) => s.streaming.status)
  const loadMessages = useChatStore((s) => s.loadMessages)
  const isStreaming = isStreamingInteractionBusy(streamingStatus)
  const displayContent = [content?.trim(), thinking?.trim()].filter(Boolean).join("\n\n")
  const segments = useMemo(
    () => message.message_data.segments?.length ? message.message_data.segments : [{ content, thinking, tool_calls }],
    [content, message.message_data.segments, thinking, tool_calls]
  )
  const isError = isErrorAssistant(message)
  const errorDetail = assistantErrorDetail(message)
  const errorDiagnostic = assistantErrorDiagnostic(message)
  const isFailedLocal = message.local_state === "failed_local"
  const isFinalizing = message.local_state === "finalizing"
  const isIncomplete = message.message_data.metadata?.incomplete === true
  const isUnsaved = message.message_data.metadata?.unsaved === true
  const retryBusy = isStreaming || retrying || switchingAttempt !== null || deletingAttempt
  const navigation = message.answer_navigation
  const canSwitchAnswer = Boolean(navigation?.can_switch && navigation.attempt_count > 1 && !isError && !isStreaming)

  function handleCopy() {
    if (!displayContent) return
    navigator.clipboard.writeText(displayContent)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }

  async function handleRetry() {
    if (retryBusy) return
    setActionError(null)
    setRetrying(true)
    try {
      await retryMessage(message.session_id, message.id)
    } catch (err) {
      setActionError(err instanceof Error ? err.message : "重试失败")
    } finally {
      setRetrying(false)
    }
  }

  async function handleAnswerAttemptSelection(attemptId: number) {
    if (retryBusy || attemptId <= 0) return
    setActionError(null)
    setSwitchingAttempt(attemptId)
    let selectionCommitted = false
    try {
      await selectAnswerAttempt(message.session_id, attemptId)
      selectionCommitted = true
      await loadMessages(message.session_id)
    } catch (err) {
      setActionError(selectionCommitted
        ? "回答已切换，但页面未能同步。请刷新页面后继续。"
        : err instanceof Error ? err.message : "切换回答失败")
    } finally {
      setSwitchingAttempt(null)
    }
  }

  async function handleAnswerAttemptDeletion() {
    if (retryBusy || !navigation || navigation.attempt_count <= 1) return
    setActionError(null)
    setDeletingAttempt(true)
    let deletionCommitted = false
    try {
      await deleteAnswerAttempt(message.session_id, navigation.attempt_id)
      deletionCommitted = true
      await loadMessages(message.session_id)
    } catch (err) {
      setActionError(deletionCommitted
        ? "回答已删除，但页面未能同步。请刷新页面后继续。"
        : err instanceof Error ? err.message : "删除回答失败")
    } finally {
      setDeletingAttempt(false)
    }
  }

  return (
    <div className="group py-8">
      <div className="flex items-start gap-0 sm:gap-4">
        <div className="hidden w-11 shrink-0 justify-center pt-1 sm:flex">
          <AppLogo className="h-8 w-8 text-foreground/80" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="w-full space-y-3">
            <MessageDate message={message} />
            {isError ? (
              <ErrorNotice detail={errorDetail} diagnostic={errorDiagnostic} onRetry={isLastAssistant ? handleRetry : undefined} retrying={retryBusy} />
            ) : (
              <>
                {isIncomplete ? <MessageStateLine label="回复未完成，可重试" tone="error" /> : null}
                {isUnsaved ? <MessageStateLine label="回复未保存到服务端；刷新前请复制或重试" tone="error" /> : null}
                {isFailedLocal && !isUnsaved ? <MessageStateLine label={message.local_error || "本地显示失败，后端结果请以下次同步为准"} tone="error" /> : null}
                {isFinalizing ? <MessageStateLine label="正在与服务端结果对齐" tone="muted" /> : null}
                <AssistantSegments messageId={message.id} segments={segments} />
              </>
            )}
            {actionError ? <MessageStateLine label={actionError} tone="error" /> : null}
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div className="flex items-center gap-2">
                {canSwitchAnswer && navigation ? (
                  <AnswerAttemptControls
                    navigation={navigation}
                    switchingAttempt={switchingAttempt}
                    deleting={deletingAttempt}
                    onSelect={handleAnswerAttemptSelection}
                    onDelete={handleAnswerAttemptDeletion}
                  />
                ) : null}
                <ActionButton onClick={handleCopy} label="复制">
                  {copied ? <Check className="h-3.5 w-3.5 text-emerald-500" /> : <Copy className="h-3.5 w-3.5" />}
                </ActionButton>
                {isLastAssistant && !isError ? (
                  <ActionButton onClick={handleRetry} label="重试" disabled={retryBusy}>
                    <RotateCcw className={`h-3.5 w-3.5 ${retrying ? "animate-spin motion-reduce:animate-none" : ""}`} />
                  </ActionButton>
                ) : null}
              </div>
              <UsageSummary message={message} />
            </div>
          </div>
        </div>
      </div>
    </div>
  )
})

function AnswerAttemptControls({
  navigation,
  switchingAttempt,
  deleting,
  onSelect,
  onDelete,
}: {
  navigation: AnswerAttemptNavigation
  switchingAttempt: number | null
  deleting: boolean
  onSelect: (attemptId: number) => void
  onDelete: () => void
}) {
  const hasPrevious = typeof navigation.previous_attempt_id === "number"
  const hasNext = typeof navigation.next_attempt_id === "number"
  const switching = switchingAttempt !== null || deleting
  return (
    <div className="flex h-8 items-center border-r border-border/60 pr-2 sm:h-7">
      <button
        type="button"
        title="查看上一个回答"
        aria-label="查看上一个回答"
        disabled={!hasPrevious || switching}
        onClick={() => hasPrevious && onSelect(navigation.previous_attempt_id!)}
        className="inline-flex h-9 w-9 items-center justify-center text-muted-foreground transition-colors motion-control hover:text-foreground disabled:pointer-events-none disabled:opacity-35 sm:h-8 sm:w-8"
      >
        {switchingAttempt === navigation.previous_attempt_id ? <Loader2 className="h-3.5 w-3.5 animate-spin motion-reduce:animate-none" aria-hidden="true" /> : <ChevronLeft className="h-3.5 w-3.5" aria-hidden="true" />}
      </button>
      <span className="min-w-10 select-none text-center text-xs tabular-nums text-muted-foreground">
        {navigation.attempt_number}/{navigation.attempt_count}
      </span>
      <button
        type="button"
        title="查看下一个回答"
        aria-label="查看下一个回答"
        disabled={!hasNext || switching}
        onClick={() => hasNext && onSelect(navigation.next_attempt_id!)}
        className="inline-flex h-9 w-9 items-center justify-center text-muted-foreground transition-colors motion-control hover:text-foreground disabled:pointer-events-none disabled:opacity-35 sm:h-8 sm:w-8"
      >
        {switchingAttempt === navigation.next_attempt_id ? <Loader2 className="h-3.5 w-3.5 animate-spin motion-reduce:animate-none" aria-hidden="true" /> : <ChevronRight className="h-3.5 w-3.5" aria-hidden="true" />}
      </button>
      <button
        type="button"
        title="删除当前回答"
        aria-label="删除当前回答"
        disabled={switching || navigation.attempt_count <= 1}
        onClick={onDelete}
        className="inline-flex h-9 w-9 items-center justify-center text-muted-foreground transition-colors motion-control hover:text-rose-600 disabled:pointer-events-none disabled:opacity-35 sm:h-8 sm:w-8"
      >
        {deleting ? <Loader2 className="h-3.5 w-3.5 animate-spin motion-reduce:animate-none" aria-hidden="true" /> : <Trash2 className="h-3.5 w-3.5" aria-hidden="true" />}
      </button>
    </div>
  )
}

function MessageStateLine({ label, tone }: { label: string; tone: "error" | "muted" }) {
  return (
    <div role={tone === "error" ? "alert" : "status"} aria-live="polite" className={tone === "error" ? "text-xs text-rose-600 dark:text-rose-400" : "text-xs text-muted-foreground"}>
      {label}
    </div>
  )
}

function ErrorNotice({ detail, diagnostic, onRetry, retrying }: { detail: string; diagnostic: string; onRetry?: () => void; retrying?: boolean }) {
  const hasDetail = detail.trim().length > 0
  return (
    <div className="rounded-lg border border-rose-200 bg-rose-50/60 px-3.5 py-3 dark:border-rose-900/50 dark:bg-rose-950/30">
      <div className="flex items-start gap-2.5">
        <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-rose-500" />
        <div className="min-w-0 flex-1 space-y-2">
          <div className="text-sm font-medium text-rose-700 dark:text-rose-300">本次回复未能完成</div>
          <p className="text-xs leading-relaxed text-rose-600/90 dark:text-rose-400/90">
            {hasDetail ? detail : "请求过程中出现错误，可以重试。如果反复出现，请检查模型配置或稍后再试。"}
          </p>
          {diagnostic ? (
            <p className="break-words text-xs leading-relaxed text-rose-600/70 dark:text-rose-400/70">
              {diagnostic}
            </p>
          ) : null}
          <div className="flex items-center gap-3 pt-0.5">
            {onRetry ? (
              <button
                onClick={onRetry}
                disabled={retrying}
                className="inline-flex items-center gap-1.5 rounded-md bg-rose-600 px-2.5 py-1 text-xs font-medium text-white transition-colors motion-control hover:bg-rose-700 disabled:cursor-not-allowed disabled:opacity-50"
              >
                <RotateCcw className="h-3 w-3" />
                {retrying ? "重试中…" : "重试"}
              </button>
            ) : null}
          </div>
        </div>
      </div>
    </div>
  )
}

const AssistantSegments = memo(function AssistantSegments({
  messageId,
  segments,
}: {
  messageId: number
  segments: AssistantSegment[]
}) {
  const rows = useMemo(() => groupAssistantSegments(segments), [segments])
  return (
    <>
      {rows.map((row, index) => (
        <div key={index} className="space-y-3">
          {row.reasoning ? (
            <ReasoningSummary
              reasoningKey={`message:${messageId}:${index}:reasoning`}
              segments={row.reasoning.segments}
            />
          ) : null}
          {row.content?.trim() ? (
            <div className="min-w-0 px-1 text-[15px] leading-[1.5]">
              <MarkdownContent content={row.content.trim()} ownerKey={`${messageId}:${index}`} />
            </div>
          ) : null}
        </div>
      ))}
    </>
  )
})

function MessageDate({ message }: { message: Message }) {
  return (
    <div className="flex items-center gap-2 text-sm text-muted-foreground">
      <span>{formatAbsoluteTime(message.created_at)}</span>
      <span className="text-muted-foreground/60">·</span>
      <span>{formatRelativeTime(message.created_at)}</span>
    </div>
  )
}

const ReasoningSummary = memo(function ReasoningSummary({
  reasoningKey,
  segments,
}: {
  reasoningKey: string
  segments: AssistantSegment[]
}) {
  const thinking = segments.map((segment) => segment.thinking?.trim()).filter(Boolean).join("\n\n")
  const toolCalls = segments.flatMap((segment) => segment.tool_calls || [])
  const hasThinking = Boolean(thinking?.trim())
  const hasTools = toolCalls.length > 0

  if (!hasThinking && !hasTools) return null

  return (
    <ReasoningPanel reasoningKey={reasoningKey} thinking={thinking} toolCalls={toolCalls}>
      <div className="space-y-1.5">
        {segments.map((segment, index) => (
          <div key={index} className="space-y-1.5">
            {segment.thinking?.trim() ? (
              <MarkdownContent
                content={segment.thinking.trim()}
                ownerKey={`${reasoningKey}:${index}:thinking`}
                allowArtifactPreviews={false}
                variant="reasoning"
              />
            ) : null}
            {segment.tool_calls?.length ? <ToolCallTree toolCalls={segment.tool_calls} /> : null}
          </div>
        ))}
      </div>
    </ReasoningPanel>
  )
})

function UsageSummary({ message }: { message: Message }) {
  const usage = message.message_data.response_meta?.usage
  const runtime = message.message_data.runtime
  const [open, setOpen] = useState(false)
  const cachedTokens = getCachedTokens(usage)
  const reasoningTokens = getReasoningTokens(usage)
  const cacheHitRate = getCacheHitRate(usage)

  const summaryParts: string[] = []
  if (usage?.total_tokens) summaryParts.push(formatTokens(usage.total_tokens))
  if (cachedTokens > 0) summaryParts.push(`缓存 ${(cacheHitRate * 100).toFixed(0)}%`)
  if (runtime?.tokens_per_second) summaryParts.push(`${runtime.tokens_per_second.toFixed(1)}/s`)
  const summary = summaryParts.length ? summaryParts.join(" · ") : "查看用量"

  if (!usage && !runtime?.tokens_per_second && !runtime?.duration_ms) return null

  return (
    <div className="relative ml-auto flex justify-end">
      <button
        onClick={() => setOpen((value) => !value)}
        className="inline-flex h-8 items-center gap-1.5 rounded-md px-2 text-xs font-medium text-muted-foreground transition-colors motion-control hover:bg-muted/60 hover:text-foreground"
      >
        <span>{summary}</span>
        {open ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
      </button>
      <div className={`absolute right-0 top-full z-20 mt-1 origin-top-right transition-[opacity,translate] motion-panel ${open ? "pointer-events-auto translate-y-0 opacity-100" : "pointer-events-none -translate-y-1 opacity-0"}`}>
        <div className="w-max max-w-[calc(100vw-2rem)] rounded-md border border-border bg-popover px-3 py-2 text-popover-foreground shadow-lg">
          <div className="flex flex-wrap items-center justify-end gap-x-3 gap-y-1 text-xs text-muted-foreground">
            {usage ? (
              <>
                <span>输入 {formatTokens(usage.prompt_tokens)}</span>
                <span>输出 {formatTokens(usage.completion_tokens)}</span>
                <span>总计 {formatTokens(usage.total_tokens)}</span>
                {cachedTokens > 0 ? <span>缓存 {formatTokens(cachedTokens)} / {(cacheHitRate * 100).toFixed(0)}%</span> : null}
                {reasoningTokens > 0 ? <span>推理 {formatTokens(reasoningTokens)}</span> : null}
              </>
            ) : null}
            {runtime?.tokens_per_second ? <span>{runtime.tokens_per_second.toFixed(1)} token/秒</span> : null}
            {runtime?.duration_ms ? <span>{formatDuration(runtime.duration_ms)}</span> : null}
          </div>
        </div>
      </div>
    </div>
  )
}

function ActionButton({
  children,
  label,
  onClick,
  disabled,
}: {
  children: React.ReactNode
  label: string
  onClick: () => void
  disabled?: boolean
}) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      className="inline-flex h-8 items-center gap-1.5 rounded-md px-2 text-xs font-medium text-muted-foreground transition-colors motion-control hover:bg-muted/50 hover:text-foreground disabled:pointer-events-none disabled:opacity-40"
    >
      {children}
      <span>{label}</span>
    </button>
  )
}

function formatDuration(durationMs: number) {
  if (durationMs < 1000) return `${durationMs}ms`
  return `${(durationMs / 1000).toFixed(1)}秒`
}

function formatRelativeTime(dateString: string) {
  const timestamp = new Date(dateString).getTime()
  if (Number.isNaN(timestamp)) return ""
  const diffSeconds = Math.max(0, Math.floor((Date.now() - timestamp) / 1000))
  if (diffSeconds < 10) return "刚刚"
  if (diffSeconds < 60) return `${diffSeconds}秒前`
  if (diffSeconds < 3600) return `${Math.floor(diffSeconds / 60)}分钟前`
  if (diffSeconds < 86400) return `${Math.floor(diffSeconds / 3600)}小时前`
  return `${Math.floor(diffSeconds / 86400)}天前`
}

function formatAbsoluteTime(dateString: string) {
  const date = new Date(dateString)
  if (Number.isNaN(date.getTime())) return ""
  return date.toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  })
}
