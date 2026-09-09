import { afterEach, describe, expect, it, vi } from "vitest"
import { decodeChatDrafts, decodeChatDraftState, removeChatDraftSession } from "@/components/chat/chatDrafts"

describe("chat draft persistence", () => {
  afterEach(() => vi.unstubAllGlobals())

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

  it("removes drafts and pending submissions for a deleted session", () => {
    const values = new Map<string, string>()
    vi.stubGlobal("sessionStorage", {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value),
      removeItem: (key: string) => values.delete(key),
      key: (index: number) => Array.from(values.keys())[index] ?? null,
      get length() { return values.size },
    })
    sessionStorage.setItem("effchat:session-drafts", JSON.stringify({
      version: 2,
      drafts: { 17: "keep", 18: "delete" },
      submissions: {
        18: { id: 4, sessionId: 18, clientRunId: "run-18", content: "submitted", attachments: [], attachmentIds: [], status: "failed" },
      },
    }))
    expect(removeChatDraftSession(18)).toBe(true)
    expect(decodeChatDraftState(sessionStorage.getItem("effchat:session-drafts"))).toEqual({ drafts: { 17: "keep" }, submissions: {} })
  })

  it("keeps the composer usable when session storage is unreadable or unwritable", async () => {
    const { loadChatDraftState, saveChatDraftState } = await import("@/components/chat/chatDrafts")
    vi.stubGlobal("sessionStorage", {
      getItem: () => { throw new DOMException("blocked", "SecurityError") },
      setItem: () => { throw new DOMException("blocked", "SecurityError") },
      removeItem: () => { throw new DOMException("blocked", "SecurityError") },
    })

    expect(loadChatDraftState()).toEqual({ drafts: {}, submissions: {} })
    expect(saveChatDraftState({ drafts: { 17: "draft" }, submissions: {} })).toBe(false)
    expect(saveChatDraftState({ drafts: {}, submissions: {} })).toBe(false)
  })
})
