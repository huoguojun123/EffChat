import { useState } from "react"
import { FileImage } from "lucide-react"
import { useAuthedBlobUrl } from "@/hooks/useAuthedBlobUrl"
import type { AttachmentMeta } from "@/types"
import { filesApi } from "@/api/files"
import { ImageLightbox } from "./ImageLightbox"

// AttachmentImage renders an authenticated thumbnail and reuses the full-screen viewer.
export function AttachmentImage({ att }: { att: AttachmentMeta }) {
  const { url, loading, error } = useAuthedBlobUrl(att.unavailable ? undefined : att.file_id || undefined)
  const [open, setOpen] = useState(false)

  if (att.unavailable || error || (!att.file_id && !url)) {
    return (
      <div className="inline-flex h-24 w-28 flex-col items-center justify-center gap-2 rounded-lg border border-border/70 bg-muted text-xs text-muted-foreground sm:h-28 sm:w-36">
        <FileImage className="h-6 w-6" />
        附件已删除
      </div>
    )
  }

  return (
    <>
      <button
        type="button"
        onClick={() => url && setOpen(true)}
        className="block h-24 w-28 overflow-hidden rounded-lg border border-border/70 bg-muted/40 p-1 transition-[border-color,background-color] motion-control hover:border-ring/50 hover:bg-muted/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring sm:h-28 sm:w-36"
        title={att.filename}
      >
        {loading || !url ? (
          <div className="h-full w-full animate-pulse rounded-md bg-muted-foreground/10" />
        ) : (
          <img src={url} alt={att.filename} className="h-full w-full rounded-md object-contain" />
        )}
      </button>
      {url ? (
        <ImageLightbox
          open={open}
          url={url}
          filename={att.filename}
          onOpenChange={setOpen}
          onDownload={att.file_id ? () => filesApi.downloadBlob(att.file_id!, att.filename) : undefined}
        />
      ) : null}
    </>
  )
}
