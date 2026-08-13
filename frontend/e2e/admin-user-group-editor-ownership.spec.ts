import { expect, test, type Page, type Route } from "@playwright/test"

const timestamp = "2026-08-05T00:00:00Z"

const groupA = group(1, "Group A", 10)
const groupB = group(2, "Group B", 20)

const userA = user(2, "alpha", "Alpha User")
const userB = user(3, "beta", "Beta User")

function group(id: number, name: string, level: number) {
  return {
    id,
    name,
    level,
    description: `${name} description`,
    is_default: id === 1,
    daily_message_limit: 0,
    daily_token_limit: 0,
    concurrent_run_limit: 0,
    daily_tool_call_limit: 0,
    daily_web_search_limit: 0,
    daily_web_extract_limit: 0,
    daily_ocr_file_limit: 0,
    daily_ocr_page_limit: 0,
    created_at: timestamp,
    updated_at: timestamp,
  }
}

function user(id: number, username: string, nickname: string) {
  return {
    id,
    username,
    nickname,
    email: `${username}@example.invalid`,
    role: "user",
    group_id: null,
    effective_group: { id: groupA.id, name: groupA.name, level: groupA.level, inherited: true },
    is_active: true,
    created_at: timestamp,
    last_login_at: timestamp,
  }
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
    if (path === "/api/v1/users/me") return fulfillJSON(route, { id: 1, username: "admin", role: "admin", is_active: true })
    if (path === "/api/v1/system/info") return fulfillJSON(route, { system_name: "EffChat" })
    if (path === "/api/v1/admin/users" && request.method() === "GET") {
      return fulfillJSON(route, { users: [userA, userB], total: 2 })
    }
    if (path === "/api/v1/admin/groups" && request.method() === "GET") {
      return fulfillJSON(route, { groups: [groupA, groupB], total: 2 })
    }
    if (path === `/api/v1/admin/users/${userA.id}` && request.method() === "PATCH") {
      const payload = request.postDataJSON()
      return fulfillJSON(route, { ...userA, ...payload }, payload.role ? 250 : 300)
    }
    if (path === `/api/v1/admin/users/${userB.id}` && request.method() === "PATCH") {
      return fulfillJSON(route, { ...userB, ...request.postDataJSON() }, 700)
    }
    if (path === `/api/v1/admin/users/${userA.id}/password` && request.method() === "PUT") {
      return fulfillJSON(route, { message: "updated" }, 300)
    }
    if (path === `/api/v1/admin/groups/${groupA.id}` && request.method() === "PATCH") {
      return fulfillJSON(route, { ...groupA, ...request.postDataJSON() }, 300)
    }
    if (path === `/api/v1/admin/groups/${groupB.id}` && request.method() === "DELETE") {
      return fulfillJSON(route, { message: "deleted" }, 300)
    }
    return fulfillJSON(route, {})
  })
}

test("user saves preserve newer input and cannot close another user editor", async ({ page }) => {
  await installRoutes(page)
  await page.goto("/admin/users")

  await page.getByText("Alpha User", { exact: true }).click()
  await page.getByLabel("昵称").fill("Alpha saved revision")
  await page.getByRole("button", { name: "保存", exact: true }).click()
  await page.getByLabel("昵称").fill("Alpha newer revision")

  await expect(page.getByText("已保存较早版本，当前修改仍未保存")).toBeVisible()
  await expect(page.getByLabel("昵称")).toHaveValue("Alpha newer revision")

  await page.getByRole("button", { name: "保存", exact: true }).click()
  page.once("dialog", (dialog) => dialog.accept())
  await page.getByText("Beta User", { exact: true }).click()
  await expect(page.getByLabel("昵称")).toHaveValue("Beta User")
  await page.waitForTimeout(350)
  await expect(page.getByLabel("昵称")).toHaveValue("Beta User")
})

test("a late user quick patch cannot release a newer quick patch", async ({ page }) => {
  await installRoutes(page)
  await page.goto("/admin/users")

  const roleSelectors = page.locator("select")
  const alphaRole = roleSelectors.nth(0)
  const betaRole = roleSelectors.nth(1)
  await alphaRole.selectOption("admin")
  await betaRole.selectOption("admin")
  await expect(betaRole).toBeDisabled()

  await page.waitForTimeout(350)
  await expect(betaRole).toBeDisabled()
  await expect(alphaRole).toHaveValue("admin")
  await expect(betaRole).toBeEnabled()
  await expect(betaRole).toHaveValue("admin")
})

test("a user quick patch cannot overwrite a newer detail draft", async ({ page }) => {
  await installRoutes(page)
  await page.goto("/admin/users")

  await page.getByText("Alpha User", { exact: true }).click()
  const listRole = page.locator("select").nth(0)
  const draftRole = page.getByLabel("角色")
  await listRole.selectOption("admin")
  await draftRole.selectOption("admin")
  await draftRole.selectOption("user")

  await page.waitForTimeout(300)
  await expect(listRole).toHaveValue("admin")
  await expect(draftRole).toHaveValue("user")
})

test("a completed password reset preserves newer password input", async ({ page }) => {
  await installRoutes(page)
  await page.goto("/admin/users")

  await page.getByText("Alpha User", { exact: true }).click()
  await page.getByRole("button", { name: "设置新密码" }).click()
  const password = page.getByLabel("新密码")
  await password.fill("fixture-password-1")
  await page.getByRole("button", { name: "确认重置" }).click()
  await password.fill("fixture-password-2")

  await expect(page.getByText("已提交较早的新密码，当前输入仍未保存")).toBeVisible()
  await expect(password).toHaveValue("fixture-password-2")
})

test("group saves and deletes stay with the generation that started them", async ({ page }) => {
  await installRoutes(page)
  await page.goto("/admin/groups")

  await page.getByText("Group A", { exact: true }).click()
  await page.getByLabel("名称").fill("Group A saved revision")
  await page.getByRole("button", { name: "保存", exact: true }).click()
  await page.getByLabel("名称").fill("Group A newer revision")
  await expect(page.getByText("已保存较早版本，当前修改仍未保存")).toBeVisible()
  await expect(page.getByLabel("名称")).toHaveValue("Group A newer revision")

  await page.getByRole("button", { name: "保存", exact: true }).click()
  page.once("dialog", (dialog) => dialog.accept())
  await page.getByText("Group B", { exact: true }).click()
  await expect(page.getByLabel("名称")).toHaveValue("Group B")
  await page.waitForTimeout(350)
  await expect(page.getByLabel("名称")).toHaveValue("Group B")

  page.once("dialog", (dialog) => dialog.accept())
  await page.getByRole("button", { name: "删除", exact: true }).click()
  await page.getByText("Group A newer revision", { exact: true }).click()
  await expect(page.getByLabel("名称")).toHaveValue("Group A newer revision")
  await page.waitForTimeout(350)
  await expect(page.getByLabel("名称")).toHaveValue("Group A newer revision")
})
