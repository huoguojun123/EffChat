import { describe, expect, it } from "vitest"
import { decodeChatDrafts } from "@/components/chat/chatDrafts"

describe("chat draft persistence", () => {
  it("restores valid per-session drafts after a page reload", () => {
    expect(decodeChatDrafts('{"17":"还没发送的内容","65":"另一个会话"}')).toEqual({
      17: "还没发送的内容",
      65: "另一个会话",
    })
  })

  it("ignores corrupt and invalid draft entries", () => {
    expect(decodeChatDrafts("not-json")).toEqual({})
    expect(decodeChatDrafts('{"0":"bad","abc":"bad","17":3,"18":""}')).toEqual({})
  })
})
