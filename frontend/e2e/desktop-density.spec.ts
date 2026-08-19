import { expect, test, type Page } from "@playwright/test"

const session = {
  id: 1,
  user_id: 1,
  title: "桌面密度回归",
  title_generated: false,
  model_id: "density-model",
  provider: "fixture",
  created_at: "2026-08-16T00:00:00Z",
  updated_at: "2026-08-16T00:00:00Z",
}

const messages = [
  {
    id: 1,
    session_id: 1,
    schema_version: "v1",
    role: "user",
    has_tool_calls: false,
    has_reasoning: false,
    created_at: "2026-08-16T00:01:00Z",
    message_data: { role: "user", content: "检查桌面密度" },
  },
  {
    id: 2,
    session_id: 1,
    schema_version: "v1",
    role: "assistant",
    has_tool_calls: true,
    has_reasoning: true,
    created_at: "2026-08-16T00:01:01Z",
    message_data: {
      role: "assistant",
      content: "桌面密度回归内容。",
      runtime: { duration_ms: 1200, tokens_per_second: 12.3 },
      segments: [
        { thinking: "读取页面结构并检查共享 token。" },
        {
          tool_calls: [{
            id: "density-tool",
            name: "web_search",
            status: "done",
            arguments: JSON.stringify({ query: "desktop density" }),
            result: JSON.stringify({ source: "fixture", citations: [] }),
          }],
        },
        { content: "桌面密度回归内容。" },
      ],
    },
  },
]

async function installRoutes(page: Page) {
  await page.addInitScript(() => {
    localStorage.setItem("token", "density-fixture-token")
    localStorage.setItem("sidebar_open", "true")
  })
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (path === "/api/v1/users/me") {
      return route.fulfill({ json: { id: 1, username: "fixture-admin", nickname: "Fixture Admin", email: "admin@example.invalid", role: "admin", is_active: true } })
    }
    if (path === "/api/v1/system/info") return route.fulfill({ json: { system_name: "EffChat", version: "fixture" } })
    if (path === "/api/v1/models") return route.fulfill({ json: { models: [{ id: "density-model", provider: "fixture", display_name: "Density Model", enabled: true, sort_order: 1 }], total: 1 } })
    if (path === "/api/v1/sessions") return route.fulfill({ json: { sessions: [session], has_more: false, next_offset: 0 } })
    if (path === "/api/v1/session-folders") return route.fulfill({ json: { folders: [] } })
    if (path === "/api/v1/sessions/1") return route.fulfill({ json: session })
    if (path === "/api/v1/sessions/1/messages" || path === "/api/v1/sessions/1/message-window") {
      return route.fulfill({ json: { messages, has_more: false, has_older: false, has_newer: false, first_turn_id: 1, last_turn_id: 1 } })
    }
    if (path === "/api/v1/sessions/1/turns") return route.fulfill({ json: { turns: [], total: 0, has_more: false, next_before_turn_id: null } })
    if (path === "/api/v1/files/upload-limits") return route.fulfill({ json: { max_file_size_mb: 20, max_session_files: 50, allowed_types: [] } })
    if (path === "/api/v1/files") return route.fulfill({ json: { files: [], has_more: false, next_offset: 0 } })
    if (path === "/api/v1/admin/channels") return route.fulfill({ json: { channels: [], total: 0 } })
    if (path === "/api/v1/admin/groups") return route.fulfill({ json: { groups: [], total: 0 } })
    if (path === "/api/v1/admin/users") return route.fulfill({ json: { users: [{ id: 1, username: "fixture-admin", nickname: "Fixture Admin", role: "admin", is_active: true }], total: 1 } })
    return route.fulfill({ json: {} })
  })
}

async function waitForFonts(page: Page) {
  await page.evaluate(() => document.fonts?.ready)
  await page.waitForLoadState("networkidle")
}

async function computed(page: Page, selector: string, property: string) {
  return page.locator(selector).first().evaluate((element, name) => getComputedStyle(element).getPropertyValue(name).trim(), property)
}

async function assertNoSmallChromeText(page: Page, selectors: string[]) {
  const offenders = await page.evaluate((rootSelectors) => {
    const result: string[] = []
    for (const selector of rootSelectors) {
      for (const root of document.querySelectorAll<HTMLElement>(selector)) {
        const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT)
        let node: Node | null
        while ((node = walker.nextNode())) {
          const text = node.textContent?.replace(/\s+/g, " ").trim()
          const element = node.parentElement
          if (!text || !element || element.closest(".markdown-body, [aria-hidden='true'], .sr-only")) continue
          const style = getComputedStyle(element)
          if (style.display === "none" || style.visibility === "hidden" || Number(style.opacity) === 0) continue
          if (Number.parseFloat(style.fontSize) < 12) result.push(`${text.slice(0, 40)} (${style.fontSize})`)
        }
      }
    }
    return result
  }, selectors)
  expect(offenders, `visible chrome text below 12px: ${offenders.join(", ")}`).toEqual([])
}

async function assertChatDensity(page: Page, expected: { spacing: string; sidebar: number; composer: number }) {
  await expect(page.locator('[data-testid="message-item"][data-role="assistant"]')).toBeVisible()
  await waitForFonts(page)

  expect(await page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue("--spacing").trim())).toBe(expected.spacing)
  expect(await page.locator('[aria-label="侧边栏"]').boundingBox()).not.toBeNull()
  expect(Math.round((await page.locator('[aria-label="侧边栏"]').boundingBox())?.width || 0)).toBe(expected.sidebar)
  expect(Math.round((await page.getByTestId("chat-input").boundingBox())?.height || 0)).toBe(expected.composer)
  expect(await page.getByText("桌面密度回归内容。", { exact: true }).evaluate((element) => getComputedStyle(element).fontSize)).toBe("15px")
  await assertNoSmallChromeText(page, ["aside", '[data-testid="composer-toolbar"]'])

  const reasoning = page.getByRole("button", { name: /思考过程与工具调用/ })
  await expect(reasoning).toBeVisible()
  expect(await reasoning.locator(".text-xs").first().evaluate((element) => getComputedStyle(element).fontSize)).toBe("12px")
  await reasoning.click()
  await expect(page.getByRole("button", { name: /联网搜索/ })).toBeVisible()

  const usageToggle = page.getByRole("button", { name: /12\.3\/s/ })
  await expect(usageToggle).toBeVisible()
  await usageToggle.click()
  await expect(page.getByText("12.3 token/秒", { exact: true })).toBeVisible()
  await expect(page.getByText("1.2秒", { exact: true })).toBeVisible()
  await page.getByText("桌面密度回归内容。", { exact: true }).click()
  await expect(page.getByText("12.3 token/秒", { exact: true })).toHaveCount(0)
  await usageToggle.click()
  await expect(page.getByText("12.3 token/秒", { exact: true })).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(page.getByText("12.3 token/秒", { exact: true })).toHaveCount(0)
  await expect(usageToggle).toBeFocused()

  await page.getByRole("button", { name: /Fixture Admin/ }).click()
  const slider = page.getByRole("slider", { name: "对话字号" })
  await slider.fill("8")
  await expect.poll(() => page.locator(".markdown-body").last().evaluate((element) => getComputedStyle(element).fontSize)).toBe("18.75px")
  expect(await computed(page, '[aria-label="侧边栏"]', "font-size")).toBe("16px")

  await page.getByRole("button", { name: "设置", exact: true }).click()
  const dialog = page.getByRole("dialog", { name: "设置" })
  await expect(dialog).toBeVisible()
  await assertNoSmallChromeText(page, ["[role='dialog'][data-state='open']"])
  return dialog
}

test("standard and compact desktop density keep chat chrome consistent", async ({ browser }) => {
  const cases = [
    { name: "standard", viewport: { width: 1920, height: 1080 }, deviceScaleFactor: 1, spacing: "0.25rem", sidebar: 280, composer: 54 },
    { name: "125-percent compact", viewport: { width: 1536, height: 864 }, deviceScaleFactor: 1.25, spacing: "0.2375rem", sidebar: 246, composer: 50 },
    { name: "low-height compact", viewport: { width: 1366, height: 768 }, deviceScaleFactor: 1, spacing: "0.2375rem", sidebar: 240, composer: 50 },
  ]

  for (const density of cases) {
    const context = await browser.newContext({ viewport: density.viewport, deviceScaleFactor: density.deviceScaleFactor })
    const page = await context.newPage()
    const errors: string[] = []
    page.on("pageerror", (error) => errors.push(error.message))
    page.on("console", (message) => { if (message.type() === "error") errors.push(message.text()) })
    await installRoutes(page)
    await page.goto("/chat/1")
    const dialog = await assertChatDensity(page, density)
    const expectedDialog = density.name === "standard" ? 600 : 560
    await expect.poll(async () => Math.round((await dialog.boundingBox())?.width || 0)).toBe(expectedDialog)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    expect(errors).toEqual([])
    await context.close()
  }
})

test("protected pages expose a keyboard skip link to the main content", async ({ page }) => {
  await installRoutes(page)
  await page.goto("/chat/1")
  await expect(page.locator('[data-testid="message-item"][data-role="assistant"]')).toBeVisible()
  await waitForFonts(page)

  const skipLink = page.getByRole("link", { name: "跳到主要内容" })
  await page.keyboard.press("Tab")
  await expect(skipLink).toBeFocused()
  await skipLink.press("Enter")
  await expect(page.locator("main#main-content")).toBeFocused()
})

test("admin density stays readable and mobile touch targets do not shrink", async ({ browser }) => {
  const desktop = await browser.newContext({ viewport: { width: 1536, height: 864 }, deviceScaleFactor: 1.25 })
  const desktopPage = await desktop.newPage()
  await installRoutes(desktopPage)
  await desktopPage.goto("/admin/models")
  await waitForFonts(desktopPage)
  const navigation = desktopPage.getByRole("navigation", { name: "管理后台导航" }).first()
  expect(Math.round((await navigation.boundingBox())?.width || 0)).toBe(200)
  await assertNoSmallChromeText(desktopPage, ["header", "nav", "main"])
  await desktopPage.getByRole("button", { name: "用户", exact: true }).click()
  await expect(desktopPage.getByRole("heading", { name: "用户" })).toBeVisible()
  expect(await desktopPage.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await desktop.close()

  const mobile = await browser.newContext({ viewport: { width: 390, height: 844 } })
  const mobilePage = await mobile.newPage()
  await installRoutes(mobilePage)
  await mobilePage.goto("/admin/models")
  await waitForFonts(mobilePage)
  expect(await mobilePage.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue("--spacing").trim())).toBe("0.25rem")
  for (const label of ["返回聊天", "刷新当前页面", "打开管理导航"]) {
    const box = await mobilePage.getByRole("button", { name: label }).boundingBox()
    expect(box?.width || 0).toBeGreaterThanOrEqual(44)
    expect(box?.height || 0).toBeGreaterThanOrEqual(44)
  }
  expect(await mobilePage.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await mobile.close()
})
