import type { AssistantSegment, Message, ToolCall } from "@/types"

export function normalizeMessages(messages: Message[]): Message[] {
  return mergeAssistantTurns(mergeToolMessages(messages.map(normalizeBackendMessage))).map((message) => ({
    ...message,
    local_state: "persisted",
    local_error: undefined,
    local_request_id: undefined,
    is_local: false,
  }))
}

function normalizeBackendMessage(message: Message): Message {
  const normalizedData = normalizeMessageData(message.message_data)
  return {
    ...message,
    message_data: normalizedData,
    has_reasoning: message.has_reasoning || Boolean(normalizedData.thinking?.trim()) || Boolean(normalizedData.segments?.some((segment) => segment.thinking?.trim())),
    has_tool_calls: message.has_tool_calls || Boolean(normalizedData.tool_calls?.length) || Boolean(normalizedData.segments?.some((segment) => segment.tool_calls?.length)),
  }
}

function normalizeMessageData(data: Message["message_data"]): Message["message_data"] {
  const thinking = typeof data.thinking === "string"
    ? data.thinking
    : typeof data.reasoning_content === "string"
      ? data.reasoning_content
      : undefined

  return {
    ...data,
    thinking,
    tool_calls: data.tool_calls?.map(normalizeToolCall),
    segments: data.segments?.map((segment) => ({
      ...segment,
      thinking: typeof segment.thinking === "string"
        ? segment.thinking
        : typeof (segment as AssistantSegment & { reasoning_content?: string }).reasoning_content === "string"
          ? (segment as AssistantSegment & { reasoning_content?: string }).reasoning_content
          : undefined,
      tool_calls: segment.tool_calls?.map(normalizeToolCall),
    })),
  }
}

function mergeToolMessages(messages: Message[]): Message[] {
  const toolResults = new Map<string, Message>()

  for (const msg of messages) {
    if (msg.role === "tool" || msg.message_data.role === "tool") {
      const toolCallId = msg.message_data.tool_call_id
      if (toolCallId) toolResults.set(toolCallId, msg)
    }
  }

  const visible: Message[] = []
  for (const msg of messages) {
    if (msg.role === "tool" || msg.message_data.role === "tool") continue
    if (msg.role === "assistant" || msg.message_data.role === "assistant") {
      const toolCalls = attachToolResults(msg.message_data.tool_calls || [], toolResults)
      visible.push({
        ...msg,
        message_data: {
          ...msg.message_data,
          tool_calls: toolCalls,
        },
      })
      continue
    }

    visible.push(msg)
  }

  return visible
}

function mergeAssistantTurns(messages: Message[]): Message[] {
  const merged: Message[] = []

  for (const msg of messages) {
    const last = merged[merged.length - 1]
    if (!canMergeAssistantMessages(last, msg)) {
      merged.push(msg)
      continue
    }

    merged[merged.length - 1] = {
      ...msg,
      created_at: last.created_at,
      has_tool_calls: last.has_tool_calls || msg.has_tool_calls,
      has_reasoning: last.has_reasoning || msg.has_reasoning,
      message_data: {
        ...last.message_data,
        ...msg.message_data,
        role: "assistant",
        content: joinAssistantText(last.message_data.content, msg.message_data.content) || "",
        thinking: joinAssistantText(last.message_data.thinking, msg.message_data.thinking),
        tool_calls: [...(last.message_data.tool_calls || []), ...(msg.message_data.tool_calls || [])],
        segments: [...assistantSegments(last), ...assistantSegments(msg)],
        response_meta: msg.message_data.response_meta || last.message_data.response_meta,
        runtime: msg.message_data.runtime || last.message_data.runtime,
      },
    }
  }

  return merged
}

function canMergeAssistantMessages(prev?: Message, next?: Message) {
  if (!prev || !next) return false
  if (prev.role !== "assistant" || next.role !== "assistant") return false
  if (prev.message_data.role !== "assistant" || next.message_data.role !== "assistant") return false
  if (isErrorAssistant(prev) || isErrorAssistant(next)) return false
  const prevRunId = messageRunId(prev)
  const nextRunId = messageRunId(next)
  if (prevRunId || nextRunId) return prevRunId === nextRunId
  return true
}

export function messageRunId(message: Message) {
  const metadata = message.message_data.metadata
  const runID = metadata?.run_id
  return typeof runID === "string" && runID.trim() ? runID.trim() : ""
}

export function isErrorAssistant(message: Message) {
  const metadata = message.message_data.metadata as { ephemeral_error?: boolean; error?: boolean } | undefined
  return Boolean(metadata?.error || metadata?.ephemeral_error)
}

export function editableTailUserMessageId(messages: Message[]): number | null {
  let targetIndex = -1
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const message = messages[index]
    if (isCompactionSummary(message)) continue
    if (message.role === "user" && message.message_data.role === "user") {
      targetIndex = index
      break
    }
  }
  if (targetIndex < 0) return null

  for (const message of messages.slice(targetIndex + 1)) {
    if (message.role === "tool" || message.message_data.role === "tool") return null
    if (message.role !== "assistant" && message.message_data.role !== "assistant") continue
    if (isErrorAssistant(message)) continue
    if (
      message.has_tool_calls
      || message.has_reasoning
      || message.message_data.content.trim()
      || message.message_data.thinking?.trim()
      || message.message_data.tool_calls?.length
      || message.message_data.segments?.some((segment) => (
        segment.content?.trim() || segment.thinking?.trim() || segment.tool_calls?.length
      ))
    ) {
      return null
    }
  }

  return messages[targetIndex].id
}

// isCompactionSummary 识别压缩检查点摘要消息（后端落库时打 metadata.compaction_summary=true）。
// 这类消息以 role=user 存储，前端不渲染成气泡，而是渲染成“以上对话已压缩”分割线。
export function isCompactionSummary(message: Message) {
  const metadata = message.message_data.metadata as { compaction_summary?: boolean } | undefined
  return metadata?.compaction_summary === true
}

// compactionKind 返回压缩摘要的来源："auto"（对话流自动触发）/"manual"（用户手动 /compact）。
// 自动压缩因撑爆阈值而触发，撤销后会立刻被重新压回，故前端不显示撤销按钮。
export function compactionKind(message: Message): "auto" | "manual" | undefined {
  const metadata = message.message_data.metadata as { compaction_kind?: string } | undefined
  if (metadata?.compaction_kind === "auto" || metadata?.compaction_kind === "manual") {
    return metadata.compaction_kind
  }
  return undefined
}

export function assistantErrorDetail(message: Message) {
  const metadata = message.message_data.metadata as { error_detail?: string } | undefined
  if (metadata?.error_detail) return metadata.error_detail
  if (typeof message.message_data.content === "string") {
    const content = message.message_data.content.trim()
    if (content.startsWith("请求失败：")) return content.replace(/^请求失败：/, "")
    if (isErrorAssistant(message)) return content
  }
  return message.local_error || ""
}

export function assistantErrorDiagnostic(message: Message) {
  const metadata = message.message_data.metadata as { error_diagnostic?: string } | undefined
  return metadata?.error_diagnostic?.trim() || ""
}

export function cloneToolCall(toolCall: ToolCall): ToolCall {
  return cloneToolCallInner(toolCall, new Set<ToolCall>())
}

function cloneToolCallInner(toolCall: ToolCall, seen: Set<ToolCall>): ToolCall {
  if (seen.has(toolCall)) return { ...toolCall, children: undefined }
  seen.add(toolCall)
  return {
    ...toolCall,
    children: toolCall.children
      ?.filter((child) => child !== toolCall)
      .map((child) => cloneToolCallInner(child, seen)),
  }
}

function joinAssistantText(a?: string, b?: string) {
  const left = (a || "").trim()
  const right = (b || "").trim()
  if (!left) return right || undefined
  if (!right) return left
  return `${left}\n\n${right}`
}

function assistantSegments(message: Message): AssistantSegment[] {
  if (message.message_data.segments?.length) return message.message_data.segments
  return [{
    content: message.message_data.content,
    thinking: message.message_data.thinking,
    tool_calls: message.message_data.tool_calls,
  }]
}

function attachToolResults(toolCalls: ToolCall[], toolResults: Map<string, Message>): ToolCall[] {
  return toolCalls.map((tc) => {
    const toolMessage = toolResults.get(tc.id)
    return {
      ...tc,
      result: toolMessage?.message_data.content ?? tc.result,
      status: toolMessage ? "done" : tc.status,
      children: tc.children ? attachToolResults(tc.children, toolResults) : undefined,
    }
  })
}

function normalizeToolCall(toolCall: ToolCall): ToolCall {
  return {
    ...toolCall,
    name: toolCall.name || toolCall.tool_name || toolCall.function?.name,
    arguments: toolCall.arguments || toolCall.function?.arguments,
    children: toolCall.children?.map(normalizeToolCall),
  }
}
