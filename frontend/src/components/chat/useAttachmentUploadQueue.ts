import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react"
import { filesApi, isFileSendBlocked, isOCRRefreshable, type FileInfo, type UploadLimits } from "@/api/files"
import { compressImageIfNeeded } from "@/lib/imageCompress"
import { DEFAULT_UPLOAD_LIMITS } from "./ChatInput.constants"
import { isAcceptedFile } from "./chatInputUpload"

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
  const [uploadError, setUploadError] = useState<string | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const uploadLimitsRef = useRef<UploadLimits>(DEFAULT_UPLOAD_LIMITS)
  const ocrRefreshInFlightRef = useRef<Set<number>>(new Set())
  const activeSessionRef = useRef(activeSessionId)
  const sessionEpochRef = useRef(0)
  const uploadCountBySessionRef = useRef<Map<number, number>>(new Map())

  useLayoutEffect(() => {
    activeSessionRef.current = activeSessionId
  }, [activeSessionId])

  const flashUploadError = useCallback((message: string) => {
    setUploadError(message)
    setTimeout(() => setUploadError(null), 10000)
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

  const uploadFiles = useCallback(async (rawFiles: File[]) => {
    if (rawFiles.length === 0) return
    if (!activeSessionId) {
      flashUploadError("请先进入一个会话再上传文件")
      return
    }
    const sessionId = activeSessionId
    const sessionEpoch = sessionEpochRef.current
    const stillCurrent = () => activeSessionRef.current === sessionId && sessionEpochRef.current === sessionEpoch
    const limits = await refreshUploadLimits()
    if (!stillCurrent()) return
    const files: File[] = []
    for (const file of rawFiles) files.push(await compressImageIfNeeded(file))
    if (!stillCurrent()) return
    const rejected = files.filter((f) => !isAcceptedFile(f, limits.allowedTypes))
    if (rejected.length > 0) {
      flashUploadError(`不支持的文件类型：${rejected.map((f) => f.name).join("、")}`)
      return
    }
    const maxUploadBytes = limits.maxFileSizeMb * 1024 * 1024
    const tooLarge = files.filter((f) => f.size > maxUploadBytes)
    if (tooLarge.length > 0) {
      flashUploadError(`文件过大（上限 ${limits.maxFileSizeMb}MB）：${tooLarge.map((f) => f.name).join("、")}`)
      return
    }

    const activeUploads = (uploadCountBySessionRef.current.get(sessionId) || 0) + 1
    uploadCountBySessionRef.current.set(sessionId, activeUploads)
    if (activeSessionRef.current === sessionId) {
      setUploading(true)
      setUploadError(null)
    }
    try {
      const failed: string[] = []
      for (const f of files) {
        if (!stillCurrent()) break
        try {
          const uploaded = await filesApi.upload(f, sessionId)
          if (stillCurrent()) {
            setAttachments((prev) => mergeFilesByID(prev, [uploaded]))
          }
        } catch {
          failed.push(f.name)
        }
      }
      if (failed.length > 0 && stillCurrent()) {
        flashUploadError(`上传失败：${failed.join("、")}`)
      }
    } finally {
      const remaining = Math.max(0, (uploadCountBySessionRef.current.get(sessionId) || 1) - 1)
      if (remaining > 0) uploadCountBySessionRef.current.set(sessionId, remaining)
      else uploadCountBySessionRef.current.delete(sessionId)
      if (activeSessionRef.current === sessionId) {
        setUploading(remaining > 0)
        if (fileInputRef.current) fileInputRef.current.value = ""
      }
    }
  }, [activeSessionId, flashUploadError, refreshUploadLimits])

  const removeAttachment = useCallback(async (id: number) => {
    if (!activeSessionId) return
    try {
      await filesApi.delete(id)
      setAttachments((prev) => prev.filter((file) => file.id !== id))
      const current = readSelection(activeSessionId)
      const next = { manual: current.manual, ids: current.ids.filter((selectedID) => selectedID !== id) }
      persistSelection(activeSessionId, next)
    } catch (err) {
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
    if (activeSessionRef.current === sessionId) setAttachments((prev) => prev.filter((file) => !sent.has(file.id)))
    persistSelection(sessionId, selectionAfterSent(readSelection(sessionId), ids))
  }, [persistSelection])

  const retryAttachmentOCR = useCallback(async (id: number) => {
    try {
      const next = await filesApi.retryOCR(id)
      setAttachments((current) => current.map((file) => file.id === id ? next : file))
    } catch (err) {
      flashUploadError(err instanceof Error ? err.message : "OCR 重试失败")
    }
  }, [flashUploadError])

  const refreshStagedAttachments = useCallback(async () => {
    const sessionId = activeSessionRef.current
    if (!sessionId) return
    try {
      const res = await filesApi.list({ sessionId, limit: 100, offset: 0, unreferenced: true })
      if (activeSessionRef.current !== sessionId) return
      const files = mergeFilesByID([], res.files)
      setAttachments(files)
      const current = readSelection(sessionId)
      const available = new Set(files.filter((file) => !isFileSendBlocked(file)).map((file) => file.id))
      persistSelection(sessionId, { manual: current.manual, ids: current.ids.filter((id) => available.has(id)) })
    } catch (err) {
      flashUploadError(err instanceof Error ? err.message : "加载暂存附件失败")
    }
  }, [flashUploadError, persistSelection])

  useLayoutEffect(() => {
    const sessionId = activeSessionId
    sessionEpochRef.current += 1
    if (activeSessionRef.current !== sessionId) return
    setAttachments([])
    setSelection(sessionId ? readSelection(sessionId) : { manual: false, ids: [] })
    setUploadError(null)
    setUploading(sessionId ? (uploadCountBySessionRef.current.get(sessionId) || 0) > 0 : false)
    if (fileInputRef.current) fileInputRef.current.value = ""
    if (!sessionId) {
      return
    }
    let cancelled = false
    void filesApi.list({ sessionId, limit: 100, offset: 0, unreferenced: true }).then((res) => {
      if (cancelled || activeSessionRef.current !== sessionId) return
      setAttachments((prev) => mergeFilesByID(prev, res.files))
      const stored = readSelection(sessionId)
      const available = new Set(res.files.filter((file) => !isFileSendBlocked(file)).map((file) => file.id))
      const next = { manual: stored.manual, ids: stored.ids.filter((id) => available.has(id)) }
      if (next.ids.length !== stored.ids.length) localStorage.setItem(selectionKey(sessionId), JSON.stringify(next))
      setSelection(next)
    }).catch(() => {})
    return () => {
      cancelled = true
    }
  }, [activeSessionId])

  useEffect(() => {
    if (!attachments.some(isOCRRefreshable)) return
    const sessionId = activeSessionId
    const timer = window.setInterval(() => {
      setAttachments((prev) => {
        const pending = prev.filter(isOCRRefreshable)
        for (const file of pending) {
          if (ocrRefreshInFlightRef.current.has(file.id)) continue
          ocrRefreshInFlightRef.current.add(file.id)
          void filesApi.refreshOCR(file.id).then((next) => {
            if (activeSessionRef.current !== sessionId) return
            setAttachments((current) => current.map((item) => (item.id === next.id ? next : item)))
          }).catch((err) => {
            if (activeSessionRef.current === sessionId) {
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

  return {
    attachments,
    setAttachments,
    selectedAttachments,
    selectionManual: selection.manual,
    uploading,
    uploadError,
    fileInputRef,
    refreshUploadLimits,
    uploadFiles,
    removeAttachment,
    toggleAttachment,
    markSent,
    retryAttachmentOCR,
    refreshStagedAttachments,
  }
}
