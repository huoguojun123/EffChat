import { clearAccountScopedStorage } from "@/lib/accountStorage"

const BASE_URL = "/api/v1"
const DEFAULT_TIMEOUT_MS = 30000
const UPLOAD_TIMEOUT_MS = 180000

class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.name = "ApiError"
    this.status = status
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

export async function readApiErrorMessage(res: Response): Promise<string> {
  const body = await res.json().catch(() => ({ error: res.statusText }))
  return typeof body.error === "string" && body.error ? body.error : res.statusText
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
    const message = await readApiErrorMessage(res)
    if (res.status === 401 && token && !path.startsWith("/auth/")) handleAuthExpired(token)
    throw new ApiError(res.status, message)
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
  download: async (path: string) => {
    const token = getToken()
    let res: Response
    try {
      res = await fetchWithTimeout(`${BASE_URL}${path}`, {
        headers: token ? { Authorization: `Bearer ${token}` } : undefined,
      })
    } catch (err) {
      if (err instanceof ApiError) throw err
      throw new ApiError(0, "网络连接失败，请检查后端服务或网络")
    }
    if (!res.ok) {
      const message = await readApiErrorMessage(res)
      if (res.status === 401 && token) handleAuthExpired(token)
      throw new ApiError(res.status, message)
    }
    return res
  },
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
