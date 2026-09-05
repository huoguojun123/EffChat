import type { UploadTask } from "./useAttachmentUploadQueue"

export function summarizeUploadTasks(tasks: UploadTask[]) {
  if (tasks.length === 0) return null
  const activeTasks = tasks.filter((task) => task.status === "queued" || task.status === "uploading" || task.status === "processing")
  const failedTasks = tasks.filter((task) => task.status === "failed" || task.status === "cancelled")
  if (activeTasks.length === 0) {
    return { active: false, label: `${failedTasks.length || tasks.length} 个文件需要处理，打开暂存附件查看` }
  }
  const processing = activeTasks.some((task) => task.status === "processing")
  const total = activeTasks.reduce((sum, task) => sum + Math.max(0, task.total), 0)
  const loaded = activeTasks.reduce((sum, task) => sum + Math.max(0, Math.min(task.loaded, task.total || task.loaded)), 0)
  const percent = total > 0 ? Math.min(100, Math.round(loaded / total * 100)) : 0
  const subject = activeTasks.length === 1 ? activeTasks[0].filename : `${activeTasks.length} 个文件`
  const stage = processing ? "正在处理" : percent > 0 ? `正在上传 ${percent}%` : "等待上传"
  return { active: true, label: `${subject} · ${stage}` }
}
