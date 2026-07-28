import { expect, test, type Page, type TestInfo } from "@playwright/test"

const session = {
  id: 1,
  user_id: 1,
  title: "长 Markdown 流",
  title_generated: false,
  model_id: "demo-model",
  provider: "demo",
  created_at: "2026-07-25T00:00:00Z",
  updated_at: "2026-07-25T00:00:00Z",
}

const markdown = [
  "# LONG-MARKDOWN-START",
  ...Array.from({ length: 360 }, (_, index) => [
    `## Section ${index + 1}`,
    `第 ${index + 1} 段保留 **粗体语义**、\`inline-code-${index + 1}\` 和 [reference](https://example.com/${index + 1})。`,
    "这是一段用于移动端流式渲染回归的稳定文本，要求内容持续增长时仍能完成最终 Markdown 解析，不能因为帧内合并而丢字、乱序或制造横向滚动。".repeat(3),
  ].join("\n\n")),
  "## LONG-MARKDOWN-END",
].join("\n\n")

if (markdown.length < 100_000) {
  throw new Error(`long Markdown fixture is only ${markdown.length} characters`)
}

function storedMessage(id: number, role: "user" | "assistant", content: string) {
  return {
    id,
    session_id: 1,
    schema_version: "v2",
    role,
    has_tool_calls: false,
    has_reasoning: false,
    created_at: "2026-07-25T00:01:00Z",
    message_data: { role, content },
  }
}

async function mockLongMarkdownStream(page: Page) {
  let messages: ReturnType<typeof storedMessage>[] = []
  const chunks = Array.from({ length: Math.ceil(markdown.length / 512) }, (_, index) => markdown.slice(index * 512, (index + 1) * 512))
  const stream = chunks
    .map((delta) => `event: content_delta\ndata: ${JSON.stringify({ delta })}\n\n`)
    .join("") + "event: message_complete\ndata: {\"finish_reason\":\"stop\"}\n\n"

  await page.addInitScript(() => {
    localStorage.setItem("token", "test-token")
    const durations: number[] = []
    Object.defineProperty(window, "__fchatLongTasks", { value: durations, writable: true })
    try {
      new PerformanceObserver((list) => {
        for (const entry of list.getEntries()) durations.push(entry.duration)
      }).observe({ entryTypes: ["longtask"] })
    } catch {
      // Older engines without Long Task support still run the semantic assertions.
    }
  })
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
      return route.fulfill({ json: { messages, has_more: false, first_turn_id: 1, last_turn_id: 1, has_older: false, has_newer: false } })
    }
    if (path === "/api/v1/sessions/1/turns") {
      return route.fulfill({ json: { turns: [], total: 0, has_more: false, next_before_turn_id: null } })
    }
    if (path === "/api/v1/files/upload-limits") {
      return route.fulfill({ json: { max_file_size_mb: 20, max_session_files: 50, allowed_types: [] } })
    }
    if (path === "/api/v1/files") return route.fulfill({ json: { files: [], has_more: false, next_offset: 0 } })
    if (path === "/api/v1/sessions/1/messages/preflight") return route.fulfill({ json: { status: "ok", needs_compaction: false } })
    if (path === "/api/v1/sessions/1/messages/stream") {
      messages = [storedMessage(1, "user", "生成长 Markdown"), storedMessage(2, "assistant", markdown)]
      return route.fulfill({ contentType: "text/event-stream", body: stream })
    }
    return route.fulfill({ json: {} })
  })
}

async function attachMetrics(page: Page, testInfo: TestInfo, renderDuration: number) {
  const longTasks = await page.evaluate(() => ((window as Window & { __fchatLongTasks?: number[] }).__fchatLongTasks || []).slice())
  const metrics = {
    characters: markdown.length,
    render_duration_ms: Math.round(renderDuration),
    long_task_count: longTasks.length,
    max_long_task_ms: Math.round(Math.max(0, ...longTasks)),
    total_long_task_ms: Math.round(longTasks.reduce((sum, value) => sum + value, 0)),
  }
  console.info(`LONG_MARKDOWN_METRICS ${JSON.stringify(metrics)}`)
  await testInfo.attach("long-markdown-metrics", {
    body: Buffer.from(JSON.stringify(metrics, null, 2)),
    contentType: "application/json",
  })
  return metrics
}

test("100K Markdown stream remains complete and bounded on mobile", async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockLongMarkdownStream(page)
  await page.goto("/chat/1")
  await page.evaluate(() => {
    const target = window as Window & { __fchatLongTasks?: number[] }
    if (target.__fchatLongTasks) target.__fchatLongTasks.length = 0
  })

  const started = Date.now()
  await page.getByTestId("chat-input").fill("生成长 Markdown")
  await page.getByTestId("send-button").click()
  const body = page.locator(".markdown-body").last()
  await expect(body.getByRole("heading", { name: "LONG-MARKDOWN-END" })).toBeVisible({ timeout: 45_000 })
  await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
  const metrics = await attachMetrics(page, testInfo, Date.now() - started)

  await expect(body.getByRole("heading", { name: "LONG-MARKDOWN-START" })).toBeVisible()
  await expect(body.locator("strong").first()).toHaveText("粗体语义")
  await expect(body.locator("code").first()).toHaveText("inline-code-1")
  await expect(body.locator('a[href="https://example.com/360"]')).toHaveText("reference")
  expect(await body.textContent()).toContain("第 360 段保留")
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  expect(metrics.max_long_task_ms).toBeLessThan(2_000)
})
