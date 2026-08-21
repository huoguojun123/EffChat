import { expect, test } from "@playwright/test"

const user = {
  id: 9,
  username: "fixture-user",
  nickname: "Fixture User",
  email: "fixture@example.invalid",
  role: "user",
  group_id: null,
  effective_group: { id: 1, name: "Default", level: 0, inherited: true },
  is_active: true,
  created_at: "2026-08-20T00:00:00Z",
  last_login_at: null,
}

test("user editor and password reset use product confirmations", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("token", "test-token"))
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (path === "/api/v1/users/me") return route.fulfill({ json: { id: 1, username: "admin", role: "admin", is_active: true } })
    if (path === "/api/v1/system/info") return route.fulfill({ json: { system_name: "EffChat" } })
    if (path === "/api/v1/models") return route.fulfill({ json: { models: [], total: 0 } })
    if (path === "/api/v1/admin/groups") return route.fulfill({ json: { groups: [{ id: 1, name: "Default", level: 0, is_default: true }], total: 1 } })
    if (path === "/api/v1/admin/users" && request.method() === "GET") return route.fulfill({ json: { users: [user], total: 1, has_more: false, next_offset: 1 } })
    return route.fulfill({ json: {} })
  })

  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/admin/users")
  await page.getByText("Fixture User", { exact: true }).click()
  await page.getByLabel("昵称").fill("Changed")
  const createButton = page.getByRole("button", { name: "新建用户" })
  await createButton.click()
  const discardDialog = page.getByRole("dialog", { name: "放弃未保存修改？" })
  await expect(discardDialog).toBeVisible()
  await discardDialog.getByRole("button", { name: "继续编辑" }).click()
  await expect(page.getByLabel("昵称")).toHaveValue("Changed")
  await createButton.click()
  await page.getByRole("dialog", { name: "放弃未保存修改？" }).getByRole("button", { name: "放弃修改" }).click()
  await expect(page.getByLabel("账号")).toHaveValue("")

  await page.getByText("Fixture User", { exact: true }).click()
  await page.getByRole("button", { name: "设置新密码" }).click()
  await page.getByLabel("新密码").fill("fixture-password")
  await page.getByRole("button", { name: "取消", exact: true }).click()
  const passwordDialog = page.getByRole("dialog", { name: "放弃未保存的新密码？" })
  await expect(passwordDialog).toBeVisible()
  await passwordDialog.getByRole("button", { name: "继续编辑" }).click()
  await expect(page.getByPlaceholder("至少 6 位")).toHaveValue("fixture-password")
  await page.getByRole("button", { name: "取消", exact: true }).click()
  await page.getByRole("dialog", { name: "放弃未保存的新密码？" }).getByRole("button", { name: "放弃密码" }).click()
  await expect(page.getByRole("button", { name: "设置新密码" })).toBeVisible()
})
