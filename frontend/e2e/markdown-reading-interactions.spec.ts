import { expect, test, type Page } from "@playwright/test"

const session = {
  id: 1,
  user_id: 1,
  title: "Markdown 阅读交互",
  title_generated: false,
  model_id: "",
  provider: "",
  created_at: "2026-08-23T00:00:00Z",
  updated_at: "2026-08-23T00:00:00Z",
}

const markdown = [
  "[外部参考](https://example.com/docs)",
  "",
  '![可用图片](/fixtures/markdown-image.png "示例图")',
  "",
  "![损坏图片](/fixtures/missing-image.png)",
].join("\n")

async function mockChat(page: Page) {
  await page.addInitScript(() => localStorage.setItem("token", "test-token"))
  await page.route("**/fixtures/markdown-image.png", (route) => route.fulfill({
    contentType: "image/png",
    body: Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=", "base64"),
  }))
  await page.route("**/fixtures/missing-image.png", (route) => route.fulfill({ status: 404, body: "missing" }))
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname
    if (path === "/api/v1/users/me") return route.fulfill({ json: { id: 1, username: "member", role: "user", is_active: true } })
    if (path === "/api/v1/system/info") return route.fulfill({ json: { system_name: "EffChat" } })
    if (path === "/api/v1/models") return route.fulfill({ json: { models: [], total: 0 } })
    if (path === "/api/v1/sessions") return route.fulfill({ json: { sessions: [session], has_more: false, next_offset: 0 } })
    if (path === "/api/v1/session-folders") return route.fulfill({ json: { folders: [] } })
    if (path === "/api/v1/sessions/1") return route.fulfill({ json: session })
    if (path === "/api/v1/sessions/1/turns") return route.fulfill({ json: { turns: [], total: 0, has_more: false } })
    if (path === "/api/v1/sessions/1/messages" || path === "/api/v1/sessions/1/message-window") {
      return route.fulfill({
        json: {
          messages: [{
            id: 1,
            session_id: 1,
            schema_version: "v2",
            role: "assistant",
            has_tool_calls: false,
            has_reasoning: false,
            created_at: "2026-08-23T00:00:00Z",
            message_data: { role: "assistant", content: markdown },
          }],
          has_more: false,
          has_older: false,
          has_newer: false,
          first_turn_id: 1,
          last_turn_id: 1,
        },
      })
    }
    return route.fulfill({ json: {} })
  })
}

test("Markdown links and images keep safe readable behavior on mobile", async ({ page }) => {
  const errors: string[] = []
  page.on("console", (message) => {
    if (message.type() === "error") errors.push(message.text())
  })
  page.on("pageerror", (error) => errors.push(error.message))

  await page.setViewportSize({ width: 390, height: 844 })
  await mockChat(page)
  await page.goto("/chat/1")

  const body = page.locator(".markdown-body").last()
  const externalLink = body.getByRole("link", { name: "外部参考" })
  await expect(externalLink).toHaveAttribute("target", "_blank")
  await expect(externalLink).toHaveAttribute("rel", "noreferrer")
  await expect(body.getByText("图片无法加载：损坏图片")).toBeVisible()

  await body.getByRole("button", { name: "放大图片：可用图片" }).click()
  await expect(page.getByRole("dialog")).toBeVisible()
  await expect(page.getByRole("img", { name: "示例图" })).toBeVisible()
  await page.getByRole("button", { name: "关闭" }).click()

  expect(await body.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true)
  expect(errors.filter((message) => !message.includes("Failed to load resource: the server responded with a status of 404"))).toEqual([])
})
