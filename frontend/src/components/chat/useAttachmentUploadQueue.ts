import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react"
import { filesApi, isFileSendBlocked, isOCRRefreshable, type FileInfo, type UploadLimits } from "@/api/files"
import { compressImageIfNeeded } from "@/lib/imageCompress"
import { DEFAULT_UPLOAD_LIMITS } from "./ChatInput.constants"
import { isAcceptedFile } from "./chatInputUpload"
import { AttachmentQueueOwnership } from "./attachmentQueueOwnership"

function mergeFilesByID(current: FileInfo[], incoming: FileInfo[]): FileInfo[] {
  const byID = new Map<number, FileInfo>()
  for (const file of current) byID.set(file.id, file)
  for (const file of incoming) byID.set(file.id, file)
  return Array.from(byID.values()).sort((a, b) => {
    const at = Date.parse(a.created_at || "")
    const bt = Date.parse(b.created_at || "")
    if (Number.isNaN(at) || Number.isNaN(bt) || at === bt) return a.id - b.id
    return at - bt
  })
}

export const MAX_MESSAGE_ATTACHMENTS = 10
export const MAX_VISION_IMAGES = 4

export type UploadTaskStatus = "queued" | "uploading" | "processing" | "failed" | "cancelled"

export interface UploadTask {
  id: string
  filename: string
  loaded: number
  total: number
  status: UploadTaskStatus
  error?: string
  retryable?: boolean
}

interface UploadTaskRuntime extends UploadTask {
  sourceFile: File
  uploadFile: File
  sessionId: number
  sessionEpoch: number
}

interface StoredSelection {
  manual: boolean
  ids: number[]
}

export function selectionAfterSent(selection: StoredSelection, sentIDs: number[]): StoredSelection {
  const sent = new Set(sentIDs)
  const ids = selection.ids.filter((id) => !sent.has(id))
  return { manual: selection.manual && ids.length > 0, ids }
}

function selectionKey(sessionId: number) {
  return `effchat:staged-selection:${sessionId}`
}

function readSelection(sessionId: number): StoredSelection {
  try {
    const value = JSON.parse(localStorage.getItem(selectionKey(sessionId)) || "") as StoredSelection
    if (typeof value?.manual === "boolean" && Array.isArray(value.ids)) return { manual: value.manual, ids: value.ids.filter(Number.isSafeInteger) }
  } catch {
    return { manual: false, ids: [] }
  }
  return { manual: false, ids: [] }
}

export function fitsMessageLimit(files: FileInfo[]) {
  return files.length <= MAX_MESSAGE_ATTACHMENTS && files.filter((file) => file.content_type.startsWith("image/")).length <= MAX_VISION_IMAGES
}

export function defaultSelection(files: FileInfo[]) {
  const selected: FileInfo[] = []
  for (const file of files) {
    if (isFileSendBlocked(file) || !fitsMessageLimit([...selected, file])) continue
    selected.push(file)
  }
  return selected
}

export function selectManualAttachment(files: FileInfo[], selectedIDs: number[], id: number): { ids: number[]; error?: string } {
  const file = files.find((item) => item.id === id)
  if (!file || isFileSendBlocked(file)) return { ids: selectedIDs }
  const available = selectedIDs.filter((selectedID) => files.some((item) => item.id === selectedID && !isFileSendBlocked(item)))
  const ids = available.includes(id) ? available.filter((selectedID) => selectedID !== id) : [...available, id]
  const selected = ids.map((selectedID) => files.find((item) => item.id === selectedID)).filter((item): item is FileInfo => !!item)
  if (fitsMessageLimit(selected)) return { ids }
  const imageCount = available.filter((selectedID) => files.find((item) => item.id === selectedID)?.content_type.startsWith("image/")).length
  return {
    ids: available,
    error: selected.length > MAX_MESSAGE_ATTACHMENTS
      ? `本次已选 ${available.length}/${MAX_MESSAGE_ATTACHMENTS} 个附件`
      : `本次已选 ${imageCount}/${MAX_VISION_IMAGES} 张图片`,
  }
}

export function useAttachmentUploadQueue(activeSessionId?: number | null) {
  const [attachments, setAttachments] = useState<FileInfo[]>([])
  const [selection, setSelection] = useState<StoredSelection>({ manual: false, ids: [] })
  const [uploading, setUploading] = useState(false)
  const [uploadTasks, setUploadTasks] = useState<UploadTaskRuntime[]>([])
  const [uploadError, setUploadError] = useState<string | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const uploadLimitsRef = useRef<UploadLimits>(DEFAULT_UPLOAD_LIMITS)
  const ocrRefreshInFlightRef = useRef<Set<number>>(new Set())
  const activeSessionRef = useRef(activeSessionId)
  const uploadCountBySessionRef = useRef<Map<number, number>>(new Map())
  const ownershipRef = useRef(new AttachmentQueueOwnership())
  const uploadControllersRef = useRef<Map<string, AbortController>>(new Map())
  const cancelledUploadTasksRef = useRef<Set<string>>(new Set())
  const uploadTaskSequenceRef = useRef(0)
  const errorTimerRef = useRef<number | null>(null)
  const ocrGenerationRef = useRef<Map<number, number>>(new Map())

  const flashUploadError = useCallback((message: string) => {
    const generation = ownershipRef.current.beginError()
    if (errorTimerRef.current !== null) window.clearTimeout(errorTimerRef.current)
    setUploadError(message)
    errorTimerRef.current = window.setTimeout(() => {
      if (ownershipRef.current.ownsError(generation)) setUploadError(null)
    }, 10000)
  }, [])

  const updateUploadTask = useCallback((id: string, update: Partial<UploadTaskRuntime>) => {
    setUploadTasks((current) => current.map((task) => task.id === id ? { ...task, ...update } : task))
  }, [])

  const persistSelection = useCallback((sessionId: number, next: StoredSelection) => {
    localStorage.setItem(selectionKey(sessionId), JSON.stringify(next))
    if (activeSessionRef.current === sessionId) setSelection(next)
  }, [])

  const refreshUploadLimits = useCallback(async () => {
    try {
      const next = await filesApi.getUploadLimits()
      const normalized: UploadLimits = {
        ...DEFAULT_UPLOAD_LIMITS,
        ...next,
        allowedTypes: next.allowedTypes.length > 0 ? next.allowedTypes : DEFAULT_UPLOAD_LIMITS.allowedTypes,
      }
      uploadLimitsRef.current = normalized
      return normalized
    } catch {
      return uploadLimitsRef.current
    }
  }, [])

  const runUploadTasks = useCallback(async (tasks: UploadTaskRuntime[], sessionId: number, sessionEpoch: number, limits: UploadLimits) => {
    const stillCurrent = () => ownershipRef.current.ownsSession(sessionId, sessionEpoch)
    const activeUploads = (uploadCountBySessionRef.current.get(sessionId) || 0) + 1
    uploadCountBySessionRef.current.set(sessionId, activeUploads)
    if (stillCurrent()) {
      setUploading(true)
      setUploadError(null)
    }
    try {
      for (const task of tasks) {
        if (!stillCurrent()) break
        if (cancelledUploadTasksRef.current.delete(task.id)) continue
        try {
          task.uploadFile = await compressImageIfNeeded(task.sourceFile)
        } catch (err) {
          updateUploadTask(task.id, { status: "failed", error: err instanceof Error ? err.message : "图片处理失败", retryable: false })
          continue
        }
        if (!stillCurrent()) break
        if (cancelledUploadTasksRef.current.delete(task.id)) continue
        if (!isAcceptedFile(task.uploadFile, limits.allowedTypes)) {
          updateUploadTask(task.id, { status: "failed", error: "不支持的文件类型", retryable: false })
          continue
        }
        if (task.uploadFile.size > limits.maxFileSizeMb * 1024 * 1024) {
          updateUploadTask(task.id, { status: "failed", error: `文件过大（上限 ${limits.maxFileSizeMb}MB）`, retryable: false })
          continue
        }
        updateUploadTask(task.id, { total: task.uploadFile.size })
        const controller = new AbortController()
        uploadControllersRef.current.set(task.id, controller)
        updateUploadTask(task.id, { status: "uploading", error: undefined })
        try {
          const uploaded = await filesApi.upload(task.uploadFile, sessionId, {
            signal: controller.signal,
            onProgress: (loaded, total) => {
              if (stillCurrent()) updateUploadTask(task.id, { loaded, total: total || task.uploadFile.size, status: "uploading" })
            },
            onProcessing: () => {
              if (stillCurrent()) updateUploadTask(task.id, { loaded: task.uploadFile.size, total: task.uploadFile.size, status: "processing" })
            },
          })
          if (stillCurrent()) {
            ownershipRef.current.mutate()
            setAttachments((prev) => mergeFilesByID(prev, [uploaded]))
            setUploadTasks((current) => current.filter((item) => item.id !== task.id))
          }
        } catch (err) {
          if (!stillCurrent()) break
          const cancelled = controller.signal.aborted
          updateUploadTask(task.id, {
            status: cancelled ? "cancelled" : "failed",
            error: cancelled ? "已取消" : err instanceof Error ? err.message : "上传失败",
            retryable: cancelled || !(err instanceof Error && "retryable" in err && err.retryable === false),
          })
        } finally {
          uploadControllersRef.current.delete(task.id)
        }
      }
    } finally {
      const remaining = Math.max(0, (uploadCountBySessionRef.current.get(sessionId) || 1) - 1)
      if (remaining > 0) uploadCountBySessionRef.current.set(sessionId, remaining)
      else uploadCountBySessionRef.current.delete(sessionId)
      if (stillCurrent()) {
        setUploading(remaining > 0)
        if (fileInputRef.current) fileInputRef.current.value = ""
      }
    }
  }, [updateUploadTask])

  const uploadFiles = useCallback(async (rawFiles: File[]) => {
    if (rawFiles.length === 0) return
    if (!activeSessionId) {
      flashUploadError("请先进入一个会话再上传文件")
      return
    }
    const sessionId = activeSessionId
    const sessionEpoch = ownershipRef.current.currentEpoch()
    const stillCurrent = () => ownershipRef.current.ownsSession(sessionId, sessionEpoch)
    const tasks = rawFiles.map((sourceFile): UploadTaskRuntime => ({
      id: `${sessionEpoch}:${++uploadTaskSequenceRef.current}`,
      sourceFile,
      uploadFile: sourceFile,
      sessionId,
      sessionEpoch,
      filename: sourceFile.name,
      loaded: 0,
      total: sourceFile.size,
      status: "queued",
    }))
    setUploadTasks((current) => [...current, ...tasks])
    setUploading(true)
    setUploadError(null)
    const limits = await refreshUploadLimits()
    if (!stillCurrent()) return
    await runUploadTasks(tasks, sessionId, sessionEpoch, limits)
  }, [activeSessionId, flashUploadError, refreshUploadLimits, runUploadTasks])

  const cancelUpload = useCallback((id: string) => {
    const controller = uploadControllersRef.current.get(id)
    if (controller) controller.abort()
    else cancelledUploadTasksRef.current.add(id)
    setUploadTasks((current) => current.map((task) => task.id === id && task.status === "queued" ? { ...task, status: "cancelled", error: "已取消", retryable: true } : task))
  }, [])

  const retryUpload = useCallback(async (id: string) => {
    const task = uploadTasks.find((item) => item.id === id)
    if (!task || !ownershipRef.current.ownsSession(task.sessionId, task.sessionEpoch)) return
    setUploadTasks((current) => current.filter((item) => item.id !== id))
    const replacement: UploadTaskRuntime = {
      ...task,
      id: `${task.sessionEpoch}:${++uploadTaskSequenceRef.current}`,
      uploadFile: task.sourceFile,
      loaded: 0,
      total: task.sourceFile.size,
      status: "queued",
      error: undefined,
      retryable: undefined,
    }
    setUploadTasks((current) => [...current, replacement])
    setUploading(true)
    setUploadError(null)
    const limits = await refreshUploadLimits()
    if (!ownershipRef.current.ownsSession(task.sessionId, task.sessionEpoch)) return
    await runUploadTasks([replacement], task.sessionId, task.sessionEpoch, limits)
  }, [refreshUploadLimits, runUploadTasks, uploadTasks])

  const dismissUpload = useCallback((id: string) => {
    const controller = uploadControllersRef.current.get(id)
    if (controller) controller.abort()
    else cancelledUploadTasksRef.current.add(id)
    setUploadTasks((current) => current.filter((item) => item.id !== id))
  }, [])

  const removeAttachment = useCallback(async (id: number) => {
    if (!activeSessionId) return
    const sessionId = activeSessionId
    const sessionEpoch = ownershipRef.current.currentEpoch()
    try {
      await filesApi.delete(id)
      if (!ownershipRef.current.ownsSession(sessionId, sessionEpoch)) return
      ownershipRef.current.mutate()
      setAttachments((prev) => prev.filter((file) => file.id !== id))
      const current = readSelection(sessionId)
      const next = { manual: current.manual, ids: current.ids.filter((selectedID) => selectedID !== id) }
      persistSelection(sessionId, next)
    } catch (err) {
      if (!ownershipRef.current.ownsSession(sessionId, sessionEpoch)) return
      flashUploadError(err instanceof Error ? err.message : "删除附件失败")
    }
  }, [activeSessionId, flashUploadError, persistSelection])

  const toggleAttachment = useCallback((id: number) => {
    if (!activeSessionId) return
    const next = selectManualAttachment(attachments, selection.ids, id)
    if (next.error) {
      flashUploadError(next.error)
      return
    }
    persistSelection(activeSessionId, { manual: true, ids: next.ids })
  }, [activeSessionId, attachments, flashUploadError, persistSelection, selection.ids])

  const markSent = useCallback((sessionId: number, ids: number[]) => {
    if (ids.length === 0) return
    const sent = new Set(ids)
    if (activeSessionRef.current === sessionId) {
      ownershipRef.current.mutate()
      setAttachments((prev) => prev.filter((file) => !sent.has(file.id)))
    }
    persistSelection(sessionId, selectionAfterSent(readSelection(sessionId), ids))
  }, [persistSelection])

  const markSentForCurrentEpoch = useCallback((sessionId: number, sessionEpoch: number, ids: number[]) => {
    if (!ownershipRef.current.ownsSession(sessionId, sessionEpoch)) return
    markSent(sessionId, ids)
  }, [markSent])

  const currentAttachmentEpoch = useCallback(() => ownershipRef.current.currentEpoch(), [])

  const retryAttachmentOCR = useCallback(async (id: number) => {
    const sessionId = activeSessionRef.current
    if (!sessionId) return
    const sessionEpoch = ownershipRef.current.currentEpoch()
    const generation = (ocrGenerationRef.current.get(id) || 0) + 1
    ocrGenerationRef.current.set(id, generation)
    try {
      const next = await filesApi.retryOCR(id)
      if (!ownershipRef.current.ownsSession(sessionId, sessionEpoch) || ocrGenerationRef.current.get(id) !== generation) return
      ownershipRef.current.mutate()
      setAttachments((current) => current.map((file) => file.id === id ? next : file))
    } catch (err) {
      if (!ownershipRef.current.ownsSession(sessionId, sessionEpoch)) return
      flashUploadError(err instanceof Error ? err.message : "OCR 重试失败")
    }
  }, [flashUploadError])

  const refreshStagedAttachments = useCallback(async () => {
    const sessionId = activeSessionRef.current
    if (!sessionId) return
    const snapshot = ownershipRef.current.beginList(sessionId)
    try {
      const res = await filesApi.list({ sessionId, limit: 100, offset: 0, unreferenced: true })
      if (!ownershipRef.current.ownsList(snapshot)) return
      const files = mergeFilesByID([], res.files)
      setAttachments(files)
      const current = readSelection(sessionId)
      const available = new Set(files.filter((file) => !isFileSendBlocked(file)).map((file) => file.id))
      persistSelection(sessionId, { manual: current.manual, ids: current.ids.filter((id) => available.has(id)) })
    } catch (err) {
      if (!ownershipRef.current.ownsList(snapshot)) return
      flashUploadError(err instanceof Error ? err.message : "加载暂存附件失败")
    }
  }, [flashUploadError, persistSelection])

  useLayoutEffect(() => {
    const sessionId = activeSessionId
    const previousSessionId = activeSessionRef.current
    activeSessionRef.current = sessionId
    ownershipRef.current.activate(sessionId || null)
    const ownership = ownershipRef.current
    const uploadControllers = uploadControllersRef.current
    const cancelledUploadTasks = cancelledUploadTasksRef.current
    if (previousSessionId) uploadCountBySessionRef.current.delete(previousSessionId)
    if (sessionId) uploadCountBySessionRef.current.delete(sessionId)
    for (const controller of uploadControllersRef.current.values()) controller.abort()
    uploadControllersRef.current.clear()
    cancelledUploadTasksRef.current.clear()
    if (errorTimerRef.current !== null) window.clearTimeout(errorTimerRef.current)
    if (activeSessionRef.current !== sessionId) return
    setAttachments([])
    setSelection(sessionId ? readSelection(sessionId) : { manual: false, ids: [] })
    setUploadError(null)
    setUploadTasks([])
    // Session navigation aborts and releases every in-memory upload task. A
    // later A visit is a new epoch and must not inherit the old A call count.
    setUploading(false)
    if (fileInputRef.current) fileInputRef.current.value = ""
    if (!sessionId) {
      return
    }
    const snapshot = ownershipRef.current.beginList(sessionId)
    void filesApi.list({ sessionId, limit: 100, offset: 0, unreferenced: true }).then((res) => {
      if (!ownershipRef.current.ownsList(snapshot)) return
      setAttachments(mergeFilesByID([], res.files))
      const stored = readSelection(sessionId)
      const available = new Set(res.files.filter((file) => !isFileSendBlocked(file)).map((file) => file.id))
      const next = { manual: stored.manual, ids: stored.ids.filter((id) => available.has(id)) }
      if (next.ids.length !== stored.ids.length) localStorage.setItem(selectionKey(sessionId), JSON.stringify(next))
      setSelection(next)
    }).catch((err) => {
      if (ownershipRef.current.ownsList(snapshot)) flashUploadError(err instanceof Error ? err.message : "加载暂存附件失败")
    })
    return () => {
      ownership.activate(null)
      for (const controller of uploadControllers.values()) controller.abort()
      uploadControllers.clear()
      cancelledUploadTasks.clear()
      if (errorTimerRef.current !== null) window.clearTimeout(errorTimerRef.current)
    }
  }, [activeSessionId, flashUploadError])

  useEffect(() => {
    if (!attachments.some(isOCRRefreshable)) return
    const sessionId = activeSessionId
    const timer = window.setInterval(() => {
      setAttachments((prev) => {
        const pending = prev.filter(isOCRRefreshable)
        for (const file of pending) {
          if (ocrRefreshInFlightRef.current.has(file.id)) continue
          ocrRefreshInFlightRef.current.add(file.id)
          const generation = (ocrGenerationRef.current.get(file.id) || 0) + 1
          ocrGenerationRef.current.set(file.id, generation)
          const sessionEpoch = ownershipRef.current.currentEpoch()
          void filesApi.refreshOCR(file.id).then((next) => {
            if (!sessionId || !ownershipRef.current.ownsSession(sessionId, sessionEpoch) || ocrGenerationRef.current.get(file.id) !== generation) return
            ownershipRef.current.mutate()
            setAttachments((current) => current.map((item) => (item.id === next.id ? next : item)))
          }).catch((err) => {
            if (sessionId && ownershipRef.current.ownsSession(sessionId, sessionEpoch)) {
              flashUploadError(err instanceof Error ? err.message : "OCR 状态刷新失败")
            }
          }).finally(() => {
            ocrRefreshInFlightRef.current.delete(file.id)
          })
        }
        return prev
      })
    }, 2500)
    return () => window.clearInterval(timer)
  }, [activeSessionId, attachments, flashUploadError])

  const readyAttachments = attachments.filter((file) => !isFileSendBlocked(file))
  const selectedAttachments = selection.manual
    ? selection.ids.map((id) => readyAttachments.find((file) => file.id === id)).filter((file): file is FileInfo => !!file)
    : defaultSelection(readyAttachments)

  const publicUploadTasks: UploadTask[] = uploadTasks.map((task) => ({
    id: task.id,
    filename: task.filename,
    loaded: task.loaded,
    total: task.total,
    status: task.status,
    error: task.error,
    retryable: task.retryable,
  }))

  return {
    attachments,
    setAttachments,
    selectedAttachments,
    selectionManual: selection.manual,
    uploading,
    uploadTasks: publicUploadTasks,
    uploadError,
    fileInputRef,
    refreshUploadLimits,
    uploadFiles,
    cancelUpload,
    retryUpload,
    dismissUpload,
    removeAttachment,
    toggleAttachment,
    markSentForCurrentEpoch,
    currentAttachmentEpoch,
    retryAttachmentOCR,
    refreshStagedAttachments,
  }
}
