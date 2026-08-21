import { expect, test, type Page, type Route } from "@playwright/test"

const currentUser = {
  id: 1,
  username: "fixture-admin",
  nickname: "Fixture Admin",
  email: "admin@example.invalid",
  role: "admin",
  is_active: true,
}

const prompt = {
  id: 11,
  user_id: 1,
  title: "Fixture Prompt",
  content: "Fixture content",
  description: "",
  group_id: 7,
  group_name: "工作",
  is_public: false,
  created_at: "2026-08-19T00:00:00Z",
  updated_at: "2026-08-19T00:00:00Z",
}

async function fulfillJSON(route: Route, json: unknown) {
  await route.fulfill({ json })
}

async function installRoutes(page: Page) {
  await page.addInitScript(() => localStorage.setItem("token", "fixture-token"))
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (path === "/api/v1/users/me") return fulfillJSON(route, currentUser)
    if (path === "/api/v1/system/info") return fulfillJSON(route, { system_name: "EffChat" })
    if (path === "/api/v1/models") return fulfillJSON(route, { models: [], total: 0 })
    if (path === "/api/v1/sessions") return fulfillJSON(route, { sessions: [], has_more: false, next_offset: 0 })
    if (path === "/api/v1/session-folders") return fulfillJSON(route, { folders: [] })
    if (path === "/api/v1/prompts" && request.method() === "GET") return fulfillJSON(route, { prompts: [prompt], total: 1, has_more: false, next_offset: 1 })
    if (path === "/api/v1/prompts/public") return fulfillJSON(route, { prompts: [], total: 0, has_more: false, next_offset: 0 })
    if (path === "/api/v1/prompt-groups" && request.method() === "GET") return fulfillJSON(route, { groups: [{ id: 7, user_id: 1, name: "工作" }] })
    if (path === "/api/v1/prompt-groups" && request.method() === "POST") {
      return fulfillJSON(route, { id: 8, user_id: 1, name: request.postDataJSON().name })
    }
    if (path === "/api/v1/prompt-groups/7" && request.method() === "DELETE") return fulfillJSON(route, { message: "deleted" })
    if (path === "/api/v1/prompts/11" && request.method() === "DELETE") return fulfillJSON(route, { message: "deleted" })
    return fulfillJSON(route, {})
  })
}

async function openPromptManager(page: Page) {
  await page.getByRole("button", { name: /Fixture Admin/ }).click()
  await page.getByRole("button", { name: "设置", exact: true }).click()
  await page.getByRole("button", { name: "提示词", exact: true }).click()
  await expect(page.getByRole("dialog", { name: "提示词管理" })).toBeVisible()
}

test("prompt manager uses product dialogs for names, dirty changes, and destructive actions", async ({ page }) => {
  await installRoutes(page)
  await page.goto("/")
  await openPromptManager(page)

  const manager = page.getByRole("dialog", { name: "提示词管理" })
  await manager.getByRole("button", { name: "Fixture Prompt", exact: true }).click()
  const title = manager.getByLabel("名称")
  await title.fill("Dirty title")
  await manager.getByRole("button", { name: "新建", exact: true }).click()

  const leaveDialog = page.getByRole("dialog", { name: "放弃未保存的修改？" })
  await expect(leaveDialog).toBeVisible()
  await leaveDialog.getByRole("button", { name: "取消" }).click()
  await expect(title).toHaveValue("Dirty title")
  await manager.getByRole("button", { name: "新建", exact: true }).click()
  await leaveDialog.getByRole("button", { name: "放弃修改" }).click()
  await expect(manager.getByText("新建提示词", { exact: true })).toBeVisible()

  const createGroup = manager.getByTitle("新建分组")
  await createGroup.click()
  const groupDialog = page.getByRole("dialog", { name: "新建分组" })
  const groupName = groupDialog.getByLabel("分组名称")
  await expect(groupName).toBeFocused()
  await groupName.fill("研究")
  await groupName.press("Enter")
  await expect(groupDialog).toHaveCount(0)
  await expect(manager.getByText("分组已创建", { exact: true })).toBeVisible()

  await manager.getByRole("button", { name: "Fixture Prompt", exact: true }).click()
  await leaveDialog.getByRole("button", { name: "放弃修改" }).click()
  const deletePrompt = manager.getByRole("button", { name: "删除", exact: true })
  await deletePrompt.click()
  const promptDeleteDialog = page.getByRole("dialog", { name: /删除提示词/ })
  await expect(promptDeleteDialog).toBeVisible()
  await promptDeleteDialog.getByRole("button", { name: "取消" }).click()
  await expect(deletePrompt).toBeFocused()

  const deleteRequest = page.waitForRequest((request) => request.method() === "DELETE" && new URL(request.url()).pathname === "/api/v1/prompts/11")
  await deletePrompt.click()
  await promptDeleteDialog.getByRole("button", { name: "删除提示词" }).click()
  await deleteRequest
  await expect(manager.getByText("已删除", { exact: true })).toBeVisible()
})
