import { useState, useRef, useEffect, useLayoutEffect, useCallback, forwardRef, useImperativeHandle, type ChangeEvent, type ClipboardEvent, type KeyboardEvent } from "react"
import { useChatStore } from "@/stores/chat"
import { useSSE } from "@/hooks/useSSE"
import { PromptPickerDialog } from "@/components/prompts/PromptPickerDialog"
import { getSession, getSessionMemory, updateSession } from "@/api/sessions"
import { updateSessionSkills } from "@/api/skills"
import { useModelStore } from "@/stores/models"
import { useSkillStore } from "@/stores/skills"
import { isStreamingAbortable, isStreamingInteractionBusy } from "@/lib/streamingStatus"
import { isFileSendBlocked, isOCRPending } from "@/api/files"
import {
  getComposerTextareaMinHeight,
  getComposerTextareaMaxHeight,
  type SearchMode,
} from "./ChatInput.constants"
import { useAttachmentUploadQueue } from "./useAttachmentUploadQueue"
import { useThinkingEffortSelection } from "./useThinkingEffortSelection"
import { ChatInputToolbar } from "./ChatInputToolbar"
import { ComposerBox } from "./ComposerBox"
import { getClipboardFiles } from "./chatInputUpload"
import { loadChatDraftState, saveChatDraftState, type StoredSubmission } from "./chatDrafts"
import { SessionMemoryDialog } from "./SessionMemoryDialog"
import { StagedAttachmentsDrawer } from "./StagedAttachmentsDrawer"
import { SessionSkillMutationCoordinator, sessionSkills } from "./sessionSkillMutation"
import type { AttachmentMeta } from "@/types"
import { safeUUID } from "@/lib/utils"


interface CapturedSubmission {
  id: number
  sessionId: number
  sessionGeneration: number
  content: string
  attachments: AttachmentMeta[]
  attachmentIds: number[]
  attachmentEpoch: number
  clientRunId: string
  thinkingEffort?: string
}

interface NoticeAction {
  label: string
  onClick: () => void
  variant?: "secondary" | "ghost"
}

export interface ChatInputHandle {
  uploadFiles: (files: File[]) => Promise<void>
  canUpload: boolean
}

export const ChatInput = forwardRef<ChatInputHandle>(function ChatInput(_props, ref) {
  const draftRevisionRef = useRef<Record<number, number>>({})
  const [draftState, setDraftState] = useState(() => {
    const initial = loadChatDraftState()
    for (const sessionId of Object.keys(initial.drafts)) draftRevisionRef.current[Number(sessionId)] = 0
    return initial
  })
  const draftsBySession = draftState.drafts
  const pendingSubmissions = draftState.submissions
  const draftStateRef = useRef(draftState)
  draftStateRef.current = draftState
  const pendingSubmissionsRef = useRef(pendingSubmissions)
  pendingSubmissionsRef.current = pendingSubmissions
  const [promptPickerOpen, setPromptPickerOpen] = useState(false)
  const [isComposing, setIsComposing] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)
  const [noticeActions, setNoticeActions] = useState<NoticeAction[]>([])
  const [preparingSessionId, setPreparingSessionId] = useState<number | null>(null)
  const [menuOpen, setMenuOpen] = useState(false)
  const [menuView, setMenuView] = useState<"main" | "skills">("main")
  const [memoryDialogOpen, setMemoryDialogOpen] = useState(false)
  const [stagingOpen, setStagingOpen] = useState(false)
  const [memoryUnseen, setMemoryUnseen] = useState(false)
  const [thinkingMenuOpen, setThinkingMenuOpen] = useState(false)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const retrySubmissionRef = useRef<(submission: CapturedSubmission) => void>(() => {})
  const submissionIdRef = useRef(Math.max(0, ...Object.values(draftState.submissions).map((submission) => submission.id)))
  const attemptTokenRef = useRef(0)
  const activeAttemptRef = useRef<number | null>(null)
  const failedSubmissionRef = useRef<CapturedSubmission | null>(null)
  const skillMutationRef = useRef<SessionSkillMutationCoordinator | null>(null)
  const streamingStatus = useChatStore((s) => s.streaming.status)
  const activeSessionId = useChatStore((s) => s.activeSessionId)
  const sessions = useChatStore((s) => s.sessions)
  const messages = useChatStore((s) => s.messages)
  const compactionOwner = useChatStore((s) => activeSessionId ? s.compactionOwners[activeSessionId] : undefined)
  const updateSessionLocal = useChatStore((s) => s.updateSessionLocal)
  const { sendMessage, startCompaction, abort } = useSSE()
  const models = useModelStore((s) => s.models)
  const modelsLoaded = useModelStore((s) => s.loaded)
  const loadModels = useModelStore((s) => s.loadModels)
  const skills = useSkillStore((s) => s.skills)
  const loadSkills = useSkillStore((s) => s.loadSkills)
  const isStreaming = isStreamingInteractionBusy(streamingStatus)
  const isAbortable = isStreamingAbortable(streamingStatus)
  const input = activeSessionId ? draftsBySession[activeSessionId] ?? "" : ""
  const preparingSend = preparingSessionId === activeSessionId

  const setDraftsBySession = useCallback((update: (current: Record<number, string>) => Record<number, string>) => {
    const next = { ...draftStateRef.current, drafts: update(draftStateRef.current.drafts) }
    draftStateRef.current = next
    setDraftState(next)
    saveChatDraftState(next)
  }, [])

  const setPendingSubmission = useCallback((submission: CapturedSubmission, status: StoredSubmission["status"]) => {
    const next = {
      ...draftStateRef.current,
      submissions: {
        ...draftStateRef.current.submissions,
        [submission.sessionId]: {
          id: submission.id,
          sessionId: submission.sessionId,
          clientRunId: submission.clientRunId,
          content: submission.content,
          attachments: submission.attachments,
          attachmentIds: submission.attachmentIds,
          ...(submission.thinkingEffort ? { thinkingEffort: submission.thinkingEffort } : {}),
          status,
        },
      },
    }
    draftStateRef.current = next
    setDraftState(next)
    saveChatDraftState(next)
  }, [])

  const clearPendingSubmission = useCallback((submission: CapturedSubmission) => {
    const current = draftStateRef.current
    const existing = current.submissions[submission.sessionId]
    if (!existing || existing.id !== submission.id || existing.clientRunId !== submission.clientRunId) return
    const submissions = { ...current.submissions }
    delete submissions[submission.sessionId]
    const next = { ...current, submissions }
    draftStateRef.current = next
    setDraftState(next)
    saveChatDraftState(next)
  }, [])

  const { attachments: stagedAttachments, selectedAttachments: attachments, uploading, uploadTasks, uploadError, fileInputRef, refreshUploadLimits, uploadFiles, cancelUpload, retryUpload, dismissUpload, removeAttachment, toggleAttachment, restoreAttachmentSelection, markSentForCurrentEpoch, currentAttachmentEpoch, retryAttachmentOCR, refreshStagedAttachments } = useAttachmentUploadQueue(activeSessionId)

  const setInput = useCallback((value: string) => {
    const sessionId = useChatStore.getState().activeSessionId
    if (!sessionId) return
    draftRevisionRef.current[sessionId] = (draftRevisionRef.current[sessionId] || 0) + 1
    setDraftsBySession((current) => {
      if (value === "") {
        if (current[sessionId] === undefined) return current
        const next = { ...current }
        delete next[sessionId]
        return next
      }
      if (current[sessionId] === value) return current
      return { ...current, [sessionId]: value }
    })
  }, [setDraftsBySession])

  const clearSubmittedDraft = useCallback((sessionId: number, revision: number) => {
    setDraftsBySession((current) => {
      // A revision, rather than the text value, identifies the submitted
      // draft. Re-entering the same text while a request is in flight must
      // not be mistaken for the old draft and cleared by a late callback.
      if (draftRevisionRef.current[sessionId] !== revision) return current
      if (current[sessionId] === undefined) return current
      const next = { ...current }
      delete next[sessionId]
      return next
    })
  }, [setDraftsBySession])

  const restoreSubmissionAsDraft = useCallback((submission: CapturedSubmission) => {
    const state = useChatStore.getState()
    if (state.activeSessionId !== submission.sessionId) {
      setNotice("请先返回原会话，再恢复这条消息")
      setNoticeActions([])
      return
    }
    const currentDraft = draftStateRef.current.drafts[submission.sessionId] || ""
    if (currentDraft && currentDraft !== submission.content) {
      const confirmed = typeof window === "undefined" || window.confirm("输入框已有新草稿，恢复失败消息会替换它。是否继续？")
      if (!confirmed) return
    }
    setInput(submission.content)
    restoreAttachmentSelection(submission.attachmentIds)
    clearPendingSubmission(submission)
    failedSubmissionRef.current = null
    setNotice("已恢复到输入框，可继续编辑后发送。")
    setNoticeActions([])
  }, [clearPendingSubmission, restoreAttachmentSelection, setInput])

  const discardSubmission = useCallback((submission: CapturedSubmission) => {
    const confirmed = typeof window === "undefined" || window.confirm("放弃这条失败消息？原内容将从当前标签页移除。")
    if (!confirmed) return
    clearPendingSubmission(submission)
    if (failedSubmissionRef.current?.id === submission.id) failedSubmissionRef.current = null
    setNotice("已放弃失败消息")
    setNoticeActions([])
  }, [clearPendingSubmission])

  const submissionNoticeActions = useCallback((submission: CapturedSubmission): NoticeAction[] => [
    {
      label: "重试",
      variant: "secondary",
      onClick: () => {
        if (failedSubmissionRef.current?.id === submission.id) retrySubmissionRef.current(submission)
      },
    },
    { label: "恢复编辑", variant: "ghost", onClick: () => restoreSubmissionAsDraft(submission) },
    { label: "放弃", variant: "ghost", onClick: () => discardSubmission(submission) },
  ], [discardSubmission, restoreSubmissionAsDraft])

  const activeSession = sessions.find((item) => item.id === activeSessionId)
  const compacting = Boolean(compactionOwner)
  const currentModel = models.find((item) => item.id === activeSession?.model_id && item.provider === activeSession?.provider)
  const modelUnavailable = modelsLoaded && (!currentModel || !currentModel.enabled)
  const { thinkingOptions, thinkingEffort, thinkingActive, thinkingLabel, setThinkingEffort } = useThinkingEffortSelection(currentModel)
  const searchMode: SearchMode = activeSession?.search_mode ?? "auto"
  const memoryEnabled = activeSession?.memory_enabled ?? false
  const enabledSkillIds = sessionSkills(activeSession)
  const activeSkillCount = skills.filter((skill) => enabledSkillIds.includes(skill.id)).length
  const hasImageAttachments = attachments.some((file) => file.content_type.startsWith("image/"))
  const imageUnsupported = hasImageAttachments && currentModel && !currentModel.vision
  const blockedAttachment = attachments.find(isFileSendBlocked)
  const attachmentNotice = blockedAttachment
    ? isOCRPending(blockedAttachment)
      ? "已选 PDF 仍在 OCR 解析中。"
      : "已选文件解析失败，请在暂存附件中重试或删除。"
    : null

  function sendCapturedSubmission(submission: CapturedSubmission) {
    const state = useChatStore.getState()
    if (
      state.activeSessionId !== submission.sessionId
      || state.activeSessionGeneration !== submission.sessionGeneration
    ) {
      setNotice("会话已切换，请重新输入")
      setNoticeActions([])
      return
    }
    if (activeAttemptRef.current !== null || isStreamingInteractionBusy(state.streaming.status)) {
      // Keep a deterministic retry target if another lifecycle transition won
      // the race between the click handler and this submission helper.
      failedSubmissionRef.current = submission
      setNotice("当前仍有消息处理中，请稍候")
      setNoticeActions([{
        label: "重试",
        onClick: () => {
          const failed = failedSubmissionRef.current
          if (failed?.id === submission.id) retrySubmissionRef.current(failed)
        },
      }])
      return
    }

    const attemptToken = ++attemptTokenRef.current
    activeAttemptRef.current = attemptToken
    let accepted = false
    setPreparingSessionId(submission.sessionId)
    setNotice(null)
    setNoticeActions([])

    const ownsAttempt = () => {
      const current = useChatStore.getState()
      return activeAttemptRef.current === attemptToken
        && current.activeSessionId === submission.sessionId
        && current.activeSessionGeneration === submission.sessionGeneration
    }

    setPendingSubmission(submission, "sending")
    void sendMessage(submission.sessionId, submission.content, submission.attachments, {
      clientRunId: submission.clientRunId,
      ...(submission.thinkingEffort ? { thinkingEffort: submission.thinkingEffort } : {}),
      onAccepted: () => {
        if (!ownsAttempt()) return
        accepted = true
        activeAttemptRef.current = null
        setPreparingSessionId((current) => (current === submission.sessionId ? null : current))
        failedSubmissionRef.current = null
        setNotice(null)
        setNoticeActions([])
        clearPendingSubmission(submission)
        markSentForCurrentEpoch(submission.sessionId, currentAttachmentEpoch(), submission.attachmentIds)
      },
    }).catch((err) => {
      if (accepted || !ownsAttempt()) return
      const message = err instanceof Error && err.message ? err.message : "发送失败"
      failedSubmissionRef.current = submission
      setPendingSubmission(submission, "failed")
      setNotice(message.includes("压缩失败") ? "压缩失败，请联系管理员" : message)
      setNoticeActions(submissionNoticeActions(submission))
    }).finally(() => {
      if (activeAttemptRef.current !== attemptToken) return
      activeAttemptRef.current = null
      if (!accepted) {
        setPreparingSessionId((current) => (current === submission.sessionId ? null : current))
      }
    })
  }

  retrySubmissionRef.current = sendCapturedSubmission

  useEffect(() => {
    saveChatDraftState(draftState)
  }, [draftState])

  const resizeComposerTextarea = useCallback(() => {
    if (!textareaRef.current) return
    const el = textareaRef.current
    el.style.height = "0px"
    const contentHeight = el.scrollHeight
    const minHeight = getComposerTextareaMinHeight(el.ownerDocument)
    const maxHeight = getComposerTextareaMaxHeight(window.innerWidth)
    const nextHeight = Math.min(Math.max(contentHeight, minHeight), maxHeight)
    el.style.height = `${nextHeight}px`
    el.style.overflowY = contentHeight > maxHeight ? "auto" : "hidden"
  }, [])

  useLayoutEffect(() => {
    resizeComposerTextarea()
  }, [activeSessionId, input, resizeComposerTextarea])

  useEffect(() => {
    function handleViewportResize() {
      resizeComposerTextarea()
    }

    window.addEventListener("resize", handleViewportResize)
    window.visualViewport?.addEventListener("resize", handleViewportResize)
    return () => {
      window.removeEventListener("resize", handleViewportResize)
      window.visualViewport?.removeEventListener("resize", handleViewportResize)
    }
  }, [resizeComposerTextarea])

  async function setSearchMode(next: SearchMode) {
    if (!activeSessionId || !activeSession || next === searchMode) return
    updateSessionLocal(activeSessionId, { search_mode: next })
    try {
      await updateSession(activeSessionId, { search_mode: next })
    } catch {
      // 回滚到原值，避免本地状态与后端不一致
      updateSessionLocal(activeSessionId, { search_mode: searchMode })
    }
  }

  const markMemorySeen = useCallback((sessionId: number, changeId: number | null) => {
    if (!changeId) return
    localStorage.setItem(`session-memory-seen:${sessionId}`, String(changeId))
    if (sessionId !== useChatStore.getState().activeSessionId) return
    setMemoryUnseen(false)
  }, [])

  const handleMemoryEnabledChange = useCallback((sessionId: number, enabled: boolean) => {
    updateSessionLocal(sessionId, { memory_enabled: enabled })
  }, [updateSessionLocal])

  if (!skillMutationRef.current) {
    skillMutationRef.current = new SessionSkillMutationCoordinator({
      update: updateSessionSkills,
      reload: async (sessionId) => sessionSkills(await getSession(sessionId)),
      applyLocal: (sessionId, skillsEnabled) => {
        const session = useChatStore.getState().sessions.find((item) => item.id === sessionId)
        if (!session) return
        useChatStore.getState().updateSessionLocal(sessionId, {
          metadata: { ...(session.metadata || {}), skills_enabled: skillsEnabled },
        })
      },
      onError: (message) => setNotice(message),
    })
  }

  useEffect(() => {
    if (!activeSessionId || !memoryEnabled || streamingStatus !== "idle") {
      if (!memoryEnabled) setMemoryUnseen(false)
      return
    }
    let canceled = false
    getSessionMemory(activeSessionId)
      .then((res) => {
        if (canceled) return
        const latestAuto = res.changes.find((change) => change.source === "auto")
        if (!latestAuto) {
          setMemoryUnseen(false)
          return
        }
        const seen = localStorage.getItem(`session-memory-seen:${activeSessionId}`)
        setMemoryUnseen(seen !== String(latestAuto.id))
      })
      .catch(() => {})
    return () => {
      canceled = true
    }
  }, [activeSessionId, memoryEnabled, streamingStatus])

  function toggleSkill(skillId: string) {
    if (!activeSessionId || !activeSession) return
    skillMutationRef.current?.toggle(activeSessionId, enabledSkillIds, skillId)
  }

  useEffect(() => {
    loadModels().catch(() => {})
  }, [loadModels])

  useEffect(() => {
    loadSkills().catch(() => {})
  }, [loadSkills])

  useEffect(() => () => {
    // A response owned by an unmounted/account-reset composer must not write
    // into the next account's session store or surface a stale error notice.
    skillMutationRef.current?.invalidateAll()
  }, [])

  useEffect(() => {
    void refreshUploadLimits()
  }, [refreshUploadLimits])

  useEffect(() => {
    setNotice(null)
    setNoticeActions([])
    failedSubmissionRef.current = null
    const stored = activeSessionId ? pendingSubmissionsRef.current[activeSessionId] : undefined
    if (!stored) return
    const restored: CapturedSubmission = {
      id: stored.id,
      sessionId: stored.sessionId,
      sessionGeneration: useChatStore.getState().activeSessionGeneration,
      content: stored.content,
      attachments: stored.attachments,
      attachmentIds: stored.attachmentIds,
      attachmentEpoch: currentAttachmentEpoch(),
      clientRunId: stored.clientRunId,
      ...(stored.thinkingEffort ? { thinkingEffort: stored.thinkingEffort } : {}),
    }
    failedSubmissionRef.current = restored
    // A page reload has no in-memory request owner. Treat an orphaned
    // `sending` snapshot as retryable unless a live stream is still mounted;
    // keeping it as sending otherwise leaves a stale state in storage forever.
    const retryableStatus = stored.status === "sending" && !isStreamingInteractionBusy(streamingStatus)
      ? "failed"
      : stored.status
    if (retryableStatus !== stored.status) setPendingSubmission(restored, retryableStatus)
    setNotice(retryableStatus === "sending" ? "上次发送尚未确认，可继续恢复。" : "上次发送失败，可重试。")
    setNoticeActions(submissionNoticeActions(restored))
  }, [activeSessionId, currentAttachmentEpoch, setPendingSubmission, streamingStatus, submissionNoticeActions])

  function handleSubmit() {
    if (!input.trim() && attachments.length === 0) return
    if (!activeSessionId || isStreaming || compacting || preparingSend || modelUnavailable || uploading) return
    if (blockedAttachment) return
    if (pendingSubmissions[activeSessionId]) {
      setNotice("上次发送尚未完成，请先重试或等待恢复。")
      return
    }

    const content = input.trim()

    // 斜杠指令：/compact 手动压缩当前会话上下文
    if (content === "/compact" && attachments.length === 0) {
      void handleCompact(activeSessionId)
      setInput("")
      return
    }

    // 压缩进行中禁止发送新消息（但允许编辑输入框），避免与压缩 run 交错。
    if (compacting) return

    // 附件只传元数据(file_id 等)。文本正文已在后端持久化，Agent 需要时通过 file_read 按需读取。
    const attachmentMeta: AttachmentMeta[] = attachments.map((f) => ({
      file_id: f.id,
      filename: f.filename,
      file_type: f.content_type,
      size: f.size,
      token_estimate: f.tokenEstimate,
    }))

    const submittedSessionId = activeSessionId
    const submittedAttachmentEpoch = currentAttachmentEpoch()
    const submittedAttachmentIds = attachments.map((item) => item.id)
    const submittedDraftRevision = draftRevisionRef.current[submittedSessionId] || 0
    const submission: CapturedSubmission = {
      id: ++submissionIdRef.current,
      sessionId: submittedSessionId,
      sessionGeneration: useChatStore.getState().activeSessionGeneration,
      content,
      attachments: attachmentMeta,
      attachmentIds: submittedAttachmentIds,
      attachmentEpoch: submittedAttachmentEpoch,
      clientRunId: safeUUID(),
      ...(thinkingActive ? { thinkingEffort } : {}),
    }

    // Move the exact submitted draft out of the editor immediately. A later
    // keystroke gets a newer revision and remains untouched by this request.
    clearSubmittedDraft(submittedSessionId, submittedDraftRevision)
    failedSubmissionRef.current = null
    setPendingSubmission(submission, "sending")
    sendCapturedSubmission(submission)
  }

  async function handleCompact(sessionId: number) {
    if (compacting) return
    setNotice(null)
    setNoticeActions([])
    try {
      const outcome = await startCompaction(sessionId, thinkingActive ? thinkingEffort : undefined)
      if (outcome === "skip") {
        setNotice("无需压缩")
        setNoticeActions([])
        setTimeout(() => setNotice(null), 3000)
      }
    } catch (err) {
      setNotice(err instanceof Error && err.message ? err.message : "压缩失败，请联系管理员")
      setNoticeActions([{ label: "重试", onClick: () => void handleCompact(sessionId) }])
    }
  }

  function handleKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.nativeEvent.isComposing || isComposing || e.keyCode === 229) {
      return
    }
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault()
      handleSubmit()
    }
  }

  async function handleFileSelect(e: ChangeEvent<HTMLInputElement>) {
    const files = Array.from(e.target.files || [])
    await uploadFiles(files)
  }

  async function handlePaste(e: ClipboardEvent<HTMLTextAreaElement>) {
    const files = getClipboardFiles(e.clipboardData)
    if (files.length === 0) return
    e.preventDefault()
    await uploadFiles(files)
  }

  // 拖拽上传由外层 ChatArea 统一处理（整个对话窗口都是放置区），
  // 这里只把上传能力与可用状态暴露出去。
  useImperativeHandle(ref, () => ({
    uploadFiles,
    canUpload: !!activeSessionId && !uploading,
  }), [activeSessionId, uploadFiles, uploading])

  const canSend = (input.trim().length > 0 || attachments.length > 0) && !uploading && !compacting && !isStreaming && !preparingSend && !blockedAttachment && !modelUnavailable

  return (
    <div className="w-full">
      <div className="relative mx-auto w-full">
        <ChatInputToolbar
          fileInputRef={fileInputRef}
          uploading={uploading}
          activeSessionId={activeSessionId}
          activeSession={activeSession}
          currentModel={currentModel}
          searchMode={searchMode}
          setSearchMode={(next) => void setSearchMode(next)}
          thinkingMenuOpen={thinkingMenuOpen}
          setThinkingMenuOpen={setThinkingMenuOpen}
          thinkingActive={thinkingActive}
          thinkingLabel={thinkingLabel}
          thinkingOptions={thinkingOptions}
          thinkingEffort={thinkingEffort}
          setThinkingEffort={setThinkingEffort}
          menuOpen={menuOpen}
          setMenuOpen={setMenuOpen}
          menuView={menuView}
          setMenuView={setMenuView}
          memoryEnabled={memoryEnabled}
          activeSkillCount={activeSkillCount}
          skills={skills}
          enabledSkillIds={enabledSkillIds}
          compacting={compacting}
          onFileSelect={(event) => void handleFileSelect(event)}
          onOpenStaging={() => {
            setStagingOpen(true)
            void refreshStagedAttachments()
          }}
          onPromptPickerOpen={() => setPromptPickerOpen(true)}
          onOpenMemoryManager={() => setMemoryDialogOpen(true)}
          onToggleSkill={(skillId) => void toggleSkill(skillId)}
          onCompact={() => {
            if (activeSessionId) void handleCompact(activeSessionId)
          }}
          memoryUnseen={memoryUnseen}
          className="absolute bottom-full left-0 z-20 mb-0.5 flex-nowrap sm:left-1 sm:mb-2.5"
        />

        <ComposerBox
          input={input}
          setInput={setInput}
          textareaRef={textareaRef}
          attachments={attachments}
          stagedCount={stagedAttachments.length}
          uploadError={uploadError}
          uploadTasks={uploadTasks}
          imageUnsupported={imageUnsupported}
          streamingStatus={streamingStatus}
          notice={modelUnavailable ? "当前会话没有可用模型，请先在顶部选择模型或联系管理员。" : notice}
          noticeActions={noticeActions}
          attachmentNotice={attachmentNotice}
          messages={messages}
          currentModel={currentModel}
          isStreaming={isStreaming}
          isAbortable={isAbortable}
          preparingSend={preparingSend}
          canSend={canSend}
          onKeyDown={handleKeyDown}
          onPaste={handlePaste}
          onCompositionStart={() => setIsComposing(true)}
          onCompositionEnd={() => setIsComposing(false)}
          onOpenStaging={() => {
            setStagingOpen(true)
            void refreshStagedAttachments()
          }}
          onCancelUploads={() => {
            for (const task of uploadTasks) {
              if (task.status === "queued" || task.status === "uploading" || task.status === "processing") cancelUpload(task.id)
            }
          }}
          onAbort={abort}
          onSubmit={handleSubmit}
        />
      </div>
      <PromptPickerDialog open={promptPickerOpen} onOpenChange={setPromptPickerOpen} />
      <SessionMemoryDialog
        open={memoryDialogOpen}
        sessionId={activeSessionId}
        onOpenChange={setMemoryDialogOpen}
        onEnabledChange={handleMemoryEnabledChange}
        onSeenChange={markMemorySeen}
      />
      <StagedAttachmentsDrawer
        open={stagingOpen}
        onOpenChange={setStagingOpen}
        files={stagedAttachments}
        selected={attachments}
        uploading={uploading}
        uploadTasks={uploadTasks}
        onUpload={() => fileInputRef.current?.click()}
        onToggle={toggleAttachment}
        onDelete={(id) => void removeAttachment(id)}
        onRetry={(id) => retryAttachmentOCR(id)}
        onRefresh={() => refreshStagedAttachments()}
        onCancelUpload={cancelUpload}
        onRetryUpload={retryUpload}
        onDismissUpload={dismissUpload}
      />
    </div>
  )
})
