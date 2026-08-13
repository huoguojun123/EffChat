import { readFileSync } from "node:fs"
import { fileURLToPath } from "node:url"
import { test, expect, newChat } from "./helpers"

// 守护 P0-2 / P1-1：上传一个声称支持的文件（docx）→ 应上传成功并出现在输入区附件列表，
// 然后发送 → 消息进入会话。docx 在全新库统一白名单后应被后端接受。
test("upload a supported docx then send", async ({ authed: page }) => {
  await newChat(page)

  const fixture = fileURLToPath(new URL("./fixtures/sample.docx.b64", import.meta.url))
  const docx = Buffer.from(readFileSync(fixture, "utf8").trim(), "base64")
  const fileInput = page.getByTestId("file-input")
  const uploadResponse = page.waitForResponse((response) => response.url().endsWith("/api/v1/files") && response.request().method() === "POST")
  await fileInput.setInputFiles({
    name: "sample.docx",
    mimeType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
    buffer: docx,
  })
  const uploaded = await (await uploadResponse).json() as { id: number; file_type: string }
  expect(uploaded.file_type).toBe("application/vnd.openxmlformats-officedocument.wordprocessingml.document")

  // Composer deliberately shows a compact count; the staged drawer owns file-level details.
  const stagedButton = page.getByRole("button", { name: /本次已选 1 个附件，暂存 1 个/ })
  await expect(stagedButton).toBeVisible({ timeout: 20_000 })
  await stagedButton.click()
  await expect(page.getByText("sample.docx")).toBeVisible({ timeout: 10_000 })
  await page.keyboard.press("Escape")

  const token = await page.evaluate(() => localStorage.getItem("token"))
  expect(token).toBeTruthy()
  await expect.poll(async () => {
    const response = await page.request.get(`/api/v1/files/${uploaded.id}/preview`, { headers: { Authorization: `Bearer ${token}` } })
    if (!response.ok()) return ""
    return (await response.json() as { content?: string }).content || ""
  }, { timeout: 20_000 }).toContain("EffChat deterministic DOCX fixture 2026")
  const download = await page.request.get(`/api/v1/files/${uploaded.id}`, { headers: { Authorization: `Bearer ${token}` } })
  expect(download.ok()).toBeTruthy()
  expect(download.headers()["content-type"]).toContain("text/plain")
  expect(download.headers()["content-disposition"]).toContain("sample.docx.txt")
  expect((await download.text())).toContain("EffChat deterministic DOCX fixture 2026")

  // 附带文字并发送。
  await page.getByTestId("chat-input").fill("请概述这个附件的内容。")
  await page.getByTestId("send-button").click()

  // 用户消息进入会话。
  await expect(
    page.locator('[data-testid="message-item"][data-role="user"]').last(),
  ).toBeVisible({ timeout: 20_000 })
})
