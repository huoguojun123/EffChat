import type { Message } from "@/types"
import type { ConversationTurnIndex } from "@/api/messages"
import { isCompactionSummary } from "@/lib/chatMessages"

export interface ConversationTurn {
  id: number
  userMessageId: number
  title: string
  userPreview: string
  assistantPreview: string
  sequence: number
  createdAt: string
}

export function buildConversationTurns(messages: Message[], index: ConversationTurnIndex[] = []) {
  const localTurns: ConversationTurn[] = []

  for (const message of messages) {
    if (isCompactionSummary(message)) continue
    if (message.message_data.role === "user") {
      const userPreview = previewText(message.message_data.content) || (message.message_data.attachments?.length ? "附件消息" : "空消息")
      localTurns.push({
        id: message.id,
        userMessageId: message.id,
        title: userPreview.slice(0, 28),
        userPreview,
        assistantPreview: "",
        sequence: localTurns.length + 1,
        createdAt: message.created_at,
      })
      continue
    }

    if (message.message_data.role === "assistant" && localTurns.length > 0 && !localTurns.at(-1)?.assistantPreview) {
      localTurns[localTurns.length - 1].assistantPreview = previewText(message.message_data.content)
    }
  }

  if (index.length === 0) return localTurns
  const loadedByID = new Map(localTurns.map((turn) => [turn.id, turn]))
  const indexed = index.map((turn) => {
    const loaded = loadedByID.get(turn.id)
    return {
      id: turn.id,
      userMessageId: turn.user_message_id,
      title: turn.user_preview.slice(0, 28),
      userPreview: turn.user_preview,
      assistantPreview: loaded?.assistantPreview || "",
      sequence: turn.sequence,
      createdAt: turn.created_at,
    }
  })
  const indexedIDs = new Set(indexed.map((turn) => turn.id))
  return [...indexed, ...localTurns.filter((turn) => !indexedIDs.has(turn.id))]
}

export function conversationTurnMarkerRange(count: number, scrollTop: number, viewportHeight: number, rowHeight = 10, overscan = 20) {
  if (count <= 500) return { start: 0, end: count }
  const start = Math.max(0, Math.floor(scrollTop / rowHeight) - overscan)
  const end = Math.min(count, start + Math.ceil(viewportHeight / rowHeight) + overscan * 2)
  return { start, end }
}

export function conversationTurnRailMode(count: number) {
  return { scrollable: count > 60, virtual: count > 500 }
}

// Scrollable rails render the preview outside the overflow viewport. Keeping
// this coordinate calculation here makes virtual and non-virtual rails share
// the same positioning contract.
export function conversationTurnPreviewTop(index: number, scrollTop: number, rowHeight = 10) {
  return index * rowHeight - scrollTop + rowHeight / 2
}

function previewText(content: string) {
  return content
    .replace(/```[\s\S]*?```/g, " [代码] ")
    .replace(/!\[[^\]]*]\([^)]*\)/g, " [图片] ")
    .replace(/\[([^\]]+)]\([^)]*\)/g, "$1")
    .replace(/[*_~`>#-]+/g, " ")
    .replace(/\s+/g, " ")
    .trim()
    .slice(0, 110)
}
