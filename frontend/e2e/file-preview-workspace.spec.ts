import { expect, test, type Page } from "@playwright/test"

const session = {
  id: 1,
  user_id: 1,
  title: "文档预览",
  title_generated: false,
  model_id: "demo-model",
  provider: "demo",
  created_at: "2026-07-27T00:00:00Z",
  updated_at: "2026-07-27T00:00:00Z",
}

const spreadsheet = {
  id: 701,
  user_id: 1,
  session_id: 1,
  file_name: "research-directory.xlsx",
  file_type: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
  file_size: 18432,
  status: "active",
  extract_status: "ready",
  token_estimate: 612,
  created_at: "2026-07-27T00:01:00Z",
}

const preview = `# Sheet1

研究机构与导师名录

| 序号 | 导师姓名 | 单位 | 研究方向 | 联系方式 |
| --- | --- | --- | --- | --- |
| 1 | 陈明远 | 智能计算研究院 | 多模态学习、知识工程与可信人工智能 | example@example.com |
| 2 | 林知夏 | 空间信息实验室 | 时空数据挖掘、遥感智能解译与地理知识图谱 | example@example.com |
`

async function mockDocumentPreview(page: Page) {
  await page.addInitScript(() => localStorage.setItem("token", "test-token"))
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname

    if (path === "/api/v1/users/me") return route.fulfill({ json: { id: 1, username: "member", role: "user", is_active: true } })
    if (path === "/api/v1/system/info") return route.fulfill({ json: { system_name: "EffChat" } })
    if (path === "/api/v1/models") {
      return route.fulfill({ json: { models: [{ id: "demo-model", provider: "demo", display_name: "Demo", enabled: true, sort_order: 1 }], total: 1 } })
    }
    if (path === "/api/v1/sessions") return route.fulfill({ json: { sessions: [session], has_more: false, next_offset: 0 } })
    if (path === "/api/v1/session-folders") return route.fulfill({ json: { folders: [] } })
    if (path === "/api/v1/sessions/1") return route.fulfill({ json: session })
    if (path === "/api/v1/sessions/1/messages" || path === "/api/v1/sessions/1/message-window") {
      return route.fulfill({ json: { messages: [], has_more: false, first_turn_id: 0, last_turn_id: 0, has_older: false, has_newer: false } })
    }
    if (path === "/api/v1/sessions/1/turns") {
      return route.fulfill({ json: { turns: [], total: 0, has_more: false, next_before_turn_id: null } })
    }
    if (path === "/api/v1/files" && request.method() === "GET") {
      return route.fulfill({ json: { files: [spreadsheet], has_more: false, next_offset: 1 } })
    }
    if (path === "/api/v1/files/701/preview") {
      return route.fulfill({ json: { file: spreadsheet, content: preview, next_cursor: "", has_more: false, truncated: false } })
    }
    return route.fulfill({ json: {} })
  })
}

test("document workspace supports wide tables and focused desktop window controls", async ({ page }) => {
  await mockDocumentPreview(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/chat/1")
  await page.getByRole("button", { name: "文件", exact: true }).click()

  const workspace = page.getByRole("dialog", { name: "对话附件" })
  const filesRegion = page.getByRole("region", { name: "会话文件" })
  await expect(filesRegion).toBeVisible()
  await expect(page.getByRole("heading", { name: "Sheet1" })).toBeVisible()
  await page.waitForTimeout(400)

  const initialBox = await workspace.boundingBox()
  expect(initialBox?.width).toBeGreaterThan(1100)
  expect(initialBox?.height).toBeGreaterThan(760)
  await expect(workspace.getByText("1 个已发送附件")).toHaveCount(0)
  await expect(workspace.getByRole("button", { name: "最小化窗口" })).toHaveCount(0)
  expect(await workspace.evaluate((element) => getComputedStyle(element).animationName)).toBe("workspace-window-open")
  await expect(page.locator(".document-markdown .markdown-table-scroll")).toBeVisible()
  expect(await page.locator(".document-markdown th").first().evaluate((cell) => cell.getBoundingClientRect().width)).toBeGreaterThanOrEqual(140)

  const titleBox = await workspace.getByTestId("workspace-titlebar").boundingBox()
  if (!titleBox || !initialBox) throw new Error("workspace title bar is not measurable")
  await page.mouse.move(titleBox.x + titleBox.width / 2, titleBox.y + titleBox.height / 2)
  await page.mouse.down()
  await page.mouse.move(titleBox.x + titleBox.width / 2 + 70, titleBox.y + titleBox.height / 2 + 40, { steps: 6 })
  await page.mouse.up()
  const afterPointerMove = await workspace.boundingBox()
  expect(Math.round(afterPointerMove?.x || 0)).toBe(Math.round(initialBox.x))
  expect(Math.round(afterPointerMove?.y || 0)).toBe(Math.round(initialBox.y))

  await workspace.getByRole("button", { name: "全屏显示" }).click()
  await expect.poll(async () => Math.round((await workspace.boundingBox())?.x ?? -1)).toBe(0)
  await expect.poll(async () => Math.round((await workspace.boundingBox())?.y ?? -1)).toBe(0)
  await expect.poll(async () => Math.round((await workspace.boundingBox())?.width || 0)).toBe(1440)
  await expect.poll(async () => Math.round((await workspace.boundingBox())?.height || 0)).toBe(900)
  await workspace.getByRole("button", { name: "退出全屏" }).click()
  await expect.poll(async () => Math.round((await workspace.boundingBox())?.width || 0)).toBe(Math.round(initialBox.width))
  await expect(filesRegion).toBeVisible()
})

test("document workspace becomes a usable full-screen reader on mobile", async ({ page }) => {
  await mockDocumentPreview(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/chat/1")
  await expect(page.locator('meta[name="viewport"]')).toHaveAttribute("content", /viewport-fit=cover/)
  const composerBox = await page.getByTestId("chat-input").locator("..").boundingBox()
  expect(composerBox).not.toBeNull()
  expect(844 - (composerBox!.y + composerBox!.height)).toBeGreaterThanOrEqual(0)
  expect(844 - (composerBox!.y + composerBox!.height)).toBeLessThanOrEqual(2)
  await page.getByRole("button", { name: "文件", exact: true }).click()

  const workspace = page.getByRole("dialog", { name: "对话附件" })
  await expect.poll(async () => Math.round((await workspace.boundingBox())?.width || 0)).toBe(390)
  await expect.poll(async () => Math.round((await workspace.boundingBox())?.height || 0)).toBe(844)
  await expect(workspace.getByRole("button", { name: "最小化窗口" })).toHaveCount(0)
  await expect(workspace.getByRole("button", { name: "全屏显示" })).toHaveCount(0)

  await page.getByRole("option", { name: /research-directory\.xlsx/ }).click()
  await expect(page.getByRole("heading", { name: "Sheet1" })).toBeVisible()
  await expect(page.getByRole("button", { name: "返回文件列表" })).toBeVisible()
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await expect(page.locator(".document-markdown .markdown-table-scroll")).toBeVisible()
})

test("sent attachment deletion uses an in-product confirmation and restores focus", async ({ page }) => {
  await mockDocumentPreview(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  const deleteRequests: string[] = []
  page.on("request", (request) => {
    if (request.method() === "DELETE") deleteRequests.push(request.url())
  })

  await page.goto("/chat/1")
  await page.getByRole("button", { name: "文件", exact: true }).click()
  const workspace = page.getByRole("dialog", { name: "对话附件" })
  const deleteButton = workspace.getByRole("button", { name: "删除文件：research-directory.xlsx" })
  await expect(deleteButton).toBeVisible()

  await deleteButton.click()
  const confirmation = page.getByRole("dialog", { name: "删除已发送附件？" })
  await expect(confirmation).toBeVisible()
  await expect(confirmation).toContainText("历史消息中的预览、下载和重新发送都将不可用")
  await confirmation.getByRole("button", { name: "取消" }).click()
  await expect(confirmation).toBeHidden()
  await expect(deleteButton).toBeFocused()
  expect(deleteRequests).toHaveLength(0)

  await deleteButton.click()
  await page.getByRole("dialog", { name: "删除已发送附件？" }).getByRole("button", { name: "删除附件" }).click()
  await expect.poll(() => deleteRequests.length).toBe(1)
  expect(deleteRequests[0]).toMatch(/\/api\/v1\/files\/701$/)
  await expect(workspace.getByText("research-directory.xlsx")).toHaveCount(0)
})
