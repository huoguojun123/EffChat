import { useMemo, useState, type Dispatch, type SetStateAction } from "react"
import { adminApi, type GovernanceEvent, type ToolConfig } from "@/api/admin"
import { History, Loader2, RotateCcw } from "lucide-react"
import { toolEventActionLabel, toolEventChange } from "./adminToolGovernance"

interface Props {
  tools: ToolConfig[]
  setTools: Dispatch<SetStateAction<ToolConfig[]>>
  setError: (error: string) => void
}

export function AdminToolsPanel({ tools, setTools, setError }: Props) {
  const sortedTools = useMemo(() => [...tools].sort((a, b) => a.sort_order - b.sort_order || a.key.localeCompare(b.key)), [tools])
  const [savingKey, setSavingKey] = useState("")
  const [expandedKey, setExpandedKey] = useState("")
  const [historyByKey, setHistoryByKey] = useState<Record<string, GovernanceEvent[]>>({})
  const [historyLoadingKey, setHistoryLoadingKey] = useState("")
  const [rollbackEventId, setRollbackEventId] = useState(0)

  async function toggleHistory(key: string) {
    if (expandedKey === key) {
      setExpandedKey("")
      return
    }
    setExpandedKey(key)
    if (historyByKey[key]) return
    setHistoryLoadingKey(key)
    setError("")
    try {
      const result = await adminApi.listToolConfigHistory(key)
      setHistoryByKey((current) => ({ ...current, [key]: result.events }))
    } catch (err) {
      setError(err instanceof Error ? err.message : "工具变更历史加载失败")
    } finally {
      setHistoryLoadingKey("")
    }
  }

  async function toggleTool(tool: ToolConfig) {
    if (savingKey) return
    const nextEnabled = !tool.enabled
    setSavingKey(tool.key)
    setError("")
    try {
      const result = await adminApi.saveToolConfig({
        key: tool.key,
        display_name: tool.display_name,
        enabled: nextEnabled,
        timeout_seconds: tool.timeout_seconds,
        sort_order: tool.sort_order,
        reason: nextEnabled ? "admin enabled Tool" : "admin disabled Tool",
      })
      setTools((prev) => prev.map((item) => (item.key === result.tool.key ? result.tool : item)))
      setHistoryByKey((current) => current[tool.key] ? { ...current, [tool.key]: [result.event, ...current[tool.key]] } : current)
    } catch (err) {
      setError(err instanceof Error ? err.message : "工具配置保存失败")
    } finally {
      setSavingKey("")
    }
  }

  async function rollbackEvent(toolKey: string, event: GovernanceEvent) {
    if (rollbackEventId) return
    setRollbackEventId(event.id)
    setError("")
    try {
      await adminApi.rollbackToolConfigEvent(event.id, `admin rollback of Tool event ${event.id}`)
      const [toolsResult, historyResult] = await Promise.all([
        adminApi.listToolConfigs(),
        adminApi.listToolConfigHistory(toolKey),
      ])
      setTools(toolsResult.tools)
      setHistoryByKey((current) => ({ ...current, [toolKey]: historyResult.events }))
    } catch (err) {
      setError(err instanceof Error ? err.message : "工具配置回滚失败")
    } finally {
      setRollbackEventId(0)
    }
  }

  return (
    <div className="h-full min-h-0 overflow-y-auto rounded-md border border-border/70">
      <div className="divide-y divide-border/70">
        {sortedTools.map((tool) => {
          const saving = savingKey === tool.key
          const events = historyByKey[tool.key] || []
          const rolledBack = new Set(events.flatMap((event) => event.rollback_of_event_id ? [event.rollback_of_event_id] : []))
          return (
            <div key={tool.key}>
              <div className="flex min-h-14 items-center justify-between gap-4 px-4 py-3">
                <div className="min-w-0">
                  <div className="flex min-w-0 items-center gap-2">
                    <span className="truncate text-sm font-medium">{tool.display_name || tool.key}</span>
                    <span className="shrink-0 rounded-md bg-muted px-1.5 py-0.5 text-xs text-muted-foreground">{tool.key}</span>
                  </div>
                  <div className={`mt-1 text-xs ${tool.enabled ? "text-emerald-600" : "text-muted-foreground"}`}>
                    {tool.enabled ? "当前可被 Agent 调用" : "已从 Agent 工具集中移除"}
                  </div>
                </div>
                <div className="flex shrink-0 items-center gap-2">
                  <button
                    type="button"
                    aria-label={`${tool.display_name || tool.key} 变更历史`}
                    aria-expanded={expandedKey === tool.key}
                    onClick={() => void toggleHistory(tool.key)}
                    className="inline-flex h-8 w-8 items-center justify-center rounded-md border border-border text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                    title="变更历史"
                  >
                    {historyLoadingKey === tool.key ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <History className="h-3.5 w-3.5" />}
                  </button>
                  <button
                    type="button"
                    aria-pressed={tool.enabled}
                    onClick={() => void toggleTool(tool)}
                    disabled={Boolean(savingKey)}
                    className={`inline-flex h-8 shrink-0 items-center gap-2 rounded-full border px-3 text-xs font-medium transition-colors motion-control ${
                      tool.enabled
                        ? "border-emerald-500/40 bg-emerald-50 text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-200"
                        : "border-border bg-background text-muted-foreground hover:bg-muted"
                    } disabled:cursor-not-allowed disabled:opacity-60`}
                  >
                    {saving ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : null}
                    {tool.enabled ? "已启用" : "已停用"}
                  </button>
                </div>
              </div>
              {expandedKey === tool.key ? (
                <div className="border-t border-border/60 bg-muted/20 px-4 py-3">
                  {events.length === 0 && historyLoadingKey !== tool.key ? <div className="text-xs text-muted-foreground">暂无变更记录</div> : null}
                  <div className="space-y-2">
                    {events.map((event) => {
                      const canRollback = event.action !== "rollback" && !rolledBack.has(event.id)
                      return (
                        <div key={event.id} className="flex items-start justify-between gap-3 text-xs">
                          <div className="min-w-0">
                            <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                              <span className="font-medium text-foreground">{toolEventActionLabel(event.action)}</span>
                              <span className="text-muted-foreground">#{event.id}</span>
                              <span className="text-muted-foreground">管理员 {event.actor_user_id ?? "系统"}</span>
                              <span className="text-muted-foreground">{new Date(event.created_at).toLocaleString("zh-CN")}</span>
                            </div>
                            <div className="mt-1 truncate text-muted-foreground" title={event.reason}>{event.reason}</div>
                            <div className="mt-1 font-mono text-xs text-muted-foreground">{toolEventChange(event)}</div>
                          </div>
                          {canRollback ? (
                            <button
                              type="button"
                              onClick={() => void rollbackEvent(tool.key, event)}
                              disabled={Boolean(rollbackEventId || savingKey)}
                              className="inline-flex h-7 shrink-0 items-center gap-1 rounded-md border border-border px-2 text-muted-foreground transition-colors hover:bg-background hover:text-foreground disabled:opacity-50"
                            >
                              {rollbackEventId === event.id ? <Loader2 className="h-3 w-3 animate-spin" /> : <RotateCcw className="h-3 w-3" />}
                              回滚
                            </button>
                          ) : null}
                        </div>
                      )
                    })}
                  </div>
                </div>
              ) : null}
            </div>
          )
        })}
      </div>
      {sortedTools.length === 0 ? (
        <div className="flex h-full items-center justify-center text-sm text-muted-foreground">暂无工具配置</div>
      ) : null}
    </div>
  )
}
