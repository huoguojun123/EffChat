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
  "- [x] 已完成事项",
  "- [ ] 待处理事项",
  "",
  "正文脚注[^1]",
  "",
  "[^1]: 用于验证返回引用与窄屏换行。",
  "",
  "[外部参考](https://example.com/docs)",
  "",
  '![宽图](/fixtures/wide-image.svg "宽图示例")',
  "",
  '![长图](/fixtures/tall-image.svg "长图示例")',
  "",
  "![损坏图片](/fixtures/missing-image.png)",
].join("\n")

async function mockChat(page: Page, theme: "light" | "dark") {
  await page.addInitScript((settings) => {
    localStorage.setItem("token", "test-token")
    localStorage.setItem("theme", settings.theme)
  }, { theme })
  await page.route("**/fixtures/wide-image.svg", (route) => route.fulfill({
    contentType: "image/svg+xml",
    body: imageFixture(1600, 320, "#2563eb"),
  }))
  await page.route("**/fixtures/tall-image.svg", (route) => route.fulfill({
    contentType: "image/svg+xml",
    body: imageFixture(320, 1600, "#16a34a"),
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

function imageFixture(width: number, height: number, color: string) {
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}"><rect width="100%" height="100%" fill="${color}"/></svg>`
}

for (const scenario of [
  { name: "desktop light", theme: "light", viewport: { width: 1440, height: 900 } },
  { name: "desktop dark", theme: "dark", viewport: { width: 1440, height: 900 } },
  { name: "mobile light", theme: "light", viewport: { width: 390, height: 844 } },
  { name: "mobile dark", theme: "dark", viewport: { width: 390, height: 844 } },
] as const) {
  test(`Markdown reading interactions remain stable on ${scenario.name}`, async ({ page }) => {
    const errors: string[] = []
    let expectedMissingImageResponses = 0
    page.on("console", (message) => {
      if (message.type() === "error") errors.push(message.text())
    })
    page.on("pageerror", (error) => errors.push(error.message))
    page.on("response", (response) => {
      if (response.url().endsWith("/fixtures/missing-image.png") && response.status() === 404) expectedMissingImageResponses++
    })

    await page.setViewportSize(scenario.viewport)
    await mockChat(page, scenario.theme)
    await page.goto("/chat/1")

    const body = page.locator(".markdown-body").last()
    expect(await page.locator("html").evaluate((element) => element.classList.contains("dark"))).toBe(scenario.theme === "dark")

    const tasks = body.locator('.task-list-item > input[type="checkbox"]')
    await expect(tasks).toHaveCount(2)
    await expect(tasks.nth(0)).toBeDisabled()
    await expect(tasks.nth(0)).toHaveAttribute("aria-label", "已完成")
    await expect(tasks.nth(1)).toHaveAttribute("aria-label", "未完成")
    const taskStyle = await tasks.nth(0).evaluate((element) => {
      const style = getComputedStyle(element)
      const box = element.getBoundingClientRect()
      return { accentColor: style.accentColor, width: box.width, height: box.height }
    })
    expect(taskStyle.accentColor).not.toBe("auto")
    expect(taskStyle.width).toBeGreaterThanOrEqual(12)
    expect(taskStyle.height).toBeGreaterThanOrEqual(12)

    const footnotes = body.locator(".footnotes")
    await expect(footnotes).toBeVisible()
    expect(await footnotes.evaluate((element) => getComputedStyle(element).borderBlockStartStyle)).toBe("solid")
    const backref = footnotes.locator(".data-footnote-backref")
    await backref.focus()
    await expect(backref).toBeFocused()
    expect(await backref.evaluate((element) => getComputedStyle(element).outlineStyle)).not.toBe("none")

    const externalLink = body.getByRole("link", { name: "外部参考" })
    await expect(externalLink).toHaveAttribute("target", "_blank")
    await expect(externalLink).toHaveAttribute("rel", "noreferrer")
    await expect(body.getByText("图片无法加载：损坏图片")).toBeVisible()

    for (const label of ["宽图", "长图"]) {
      const image = body.getByRole("img", { name: label })
      await expect(image).toBeVisible()
      expect(await image.evaluate((element) => element.getBoundingClientRect().width <= element.closest(".markdown-body")!.getBoundingClientRect().width)).toBe(true)
    }

    await body.getByRole("button", { name: "放大图片：宽图" }).click()
    await expect(page.getByRole("dialog")).toBeVisible()
    await expect(page.getByRole("img", { name: "宽图示例" })).toBeVisible()
    await page.getByRole("button", { name: "关闭" }).click()

    expect(await body.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true)
    expect(expectedMissingImageResponses).toBe(1)
    expect(errors.filter((message) => message.includes("Failed to load resource: the server responded with a status of 404"))).toHaveLength(1)
    expect(errors.filter((message) => !message.includes("Failed to load resource: the server responded with a status of 404"))).toEqual([])
  })
}
