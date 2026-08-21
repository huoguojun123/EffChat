import { test, expect, type Page } from "@playwright/test"

const session = {
  id: 1,
  user_id: 1,
  title: "图表预览",
  title_generated: false,
  model_id: "",
  provider: "",
  created_at: "2026-07-11T00:00:00Z",
  updated_at: "2026-07-11T00:00:00Z",
}

const tallMermaid = [
  "graph TD",
  ...Array.from({ length: 24 }, (_, index) => `  N${index}[节点 ${index + 1}] --> N${index + 1}[节点 ${index + 2}]`),
].join("\n")

const graphvizCode = `digraph G {
  rankdir=LR;
  bgcolor="#f8fafc";
  fontname="system-ui, sans-serif";
  node [shape=box, style=filled, fillcolor="#e0e7ff", color="#4338ca", fontname="system-ui, sans-serif", fontsize=12, margin="0.2,0.1"];
  edge [color="#94a3b8", fontname="system-ui, sans-serif", fontsize=10];

  subgraph cluster_fe {
    label="前端层";
    style=filled;
    fillcolor="#fef2f2";
    color="#fca5a5";
    node [fillcolor="#fee2e2", color="#ef4444"];
    React [label="React SPA"];
    Mobile [label="Flutter App"];
  }

  subgraph cluster_api {
    label="API 层";
    style=filled;
    fillcolor="#f0fdf4";
    color="#86efac";
    node [fillcolor="#dcfce7", color="#22c55e"];
    Gateway [label="API 网关"];
    Auth [label="认证服务"];
  }

  subgraph cluster_svc {
    label="服务层";
    style=filled;
    fillcolor="#fefce8";
    color="#fde68a";
    node [fillcolor="#fef9c3", color="#eab308"];
    SvcA [label="订单服务"];
    SvcB [label="支付服务"];
    SvcC [label="库存服务"];
  }

  subgraph cluster_data {
    label="数据层";
    style=filled;
    fillcolor="#f5f3ff";
    color="#c4b5fd";
    node [fillcolor="#ede9fe", color="#8b5cf6"];
    PG [label="PostgreSQL"];
    Redis;
    Kafka;
  }

  React -> Gateway;
  Mobile -> Gateway;
  Gateway -> Auth;
  Gateway -> SvcA;
  Gateway -> SvcB;
  Gateway -> SvcC;
  SvcA -> PG;
  SvcA -> Redis;
  SvcA -> Kafka;
  SvcB -> PG;
  SvcB -> Kafka;
  SvcC -> PG;
  SvcC -> Redis;
}`

async function mockChat(page: Page, content: string) {
  await page.addInitScript(() => localStorage.setItem("token", "test-token"))
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname
    if (path === "/api/v1/users/me") return route.fulfill({ json: { id: 1, username: "member", role: "user", is_active: true } })
    if (path === "/api/v1/system/info") return route.fulfill({ json: { system_name: "EffChat" } })
    if (path === "/api/v1/models") return route.fulfill({ json: { models: [], total: 0 } })
    if (path === "/api/v1/sessions") return route.fulfill({ json: { sessions: [session], has_more: false, next_offset: 0 } })
    if (path === "/api/v1/session-folders") return route.fulfill({ json: { folders: [] } })
    if (path === "/api/v1/sessions/1") return route.fulfill({ json: session })
    if (path === "/api/v1/sessions/1/turns") {
      return route.fulfill({ json: { turns: [], total: 0, has_more: false } })
    }
    if (path === "/api/v1/sessions/1/messages" || path === "/api/v1/sessions/1/message-window") {
      return route.fulfill({
        json: {
          messages: [{
            id: 1,
            session_id: 1,
            schema_version: "v1",
            role: "assistant",
            has_tool_calls: false,
            has_reasoning: false,
            created_at: "2026-07-11T00:00:00Z",
            message_data: { content },
          }],
          has_more: false,
          has_older: false,
          has_newer: false,
          first_turn_id: 0,
          last_turn_id: 0,
        },
      })
    }
    return route.fulfill({ json: {} })
  })
}

test("Mermaid waits with the message and appears within the compact height cap", async ({ page }) => {
  await page.addInitScript(() => {
    const samples: string[] = []
    Object.defineProperty(window, "__markdownOpacitySamples", { value: samples })
    const start = () => {
      const timer = window.setInterval(() => {
        const body = document.querySelector<HTMLElement>(".markdown-body")
        if (!body) return
        const opacity = Number.parseFloat(getComputedStyle(body).opacity)
        const state = opacity < 0.01 ? "hidden" : opacity > 0.99 ? "visible" : "transitioning"
        if (samples.at(-1) !== state) samples.push(state)
      }, 8)
      window.setTimeout(() => window.clearInterval(timer), 5000)
    }
    if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", start, { once: true })
    else start()
  })
  await page.route(/mermaid[^/]*\.js(?:\?.*)?$/, async (route) => {
    await new Promise((resolve) => setTimeout(resolve, 1000))
    await route.continue()
  })
  await mockChat(page, `\`\`\`mermaid\n${tallMermaid}\n\`\`\``)

  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/chat/1")

  await page.waitForFunction(() => {
    const markdown = document.querySelector<HTMLElement>('[data-markdown-preparing="true"]')
    const loading = markdown?.querySelector<HTMLElement>('[role="status"]')
    const body = markdown?.querySelector<HTMLElement>(".markdown-body")
    return loading?.innerText.includes("正在准备图表") && body && getComputedStyle(body).opacity === "0"
  })

  const body = page.locator("[data-inline-view]")
  const preview = page.locator(".diagram-inline-preview")
  await expect(preview).toBeVisible()
  await expect(page.locator("[data-markdown-preparing]")).toHaveCount(0)
  await expect(page.locator(".markdown-body")).toHaveCSS("opacity", "1")
  const opacitySamples = await page.evaluate(() => (
    (window as Window & { __markdownOpacitySamples?: string[] }).__markdownOpacitySamples || []
  ))
  expect(opacitySamples[0]).toBe("hidden")
  const firstVisible = opacitySamples.indexOf("visible")
  expect(firstVisible).toBeGreaterThan(-1)
  expect(opacitySamples.slice(firstVisible + 1)).not.toContain("hidden")
  await expect(body).toHaveAttribute("data-inline-view", "preview")
  await expect(page.locator('[aria-label="Mermaid rendering"]')).toHaveCount(0)
  await expect.poll(async () => (await preview.boundingBox())?.height || 0).toBeGreaterThanOrEqual(240)
  await expect.poll(async () => (await preview.boundingBox())?.height || Infinity).toBeLessThanOrEqual(480)

  await page.getByRole("button", { name: "查看源码", exact: true }).click()
  await expect(body).toHaveAttribute("data-inline-view", "source")
  await expect.poll(async () => Math.round((await body.boundingBox())?.height || 0)).toBe(360)
  await page.getByRole("button", { name: "查看预览", exact: true }).click()
  await expect(body).toHaveAttribute("data-inline-view", "preview")

  await page.setViewportSize({ width: 390, height: 844 })
  await page.reload()
  await expect(body).toHaveAttribute("data-inline-view", "preview")
  const toolbar = page.getByTestId("composer-toolbar")
  const firstButton = await toolbar.getByRole("button", { name: "暂存附件" }).boundingBox()
  const secondButton = await toolbar.getByRole("button", { name: /联网/ }).boundingBox()
  expect(firstButton).not.toBeNull()
  expect(secondButton).not.toBeNull()
  expect(Math.round((secondButton?.x || 0) - (firstButton?.x || 0))).toBe(44)
})

test("Graphviz inline and dialog previews become visible", async ({ page }) => {
  await mockChat(page, `\`\`\`dot\n${graphvizCode}\n\`\`\``)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/chat/1")

  const body = page.locator("[data-inline-view]")
  await expect(body).toHaveAttribute("data-inline-view", "preview")
  await expect(body.locator('iframe[title="Graphviz preview"]')).toBeVisible()

  await page.getByRole("button", { name: "在弹窗中查看预览" }).click()
  const dialog = page.getByRole("dialog", { name: "Graphviz 图表" })
  await expect(dialog).toBeVisible()
  expect(await dialog.evaluate((element) => getComputedStyle(element).animationName)).toBe("workspace-window-open")
  await dialog.getByRole("button", { name: "全屏显示" }).click()
  await expect.poll(async () => Math.round((await dialog.boundingBox())?.width || 0)).toBe(1440)
  await expect.poll(async () => Math.round((await dialog.boundingBox())?.height || 0)).toBe(900)
})

test("HTML dialog preview opens in the same full-screen workspace", async ({ page }) => {
  await mockChat(page, "```html\n<main><h1>HTML workspace</h1><p>Preview content</p></main>\n```")
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/chat/1")

  await page.getByRole("button", { name: "在弹窗中查看预览" }).click()
  const dialog = page.getByRole("dialog", { name: "网页预览" })
  await expect(dialog).toBeVisible()
  await expect(dialog.locator("iframe")).toBeVisible()
  await dialog.getByRole("button", { name: "全屏显示" }).click()
  await expect.poll(async () => Math.round((await dialog.boundingBox())?.width || 0)).toBe(1440)
  await expect.poll(async () => Math.round((await dialog.boundingBox())?.height || 0)).toBe(900)
})
