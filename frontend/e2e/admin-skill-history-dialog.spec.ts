import { expect, test } from "@playwright/test"

const skill = {
  id: "fixture-skill",
  name: "Fixture Skill",
  description: "Fixture description",
  source_type: "manual",
  checksum: "fixture-checksum",
  package_checksum: "fixture-package",
  entry_path: "SKILL.md",
  min_group_level: 0,
  files: [{ path: "SKILL.md", kind: "entry", size: 20, checksum: "fixture-file" }],
  enabled: true,
  is_builtin: false,
  authorized: true,
  created_at: "2026-08-20T00:00:00Z",
  updated_at: "2026-08-20T00:00:00Z",
}

const event = {
  id: 42,
  resource_type: "skill",
  resource_key: "fixture-skill",
  action: "update",
  actor_type: "admin",
  actor_user_id: 1,
  reason: "fixture update",
  before_state: { enabled: true },
  after_state: { enabled: false },
  created_at: "2026-08-20T00:00:00Z",
}

test("Skill history rollback explains the new event and keeps cancellation reversible", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("token", "test-token"))
  const rollbackRequests: string[] = []
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (path === "/api/v1/users/me") return route.fulfill({ json: { id: 1, username: "admin", role: "admin", is_active: true } })
    if (path === "/api/v1/system/info") return route.fulfill({ json: { system_name: "EffChat" } })
    if (path === "/api/v1/models") return route.fulfill({ json: { models: [], total: 0 } })
    if (path === "/api/v1/admin/groups") return route.fulfill({ json: { groups: [], total: 0 } })
    if (path === "/api/v1/admin/skills" && request.method() === "GET") return route.fulfill({ json: { skills: [skill] } })
    if (path === "/api/v1/admin/skills/fixture-skill/history" && request.method() === "GET") return route.fulfill({ json: { events: [event] } })
    if (path === "/api/v1/admin/skills/events/42/rollback" && request.method() === "POST") {
      rollbackRequests.push(path)
      return route.fulfill({ json: { skill, event: { ...event, id: 43, action: "rollback", rollback_of_event_id: 42 } } })
    }
    return route.fulfill({ json: {} })
  })

  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/admin/skills")
  await expect(page.getByText("Fixture Skill", { exact: true })).toBeVisible()
  await page.getByRole("button", { name: "查看 Skill 变更历史：Fixture Skill" }).click()
  await expect(page.getByText("fixture update", { exact: true })).toBeVisible()

  const rollbackButton = page.getByRole("button", { name: "回滚", exact: true })
  await rollbackButton.click()
  const confirmation = page.getByRole("dialog", { name: "回滚 Skill 变更？" })
  await expect(confirmation).toBeVisible()
  await expect(confirmation).toContainText("恢复事件 #42 记录的状态")
  await confirmation.getByRole("button", { name: "取消" }).click()
  await expect(confirmation).toBeHidden()
  await expect(rollbackButton).toBeFocused()
  expect(rollbackRequests).toHaveLength(0)

  await rollbackButton.click()
  await page.getByRole("dialog", { name: "回滚 Skill 变更？" }).getByRole("button", { name: "确认回滚" }).click()
  await expect.poll(() => rollbackRequests.length).toBe(1)
})
