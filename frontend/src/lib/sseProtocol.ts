import { handleAuthExpired } from "@/api/client"

export type StreamHTTPError = Error & { code?: string; retryable?: boolean; preserveMessageId?: number }

type ErrorPayload = {
  error?: unknown
  message?: unknown
  code?: unknown
  diagnostic?: unknown
  provider?: unknown
  model_id?: unknown
  finish_reason?: unknown
  limit?: number
  used?: number
  reset_at?: string
  retryable?: boolean
  preserve_message_id?: number
}

const modelUnavailableCodes = new Set([
  "channel_not_configured",
  "channel_disabled",
  "channel_api_key_missing",
  "session_model_missing",
  "session_model_channel_mismatch",
  "session_model_disabled",
])

export async function createStreamHTTPError(res: Response, expectedToken?: string | null): Promise<StreamHTTPError> {
  if (res.status === 401) handleAuthExpired(expectedToken)
  const fallback = `HTTP ${res.status}`
  const body = await res.json().catch(() => null) as ErrorPayload | null
  let message = fallback
  if (res.status === 429 && body?.code) {
    const reset = body.reset_at ? `，重置时间 ${formatResetTime(body.reset_at)}` : ""
    const usage = typeof body.used === "number" && typeof body.limit === "number" ? `（${body.used}/${body.limit}）` : ""
    message = `${body.error || "已达到使用限额"}${usage}${reset}`
  } else if (body) {
    message = formatErrorPayload(body, fallback)
  }
  const error = new Error(message) as StreamHTTPError
  if (typeof body?.code === "string") error.code = body.code
  if (typeof body?.retryable === "boolean") error.retryable = body.retryable
  if (typeof body?.preserve_message_id === "number" && body.preserve_message_id > 0) error.preserveMessageId = body.preserve_message_id
  return error
}

export async function readStreamHTTPError(res: Response, expectedToken?: string | null) {
  return (await createStreamHTTPError(res, expectedToken)).message
}

export function isCompactionRequired(err: unknown): err is StreamHTTPError {
  return err instanceof Error && (err as StreamHTTPError).code === "compaction_required"
}

export async function readSSEEvents(response: Response, onEvent: (event: string, data: string) => void | Promise<void>) {
  const reader = response.body!.getReader()
  const decoder = new TextDecoder()
  let buffer = ""

  const emit = async (frame: string) => {
    let event = ""
    const data: string[] = []
    for (const line of frame.split(/\r?\n/)) {
      if (line.startsWith("event:")) event = line.slice(6).trim()
      else if (line.startsWith("data:")) data.push(line.slice(5).replace(/^ /, ""))
    }
    if (event) await onEvent(event, data.join("\n"))
  }

  const drain = async (flush = false) => {
    while (true) {
      const boundary = /\r?\n\r?\n/.exec(buffer)
      if (!boundary || boundary.index === undefined) break
      const frame = buffer.slice(0, boundary.index)
      buffer = buffer.slice(boundary.index + boundary[0].length)
      await emit(frame)
    }
    if (flush && buffer) {
      const frame = buffer
      buffer = ""
      await emit(frame)
    }
  }

  try {
    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })
      await drain()
    }
    buffer += decoder.decode()
    await drain(true)
  } catch (error) {
    await reader.cancel(error).catch(() => undefined)
    throw error
  } finally {
    reader.releaseLock()
  }
}

export function formatErrorPayload(body: ErrorPayload, fallback: string) {
  const code = typeof body.code === "string" ? body.code : ""
  const error = typeof body.error === "string" ? body.error : fallback
  const provider = typeof body.provider === "string" && body.provider ? `渠道 ${body.provider}` : ""
  const model = typeof body.model_id === "string" && body.model_id ? `模型 ${body.model_id}` : ""
  const subject = [provider, model].filter(Boolean).join("，")

  if (modelUnavailableCodes.has(code)) {
    return `${subject ? `${subject}：` : ""}${error}。请在上方模型切换器选择一个可用模型。`
  }
  if (code === "model_empty_response") {
    const finish = typeof body.finish_reason === "string" && body.finish_reason ? `（finish_reason=${body.finish_reason}）` : ""
    return `${subject ? `${subject}：` : ""}${error}${finish}`
  }
  return error
}

export function formatErrorDiagnostic(body: ErrorPayload) {
  return typeof body.diagnostic === "string" ? body.diagnostic.trim() : ""
}

export function trimTrailingCodePoints(value: string, count: number) {
  if (count <= 0 || !value) return value
  const points = Array.from(value)
  return points.slice(0, Math.max(0, points.length - count)).join("")
}

export async function consumeCompactionSSE(response: Response): Promise<"complete" | "skip"> {
  let outcome: "complete" | "skip" | null = null

  await readSSEEvents(response, (event, data) => {
    if (event === "compaction_complete") outcome = "complete"
    else if (event === "compaction_skip") outcome = "skip"
    else if (event === "error") throw new Error(formatCompactionError(data))
  })
  if (!outcome) throw new Error("压缩结果未确认，请重试")
  return outcome
}

export async function consumeMemoryMaintenanceSSE(response: Response): Promise<void> {
  let completed = false
  await readSSEEvents(response, (event, data) => {
    if (event === "memory_maintenance_complete") completed = true
    else if (event === "memory_maintenance_canceled") throw new Error("记忆维护已取消")
    else if (event === "error") throw new Error(formatCompactionError(data).replace("压缩", "记忆维护"))
  })
  if (!completed) throw new Error("记忆维护结果未确认，请重试")
}

export function parseUsage(value: unknown) {
  if (!value || typeof value !== "object") return undefined
  const usage = value as Record<string, unknown>
  const promptDetails = typeof usage.prompt_token_details === "object" && usage.prompt_token_details
    ? usage.prompt_token_details as Record<string, unknown>
    : undefined
  const completionDetails = typeof usage.completion_token_details === "object" && usage.completion_token_details
    ? usage.completion_token_details as Record<string, unknown>
    : undefined
  return {
    prompt_tokens: Number(usage.prompt_tokens || 0),
    completion_tokens: Number(usage.completion_tokens || 0),
    total_tokens: Number(usage.total_tokens || 0),
    cached_tokens: Number(usage.cached_tokens ?? promptDetails?.cached_tokens ?? 0),
    reasoning_tokens: Number(usage.reasoning_tokens ?? completionDetails?.reasoning_tokens ?? 0),
  }
}

function formatCompactionError(raw: string) {
  const fallback = "压缩失败，请联系管理员"
  try {
    const body = JSON.parse(raw || "{}") as ErrorPayload
    if (typeof body.message === "string" && body.message) return body.message
    if (typeof body.error === "string" && body.error) return body.error
  } catch {
    return fallback
  }
  return fallback
}

function formatResetTime(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString()
}
