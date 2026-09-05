import { memo, useEffect, useState } from "react"
import type { AssistantSegment } from "@/types"
import { useChatStore } from "@/stores/chat"
import { Loader2 } from "lucide-react"
import { AppLogo } from "@/components/AppLogo"
import { MarkdownContent } from "./MarkdownContent"
import { ToolCallTree } from "./ToolCallTree"
import { ReasoningPanel } from "./ReasoningPanel"
import { groupAssistantSegments } from "./assistantSegments"
import { formatRetryDelay } from "@/lib/streamingRetry"

export function StreamingMessage() {
  const { content, thinking, toolCalls, segments, status, replayGap, retryTrace } = useChatStore((s) => s.streaming)
  const [retryRemainingMs, setRetryRemainingMs] = useState(0)
  const retryDelayMs = retryTrace?.delayMs ?? 0
  const showRecovering = useDelayedFlag(status === "recovering", 800)
  const hasRetryTrace = retryTrace !== null && retryTrace !== undefined
  const retryTraceKey = retryTrace
    ? `${retryTrace.attempt}:${retryTrace.maxAttempts}:${retryTrace.delayMs}:${retryTrace.category}`
    : null

  useEffect(() => {
    if (!hasRetryTrace) {
      return
    }

    const deadline = Date.now() + Math.max(0, retryDelayMs)
    const updateRemaining = () => {
      const remaining = Math.max(0, deadline - Date.now())
      setRetryRemainingMs(remaining)
      return remaining > 0
    }

    updateRemaining()
    const timer = window.setInterval(() => {
      if (!updateRemaining()) window.clearInterval(timer)
    }, 100)
    return () => window.clearInterval(timer)
  }, [hasRetryTrace, retryDelayMs, retryTraceKey])

  // 仅实时增量标记当前 Markdown 块；syncing/恢复快照直接稳定显示，避免重播入场动效。
  const revealing = status === "sending" || status === "streaming"
  const rows = groupAssistantSegments(segments)

  return (
    <div className="py-5 sm:py-6">
      <div className="flex items-start gap-0 sm:gap-4">
        <div className="hidden w-11 shrink-0 justify-center pt-1 sm:flex">
          <AppLogo className="h-8 w-8 text-foreground/80" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="w-full space-y-3">
            <div className="flex items-center gap-2 text-sm text-muted-foreground">
              <span>{new Date().toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" })}</span>
              <span className="text-muted-foreground/60">·</span>
              <span className="text-muted-foreground">
                {status === "syncing"
                  ? "正在同步结果…"
                  : showRecovering
                    ? replayGap ? "正在补全回答…" : "正在接续回答…"
                    : !content && !thinking && toolCalls.length === 0
                      ? "已接收，等待回复…"
                      : "正在回复…"}
              </span>
            </div>
            {rows.length > 0 ? (
              rows.map((row, index) => (
                <div key={index} className="space-y-3">
                  {row.reasoning ? (
                    <StreamingReasoningSummary
                      reasoningKey={`stream:${index}`}
                      segments={row.reasoning.segments}
                    />
                  ) : null}
                  {row.content?.trim() ? (
                    <StreamingText
                      content={row.content}
                      ownerKey={`stream:${index}`}
                      // 只有最后一个 segment 在吐字；之前的 segment 已定稿，直接全量显示。
                      revealing={revealing && index === rows.length - 1}
                    />
                  ) : null}
                </div>
              ))
            ) : content ? (
              <StreamingText content={content} ownerKey="stream:fallback" revealing={revealing} />
            ) : null}

            {retryTrace ? (
              <div className="flex h-7 items-center gap-2 text-sm text-muted-foreground" role="status" aria-live="polite">
                <Loader2 className="h-4 w-4 animate-spin motion-reduce:animate-none" aria-hidden="true" />
                <span>
                  第 {retryTrace.attempt}/{retryTrace.maxAttempts} 次请求暂时失败，
                  {retryRemainingMs > 0 ? `${formatRetryDelay(retryRemainingMs)}后自动重试` : "正在重新请求"}
                </span>
              </div>
            ) : !content && !thinking && toolCalls.length === 0 ? (
              <div className="flex h-7 items-center gap-2 text-sm text-muted-foreground">
                <Loader2 className="h-4 w-4 animate-spin motion-reduce:animate-none" aria-hidden="true" />
              </div>
            ) : null}
          </div>
        </div>
      </div>
    </div>
  )
}

// ReactMarkdown keeps completed top-level nodes stable while content appends.
// CSS animates only the current last node when it first becomes a new visual
// block; existing text is never replayed through a synthetic typewriter.
const StreamingText = memo(function StreamingText({
  content,
  ownerKey,
  revealing,
}: {
  content: string
  ownerKey: string
  revealing: boolean
}) {
  return (
    <div className="min-w-0 px-1 text-[15px] leading-[1.5]">
      <MarkdownContent content={content.trimStart()} streaming={revealing} ownerKey={ownerKey} />
    </div>
  )
})

function useDelayedFlag(active: boolean, delayMs: number) {
  const [visible, setVisible] = useState(false)
  useEffect(() => {
    const timer = window.setTimeout(() => setVisible(active), active ? delayMs : 0)
    return () => window.clearTimeout(timer)
  }, [active, delayMs])
  return active && visible
}

const StreamingReasoningSummary = memo(function StreamingReasoningSummary({
  reasoningKey,
  segments,
}: {
  reasoningKey: string
  segments: AssistantSegment[]
}) {
  const thinking = segments.map((segment) => segment.thinking?.trim()).filter(Boolean).join("\n\n")
  const toolCalls = segments.flatMap((segment) => segment.tool_calls || [])
  return (
    <ReasoningPanel reasoningKey={reasoningKey} thinking={thinking} toolCalls={toolCalls} loading>
      <div className="space-y-1.5">
        {segments.map((segment, index) => (
          <div key={index} className="space-y-1.5">
            {segment.thinking?.trim() ? (
              <MarkdownContent
                content={segment.thinking.trim()}
                streaming
                ownerKey={`${reasoningKey}:${index}:thinking`}
                allowArtifactPreviews={false}
                variant="reasoning"
              />
            ) : null}
            {segment.tool_calls?.length ? <ToolCallTree toolCalls={segment.tool_calls} streaming /> : null}
          </div>
        ))}
      </div>
    </ReasoningPanel>
  )
})
