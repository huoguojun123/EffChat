import { expect, test, type Page, type Route } from "@playwright/test"

const template = "You are EffChat.\nUse the configured context."

async function fulfillJSON(route: Route, json: unknown) {
  await route.fulfill({ json })
}

async function installRoutes(page: Page) {
  await page.addInitScript(() => localStorage.setItem("token", "fixture-token"))
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (path === "/api/v1/users/me") return fulfillJSON(route, { id: 1, username: "admin", role: "admin", is_active: true })
    if (path === "/api/v1/system/info") return fulfillJSON(route, { system_name: "EffChat" })
    if (path === "/api/v1/models") return fulfillJSON(route, { models: [], total: 0 })
    if (path === "/api/v1/admin/groups") return fulfillJSON(route, { groups: [], total: 0 })
    if (path === "/api/v1/admin/channels") return fulfillJSON(route, { channels: [] })
    if (path === "/api/v1/admin/config") {
      return fulfillJSON(route, {
        config: [
          { key: "system_prompt_template", value: "Existing prompt", default: template, config_type: "string", display_name: "系统提示词", category: "提示词", sort_order: 1 },
          { key: "file_upload_allowed_types", value: "image/png", config_type: "string", display_name: "允许的文件类型", category: "文件", sort_order: 2 },
        ],
      })
    }
    if (path === "/api/v1/admin/files/cleanup-orphans" && request.method() === "POST") {
      return fulfillJSON(route, { marked: 1, removed: 1, failed: 0, failures: [], skipped_referenced: 2, older_than_hours: 24 })
    }
    return fulfillJSON(route, {})
  })
}

test("template overwrite uses a themed confirmation and restores focus on cancel", async ({ page }) => {
  await installRoutes(page)
  await page.goto("/admin/systemPrompt")

  const templateButton = page.getByRole("button", { name: "填入推荐模板" })
  await templateButton.click()
  await expect(page.getByRole("heading", { name: "覆盖当前提示词？" })).toBeVisible()
  await page.getByRole("button", { name: "取消" }).click()
  await expect(page.getByLabel("系统提示词")).toHaveValue("Existing prompt")
  await expect(templateButton).toBeFocused()

  await templateButton.click()
  await page.getByRole("button", { name: "覆盖内容" }).click()
  await expect(page.getByLabel("系统提示词")).toHaveValue(template)
})

test("orphan cleanup requires explicit destructive confirmation", async ({ page }) => {
  await installRoutes(page)
  await page.goto("/admin/config")

  const cleanupButton = page.getByRole("button", { name: "清理遗留文件" })
  await cleanupButton.click()
  await expect(page.getByRole("heading", { name: "清理暂存附件？" })).toBeVisible()
  await page.getByRole("button", { name: "取消" }).click()
  await expect(cleanupButton).toBeFocused()
  await expect(page.getByText("清理遗留文件：")).toHaveCount(0)

  await cleanupButton.click()
  await page.getByRole("button", { name: "清理文件" }).click()
  await expect(page.getByText(/清理遗留文件：标记 1，磁盘删除 1/)).toBeVisible()
})
