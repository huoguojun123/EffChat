import { expect, test, type Page } from "@playwright/test"

const session = {
  id: 1,
  user_id: 1,
  title: "移动提示词回归",
  title_generated: false,
  model_id: "fixture-model",
  provider: "fixture",
  created_at: "2026-08-21T00:00:00Z",
  updated_at: "2026-08-21T00:00:00Z",
}

const prompt = {
  id: 11,
  user_id: 1,
  title: "代码审查助手",
  content: Array.from({ length: 24 }, (_, index) => `审查规则 ${index + 1}：检查真实的数据流和失败恢复。`).join("\n"),
  description: "",
  tags: [],
  group_id: 7,
  group_name: "研发",
  is_public: false,
  created_at: "2026-08-21T00:00:00Z",
  updated_at: "2026-08-21T00:00:00Z",
}

async function installRoutes(page: Page) {
  await page.addInitScript(() => localStorage.setItem("token", "prompt-picker-fixture-token"))
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (path === "/api/v1/users/me") return route.fulfill({ json: { id: 1, username: "fixture-user", nickname: "Fixture User", role: "user", is_active: true } })
    if (path === "/api/v1/system/info") return route.fulfill({ json: { system_name: "EffChat", version: "fixture" } })
    if (path === "/api/v1/models") return route.fulfill({ json: { models: [{ id: "fixture-model", provider: "fixture", display_name: "Fixture Model", enabled: true }], total: 1 } })
    if (path === "/api/v1/sessions") return route.fulfill({ json: { sessions: [session], has_more: false, next_offset: 0 } })
    if (path === "/api/v1/session-folders") return route.fulfill({ json: { folders: [] } })
    if (path === "/api/v1/sessions/1" && request.method() === "PATCH") {
      return route.fulfill({ json: { ...session, system_prompt: request.postDataJSON().system_prompt } })
    }
    if (path === "/api/v1/sessions/1") return route.fulfill({ json: session })
    if (path === "/api/v1/sessions/1/messages" || path === "/api/v1/sessions/1/message-window") {
      return route.fulfill({ json: { messages: [], has_more: false, has_older: false, has_newer: false } })
    }
    if (path === "/api/v1/sessions/1/turns") return route.fulfill({ json: { turns: [], total: 0, has_more: false, next_before_turn_id: null } })
    if (path === "/api/v1/prompts") return route.fulfill({ json: { prompts: [prompt], total: 1, has_more: false, next_offset: 1 } })
    if (path === "/api/v1/prompts/public") return route.fulfill({ json: { prompts: [], total: 0, has_more: false, next_offset: 0 } })
    if (path === "/api/v1/files/upload-limits") return route.fulfill({ json: { max_file_size_mb: 20, max_session_files: 50, allowed_types: [] } })
    if (path === "/api/v1/files") return route.fulfill({ json: { files: [], has_more: false, next_offset: 0 } })
    return route.fulfill({ json: {} })
  })
}

async function openPicker(page: Page) {
  await page.getByTestId("composer-toolbar").getByRole("button", { name: "更多", exact: true }).click()
  await page.getByRole("button", { name: "系统提示词" }).click()
  const dialog = page.getByRole("dialog", { name: "选择系统提示词" })
  await expect(dialog).toBeVisible()
  await expect(dialog.getByRole("button", { name: "代码审查助手" })).toBeVisible()
  return dialog
}

test("mobile prompt picker uses a reachable list and preview flow", async ({ browser }) => {
  const context = await browser.newContext({ viewport: { width: 390, height: 844 } })
  const page = await context.newPage()
  await installRoutes(page)
  await page.goto("/chat/1")

  const dialog = await openPicker(page)
  await expect(dialog.locator("pre")).toBeHidden()
  const search = dialog.getByLabel("搜索提示词")
  await expect.poll(() => search.evaluate((element) => Number.parseFloat(getComputedStyle(element).height))).toBeGreaterThanOrEqual(44)
  await search.fill("不存在的提示词")
  await expect(dialog.getByRole("button", { name: "代码审查助手" })).toBeHidden()
  await search.clear()
  await dialog.getByRole("button", { name: "代码审查助手" }).click()
  await expect(dialog.locator("pre")).toContainText("审查规则 1：检查真实的数据流和失败恢复。")

  const back = dialog.getByRole("button", { name: "返回提示词列表" })
  const backBox = await back.boundingBox()
  expect(Math.round(backBox?.width || 0)).toBeGreaterThanOrEqual(44)
  expect(Math.round(backBox?.height || 0)).toBeGreaterThanOrEqual(44)
  await back.click()
  await expect(dialog.getByLabel("搜索提示词")).toBeVisible()

  await dialog.getByRole("button", { name: "代码审查助手" }).click()
  const applyRequest = page.waitForRequest((request) => request.method() === "PATCH" && new URL(request.url()).pathname === "/api/v1/sessions/1")
  await dialog.getByRole("button", { name: "应用", exact: true }).click()
  expect((await applyRequest).postDataJSON()).toEqual({ system_prompt: prompt.content })
  await expect(dialog).toHaveCount(0)

  const reopened = await openPicker(page)
  await expect(reopened.getByLabel("搜索提示词")).toBeVisible()
  const clearRequest = page.waitForRequest((request) => request.method() === "PATCH" && new URL(request.url()).pathname === "/api/v1/sessions/1")
  await reopened.getByRole("button", { name: "清空", exact: true }).click()
  expect((await clearRequest).postDataJSON()).toEqual({ system_prompt: "" })

  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await context.close()
})

test("low-height mobile keeps the picker and actions inside the viewport", async ({ browser }) => {
  const context = await browser.newContext({ viewport: { width: 390, height: 667 } })
  const page = await context.newPage()
  await installRoutes(page)
  await page.goto("/chat/1")

  const dialog = await openPicker(page)
  await dialog.getByRole("button", { name: "代码审查助手" }).click()
  const box = await dialog.boundingBox()
  expect(box).not.toBeNull()
  expect((box?.y || 0) + (box?.height || 0)).toBeLessThanOrEqual(667)
  await expect(dialog.getByRole("button", { name: "清空", exact: true })).toBeInViewport()
  await expect(dialog.getByRole("button", { name: "应用", exact: true })).toBeInViewport()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await context.close()
})

test("desktop prompt picker keeps the two-pane layout", async ({ page }) => {
  await page.setViewportSize({ width: 1536, height: 864 })
  await installRoutes(page)
  await page.goto("/chat/1")

  const dialog = await openPicker(page)
  await expect(dialog.getByLabel("搜索提示词")).toBeVisible()
  await expect(dialog.locator("pre")).toContainText("审查规则 1：检查真实的数据流和失败恢复。")
  await expect(dialog.getByRole("button", { name: "返回提示词列表" })).toBeHidden()
})
