import { useEffect, useMemo, useRef, useState } from "react"
import {
  adminApi,
  type AdminUsageResponse,
  type QuotaUserUsage,
  type ToolUsageTotals,
  type UsageByKind,
  type UsageByModel,
  type UsageByTool,
  type UsageByUser,
  type UsageQuery,
  type UsageRange,
  type UsageTotals,
} from "@/api/admin"
import { Button } from "@/components/ui/button"
import { UsageQueryOwnership } from "@/components/admin/usageQueryOwnership"
import { Activity, Database, FileText, RefreshCw, Search, Wrench, Zap } from "lucide-react"

interface Props {
  setError: (error: string) => void
}

const ranges: { value: UsageRange; label: string }[] = [
  { value: "today", label: "今天治理" },
  { value: "7d", label: "7 天治理" },
  { value: "30d", label: "30 天治理" },
]

type UsageMode = UsageRange | "custom"

const emptyUsage: AdminUsageResponse = {
  totals: emptyTotals(),
  run_totals: emptyRunTotals(),
  by_user: [],
  by_model: [],
  by_kind: [],
  tool_totals: emptyToolTotals(),
  by_tool: [],
  quota_users: [],
}

export function AdminUsagePanel({ setError }: Props) {
  const [mode, setMode] = useState<UsageMode>("today")
  const [customStart, setCustomStart] = useState(() => localDateValue(-6))
  const [customEnd, setCustomEnd] = useState(() => localDateValue(0))
  const [activeLabel, setActiveLabel] = useState("今天治理")
  const [data, setData] = useState<AdminUsageResponse>(emptyUsage)
  const [loading, setLoading] = useState(false)
  const usageOwner = useRef(new UsageQueryOwnership()).current

  function queryKey(query: UsageQuery) {
    return typeof query === "string" ? `range:${query}` : `custom:${query.start_at}:${query.end_at}`
  }

  function selectionKey(nextMode: UsageMode, start = customStart, end = customEnd) {
    if (nextMode !== "custom") return `range:${nextMode}`
    return `custom:${start}:${end}`
  }

  function invalidateSelection(key: string) {
    usageOwner.activate(key)
    setLoading(false)
    setError("")
  }

  async function loadUsage(query: UsageQuery, label: string) {
    const operation = usageOwner.begin(queryKey(query))
    setLoading(true)
    setError("")
    try {
      const nextData = normalizeUsage(await adminApi.getUsage(query))
      if (usageOwner.owns(operation)) {
        setData(nextData)
        setActiveLabel(label)
      }
    } catch (err) {
      if (usageOwner.owns(operation)) setError(err instanceof Error ? err.message : "用量加载失败")
    } finally {
      if (usageOwner.owns(operation)) setLoading(false)
    }
  }

  function refreshUsage() {
    if (mode !== "custom") {
      void loadUsage(mode, rangeLabel(mode))
      return
    }
    const query = customUsageQuery(customStart, customEnd)
    if (!query) {
      invalidateSelection(selectionKey("custom"))
      setError("请选择不超过 90 天的有效日期范围")
      return
    }
    void loadUsage(query, `${customStart} 至 ${customEnd}`)
  }

  useEffect(() => {
    let active = true
    usageOwner.activate(selectionKey(mode))
    if (mode !== "custom") {
      queueMicrotask(() => {
        if (active) void loadUsage(mode, rangeLabel(mode))
      })
    }
    return () => {
      active = false
      usageOwner.activate("")
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mode])

  const activeUsers = useMemo(
    () => data.quota_users
      .filter((user) => user.daily_messages || user.daily_model_tokens || user.daily_tool_calls || user.daily_ocr_files)
      .sort((a, b) => quotaPressure(b) - quotaPressure(a)),
    [data.quota_users]
  )

  return (
    <div className="flex h-full min-h-0 flex-col overflow-hidden">
      <div className="flex flex-wrap items-center gap-2 border-b border-border/70 pb-3">
        <div className="mr-auto">
          <div className="text-sm font-semibold">用量与限额</div>
          <div className="text-sm text-muted-foreground">按时间范围看模型、工具和用户调用；每日限额压力放在底部。</div>
        </div>
        <div className="flex rounded-md border border-border/70 p-0.5">
          {ranges.map((item) => (
            <button
              key={item.value}
              onClick={() => {
                if (mode !== item.value) {
                  invalidateSelection(selectionKey(item.value))
                  setMode(item.value)
                }
              }}
              className={`h-11 rounded px-3 text-sm transition-colors motion-control focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50 sm:h-8 ${
                mode === item.value ? "bg-foreground text-background" : "text-muted-foreground hover:bg-muted hover:text-foreground"
              }`}
            >
              {item.label}
            </button>
          ))}
          <button
            onClick={() => {
              if (mode !== "custom") {
                invalidateSelection(selectionKey("custom"))
                setMode("custom")
              }
            }}
            className={`h-11 rounded px-3 text-sm transition-colors motion-control focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50 sm:h-8 ${
              mode === "custom" ? "bg-foreground text-background" : "text-muted-foreground hover:bg-muted hover:text-foreground"
            }`}
          >
            自定义
          </button>
        </div>
        {mode === "custom" ? (
          <div className="flex items-center gap-1.5">
            <input type="date" aria-label="开始日期" value={customStart} max={localDateValue(0)} onChange={(event) => {
              invalidateSelection(selectionKey("custom", event.target.value, customEnd))
              setCustomStart(event.target.value)
            }} className="h-11 min-w-0 rounded-md border border-border/70 bg-background px-2 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50 sm:h-8" />
            <span className="text-xs text-muted-foreground">至</span>
            <input type="date" aria-label="结束日期" value={customEnd} max={localDateValue(0)} onChange={(event) => {
              invalidateSelection(selectionKey("custom", customStart, event.target.value))
              setCustomEnd(event.target.value)
            }} className="h-11 min-w-0 rounded-md border border-border/70 bg-background px-2 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50 sm:h-8" />
          </div>
        ) : null}
        <Button variant="outline" size="sm" className="h-11 sm:h-8" onClick={refreshUsage} disabled={loading}>
          <RefreshCw className={`h-3.5 w-3.5 ${loading ? "animate-spin" : ""}`} />
          刷新
        </Button>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto py-3">
        <section>
          <SectionTitle title="回答运行" detail={`${activeLabel} · ${formatNumber(data.run_totals.runs)} 次`} />
          <div className="mt-3 grid gap-2 sm:grid-cols-2 xl:grid-cols-4">
            <Metric icon={<Activity className="h-4 w-4" />} label="已完成" value={data.run_totals.completed} detail={`完成率 ${formatRate(data.run_totals.completed, terminalRuns(data))} · 均耗时 ${formatDuration(data.run_totals.avg_duration_ms)}`} />
            <Metric icon={<Activity className="h-4 w-4" />} label="运行中" value={data.run_totals.running} />
            <Metric icon={<Activity className="h-4 w-4" />} label="回答失败" value={data.run_totals.failed} detail={`失败率 ${formatRate(data.run_totals.failed, terminalRuns(data))}`} />
            <Metric icon={<Activity className="h-4 w-4" />} label="已取消" value={data.run_totals.canceled} detail={`用户停止 ${formatNumber(data.run_totals.user_stopped)} / 系统 ${formatNumber(data.run_totals.system_canceled)}`} />
          </div>
        </section>

        <section className="mt-4 grid gap-3 border-t border-border/70 pt-4 xl:grid-cols-2">
          <div className="space-y-3">
            <SectionTitle title="模型调用尝试" detail={`${activeLabel} · ${formatNumber(data.totals.requests)} 次尝试`} />
            <div className="grid gap-2 sm:grid-cols-2">
              <Metric icon={<Database className="h-4 w-4" />} label="总 Token" value={data.totals.total_tokens} detail={`输入 ${formatNumber(data.totals.prompt_tokens)} / 输出 ${formatNumber(data.totals.completion_tokens)}`} />
              <Metric icon={<Activity className="h-4 w-4" />} label="上游失败率" value={formatRate(data.totals.failures, data.totals.requests)} detail={`失败 ${formatNumber(data.totals.failures)} / 取消 ${formatNumber(data.totals.canceled)}`} />
            </div>
            <UsageTable title="按模型" rows={data.by_model} columns={modelColumns} empty="暂无模型调用" />
            <UsageTable title="按调用类型" rows={data.by_kind} columns={kindColumns} empty="暂无调用类型" />
          </div>

          <div className="space-y-3">
            <SectionTitle title="工具用量" detail={`${activeLabel} · ${formatNumber(data.tool_totals.calls)} 次调用`} />
            <div className="grid gap-2 sm:grid-cols-2">
              <Metric icon={<Wrench className="h-4 w-4" />} label="工具调用" value={data.tool_totals.calls} detail={`失败 ${formatNumber(data.tool_totals.failures)} / 降级 ${formatNumber(data.tool_totals.degraded)}`} />
              <Metric icon={<Zap className="h-4 w-4" />} label="工具结果体量" value={data.tool_totals.context_tokens} detail={`近似估算 · 均耗时 ${formatDuration(data.tool_totals.avg_duration_ms)}`} />
            </div>
            <ToolTable rows={data.by_tool} />
          </div>
        </section>

        <section className="mt-4 border-t border-border/70 pt-4">
          <SectionTitle title="全部用户" detail={`${activeLabel} · ${formatNumber(data.by_user.length)} 人`} />
          <div className="mt-3">
            <UsageTable title="按用户" rows={data.by_user} columns={userColumns} empty="暂无用户调用" />
          </div>
        </section>

        <section className="mt-4 border-t border-border/70 pt-4">
          <SectionTitle title="今天用户组限额压力" detail="按最接近限额排序，0 表示不限" />
          <div className="mt-3 grid gap-2 md:grid-cols-3 xl:grid-cols-6">
            <Metric icon={<Activity className="h-4 w-4" />} label="消息" value={sumQuota(activeUsers, "daily_messages")} />
            <Metric icon={<Database className="h-4 w-4" />} label="模型 Token" value={sumQuota(activeUsers, "daily_model_tokens")} />
            <Metric icon={<Wrench className="h-4 w-4" />} label="工具调用" value={sumQuota(activeUsers, "daily_tool_calls")} />
            <Metric icon={<Search className="h-4 w-4" />} label="搜索" value={sumQuota(activeUsers, "daily_web_searches")} />
            <Metric icon={<Zap className="h-4 w-4" />} label="提取" value={sumQuota(activeUsers, "daily_web_extracts")} />
            <Metric icon={<FileText className="h-4 w-4" />} label="OCR" value={sumQuota(activeUsers, "daily_ocr_files")} detail={`${formatNumber(sumQuota(activeUsers, "daily_ocr_pages"))} 页`} />
          </div>
          <div className="mt-3 divide-y divide-border/70 rounded-md border border-border/70">
            {activeUsers.length === 0 ? (
              <div className="px-3 py-8 text-center text-sm text-muted-foreground">今天暂无触发限额统计的用户</div>
            ) : (
              activeUsers.map((user) => <QuotaUserRow key={user.user_id} user={user} />)
            )}
          </div>
        </section>
      </div>
    </div>
  )
}

function QuotaUserRow({ user }: { user: QuotaUserUsage }) {
  const pressure = quotaPressure(user)
  return (
    <div className="grid gap-3 px-3 py-3 lg:grid-cols-[220px_minmax(0,1fr)]">
      <div className="min-w-0">
        <div className="truncate text-sm font-semibold">{user.username || `用户 #${user.user_id}`}</div>
        <div className="mt-1 text-sm text-muted-foreground">{user.group_name || "未分组"}</div>
        <div className="mt-2 text-sm text-muted-foreground">{pressure >= 0 ? `最高压力 ${Math.round(pressure * 100)}%` : "全部不限额"}</div>
        <div className="mt-2 text-sm text-muted-foreground">重置 {formatTime(user.reset_at)}</div>
      </div>
      <div className="grid gap-2 md:grid-cols-2">
        <LimitBar label="消息" used={user.daily_messages} limit={user.daily_message_limit} />
        <LimitBar label="模型 Token" used={user.daily_model_tokens} limit={user.daily_token_limit} />
        <LimitBar label="工具调用" used={user.daily_tool_calls} limit={user.daily_tool_call_limit} />
        <LimitBar label="搜索" used={user.daily_web_searches} limit={user.daily_web_search_limit} />
        <LimitBar label="提取" used={user.daily_web_extracts} limit={user.daily_web_extract_limit} />
        <LimitBar label="OCR 文件" used={user.daily_ocr_files} limit={user.daily_ocr_file_limit} />
        <LimitBar label="OCR 页数" used={user.daily_ocr_pages} limit={user.daily_ocr_page_limit} />
      </div>
    </div>
  )
}

function LimitBar({ label, used, limit }: { label: string; used: number; limit: number }) {
  const percent = limit > 0 ? Math.min(100, Math.round((used / limit) * 100)) : 0
  const tone = limit > 0 && percent >= 90 ? "bg-rose-500" : limit > 0 && percent >= 70 ? "bg-amber-500" : "bg-emerald-500"
  return (
    <div>
      <div className="mb-1 flex items-center justify-between gap-2 text-sm">
        <span>{label}</span>
        <span className="tabular-nums text-muted-foreground">{limit > 0 ? `${formatNumber(used)} / ${formatNumber(limit)}` : `${formatNumber(used)} / 不限`}</span>
      </div>
      <div className="h-1.5 overflow-hidden rounded bg-muted">
        <div className={`h-full ${tone}`} style={{ width: `${limit > 0 ? percent : 0}%` }} />
      </div>
    </div>
  )
}

function Metric({ icon, label, value, detail }: { icon: React.ReactElement; label: string; value: number | string; detail?: string }) {
  return (
    <div className="rounded-md border border-border/70 px-3 py-2.5">
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        {icon}
        <span>{label}</span>
      </div>
      <div className="mt-1 text-lg font-semibold tabular-nums">{typeof value === "number" ? formatNumber(value) : value}</div>
      {detail ? <div className="mt-1 truncate text-sm text-muted-foreground">{detail}</div> : null}
    </div>
  )
}

function SectionTitle({ title, detail }: { title: string; detail: string }) {
  return (
    <div className="flex items-center justify-between gap-3">
      <div className="text-sm font-semibold">{title}</div>
      <div className="text-sm text-muted-foreground">{detail}</div>
    </div>
  )
}

type Column<T> = {
  key: string
  label: string
  className?: string
  render: (row: T) => React.ReactNode
}

function UsageTable<T extends UsageTotals>({ title, rows, columns, empty }: { title: string; rows: T[]; columns: Column<T>[]; empty: string }) {
  return (
    <div className="overflow-hidden rounded-md border border-border/70">
      <div className="flex h-9 items-center justify-between border-b border-border/70 px-3">
        <div className="text-sm font-medium">{title}</div>
        <div className="text-sm text-muted-foreground">{rows.length} 项</div>
      </div>
      <div className="overflow-x-auto">
        <table className="w-full min-w-[640px] text-sm">
          <thead className="bg-muted/40 text-muted-foreground">
            <tr>
              {columns.map((column) => (
                <th key={column.key} className={`px-3 py-2 text-left font-medium ${column.className || ""}`}>{column.label}</th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-y divide-border/60">
            {rows.length === 0 ? (
              <tr>
                <td colSpan={columns.length} className="px-3 py-8 text-center text-sm text-muted-foreground">{empty}</td>
              </tr>
            ) : rows.map((row, index) => (
              <tr key={rowKey(row, index)} className="hover:bg-muted/30">
                {columns.map((column) => (
                  <td key={column.key} className={`px-3 py-2 align-middle ${column.className || ""}`}>{column.render(row)}</td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}

function ToolTable({ rows }: { rows: UsageByTool[] }) {
  return (
    <div className="divide-y divide-border/70 rounded-md border border-border/70">
      {rows.length === 0 ? (
        <div className="px-3 py-8 text-center text-sm text-muted-foreground">暂无工具调用</div>
      ) : rows.map((row) => (
        <div key={row.tool_key} className="grid gap-2 px-3 py-3 sm:grid-cols-[minmax(0,1fr)_160px_160px]">
          <div className="min-w-0">
            <div className="truncate text-sm font-semibold">{toolLabel(row.tool_key)}</div>
            <div className="mt-1 text-sm text-muted-foreground">{row.tool_key}</div>
          </div>
          <div className="text-sm">
            <div className="font-medium tabular-nums">{formatNumber(row.calls)} 次</div>
            <div className="text-muted-foreground">失败 {formatNumber(row.failures)} · 降级 {formatNumber(row.degraded)}</div>
          </div>
          <div className="text-sm">
            <div className="font-medium tabular-nums">{formatNumber(row.context_tokens)}</div>
            <div className="text-muted-foreground">结果体量 · 均耗时 {formatDuration(row.avg_duration_ms)}</div>
          </div>
        </div>
      ))}
    </div>
  )
}

const modelColumns: Column<UsageByModel>[] = [
  { key: "model", label: "模型", render: (row) => <ModelCell provider={row.provider} model={row.model_id} /> },
  ...commonColumns<UsageByModel>(),
]

const kindColumns: Column<UsageByKind>[] = [
  { key: "kind", label: "类型", render: (row) => <span className="font-medium">{kindLabel(row.kind)}</span> },
  ...commonColumns<UsageByKind>(),
]

const userColumns: Column<UsageByUser>[] = [
  { key: "user", label: "用户", render: (row) => <span className="font-medium">{row.username || `用户 #${row.user_id || "-"}`}</span> },
  ...commonColumns<UsageByUser>(),
]

function commonColumns<T extends UsageTotals>(): Column<T>[] {
  return [
    { key: "requests", label: "尝试", className: "text-right tabular-nums", render: (row) => formatNumber(row.requests) },
    { key: "failed", label: "上游失败", className: "text-right tabular-nums", render: (row) => `${formatNumber(row.failures)} / ${formatRate(row.failures, row.requests)}` },
    { key: "canceled", label: "取消", className: "text-right tabular-nums", render: (row) => formatNumber(row.canceled) },
    { key: "tokens", label: "Token", className: "text-right tabular-nums", render: (row) => formatNumber(row.total_tokens) },
    { key: "duration", label: "均耗时", className: "text-right tabular-nums", render: (row) => formatDuration(row.avg_duration_ms) },
  ]
}

function ModelCell({ provider, model }: { provider: string; model: string }) {
  return (
    <div className="min-w-0">
      <div className="truncate font-medium">{model || "unknown"}</div>
      <div className="text-sm text-muted-foreground">{provider || "unknown"}</div>
    </div>
  )
}

function normalizeUsage(res: AdminUsageResponse): AdminUsageResponse {
  return {
    totals: res.totals || emptyTotals(),
    run_totals: res.run_totals || emptyRunTotals(),
    by_user: res.by_user || [],
    by_model: res.by_model || [],
    by_kind: res.by_kind || [],
    tool_totals: res.tool_totals || emptyToolTotals(),
    by_tool: res.by_tool || [],
    quota_users: res.quota_users || [],
  }
}

function emptyTotals(): UsageTotals {
  return {
    requests: 0,
    successes: 0,
    failures: 0,
    canceled: 0,
    prompt_tokens: 0,
    completion_tokens: 0,
    total_tokens: 0,
    cached_tokens: 0,
    reasoning_tokens: 0,
    avg_duration_ms: 0,
  }
}

function emptyRunTotals() {
  return {
    runs: 0,
    running: 0,
    completed: 0,
    failed: 0,
    canceled: 0,
    user_stopped: 0,
    system_canceled: 0,
    avg_duration_ms: 0,
  }
}

function emptyToolTotals(): ToolUsageTotals {
  return {
    calls: 0,
    successes: 0,
    failures: 0,
    degraded: 0,
    web_search_calls: 0,
    web_extract_calls: 0,
    context_tokens: 0,
    truncated: 0,
    avg_duration_ms: 0,
  }
}

function terminalRuns(data: AdminUsageResponse) {
  return data.run_totals.completed + data.run_totals.failed + data.run_totals.canceled
}

function sumQuota(rows: QuotaUserUsage[], key: keyof QuotaUserUsage) {
  return rows.reduce((sum, row) => {
    const value = row[key]
    return sum + (typeof value === "number" ? value : 0)
  }, 0)
}

function quotaPressure(user: QuotaUserUsage) {
  const ratios = [
    ratio(user.daily_messages, user.daily_message_limit),
    ratio(user.daily_model_tokens, user.daily_token_limit),
    ratio(user.daily_tool_calls, user.daily_tool_call_limit),
    ratio(user.daily_web_searches, user.daily_web_search_limit),
    ratio(user.daily_web_extracts, user.daily_web_extract_limit),
    ratio(user.daily_ocr_files, user.daily_ocr_file_limit),
    ratio(user.daily_ocr_pages, user.daily_ocr_page_limit),
  ].filter((value) => value >= 0)
  return ratios.length > 0 ? Math.max(...ratios) : -1
}

function ratio(used: number, limit: number) {
  return limit > 0 ? used / limit : -1
}

function rowKey(row: UsageTotals, index: number) {
  return `${row.last_called_at || "none"}-${row.requests}-${index}`
}

function kindLabel(kind: UsageByKind["kind"]) {
  if (kind === "retry") return "重试"
  if (kind === "title") return "标题生成"
  if (kind === "compression") return "压缩"
  if (kind === "tool_chain") return "工具链小模型"
  return "普通聊天"
}

function toolLabel(key: string) {
  if (key === "web_search") return "联网搜索"
  if (key === "web_extract") return "网页提取"
  if (key === "file_read") return "读取文件"
  if (key === "file_search") return "搜索文件"
  if (key === "file_list") return "文件列表"
  if (key === "memory") return "会话记忆"
  if (key.startsWith("skill_")) return "Skill 工具"
  return key || "工具"
}

function rangeLabel(range: UsageRange) {
  if (range === "today") return "今天治理"
  if (range === "30d") return "30 天治理"
  return "7 天治理"
}

function localDateValue(offsetDays: number) {
  const date = new Date()
  date.setHours(12, 0, 0, 0)
  date.setDate(date.getDate() + offsetDays)
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`
}

function customUsageQuery(startValue: string, endValue: string): UsageQuery | null {
  const start = localDateBoundary(startValue, 0)
  const end = localDateBoundary(endValue, 1)
  if (!start || !end || start >= end || end.getTime() - start.getTime() > 90 * 24 * 60 * 60 * 1000) return null
  return { start_at: start.toISOString(), end_at: end.toISOString() }
}

function localDateBoundary(value: string, dayOffset: number) {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value)
  if (!match) return null
  const date = new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3]) + dayOffset)
  return Number.isNaN(date.getTime()) ? null : date
}

function formatNumber(value: number) {
  return new Intl.NumberFormat("zh-CN").format(value || 0)
}

function formatRate(part: number, total: number) {
  if (!total) return "0%"
  return `${((part / total) * 100).toFixed(part === 0 ? 0 : 1)}%`
}

function formatDuration(ms: number) {
  if (!ms) return "0s"
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(ms < 10000 ? 1 : 0)}s`
}

function formatTime(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return "-"
  return date.toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" })
}
