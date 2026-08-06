import { useId, useState } from "react"
import type { ToolCall } from "@/types"
import { AlertCircle, AlertTriangle, Check, ChevronDown, ChevronRight, ExternalLink, Globe, Loader2, Search, Wrench } from "lucide-react"
import { cn } from "@/lib/utils"
import { cleanUrl, extractResultWarning, getToolArguments, getToolName, hostOf, normalizeCitation, parseExtractResult, parseSearchResult, parseToolFailure, searchQueryFromArguments, toolNameLabel, toolSourceLabel, type ExtractResult, type SearchResult, type ToolFailure } from "@/lib/toolResults"

export function ToolCallTree({
  toolCalls,
  streaming = false,
}: {
  toolCalls: ToolCall[]
  streaming?: boolean
}) {
  return (
    <div className="divide-y divide-border/60">
      {toolCalls.map((toolCall) => (
        <ToolCallNode key={toolCall.id} toolCall={toolCall} streaming={streaming} depth={0} />
      ))}
    </div>
  )
}

function ToolCallNode({
  toolCall,
  streaming,
  depth,
}: {
  toolCall: ToolCall
  streaming: boolean
  depth: number
}) {
  const [open, setOpen] = useState(false)
  const status = toolCall.status || (streaming ? "running" : "done")
  const toolName = getToolName(toolCall)
  const toolArguments = getToolArguments(toolCall)
  const searchResult = toolName === "web_search" ? parseSearchResult(toolCall.result) : null
  const extractResult = toolName === "web_extract" ? parseExtractResult(toolCall.result) : null
  const toolFailure = parseToolFailure(toolCall.result)
  const searchQuery = toolName === "web_search" ? searchQueryFromArguments(toolArguments) : ""
  const sourceLabel = toolSourceLabel(searchResult?.source || extractResult?.source)
  const citationsCount = searchResult?.citations?.length || 0
  const hasChildren = (toolCall.children?.length || 0) > 0
  const extractWarning = extractResult ? extractResultWarning(extractResult) : ""
  const displayStatus = toolFailure || status === "error" ? "error" : extractWarning ? "warning" : status
  const hasDetail = hasChildren || searchResult || extractResult || toolFailure || toolArguments || toolCall.result
  const detailId = useId()

  return (
    <div className={cn("transition-colors motion-control", depth > 0 && "pl-4")}>
      <button
        onClick={() => hasDetail && setOpen((value) => !value)}
        aria-expanded={hasDetail ? open : undefined}
        aria-controls={hasDetail ? detailId : undefined}
        className={cn(
          "flex w-full items-center gap-2 py-1.5 text-left",
          hasDetail ? "cursor-pointer hover:text-foreground" : "cursor-default"
        )}
      >
        {iconOf(toolName)}
        <span className="min-w-0 flex-1 truncate text-[13px] font-medium">
          {toolNameLabel(toolName)}{sourceLabel ? `（${sourceLabel}）` : ""}
        </span>
        {citationsCount > 0 && <span className="shrink-0 text-[11px] text-muted-foreground">{citationsCount} 个来源</span>}
        <StatusBadge status={displayStatus} />
        {hasDetail ? (
          open ? <ChevronDown className="h-3.5 w-3.5 text-muted-foreground" /> : <ChevronRight className="h-3.5 w-3.5 text-muted-foreground" />
        ) : null}
      </button>

      <div
        id={hasDetail ? detailId : undefined}
        className="grid transition-[grid-template-rows] motion-panel"
        style={{ gridTemplateRows: open ? "1fr" : "0fr" }}
      >
        <div className="min-h-0 overflow-hidden">
          <div className="space-y-1.5 pb-1.5 pl-5">
            {searchResult ? <SearchResultView result={searchResult} query={searchQuery} /> : null}
            {extractResult ? <ExtractResultView result={extractResult} /> : null}
            {toolFailure ? <ToolFailureView failure={toolFailure} /> : null}
            {!searchResult && !extractResult && !toolFailure && toolArguments ? (
              <pre className="overflow-x-auto text-xs leading-6 text-muted-foreground">{formatJson(toolArguments)}</pre>
            ) : null}
            {!searchResult && !extractResult && !toolFailure && toolCall.result ? (
              <pre className="max-h-[220px] overflow-auto whitespace-pre-wrap text-xs leading-6 text-foreground/75">{toolCall.result}</pre>
            ) : null}
            {hasChildren ? (
              <div className="divide-y divide-border/50">
                {toolCall.children!.map((child) => (
                  <ToolCallNode key={child.id} toolCall={child} streaming={streaming} depth={depth + 1} />
                ))}
              </div>
            ) : null}
          </div>
        </div>
      </div>
    </div>
  )
}

function ToolFailureView({ failure }: { failure: ToolFailure }) {
  return (
    <div className="rounded-md border border-rose-200/70 bg-rose-50 px-2 py-1.5 text-xs leading-5 text-rose-700 dark:border-rose-900/60 dark:bg-rose-950/25 dark:text-rose-300">
      <div className="font-medium">工具调用失败</div>
      <div>{failure.error || failure.message || "未知错误"}</div>
      {failure.source === "tool_quota" && failure.code ? <div className="mt-1 text-rose-600/80 dark:text-rose-300/80">限额：{failure.code}</div> : null}
      {failure.retryable ? <div className="mt-1 text-rose-600/80 dark:text-rose-300/80">可稍后重试</div> : null}
    </div>
  )
}

function iconOf(toolName: string) {
  if (toolName === "web_search") return <Search className="h-3.5 w-3.5 text-emerald-600 dark:text-emerald-400" />
  if (toolName === "web_extract") return <Globe className="h-3.5 w-3.5 text-sky-600 dark:text-sky-400" />
  return <Wrench className="h-3.5 w-3.5 text-muted-foreground" />
}

function StatusBadge({ status }: { status: string }) {
  return (
    <span className={cn(
      "inline-flex items-center gap-1 text-[11px]",
      status === "done" && "text-emerald-600 dark:text-emerald-400",
      status === "warning" && "text-amber-700 dark:text-amber-300",
      status === "error" && "text-rose-600 dark:text-rose-400",
      status === "running" && "text-blue-600 dark:text-blue-400"
    )}>
      {status === "done" ? <Check className="h-3 w-3" /> : null}
      {status === "warning" ? <><AlertTriangle className="h-3 w-3" /><span>内容受限</span></> : null}
      {status === "error" ? <AlertCircle className="h-3 w-3" /> : null}
      {status === "running" ? <Loader2 className="h-3 w-3 animate-spin" /> : null}
    </span>
  )
}

function SearchResultView({ result, query }: { result: SearchResult; query?: string }) {
  const citations = result.citations || []
  return (
    <div>
      {query ? (
        <div className="mb-1.5 rounded-md bg-muted/45 px-2 py-1 text-xs leading-5 text-foreground/80">
          <span className="text-muted-foreground">关键词：</span>{query}
        </div>
      ) : null}
      <div className="mb-1 flex items-center justify-between text-xs">
        <span className="font-medium text-foreground/80">搜索来源</span>
        <span className="text-muted-foreground">{citations.length}</span>
      </div>
      <div className="space-y-1.5">
        {citations.map((rawItem, index) => {
          const item = normalizeCitation(rawItem)
          return (
            <a
              key={`${item.url}-${index}`}
              href={item.url}
              target="_blank"
              rel="noreferrer"
              className="block transition-colors motion-control hover:text-foreground"
            >
              <div className="flex items-center gap-2">
                <span className="shrink-0 text-[11px] text-muted-foreground">{index + 1}.</span>
                <span className="min-w-0 flex-1 truncate text-xs font-medium">{item.title || hostOf(item.url)}</span>
                <ExternalLink className="h-3.5 w-3.5 text-muted-foreground" />
              </div>
              <div className="mt-0.5 truncate text-[11px] text-muted-foreground">{hostOf(item.url)}</div>
              {item.snippet ? <div className="mt-1 line-clamp-2 text-xs leading-5 text-muted-foreground">{item.snippet}</div> : null}
            </a>
          )
        })}
      </div>
    </div>
  )
}

function ExtractResultView({ result }: { result: ExtractResult }) {
  const normalizedUrl = cleanUrl(result.url)
  const warning = extractResultWarning(result)
  return (
    <div>
      <div className="mb-2 flex items-center gap-2 text-xs font-medium">
        <span className="min-w-0 flex-1 truncate">{result.title || "网页内容"}</span>
        {normalizedUrl ? (
          <a href={normalizedUrl} target="_blank" rel="noreferrer" className="text-muted-foreground hover:text-foreground">
            <ExternalLink className="h-3.5 w-3.5" />
          </a>
        ) : null}
      </div>
      {warning ? (
        <div className="mb-1.5 flex items-start gap-1.5 text-xs leading-5 text-amber-800 dark:text-amber-200">
          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
          <span>{warning}</span>
        </div>
      ) : null}
      {result.ok === false ? (
        <div className="text-xs leading-6 text-muted-foreground">
          网页不可直接读取
          {result.status_code ? `（${result.status_code}）` : ""}
          {result.error ? `：${result.error}` : ""}
        </div>
      ) : null}
      {result.content ? (
        <div className="max-h-[240px] overflow-auto whitespace-pre-wrap text-xs leading-6 text-foreground/75">
          {result.content}
        </div>
      ) : null}
    </div>
  )
}

function formatJson(str: string): string {
  try {
    return JSON.stringify(JSON.parse(str), null, 2)
  } catch {
    return str
  }
}
