import type { GovernanceEvent } from "@/api/admin"

export function toolEventChange(event: GovernanceEvent) {
  const before = event.before_state as { enabled?: boolean; timeout_seconds?: number } | undefined
  const after = event.after_state as { enabled?: boolean; timeout_seconds?: number } | undefined
  const enabled = (value?: boolean) => value === undefined ? "无" : value ? "启用" : "停用"
  const timeout = (value?: number) => value === undefined ? "—" : `${value}s`
  return `${enabled(before?.enabled)} / ${timeout(before?.timeout_seconds)} → ${enabled(after?.enabled)} / ${timeout(after?.timeout_seconds)}`
}

export function toolEventActionLabel(action: GovernanceEvent["action"]) {
  if (action === "create") return "创建"
  if (action === "delete") return "删除"
  if (action === "import") return "导入"
  if (action === "rollback") return "回滚"
  return "更新"
}
