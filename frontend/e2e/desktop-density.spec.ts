import { expect, test, type Page } from "@playwright/test"

const session = {
  id: 1,
  user_id: 1,
  title: "桌面密度回归",
  title_generated: false,
  model_id: "density-model",
  provider: "fixture",
  created_at: "2026-08-16T00:00:00Z",
  updated_at: "2026-08-16T00:00:00Z",
}

const longSession = {
  ...session,
  id: 2,
  title: "这是一个用于验证侧栏标题有效宽度的较长会话标题",
  created_at: "2026-08-16T00:02:00Z",
  updated_at: "2026-08-16T00:02:00Z",
}

const messages = [
  {
    id: 1,
    session_id: 1,
    schema_version: "v1",
    role: "user",
    has_tool_calls: false,
    has_reasoning: false,
    created_at: "2026-08-16T00:01:00Z",
    message_data: { role: "user", content: "检查桌面密度" },
  },
  {
    id: 2,
    session_id: 1,
    schema_version: "v1",
    role: "assistant",
    has_tool_calls: true,
    has_reasoning: true,
    created_at: "2026-08-16T00:01:01Z",
    answer_navigation: {
      attempt_id: 2,
      attempt_number: 2,
      attempt_count: 2,
      previous_attempt_id: 1,
      can_switch: true,
    },
    message_data: {
      role: "assistant",
      content: "桌面密度回归内容。",
      runtime: { duration_ms: 1200, tokens_per_second: 12.3 },
      segments: [
        { thinking: "读取页面结构并检查共享 token。" },
        {
          tool_calls: [{
            id: "density-tool",
            name: "web_search",
            status: "done",
            arguments: JSON.stringify({ query: "desktop density" }),
            result: JSON.stringify({ source: "fixture", citations: [] }),
          }],
        },
        { content: "桌面密度回归内容。" },
      ],
    },
  },
]

async function installRoutes(page: Page, options: { theme?: "light" | "dark"; sidebarWidth?: number; sessions?: typeof session[] } = {}) {
  await page.addInitScript((settings) => {
    localStorage.setItem("token", "density-fixture-token")
    localStorage.setItem("sidebar_open", "true")
    if (settings.theme) localStorage.setItem("theme", settings.theme)
    if (settings.sidebarWidth !== undefined && localStorage.getItem("sidebar_width") === null) {
      localStorage.setItem("sidebar_width", String(settings.sidebarWidth))
    }
  }, options)
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (path === "/api/v1/users/me") {
      return route.fulfill({ json: { id: 1, username: "fixture-admin", nickname: "Fixture Admin", email: "admin@example.invalid", role: "admin", is_active: true } })
    }
    if (path === "/api/v1/system/info") return route.fulfill({ json: { system_name: "EffChat", version: "fixture" } })
    if (path === "/api/v1/models") return route.fulfill({ json: { models: [{ id: "density-model", provider: "fixture", display_name: "Density Model", enabled: true, sort_order: 1 }], total: 1 } })
    if (path === "/api/v1/sessions") return route.fulfill({ json: { sessions: options.sessions ?? [session], has_more: false, next_offset: 0 } })
    if (path === "/api/v1/session-folders") return route.fulfill({ json: { folders: [] } })
    if (path === "/api/v1/sessions/1") return route.fulfill({ json: session })
    if (path === "/api/v1/sessions/1/messages" || path === "/api/v1/sessions/1/message-window") {
      return route.fulfill({ json: { messages, has_more: false, has_older: false, has_newer: false, first_turn_id: 1, last_turn_id: 1 } })
    }
    if (path === "/api/v1/sessions/1/turns") return route.fulfill({ json: { turns: [], total: 0, has_more: false, next_before_turn_id: null } })
    if (path === "/api/v1/files/upload-limits") return route.fulfill({ json: { max_file_size_mb: 20, max_session_files: 50, allowed_types: [] } })
    if (path === "/api/v1/files") return route.fulfill({ json: { files: [], has_more: false, next_offset: 0 } })
    if (path === "/api/v1/admin/channels") return route.fulfill({ json: { channels: [], total: 0 } })
    if (path === "/api/v1/admin/groups") return route.fulfill({ json: { groups: [], total: 0 } })
    if (path === "/api/v1/admin/users") return route.fulfill({ json: { users: [{ id: 1, username: "fixture-admin", nickname: "Fixture Admin", role: "admin", is_active: true }], total: 1 } })
    return route.fulfill({ json: {} })
  })
}

async function waitForFonts(page: Page) {
  await page.evaluate(() => document.fonts?.ready)
  await page.waitForLoadState("networkidle")
}

async function computed(page: Page, selector: string, property: string) {
  return page.locator(selector).first().evaluate((element, name) => getComputedStyle(element).getPropertyValue(name).trim(), property)
}

function contrastRatio(foreground: string, background: string) {
  const parse = (value: string) => {
    const match = value.trim().match(/^#([0-9a-f]{6})$/i)
    if (!match) throw new Error(`Expected a six-digit hex color, got ${value}`)
    return match[1].match(/../g)!.map((part) => Number.parseInt(part, 16) / 255).map((channel) => channel <= 0.03928 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4)
  }
  const luminance = (value: string) => {
    const [r, g, b] = parse(value)
    return 0.2126 * r + 0.7152 * g + 0.0722 * b
  }
  const [high, low] = [luminance(foreground), luminance(background)].sort((a, b) => b - a)
  return (high + 0.05) / (low + 0.05)
}

async function assertNoSmallChromeText(page: Page, selectors: string[]) {
  const offenders = await page.evaluate((rootSelectors) => {
    const result: string[] = []
    for (const selector of rootSelectors) {
      for (const root of document.querySelectorAll<HTMLElement>(selector)) {
        const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT)
        let node: Node | null
        while ((node = walker.nextNode())) {
          const text = node.textContent?.replace(/\s+/g, " ").trim()
          const element = node.parentElement
          if (!text || !element || element.closest(".markdown-body, [aria-hidden='true'], .sr-only")) continue
          const style = getComputedStyle(element)
          if (style.display === "none" || style.visibility === "hidden" || Number(style.opacity) === 0) continue
          if (Number.parseFloat(style.fontSize) < 12) result.push(`${text.slice(0, 40)} (${style.fontSize})`)
        }
      }
    }
    return result
  }, selectors)
  expect(offenders, `visible chrome text below 12px: ${offenders.join(", ")}`).toEqual([])
}

async function assertChatDensity(page: Page, expected: { spacing: string; sidebar: number; composer: number; contentWidth: number }) {
  await expect(page.locator('[data-testid="message-item"][data-role="assistant"]')).toBeVisible()
  await waitForFonts(page)

  expect(await page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue("--spacing").trim())).toBe(expected.spacing)
  expect(await page.locator('[aria-label="侧边栏"]').boundingBox()).not.toBeNull()
  expect(Math.round((await page.locator('[aria-label="侧边栏"]').boundingBox())?.width || 0)).toBe(expected.sidebar)
  expect(Math.round((await page.getByTestId("chat-input").boundingBox())?.height || 0)).toBe(expected.composer)
  expect(Math.round((await page.getByTestId("message-list").boundingBox())?.width || 0)).toBe(expected.contentWidth)
  const composerWidth = Math.round((await page.getByTestId("composer-surface").boundingBox())?.width || 0)
  expect(composerWidth).toBeLessThan(expected.contentWidth)
  expect(composerWidth).toBeGreaterThan(expected.contentWidth - 40)
  const modelSelector = page.getByRole("combobox", { name: /当前模型/ })
  const modelWidth = Math.round((await modelSelector.boundingBox())?.width || 0)
  expect(modelWidth).toBeGreaterThan(120)
  expect(modelWidth).toBeLessThanOrEqual(320)
  expect(await page.getByTestId("chat-composer-dock").evaluate((element) => getComputedStyle(element).backgroundColor)).toBe("rgba(0, 0, 0, 0)")
  expect(await page.getByText("桌面密度回归内容。", { exact: true }).evaluate((element) => getComputedStyle(element).fontSize)).toBe("15px")
  expect(await page.getByTestId("chat-input").evaluate((element) => getComputedStyle(element).fontSize)).toBe("15px")
  expect(await page.getByText("桌面密度回归内容。", { exact: true }).evaluate((element) => getComputedStyle(element).fontFamily)).toContain("Plus Jakarta Sans")
  expect(await page.getByTestId("composer-surface").evaluate((element) => getComputedStyle(element).backdropFilter)).toBe("none")
  const messageSurfaces = await page.evaluate(() => {
    const user = document.querySelector<HTMLElement>('[data-testid="user-message-surface"]')
    const assistant = document.querySelector<HTMLElement>('[data-testid="message-item"][data-role="assistant"] .markdown-body')
    return {
      user: user ? getComputedStyle(user).backgroundColor : "",
      assistant: assistant ? getComputedStyle(assistant).backgroundColor : "",
    }
  })
  expect(messageSurfaces.user).not.toBe("")
  expect(messageSurfaces.user).not.toBe("rgba(0, 0, 0, 0)")
  expect(messageSurfaces.user).not.toBe(messageSurfaces.assistant)
  await assertNoSmallChromeText(page, ["aside", '[data-testid="composer-toolbar"]'])

  const reasoning = page.getByRole("button", { name: /思考过程与工具调用/ })
  await expect(reasoning).toBeVisible()
  expect(await reasoning.locator(".text-xs").first().evaluate((element) => getComputedStyle(element).fontSize)).toBe("12px")
  await reasoning.click()
  await expect(page.getByRole("button", { name: /联网搜索/ })).toBeVisible()

  const usageToggle = page.getByRole("button", { name: /12\.3\/s/ })
  await expect(usageToggle).toBeVisible()
  await usageToggle.click()
  await expect(page.getByText("12.3 token/秒", { exact: true })).toBeVisible()
  await expect(page.getByText("1.2秒", { exact: true })).toBeVisible()
  await page.getByText("桌面密度回归内容。", { exact: true }).click()
  await expect(page.getByText("12.3 token/秒", { exact: true })).toHaveCount(0)
  await usageToggle.click()
  await expect(page.getByText("12.3 token/秒", { exact: true })).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(page.getByText("12.3 token/秒", { exact: true })).toHaveCount(0)
  await expect(usageToggle).toBeFocused()

  await page.getByRole("button", { name: /Fixture Admin/ }).click()
  const slider = page.getByRole("slider", { name: "对话字号" })
  await slider.fill("8")
  await expect.poll(() => page.locator(".markdown-body").last().evaluate((element) => getComputedStyle(element).fontSize)).toBe("18.75px")
  expect(await computed(page, '[aria-label="侧边栏"]', "font-size")).toBe("16px")

  await page.getByRole("button", { name: "设置", exact: true }).click()
  const dialog = page.getByRole("dialog", { name: "设置" })
  await expect(dialog).toBeVisible()
  await assertNoSmallChromeText(page, ["[role='dialog'][data-state='open']"])
  return dialog
}

test("standard and compact desktop density keep chat chrome consistent", async ({ browser }) => {
  const cases = [
    { name: "standard", viewport: { width: 1920, height: 1080 }, deviceScaleFactor: 1, spacing: "0.25rem", sidebar: 280, composer: 54, contentWidth: 1180 },
    { name: "125-percent compact", viewport: { width: 1536, height: 864 }, deviceScaleFactor: 1.25, spacing: "0.2375rem", sidebar: 246, composer: 50, contentWidth: 1120 },
    { name: "low-height compact", viewport: { width: 1366, height: 768 }, deviceScaleFactor: 1, spacing: "0.2375rem", sidebar: 240, composer: 50, contentWidth: 1120 },
  ]

  for (const density of cases) {
    const context = await browser.newContext({ viewport: density.viewport, deviceScaleFactor: density.deviceScaleFactor })
    const page = await context.newPage()
    const errors: string[] = []
    page.on("pageerror", (error) => errors.push(error.message))
    page.on("console", (message) => { if (message.type() === "error") errors.push(message.text()) })
    await installRoutes(page)
    await page.goto("/chat/1")
    const dialog = await assertChatDensity(page, density)
    const expectedDialog = density.name === "standard" ? 600 : 560
    await expect.poll(async () => Math.round((await dialog.boundingBox())?.width || 0)).toBe(expectedDialog)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    expect(errors).toEqual([])
    await context.close()
  }
})

test("inactive session actions do not reserve sidebar title width", async ({ page }) => {
  await installRoutes(page, { sessions: [session, longSession] })
  await page.goto("/chat/1")
  await expect(page.getByTestId("session-row-2")).toBeVisible()
  await waitForFonts(page)

  const row = page.getByTestId("session-row-2")
  const title = row.getByRole("button", { name: longSession.title })
  const actions = row.getByTestId("session-actions")
  await expect(actions).toBeHidden()
  expect(await actions.evaluate((element) => getComputedStyle(element).pointerEvents)).toBe("none")

  const titleWidthBeforeFocus = (await title.boundingBox())?.width ?? 0
  const rowWidth = (await row.boundingBox())?.width ?? 0
  expect(titleWidthBeforeFocus).toBeGreaterThan(rowWidth - 40)

  await title.focus()
  await expect(actions).toBeVisible()
  expect(await actions.evaluate((element) => getComputedStyle(element).pointerEvents)).toBe("auto")
})

test("mobile chat chrome keeps equal top controls and a bottom breathing room", async ({ browser }) => {
  for (const viewport of [{ width: 390, height: 844 }, { width: 700, height: 844 }]) {
    const context = await browser.newContext({ viewport })
    const page = await context.newPage()
    await installRoutes(page)
    await page.goto("/chat/1")
    await expect(page.getByTestId("message-item").first()).toBeVisible()
    await waitForFonts(page)

    const topbar = page.getByTestId("chat-topbar")
    const topControls = [
      page.getByRole("button", { name: /侧边栏/ }),
      page.getByRole("button", { name: "文件" }),
      page.getByRole("button", { name: "更多会话操作" }),
      page.getByRole("combobox", { name: /当前模型/ }),
    ]
    for (const control of topControls) {
      await expect(control).toBeVisible()
      expect(Math.round((await control.boundingBox())?.height || 0)).toBe(32)
    }
    expect(await topbar.evaluate((element) => getComputedStyle(element).backdropFilter)).not.toBe("none")
    expect(await topbar.evaluate((element) => getComputedStyle(element).borderBottomWidth)).toBe("0px")
    expect(await topbar.evaluate((element) => getComputedStyle(element, "::after").content)).toBe('""')
    expect(Number.parseFloat(await page.getByTestId("chat-composer-dock").evaluate((element) => getComputedStyle(element).paddingBottom))).toBeGreaterThanOrEqual(8)
    const assistantActions = page.getByTestId("assistant-actions")
    const usage = page.getByTestId("assistant-usage")
    await expect(assistantActions).toBeVisible()
    await expect(usage).toBeVisible()
    const actionsBox = await assistantActions.boundingBox()
    const usageBox = await usage.boundingBox()
    expect(Math.abs((actionsBox?.y ?? 0) - (usageBox?.y ?? 0))).toBeLessThanOrEqual(1)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    await context.close()
  }
})

test("protected pages expose a keyboard skip link to the main content", async ({ page }) => {
  await installRoutes(page)
  await page.goto("/chat/1")
  await expect(page.locator('[data-testid="message-item"][data-role="assistant"]')).toBeVisible()
  await waitForFonts(page)

  const skipLink = page.getByRole("link", { name: "跳到主要内容" })
  await page.keyboard.press("Tab")
  await expect(skipLink).toBeFocused()
  await skipLink.press("Enter")
  await expect(page.locator("main#main-content")).toBeFocused()
})

test("admin density stays readable and mobile touch targets do not shrink", async ({ browser }) => {
  const desktop = await browser.newContext({ viewport: { width: 1536, height: 864 }, deviceScaleFactor: 1.25 })
  const desktopPage = await desktop.newPage()
  await installRoutes(desktopPage)
  await desktopPage.goto("/admin/models")
  await waitForFonts(desktopPage)
  const navigation = desktopPage.getByRole("navigation", { name: "管理后台导航" }).first()
  expect(Math.round((await navigation.boundingBox())?.width || 0)).toBe(200)
  await assertNoSmallChromeText(desktopPage, ["header", "nav", "main"])
  await desktopPage.getByRole("button", { name: "用户", exact: true }).click()
  await expect(desktopPage.getByRole("heading", { name: "用户" })).toBeVisible()
  expect(await desktopPage.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await desktop.close()

  const mobile = await browser.newContext({ viewport: { width: 390, height: 844 } })
  const mobilePage = await mobile.newPage()
  await installRoutes(mobilePage)
  await mobilePage.goto("/admin/models")
  await waitForFonts(mobilePage)
  expect(await mobilePage.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue("--spacing").trim())).toBe("0.25rem")
  for (const label of ["返回聊天", "刷新当前页面", "打开管理导航"]) {
    const box = await mobilePage.getByRole("button", { name: label }).boundingBox()
    expect(box?.width || 0).toBeGreaterThanOrEqual(32)
    expect(box?.height || 0).toBeGreaterThanOrEqual(32)
  }
  expect(await mobilePage.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await mobile.close()
})

test("mobile composer keeps its primary controls touchable", async ({ browser }) => {
  const mobile = await browser.newContext({ viewport: { width: 390, height: 844 } })
  const page = await mobile.newPage()
  await installRoutes(page)
  await page.goto("/chat/1")
  await expect(page.locator('[data-testid="message-item"][data-role="assistant"]')).toBeVisible()
  await waitForFonts(page)

  for (const button of await page.getByTestId("composer-toolbar").getByRole("button").all()) {
    const box = await button.boundingBox()
    expect(box?.width || 0).toBeGreaterThanOrEqual(32)
    expect(box?.height || 0).toBeGreaterThanOrEqual(32)
  }
  const send = await page.getByTestId("send-button").boundingBox()
  expect(send?.width || 0).toBeGreaterThanOrEqual(32)
  expect(send?.height || 0).toBeGreaterThanOrEqual(32)
  expect(await page.getByTestId("composer-surface").evaluate((element) => getComputedStyle(element).backdropFilter)).toBe("none")

  await mobile.close()
})

test("composer and message surfaces stay distinct in light and dark themes", async ({ browser }) => {
  for (const theme of ["light", "dark"] as const) {
    const context = await browser.newContext({ viewport: { width: 1536, height: 864 }, deviceScaleFactor: 1.25 })
    const page = await context.newPage()
    await installRoutes(page, { theme })
    await page.goto("/chat/1")
    await expect(page.locator('[data-testid="message-item"][data-role="assistant"]')).toBeVisible()
    await waitForFonts(page)

    expect(await page.locator("html").evaluate((element) => element.classList.contains("dark"))).toBe(theme === "dark")
    expect(await page.getByTestId("composer-surface").evaluate((element) => getComputedStyle(element).backdropFilter)).toBe("none")
    const surfaces = await page.evaluate(() => {
      const user = document.querySelector<HTMLElement>('[data-testid="user-message-surface"]')
      const assistant = document.querySelector<HTMLElement>('[data-testid="message-item"][data-role="assistant"] .markdown-body')
      const composer = document.querySelector<HTMLElement>('[data-testid="composer-surface"]')
      return {
        user: user ? getComputedStyle(user).backgroundColor : "",
        assistant: assistant ? getComputedStyle(assistant).backgroundColor : "",
        composer: composer ? getComputedStyle(composer).backgroundColor : "",
      }
    })
    expect(surfaces.user).not.toBe("")
    expect(surfaces.composer).not.toBe("")
    expect(surfaces.user).not.toBe(surfaces.assistant)
    expect(surfaces.composer).not.toBe(surfaces.assistant)
    const statusColor = await page.locator(".status-success-solid").first().evaluate((element) => getComputedStyle(element).color)
    const statusToken = await page.locator("html").evaluate((element) => getComputedStyle(element).getPropertyValue("--status-success-solid").trim())
    expect(statusColor).not.toBe("")
    expect(statusToken).toMatch(/^#(?:[0-9a-f]{3}|[0-9a-f]{6})$/i)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)

    await context.close()
  }
})

test("chat chrome controls share the file surface contract", async ({ page }) => {
  await installRoutes(page, { theme: "dark" })
  await page.setViewportSize({ width: 1536, height: 864 })
  await page.goto("/chat/1")
  await expect(page.locator('[data-testid="message-item"][data-role="assistant"]')).toBeVisible()
  await waitForFonts(page)

  const controls = [
    page.getByRole("button", { name: "收起侧边栏" }),
    page.getByRole("button", { name: "文件", exact: true }),
    page.getByRole("button", { name: "更多会话操作" }),
    page.getByRole("combobox", { name: /当前模型/ }),
  ]
  const styles = await Promise.all(controls.map((control) => control.evaluate((element) => {
    const style = getComputedStyle(element)
    return {
      borderColor: style.borderColor,
      backgroundColor: style.backgroundColor,
      boxShadow: style.boxShadow,
      backdropFilter: style.backdropFilter,
    }
  })))
  expect(styles).toHaveLength(4)
  for (const property of ["borderColor", "backgroundColor", "boxShadow", "backdropFilter"] as const) {
    expect(new Set(styles.map((style) => style[property])).size, `${property} should use one shared chat surface`).toBe(1)
  }
})

test("light and dark semantic text tokens meet the representative contrast floor", async ({ browser }) => {
  for (const theme of ["light", "dark"] as const) {
    const context = await browser.newContext({ viewport: { width: 1536, height: 864 }, deviceScaleFactor: 1.25 })
    const page = await context.newPage()
    await installRoutes(page, { theme })
    await page.goto("/chat/1")
    await expect(page.locator('[data-testid="message-item"][data-role="assistant"]')).toBeVisible()
    await waitForFonts(page)

    const tokens = await page.locator("html").evaluate((element) => {
      const style = getComputedStyle(element)
      return {
        foreground: style.getPropertyValue("--theme-fg").trim(),
        background: style.getPropertyValue("--theme-bg").trim(),
        mutedForeground: style.getPropertyValue("--theme-muted-fg").trim(),
        surface: style.getPropertyValue("--theme-surface").trim(),
        error: style.getPropertyValue("--status-error-fg").trim(),
        ring: style.getPropertyValue("--ring").trim(),
      }
    })
    expect(contrastRatio(tokens.foreground, tokens.background)).toBeGreaterThanOrEqual(4.5)
    expect(contrastRatio(tokens.mutedForeground, tokens.surface)).toBeGreaterThanOrEqual(4.5)
    expect(contrastRatio(tokens.error, tokens.background)).toBeGreaterThanOrEqual(4.5)
    expect(contrastRatio(tokens.ring, tokens.background)).toBeGreaterThanOrEqual(3)
    await context.close()
  }
})

test("icon-only chat controls expose a keyboard-visible tooltip", async ({ browser }) => {
  const context = await browser.newContext({ viewport: { width: 1536, height: 864 } })
  const page = await context.newPage()
  await installRoutes(page)
  await page.goto("/chat/1")
  const sidebarToggle = page.getByRole("button", { name: "收起侧边栏" })
  await expect(sidebarToggle).toBeVisible()
  await sidebarToggle.focus()
  await expect(page.getByRole("tooltip")).toHaveText("收起侧边栏", { timeout: 1200 })
  await context.close()
})

test("desktop sidebar resize is bounded, keyboard accessible, and persistent", async ({ browser }) => {
  const context = await browser.newContext({ viewport: { width: 1280, height: 800 } })
  const page = await context.newPage()
  await installRoutes(page, { sidebarWidth: 320 })
  await page.goto("/chat/1")
  await expect(page.locator('[data-testid="message-item"][data-role="assistant"]')).toBeVisible()
  await waitForFonts(page)

  const sidebar = page.getByRole("complementary", { name: "侧边栏" })
  let separator = page.getByRole("separator", { name: "调整侧边栏宽度" })
  await expect.poll(async () => Math.round((await sidebar.boundingBox())?.width || 0)).toBe(320)
  await expect(separator).toHaveAttribute("aria-valuemin", "240")
  await expect(separator).toHaveAttribute("aria-valuemax", "360")
  await expect(separator).toHaveAttribute("aria-valuenow", "320")

  await separator.focus()
  await separator.press("ArrowRight")
  await expect.poll(async () => Math.round((await sidebar.boundingBox())?.width || 0)).toBe(336)
  await page.reload()
  separator = page.getByRole("separator", { name: "调整侧边栏宽度" })
  await expect.poll(async () => Math.round((await sidebar.boundingBox())?.width || 0)).toBe(336)

  await separator.press("Home")
  await expect.poll(async () => Math.round((await sidebar.boundingBox())?.width || 0)).toBe(240)
  await separator.press("End")
  await expect.poll(async () => Math.round((await sidebar.boundingBox())?.width || 0)).toBe(360)

  const handle = await separator.boundingBox()
  expect(handle).not.toBeNull()
  await page.mouse.move((handle?.x || 0) + 2, (handle?.y || 0) + 80)
  await page.mouse.down()
  await page.mouse.move(120, (handle?.y || 0) + 80)
  await page.mouse.up()
  await expect.poll(async () => Math.round((await sidebar.boundingBox())?.width || 0)).toBe(240)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await context.close()
})

test("mobile sidebar ignores the desktop width preference", async ({ browser }) => {
  const context = await browser.newContext({ viewport: { width: 390, height: 844 } })
  const page = await context.newPage()
  await installRoutes(page, { sidebarWidth: 340 })
  await page.goto("/chat/1")
  await expect(page.locator('[data-testid="message-item"][data-role="assistant"]')).toBeVisible()

  await expect(page.getByRole("separator", { name: "调整侧边栏宽度" })).toHaveCount(0)
  await page.getByRole("button", { name: "打开侧边栏" }).click()
  const sidebar = page.getByRole("complementary", { name: "侧边栏" })
  await expect.poll(async () => Math.round((await sidebar.boundingBox())?.width || 0)).toBe(300)
  await expect(page.getByRole("separator", { name: "调整侧边栏宽度" })).toHaveCount(0)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await context.close()
})
