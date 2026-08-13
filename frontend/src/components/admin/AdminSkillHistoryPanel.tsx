import { useEffect, useMemo, useState } from "react"
import { adminApi, type GovernanceEvent } from "@/api/admin"
import type { SkillDefinition } from "@/types"
import { Loader2, RotateCcw } from "lucide-react"
import { governanceActionLabel } from "./adminGovernance"
import { skillEventChange } from "./adminSkillGovernance"

interface Props {
  skill: SkillDefinition
  onRollback: (skillID: string, restored: SkillDefinition | null) => void
  setError: (error: string) => void
}

export function AdminSkillHistoryPanel({ skill, onRollback, setError }: Props) {
  const [events, setEvents] = useState<GovernanceEvent[]>([])
  const [loading, setLoading] = useState(true)
  const [rollbackEventID, setRollbackEventID] = useState(0)
  const rolledBack = useMemo(() => new Set(events.flatMap((event) => event.rollback_of_event_id ? [event.rollback_of_event_id] : [])), [events])

  useEffect(() => {
    let active = true
    void adminApi.listSkillHistory(skill.id).then((result) => {
      if (active) setEvents(result.events)
    }).catch((err) => {
      if (active) setError(err instanceof Error ? err.message : "Skill 变更历史加载失败")
    }).finally(() => {
      if (active) setLoading(false)
    })
    return () => { active = false }
  }, [setError, skill.id])

  async function rollback(event: GovernanceEvent) {
    if (!window.confirm(`确定回滚 Skill「${skill.name}」的事件 #${event.id} 吗？`)) return
    setRollbackEventID(event.id)
    setError("")
    try {
      const result = await adminApi.rollbackSkillEvent(event.id, `admin rollback of Skill event ${event.id}`)
      onRollback(skill.id, result.skill)
      const history = await adminApi.listSkillHistory(skill.id)
      setEvents(history.events)
    } catch (err) {
      setError(err instanceof Error ? err.message : "Skill 回滚失败")
    } finally {
      setRollbackEventID(0)
    }
  }

  if (loading) {
    return <div className="flex items-center gap-2 text-xs text-muted-foreground"><Loader2 className="h-3.5 w-3.5 animate-spin" />加载变更历史</div>
  }
  if (events.length === 0) {
    return <div className="text-xs text-muted-foreground">暂无变更记录</div>
  }
  return (
    <div className="space-y-2">
      {events.map((event) => {
        const canRollback = event.action !== "rollback" && !rolledBack.has(event.id)
        return (
          <div key={event.id} className="flex items-start justify-between gap-3 text-xs">
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                <span className="font-medium text-foreground">{governanceActionLabel(event.action)}</span>
                <span className="text-muted-foreground">#{event.id}</span>
                <span className="text-muted-foreground">{event.actor_type === "import" ? "导入" : "管理员"} {event.actor_user_id ?? "系统"}</span>
                <span className="text-muted-foreground">{new Date(event.created_at).toLocaleString("zh-CN")}</span>
              </div>
              <div className="mt-1 truncate text-muted-foreground" title={event.reason}>{event.reason}</div>
              <div className="mt-1 font-mono text-[11px] text-muted-foreground">{skillEventChange(event)}</div>
            </div>
            {canRollback ? (
              <button
                type="button"
                onClick={() => void rollback(event)}
                disabled={Boolean(rollbackEventID)}
                className="inline-flex h-7 shrink-0 items-center gap-1 rounded-md border border-border px-2 text-muted-foreground transition-colors hover:bg-background hover:text-foreground disabled:opacity-50"
              >
                {rollbackEventID === event.id ? <Loader2 className="h-3 w-3 animate-spin" /> : <RotateCcw className="h-3 w-3" />}
                回滚
              </button>
            ) : null}
          </div>
        )
      })}
    </div>
  )
}
