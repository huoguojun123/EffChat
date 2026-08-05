import { describe, expect, it, vi } from "vitest"
import { collectOffsetPages } from "@/api/pagination"

describe("collectOffsetPages", () => {
  it.each([101, 201])("collects all %i items with advancing offsets", async (total) => {
    const items = Array.from({ length: total }, (_, index) => index + 1)
    const load = vi.fn(async (limit: number, offset: number) => {
      const pageItems = items.slice(offset, offset + limit)
      const nextOffset = offset + pageItems.length
      return {
        items: pageItems,
        total,
        has_more: nextOffset < total,
        next_offset: nextOffset,
      }
    })

    await expect(collectOffsetPages(load)).resolves.toEqual(items)
    expect(load.mock.calls.map(([, offset]) => offset)).toEqual(
      total === 101 ? [0, 100] : [0, 100, 200],
    )
  })

  it("rejects a page that claims more data without advancing", async () => {
    await expect(collectOffsetPages(async () => ({
      items: [], total: 1, has_more: true, next_offset: 0,
    }))).rejects.toThrow("分页响应未推进")
  })
})
