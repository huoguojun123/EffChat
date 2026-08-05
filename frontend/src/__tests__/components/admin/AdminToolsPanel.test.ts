import { describe, expect, it } from "vitest"
import type { GovernanceEvent } from "@/api/admin"
import { toolEventActionLabel, toolEventChange } from "@/components/admin/adminToolGovernance"

function event(overrides: Partial<GovernanceEvent> = {}): GovernanceEvent {
  return {
    id: 17,
    resource_type: "tool",
    resource_key: "web_search",
    action: "update",
    actor_type: "admin",
    actor_user_id: 3,
    reason: "fixture update",
    created_at: "2026-08-05T00:00:00Z",
    ...overrides,
  }
}

describe("AdminToolsPanel governance history", () => {
  it("renders the audited enabled and timeout transition", () => {
    expect(toolEventChange(event({
      before_state: { enabled: true, timeout_seconds: 20 },
      after_state: { enabled: false, timeout_seconds: 35 },
    }))).toBe("启用 / 20s → 停用 / 35s")

    expect(toolEventChange(event({ before_state: undefined, after_state: { enabled: true } })))
      .toBe("无 / — → 启用 / —")
  })

  it("keeps each governance action distinguishable", () => {
    expect(["create", "update", "delete", "import", "rollback"].map((action) =>
      toolEventActionLabel(action as GovernanceEvent["action"]),
    )).toEqual(["创建", "更新", "删除", "导入", "回滚"])
  })
})
