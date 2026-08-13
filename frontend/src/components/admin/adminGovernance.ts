import type { GovernanceEvent } from "@/api/admin"

export function governanceActionLabel(action: GovernanceEvent["action"]) {
  if (action === "create") return "创建"
  if (action === "delete") return "删除"
  if (action === "import") return "导入"
  if (action === "rollback") return "回滚"
  return "更新"
}
