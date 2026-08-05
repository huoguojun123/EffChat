import { describe, expect, it } from "vitest"
import type { GovernanceEvent } from "@/api/admin"
import { governanceActionLabel } from "@/components/admin/adminGovernance"
import { skillEventChange } from "@/components/admin/adminSkillGovernance"

function event(overrides: Partial<GovernanceEvent> = {}): GovernanceEvent {
  return {
    id: 21,
    resource_type: "skill",
    resource_key: "fixture-skill",
    action: "update",
    actor_type: "admin",
    actor_user_id: 4,
    reason: "fixture update",
    created_at: "2026-08-05T00:00:00Z",
    ...overrides,
  }
}

describe("Admin Skill governance history", () => {
  it("summarizes metadata, package version, and tombstone transitions", () => {
    expect(skillEventChange(event({
      before_state: { enabled: true, min_group_level: 10, package_checksum: "aaaaaaaa11111111" },
      after_state: { enabled: false, min_group_level: 20, package_checksum: "bbbbbbbb22222222" },
    }))).toBe("启用 · L10 · aaaaaaaa → 停用 · L20 · bbbbbbbb")

    expect(skillEventChange(event({
      action: "delete",
      before_state: { enabled: true, min_group_level: 0, package_checksum: "cccccccc33333333" },
      after_state: { deleted: true },
    }))).toBe("启用 · L0 · cccccccc → 已删除")
  })

  it("uses the shared governance action vocabulary", () => {
    expect(governanceActionLabel("import")).toBe("导入")
    expect(governanceActionLabel("rollback")).toBe("回滚")
  })
})
