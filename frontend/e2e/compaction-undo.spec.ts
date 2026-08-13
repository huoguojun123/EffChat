import { test, expect, newChat } from "./helpers"

// 守护压缩撤销链路：手动 /compact → 出现压缩分割线（可撤销）→ 撤销 → 分割线消失、历史恢复。
// 隔离模型 stub 确定性返回摘要；主链不得以 skip 吞掉模型、协议或环境回归。
test("manual compaction then undo restores history", async ({ authed: page }) => {
  await newChat(page)

  // 先产生一轮对话，给 /compact 提供可压缩的历史。
  await page.getByTestId("chat-input").fill("用一句话介绍你自己。")
  await page.getByTestId("send-button").click()
  // 等本轮结束（发送按钮回来）。
  await expect(page.getByTestId("send-button")).toBeVisible({ timeout: 90_000 })
  // 等助手回复落为 message-item（确保有可压缩的已落库历史）。
  await expect(
    page.locator('[data-testid="message-item"][data-role="assistant"]').last(),
  ).toBeVisible({ timeout: 30_000 })

  // 触发手动压缩。
  await page.getByTestId("chat-input").fill("/compact")
  await page.getByTestId("send-button").click()

  const undo = page.getByTestId("undo-compaction")
  await expect(page.getByTestId("compaction-divider")).toBeVisible({ timeout: 45_000 })
  await expect(undo).toBeVisible({ timeout: 10_000 })

  // 撤销并确认。
  await undo.click()
  await page.getByTestId("undo-compaction-confirm").click()

  // 撤销后分割线应消失（历史恢复为完整消息）。
  await expect(page.getByTestId("compaction-divider")).toHaveCount(0, { timeout: 20_000 })
})
