import { useState } from "react"
import { FileText, FileImage, FileSpreadsheet, FileCode, File } from "lucide-react"
import type { AttachmentMeta } from "@/types"
import { formatBytes } from "@/lib/format"
import { AttachmentImage } from "@/components/message/AttachmentImage"
import { DocumentPreview } from "@/components/files/DocumentPreview"
import { WorkspaceWindow } from "@/components/ui/workspace-window"

// 优先用 MIME 决定图标/配色，扩展名兜底（历史消息只有文件名时）。
function iconFor(att: AttachmentMeta) {
  const type = att.file_type || ""
  const ext = att.filename.split(".").pop()?.toLowerCase() || ""
  if (type.startsWith("image/") || ["png", "jpg", "jpeg", "gif", "webp", "svg", "bmp"].includes(ext))
    return { Icon: FileImage, tint: "text-violet-500", bg: "bg-violet-500/10" }
  if (
    type === "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" ||
    type === "text/csv" ||
    ["xls", "xlsx", "csv"].includes(ext)
  )
    return { Icon: FileSpreadsheet, tint: "text-emerald-600", bg: "bg-emerald-500/10" }
  if (["js", "ts", "tsx", "jsx", "go", "py", "java", "json", "html", "css", "sh"].includes(ext))
    return { Icon: FileCode, tint: "text-sky-500", bg: "bg-sky-500/10" }
  if (type === "application/pdf" || type.startsWith("text/") || ["pdf", "doc", "docx", "txt", "md"].includes(ext))
    return { Icon: FileText, tint: "text-rose-500", bg: "bg-rose-500/10" }
  return { Icon: File, tint: "text-muted-foreground", bg: "bg-muted" }
}

function isImage(att: AttachmentMeta) {
  if (att.file_type) return att.file_type.startsWith("image/")
  const ext = att.filename.split(".").pop()?.toLowerCase() || ""
  return ["png", "jpg", "jpeg", "gif", "webp", "bmp"].includes(ext)
}

export function AttachmentCard({ att }: { att: AttachmentMeta }) {
  const [open, setOpen] = useState(false)

  // 图片：直接渲染缩略图（鉴权 blob）。
  if (isImage(att) && att.file_id) {
    return <AttachmentImage att={att} />
  }

  const { Icon, tint, bg } = iconFor(att)
  const ext = att.filename.split(".").pop()?.toUpperCase() || "文件"
  const previewable = att.file_id > 0 && !att.unavailable

  if (att.unavailable) {
    return (
      <div className="inline-flex max-w-full items-center gap-2.5 rounded-xl border border-border/70 bg-muted/45 px-3 py-2 text-left text-muted-foreground sm:max-w-[260px]" title={`${att.filename} 已删除`}>
        <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-muted">
          <Icon className="h-5 w-5" />
        </div>
        <div className="min-w-0">
          <div className="truncate text-sm font-medium leading-tight">{att.filename}</div>
          <div className="text-xs">附件已删除</div>
        </div>
      </div>
    )
  }

  return (
    <>
      <button
        type="button"
        disabled={!previewable}
        onClick={() => setOpen(true)}
        className="inline-flex max-w-full items-center gap-2.5 rounded-xl border border-border/70 bg-card px-3 py-2 text-left shadow-sm transition-colors motion-control enabled:hover:bg-muted disabled:cursor-default sm:max-w-[260px]"
        title={previewable ? `预览 ${att.filename}` : att.filename}
      >
        <div className={`flex h-9 w-9 shrink-0 items-center justify-center rounded-lg ${bg}`}>
          <Icon className={`h-5 w-5 ${tint}`} />
        </div>
        <div className="min-w-0">
          <div className="truncate text-sm font-medium leading-tight">{att.filename}</div>
          <div className="text-xs text-muted-foreground">
            {ext}
            {att.size > 0 ? ` · ${formatBytes(att.size)}` : ""}
          </div>
        </div>
      </button>
      <WorkspaceWindow
        open={open}
        onOpenChange={setOpen}
        title={att.filename}
        defaultWidth={1080}
        defaultHeight={760}
      >
        <div className="h-full overflow-auto bg-muted/10 p-3 sm:p-6 scrollbar-thin">
          {open ? <DocumentPreview fileId={att.file_id} className="mx-auto w-full max-w-[1040px]" /> : null}
        </div>
      </WorkspaceWindow>
    </>
  )
}
