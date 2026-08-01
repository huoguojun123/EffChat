import { api, fetchWithTimeout, handleAuthExpired, readApiError } from "./client"

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

  async upload(file: File, sessionId: number): Promise<FileInfo> {
    const form = new FormData()
    form.append("file", file)
    form.append("session_id", String(sessionId))
    const uploaded = await api.upload<FileRecord>("/files", form)
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
  async downloadBlob(id: number, filename: string): Promise<void> {
    const blob = await fetchFileBlob(id)
    const url = URL.createObjectURL(blob)
    const a = document.createElement("a")
    a.href = url
    a.download = filename
    document.body.appendChild(a)
    a.click()
    a.remove()
    URL.revokeObjectURL(url)
  },
}

// fetchFileBlob 用 localStorage 里的 token 鉴权拉取文件二进制，供下载与图片缩略图复用。
export async function fetchFileBlob(id: number): Promise<Blob> {
  const token = localStorage.getItem("token")
  const res = await fetchWithTimeout(`/api/v1/files/${id}`, {
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  }, 60000)
  if (!res.ok) {
    const error = await readApiError(res, `下载失败 (HTTP ${res.status})`)
    if (res.status === 401 && token) handleAuthExpired(token)
    throw error
  }
  return res.blob()
}
