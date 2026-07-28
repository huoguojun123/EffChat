import type { AttachmentMeta } from "@/types"

// 附件渲染的两条信号源：
//   - 新消息：message_data.attachments 结构化数组（首选，含 filename/type/size/file_id）。
//   - 历史消息：正文里的 `[附件：filename]` 文本行（向后兼容兜底，仅有文件名）。
// 后端在发送给模型时才注入提取正文，content 本身保持干净，所以这里是纯展示层解析。
const ATTACHMENT_RE = /^\[附件：(.+?)\]$/

export interface ParsedContent {
  files: AttachmentMeta[]
  text: string
}

export function parseAttachments(content: string, attachments?: AttachmentMeta[]): ParsedContent {
  // 新路径：结构化 attachments 存在时直接用，content 即正文无需剥离。
  if (attachments && attachments.length > 0) {
    return { files: attachments, text: content ?? "" }
  }

  // 兼容旧消息：从正文开头连续的 `[附件：...]` 行解析，构造仅含文件名的占位 meta。
  if (!content) return { files: [], text: "" }
  const lines = content.split("\n")
  const names: string[] = []
  let i = 0
  for (; i < lines.length; i++) {
    const m = lines[i].match(ATTACHMENT_RE)
    if (m) {
      names.push(m[1])
    } else if (lines[i].trim() === "" && names.length > 0) {
      continue
    } else {
      break
    }
  }
  if (names.length === 0) return { files: [], text: content }
  const files: AttachmentMeta[] = names.map((filename) => ({
    file_id: 0,
    filename,
    file_type: "",
    size: 0,
  }))
  return { files, text: lines.slice(i).join("\n").trim() }
}
