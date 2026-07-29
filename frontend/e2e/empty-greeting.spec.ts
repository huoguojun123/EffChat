import { expect, test, type Page } from "@playwright/test"

async function mockEmptyAccount(page: Page) {
  await page.addInitScript(() => {
    localStorage.setItem("token", "test-token")
    Math.random = () => 0
  })
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname
    if (path === "/api/v1/users/me") return route.fulfill({ json: { id: 1, username: "member", role: "user", is_active: true } })
    if (path === "/api/v1/system/info") return route.fulfill({ json: { system_name: "effchat" } })
    if (path === "/api/v1/models") {
      return route.fulfill({ json: { models: [{ id: "demo-model", provider: "demo", display_name: "Demo", enabled: true, sort_order: 1 }], total: 1 } })
    }
    if (path === "/api/v1/sessions") return route.fulfill({ json: { sessions: [], has_more: false, next_offset: 0 } })
    if (path === "/api/v1/session-folders") return route.fulfill({ json: { folders: [] } })
    return route.fulfill({ json: {} })
  })
}

test("empty accounts receive the shared quote greeting on desktop and mobile", async ({ page }) => {
  await mockEmptyAccount(page)
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
