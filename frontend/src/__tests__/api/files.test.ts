import { beforeEach, describe, expect, it, vi } from "vitest"
import type { ApiError } from "@/api/client"
import { fetchFileBlob, filesApi } from "@/api/files"

const localStorageData = new Map<string, string>()

beforeEach(() => {
  vi.resetModules()
  vi.clearAllMocks()
  localStorageData.clear()
  vi.stubGlobal("localStorage", {
    getItem: vi.fn((key: string) => localStorageData.get(key) ?? null),
    setItem: vi.fn((key: string, value: string) => localStorageData.set(key, String(value))),
    removeItem: vi.fn((key: string) => localStorageData.delete(key)),
    clear: vi.fn(() => localStorageData.clear()),
  })
  vi.stubGlobal("window", {
    location: { href: "" },
  })
})

describe("fetchFileBlob", () => {
  it("does not clear a newer login when an old download receives 401", async () => {
    localStorage.setItem("token", "old-token")
    let resolveResponse!: (value: Response) => void
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>((resolve) => {
      resolveResponse = resolve
    })))

    const pending = fetchFileBlob(7)
    localStorage.setItem("token", "new-token")
    resolveResponse({
      ok: false,
      status: 401,
      statusText: "Unauthorized",
      json: async () => ({ error: "unauthorized" }),
    } as Response)

    await expect(pending).rejects.toMatchObject({ status: 401 })

    expect(localStorage.getItem("token")).toBe("new-token")
    expect(window.location.href).toBe("")
  })

  it("clears auth state on 401 like the shared API client", async () => {
    localStorage.setItem("token", "expired-token")
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: false,
      status: 401,
      statusText: "Unauthorized",
      json: async () => ({ error: "unauthorized" }),
    }))

    await expect(fetchFileBlob(7)).rejects.toMatchObject({ status: 401 })

    expect(localStorage.getItem("token")).toBeNull()
    expect(window.location.href).toBe("/login")
  })

  it("throws backend readable errors for failed blob requests", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: false,
      status: 404,
      statusText: "Not Found",
      json: async () => ({ error: "file not found", code: "file_not_found", retryable: false }),
    }))

    await expect(fetchFileBlob(404)).rejects.toEqual(expect.objectContaining({
      name: "ApiError",
      status: 404,
      message: "file not found",
      code: "file_not_found",
      retryable: false,
    } satisfies Partial<ApiError>))
  })
})

describe("filesApi.preview", () => {
  it("passes an opaque cursor and normalizes paged preview metadata", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({
        file: {
          id: 7,
          user_id: 1,
          file_name: "notes.md",
          file_type: "text/markdown",
          file_size: 42,
          status: "ready",
          created_at: "2026-07-27T00:00:00Z",
        },
        content: "第二段",
        next_cursor: "djE6NDI",
        has_more: true,
        truncated: true,
      }),
    }))

    const preview = await filesApi.preview(7, 16000, "djE6MjE")

    expect(fetch).toHaveBeenCalledWith(
      "/api/v1/files/7/preview?max_chars=16000&cursor=djE6MjE",
      expect.objectContaining({ signal: expect.any(AbortSignal) })
    )
    expect(preview).toMatchObject({ content: "第二段", nextCursor: "djE6NDI", hasMore: true, truncated: true })
  })

  it("preserves preview state codes without parsing messages", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({
        file: {
          id: 8,
          user_id: 1,
          file_name: "pending.pdf",
          file_type: "application/pdf",
          file_size: 42,
          status: "staged",
          extract_status: "ocr_pending",
          created_at: "2026-07-27T00:00:00Z",
        },
        content: "",
        next_cursor: "",
        has_more: false,
        truncated: false,
        error: "no extracted text",
        code: "file_text_unavailable",
        retryable: true,
      }),
    }))

    await expect(filesApi.preview(8)).resolves.toMatchObject({
      error: "no extracted text",
      code: "file_text_unavailable",
      retryable: true,
    })
  })
})
