import { expect, test, type Page, type Route } from "@playwright/test"

const currentUser = {
  id: 1,
  username: "fixture-admin",
  nickname: "Fixture Admin",
  email: "admin@example.invalid",
  role: "admin",
  is_active: true,
}

async function fulfillJSON(route: Route, json: unknown, delay = 0) {
  if (delay) await new Promise((resolve) => setTimeout(resolve, delay))
  await route.fulfill({ json })
}

async function installRoutes(page: Page) {
  await page.addInitScript(() => localStorage.setItem("token", "fixture-token"))
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (path === "/api/v1/users/me" && request.method() === "GET") return fulfillJSON(route, currentUser)
    if (path === "/api/v1/users/me" && request.method() === "PATCH") {
      return fulfillJSON(route, { ...currentUser, ...request.postDataJSON() }, 300)
    }
    if (path === "/api/v1/users/me/password" && request.method() === "PUT") {
      return fulfillJSON(route, { message: "updated" }, 300)
    }
    if (path === "/api/v1/system/info") return fulfillJSON(route, { system_name: "EffChat" })
    if (path === "/api/v1/models") return fulfillJSON(route, { models: [], total: 0 })
    if (path === "/api/v1/sessions") return fulfillJSON(route, { sessions: [], has_more: false, next_offset: 0 })
    if (path === "/api/v1/session-folders") return fulfillJSON(route, { folders: [] })
    if (path === "/api/v1/prompts" || path === "/api/v1/prompts/public") return fulfillJSON(route, { prompts: [], total: 0 })
    if (path === "/api/v1/prompt-groups") return fulfillJSON(route, { groups: [] })
    return fulfillJSON(route, {})
  })
}

async function openSettings(page: Page) {
  await page.getByRole("button", { name: /Fixture Admin/ }).click()
  await page.getByRole("button", { name: "设置", exact: true }).click()
  await expect(page.getByRole("dialog", { name: "设置" })).toBeVisible()
}

test.beforeEach(async ({ page }) => {
  await installRoutes(page)
  await page.goto("/")
  await openSettings(page)
})

test("dirty settings guard tab switches, close, and Prompt Manager navigation", async ({ page }) => {
  const email = page.getByLabel("邮箱")
  await email.fill("dirty-profile@example.invalid")

  page.once("dialog", (dialog) => dialog.dismiss())
  await page.getByRole("button", { name: "密码", exact: true }).click()
  await expect(email).toHaveValue("dirty-profile@example.invalid")

  page.once("dialog", (dialog) => dialog.accept())
  await page.getByRole("button", { name: "密码", exact: true }).click()
  const currentPassword = page.getByLabel("当前密码")
  await expect(currentPassword).toBeVisible()
  await currentPassword.fill("old-secret")

  page.once("dialog", (dialog) => dialog.dismiss())
  await page.keyboard.press("Escape")
  await expect(currentPassword).toHaveValue("old-secret")

  page.once("dialog", (dialog) => dialog.accept())
  await page.keyboard.press("Escape")
  await expect(page.getByRole("dialog", { name: "设置" })).toHaveCount(0)

  await openSettings(page)
  await page.getByLabel("昵称").fill("New Fixture Name")
  page.once("dialog", (dialog) => dialog.dismiss())
  await page.getByRole("button", { name: "提示词", exact: true }).click()
  await expect(page.getByLabel("昵称")).toHaveValue("New Fixture Name")

  page.once("dialog", (dialog) => dialog.accept())
  await page.getByRole("button", { name: "提示词", exact: true }).click()
  await expect(page.getByRole("dialog", { name: "提示词管理" })).toBeVisible()
})

test("an older profile save cannot close or overwrite a newer revision", async ({ page }) => {
  const email = page.getByLabel("邮箱")
  const nickname = page.getByLabel("昵称")
  await email.fill("saved-profile@example.invalid")
  await nickname.fill("Saved Fixture Name")
  await page.getByRole("button", { name: "保存", exact: true }).click()

  await email.fill("newer-profile@example.invalid")
  await nickname.fill("Newer Fixture Name")
  await expect(page.getByText("已保存较早版本，当前修改仍未保存")).toBeVisible()
  await page.getByRole("button", { name: "保存", exact: true }).click()
  await expect(page.getByText("已保存", { exact: true })).toBeVisible()
  await nickname.fill("Revision after success")
  await page.waitForTimeout(900)

  await expect(page.getByRole("dialog", { name: "设置" })).toBeVisible()
  await expect(email).toHaveValue("newer-profile@example.invalid")
  await expect(nickname).toHaveValue("Revision after success")
})

test("an older password save cannot clear or close a newer revision", async ({ page }) => {
  await page.getByRole("button", { name: "密码", exact: true }).click()
  const currentPassword = page.getByLabel("当前密码")
  const newPassword = page.getByLabel("新密码")
  const confirmation = page.getByLabel("确认密码")
  await currentPassword.fill("old-secret")
  await newPassword.fill("first-secret")
  await confirmation.fill("first-secret")
  await page.getByRole("button", { name: "更新密码", exact: true }).click()

  await currentPassword.fill("newer-old-secret")
  await newPassword.fill("newer-secret")
  await confirmation.fill("newer-secret")
  await expect(page.getByText("已提交较早的新密码，当前输入仍未保存")).toBeVisible()
  await page.getByRole("button", { name: "更新密码", exact: true }).click()
  await expect(page.getByText("密码已更新", { exact: true })).toBeVisible()
  await currentPassword.fill("after-success-old-secret")
  await newPassword.fill("after-success-secret")
  await confirmation.fill("after-success-secret")
  await page.waitForTimeout(900)

  await expect(page.getByRole("dialog", { name: "设置" })).toBeVisible()
  await expect(currentPassword).toHaveValue("after-success-old-secret")
  await expect(newPassword).toHaveValue("after-success-secret")
  await expect(confirmation).toHaveValue("after-success-secret")
})
