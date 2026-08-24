import { expect, test, type Page } from "@playwright/test"

const session = {
  id: 1,
  user_id: 1,
  title: "Retry presentation",
  title_generated: false,
  model_id: "demo-model",
  provider: "demo",
  created_at: "2026-08-24T00:00:00Z",
  updated_at: "2026-08-24T00:00:00Z",
}

function message(id: number, role: "user" | "assistant", content: string, metadata: Record<string, unknown> = {}) {
  return {
    id,
    session_id: 1,
    schema_version: "v2",
    role,
    has_tool_calls: false,
    has_reasoning: false,
    created_at: `2026-08-24T00:00:0${id}Z`,
    message_data: { role, content, metadata },
  }
}

function historyPayload(messages: ReturnType<typeof message>[]) {
  const turns = messages.filter((item) => item.role === "user")
  return {
    messages,
    first_turn_id: turns[0]?.id || 0,
    last_turn_id: turns.at(-1)?.id || 0,
    has_older: false,
    has_newer: false,
  }
}

async function installRoutes(page: Page, initial: ReturnType<typeof message>[]) {
  let messages = initial
  await page.addInitScript(() => localStorage.setItem("token", "test-token"))
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
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
      return route.fulfill({ json: historyPayload(messages) })
    }
    if (path === "/api/v1/sessions/1/turns") {
      return route.fulfill({ json: { turns: [], total: 0, has_more: false, next_before_turn_id: null } })
    }
    if (path === "/api/v1/sessions/1/runs/active") return route.fulfill({ json: { run: null } })
    if (path === "/api/v1/files/upload-limits") {
      return route.fulfill({ json: { max_file_size_mb: 20, max_session_files: 50, allowed_types: [] } })
    }
    if (path === "/api/v1/files") return route.fulfill({ json: { files: [], has_more: false, next_offset: 0 } })
    if (path === "/api/v1/sessions/1/messages/2/retry") {
      const retryRunId = url.searchParams.get("client_run_id") || "retry-run"
      messages = [
        message(1, "user", "Retry this question", { run_id: "original-run" }),
        message(3, "assistant", "Replacement answer", { run_id: retryRunId }),
      ]
      return route.fulfill({
        contentType: "text/event-stream",
        body: [
          'event: content_delta\ndata: {"delta":"Replacement answer"}\n\n',
          'event: message_complete\ndata: {"message_id":3,"finish_reason":"stop"}\n\n',
        ].join(""),
      })
    }
    return route.fulfill({ json: {} })
  })
}

test("accepted retry replaces the failed answer in its original turn", async ({ page }) => {
  await installRoutes(page, [
    message(1, "user", "Retry this question", { run_id: "original-run" }),
    message(2, "assistant", "", { error: true, error_detail: "Temporary failure", run_id: "original-run" }),
  ])
  await page.goto("/chat/1")

  await expect(page.getByText("Temporary failure")).toBeVisible()
  await page.getByRole("button", { name: "重试" }).click()
  await expect(page.getByText("Replacement answer")).toBeVisible()
  await expect(page.getByText("Temporary failure")).toHaveCount(0)

  const roles = await page.getByTestId("message-item").evaluateAll((items) => items.map((item) => item.getAttribute("data-role")))
  expect(roles).toEqual(["user", "assistant"])
  await expect(page.getByText("正在回复")).toHaveCount(0)
})

for (const viewport of [
  { name: "desktop", width: 1536, height: 864 },
  { name: "mobile", width: 390, height: 844 },
]) {
  test(`${viewport.name} keeps the latest answer above the composer`, async ({ page }) => {
    const body = Array.from({ length: 36 }, (_, index) => `Line ${index + 1}: stable reading content`).join("\n\n")
    await page.setViewportSize({ width: viewport.width, height: viewport.height })
    await installRoutes(page, [message(1, "user", "Long answer"), message(2, "assistant", body)])
    await page.goto("/chat/1")
    await expect(page.getByText("Line 36: stable reading content")).toBeVisible()

    const geometry = await page.evaluate(() => {
      const latest = document.querySelector<HTMLElement>('[data-testid="message-item"][data-role="assistant"]')
      const composer = document.querySelector<HTMLElement>('[data-testid="chat-composer-dock"]')
      const scroller = document.querySelector<HTMLElement>("[data-chat-scroll-container]")
      if (!latest || !composer || !scroller) throw new Error("chat geometry unavailable")
      return {
        gap: composer.getBoundingClientRect().top - latest.getBoundingClientRect().bottom,
        distance: scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight,
        expectedGap: Number.parseFloat(getComputedStyle(document.documentElement).getPropertyValue("--chat-scroll-gap")),
      }
    })

    expect(geometry.gap).toBeGreaterThanOrEqual(geometry.expectedGap - 1)
    expect(geometry.distance).toBeLessThanOrEqual(2)
  })
}
