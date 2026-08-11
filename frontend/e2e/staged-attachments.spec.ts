import { expect, test, type Locator, type Page } from "@playwright/test"

const session = {
  id: 1,
  user_id: 1,
  title: "附件暂存",
  title_generated: false,
  model_id: "demo-model",
  provider: "demo",
  created_at: "2026-07-15T00:00:00Z",
  updated_at: "2026-07-15T00:00:00Z",
}

type StoredFile = {
  id: number
  user_id: number
  session_id: number
  file_name: string
  file_type: string
  file_size: number
  status: string
  extract_status: string
  created_at: string
}

const tinyPNG = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScL9WQAAAABJRU5ErkJggg==", "base64")

function screenshot(id: number): StoredFile {
  return {
    id,
    user_id: 1,
    session_id: 1,
    file_name: `screen-${id}.png`,
    file_type: "image/png",
    file_size: 1024,
    status: "active",
    extract_status: "ready",
    created_at: `2026-07-15T00:00:${String(id).padStart(2, "0")}Z`,
  }
}

async function mockAttachmentChat(page: Page, initialStaged = 24) {
  const staged = Array.from({ length: initialStaged }, (_, index) => screenshot(index + 1))
  const referenced: StoredFile[] = []
  let messages: unknown[] = []
  let nextFileID = initialStaged + 1
  const deleted = new Set<number>()

  await page.addInitScript(() => localStorage.setItem("token", "test-token"))
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname

    if (path === "/api/v1/users/me") return route.fulfill({ json: { id: 1, username: "member", role: "user", is_active: true } })
    if (path === "/api/v1/system/info") return route.fulfill({ json: { system_name: "EffChat" } })
    if (path === "/api/v1/models") {
      return route.fulfill({ json: { models: [{ id: "demo-model", provider: "demo", display_name: "Demo", enabled: true, vision: true, sort_order: 1 }], total: 1 } })
    }
    if (path === "/api/v1/sessions") return route.fulfill({ json: { sessions: [session], has_more: false, next_offset: 0 } })
    if (path === "/api/v1/session-folders") return route.fulfill({ json: { folders: [] } })
    if (path === "/api/v1/sessions/1") return route.fulfill({ json: session })
    if (path === "/api/v1/sessions/1/messages" || path === "/api/v1/sessions/1/message-window") {
      const firstTurnId = (messages[0] as { id?: number } | undefined)?.id || 0
      return route.fulfill({ json: { messages, has_more: false, first_turn_id: firstTurnId, last_turn_id: firstTurnId, has_older: false, has_newer: false } })
    }
    if (path === "/api/v1/sessions/1/turns") {
      return route.fulfill({ json: { turns: [], total: 0, has_more: false, next_before_turn_id: null } })
    }
    if (path === "/api/v1/files/upload-limits") {
      return route.fulfill({ json: { max_file_size_mb: 20, max_session_files: 50, allowed_types: ["image/png"] } })
    }
    if (path === "/api/v1/files" && request.method() === "GET") {
      const files = url.searchParams.get("referenced") === "true" ? referenced : staged
      return route.fulfill({ json: { files, has_more: false, next_offset: files.length } })
    }
    if (path === "/api/v1/files" && request.method() === "POST") {
      const file = screenshot(nextFileID++)
      staged.push(file)
      return route.fulfill({ status: 201, json: file })
    }
    if (/^\/api\/v1\/files\/\d+$/.test(path)) {
      const id = Number(path.split("/").pop())
      if (request.method() === "DELETE") {
        deleted.add(id)
        const index = referenced.findIndex((file) => file.id === id)
        if (index >= 0) referenced.splice(index, 1)
        return route.fulfill({ json: { message: "file deleted" } })
      }
      if (deleted.has(id)) return route.fulfill({ status: 404, json: { error: "file not found" } })
      return route.fulfill({ contentType: "image/png", body: tinyPNG })
    }
    if (path === "/api/v1/sessions/1/messages/preflight") return route.fulfill({ json: { needs_compaction: false } })
    if (path === "/api/v1/sessions/1/messages/stream") {
      const body = JSON.parse(request.postData() || "{}") as { attachments?: number[]; content?: string }
      if (body.content === "force failure") return route.fulfill({ status: 500, json: { error: "mock send failure" } })
      const selected = new Set(body.attachments || [])
      const sent = staged.filter((file) => selected.has(file.id))
      for (const file of sent) referenced.push(file)
      for (let index = staged.length - 1; index >= 0; index--) {
        if (selected.has(staged[index].id)) staged.splice(index, 1)
      }
      messages = [{
        id: 101,
        session_id: 1,
        schema_version: "v2",
        role: "user",
        has_tool_calls: false,
        has_reasoning: false,
        created_at: "2026-07-15T00:01:00Z",
        message_data: {
          role: "user",
          content: body.content || "",
          attachments: sent.map((file) => ({ file_id: file.id, filename: file.file_name, file_type: file.file_type, size: file.file_size })),
        },
      }]
      return route.fulfill({
        contentType: "text/event-stream",
        body: "event: message_complete\ndata: {\"finish_reason\":\"stop\"}\n\n",
      })
    }
    return route.fulfill({ json: {} })
  })
}

async function expectNoHorizontalOverflow(page: Page) {
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
}

async function expectPointerActionable(locator: Locator) {
  await expect.poll(async () => locator.evaluate((element) => {
    const rect = element.getBoundingClientRect()
    const hit = document.elementFromPoint(rect.left + rect.width / 2, rect.top + rect.height / 2)
    return hit === element || (hit != null && element.contains(hit))
  })).toBe(true)
}

async function expectMinimumTouchTarget(locator: Locator, minimum = 44) {
  const box = await locator.boundingBox()
  expect(box).not.toBeNull()
  expect(box!.width).toBeGreaterThanOrEqual(minimum)
  expect(box!.height).toBeGreaterThanOrEqual(minimum)
}

test("staged attachments batch, persist selection, and move only sent files", async ({ page }) => {
  await mockAttachmentChat(page, 0)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/chat/1")

  await page.getByTestId("file-input").setInputFiles(
    Array.from({ length: 24 }, (_, index) => ({ name: `screen-${index + 1}.png`, mimeType: "image/png", buffer: tinyPNG })),
  )

  const staging = page.getByTestId("composer-toolbar").getByRole("button", { name: "暂存附件", exact: true })
  await staging.click()
  await expect(page.getByRole("dialog", { name: "暂存附件" })).toBeVisible()
  await expect(page.getByText("本次已选 4/10 个附件 · 4/4 张图片")).toBeVisible()
  await expect(page.getByRole("button", { name: "取消选择附件：screen-1.png" })).toBeVisible()
  await expect(page.getByRole("button", { name: "选择附件：screen-5.png" })).toBeVisible()

  for (const id of [3, 1, 4, 2]) {
    const button = page.getByRole("button", { name: `选择附件：screen-${id}.png` })
    await button.click()
  }
  await expect(page.getByRole("button", { name: "取消选择附件：screen-3.png" })).toBeVisible()
  await page.getByRole("button", { name: "选择附件：screen-5.png" }).click()
  await expect(page.getByText("本次已选 4/4 张图片")).toBeVisible()

  await page.getByRole("dialog", { name: "暂存附件" }).getByRole("button", { name: "关闭暂存附件" }).click()
  await page.reload()
  await staging.click()
  await expect(page.getByRole("button", { name: "取消选择附件：screen-3.png" })).toBeVisible()
  await expect(page.getByRole("button", { name: "选择附件：screen-5.png" })).toBeVisible()
  await page.getByRole("dialog", { name: "暂存附件" }).getByRole("button", { name: "关闭暂存附件" }).click()

  await page.getByTestId("chat-input").fill("force failure")
  await page.getByTestId("send-button").click()
  await expect(page.getByText("mock send failure")).toBeVisible()
  await staging.click()
  await expect(page.getByRole("button", { name: "取消选择附件：screen-3.png" })).toBeVisible()
  await page.getByRole("dialog", { name: "暂存附件" }).getByRole("button", { name: "关闭暂存附件" }).click()

  await page.getByTestId("chat-input").fill("请看这些截图")
  await page.getByTestId("send-button").click()
  await expect(page.locator('[data-testid="message-item"][data-role="user"]')).not.toHaveCount(0)
  const sentThumbnail = page.locator('[data-testid="message-item"][data-role="user"] button[title="screen-3.png"]').first()
  await expect.poll(async () => (await sentThumbnail.boundingBox())?.height || 0).toBeLessThanOrEqual(112)

  await staging.click()
  await expect(page.getByRole("button", { name: "取消选择附件：screen-5.png" })).toBeVisible()
  await expect(page.getByText("本次已选 4/10 个附件 · 4/4 张图片")).toBeVisible()
  await page.getByRole("dialog", { name: "暂存附件" }).getByRole("button", { name: "关闭暂存附件" }).click()

  await page.getByRole("button", { name: "文件", exact: true }).click()
  await expect(page.getByRole("region", { name: "会话文件" })).toBeVisible()
  await expect(page.getByRole("listbox", { name: "会话文件列表" }).getByRole("option")).toHaveCount(4)
  await expect(page.getByText("screen-3.png")).toBeVisible()
  await page.getByRole("listbox", { name: "会话文件列表" }).getByText("screen-3.png").click()
  page.once("dialog", (dialog) => dialog.accept())
  await page.getByRole("region", { name: "会话文件" }).getByRole("button", { name: "删除文件：screen-3.png" }).click()
  await expect(page.getByText("screen-3.png")).toHaveCount(0)
  await page.getByRole("dialog", { name: "对话附件" }).getByRole("button", { name: "关闭窗口" }).click()
  await page.reload()
  await expect(page.getByText("附件已删除")).toBeVisible()
  await expectNoHorizontalOverflow(page)
})

for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }, { width: 430, height: 932 }]) {
  test(`staged attachments drawer stays usable at ${viewport.width}px`, async ({ page }) => {
    await mockAttachmentChat(page)
    await page.setViewportSize(viewport)
    await page.goto("/chat/1")
    const trigger = page.getByRole("button", { name: "暂存附件" })
    await trigger.click()
    const drawer = page.getByRole("dialog", { name: "暂存附件" })
    await expect(drawer).toBeVisible()
    await expect.poll(async () => (await drawer.boundingBox())?.height || 0).toBeGreaterThanOrEqual(viewport.height - 1)
    await expect(page.getByRole("button", { name: "预览附件：screen-1.png" })).toBeVisible()

    const upload = drawer.getByRole("button", { name: "上传", exact: true })
    const refresh = drawer.getByRole("button", { name: "刷新暂存附件" })
    const close = drawer.getByRole("button", { name: "关闭暂存附件" })
    await expectPointerActionable(upload)
    await expectPointerActionable(refresh)
    await expectPointerActionable(close)
    if (viewport.width <= 430) {
      await expectMinimumTouchTarget(upload)
      await expectMinimumTouchTarget(refresh)
      await expectMinimumTouchTarget(close)
      await expectMinimumTouchTarget(drawer.getByRole("button", { name: "删除暂存附件：screen-1.png" }))
    }

    await expect(upload).toBeFocused()
    await page.keyboard.press("Tab")
    await expect(refresh).toBeFocused()
    await page.keyboard.press("Tab")
    await expect(close).toBeFocused()
    await page.keyboard.press("Tab")
    await expect(drawer.getByRole("button", { name: "选择附件：screen-1.png" })).toBeFocused()

    const chooserPromise = page.waitForEvent("filechooser")
    await upload.click()
    const chooser = await chooserPromise
    await chooser.setFiles({ name: "mobile-upload.png", mimeType: "image/png", buffer: tinyPNG })
    await expect(drawer.getByText("screen-25.png")).toBeVisible()

    const refreshed = page.waitForResponse((response) => response.request().method() === "GET" && new URL(response.url()).pathname === "/api/v1/files")
    await refresh.click()
    await refreshed

    await close.click()
    await expect(drawer).toBeHidden()
    await expect(trigger).toBeFocused()

    await trigger.click()
    await page.keyboard.press("Escape")
    await expect(drawer).toBeHidden()
    await expect(trigger).toBeFocused()
    await expectNoHorizontalOverflow(page)
  })
}
