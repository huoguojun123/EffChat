import { describe, expect, it } from "vitest"
import { groupSessionsByDate } from "@/components/sidebar/sessionGroups"
import type { Session } from "@/types"

function session(id: number, createdAt: string, pinned = false): Session {
  return {
    id,
    user_id: 1,
    title: `Session ${id}`,
    title_generated: false,
    model_id: "test",
    provider: "test",
    created_at: createdAt,
    updated_at: createdAt,
    pinned_at: pinned ? createdAt : null,
  }
}

describe("groupSessionsByDate", () => {
  it("keeps pinned sessions separate and preserves every date bucket", () => {
    const now = new Date()
    const date = (daysAgo: number) => new Date(now.getFullYear(), now.getMonth(), now.getDate() - daysAgo, 12).toISOString()

    const groups = groupSessionsByDate([
      session(1, date(20), true),
      session(2, date(0)),
      session(3, date(1)),
      session(4, date(4)),
      session(5, date(8)),
    ])

    expect(groups.map((group) => [group.label, group.items.map((item) => item.id)])).toEqual([
      ["", [1]],
      ["今天", [2]],
      ["昨天", [3]],
      ["最近 7 天", [4]],
      ["更早", [5]],
    ])
  })
})
