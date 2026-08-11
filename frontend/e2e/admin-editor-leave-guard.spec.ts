import { expect, test, type Page, type Route } from "@playwright/test"

const skill = {
  id: "guard-skill",
  name: "Guard Skill",
  description: "Guard description",
  source_type: "manual",
  checksum: "guard-checksum",
  package_checksum: "guard-package",
  entry_path: "SKILL.md",
  min_group_level: 0,
  files: [{ path: "SKILL.md", kind: "entry", size: 20, checksum: "guard-file" }],
  enabled: true,
  is_builtin: false,
  authorized: true,
  created_at: "2026-08-05T00:00:00Z",
  updated_at: "2026-08-05T00:00:00Z",
}

const prompt = {
  id: 1,
  title: "Guard Prompt",
  content: "Guard content",
  description: "Guard description",
  tags: [],
  group_id: null,
  group_name: "默认分组",
  is_public: true,
  created_at: "2026-08-05T00:00:00Z",
  updated_at: "2026-08-05T00:00:00Z",
}

async function fulfillJSON(route: Route, json: unknown) {
  await route.fulfill({ json })
}

async function installRoutes(page: Page) {
  await page.addInitScript(() => localStorage.setItem("token", "fixture-token"))
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname
    if (path === "/api/v1/users/me") return fulfillJSON(route, { id: 1, username: "admin", role: "admin", is_active: true })
    if (path === "/api/v1/system/info") return fulfillJSON(route, { system_name: "EffChat" })
    if (path === "/api/v1/admin/skills") return fulfillJSON(route, { skills: [skill] })
    if (path.endsWith("/files/content")) return fulfillJSON(route, { file: skill.files[0], content: "# Guard Skill" })
    if (path === "/api/v1/admin/prompts") return fulfillJSON(route, { prompts: [prompt], total: 1 })
    if (path === "/api/v1/admin/groups") return fulfillJSON(route, { groups: [], total: 0 })
    if (path === "/api/v1/admin/channels") return fulfillJSON(route, { channels: [], total: 0 })
    if (path === "/api/v1/models") return fulfillJSON(route, { models: [], total: 0 })
    if (path === "/api/v1/sessions") return fulfillJSON(route, { sessions: [], has_more: false, next_offset: 0 })
    if (path === "/api/v1/session-folders") return fulfillJSON(route, { folders: [] })
    return fulfillJSON(route, {})
  })
}

test.beforeEach(async ({ page }) => installRoutes(page))

test("a dirty Skill blocks Admin section navigation until explicitly discarded", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/admin/skills")
  await page.getByText("Guard Skill", { exact: true }).click()
  const description = page.getByLabel("描述")
  await description.fill("Unsaved Skill description")

  const navigation = page.getByRole("navigation", { name: "管理后台导航" }).first()
  await navigation.getByRole("button", { name: "模型", exact: true }).click()
  await expect(page.getByRole("heading", { name: "放弃未保存修改？" })).toBeVisible()
  await page.getByRole("button", { name: "继续编辑" }).click()
  await expect(page).toHaveURL(/\/admin\/skills$/)
  await expect(description).toHaveValue("Unsaved Skill description")

  await navigation.getByRole("button", { name: "模型", exact: true }).click()
  await page.getByRole("button", { name: "放弃修改" }).click()
  await expect(page).toHaveURL(/\/admin\/models$/)
})

test("a dirty Prompt blocks mobile return-to-chat navigation", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/admin/prompts")
  await page.getByText("Guard Prompt", { exact: true }).click()
  const content = page.getByLabel("内容")
  await content.fill("Unsaved mobile Prompt")

  await page.getByRole("button", { name: "返回聊天" }).click()
  await expect(page.getByRole("heading", { name: "放弃未保存修改？" })).toBeVisible()
  await page.getByRole("button", { name: "继续编辑" }).click()
  await expect(page).toHaveURL(/\/admin\/prompts$/)
  await expect(content).toHaveValue("Unsaved mobile Prompt")

  await page.getByRole("button", { name: "返回聊天" }).click()
  await page.getByRole("button", { name: "放弃修改" }).click()
  await expect(page).toHaveURL(/\/$/)
})
