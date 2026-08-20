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

test("Skill editor and deletion use product confirmations", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("token", "test-token"))
  const deleteRequests: string[] = []
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (request.method() === "DELETE") deleteRequests.push(path)
    if (path === "/api/v1/users/me") return route.fulfill({ json: { id: 1, username: "admin", role: "admin", is_active: true } })
    if (path === "/api/v1/system/info") return route.fulfill({ json: { system_name: "EffChat" } })
    if (path === "/api/v1/models") return route.fulfill({ json: { models: [], total: 0 } })
    if (path === "/api/v1/admin/groups") return route.fulfill({ json: { groups: [], total: 0 } })
    if (path === "/api/v1/admin/skills" && request.method() === "GET") return route.fulfill({ json: { skills: [skill] } })
    if (path === "/api/v1/admin/skills/fixture-skill/files") return route.fulfill({ json: { files: skill.files } })
    if (path === "/api/v1/admin/skills/fixture-skill/files/content") return route.fulfill({ json: { file: skill.files[0], content: "# Fixture Skill" } })
    if (path === "/api/v1/admin/skills/fixture-skill" && request.method() === "DELETE") return route.fulfill({ json: { message: "deleted" } })
    return route.fulfill({ json: {} })
  })

  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/admin/skills")
  await page.getByText("Fixture Skill", { exact: true }).click()
  await expect(page.getByLabel("名称")).toHaveValue("Fixture Skill")
  await page.getByLabel("描述").fill("changed")

  const createButton = page.getByRole("button", { name: "新建", exact: true })
  await createButton.click()
  const discardDialog = page.getByRole("dialog", { name: "放弃未保存修改？" })
  await expect(discardDialog).toBeVisible()
  await discardDialog.getByRole("button", { name: "继续编辑" }).click()
  await expect(page.getByLabel("描述")).toHaveValue("changed")
  await createButton.click()
  await page.getByRole("dialog", { name: "放弃未保存修改？" }).getByRole("button", { name: "放弃修改" }).click()
  await expect(page.getByLabel("名称")).toHaveValue("")

  await page.getByText("Fixture Skill", { exact: true }).click()
  const deleteButton = page.getByRole("button", { name: "删除 Skill：Fixture Skill" })
  await deleteButton.click()
  const deleteDialog = page.getByRole("dialog", { name: "删除 Skill？" })
  await expect(deleteDialog).toContainText("旧文件包也会被清理")
  await deleteDialog.getByRole("button", { name: "取消" }).click()
  await expect(deleteButton).toBeFocused()
  expect(deleteRequests).toHaveLength(0)

  await deleteButton.click()
  await page.getByRole("dialog", { name: "删除 Skill？" }).getByRole("button", { name: "删除 Skill" }).click()
  await expect.poll(() => deleteRequests.length).toBe(1)
  expect(deleteRequests[0]).toBe("/api/v1/admin/skills/fixture-skill")
})
