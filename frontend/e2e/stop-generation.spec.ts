import { test, expect, newChat } from "./helpers"

// 守护 P0-1：发长回复 → 流式中点停止 → 不刷新页面 → UI 必须从流式态恢复（发送按钮回来，
// 不卡死），且本轮用户消息已落库可见。停止后会主动同步后端结果，
// 后端取消时已落库的部分助手回复（若有）也应出现在当前页。
test("stop generation recovers the UI and syncs from server", async ({ authed: page }) => {
  await newChat(page)

  const input = page.getByTestId("chat-input")
  await input.fill("E2E_STOP_AFTER_FIRST_DELTA")
  await page.getByTestId("send-button").click()

  // 进入流式：停止按钮出现（流式期间正文在 streaming buffer 中，尚未落为 message-item）。
  const stop = page.getByTestId("stop-button")
  await expect(stop).toBeVisible({ timeout: 20_000 })

  await expect(page.getByText("首包已经到达，后续内容必须由用户停止。")).toBeVisible({ timeout: 20_000 })
  await stop.click()

  // 关键断言：停止后 UI 从流式态恢复（发送按钮回来），不卡在 streaming/syncing。
  await expect(page.getByTestId("send-button")).toBeVisible({ timeout: 25_000 })

  // 同步后本轮用户消息应已落库可见（证明 abort 后做了 DB 同步，而非仅本地切流）。
  await expect(
    page.locator('[data-testid="message-item"][data-role="user"]').last(),
  ).toBeVisible({ timeout: 15_000 })

  await page.reload()
  await expect(page.getByText("首包已经到达，后续内容必须由用户停止。")).toBeVisible({ timeout: 20_000 })
})
