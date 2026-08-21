import { expect, test } from "@playwright/test"

const group = {
  id: 7,
  name: "Fixture Group",
  level: 10,
  description: "Fixture description",
  is_default: false,
  daily_message_limit: 0,
  daily_token_limit: 0,
  concurrent_run_limit: 0,
  daily_tool_call_limit: 0,
  daily_web_search_limit: 0,
  daily_web_extract_limit: 0,
  daily_ocr_file_limit: 0,
  daily_ocr_page_limit: 0,
}

test("group editor uses product dialogs for discard and deletion", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("token", "test-token"))
  const deleteRequests: string[] = []
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (request.method() === "DELETE") deleteRequests.push(path)
    if (path === "/api/v1/users/me") return route.fulfill({ json: { id: 1, username: "admin", role: "admin", is_active: true } })
    if (path === "/api/v1/system/info") return route.fulfill({ json: { system_name: "EffChat" } })
    if (path === "/api/v1/models") return route.fulfill({ json: { models: [], total: 0 } })
    if (path === "/api/v1/admin/groups" && request.method() === "GET") return route.fulfill({ json: { groups: [group], total: 1 } })
    if (path === "/api/v1/admin/groups/7" && request.method() === "DELETE") return route.fulfill({ json: { message: "deleted" } })
    return route.fulfill({ json: {} })
  })

  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/admin/groups")
  const groupButton = page.getByText("Fixture Group", { exact: true }).first()
  await expect(groupButton).toBeVisible()
  await groupButton.click()
  const nameInput = page.getByLabel("名称")
  await nameInput.fill("Fixture Group changed")

  const newGroupButton = page.getByRole("button", { name: "新建分组" })
  await newGroupButton.click()
  const discardDialog = page.getByRole("dialog", { name: "放弃未保存修改？" })
  await expect(discardDialog).toBeVisible()
  await discardDialog.getByRole("button", { name: "继续编辑" }).click()
  await expect(nameInput).toHaveValue("Fixture Group changed")
  await newGroupButton.click()
  await page.getByRole("dialog", { name: "放弃未保存修改？" }).getByRole("button", { name: "放弃修改" }).click()
  await expect(nameInput).toHaveValue("")

  await groupButton.click()
  const deleteButton = page.getByRole("button", { name: "删除" })
  await deleteButton.click()
  const deleteDialog = page.getByRole("dialog", { name: "删除分组？" })
  await expect(deleteDialog).toContainText("显式绑定该组的用户将继承当前默认组")
  await deleteDialog.getByRole("button", { name: "取消" }).click()
  await expect(deleteButton).toBeFocused()
  expect(deleteRequests).toHaveLength(0)

  await deleteButton.click()
  await page.getByRole("dialog", { name: "删除分组？" }).getByRole("button", { name: "删除分组" }).click()
  await expect.poll(() => deleteRequests.length).toBe(1)
  expect(deleteRequests[0]).toBe("/api/v1/admin/groups/7")
})
