import { beforeEach, describe, expect, it, vi } from "vitest"

const { get } = vi.hoisted(() => ({ get: vi.fn() }))

vi.mock("@/api/client", () => ({ api: { get } }))

import { downloadFilename, searchConversations } from "@/api/sessions"

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
