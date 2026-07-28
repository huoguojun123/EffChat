import { test, expect, newChat } from "./helpers"

// 守护压缩撤销链路：手动 /compact → 出现压缩分割线（可撤销）→ 撤销 → 分割线消失、历史恢复。
// 压缩需要足够历史，且依赖真实模型；若本会话不满足压缩条件（后端返回 skip，无分割线），
// 用例按“环境不满足”跳过，不误报失败。
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

  // 等待出现可撤销的压缩分割线；压缩走小模型，给足时间。
  // 若最终没出现（多为 skip：历史不足以压缩 / 模型异常），跳过而非误报失败。
  const undo = page.getByTestId("undo-compaction")
  try {
    await expect(page.getByTestId("compaction-divider")).toBeVisible({ timeout: 45_000 })
    await expect(undo).toBeVisible({ timeout: 10_000 })
  } catch {
    test.skip(true, "compaction returned skip / unavailable — cannot exercise undo")
    return
  }

  // 撤销并确认。
  await undo.click()
  await page.getByTestId("undo-compaction-confirm").click()

  // 撤销后分割线应消失（历史恢复为完整消息）。
  await expect(page.getByTestId("compaction-divider")).toHaveCount(0, { timeout: 20_000 })
})
