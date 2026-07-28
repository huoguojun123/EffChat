import { memo } from "react"
import type { AssistantSegment } from "@/types"
import { useChatStore } from "@/stores/chat"
import { Loader2 } from "lucide-react"
import { AppLogo } from "@/components/AppLogo"
import { MarkdownContent } from "./MarkdownContent"
import { ToolCallTree } from "./ToolCallTree"
import { ReasoningPanel } from "./ReasoningPanel"
import { useTypewriter } from "@/hooks/useTypewriter"
import { compactReasoningText } from "@/lib/reasoningText"
import { groupAssistantSegments } from "./assistantSegments"

export function StreamingMessage() {
  const { content, thinking, toolCalls, segments, status, replayGap, retryTrace } = useChatStore((s) => s.streaming)
  // 文本仍在到达时 active=true（恒速吐字）；syncing/收尾时直接对齐，避免拖尾。
  const revealing = status === "sending" || status === "streaming"
  const rows = groupAssistantSegments(segments)

  return (
    <div className="py-8">
      <div className="flex items-start gap-0 sm:gap-4">
        <div className="hidden w-11 shrink-0 justify-center pt-1 sm:flex">
          <AppLogo className="h-8 w-8 text-foreground/80" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="w-full space-y-3">
            <div className="flex items-center gap-2 text-sm text-muted-foreground">
              <span>{new Date().toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" })}</span>
              <span className="text-muted-foreground/60">·</span>
              <span className="text-muted-foreground">{replayGap ? "正在补全回答" : status === "recovering" ? "正在恢复连接" : "正在回复"}</span>
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
              <div className="flex h-7 items-center gap-2 text-sm text-muted-foreground">
                <Loader2 className="h-4 w-4 animate-spin" />
                <span>第 {retryTrace.attempt} 次请求暂时失败，{formatRetryDelay(retryTrace.delayMs)}后自动重试</span>
              </div>
            ) : !content && !thinking && toolCalls.length === 0 ? (
              <div className="flex h-7 items-center gap-2 text-sm text-muted-foreground">
                <Loader2 className="h-4 w-4 animate-spin" />
              </div>
            ) : null}
          </div>
        </div>
      </div>
    </div>
  )
}

function formatRetryDelay(delayMs: number) {
  const seconds = Math.max(0, delayMs) / 1000
  return `${Number.isInteger(seconds) ? seconds : seconds.toFixed(1)} 秒`
}

// StreamingText 用打字机恒速揭示文本，并在底部加一层渐隐遮罩，让新行从虚化处浮现，
// 形成 LobeHub 式的平滑淡入。揭示完成（revealing=false）时撤掉遮罩，显示完整内容。
const StreamingText = memo(function StreamingText({
  content,
  ownerKey,
  revealing,
}: {
  content: string
  ownerKey: string
  revealing: boolean
}) {
  const shown = useTypewriter(content.trimStart(), revealing)
  return (
    <div className="min-w-0 px-1 text-[15px] leading-[1.5]">
      <div className={revealing ? "streaming-reveal streaming-fade" : "streaming-reveal"}>
        <MarkdownContent content={shown} streaming ownerKey={ownerKey} />
      </div>
    </div>
  )
})

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
              <div className="whitespace-pre-wrap text-[12px] leading-[1.45] text-muted-foreground">{compactReasoningText(segment.thinking)}</div>
            ) : null}
            {segment.tool_calls?.length ? <ToolCallTree toolCalls={segment.tool_calls} streaming /> : null}
          </div>
        ))}
      </div>
    </ReasoningPanel>
  )
})
