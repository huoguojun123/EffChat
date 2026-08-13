import { expect, test, type Page, type Route } from "@playwright/test"

const session = {
  id: 1,
  user_id: 1,
  title: "Blob ownership",
  title_generated: false,
  model_id: "demo-model",
  provider: "demo",
  created_at: "2026-08-12T00:00:00Z",
  updated_at: "2026-08-12T00:00:00Z",
}

const tinyPNG = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScL9WQAAAABJRU5ErkJggg==", "base64")
const files = [imageFile(701, "alpha.png"), imageFile(702, "beta.png")]

function imageFile(id: number, fileName: string) {
  return {
    id,
    user_id: 1,
    session_id: 1,
    file_name: fileName,
    file_type: "image/png",
    file_size: tinyPNG.length,
    status: "active",
    extract_status: "ready",
    created_at: "2026-08-12T00:01:00Z",
  }
}

type PendingBlob = { id: number; route: Route }

async function mockImageFiles(page: Page) {
  const pending: PendingBlob[] = []
  await page.addInitScript(() => {
    localStorage.setItem("token", "test-token")
    const created: string[] = []
    const revoked: string[] = []
    const createObjectURL = URL.createObjectURL.bind(URL)
    const revokeObjectURL = URL.revokeObjectURL.bind(URL)
    Object.assign(window, { __blobLifecycle: { created, revoked } })
    URL.createObjectURL = (blob) => {
      const url = createObjectURL(blob)
      created.push(url)
      return url
    }
    URL.revokeObjectURL = (url) => {
      revoked.push(url)
      revokeObjectURL(url)
    }
  })
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (path === "/api/v1/users/me") return route.fulfill({ json: { id: 1, username: "member", role: "user", is_active: true } })
    if (path === "/api/v1/system/info") return route.fulfill({ json: { system_name: "EffChat" } })
    if (path === "/api/v1/models") return route.fulfill({ json: { models: [{ id: "demo-model", provider: "demo", display_name: "Demo", enabled: true, vision: true, sort_order: 1 }], total: 1 } })
    if (path === "/api/v1/sessions") return route.fulfill({ json: { sessions: [session], has_more: false, next_offset: 0 } })
    if (path === "/api/v1/session-folders") return route.fulfill({ json: { folders: [] } })
    if (path === "/api/v1/sessions/1") return route.fulfill({ json: session })
    if (path === "/api/v1/sessions/1/messages" || path === "/api/v1/sessions/1/message-window") {
      return route.fulfill({ json: { messages: [], has_more: false, first_turn_id: 0, last_turn_id: 0, has_older: false, has_newer: false } })
    }
    if (path === "/api/v1/sessions/1/turns") return route.fulfill({ json: { turns: [], total: 0, has_more: false, next_before_turn_id: null } })
    if (path === "/api/v1/files" && request.method() === "GET") return route.fulfill({ json: { files, has_more: false, next_offset: files.length } })
    const match = path.match(/^\/api\/v1\/files\/(701|702)$/)
    if (match) {
      pending.push({ id: Number(match[1]), route })
      return
    }
    return route.fulfill({ json: {} })
  })
  return pending
}

async function fulfillAll(pending: PendingBlob[], id: number, status = 200) {
  const matches = pending.filter((entry) => entry.id === id)
  const others = pending.filter((entry) => entry.id !== id)
  pending.splice(0, pending.length, ...others)
  for (const entry of matches) {
    await entry.route.fulfill(status === 200
      ? { status, contentType: "image/png", body: tinyPNG }
      : { status, json: { error: "fixture failure", code: "file_read_failed", retryable: true } }).catch(() => undefined)
  }
}

async function waitForPending(page: Page, pending: PendingBlob[], id: number) {
  await expect.poll(() => pending.filter((entry) => entry.id === id).length).toBeGreaterThan(0)
  await page.waitForTimeout(0)
}

test("rapid A to B to A switches never expose stale blobs and revoke every created URL once", async ({ page }) => {
  const pending = await mockImageFiles(page)
  await page.goto("/chat/1")
  await page.getByRole("button", { name: "文件", exact: true }).click()
  await waitForPending(page, pending, 701)

  await page.getByRole("option", { name: /beta\.png/ }).click()
  await waitForPending(page, pending, 702)
  await expect(page.getByLabel("图片加载中")).toBeVisible()
  await expect(page.getByRole("img")).toHaveCount(0)

  await fulfillAll(pending, 701)
  await expect(page.getByLabel("图片加载中")).toBeVisible()
  await expect(page.getByRole("img")).toHaveCount(0)

  await fulfillAll(pending, 702)
  await expect(page.getByRole("img", { name: "beta.png" })).toBeVisible()

  await page.getByRole("option", { name: /alpha\.png/ }).click()
  await waitForPending(page, pending, 701)
  await expect(page.getByLabel("图片加载中")).toBeVisible()
  await expect(page.getByRole("img", { name: "beta.png" })).toHaveCount(0)
  await fulfillAll(pending, 701)
  await expect(page.getByRole("img", { name: "alpha.png" })).toBeVisible()

  await page.getByRole("button", { name: "关闭窗口" }).click()
  await expect(page.getByRole("dialog", { name: "对话附件" })).toHaveCount(0)
  await expect.poll(() => page.evaluate(() => {
    const lifecycle = (window as unknown as { __blobLifecycle: { created: string[]; revoked: string[] } }).__blobLifecycle
    return lifecycle.created.length > 0 && lifecycle.created.every((url) => lifecycle.revoked.filter((item) => item === url).length === 1)
  })).toBe(true)
})

test("a replacement failure cannot reveal the previous image", async ({ page }) => {
  const pending = await mockImageFiles(page)
  await page.goto("/chat/1")
  await page.getByRole("button", { name: "文件", exact: true }).click()
  await waitForPending(page, pending, 701)
  await fulfillAll(pending, 701)
  await expect(page.getByRole("img", { name: "alpha.png" })).toBeVisible()

  await page.getByRole("option", { name: /beta\.png/ }).click()
  await waitForPending(page, pending, 702)
  await expect(page.getByRole("img", { name: "alpha.png" })).toHaveCount(0)
  await fulfillAll(pending, 702, 503)
  await expect(page.getByText("图片预览加载失败")).toBeVisible()
  await expect(page.getByRole("img")).toHaveCount(0)
})
