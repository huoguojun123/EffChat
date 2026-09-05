import { expect, test, type BrowserContext, type Page, type Route } from "@playwright/test"

const sessions = [
  {
    id: 1,
    user_id: 1,
    title: "Session One",
    title_generated: false,
    model_id: "demo-model",
    provider: "demo",
    created_at: "2026-07-25T00:00:00Z",
    updated_at: "2026-07-25T00:00:00Z",
  },
  {
    id: 2,
    user_id: 1,
    title: "Session Two",
    title_generated: false,
    model_id: "demo-model",
    provider: "demo",
    created_at: "2026-07-25T00:00:01Z",
    updated_at: "2026-07-25T00:00:01Z",
  },
]

function message(id: number, sessionId: number, role: "user" | "assistant", content: string, runId?: string) {
  return {
    id,
    session_id: sessionId,
    schema_version: "v2",
    role,
    has_tool_calls: false,
    has_reasoning: false,
    created_at: "2026-07-25T00:01:00Z",
    message_data: {
      role,
      content,
      ...(runId ? { metadata: { run_id: runId } } : {}),
    },
  }
}

type RouteOverride = (route: Route, path: string) => Promise<boolean>

function isHistoryPath(path: string, sessionId: number) {
  return path === `/api/v1/sessions/${sessionId}/messages`
    || path === `/api/v1/sessions/${sessionId}/message-window`
}

function historyPayload(messages: ReturnType<typeof message>[]) {
  const turns = messages.filter((item) => item.role === "user")
  return {
    messages,
    has_more: false,
    first_turn_id: turns[0]?.id || 0,
    last_turn_id: turns.at(-1)?.id || 0,
    has_older: false,
    has_newer: false,
  }
}

async function installChatRoutes(context: BrowserContext, override?: RouteOverride) {
  await context.addInitScript(() => localStorage.setItem("token", "test-token"))
  await context.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname
    if (override && await override(route, path)) return
    if (path === "/api/v1/users/me") return route.fulfill({ json: { id: 1, username: "member", role: "user", is_active: true } })
    if (path === "/api/v1/system/info") return route.fulfill({ json: { system_name: "EffChat" } })
    if (path === "/api/v1/models") {
      return route.fulfill({ json: { models: [{ id: "demo-model", provider: "demo", display_name: "Demo", enabled: true, sort_order: 1 }], total: 1 } })
    }
    if (path === "/api/v1/sessions") return route.fulfill({ json: { sessions, has_more: false, next_offset: 0 } })
    if (path === "/api/v1/session-folders") return route.fulfill({ json: { folders: [] } })
    if (path === "/api/v1/sessions/1") return route.fulfill({ json: sessions[0] })
    if (path === "/api/v1/sessions/2") return route.fulfill({ json: sessions[1] })
    if (isHistoryPath(path, 1)) {
      return route.fulfill({ json: historyPayload([message(1, 1, "user", "session-one-message")]) })
    }
    if (isHistoryPath(path, 2)) {
      return route.fulfill({ json: historyPayload([message(2, 2, "user", "session-two-message")]) })
    }
    if (/\/api\/v1\/sessions\/\d+\/turns$/.test(path)) {
      return route.fulfill({ json: { turns: [], total: 0, has_more: false, next_before_turn_id: null } })
    }
    if (/\/api\/v1\/sessions\/\d+\/runs\/active$/.test(path)) return route.fulfill({ json: { run: null } })
    if (path === "/api/v1/files/upload-limits") {
      return route.fulfill({ json: { max_file_size_mb: 20, max_session_files: 50, allowed_types: [] } })
    }
    if (path === "/api/v1/files") return route.fulfill({ json: { files: [], has_more: false, next_offset: 0 } })
    return route.fulfill({ json: {} })
  })
}

test("a transient startup outage preserves the token and can recover", async ({ context, page }) => {
  let userChecks = 0
  let unavailable = true
  await installChatRoutes(context, async (route, path) => {
    if (path !== "/api/v1/users/me") return false
    userChecks += 1
    if (unavailable) await route.abort("failed")
    else await route.fulfill({ json: { id: 1, username: "member", role: "user", is_active: true } })
    return true
  })

  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/chat/1")

  await expect(page.getByRole("alert")).toContainText("暂时无法连接")
  expect(await page.evaluate(() => localStorage.getItem("token"))).toBe("test-token")
  unavailable = false
  await page.getByRole("button", { name: "重新连接" }).click()

  await expect(page.getByText("session-one-message")).toBeVisible()
  await expect(page).toHaveURL(/\/chat\/1$/)
  expect(userChecks).toBeGreaterThanOrEqual(2)
})

test("390px foreground return rechecks the active run without losing the current session", async ({ context, page }) => {
  const activeChecks = new Map<Page, number>()
  await installChatRoutes(context, async (route, path) => {
    if (path !== "/api/v1/sessions/1/runs/active") return false
    const owner = route.request().frame().page()
    activeChecks.set(owner, (activeChecks.get(owner) || 0) + 1)
    await route.fulfill({ json: { run: null } })
    return true
  })
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/chat/1")
  await expect(page.getByText("session-one-message")).toBeVisible()
  await expect.poll(() => activeChecks.get(page) || 0).toBeGreaterThan(0)
  const before = activeChecks.get(page) || 0

  await page.evaluate(() => {
    Object.defineProperty(document, "visibilityState", { configurable: true, value: "hidden" })
    document.dispatchEvent(new Event("visibilitychange"))
  })
  await expect(page.getByText("session-one-message")).toBeVisible()
  await page.evaluate(() => {
    Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" })
    document.dispatchEvent(new Event("visibilitychange"))
  })
  await expect.poll(() => activeChecks.get(page) || 0).toBeGreaterThan(before)
  await expect(page.getByText("session-one-message")).toBeVisible()
  await expect(page).toHaveURL(/\/chat\/1$/)
})

test("mobile chrome keeps high-frequency controls reachable through the 768px breakpoint", async ({ context, page }) => {
  await installChatRoutes(context)
  for (const width of [320, 390, 700, 768]) {
    await page.setViewportSize({ width, height: 844 })
    await page.goto("/chat/1")
    await expect(page.getByText("session-one-message")).toBeVisible()
    await page.evaluate(() => document.fonts.ready)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    for (const button of [
      page.getByRole("button", { name: "打开侧边栏" }),
      page.getByRole("button", { name: "文件", exact: true }),
      page.getByTestId("composer-toolbar").getByRole("button", { name: "更多", exact: true }),
    ]) {
      const box = await button.boundingBox()
      expect(box).not.toBeNull()
      // The painted button stays compact; test the real extended hit area,
      // including its corners, rather than mistaking layout bounds for hit bounds.
      await expect.poll(() => button.evaluate((element) => {
        const box = element.getBoundingClientRect()
        return [-21, 21].every((dx) => [-21, 21].every((dy) => {
          const hit = document.elementFromPoint(box.x + box.width / 2 + dx, box.y + box.height / 2 + dy)
          return hit === element || (hit !== null && element.contains(hit))
        }))
      }), { message: await button.getAttribute("aria-label") || "mobile hit target" }).toBe(true)
    }
  }
})

test("a late active-run lookup cannot take over after switching sessions", async ({ context, page }) => {
  let releaseLookup!: () => void
  const lookupBlocked = new Promise<void>((resolve) => { releaseLookup = resolve })
  let lookupStarted!: () => void
  const activeRequested = new Promise<void>((resolve) => { lookupStarted = resolve })
  let staleResumeCalls = 0
  await installChatRoutes(context, async (route, path) => {
    if (path === "/api/v1/sessions/1/runs/active") {
      lookupStarted()
      await lookupBlocked
      await route.fulfill({
        json: {
          run: { run_id: "stale-run", kind: "chat", status: "running", cursor: 0, content: "stale-output", thinking: "", tool_calls: [] },
        },
      }).catch(() => undefined)
      return true
    }
    if (path.includes("/runs/stale-run/resume")) {
      staleResumeCalls += 1
      await route.fulfill({ contentType: "text/event-stream", body: "event: message_complete\ndata: {\"finish_reason\":\"stop\"}\n\n" })
      return true
    }
    return false
  })
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/chat/1")
  await activeRequested
  await page.getByRole("button", { name: "打开侧边栏" }).click()
  await page.getByText("Session Two", { exact: true }).click()
  releaseLookup()

  await expect(page).toHaveURL(/\/chat\/2$/)
  await expect(page.getByText("session-two-message")).toBeVisible()
  await expect(page.getByText("stale-output")).toHaveCount(0)
  await expect.poll(() => staleResumeCalls).toBe(0)
})

test("two tabs can independently recover the same durable run without duplicate answers", async ({ context, page }) => {
  const runId = "shared-run"
  const activePages = new Set<Page>()
  let releaseActive!: () => void
  const bothTabsActive = new Promise<void>((resolve) => { releaseActive = resolve })
  let durable = false
  let resumeCalls = 0
  await installChatRoutes(context, async (route, path) => {
    if (path === "/api/v1/sessions/1/runs/active") {
      if (durable) {
        await route.fulfill({ json: { run: null } })
        return true
      }
      activePages.add(route.request().frame().page())
      if (activePages.size >= 2) releaseActive()
      await bothTabsActive
      await route.fulfill({
        json: { run: { run_id: runId, kind: "chat", status: "running", cursor: 0, content: "", thinking: "", tool_calls: [] } },
      })
      return true
    }
    if (path === `/api/v1/sessions/1/runs/${runId}/resume`) {
      resumeCalls += 1
      durable = true
      await route.fulfill({
        contentType: "text/event-stream",
        body: "event: content_delta\ndata: {\"delta\":\"shared-answer\"}\n\nevent: message_complete\ndata: {\"finish_reason\":\"stop\"}\n\n",
      })
      return true
    }
    if (isHistoryPath(path, 1)) {
      const messages = [message(1, 1, "user", "shared-question")]
      if (durable) messages.push(message(2, 1, "assistant", "shared-answer", runId))
      await route.fulfill({ json: historyPayload(messages) })
      return true
    }
    return false
  })
  await page.setViewportSize({ width: 390, height: 844 })
  const second = await context.newPage()
  await second.setViewportSize({ width: 390, height: 844 })
  await Promise.all([page.goto("/chat/1"), second.goto("/chat/1")])

  await expect.poll(() => resumeCalls).toBeGreaterThanOrEqual(2)
  for (const tab of [page, second]) {
    await expect(tab.getByText("shared-answer")).toBeVisible()
    await expect(tab.getByText("shared-answer")).toHaveCount(1)
    await expect(tab).toHaveURL(/\/chat\/1$/)
  }
})

test("a second tab restores the accepted user turn before rendering a live assistant", async ({ context, page }) => {
  const runId = "other-tab-run"
  const acceptedUser = message(10, 1, "user", "sent-from-the-first-tab")
  let second: Page | null = null
  let secondHistoryRequests = 0

  await installChatRoutes(context, async (route, path) => {
    const owner = route.request().frame().page()
    if (isHistoryPath(path, 1)) {
      if (owner !== second) {
        await route.fulfill({ json: historyPayload([acceptedUser]) })
        return true
      }
      secondHistoryRequests += 1
      await route.fulfill({
        json: historyPayload(secondHistoryRequests === 1 ? [] : [acceptedUser]),
      })
      return true
    }
    if (path === "/api/v1/sessions/1/runs/active") {
      await route.fulfill({
        json: {
          run: owner === second
            ? { run_id: runId, session_id: 1, user_message_id: 10, kind: "chat", status: "running", cursor: 0, content: "", thinking: "", tool_calls: [] }
            : null,
        },
      })
      return true
    }
    if (path === `/api/v1/sessions/1/runs/${runId}/resume`) {
      await route.fulfill({
        contentType: "text/event-stream",
        body: 'event: content_delta\ndata: {"delta":"assistant-is-live"}\n\n',
      })
      return true
    }
    return false
  })

  await page.goto("/chat/1")
  await expect(page.getByText("sent-from-the-first-tab")).toBeVisible()

  second = await context.newPage()
  await second.goto("/chat/1")

  await expect(second.getByText("assistant-is-live")).toBeVisible()
  await expect(second.getByText("sent-from-the-first-tab")).toBeVisible()
  expect(secondHistoryRequests).toBeGreaterThanOrEqual(2)
})

test("PWA update failure returns the refresh action instead of spinning forever", async ({ page }) => {
  test.skip(process.env.E2E_PWA_PRODUCTION !== "1", "requires the production PWA bundle")
  await page.addInitScript(() => {
    const waitingWorker = {
      scriptURL: "/sw.js",
      state: "installed",
      addEventListener() {},
      postMessage() {},
    }
    const registration = {
      installing: null,
      waiting: waitingWorker,
      active: waitingWorker,
      addEventListener() {},
      update: () => Promise.reject(new Error("mock update failure")),
    }
    const serviceWorker = {
      controller: waitingWorker,
      addEventListener() {},
      removeEventListener() {},
      register: () => Promise.resolve(registration),
      getRegistration: () => Promise.resolve(registration),
      ready: Promise.resolve(registration),
    }
    Object.defineProperty(Navigator.prototype, "serviceWorker", {
      configurable: true,
      get: () => serviceWorker,
    })
  })
  await page.route("**/api/v1/system/info", (route) => route.fulfill({ json: { system_name: "EffChat" } }))
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/login")
  const prompt = page.getByRole("status").filter({ hasText: "新版本已准备好" })
  await expect(prompt).toBeVisible()
  await prompt.getByRole("button", { name: "更新" }).click()
  await expect(prompt.getByRole("button", { name: "更新中" })).toBeDisabled()
  await expect(prompt.getByRole("button", { name: "更新" })).toBeEnabled({ timeout: 6_000 })
})
