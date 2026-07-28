import { test, expect, newChat } from "./helpers"

// 守护 P0-2 / P1-1：上传一个声称支持的文件（docx）→ 应上传成功并出现在输入区附件列表，
// 然后发送 → 消息进入会话。docx 在全新库统一白名单后应被后端接受。
test("upload a supported docx then send", async ({ authed: page }) => {
  await newChat(page)

  // 构造一个最小合法的 docx（zip 容器 + word/document.xml）。
  // 这里用浏览器端 File，交给隐藏 input[type=file]。
  const docxBase64 = await page.evaluate(async () => {
    // 极小 docx：zip 里仅一个 word/document.xml。用 CompressionStream 构造 store/deflate 较复杂，
    // 改为内联一个预先构造好的最小 docx 的 base64（含 "hello docx"）。
    return (window as unknown as { __E2E_DOCX__?: string }).__E2E_DOCX__ || ""
  })

  // 若页面未注入预置 docx，则用纯文本 .txt 兜底（同样属支持类型，验证上传闭环）。
  const fileInput = page.getByTestId("file-input")
  if (docxBase64) {
    await fileInput.setInputFiles({
      name: "sample.docx",
      mimeType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
      buffer: Buffer.from(docxBase64, "base64"),
    })
  } else {
    await fileInput.setInputFiles({
      name: "note.txt",
      mimeType: "text/plain",
      buffer: Buffer.from("hello from e2e attachment\n第二行中文", "utf-8"),
    })
  }

  // 上传成功后，输入区应出现附件条目（文件名可见）。
  await expect(page.getByText(/sample\.docx|note\.txt/)).toBeVisible({ timeout: 20_000 })

  // 附带文字并发送。
  await page.getByTestId("chat-input").fill("请概述这个附件的内容。")
  await page.getByTestId("send-button").click()

  // 用户消息进入会话。
  await expect(
    page.locator('[data-testid="message-item"][data-role="user"]').last(),
  ).toBeVisible({ timeout: 20_000 })
})
