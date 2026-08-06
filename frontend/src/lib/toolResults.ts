export interface SearchResult {
  summary?: string
  citations?: SearchCitation[]
  source?: string
  attempted_sources?: string[]
  search_failed?: boolean
  error_code?: string
  error?: string
  retryable?: boolean
}

export interface SearchCitation {
  title?: string
  url?: string
  snippet?: string
}

export interface ExtractResult {
  ok?: boolean
  title?: string
  url?: string
  content?: string
  source?: string
  attempted_sources?: string[]
  summarized?: boolean
  detail?: string
  truncated?: boolean
  refinement_attempted?: boolean
  degraded?: boolean
  degradation_reason?: string
  error?: string
  error_code?: string
  retryable?: boolean
  status_code?: number
}

const extractDegradationMessages: Record<string, string> = {
  refinement_disabled: "未启用模型提炼，显示抓取原文",
  refinement_unavailable: "提炼模型当前不可用，显示抓取原文",
  refinement_cooldown: "提炼服务暂时冷却，显示抓取原文",
  refinement_failed: "模型提炼未完成，显示抓取原文",
  source_truncated: "网页原文过长，仅保留部分内容",
}

export interface ToolFailure {
  ok?: false
  tool?: string
  code?: string
  error?: string
  message?: string
  retryable?: boolean
  source?: string
}

export interface ToolLike {
  name?: string
  tool_name?: string
  function?: {
    name?: string
    arguments?: string
  }
  arguments?: string
}

export function getToolName(toolCall: ToolLike) {
  return toolCall.name || toolCall.tool_name || toolCall.function?.name || ""
}

export function getToolArguments(toolCall: ToolLike) {
  return toolCall.arguments || toolCall.function?.arguments || ""
}

export function parseToolArguments(value?: string): Record<string, unknown> | null {
  if (!value) return null
  try {
    const parsed = JSON.parse(value)
    return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? parsed as Record<string, unknown> : null
  } catch {
    return null
  }
}

export function searchQueryFromArguments(value?: string) {
  const parsed = parseToolArguments(value)
  const rawQuery = parsed?.query ?? parsed?.q ?? parsed?.keyword ?? parsed?.keywords
  if (Array.isArray(rawQuery)) return rawQuery.map((item) => cleanText(String(item))).filter(Boolean).join("、")
  if (typeof rawQuery === "string") return cleanText(rawQuery)
  return ""
}

export function toolNameLabel(name: string) {
  if (name === "web_search") return "联网搜索"
  if (name === "web_extract") return "读取网页"
  if (name === "file_list") return "列出文件"
  if (name === "file_search") return "搜索文件"
  if (name === "file_read") return "读取文件"
  return name || "工具调用"
}

export function toolSourceLabel(source?: string) {
  const normalized = source?.trim()
  if (!normalized) return ""
  return normalized
}

export function parseSearchResult(value?: string): SearchResult | null {
  if (!value) return null
  try {
    const parsed = JSON.parse(value) as SearchResult
    if (!Array.isArray(parsed.citations)) return parsed.search_failed ? parsed : null
    return {
      ...parsed,
      citations: parsed.citations.map(normalizeCitation).filter((item) => !!item.url),
    }
  } catch {
    return null
  }
}

export function parseExtractResult(value?: string): ExtractResult | null {
  if (!value) return null
  try {
    const parsed = JSON.parse(value) as ExtractResult
    return parsed.url || parsed.content || parsed.error ? parsed : null
  } catch {
    return null
  }
}

export function extractResultWarning(result: ExtractResult) {
  if (result.ok === false) return ""
  const message = result.degradation_reason ? extractDegradationMessages[result.degradation_reason] : ""
  if (message === extractDegradationMessages.source_truncated) return message
  if (message && result.truncated) return `${message}；网页原文过长，仅保留部分内容`
  if (message) return message
  if (result.truncated) return "内容已截断，仅显示部分结果"
  if (result.degraded) return "网页内容以降级方式返回，可能不完整"
  return ""
}

export function parseToolFailure(value?: string): ToolFailure | null {
  if (!value) return null
  try {
    const parsed = JSON.parse(value) as ToolFailure
    if (parsed && parsed.ok === false && parsed.error) return parsed
    const web = parsed as SearchResult & ExtractResult
    if (web && (web.search_failed || web.ok === false) && web.error) {
      return { ok: false, code: web.error_code, error: web.error, retryable: web.retryable, source: "web" }
    }
    return null
  } catch {
    return null
  }
}

export function normalizeCitation(item: SearchCitation): SearchCitation {
  const url = cleanUrl(item.url)
  return {
    title: cleanText(item.title) || hostOf(url),
    url,
    snippet: cleanText(item.snippet),
  }
}

export function cleanUrl(value?: string) {
  if (!value) return ""
  const first = value.trim().replace(/^[[(<"']+|[\])>"']+$/g, "").split(/\s|\\n|\n|\r/)[0]
  try {
    const parsed = new URL(first)
    return parsed.protocol === "http:" || parsed.protocol === "https:" ? parsed.href : ""
  } catch {
    return ""
  }
}

export function cleanText(value?: string) {
  if (!value) return ""
  return value
    .replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g, "$1")
    .replace(/\]\((https?:\/\/[^\s)]+)\)/g, "")
    .replace(/\s+/g, " ")
    .replace(/^[\s[\]()<>"]+|[\s[\]()<>"]+$/g, "")
    .trim()
}

export function hostOf(value?: string) {
  if (!value) return ""
  try {
    return new URL(value).host.replace(/^www\./, "")
  } catch {
    return ""
  }
}
