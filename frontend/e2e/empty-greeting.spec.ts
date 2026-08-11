import { expect, test, type Page } from "@playwright/test"

const existingSession = {
  id: 1,
  user_id: 1,
  title: "Reference conversation",
  title_generated: false,
  model_id: "demo-model",
  provider: "demo",
  created_at: "2026-08-11T00:00:00Z",
  updated_at: "2026-08-11T00:00:00Z",
}

async function mockAccount(
  page: Page,
  options: { withSession?: boolean; readiness?: "ready" | "pending" | "error" | "blocked" } = {},
) {
  const { withSession = false, readiness = "ready" } = options
  await page.addInitScript(() => {
    localStorage.setItem("token", "test-token")
    localStorage.setItem("sidebar_open", "false")
    Math.random = () => 0
  })
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname
    if (path === "/api/v1/users/me") return route.fulfill({ json: { id: 1, username: "member", role: "user", is_active: true } })
    if (path === "/api/v1/system/info") return route.fulfill({ json: { system_name: "effchat" } })
    if (path === "/api/v1/models") {
      return route.fulfill({ json: { models: [{ id: "demo-model", provider: "demo", display_name: "Demo", enabled: true, sort_order: 1 }], total: 1 } })
    }
    if (path === "/api/v1/sessions/readiness") {
      if (readiness === "pending") await new Promise((resolve) => setTimeout(resolve, 500))
      if (readiness === "error") return route.fulfill({ status: 503, json: { error: { code: "readiness_unavailable", message: "Temporary test failure" } } })
      if (readiness === "blocked") return route.fulfill({ json: { ready: false, retryable: true, code: "default_model_not_configured" } })
      return route.fulfill({ json: { ready: true, retryable: false } })
    }
    if (path === "/api/v1/sessions") return route.fulfill({ json: { sessions: withSession ? [existingSession] : [], has_more: false, next_offset: 0 } })
    if (path === "/api/v1/session-folders") return route.fulfill({ json: { folders: [] } })
    if (path === "/api/v1/sessions/1") return route.fulfill({ json: existingSession })
    return route.fulfill({ json: {} })
  })
}

test("empty accounts receive the shared quote greeting on desktop and mobile", async ({ page }) => {
  await mockAccount(page)
  await page.goto("/")

  const greeting = page.locator(".empty-greeting")
  await expect(greeting).toBeVisible()
  await expect(page.getByText("有什么我可以帮你的？", { exact: true })).toHaveCount(0)
  await expect(page.getByRole("blockquote")).toHaveAttribute("aria-label", "行到水穷处，坐看云起时。")
  await expect(page.getByText("王维《终南别业》", { exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "新建对话", exact: true })).toBeVisible()

  for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
    await page.setViewportSize(viewport)
    const box = await greeting.boundingBox()
    expect(box).not.toBeNull()
    expect(box!.x).toBeGreaterThanOrEqual(0)
    expect(box!.y).toBeGreaterThanOrEqual(0)
    expect(box!.x + box!.width).toBeLessThanOrEqual(viewport.width)
    expect(box!.y + box!.height).toBeLessThanOrEqual(viewport.height)
  }
})

test("desktop empty state restores a persistently collapsed sidebar", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockAccount(page)
  await page.goto("/")

  const opener = page.locator('button[aria-controls="app-sidebar"]')
  const sidebar = page.getByRole("complementary", { name: "侧边栏", includeHidden: true })
  await expect(opener).toHaveAccessibleName("打开侧边栏")
  await expect(opener).toHaveAttribute("aria-expanded", "false")
  await expect(sidebar).toHaveAttribute("aria-hidden", "true")
  await expect(sidebar).toHaveAttribute("inert", "")

  await page.keyboard.press("Tab")
  await expect(opener).toBeFocused()
  await opener.click()
  await expect(opener).toHaveAccessibleName("收起侧边栏")
  await expect(opener).toHaveAttribute("aria-expanded", "true")
  await expect(sidebar).toHaveAttribute("aria-hidden", "false")
  await expect(sidebar).not.toHaveAttribute("inert", "")

  await page.getByRole("button", { name: "member" }).click()
  await expect(page.getByRole("button", { name: "退出登录" })).toBeVisible()
})

for (const width of [390, 430]) {
  test(`${width}px root route can reopen navigation and enter an existing session`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 })
    await mockAccount(page, { withSession: true })
    await page.goto("/")

    const opener = page.locator('button[aria-controls="app-sidebar"]')
    const sidebar = page.getByRole("complementary", { name: "侧边栏", includeHidden: true })
    await expect(opener).toHaveAccessibleName("打开侧边栏")
    await expect(opener).toHaveAttribute("aria-expanded", "false")
    await expect(sidebar).toHaveAttribute("aria-hidden", "true")
    await expect(sidebar).toHaveAttribute("inert", "")

    await opener.click()
    await expect(opener).toHaveAccessibleName("收起侧边栏")
    await expect(opener).toHaveAttribute("aria-expanded", "true")
    await expect(sidebar).toHaveAttribute("aria-hidden", "false")
    await expect(sidebar).not.toHaveAttribute("inert", "")

    await page.getByRole("button", { name: "Reference conversation", exact: true }).click()
    await expect(page).toHaveURL(/\/chat\/1$/)
    await expect(sidebar).toHaveAttribute("aria-hidden", "true")
    await expect(sidebar).toHaveAttribute("inert", "")
  })
}

for (const readiness of ["pending", "error", "blocked"] as const) {
  test(`sidebar opener survives ${readiness} session readiness state`, async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await mockAccount(page, { readiness })
    await page.goto("/")

    const opener = page.locator('button[aria-controls="app-sidebar"]')
    await expect(opener).toHaveAccessibleName("打开侧边栏")
    await opener.click()
    await expect(page.getByRole("complementary", { name: "侧边栏" })).toHaveAttribute("aria-hidden", "false")
  })
}
