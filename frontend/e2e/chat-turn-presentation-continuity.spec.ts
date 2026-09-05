import { expect, test, type Page } from "@playwright/test"

const session = {
  id: 1,
  user_id: 1,
  title: "Turn continuity",
  title_generated: false,
  model_id: "demo-model",
  provider: "demo",
  created_at: "2026-09-05T00:00:00Z",
  updated_at: "2026-09-05T00:00:00Z",
}

function message(id: number, role: "user" | "assistant", content: string) {
  return {
    id,
    session_id: 1,
    schema_version: "v2",
    role,
    has_tool_calls: false,
    has_reasoning: false,
    created_at: `2026-09-05T00:00:0${id}Z`,
    message_data: { role, content },
  }
}

async function installBaseRoutes(page: Page, messageWindow: () => ReturnType<typeof message>[]) {
  await page.addInitScript(() => localStorage.setItem("token", "test-token"))
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (path === "/api/v1/users/me") return route.fulfill({ json: { id: 1, username: "member", role: "user", is_active: true } })
    if (path === "/api/v1/system/info") return route.fulfill({ json: { system_name: "EffChat" } })
    if (path === "/api/v1/models") return route.fulfill({ json: { models: [{ id: "demo-model", provider: "demo", display_name: "Demo", enabled: true, sort_order: 1 }], total: 1 } })
    if (path === "/api/v1/sessions") return route.fulfill({ json: { sessions: [session], has_more: false, next_offset: 0 } })
    if (path === "/api/v1/session-folders") return route.fulfill({ json: { folders: [] } })
    if (path === "/api/v1/sessions/1") return route.fulfill({ json: session })
    if (path === "/api/v1/sessions/1/messages" || path === "/api/v1/sessions/1/message-window") {
      const messages = messageWindow()
      return route.fulfill({ json: { messages, first_turn_id: messages.find((item) => item.role === "user")?.id || 0, last_turn_id: messages.findLast((item) => item.role === "user")?.id || 0, has_older: false, has_newer: false } })
    }
    if (path === "/api/v1/sessions/1/turns") return route.fulfill({ json: { turns: [], total: 0, has_more: false, next_before_turn_id: null } })
    if (path === "/api/v1/files/upload-limits") return route.fulfill({ json: { max_file_size_mb: 20, max_session_files: 50, allowed_types: [] } })
    if (path === "/api/v1/files") return route.fulfill({ json: { files: [], has_more: false, next_offset: 0 } })
    if (path === "/api/v1/sessions/1/message-cursor") return route.fulfill({ json: { latest_message_id: 0, session_updated_at: session.updated_at } })
    return route.fulfill({ json: {} })
  })
}

test("send feedback, block streaming, and durable handoff remain one visual turn", async ({ page }) => {
  let historyRequests = 0
  await installBaseRoutes(page, () => {
    historyRequests += 1
    return historyRequests === 1 ? [] : [message(2, "assistant", "First paragraph\n\nSecond paragraph")]
  })
  await page.route("**/api/v1/sessions/1/messages/preflight", async (route) => {
    await new Promise((resolve) => setTimeout(resolve, 350))
    await route.fulfill({ json: { status: "ok", needs_compaction: false } })
  })
  await page.addInitScript(() => {
    const nativeFetch = window.fetch.bind(window)
    window.fetch = (input, init) => {
      const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url
      if (!url.includes("/api/v1/sessions/1/messages/stream")) return nativeFetch(input, init)
      const encoder = new TextEncoder()
      return Promise.resolve(new Response(new ReadableStream<Uint8Array>({
        start(controller) {
          window.setTimeout(() => controller.enqueue(encoder.encode('event: content_delta\ndata: {"delta":"First paragraph"}\n\n')), 800)
          window.setTimeout(() => controller.enqueue(encoder.encode('event: content_delta\ndata: {"delta":"\\n\\nSecond paragraph"}\n\n')), 1_050)
          window.setTimeout(() => {
            controller.enqueue(encoder.encode('event: message_complete\ndata: {"message_id":2,"finish_reason":"stop"}\n\n'))
            controller.close()
          }, 1_350)
        },
      }), { status: 200, headers: { "Content-Type": "text/event-stream" } }))
    }
  })

  await page.goto("/chat/1")
  await page.getByTestId("chat-input").fill("Explain this")
  await page.getByTestId("send-button").click()

  // Preparing is announced by the stable send control rather than inserting
  // a transient row into the composer layout.
  await expect(page.getByTestId("send-button")).toHaveAttribute("aria-label", "正在准备消息…")
  await expect(page.getByText("正在回复…")).toHaveCount(0)
  await expect(page.getByText("Explain this")).toBeVisible()
  await expect(page.getByText("已接收，等待回复…")).toBeVisible()
  await expect(page.getByText("First paragraph")).toBeVisible()

  const turnPosition = await page.evaluate(() => {
    const scroller = document.querySelector<HTMLElement>("[data-chat-scroll-container]")
    const user = document.querySelector<HTMLElement>('[data-testid="message-item"][data-role="user"]')
    if (!scroller || !user) throw new Error("turn geometry unavailable")
    return (user.getBoundingClientRect().top - scroller.getBoundingClientRect().top) / scroller.clientHeight
  })
  expect(turnPosition).toBeGreaterThan(0.12)
  expect(turnPosition).toBeLessThan(0.4)

  const liveMarkdown = page.locator(".streaming-markdown .markdown-body")
  await expect(liveMarkdown.locator(":scope > *")).toHaveCount(2)
  await expect(liveMarkdown.locator(":scope > :last-child")).toHaveCSS("animation-name", "streaming-block-in")
  await expect(page.locator(".streaming-fade")).toHaveCount(0)

  await expect(page.getByText("Second paragraph")).toBeVisible()
  await expect(page.locator('[data-testid="message-item"][data-role="assistant"]')).toHaveCount(1)
  await expect(page.getByText("正在同步结果…")).toHaveCount(0)
})

test("clears only the submitted draft while preserving edits made during preparation", async ({ page }) => {
  let releasePreflight!: () => void
  const preflightReleased = new Promise<void>((resolve) => { releasePreflight = resolve })
  let historyRequests = 0
  await installBaseRoutes(page, () => {
    historyRequests += 1
    return historyRequests === 1 ? [] : [message(2, "assistant", "Accepted")]
  })
  await page.route("**/api/v1/sessions/1/messages/preflight", async (route) => {
    await preflightReleased
    await route.fulfill({ json: { status: "ok", needs_compaction: false } })
  })
  await page.route("**/api/v1/sessions/1/messages/stream", async (route) => {
    await route.fulfill({
      status: 200,
      headers: { "Content-Type": "text/event-stream" },
      body: 'event: message_complete\ndata: {"message_id":2,"finish_reason":"stop"}\n\n',
    })
  })

  await page.goto("/chat/1")
  const input = page.getByTestId("chat-input")
  await input.fill("original draft")
  const composer = page.getByTestId("composer-surface")
  const beforeSubmitHeight = Math.round((await composer.boundingBox())?.height || 0)
  await page.getByTestId("send-button").click()
  await expect(input).toHaveValue("")
  expect(Math.round((await composer.boundingBox())?.height || 0)).toBe(beforeSubmitHeight)

  // A user edit, including an Enter key while the request is preparing, is a
  // new draft and must survive the old request's accepted callback.
  await input.fill("new draft")
  await input.press("Enter")
  await expect(input).toHaveValue("new draft")
  releasePreflight()
  await expect(page.getByText("Accepted")).toBeVisible()
  await expect(input).toHaveValue("new draft")
})

test("retries the captured request without refilling or replacing a newer draft", async ({ page }) => {
  const streamBodies: Array<{ content?: string }> = []
  let historyRequests = 0
  await installBaseRoutes(page, () => {
    historyRequests += 1
    return historyRequests === 1 ? [] : [message(2, "assistant", "Retried answer")]
  })
  await page.route("**/api/v1/sessions/1/messages/preflight", async (route) => {
    await route.fulfill({ json: { status: "ok", needs_compaction: false } })
  })
  await page.route("**/api/v1/sessions/1/messages/stream", async (route) => {
    streamBodies.push((route.request().postDataJSON() || {}) as { content?: string })
    if (streamBodies.length === 1) {
      await route.fulfill({ status: 400, contentType: "application/json", body: JSON.stringify({ error: "bad request" }) })
      return
    }
    await route.fulfill({
      status: 200,
      headers: { "Content-Type": "text/event-stream" },
      body: 'event: message_complete\ndata: {"message_id":2,"finish_reason":"stop"}\n\n',
    })
  })

  await page.goto("/chat/1")
  const input = page.getByTestId("chat-input")
  await input.fill("original payload")
  await page.getByTestId("send-button").click()
  await expect(input).toHaveValue("")
  await expect(page.getByRole("button", { name: "重试" })).toBeVisible()

  await input.fill("keep this draft")
  await page.getByRole("button", { name: "重试" }).click()
  await expect.poll(() => streamBodies.length).toBe(2)
  expect(streamBodies[0]?.content).toBe("original payload")
  expect(streamBodies[1]?.content).toBe("original payload")
  await expect(input).toHaveValue("keep this draft")
  await expect(page.getByText("Retried answer")).toBeVisible()
})

test("replay gaps delay recovery feedback and settle without replaying the whole answer", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.emulateMedia({ reducedMotion: "reduce" })
  let recoveryStartedAt = Number.POSITIVE_INFINITY
  await installBaseRoutes(page, () => {
    return Date.now() < recoveryStartedAt + 1_025
      ? [message(1, "user", "Resume this")]
      : [message(1, "user", "Resume this"), message(2, "assistant", "Recovered prefix continued")]
  })
  await page.route("**/api/v1/sessions/1/runs/active", (route) => {
    recoveryStartedAt = Date.now()
    return route.fulfill({ json: { run: {
      run_id: "run-resume",
      session_id: 1,
      kind: "chat",
      user_message_id: 1,
      status: "running",
      cursor: 1,
      content: "Recovered prefix",
      thinking: "",
      output_truncated: false,
    } } })
  })
  await page.addInitScript(() => {
    const nativeFetch = window.fetch.bind(window)
    window.fetch = (input, init) => {
      const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url
      if (!url.includes("/runs/run-resume/resume")) return nativeFetch(input, init)
      const encoder = new TextEncoder()
      return Promise.resolve(new Response(new ReadableStream<Uint8Array>({
        start(controller) {
          controller.enqueue(encoder.encode('event: replay_gap\ndata: {"cursor":1}\n\n'))
          window.setTimeout(() => controller.enqueue(encoder.encode('event: content_delta\ndata: {"delta":" continued"}\n\n')), 900)
          window.setTimeout(() => {
            controller.enqueue(encoder.encode('event: message_complete\ndata: {"message_id":2,"finish_reason":"stop"}\n\n'))
            controller.close()
          }, 1050)
        },
      }), { status: 200, headers: { "Content-Type": "text/event-stream" } }))
    }
  })

  await page.goto("/chat/1")
  await expect(page.getByText("Recovered prefix")).toBeVisible()
  await expect(page.locator(".markdown-body > :last-child").filter({ hasText: "Recovered prefix" })).toHaveCSS("animation-name", "none")
  await expect(page.getByText("正在补全回答…")).toHaveCount(0)
  await expect(page.getByText("正在补全回答…")).toBeVisible({ timeout: 1_000 })
  await expect(page.getByText("Recovered prefix continued")).toBeVisible()
  await expect(page.getByText("已接续")).toBeVisible()
  await expect(page.locator('[data-testid="message-item"][data-role="assistant"]')).toHaveCount(1)
})
