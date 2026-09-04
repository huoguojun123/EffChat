import { describe, expect, it } from "vitest"
import { summarizeUploadTasks } from "@/components/chat/uploadTaskPresentation"
import type { UploadTask } from "@/components/chat/useAttachmentUploadQueue"

function task(status: UploadTask["status"], overrides: Partial<UploadTask> = {}): UploadTask {
  return { id: "upload-1", filename: "notes.pdf", loaded: 0, total: 100, status, ...overrides }
}

describe("summarizeUploadTasks", () => {
  it("describes single-file progress", () => {
    expect(summarizeUploadTasks([task("uploading", { loaded: 42 })])).toEqual({
      active: true,
      label: "notes.pdf · 正在上传 42%",
    })
  })

  it("collapses multiple tasks and prioritizes processing state", () => {
    expect(summarizeUploadTasks([
      task("uploading", { loaded: 100 }),
      task("processing", { id: "upload-2", filename: "chart.png", loaded: 50 }),
    ])).toEqual({ active: true, label: "2 个文件 · 正在处理" })
  })

  it("routes failed tasks to the existing staging drawer", () => {
    expect(summarizeUploadTasks([task("failed")])).toEqual({
      active: false,
      label: "1 个文件需要处理，打开暂存附件查看",
    })
  })
})
