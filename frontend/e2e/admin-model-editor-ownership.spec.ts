import { expect, test, type Page, type Route } from "@playwright/test"

const modelA = model("model-a", "Model A", 100)
const modelB = model("model-b", "Model B", 200)

function model(id: string, displayName: string, contextWindow: number) {
  return {
    id,
    display_name: displayName,
    provider: "fixture-channel",
    vision: false,
    tool_use: true,
    reasoning: false,
    thinking_format: "auto",
    search_impl: "none",
    context_window: contextWindow,
    max_output: 32,
    enabled: true,
    min_group_level: 0,
    sort_order: contextWindow,
    catalog_source: "manual",
    catalog_checked_at: null,
    lifecycle_status: "active",
    temperature_policy: "configurable",
    temperature_value: null,
    openai_request_profile: {},
    channel_adapter: "openai_compatible",
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
    if (path === "/api/v1/models") return fulfillJSON(route, { models: [modelA, modelB], total: 2 })
    if (path === "/api/v1/admin/groups") return fulfillJSON(route, { groups: [], total: 0 })
    if (path === "/api/v1/admin/channels") {
      return fulfillJSON(route, { channels: [{
        id: 1,
        key: "fixture-channel",
        display_name: "Fixture Channel",
        adapter: "openai_compatible",
        base_url: "https://example.invalid/v1",
        api_key_set: true,
        enabled: true,
        sort_order: 1,
        created_at: "2026-08-05T00:00:00Z",
        updated_at: "2026-08-05T00:00:00Z",
      }] })
    }
    if (path === "/api/v1/admin/models/catalog/model-a") {
      return fulfillJSON(route, { model: { ...modelA, context_window: 999, catalog_source: "models_dev" } }, 350)
    }
    if (path === "/api/v1/admin/models/model-b" && request.method() === "PATCH") {
      return fulfillJSON(route, { ...modelB, ...request.postDataJSON(), display_name: "Model B (saved)" }, 300)
    }
    return fulfillJSON(route, {})
  })
}

test("catalog metadata and save responses stay with their model draft", async ({ page }) => {
  await installRoutes(page)
  await page.goto("/admin/models")
  await page.getByRole("button", { name: "模型管理" }).click()

  await page.getByText("Model A", { exact: true }).click()
  await page.getByRole("button", { name: "补能力" }).click()
  await page.getByText("Model B", { exact: true }).click()
  await expect(page.getByLabel("显示名称")).toHaveValue("Model B")
  await page.waitForTimeout(450)
  await expect(page.getByLabel("上下文")).toHaveValue("200")

  await page.getByLabel("显示名称").fill("saved revision")
  await page.getByRole("button", { name: "保存", exact: true }).click()
  await page.getByLabel("显示名称").fill("newer unsaved revision")
  await expect(page.getByText("已保存较早版本，当前修改仍未保存")).toBeVisible()
  await expect(page.getByLabel("显示名称")).toHaveValue("newer unsaved revision")
  await expect(page.getByText("Model B (saved)", { exact: true })).toBeVisible()

  page.once("dialog", (dialog) => dialog.dismiss())
  await page.getByText("Model A", { exact: true }).click()
  await expect(page.getByLabel("显示名称")).toHaveValue("newer unsaved revision")
  page.once("dialog", (dialog) => dialog.accept())
  await page.getByText("Model A", { exact: true }).click()
  await expect(page.getByLabel("显示名称")).toHaveValue("Model A")
})
