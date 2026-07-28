import { test as base, expect, type Page } from "@playwright/test"

// 这些 e2e 需要真实全栈（前端 + 后端 + DB + 可用模型），用环境变量提供登录凭据：
//   E2E_USERNAME / E2E_PASSWORD（默认 admin / 见下），可选 E2E_BASE_URL。
// 服务不可达或登录失败时整组用例 skip——本地未起服务时不应让 CI/本地校验失败。
const USERNAME = process.env.E2E_USERNAME || "admin"
const PASSWORD = process.env.E2E_PASSWORD || "admin123456"

export { expect }

// serverUp 探测前端是否在线（dev server 经 vite 代理转发到后端）。
async function serverUp(page: Page): Promise<boolean> {
  try {
    const res = await page.request.get("/api/v1/models", { timeout: 3000 })
    // 任意 HTTP 响应（含 401）都说明服务在线；网络错误才视为未起服务。
    return res.status() > 0
  } catch {
    return false
  }
}

// authOK 用环境凭据做一次真实 API 登录，验证凭据有效（拿到 token）。
// 未配置或凭据无效时返回 false → 用例跳过，不误报失败。
async function authOK(page: Page): Promise<boolean> {
  try {
    const res = await page.request.post("/api/v1/auth/login", {
      data: { username: USERNAME, password: PASSWORD },
      timeout: 5000,
    })
    if (!res.ok()) return false
    const body = await res.json()
    return typeof body?.token === "string" && body.token.length > 0
  } catch {
    return false
  }
}

// login 走真实登录页，成功后停留在主界面。
async function login(page: Page) {
  await page.goto("/login")
  await page.locator("#username").fill(USERNAME)
  await page.locator("#password").fill(PASSWORD)
  await page.getByRole("button", { name: /登录/ }).click()
  await page.waitForURL("**/", { timeout: 15_000 })
}

// newChat 新建一个会话并等待输入框就绪。
async function newChat(page: Page) {
  await page.getByTestId("new-chat").click()
  await expect(page.getByTestId("chat-input")).toBeVisible()
}

// test fixture：登录态 + 服务探测。无服务或凭据无效则跳过。
export const test = base.extend<{ authed: Page }>({
  authed: async ({ page }, runTest, testInfo) => {
    if (!(await serverUp(page))) {
      testInfo.skip(true, "no running stack (start.sh) — skipping e2e")
    }
    if (!(await authOK(page))) {
      testInfo.skip(true, "E2E_USERNAME/E2E_PASSWORD not valid for this stack — skipping e2e")
    }
    await login(page)
    await runTest(page)
  },
})

export { login, newChat }
