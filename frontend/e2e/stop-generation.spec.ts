import { test, expect, newChat } from "./helpers"

// 守护 P0-1：发长回复 → 流式中点停止 → 不刷新页面 → UI 必须从流式态恢复（发送按钮回来，
// 不卡死），且本轮用户消息已落库可见。停止后会主动同步后端结果，
// 后端取消时已落库的部分助手回复（若有）也应出现在当前页。
test("stop generation recovers the UI and syncs from server", async ({ authed: page }) => {
  await newChat(page)

  const input = page.getByTestId("chat-input")
  await input.fill("请用中文写一篇约 1200 字、分多个段落的长文，详细介绍 Go 语言的并发模型与调度器。")
  await page.getByTestId("send-button").click()

  // 进入流式：停止按钮出现（流式期间正文在 streaming buffer 中，尚未落为 message-item）。
  const stop = page.getByTestId("stop-button")
  await expect(stop).toBeVisible({ timeout: 20_000 })

  // 给后端一点时间产出并能落库部分内容，再停止（不等流式自然结束）。
  await page.waitForTimeout(2500)
  if (!(await stop.isVisible())) {
    test.skip(true, "generation finished before stop could be exercised (model too fast/short)")
    return
  }
  await stop.click()

  // 关键断言：停止后 UI 从流式态恢复（发送按钮回来），不卡在 streaming/syncing。
  await expect(page.getByTestId("send-button")).toBeVisible({ timeout: 25_000 })

  // 同步后本轮用户消息应已落库可见（证明 abort 后做了 DB 同步，而非仅本地切流）。
  await expect(
    page.locator('[data-testid="message-item"][data-role="user"]').last(),
  ).toBeVisible({ timeout: 15_000 })
})
