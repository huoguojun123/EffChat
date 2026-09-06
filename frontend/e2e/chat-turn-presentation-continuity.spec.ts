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

async function installBaseRoutes(page: Page, messageWindow: () => ReturnType<typeof message>[], role: "user" | "admin" = "user") {
  await page.addInitScript(() => localStorage.setItem("token", "test-token"))
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (path === "/api/v1/users/me") return route.fulfill({ json: { id: 1, username: role === "admin" ? "admin" : "member", role, is_active: true } })
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
  await liveMarkdown.locator(".stream-reveal-text").first().waitFor({ state: "attached" })
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

test("restores or discards a failed submission without losing a newer draft", async ({ page }) => {
  await installBaseRoutes(page, () => [])
  await page.route("**/api/v1/sessions/1/messages/preflight", async (route) => {
    await route.fulfill({ json: { status: "ok", needs_compaction: false } })
  })
  await page.route("**/api/v1/sessions/1/messages/stream", async (route) => {
    await route.fulfill({ status: 400, contentType: "application/json", body: JSON.stringify({ error: "synthetic upstream rejection" }) })
  })

  await page.goto("/chat/1")
  const input = page.getByTestId("chat-input")
  await input.fill("failed payload")
  await page.getByTestId("send-button").click()
  await expect(page.getByRole("button", { name: "恢复编辑" })).toBeVisible()

  await input.fill("new draft to protect")
  let confirmationMessage = ""
  page.once("dialog", async (dialog) => {
    confirmationMessage = dialog.message()
    await dialog.accept()
  })
  await page.getByRole("button", { name: "恢复编辑" }).click()
  expect(confirmationMessage).toContain("已有新草稿")
  await expect(input).toHaveValue("failed payload")
  await expect.poll(() => page.evaluate(() => sessionStorage.getItem("effchat:session-drafts"))).toContain('"submissions":{}')

  await input.fill("failed again")
  await page.getByTestId("send-button").click()
  await expect(page.getByRole("button", { name: "放弃" })).toBeVisible()
  page.once("dialog", async (dialog) => dialog.accept())
  await page.getByRole("button", { name: "放弃" }).click()
  await expect(page.getByText("已放弃失败消息")).toBeVisible()
  await expect.poll(() => page.evaluate(() => sessionStorage.getItem("effchat:session-drafts"))).toBeNull()
})

test("turns an orphaned sending snapshot into a retryable failure after reload", async ({ page }) => {
  await installBaseRoutes(page, () => [])
  await page.route("**/api/v1/sessions/1/messages/preflight", async (route) => {
    await route.fulfill({ json: { status: "ok", needs_compaction: false } })
  })
  await page.route("**/api/v1/sessions/1/messages/stream", async (route) => {
    await route.fulfill({ status: 400, contentType: "application/json", body: JSON.stringify({ error: "synthetic upstream rejection" }) })
  })

  await page.goto("/chat/1")
  const input = page.getByTestId("chat-input")
  await input.fill("orphaned sending snapshot")
  await page.getByTestId("send-button").click()
  await expect(page.getByRole("button", { name: "重试" })).toBeVisible()
  await expect.poll(() => page.evaluate(() => sessionStorage.getItem("effchat:session-drafts"))).toContain('"status":"failed"')

  await page.reload()
  await expect(input).toHaveValue("")
  await expect(page.getByRole("button", { name: "重试" })).toBeVisible()
  await expect.poll(() => page.evaluate(() => sessionStorage.getItem("effchat:session-drafts"))).toContain('"status":"failed"')
})

test("remounting through the admin route keeps the composer send path usable", async ({ page }) => {
  let streamCalls = 0
  let historyRequests = 0
  await installBaseRoutes(page, () => {
    historyRequests += 1
    return historyRequests === 1 ? [] : [message(2, "assistant", "Admin route answer")]
  }, "admin")
  await page.route("**/api/v1/sessions/1/messages/preflight", async (route) => {
    await route.fulfill({ json: { status: "ok", needs_compaction: false } })
  })
  await page.route("**/api/v1/sessions/1/messages/stream", async (route) => {
    streamCalls += 1
    await route.fulfill({
      status: 200,
      headers: { "Content-Type": "text/event-stream" },
      body: 'event: message_complete\ndata: {"message_id":2,"finish_reason":"stop"}\n\n',
    })
  })

  await page.goto("/admin/models")
  await expect(page).toHaveURL(/\/admin\/models$/)
  await page.goto("/chat/1")
  await page.getByTestId("chat-input").fill("after admin remount")
  await page.getByTestId("send-button").click()

  await expect(page.getByText("Admin route answer")).toBeVisible()
  expect(streamCalls).toBe(1)
  await expect(page.getByTestId("chat-input")).toHaveValue("")
})

test("unknown delivery reconciles the existing run without sending a second request", async ({ page }) => {
  let streamCalls = 0
  let historyRequests = 0
  let runId = ""
  await installBaseRoutes(page, () => {
    historyRequests += 1
    return historyRequests === 1
      ? []
      : [
          { ...message(1, "user", "uncertain delivery"), message_data: { role: "user", content: "uncertain delivery" } },
          { ...message(2, "assistant", "reconciled answer"), message_data: { role: "assistant", content: "reconciled answer", metadata: { run_id: runId } } },
        ]
  })
  await page.route("**/api/v1/sessions/1/messages/preflight", async (route) => {
    await route.fulfill({ json: { status: "ok", needs_compaction: false } })
  })
  await page.route("**/api/v1/sessions/1/messages/stream", async (route) => {
    streamCalls += 1
    runId = (route.request().postDataJSON() as { client_run_id?: string } | null)?.client_run_id || "unknown-run"
    await route.abort("failed")
  })
  await page.route("**/api/v1/sessions/1/runs/*", async (route) => {
    const path = new URL(route.request().url()).pathname
    const statusRunId = path.match(/\/runs\/([^/]+)$/)?.[1]
    if (!statusRunId || statusRunId === "active") return route.fallback()
    runId = statusRunId
    await route.fulfill({ json: { run: { run_id: statusRunId, session_id: 1, kind: "chat", status: "completed", terminal_message_id: 2 } } })
  })

  await page.goto("/chat/1")
  await page.getByTestId("chat-input").fill("uncertain delivery")
  await page.getByTestId("send-button").click()

  await expect(page.getByText("reconciled answer")).toBeVisible({ timeout: 10_000 })
  await expect(page.getByTestId("chat-input")).toHaveValue("")
  expect(streamCalls).toBe(1)
  await expect(page.locator('[data-testid="message-item"][data-role="user"]')).toHaveCount(1)
  await expect(page.locator('[data-testid="message-item"][data-role="assistant"]')).toHaveCount(1)
})

test("Enter and the send button stay blocked while an attachment is uploading", async ({ page }) => {
  let uploadStarted!: () => void
  const uploadBegun = new Promise<void>((resolve) => { uploadStarted = resolve })
  let releaseUpload!: () => void
  const uploadReleased = new Promise<void>((resolve) => { releaseUpload = resolve })
  let preflightCalls = 0
  let streamCalls = 0
  await installBaseRoutes(page, () => [])
  await page.route("**/api/v1/sessions/1/messages/preflight", async (route) => {
    preflightCalls += 1
    await route.fulfill({ json: { status: "ok", needs_compaction: false } })
  })
  await page.route("**/api/v1/sessions/1/messages/stream", async (route) => {
    streamCalls += 1
    await route.fulfill({ status: 200, headers: { "Content-Type": "text/event-stream" }, body: "" })
  })
  await page.route("**/api/v1/files", async (route) => {
    if (route.request().method() !== "POST") return route.fallback()
    uploadStarted()
    await uploadReleased
    await route.fulfill({ status: 201, json: {
      id: 7,
      user_id: 1,
      session_id: 1,
      file_name: "pending.txt",
      file_type: "text/plain",
      file_size: 4,
      status: "active",
      extract_status: "ready",
    } })
  })

  await page.goto("/chat/1")
  await page.getByTestId("chat-input").fill("message with pending upload")
  await page.getByTestId("file-input").setInputFiles({ name: "pending.txt", mimeType: "text/plain", buffer: Buffer.from("test") })
  await uploadBegun
  await expect(page.getByTestId("send-button")).toBeDisabled()
  await page.getByTestId("chat-input").press("Enter")
  expect(preflightCalls).toBe(0)
  expect(streamCalls).toBe(0)
  releaseUpload()
  await expect(page.getByTestId("send-button")).toBeEnabled()
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
  await expect(page.locator(".streaming-markdown .stream-reveal-text").first()).toHaveCSS("animation-name", "none")
  await expect(page.getByText("正在补全回答…")).toHaveCount(0)
  await expect(page.getByText("正在补全回答…")).toBeVisible({ timeout: 1_000 })
  await expect(page.getByText("Recovered prefix continued").first()).toBeVisible()
  await expect(page.getByText("已接续")).toBeVisible()
  await expect(page.locator('[data-testid="message-item"][data-role="assistant"]')).toHaveCount(1)
})
