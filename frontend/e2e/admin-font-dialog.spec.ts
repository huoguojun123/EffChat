import { expect, test } from "@playwright/test"

const font = {
  id: 31,
  display_name: "Fixture Sans",
  family_name: "Fixture Sans",
  file_name: "fixture-sans.woff2",
  mime_type: "font/woff2",
  file_size: 2048,
  checksum: "fixture-checksum",
  weight: 400,
  style: "normal",
  enabled: true,
  created_at: "2026-08-20T00:00:00Z",
  updated_at: "2026-08-20T00:00:00Z",
}

test("font deletion explains affected slots and keeps the action inside a product dialog", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("token", "test-token"))
  const deleteRequests: string[] = []
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (request.method() === "DELETE") deleteRequests.push(path)
    if (path === "/api/v1/users/me") return route.fulfill({ json: { id: 1, username: "admin", role: "admin", is_active: true } })
    if (path === "/api/v1/system/info") return route.fulfill({ json: { system_name: "EffChat" } })
    if (path === "/api/v1/models") return route.fulfill({ json: { models: [], total: 0 } })
    if (path === "/api/v1/admin/fonts" && request.method() === "GET") {
      return route.fulfill({ json: { fonts: [font], selected_font_ids: { chinese: 31, latin: null, code: null } } })
    }
    if (path === "/api/v1/admin/fonts/31" && request.method() === "DELETE") {
      return route.fulfill({ json: { message: "deleted", selected_font_ids: { chinese: null, latin: null, code: null } } })
    }
    return route.fulfill({ json: {} })
  })

  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/admin/fonts")
  await expect(page.getByRole("heading", { name: "字体" })).toBeVisible()
  const deleteButton = page.getByRole("button", { name: "删除字体：Fixture Sans" })
  await expect(deleteButton).toBeVisible()

  await deleteButton.click()
  const confirmation = page.getByRole("dialog", { name: "删除字体？" })
  await expect(confirmation).toBeVisible()
  await expect(confirmation).toContainText("当前用于：中文字体")
  await confirmation.getByRole("button", { name: "取消" }).click()
  await expect(confirmation).toBeHidden()
  await expect(deleteButton).toBeFocused()
  expect(deleteRequests).toHaveLength(0)

  await deleteButton.click()
  await page.getByRole("dialog", { name: "删除字体？" }).getByRole("button", { name: "删除字体" }).click()
  await expect.poll(() => deleteRequests).toContain("/api/v1/admin/fonts/31")
  await expect(page.getByText("Fixture Sans")).toHaveCount(0)
})
