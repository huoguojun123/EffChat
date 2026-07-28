import { type ReactNode, useId } from "react"
import type { ToolCall } from "@/types"
import { Brain, ChevronDown, ChevronRight, Wrench } from "lucide-react"
import { useUIStore } from "@/stores/ui"
import { cn } from "@/lib/utils"

export function ReasoningPanel({
  reasoningKey,
  thinking,
  toolCalls,
  loading = false,
  children,
}: {
  reasoningKey: string
  thinking?: string
  toolCalls: ToolCall[]
  loading?: boolean
  children?: ReactNode
}) {
  const state = useUIStore((s) => s.reasoningOpenStates[reasoningKey])
  const setReasoningOpen = useUIStore((s) => s.setReasoningOpen)
  const hasThinking = Boolean(thinking?.trim())
  const hasTools = toolCalls.length > 0
  const open = state?.open ?? false
  const contentId = useId()

  if (!hasThinking && !hasTools) return null

  return (
    <div className="w-full">
      <div className="my-2 border-l-2 border-border/50 pl-4">
        <button
          onClick={() => setReasoningOpen(reasoningKey, !open, "user")}
          aria-expanded={open}
          aria-controls={contentId}
          className="flex min-h-7 w-full items-center gap-2 py-1 text-left text-muted-foreground transition-colors motion-control hover:text-foreground"
        >
          {loading ? (
            <span className="relative flex h-2 w-2">
              <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75 motion-reduce:animate-none"></span>
              <span className="relative inline-flex h-2 w-2 rounded-full bg-emerald-500"></span>
            </span>
          ) : (
            hasTools ? <Wrench className="h-3.5 w-3.5" /> : <Brain className="h-3.5 w-3.5" />
          )}
          <div className="min-w-0 flex-1">
            <div className="truncate text-xs font-medium leading-5">
              {summaryLabel(hasThinking, hasTools, toolCalls.length)}
            </div>
          </div>
          {open ? (
            <ChevronDown className="h-3.5 w-3.5 transition-transform motion-icon" />
          ) : (
            <ChevronRight className="h-3.5 w-3.5 transition-transform motion-icon" />
          )}
        </button>
        {/* grid-template-rows 0fr↔1fr 实现内容感知的平滑高度过渡，无需 JS 测量 */}
        <div
          id={contentId}
          className="grid transition-[grid-template-rows] motion-panel"
          style={{ gridTemplateRows: open ? "1fr" : "0fr" }}
        >
          <div className="min-h-0 overflow-hidden">
            {/* opacity 比高度略快收起，避免折叠尾段出现文字硬裁切 */}
            <div
              className={cn(
                "space-y-1.5 pt-1.5 pb-0.5 transition-opacity motion-panel",
                open ? "opacity-100" : "opacity-0"
              )}
            >
              {children}
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}

function summaryLabel(hasThinking: boolean, hasTools: boolean, toolCount: number) {
  if (hasThinking && hasTools) return `思考过程与工具调用（${toolCount}）`
  if (hasThinking) return "思考过程"
  return `工具调用（${toolCount}）`
}
