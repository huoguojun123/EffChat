import { useMemo, useState, type Dispatch, type SetStateAction } from "react"
import { adminApi, type ToolConfig } from "@/api/admin"
import { Loader2 } from "lucide-react"

interface Props {
  tools: ToolConfig[]
  setTools: Dispatch<SetStateAction<ToolConfig[]>>
  setError: (error: string) => void
}

export function AdminToolsPanel({ tools, setTools, setError }: Props) {
  const sortedTools = useMemo(() => [...tools].sort((a, b) => a.sort_order - b.sort_order || a.key.localeCompare(b.key)), [tools])
  const [savingKey, setSavingKey] = useState("")

  async function toggleTool(tool: ToolConfig) {
    if (savingKey) return
    const nextEnabled = !tool.enabled
    setSavingKey(tool.key)
    setError("")
    try {
      const saved = await adminApi.saveToolConfig({
        key: tool.key,
        display_name: tool.display_name,
        enabled: nextEnabled,
        timeout_seconds: tool.timeout_seconds,
        sort_order: tool.sort_order,
      })
      setTools((prev) => prev.map((item) => (item.key === saved.key ? saved : item)))
    } catch (err) {
      setError(err instanceof Error ? err.message : "工具配置保存失败")
    } finally {
      setSavingKey("")
    }
  }

  return (
    <div className="h-full min-h-0 overflow-y-auto rounded-md border border-border/70">
      <div className="divide-y divide-border/70">
        {sortedTools.map((tool) => {
          const saving = savingKey === tool.key
          return (
            <div key={tool.key} className="flex min-h-14 items-center justify-between gap-4 px-4 py-3">
              <div className="min-w-0">
                <div className="flex min-w-0 items-center gap-2">
                  <span className="truncate text-sm font-medium">{tool.display_name || tool.key}</span>
                  <span className="shrink-0 rounded-md bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">{tool.key}</span>
                </div>
                <div className={`mt-1 text-xs ${tool.enabled ? "text-emerald-600" : "text-muted-foreground"}`}>
                  {tool.enabled ? "当前可被 Agent 调用" : "已从 Agent 工具集中移除"}
                </div>
              </div>
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
          )
        })}
      </div>
      {sortedTools.length === 0 ? (
        <div className="flex h-full items-center justify-center text-sm text-muted-foreground">暂无工具配置</div>
      ) : null}
    </div>
  )
}
