import { beforeEach, describe, expect, it, vi } from "vitest"
import type { ApiError } from "@/api/client"
import { fetchFileBlob, filesApi } from "@/api/files"

const localStorageData = new Map<string, string>()

class FakeXMLHttpRequest {
  static instances: FakeXMLHttpRequest[] = []
  upload = new EventTarget()
  status = 0
  responseText = ""
  timeout = 0
  private events = new EventTarget()
  abort = vi.fn(() => this.events.dispatchEvent(new Event("abort")))
  open = vi.fn()
  setRequestHeader = vi.fn()
  send = vi.fn()

  constructor() {
    FakeXMLHttpRequest.instances.push(this)
  }

  addEventListener(type: string, listener: EventListenerOrEventListenerObject) {
    this.events.addEventListener(type, listener)
  }

  respond(status: number, body: unknown) {
    this.status = status
    this.responseText = JSON.stringify(body)
    this.events.dispatchEvent(new Event("load"))
  }

  fail(type: "error" | "timeout") {
    this.events.dispatchEvent(new Event(type))
  }
}

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
  FakeXMLHttpRequest.instances = []
})

describe("filesApi.upload", () => {
  it("reports byte progress and processing before normalizing success", async () => {
    vi.stubGlobal("XMLHttpRequest", FakeXMLHttpRequest)
    const onProgress = vi.fn()
    const onProcessing = vi.fn()
    const pending = filesApi.upload(new File(["hello"], "note.txt", { type: "text/plain" }), 11, { onProgress, onProcessing })
    const xhr = FakeXMLHttpRequest.instances[0]

    const progress = new Event("progress")
    Object.defineProperties(progress, {
      lengthComputable: { value: true },
      loaded: { value: 3 },
      total: { value: 5 },
    })
    xhr.upload.dispatchEvent(progress)
    xhr.upload.dispatchEvent(new Event("load"))
    xhr.respond(201, {
      id: 7, user_id: 1, session_id: 11, file_name: "note.txt", file_type: "text/plain",
      file_size: 5, status: "staged", extract_status: "ready", created_at: "2026-08-12T00:00:00Z",
    })

    await expect(pending).resolves.toMatchObject({ id: 7, filename: "note.txt", content_type: "text/plain" })
    expect(onProgress).toHaveBeenCalledWith(3, 5)
    expect(onProcessing).toHaveBeenCalledOnce()
  })

  it("preserves structured failure metadata and aborts from the caller signal", async () => {
    vi.stubGlobal("XMLHttpRequest", FakeXMLHttpRequest)
    const failed = filesApi.upload(new File(["x"], "bad.bin"), 11)
    FakeXMLHttpRequest.instances[0].respond(415, { error: "file type not allowed", code: "file_type_not_allowed", retryable: false })
    await expect(failed).rejects.toMatchObject({ status: 415, code: "file_type_not_allowed", retryable: false })

    const controller = new AbortController()
    const cancelled = filesApi.upload(new File(["x"], "cancel.txt"), 11, { signal: controller.signal })
    const xhr = FakeXMLHttpRequest.instances[1]
    controller.abort()

    await expect(cancelled).rejects.toMatchObject({ code: "request_cancelled", retryable: true })
    expect(xhr.abort).toHaveBeenCalledOnce()
  })

  it("maps network and timeout failures to the shared readable errors", async () => {
    vi.stubGlobal("XMLHttpRequest", FakeXMLHttpRequest)
    const network = filesApi.upload(new File(["x"], "network.txt"), 11)
    FakeXMLHttpRequest.instances[0].fail("error")
    await expect(network).rejects.toMatchObject({ status: 0, message: "网络连接失败，请检查后端服务或网络" })

    const timeout = filesApi.upload(new File(["x"], "timeout.txt"), 11)
    FakeXMLHttpRequest.instances[1].fail("timeout")
    await expect(timeout).rejects.toMatchObject({ status: 0, message: "请求超时，请稍后重试" })
  })

  it("does not clear a newer login when an old upload receives 401", async () => {
    vi.stubGlobal("XMLHttpRequest", FakeXMLHttpRequest)
    localStorage.setItem("token", "old-token")
    const pending = filesApi.upload(new File(["x"], "auth.txt"), 11)
    localStorage.setItem("token", "new-token")
    FakeXMLHttpRequest.instances[0].respond(401, { error: "unauthorized" })

    await expect(pending).rejects.toMatchObject({ status: 401 })
    expect(localStorage.getItem("token")).toBe("new-token")
    expect(window.location.href).toBe("")
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

  it("wraps download network failures in the shared readable error", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("network down")))

    await expect(fetchFileBlob(7)).rejects.toMatchObject({
      name: "ApiError",
      status: 0,
      message: "网络连接失败，请检查后端服务或网络",
    })
  })
})

describe("filesApi.downloadBlob", () => {
  it("prefers the handler filename over stale attachment metadata", async () => {
    const anchor = { href: "", download: "", click: vi.fn(), remove: vi.fn() }
    vi.stubGlobal("document", {
      createElement: vi.fn(() => anchor),
      body: { appendChild: vi.fn() },
    })
    vi.stubGlobal("URL", { createObjectURL: vi.fn(() => "blob:fixture"), revokeObjectURL: vi.fn() })
    vi.stubGlobal("window", { location: { href: "" }, setTimeout: vi.fn() })
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("extracted workbook text", {
      headers: { "Content-Disposition": "attachment; filename*=UTF-8''budget.xlsx.txt" },
    })))

    await filesApi.downloadBlob(7, "budget.xlsx")

    expect(anchor.download).toBe("budget.xlsx.txt")
    expect(anchor.click).toHaveBeenCalledOnce()
  })

  it("uses the explicit safe fallback only when a response omits Content-Disposition", async () => {
    const anchor = { href: "", download: "", click: vi.fn(), remove: vi.fn() }
    vi.stubGlobal("document", {
      createElement: vi.fn(() => anchor),
      body: { appendChild: vi.fn() },
    })
    vi.stubGlobal("URL", { createObjectURL: vi.fn(() => "blob:fixture"), revokeObjectURL: vi.fn() })
    vi.stubGlobal("window", { location: { href: "" }, setTimeout: vi.fn() })
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("extracted text")))

    await filesApi.downloadBlob(7, "budget.xlsx.txt")

    expect(anchor.download).toBe("budget.xlsx.txt")
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
