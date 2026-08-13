import { clearAccountScopedStorage } from "@/lib/accountStorage"

const BASE_URL = "/api/v1"
const DEFAULT_TIMEOUT_MS = 30000
const UPLOAD_TIMEOUT_MS = 180000

class ApiError extends Error {
  status: number
  code?: string
  retryable?: boolean
  requestId?: string

  constructor(status: number, message: string, details: { code?: string; retryable?: boolean; requestId?: string } = {}) {
    super(message)
    this.name = "ApiError"
    this.status = status
    this.code = details.code
    this.retryable = details.retryable
    this.requestId = details.requestId
  }
}

let authRedirecting = false

function getToken(): string | null {
  return localStorage.getItem("token")
}

export function handleAuthExpired(expectedToken?: string | null) {
  if (expectedToken !== undefined && expectedToken !== getToken()) return
  if (authRedirecting) return
  authRedirecting = true
  clearAccountScopedStorage()
  localStorage.removeItem("token")
  window.location.href = "/login"
}

export async function readApiError(res: Response, fallbackMessage = res.statusText): Promise<ApiError> {
  const body = await res.json().catch(() => null) as Record<string, unknown> | null
  const message = typeof body?.error === "string" && body.error ? body.error : fallbackMessage
  return new ApiError(res.status, message, {
    code: typeof body?.code === "string" ? body.code : undefined,
    retryable: typeof body?.retryable === "boolean" ? body.retryable : undefined,
    requestId: typeof body?.request_id === "string" ? body.request_id : undefined,
  })
}

async function request<T>(
  path: string,
  options: RequestInit & { timeoutMs?: number } = {}
): Promise<T> {
  const token = getToken()
  const { timeoutMs, ...fetchOptions } = options
  const headers: HeadersInit = {
    ...fetchOptions.headers,
  }

  if (!(fetchOptions.body instanceof FormData)) {
    (headers as Record<string, string>)["Content-Type"] = "application/json"
  }

  if (token) {
    (headers as Record<string, string>)["Authorization"] = `Bearer ${token}`
  }

  let res: Response
  try {
    res = await fetchWithTimeout(`${BASE_URL}${path}`, { ...fetchOptions, headers }, timeoutMs ?? DEFAULT_TIMEOUT_MS)
  } catch (err) {
    if (err instanceof ApiError) throw err
    throw new ApiError(0, "网络连接失败，请检查后端服务或网络")
  }

  if (!res.ok) {
    const error = await readApiError(res)
    if (res.status === 401 && token && !path.startsWith("/auth/")) handleAuthExpired(token)
    throw error
  }

  if (res.status === 204) return undefined as T

  return res.json()
}

export const api = {
  get: <T>(path: string, options?: { timeoutMs?: number }) => request<T>(path, options),
  post: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: "POST", body: body ? JSON.stringify(body) : undefined }),
  patch: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: "PATCH", body: body ? JSON.stringify(body) : undefined }),
  put: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: "PUT", body: body ? JSON.stringify(body) : undefined }),
  delete: <T>(path: string) => request<T>(path, { method: "DELETE" }),
  upload: <T>(path: string, formData: FormData) =>
    request<T>(path, { method: "POST", body: formData, timeoutMs: UPLOAD_TIMEOUT_MS }),
  download: async (path: string, options?: { timeoutMs?: number; signal?: AbortSignal }) => {
    const token = getToken()
    let res: Response
    try {
      res = await fetchWithTimeout(`${BASE_URL}${path}`, {
        headers: token ? { Authorization: `Bearer ${token}` } : undefined,
        signal: options?.signal,
      }, options?.timeoutMs)
    } catch (err) {
      if (err instanceof ApiError) throw err
      throw new ApiError(0, "网络连接失败，请检查后端服务或网络")
    }
    if (!res.ok) {
      const error = await readApiError(res)
      if (res.status === 401 && token) handleAuthExpired(token)
      throw error
    }
    return res
  },
}

export function downloadFilename(disposition: string | null) {
  if (!disposition) return ""
  const encoded = /filename\*=UTF-8''([^;]+)/i.exec(disposition)?.[1]
  if (encoded) {
    try {
      return decodeURIComponent(encoded)
    } catch {
      return ""
    }
  }
  return /filename="([^"]+)"/i.exec(disposition)?.[1] || /filename=([^;]+)/i.exec(disposition)?.[1]?.trim() || ""
}

export async function fetchWithTimeout(input: RequestInfo | URL, init: RequestInit = {}, timeoutMs = DEFAULT_TIMEOUT_MS): Promise<Response> {
  const controller = new AbortController()
  const timeout = globalThis.setTimeout(() => controller.abort(), timeoutMs)
  init.signal?.addEventListener("abort", () => controller.abort(), { once: true })
  try {
    return await fetch(input, { ...init, signal: controller.signal })
  } catch (err) {
    if (controller.signal.aborted || isAbortError(err)) {
      throw new ApiError(0, "请求超时，请稍后重试")
    }
    throw err
  } finally {
    globalThis.clearTimeout(timeout)
  }
}

function isAbortError(err: unknown) {
  return err instanceof DOMException && err.name === "AbortError"
}

export { ApiError }
