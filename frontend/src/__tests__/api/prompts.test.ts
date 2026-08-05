import { describe, expect, it, vi } from "vitest"
import { api } from "@/api/client"
import { promptsApi } from "@/api/prompts"

vi.mock("@/api/client", () => ({
  api: { get: vi.fn(), post: vi.fn(), patch: vi.fn(), delete: vi.fn() },
}))

describe("promptsApi bounded catalogs", () => {
  it.each([
    ["mine", () => promptsApi.listAllMine(), "/prompts"],
    ["public", () => promptsApi.listAllPublic(), "/prompts/public"],
  ] as const)("loads every %s prompt page", async (_name, load, path) => {
    vi.mocked(api.get)
      .mockResolvedValueOnce({ prompts: Array.from({ length: 100 }, (_, index) => ({ id: index + 1 })), total: 101, has_more: true, next_offset: 100 })
      .mockResolvedValueOnce({ prompts: [{ id: 101 }], total: 101, has_more: false, next_offset: 101 })

    const prompts = await load()

    expect(prompts).toHaveLength(101)
    expect(api.get).toHaveBeenNthCalledWith(1, `${path}?limit=100&offset=0`)
    expect(api.get).toHaveBeenNthCalledWith(2, `${path}?limit=100&offset=100`)
    vi.mocked(api.get).mockReset()
  })
})
