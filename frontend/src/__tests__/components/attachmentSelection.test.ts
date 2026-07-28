import { describe, expect, it } from "vitest"
import type { FileInfo } from "@/api/files"
import { defaultSelection, selectionAfterSent, selectManualAttachment } from "@/components/chat/useAttachmentUploadQueue"

function file(id: number, contentType = "image/png", extractStatus = "ready"): FileInfo {
  return { id, user_id: 1, session_id: 1, filename: `file-${id}`, content_type: contentType, size: 1, extractStatus, created_at: "2026-07-15T00:00:00Z" }
}

describe("attachment selection", () => {
  it("automatically batches 24 screenshots into the first four upload-order images", () => {
    const files = Array.from({ length: 24 }, (_, index) => file(index + 1))
    expect(defaultSelection(files).map((item) => item.id)).toEqual([1, 2, 3, 4])
  })

  it("keeps manual click order and rejects a fifth image", () => {
    const files = [file(1), file(2), file(3), file(4), file(5)]
    let selected: number[] = []
    for (const id of [3, 1, 4, 2]) selected = selectManualAttachment(files, selected, id).ids
    expect(selected).toEqual([3, 1, 4, 2])
    expect(selectManualAttachment(files, selected, 5)).toEqual({ ids: [3, 1, 4, 2], error: "本次已选 4/4 张图片" })
  })

  it("skips OCR-pending PDFs without blocking ready attachments", () => {
    const files = [file(1, "application/pdf", "ocr_running"), file(2, "text/plain"), file(3)]
    expect(defaultSelection(files).map((item) => item.id)).toEqual([2, 3])
  })

  it("rejects an eleventh non-image attachment with the current count", () => {
    const files = Array.from({ length: 11 }, (_, index) => file(index + 1, "text/plain"))
    const selected = files.slice(0, 10).map((item) => item.id)
    expect(selectManualAttachment(files, selected, 11)).toEqual({ ids: selected, error: "本次已选 10/10 个附件" })
  })

  it("keeps remaining staged attachments in manual mode after a partial send", () => {
    expect(selectionAfterSent({ manual: true, ids: [4, 2, 8] }, [4])).toEqual({ manual: true, ids: [2, 8] })
  })

  it("returns to automatic batching after the complete manual selection is sent", () => {
    expect(selectionAfterSent({ manual: true, ids: [3, 1, 4, 2] }, [3, 1, 4, 2])).toEqual({ manual: false, ids: [] })
  })
})
