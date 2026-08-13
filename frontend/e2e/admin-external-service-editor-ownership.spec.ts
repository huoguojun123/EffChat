import { expect, test, type Page, type Route } from "@playwright/test"

const timestamp = "2026-08-05T00:00:00Z"
const tavily = service(1, "tavily_search", "Tavily", "search", "https://api.tavily.com/search", 10)
const firecrawl = service(2, "firecrawl", "Firecrawl", "crawler", "https://api.firecrawl.dev/v2", 10)

function service(id: number, key: string, displayName: string, kind: "search" | "crawler", baseURL: string, sortOrder: number) {
  return {
    id,
    key,
    display_name: displayName,
    kind,
    base_url: baseURL,
    api_key_set: true,
    enabled: true,
    sort_order: sortOrder,
    max_concurrency: 0,
    created_at: timestamp,
    updated_at: timestamp,
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
    if (path === "/api/v1/admin/external-services" && request.method() === "GET") {
      return fulfillJSON(route, { services: [tavily, firecrawl] })
    }
    if (path === "/api/v1/admin/external-services" && request.method() === "POST") {
      const payload = request.postDataJSON()
      const current = payload.key === tavily.key ? tavily : firecrawl
      return fulfillJSON(route, { ...current, ...payload, api_key_set: true }, 300)
    }
    if (path === "/api/v1/admin/external-services/test" && request.method() === "POST") {
      return fulfillJSON(route, { ok: true, duration_ms: 42 }, 300)
    }
    return fulfillJSON(route, {})
  })
}

test("external service save and test responses stay with their draft", async ({ page }) => {
  await installRoutes(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/admin/channels")

  const handleBox = await page.getByRole("button", { name: "调整 Tavily 顺序" }).boundingBox()
  expect(handleBox).not.toBeNull()
  expect(handleBox!.width).toBeGreaterThanOrEqual(43.5)
  expect(handleBox!.height).toBeGreaterThanOrEqual(43.5)
  await page.getByText("Tavily", { exact: true }).click()
  await expect(page.getByRole("dialog", { name: "Tavily" })).toHaveAccessibleDescription("编辑外部服务地址、凭据、并发和启用状态。")
  const baseURL = page.getByLabel("Base URL")
  await baseURL.fill("https://saved.example.invalid/search")
  await page.getByRole("button", { name: "保存", exact: true }).click()
  await baseURL.fill("https://newer.example.invalid/search")
  await expect(page.getByText("已保存较早版本，当前修改仍未保存")).toBeVisible()
  await expect(baseURL).toHaveValue("https://newer.example.invalid/search")

  await page.getByRole("button", { name: "保存", exact: true }).click()
  page.once("dialog", (dialog) => dialog.accept())
  await page.keyboard.press("Escape")
  await page.getByText("Firecrawl", { exact: true }).click()
  await expect(baseURL).toHaveValue("https://api.firecrawl.dev/v2")
  await page.waitForTimeout(350)
  await expect(baseURL).toHaveValue("https://api.firecrawl.dev/v2")

  await page.getByRole("button", { name: "测试", exact: true }).click()
  await baseURL.fill("https://firecrawl-newer.example.invalid/v2")
  await page.waitForTimeout(350)
  await expect(page.getByText(/连接成功/)).toHaveCount(0)
  await expect(baseURL).toHaveValue("https://firecrawl-newer.example.invalid/v2")

  await page.getByRole("button", { name: "保存", exact: true }).click()
  await expect(baseURL).toHaveCount(0)
  await page.getByText("Tavily", { exact: true }).click()
  await expect(baseURL).toHaveValue("https://newer.example.invalid/search")
})
