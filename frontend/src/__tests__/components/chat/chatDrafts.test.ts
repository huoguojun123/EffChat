import { describe, expect, it } from "vitest"
import { decodeChatDrafts, decodeChatDraftState } from "@/components/chat/chatDrafts"

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

  it("reads legacy drafts and versioned pending submissions", () => {
    expect(decodeChatDraftState('{"17":"legacy draft"}')).toEqual({
      drafts: { 17: "legacy draft" },
      submissions: {},
    })
    expect(decodeChatDraftState(JSON.stringify({
      version: 2,
      drafts: { 17: "current draft" },
      submissions: {
        17: {
          id: 4,
          sessionId: 17,
          clientRunId: "run-17",
          content: "submitted content",
          attachments: [],
          attachmentIds: [],
          status: "failed",
        },
      },
    }))).toMatchObject({
      drafts: { 17: "current draft" },
      submissions: { 17: { id: 4, clientRunId: "run-17", status: "failed" } },
    })
  })

  it("rejects pending submissions stored under a mismatched session key", () => {
    expect(decodeChatDraftState(JSON.stringify({
      version: 2,
      drafts: {},
      submissions: {
        18: {
          id: 4,
          sessionId: 17,
          clientRunId: "run-17",
          content: "submitted content",
          attachments: [],
          attachmentIds: [],
          status: "failed",
        },
      },
    }))).toEqual({ drafts: {}, submissions: {} })
  })
})
