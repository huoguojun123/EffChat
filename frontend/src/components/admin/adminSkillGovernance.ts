import type { GovernanceEvent } from "@/api/admin"

type SkillGovernanceState = {
  enabled?: boolean
  min_group_level?: number
  package_checksum?: string
  deleted?: boolean
}

export function skillEventChange(event: GovernanceEvent) {
  const describe = (raw?: Record<string, unknown>) => {
    if (!raw) return "无"
    const state = raw as SkillGovernanceState
    if (state.deleted) return "已删除"
    const enabled = state.enabled ? "启用" : "停用"
    const level = state.min_group_level ?? 0
    const version = state.package_checksum ? state.package_checksum.slice(0, 8) : "无版本"
    return `${enabled} · L${level} · ${version}`
  }
  return `${describe(event.before_state)} → ${describe(event.after_state)}`
}
