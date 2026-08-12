import { ApiError, api, downloadFilename, handleAuthExpired } from "./client"

interface FileRecord {
  id: number
  user_id: number
  session_id?: number | null
  file_name: string
  file_type: string
  file_size: number
  status: string
  extract_status?: string
  extract_error?: string
  token_estimate?: number
  ocr_provider?: string
  ocr_task_id?: string
  ocr_page_count?: number
  ocr_progress_pages?: number
  ocr_started_at?: string
  ocr_completed_at?: string
  ocr_error_type?: string
  created_at: string
}

export interface FileInfo {
  id: number
  user_id: number
  session_id?: number | null
  filename: string
  content_type: string
  size: number
  extractStatus?: string
  extractError?: string
  tokenEstimate?: number
  ocrProvider?: string
  ocrTaskId?: string
  ocrPageCount?: number
  ocrProgressPages?: number
  ocrErrorType?: string
  ocrStartedAt?: string
  ocrCompletedAt?: string
  created_at: string
}

interface FileListParams {
  sessionId?: number
  limit?: number
  offset?: number
  unreferenced?: boolean
  referenced?: boolean
}

export interface FileListResult {
  files: FileInfo[]
  hasMore: boolean
  nextOffset: number
}

export interface FilePreview {
  file: FileInfo
  content: string
  truncated: boolean
  nextCursor: string
  hasMore: boolean
  isImage?: boolean
  error?: string
  code?: string
  retryable?: boolean
}

export interface UploadLimits {
  maxFileSizeMb: number
  maxSessionFiles: number
  allowedTypes: string[]
}

export interface FileUploadOptions {
  signal?: AbortSignal
  onProgress?: (loaded: number, total: number) => void
  onProcessing?: () => void
}

function normalizeFile(file: FileRecord): FileInfo {
  return {
    id: file.id,
    user_id: file.user_id,
    session_id: file.session_id,
    filename: file.file_name,
    content_type: file.file_type,
    size: file.file_size,
    extractStatus: file.extract_status,
    extractError: file.extract_error,
    tokenEstimate: file.token_estimate,
    ocrProvider: file.ocr_provider,
    ocrTaskId: file.ocr_task_id,
    ocrPageCount: file.ocr_page_count || 0,
    ocrProgressPages: file.ocr_progress_pages || 0,
    ocrErrorType: file.ocr_error_type,
    ocrStartedAt: file.ocr_started_at,
    ocrCompletedAt: file.ocr_completed_at,
    created_at: file.created_at,
  }
}

export function isOCRPending(file: Pick<FileInfo, "extractStatus">): boolean {
  return file.extractStatus === "ocr_pending" || file.extractStatus === "ocr_running"
}

export function canRetryOCR(file: Pick<FileInfo, "extractStatus">): boolean {
  return file.extractStatus === "failed"
}

export function isOCRRefreshable(file: Pick<FileInfo, "extractStatus" | "ocrTaskId" | "ocrErrorType">): boolean {
  return isOCRPending(file)
}

export function isFileSendBlocked(file: Pick<FileInfo, "extractStatus" | "ocrTaskId" | "ocrErrorType">): boolean {
  return isOCRPending(file) || file.extractStatus === "failed"
}

export const filesApi = {
  async getUploadLimits(): Promise<UploadLimits> {
    const res = await api.get<{
      max_file_size_mb?: number
      max_session_files?: number
      allowed_types?: string[]
    }>("/files/upload-limits")
    return {
      maxFileSizeMb: typeof res.max_file_size_mb === "number" ? res.max_file_size_mb : 20,
      maxSessionFiles: typeof res.max_session_files === "number" ? res.max_session_files : 50,
      allowedTypes: Array.isArray(res.allowed_types) ? res.allowed_types : [],
    }
  },

  async upload(file: File, sessionId: number, options: FileUploadOptions = {}): Promise<FileInfo> {
    const form = new FormData()
    form.append("file", file)
    form.append("session_id", String(sessionId))
    const uploaded = await uploadFileRequest<FileRecord>(form, options)
    return normalizeFile(uploaded)
  },

  async list(params: FileListParams = {}): Promise<FileListResult> {
    const search = new URLSearchParams()
    if (params.sessionId) search.set("session_id", String(params.sessionId))
    if (params.limit) search.set("limit", String(params.limit))
    if (params.offset) search.set("offset", String(params.offset))
    if (params.unreferenced) search.set("unreferenced", "true")
    if (params.referenced) search.set("referenced", "true")
    const query = search.toString()
    const res = await api.get<{ files: FileRecord[]; has_more?: boolean; next_offset?: number }>(`/files${query ? `?${query}` : ""}`)
    const files = (res.files || []).map(normalizeFile)
    return {
      files,
      hasMore: Boolean(res.has_more),
      nextOffset: typeof res.next_offset === "number" ? res.next_offset : files.length,
    }
  },

  async preview(id: number, maxChars = 16000, cursor = ""): Promise<FilePreview> {
    const params = new URLSearchParams({ max_chars: String(maxChars) })
    if (cursor) params.set("cursor", cursor)
    const res = await api.get<{ file: FileRecord; content?: string; truncated?: boolean; next_cursor?: string; has_more?: boolean; is_image?: boolean; error?: string; code?: string; retryable?: boolean }>(`/files/${id}/preview?${params.toString()}`)
    return {
      file: normalizeFile(res.file),
      content: res.content || "",
      truncated: Boolean(res.truncated),
      nextCursor: res.next_cursor || "",
      hasMore: Boolean(res.has_more),
      isImage: Boolean(res.is_image),
      error: res.error,
      code: res.code,
      retryable: res.retryable,
    }
  },

  async refreshOCR(id: number): Promise<FileInfo> {
    return normalizeFile(await api.post<FileRecord>(`/files/${id}/ocr-refresh`, {}))
  },

  async retryOCR(id: number): Promise<FileInfo> {
    return normalizeFile(await api.post<FileRecord>(`/files/${id}/ocr-retry`, {}))
  },

  delete(id: number): Promise<void> {
    return api.delete(`/files/${id}`)
  },

  getDownloadUrl(id: number): string {
    return `/api/v1/files/${id}`
  },

  // 文件路由在鉴权组下，<a href> / <img src> 带不上 Authorization header，
  // 因此用带 token 的 fetch 拿 blob，再触发临时 <a download>。
  async downloadBlob(id: number, fallbackFilename: string, signal?: AbortSignal): Promise<void> {
    const { blob, filename } = await fetchFileDownload(id, signal)
    const url = URL.createObjectURL(blob)
    const a = document.createElement("a")
    a.href = url
    // The handler is authoritative: documents are served as extracted text,
    // while images retain their original binary filename. The fallback only
    // protects an older or intermediary response that dropped the header.
    a.download = filename || fallbackFilename
    document.body.appendChild(a)
    a.click()
    a.remove()
    window.setTimeout(() => URL.revokeObjectURL(url), 1000)
  },
}

function uploadFileRequest<T>(form: FormData, options: FileUploadOptions): Promise<T> {
  return new Promise((resolve, reject) => {
    const token = localStorage.getItem("token")
    const xhr = new XMLHttpRequest()
    let settled = false

    const finish = (callback: () => void) => {
      if (settled) return
      settled = true
      options.signal?.removeEventListener("abort", abort)
      callback()
    }
    const abort = () => xhr.abort()

    xhr.open("POST", "/api/v1/files")
    xhr.timeout = 180000
    if (token) xhr.setRequestHeader("Authorization", `Bearer ${token}`)
    xhr.upload.addEventListener("progress", (event) => {
      const total = event.lengthComputable ? event.total : 0
      options.onProgress?.(event.loaded, total)
    })
    xhr.upload.addEventListener("load", () => options.onProcessing?.())
    xhr.addEventListener("load", () => {
      const body = parseXHRBody(xhr.responseText)
      if (xhr.status < 200 || xhr.status >= 300) {
        if (xhr.status === 401 && token) handleAuthExpired(token)
        finish(() => reject(new ApiError(xhr.status, body.error || `上传失败 (HTTP ${xhr.status})`, {
          code: body.code,
          retryable: body.retryable,
          requestId: body.request_id,
        })))
        return
      }
      finish(() => resolve(body as T))
    })
    xhr.addEventListener("error", () => finish(() => reject(new ApiError(0, "网络连接失败，请检查后端服务或网络"))))
    xhr.addEventListener("timeout", () => finish(() => reject(new ApiError(0, "请求超时，请稍后重试"))))
    xhr.addEventListener("abort", () => finish(() => reject(new ApiError(0, "上传已取消", { code: "request_cancelled", retryable: true }))))

    if (options.signal?.aborted) {
      finish(() => reject(new ApiError(0, "上传已取消", { code: "request_cancelled", retryable: true })))
      return
    }
    options.signal?.addEventListener("abort", abort, { once: true })
    xhr.send(form)
  })
}

function parseXHRBody(raw: string): { error?: string; code?: string; retryable?: boolean; request_id?: string } & Record<string, unknown> {
  try {
    const parsed = JSON.parse(raw) as unknown
    return parsed && typeof parsed === "object" ? parsed as Record<string, unknown> : {}
  } catch {
    return {}
  }
}

// fetchFileBlob 用 localStorage 里的 token 鉴权拉取文件二进制，供下载与图片缩略图复用。
export async function fetchFileBlob(id: number, signal?: AbortSignal): Promise<Blob> {
  return (await fetchFileDownload(id, signal)).blob
}

async function fetchFileDownload(id: number, signal?: AbortSignal): Promise<{ blob: Blob; filename: string }> {
  const res = await api.download(`/files/${id}`, { timeoutMs: 60000, signal })
  return {
    blob: await res.blob(),
    filename: downloadFilename(res.headers.get("Content-Disposition")),
  }
}
