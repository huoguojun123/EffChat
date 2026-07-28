import { describe, expect, it } from "vitest"
import type { AssistantSegment } from "@/types"
import { groupAssistantSegments } from "@/components/message/assistantSegments"

describe("groupAssistantSegments", () => {
  it("keeps all reasoning and tool calls together until the first answer content", () => {
    const segments: AssistantSegment[] = [
      { thinking: "先判断问题" },
      { tool_calls: [{ id: "search", name: "web_search", status: "done" }] },
      { thinking: "根据搜索结果继续" },
      { tool_calls: [{ id: "extract", name: "web_extract", status: "done" }] },
      { content: "最终回答" },
    ]

    expect(groupAssistantSegments(segments)).toEqual([
      {
        reasoning: {
          segments: [
            { thinking: "先判断问题" },
            { tool_calls: [{ id: "search", name: "web_search", status: "done" }] },
            { thinking: "根据搜索结果继续" },
            { tool_calls: [{ id: "extract", name: "web_extract", status: "done" }] },
          ],
        },
      },
      { content: "最终回答" },
    ])
  })
})
