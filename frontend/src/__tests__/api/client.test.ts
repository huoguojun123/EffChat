import { beforeEach, describe, expect, it, vi } from "vitest"
import { api, fetchWithTimeout } from "@/api/client"

const localStorageData = new Map<string, string>()

beforeEach(() => {
  vi.clearAllMocks()
  vi.useRealTimers()
  localStorageData.clear()
  vi.stubGlobal("localStorage", {
    getItem: vi.fn((key: string) => localStorageData.get(key) ?? null),
    setItem: vi.fn((key: string, value: string) => localStorageData.set(key, String(value))),
    removeItem: vi.fn((key: string) => localStorageData.delete(key)),
  })
  vi.stubGlobal("window", { location: { href: "" } })
})

describe("api client", () => {
  it("wraps network failures in a readable ApiError", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("Failed to fetch")))

    await expect(api.get("/health")).rejects.toMatchObject({
      name: "ApiError",
      status: 0,
      message: "网络连接失败，请检查后端服务或网络",
    })
  })

  it("turns request timeout into a readable ApiError", async () => {
    vi.useFakeTimers()
    vi.stubGlobal("fetch", vi.fn((_input: RequestInfo | URL, init?: RequestInit) => new Promise((_resolve, reject) => {
      init?.signal?.addEventListener("abort", () => reject(new DOMException("Aborted", "AbortError")))
    })))

    const pending = expect(fetchWithTimeout("/slow", {}, 10)).rejects.toMatchObject({
      name: "ApiError",
      status: 0,
      message: "请求超时，请稍后重试",
    })
    await vi.advanceTimersByTimeAsync(11)

    await pending
  })

  it("does not evict a newer login when a stale shared request receives 401", async () => {
    localStorage.setItem("token", "old-token")
    let resolveResponse!: (value: Response) => void
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>((resolve) => {
      resolveResponse = resolve
    })))

    const pending = api.get("/sessions")
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
})
