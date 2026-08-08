import { defineConfig, devices } from "@playwright/test"

// 真实全栈用例由 scripts/run-isolated-playwright.sh 启动独立 Compose/DB 和确定性模型 stub。
// 普通 mocked UI spec 仍可指向显式 E2E_BASE_URL；CI 强制 E2E_REQUIRE_STACK=1，readiness
// 或凭据失败必须报错，不能以整组 skip 形成零执行绿灯。
const BASE_URL = process.env.E2E_BASE_URL || "http://localhost:5173"

export default defineConfig({
  testDir: "./e2e",
  timeout: 60_000,
  expect: { timeout: 15_000 },
  fullyParallel: false,
  retries: 0,
  reporter: [["list"]],
  use: {
    baseURL: BASE_URL,
    browserName: "chromium",
    trace: "retain-on-failure",
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
})
