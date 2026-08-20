import { test, expect } from "@playwright/test"

test("admin route loads only the model page dependencies and opens mobile navigation", async ({ page }) => {
  const requested: string[] = []
  await page.addInitScript(() => localStorage.setItem("token", "test-token"))
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname
    requested.push(path)
    if (path === "/api/v1/users/me") return route.fulfill({ json: { id: 1, username: "admin", role: "admin", is_active: true } })
    if (path === "/api/v1/system/info") return route.fulfill({ json: { system_name: "EffChat" } })
    if (path === "/api/v1/models") return route.fulfill({ json: { models: [], total: 0 } })
    if (path === "/api/v1/admin/channels") return route.fulfill({ json: { channels: [], total: 0 } })
    if (path === "/api/v1/admin/groups") return route.fulfill({ json: { groups: [], total: 0 } })
    return route.fulfill({ json: {} })
  })

  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/admin")

  await expect(page).toHaveURL(/\/admin\/models$/)
  await expect(page.getByRole("heading", { name: "模型" })).toBeVisible()
  await expect.poll(() => requested).toEqual(expect.arrayContaining([
    "/api/v1/users/me",
    "/api/v1/models",
    "/api/v1/admin/channels",
    "/api/v1/admin/groups",
  ]))
  expect(requested).not.toContain("/api/v1/admin/users?limit=100&offset=0")
  expect(requested).not.toContain("/api/v1/admin/config")

  for (const name of ["返回聊天", "刷新当前页面", "打开管理导航"]) {
    const box = await page.getByRole("button", { name }).boundingBox()
    expect(box).not.toBeNull()
    expect(box!.width).toBeGreaterThanOrEqual(44)
    expect(box!.height).toBeGreaterThanOrEqual(44)
  }

  await page.getByRole("button", { name: "打开管理导航" }).click()
  const mobileNavigation = page.getByRole("navigation", { name: "管理后台导航" }).last()
  await expect(mobileNavigation).toBeVisible()
  await expect(page.getByRole("button", { name: "渠道与联网服务" }).last()).toBeVisible()
  const mobileNavigationBox = await mobileNavigation.boundingBox()
  expect(mobileNavigationBox).not.toBeNull()
  expect((mobileNavigationBox?.y || 0) + (mobileNavigationBox?.height || 0)).toBeGreaterThan(820)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  const close = page.getByRole("button", { name: "关闭管理导航" })
  await expect(close).toHaveCount(1)
  await expect.poll(async () => (await close.boundingBox())?.width || 0).toBeGreaterThanOrEqual(43.5)
  await expect.poll(async () => (await close.boundingBox())?.height || 0).toBeGreaterThanOrEqual(43.5)
})

test("non-admin users are returned to chat before the admin page loads", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("token", "test-token"))
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname
    if (path === "/api/v1/users/me") return route.fulfill({ json: { id: 2, username: "member", role: "user", is_active: true } })
    if (path === "/api/v1/system/info") return route.fulfill({ json: { system_name: "EffChat" } })
    if (path === "/api/v1/models") return route.fulfill({ json: { models: [], total: 0 } })
    if (path === "/api/v1/sessions") return route.fulfill({ json: { sessions: [], has_more: false, next_offset: 0 } })
    if (path === "/api/v1/session-folders") return route.fulfill({ json: { folders: [] } })
    return route.fulfill({ json: {} })
  })

  await page.goto("/admin/models")

  await expect(page).toHaveURL(/\/$/)
  await expect(page.getByText("管理后台")).toHaveCount(0)
})

test("dirty system configuration blocks section changes until the user decides", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("token", "test-token"))
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname
    if (path === "/api/v1/users/me") return route.fulfill({ json: { id: 1, username: "admin", role: "admin", is_active: true } })
    if (path === "/api/v1/system/info") return route.fulfill({ json: { system_name: "EffChat" } })
    if (path === "/api/v1/models") return route.fulfill({ json: { models: [], total: 0 } })
    if (path === "/api/v1/admin/config") {
      return route.fulfill({
        json: {
          config: [{ key: "system_name", value: "EffChat", config_type: "string", display_name: "系统名称", category: "基础", sort_order: 1 }],
        },
      })
    }
    if (path === "/api/v1/admin/channels") return route.fulfill({ json: { channels: [], total: 0 } })
    if (path === "/api/v1/admin/groups") return route.fulfill({ json: { groups: [], total: 0 } })
    return route.fulfill({ json: {} })
  })

  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/admin/config")
  const configInput = page.locator("main input").first()
  await expect(configInput).toBeVisible()
  await configInput.fill("EffChat test")

  const desktopNavigation = page.getByRole("navigation", { name: "管理后台导航" }).first()
  await desktopNavigation.getByRole("button", { name: "模型", exact: true }).click()
  await expect(page.getByRole("heading", { name: "放弃未保存修改？" })).toBeVisible()
  await page.getByRole("button", { name: "继续编辑" }).click()
  await expect(page).toHaveURL(/\/admin\/config$/)

  await desktopNavigation.getByRole("button", { name: "模型", exact: true }).click()
  await page.getByRole("button", { name: "放弃修改" }).click()
  await expect(page).toHaveURL(/\/admin\/models$/)
})
