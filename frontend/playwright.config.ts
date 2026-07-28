import { defineConfig, devices } from "@playwright/test"

// E2E 守护最脆弱的前端链路（停止生成 / 附件上传 / 压缩撤销）。
// 这些用例需要真实运行的全栈（前端 5173 + 后端 8080 + DB + 可用模型），
// 因此默认指向本地 start.sh 起的服务；服务不可达时各 spec 自行 skip，不阻断主验证。
// 运行：先 `./start.sh`，再 `cd frontend && npm run e2e`。
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
    trace: "retain-on-failure",
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
})
