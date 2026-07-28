import { describe, expect, it } from "vitest"
import { buildConversationTurns, conversationTurnMarkerRange, conversationTurnRailMode } from "@/lib/conversationTurns"
import type { Message } from "@/types"

function message(id: number, role: "user" | "assistant" | "tool", content: string) {
  return {
    id,
    role,
    message_data: { role, content },
  } as Message
}

describe("buildConversationTurns", () => {
  it("groups each user message with its following assistant reply", () => {
    const turns = buildConversationTurns([
      message(1, "user", "检查 **部署状态**"),
      message(2, "assistant", "容器运行正常"),
      message(3, "tool", "internal"),
      message(4, "user", "继续"),
    ])

    expect(turns).toEqual([
      expect.objectContaining({ id: 1, title: "检查 部署状态", assistantPreview: "容器运行正常" }),
      expect.objectContaining({ id: 4, title: "继续", assistantPreview: "" }),
    ])
  })

  it("uses an attachment label when a user turn has no text", () => {
    const attachment = {
      ...message(5, "user", ""),
      message_data: {
        role: "user" as const,
        content: "",
        attachments: [{ file_id: 1, filename: "sample.png", file_type: "image/png", size: 100 }],
      },
    }

    expect(buildConversationTurns([attachment])[0].title).toBe("附件消息")
  })

  it("does not add compaction summaries back into the turn index", () => {
    const summary = {
      ...message(2, "user", "compressed checkpoint"),
      message_data: {
        role: "user" as const,
        content: "compressed checkpoint",
        metadata: { compaction_summary: true },
      },
    }

    expect(buildConversationTurns([
      message(1, "user", "第一轮"),
      summary,
      message(3, "user", "第二轮"),
    ])).toHaveLength(2)
  })
})

describe("conversationTurnMarkerRange", () => {
  it("keeps small and medium rails fully mounted", () => {
    expect(conversationTurnMarkerRange(500, 3000, 520)).toEqual({ start: 0, end: 500 })
  })

  it("bounds a 5000-turn rail to the viewport and overscan", () => {
    const range = conversationTurnMarkerRange(5000, 24000, 520)
    expect(range.start).toBe(2380)
    expect(range.end - range.start).toBe(92)
  })

  it("clamps overscan at both ends", () => {
    expect(conversationTurnMarkerRange(5000, 0, 520)).toEqual({ start: 0, end: 92 })
    expect(conversationTurnMarkerRange(5000, 50000, 520)).toEqual({ start: 4980, end: 5000 })
  })
})

describe("conversationTurnRailMode", () => {
  it.each([
    [2, false, false],
    [60, false, false],
    [61, true, false],
    [500, true, false],
    [501, true, true],
    [5000, true, true],
  ])("classifies %i turns", (count, scrollable, virtual) => {
    expect(conversationTurnRailMode(count)).toEqual({ scrollable, virtual })
  })
})
