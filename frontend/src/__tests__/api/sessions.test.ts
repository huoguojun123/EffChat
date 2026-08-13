import { beforeEach, describe, expect, it, vi } from "vitest"

const { get, patch } = vi.hoisted(() => ({ get: vi.fn(), patch: vi.fn() }))

vi.mock("@/api/client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/api/client")>()),
  api: { get, patch },
}))

import { downloadFilename } from "@/api/client"
import { searchConversations, updateSession } from "@/api/sessions"

describe("searchConversations", () => {
  beforeEach(() => get.mockReset())

  it.each([
    ["all", false, "scope=all"],
    ["unfiled", false, "scope=unfiled"],
    [7, false, "scope=folder&folder_id=7"],
    [7, true, "scope=all"],
  ] as const)("builds the expected scope for %s", async (folderId, searchAll, expected) => {
    get.mockResolvedValue({ results: [] })

    await searchConversations("检索目标", folderId, searchAll)

    expect(get).toHaveBeenCalledOnce()
    const url = String(get.mock.calls[0][0])
    expect(url).toContain("q=%E6%A3%80%E7%B4%A2%E7%9B%AE%E6%A0%87")
    expect(url).toContain(expected)
    expect(url).toContain("limit=30")
  })
})

describe("downloadFilename", () => {
  it("reads RFC 5987 UTF-8 filenames", () => {
    expect(downloadFilename("attachment; filename*=UTF-8''%E4%BC%9A%E8%AF%9D.md")).toBe("会话.md")
  })

  it("falls back to quoted filenames", () => {
    expect(downloadFilename('attachment; filename="conversation-4.md"')).toBe("conversation-4.md")
  })

  it("rejects malformed encoded filenames", () => {
    expect(downloadFilename("attachment; filename*=UTF-8''%ZZ")).toBe("")
  })
})

describe("updateSession", () => {
  beforeEach(() => patch.mockReset())

  it("returns the canonical session after a partial update", async () => {
    const updated = {
      id: 42,
      user_id: 1,
      folder_id: null,
      title: "fixture",
      title_generated: false,
      model_id: "fixture-model",
      provider: "openai",
      memory_enabled: false,
      created_at: "2026-08-06T00:00:00Z",
      updated_at: "2026-08-06T00:01:00Z",
    }
    patch.mockResolvedValue(updated)

    await expect(updateSession(42, { memory_enabled: false })).resolves.toBe(updated)

    expect(patch).toHaveBeenCalledOnce()
    expect(patch).toHaveBeenCalledWith("/sessions/42", { memory_enabled: false })
  })
})
