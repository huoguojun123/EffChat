import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import type { LucideIcon } from "lucide-react"
import { AlertTriangle, BadgeCheck, Check, ChevronDown, ChevronUp, Clock3, ListChecks, Loader2, MapPinned, Plus, RotateCcw, Save, ShieldOff, SlidersHorizontal, Trash2, UserRound } from "lucide-react"
import {
  getSessionMemory,
  memoryMaintenanceUrl,
  saveSessionMemory,
  updateSession,
  undoSessionMemoryChange,
  type ModelTaskRun,
  type SessionMemoryChange,
  type SessionMemoryResponse,
  type SessionMemorySection,
} from "@/api/sessions"
import { getActiveRun } from "@/api/runs"
import { handleAuthExpired } from "@/api/client"
import { Button } from "@/components/ui/button"
import { DialogFooter } from "@/components/ui/dialog"
import { WorkspaceWindow } from "@/components/ui/workspace-window"
import { cn, safeUUID } from "@/lib/utils"
import { consumeMemoryMaintenanceSSE, createStreamHTTPError } from "@/lib/sseProtocol"

interface Props {
  open: boolean
  sessionId: number | null
  onOpenChange: (open: boolean) => void
  onEnabledChange: (sessionId: number, enabled: boolean) => void
  onSeenChange: (sessionId: number, changeId: number | null) => void
}

interface DialogOwner {
  sessionId: number
  generation: number
}

interface OperationOwner extends DialogOwner {
  operationId: number
}

const emptyItem = ""

const sectionTones: Record<string, {
  icon: LucideIcon
}> = {
  user_background: { icon: UserRound },
  user_preferences: { icon: SlidersHorizontal },
  project_context: { icon: MapPinned },
  current_progress: { icon: ListChecks },
  decisions: { icon: BadgeCheck },
  do_not_remember: { icon: ShieldOff },
}

const sectionLabels: Record<string, string> = {
  user_background: "用户背景",
  user_preferences: "用户偏好",
  project_context: "项目背景",
  current_progress: "当前进度",
  decisions: "决策",
  do_not_remember: "不要记住",
}

const fallbackTone = {
  icon: ListChecks,
}

type PendingConfirmation =
  | { type: "delete-item"; sectionIndex: number; itemIndex: number; title: string; detail: string }
  | { type: "clear"; title: string; detail: string }
  | { type: "undo"; changeId: number; title: string; detail: string }
  | { type: "close"; title: string; detail: string }

export function SessionMemoryDialog({ open, sessionId, onOpenChange, onEnabledChange, onSeenChange }: Props) {
  const [data, setData] = useState<SessionMemoryResponse | null>(null)
  const [sections, setSections] = useState<SessionMemorySection[]>([])
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [success, setSuccess] = useState<string | null>(null)
  const [pending, setPending] = useState<PendingConfirmation | null>(null)
  const [expandedItems, setExpandedItems] = useState<Record<string, boolean>>({})
  const ownerRef = useRef<{ sessionId: number | null; generation: number; operationId: number }>({
    sessionId: null,
    generation: 0,
    operationId: 0,
  })

  const changed = useMemo(() => JSON.stringify(sections) !== JSON.stringify(data?.sections || []), [sections, data])

  const ownsDialog = useCallback((owner: DialogOwner) => {
    const current = ownerRef.current
    return current.sessionId === owner.sessionId && current.generation === owner.generation
  }, [])

  function beginOperation(requestSessionId: number): OperationOwner | null {
    const current = ownerRef.current
    if (current.sessionId !== requestSessionId) return null
    const owner = { sessionId: requestSessionId, generation: current.generation, operationId: current.operationId + 1 }
    ownerRef.current = owner
    return owner
  }

  const ownsOperation = useCallback((owner: OperationOwner) => {
    const current = ownerRef.current
    return ownsDialog(owner) && current.operationId === owner.operationId
  }, [ownsDialog])

  function invalidateDialog() {
    const current = ownerRef.current
    ownerRef.current = { sessionId: null, generation: current.generation + 1, operationId: 0 }
  }

  useEffect(() => {
    if (!open || !sessionId) return
    const requestSessionId = sessionId
    const owner: OperationOwner = { sessionId: requestSessionId, generation: ownerRef.current.generation + 1, operationId: 0 }
    ownerRef.current = { ...owner, operationId: 0 }
    async function loadMemory() {
      setLoading(true)
      setData(null)
      setSections([])
      setError(null)
      setSuccess(null)
      try {
        const res = await getSessionMemory(requestSessionId)
        if (!ownsOperation(owner)) return
        setData(res)
        setSections(res.sections)
        setPending(null)
        setExpandedItems({})
        onSeenChange(requestSessionId, latestAutoChangeId(res))
      } catch (err) {
        if (ownsOperation(owner)) setError(err instanceof Error ? err.message : "记忆加载失败")
      } finally {
        if (ownsOperation(owner)) setLoading(false)
      }

      // Load the durable memory snapshot before attaching to an active run.
      // Running these requests in parallel lets a slower initial response
      // overwrite the post-run refresh with stale sections.
      try {
        const { run } = await getActiveRun(requestSessionId)
        if (!ownsOperation(owner) || run?.kind !== "memory_maintenance" || run.status !== "running") return
        setSaving(true)
        setError(null)
        const token = localStorage.getItem("token")
        const res = await fetch(`/api/v1/sessions/${requestSessionId}/runs/${encodeURIComponent(run.run_id)}/resume?cursor=${run.cursor}`, {
          headers: { Authorization: `Bearer ${token}` },
        })
        if (!res.ok) throw await createStreamHTTPError(res, token)
        if (!res.body) throw new Error("流式响应为空")
        await consumeMemoryMaintenanceSSE(res)
        if (!ownsOperation(owner)) return
        const refreshed = await getSessionMemory(requestSessionId)
        if (!ownsOperation(owner)) return
        setData(refreshed)
        setSections(refreshed.sections)
        onEnabledChange(requestSessionId, refreshed.enabled)
        onSeenChange(requestSessionId, latestAutoChangeId(refreshed))
        setSuccess("记忆维护已完成")
      } catch (err) {
        if (ownsOperation(owner)) setError(err instanceof Error ? err.message : "记忆维护恢复失败")
      } finally {
        if (ownsOperation(owner)) setSaving(false)
      }
    }
    void loadMemory()
    return () => {
      if (ownsDialog(owner)) invalidateDialog()
    }
  }, [open, sessionId, onEnabledChange, onSeenChange, ownsDialog, ownsOperation])

  function updateSection(index: number, next: SessionMemorySection) {
    setSections((prev) => prev.map((section, i) => (i === index ? next : section)))
  }

  function updateItem(sectionIndex: number, itemIndex: number, value: string) {
    const section = sections[sectionIndex]
    if (!section) return
    const items = section.items.map((item, i) => (i === itemIndex ? value : item))
    updateSection(sectionIndex, { ...section, items })
  }

  function addItem(sectionIndex: number) {
    const section = sections[sectionIndex]
    if (!section) return
    const itemIndex = section.items.length
    updateSection(sectionIndex, { ...section, items: [...section.items, emptyItem] })
    setExpandedItems((prev) => ({ ...prev, [`${section.key}-${itemIndex}`]: true }))
  }

  function addEmptyItem(sectionIndex: number) {
    const section = sections[sectionIndex]
    if (!section) return
    updateSection(sectionIndex, { ...section, items: [emptyItem] })
    setExpandedItems((prev) => ({ ...prev, [`${section.key}-0`]: true }))
  }

  function reloadFromResponse(res: SessionMemoryResponse, owner: DialogOwner) {
    if (!ownsDialog(owner)) return false
    setData(res)
    setSections(res.sections)
    onEnabledChange(owner.sessionId, res.enabled)
    onSeenChange(owner.sessionId, latestAutoChangeId(res))
    setPending(null)
    return true
  }

  async function reloadMemoryQuietly(owner: OperationOwner) {
    try {
      const res = await getSessionMemory(owner.sessionId)
      if (ownsOperation(owner)) reloadFromResponse(res, owner)
    } catch {
      // Keep the original action error visible.
    }
  }

  async function save(enabled = data?.enabled ?? true, nextSections = sections) {
    if (!sessionId) return
    const owner = beginOperation(sessionId)
    if (!owner) return
    setSaving(true)
    setError(null)
    try {
      const cleaned = nextSections.map((section) => ({
        ...section,
        items: section.items.map((item) => item.trim()).filter(Boolean),
      }))
      const res = await saveSessionMemory(owner.sessionId, { enabled, sections: cleaned, expected_updated_at: data?.updated_at })
      if (!ownsOperation(owner) || !reloadFromResponse(res, owner)) return
      setSuccess("会话记忆已保存")
      setExpandedItems({})
    } catch (err) {
      if (ownsOperation(owner)) setError(err instanceof Error ? err.message : "保存失败")
    } finally {
      if (ownsOperation(owner)) setSaving(false)
    }
  }

  async function toggleEnabled(enabled: boolean) {
    if (!sessionId || !data || enabled === data.enabled) return
    const owner = beginOperation(sessionId)
    if (!owner) return
    setSaving(true)
    setError(null)
    try {
      // Enabling memory is a session-setting mutation, not a memory-document
      // commit. Preserve both the editor draft and its server baseline so a
      // concurrent background memory update still trips the existing CAS when
      await updateSession(owner.sessionId, { memory_enabled: enabled })
      if (!ownsOperation(owner)) return
      setData((current) => current ? { ...current, enabled } : current)
      onEnabledChange(owner.sessionId, enabled)
      setSuccess(enabled ? "会话记忆已启用" : "会话记忆已停用")
    } catch (err) {
      if (ownsOperation(owner)) setError(err instanceof Error ? err.message : "记忆启停失败")
    } finally {
      if (ownsOperation(owner)) setSaving(false)
    }
  }

  async function runMaintenance(operation: "compact" | "retry") {
    if (!sessionId || (operation === "retry" && changed)) return
    const owner = beginOperation(sessionId)
    if (!owner) return
    setSaving(true)
    setError(null)
    try {
      const token = localStorage.getItem("token")
      const runId = safeUUID()
      const response = await fetch(memoryMaintenanceUrl(owner.sessionId, operation, runId), {
        method: "POST",
        headers: { Authorization: `Bearer ${token}` },
      })
      if (response.status === 401) handleAuthExpired(token)
      if (!response.ok) throw await createStreamHTTPError(response, token)
      if (!response.body) throw new Error("流式响应为空")
      await consumeMemoryMaintenanceSSE(response)
      if (!ownsOperation(owner)) return
      const refreshed = await getSessionMemory(owner.sessionId)
      if (!ownsOperation(owner) || !reloadFromResponse(refreshed, owner)) return
      setSuccess(operation === "compact" ? "会话记忆已整理" : "记忆维护已重试")
    } catch (err) {
      if (!ownsOperation(owner)) return
      setError(err instanceof Error ? err.message : operation === "compact" ? "整理失败" : "重试失败")
      await reloadMemoryQuietly(owner)
    } finally {
      if (ownsOperation(owner)) setSaving(false)
    }
  }

  async function compact() {
    await runMaintenance("compact")
  }

  async function retryMaintenance() {
    await runMaintenance("retry")
  }

  function clearMemory() {
    setPending({
      type: "clear",
      title: "清空当前会话记忆？",
      detail: "这会立即保存为空记忆，之后仍可手动重新添加条目。",
    })
  }

  function requestUndoChange(change: SessionMemoryChange) {
    setPending({
      type: "undo",
      changeId: change.id,
      title: "撤销这次记忆更新？",
      detail: `会恢复到这次更新前的记忆：${displayChangeSummary(change)}`,
    })
  }

  function requestDeleteItem(sectionIndex: number, itemIndex: number) {
    const section = sections[sectionIndex]
    const item = section?.items[itemIndex]
    if (!section || item === undefined) return
    setPending({
      type: "delete-item",
      sectionIndex,
      itemIndex,
      title: "删除这条记忆？",
      detail: `将从编辑草稿中移除，点保存后生效：${item.trim() || "空白条目"}`,
    })
  }

  function handleOpenChange(nextOpen: boolean) {
    if (!nextOpen && pending) {
      setPending(null)
      return
    }
    if (!nextOpen && changed) {
      setPending({
        type: "close",
        title: "放弃未保存修改？",
        detail: "当前编辑草稿还没有保存，关闭后会丢失这些改动。",
      })
      return
    }
    if (!nextOpen) invalidateDialog()
    onOpenChange(nextOpen)
  }

  function confirmDeleteItem(action: Extract<PendingConfirmation, { type: "delete-item" }>) {
    const section = sections[action.sectionIndex]
    if (!section) return
    updateSection(action.sectionIndex, { ...section, items: section.items.filter((_, i) => i !== action.itemIndex) })
    setPending(null)
  }

  async function confirmPending() {
    if (!pending) return
    if (pending.type === "delete-item") {
      confirmDeleteItem(pending)
      return
    }
    if (pending.type === "clear") {
      await save(data?.enabled ?? true, sections.map((section) => ({ ...section, items: [] })))
      return
    }
    if (pending.type === "undo") {
      if (!sessionId) return
      const owner = beginOperation(sessionId)
      if (!owner) return
      setSaving(true)
      setError(null)
      try {
        const res = await undoSessionMemoryChange(owner.sessionId, pending.changeId)
        if (!ownsOperation(owner) || !reloadFromResponse(res, owner)) return
        setSuccess("记忆更新已撤销")
      } catch (err) {
        if (!ownsOperation(owner)) return
        setError(err instanceof Error ? err.message : "撤销失败")
        setPending(null)
        await reloadMemoryQuietly(owner)
      } finally {
        if (ownsOperation(owner)) setSaving(false)
      }
      return
    }
    if (pending.type === "close") {
      setPending(null)
      invalidateDialog()
      onOpenChange(false)
      return
    }
  }

  const enabled = data?.enabled ?? false
  const stats = data?.stats
  const recentChanges = data?.changes.slice(0, 5) || []
  const undoableChangeId = latestUndoableChangeId(data?.changes || [])
  const maintenanceRuns = data?.task_runs?.length ? data.task_runs : data?.last_task_run ? [data.last_task_run] : []
  const latestTaskRun = maintenanceRuns[0]

  return (
    <WorkspaceWindow
      open={open}
      onOpenChange={handleOpenChange}
      title="会话记忆"
      defaultWidth={1120}
      defaultHeight={820}
      contentClassName="flex flex-col"
    >
        <div className="grid min-h-0 flex-1 grid-rows-[minmax(0,1fr)_auto] overflow-hidden md:grid-cols-[minmax(0,1fr)_18rem] md:grid-rows-1">
          <div className="min-h-0 overflow-y-auto px-2.5 py-2.5 sm:px-4 sm:py-3">
            <div className="mb-2 flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-border/70 px-1 pb-2.5">
              <label className="inline-flex items-center gap-2 text-sm font-medium">
                <input
                  type="checkbox"
                  checked={enabled}
                  disabled={!data || saving}
                  onChange={(event) => void toggleEnabled(event.currentTarget.checked)}
                  className="h-4 w-4 rounded border-input"
                />
                启用
              </label>
              <div className={cn("text-xs tabular-nums", stats?.near_limit ? "text-amber-600 dark:text-amber-300" : "text-muted-foreground")}>
                {stats ? `${stats.chars}/${stats.max_chars} 字符 · ${stats.item_count} 条` : "加载中"}
              </div>
              {data?.last_auto_updated_at ? (
                <div className="min-w-0 truncate text-xs text-muted-foreground">
                  自动更新 {formatDateTime(data.last_auto_updated_at)}
                </div>
              ) : null}
            </div>

            {loading ? (
              <div className="flex h-48 items-center justify-center text-sm text-muted-foreground">
                <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                加载中
              </div>
            ) : (
              <div className="divide-y divide-border/70">
                {sections.map((section, sectionIndex) => (
                  <MemorySection
                    key={section.key}
                    section={section}
                    sectionIndex={sectionIndex}
                    expandedItems={expandedItems}
                    saving={saving}
                    onAdd={() => addItem(sectionIndex)}
                    onEmptyAdd={() => addEmptyItem(sectionIndex)}
                    onDelete={requestDeleteItem}
                    onToggleItem={(itemKey) => setExpandedItems((prev) => ({ ...prev, [itemKey]: !prev[itemKey] }))}
                    onItemChange={updateItem}
                  />
                ))}
              </div>
            )}
          </div>

          <aside className="max-h-[38dvh] min-h-0 overflow-y-auto border-t border-border/70 bg-muted/15 px-3 py-3 sm:px-4 md:max-h-none md:border-l md:border-t-0">
            <div className="mb-3 flex items-center justify-between gap-2">
              <div className="text-sm font-medium tracking-tight">记忆维护</div>
              {latestTaskRun ? <span className={cn("rounded-full px-2 py-0.5 text-xs", taskRunTone(latestTaskRun))}>{taskRunLabel(latestTaskRun)}</span> : null}
            </div>
            <MemoryMaintenancePanel runs={maintenanceRuns} disabled={!data || saving || changed} onRetry={() => void retryMaintenance()} />

            <div className="mt-4 flex items-center justify-between gap-2">
              <div className="text-sm font-medium tracking-tight">最近更新</div>
              <div className="rounded-full bg-background px-2 py-0.5 text-xs text-muted-foreground ring-1 ring-border/70">{recentChanges.length}</div>
            </div>
            <div className="mt-2 space-y-2">
              {recentChanges.length === 0 ? (
                <div className="rounded-md border border-dashed border-border/80 bg-background/70 px-3 py-6 text-center text-sm text-muted-foreground">暂无更新摘要</div>
              ) : recentChanges.map((change) => (
                <MemoryChangeTimelineItem
                  key={change.id}
                  change={change}
                  canUndo={!changed && !saving && change.id === undoableChangeId}
                  onUndo={() => requestUndoChange(change)}
                />
              ))}
            </div>
          </aside>
        </div>

        {error ? <div className="border-t border-border/70 px-4 py-2 text-sm text-destructive">{error}</div> : null}
        {success ? (
          <div className="flex items-center gap-2 border-t border-border/70 px-4 py-2 text-sm text-emerald-700 dark:text-emerald-300">
            <Check className="h-4 w-4" />
            {success}
          </div>
        ) : null}
        {pending ? (
          <div className="border-t border-border/70 bg-amber-50/70 px-4 py-3 text-amber-950 dark:bg-amber-500/10 dark:text-amber-100">
            <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
              <AlertTriangle className="hidden h-4 w-4 shrink-0 text-amber-600 sm:block" />
              <div className="min-w-0 flex-1">
                <div className="text-sm font-medium">{pending.title}</div>
                <div className="mt-0.5 line-clamp-2 break-words text-sm opacity-80">{pending.detail}</div>
              </div>
              <div className="flex shrink-0 justify-end gap-2">
                <Button type="button" variant="ghost" size="sm" onClick={() => setPending(null)} disabled={saving}>取消</Button>
                <Button
                  type="button"
                  variant={pending.type === "close" ? "outline" : "destructive"}
                  size="sm"
                  className={pending.type === "close" ? undefined : "bg-red-600 text-white hover:bg-red-700 dark:text-white"}
                  onClick={() => void confirmPending()}
                  disabled={saving}
                >
                  确认
                </Button>
              </div>
            </div>
          </div>
        ) : null}
        <DialogFooter className="sticky bottom-0 z-10 flex-wrap border-t border-border/70 bg-background/95 px-3 py-2 backdrop-blur sm:px-4 sm:py-3">
          <Button className="flex-1 sm:flex-none" variant="ghost" onClick={clearMemory} disabled={!data || saving}>清空</Button>
          <Button className="flex-1 sm:flex-none" variant="outline" onClick={() => void compact()} disabled={!data || saving || changed}>
            {saving ? <Loader2 className="h-4 w-4 animate-spin" /> : <RotateCcw className="h-4 w-4" />}
            整理
          </Button>
          <Button className="flex-1 sm:flex-none" onClick={() => void save()} disabled={!data || saving || !changed}>
            {saving ? <Loader2 className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}
            保存
          </Button>
        </DialogFooter>
    </WorkspaceWindow>
  )
}

function sourceLabel(source: string) {
  switch (source) {
    case "auto":
      return "自动"
    case "tool":
      return "工具"
    case "compact":
      return "整理"
    case "undo":
      return "撤销"
    case "system":
      return "系统"
    default:
      return "手动"
  }
}

function MemorySection({
  section,
  sectionIndex,
  expandedItems,
  saving,
  onAdd,
  onEmptyAdd,
  onDelete,
  onToggleItem,
  onItemChange,
}: {
  section: SessionMemorySection
  sectionIndex: number
  expandedItems: Record<string, boolean>
  saving: boolean
  onAdd: () => void
  onEmptyAdd: () => void
  onDelete: (sectionIndex: number, itemIndex: number) => void
  onToggleItem: (itemKey: string) => void
  onItemChange: (sectionIndex: number, itemIndex: number, value: string) => void
}) {
  const tone = sectionTones[section.key] ?? fallbackTone
  const Icon = tone.icon
  const filledCount = section.items.filter((item) => item.trim()).length

  return (
    <section className="py-2.5">
      <div className="flex min-h-9 items-center justify-between gap-2 px-1 pb-1.5">
        <div className="flex min-w-0 items-center gap-2">
          <span className="flex h-6 w-6 shrink-0 items-center justify-center text-muted-foreground">
            <Icon className="h-3.5 w-3.5" />
          </span>
          <div className="flex min-w-0 items-baseline gap-2">
            <h3 className="truncate text-sm font-semibold tracking-tight">{sectionLabels[section.key] || section.title}</h3>
            <div className="shrink-0 text-xs text-muted-foreground">{filledCount > 0 ? `${filledCount} 条` : "暂无条目"}</div>
          </div>
        </div>
        <Button type="button" variant="ghost" size="sm" onClick={onAdd} disabled={saving} className="h-7 shrink-0 px-2">
          <Plus className="h-4 w-4" />
          新增
        </Button>
      </div>

      <div className="space-y-1.5">
        {section.items.length === 0 ? (
          <button
            type="button"
            onClick={onEmptyAdd}
            disabled={saving}
            className="flex min-h-10 w-full items-center justify-center rounded-lg border border-dashed border-border bg-muted/15 text-sm text-muted-foreground transition-[background-color,color] motion-control hover:bg-muted/35 hover:text-foreground disabled:pointer-events-none disabled:opacity-60"
          >
            点击添加条目
          </button>
        ) : section.items.map((item, itemIndex) => (
          <MemoryItemRow
            key={`${section.key}-${itemIndex}`}
            itemKey={`${section.key}-${itemIndex}`}
            sectionIndex={sectionIndex}
            item={item}
            itemIndex={itemIndex}
            expanded={Boolean(expandedItems[`${section.key}-${itemIndex}`])}
            saving={saving}
            onDelete={onDelete}
            onToggleItem={onToggleItem}
            onItemChange={onItemChange}
          />
        ))}
      </div>
    </section>
  )
}

function MemoryItemRow({
  itemKey,
  sectionIndex,
  item,
  itemIndex,
  expanded,
  saving,
  onDelete,
  onToggleItem,
  onItemChange,
}: {
  itemKey: string
  sectionIndex: number
  item: string
  itemIndex: number
  expanded: boolean
  saving: boolean
  onDelete: (sectionIndex: number, itemIndex: number) => void
  onToggleItem: (itemKey: string) => void
  onItemChange: (sectionIndex: number, itemIndex: number, value: string) => void
}) {
  const current = isCurrentProgressItem(item)
  return (
    <div className={cn("group grid grid-cols-[1.5rem_minmax(0,1fr)_auto] gap-1.5 rounded-lg border border-border/75 bg-background px-2.5 shadow-[0_1px_2px_rgba(0,0,0,0.035)] transition-[background-color,border-color,box-shadow] motion-control hover:border-ring/35 hover:bg-muted/15 focus-within:border-ring/45 sm:gap-2 sm:px-3", expanded ? "py-2" : "min-h-10 items-center py-1")}>
      <div className={cn("text-xs tabular-nums text-muted-foreground", expanded ? "pt-2" : "self-center")}>{itemIndex + 1}</div>
      <div className="min-w-0">
        {expanded ? (
          <>
            {current ? (
              <div className="mb-1 inline-flex rounded-full bg-muted px-1.5 py-0.5 text-xs font-medium uppercase text-muted-foreground">
                Current
              </div>
            ) : null}
            <textarea
              value={item}
              disabled={saving}
              onChange={(event) => onItemChange(sectionIndex, itemIndex, event.currentTarget.value)}
              className="min-h-24 w-full min-w-0 resize-y rounded-md border border-ring/35 bg-muted/15 px-2 py-1.5 text-sm leading-6 outline-none transition-[background-color,border-color] motion-control placeholder:text-muted-foreground/55 focus-visible:border-ring/60 focus-visible:bg-background disabled:opacity-70"
            />
          </>
        ) : (
          <button
            type="button"
            onClick={() => onToggleItem(itemKey)}
            disabled={saving}
            className="flex min-h-8 w-full min-w-0 items-center gap-2 rounded-sm text-left text-sm leading-5 text-foreground/90 outline-none transition-colors motion-control hover:text-foreground focus-visible:bg-muted/35 disabled:opacity-70"
            title="展开编辑"
          >
            {current ? <span className="shrink-0 rounded-full bg-muted px-1.5 py-0.5 text-xs font-medium uppercase text-muted-foreground">Current</span> : null}
            <span className={cn("min-w-0 break-words", item.trim() ? "line-clamp-1" : "text-muted-foreground")}>
              {item.trim() || "空条目，展开后编辑"}
            </span>
          </button>
        )}
      </div>
      <div className={cn("flex shrink-0 items-start gap-0.5", expanded ? "pt-1" : "self-center")}>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="h-7 w-7 text-muted-foreground hover:bg-muted"
          onClick={() => onToggleItem(itemKey)}
          disabled={saving}
          title={expanded ? "收起" : "展开"}
        >
          {expanded ? <ChevronUp className="h-3.5 w-3.5" /> : <ChevronDown className="h-3.5 w-3.5" />}
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="h-7 w-7 text-muted-foreground hover:bg-red-500/10 hover:text-red-600"
          onClick={() => onDelete(sectionIndex, itemIndex)}
          disabled={saving}
          title="删除"
        >
          <Trash2 className="h-3.5 w-3.5" />
        </Button>
      </div>
    </div>
  )
}

function MemoryMaintenancePanel({ runs, disabled, onRetry }: { runs: ModelTaskRun[]; disabled: boolean; onRetry: () => void }) {
  const run = runs[0]
  if (!run) {
    return (
      <div className="rounded-md border border-dashed border-border/80 bg-background/70 px-3 py-8 text-center text-sm text-muted-foreground">
        暂无维护记录
      </div>
    )
  }
  const cooling = retryAfterDate(run)
  const model = [run.provider, run.model_id].filter(Boolean).join(" / ")
  const canRetry = run.status === "failed" || Boolean(cooling)
  const history = runs.slice(1, 5)

  return (
    <div className="space-y-2">
      <div className={cn("rounded-md border px-3 py-3 shadow-sm", taskRunFrame(run))}>
        <div className="flex items-start gap-2">
          {run.status === "failed" ? <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" /> : <Check className="mt-0.5 h-4 w-4 shrink-0" />}
          <div className="min-w-0 flex-1">
            <div className="text-sm font-medium">{taskRunLabel(run)}</div>
            <div className="mt-1 space-y-1 text-xs leading-5 text-muted-foreground">
              <div className="flex min-w-0 justify-between gap-2">
                <span className="shrink-0">来源</span>
                <span className="truncate text-foreground/80">{sourceLabel(run.source)}</span>
              </div>
              {model ? (
                <div className="flex min-w-0 justify-between gap-2">
                  <span className="shrink-0">模型</span>
                  <span className="min-w-0 break-all text-right text-foreground/80">{model}</span>
                </div>
              ) : null}
              <div className="flex justify-between gap-2">
                <span>耗时</span>
                <span className="text-foreground/80">{formatDuration(run.duration_ms)}</span>
              </div>
              <div className="flex justify-between gap-2">
                <span>时间</span>
                <span className="text-right text-foreground/80">{formatDateTime(run.finished_at || run.started_at)}</span>
              </div>
            </div>
            {run.status === "failed" && (run.error_type || run.error_message) ? (
              <div className="mt-2 rounded-md bg-background/65 px-2 py-1.5 text-xs leading-5">
                <div className="font-medium text-foreground/80">{taskRunErrorTitle(run)}</div>
                <div className="mt-0.5 break-words text-muted-foreground">{taskRunErrorMessage(run)}</div>
              </div>
            ) : null}
            {cooling ? (
              <div className="mt-2 rounded-md bg-amber-500/10 px-2 py-1.5 text-xs text-amber-700 dark:text-amber-300">
                自动维护冷却至 {formatTime(cooling)}
              </div>
            ) : null}
            {canRetry ? (
              <Button type="button" variant="outline" size="sm" className="mt-3 h-8 w-full bg-background/60" disabled={disabled} onClick={onRetry}>
                <RotateCcw className="h-3.5 w-3.5" />
                重试
              </Button>
            ) : null}
          </div>
        </div>
      </div>
      {history.length > 0 ? (
        <div className="overflow-hidden rounded-md border border-border/70 bg-background/75">
          <div className="border-b border-border/60 px-3 py-2 text-xs font-medium text-muted-foreground">最近尝试</div>
          {history.map((item) => (
            <div key={item.id} className="border-b border-border/50 px-3 py-2 last:border-b-0">
              <div className="flex min-w-0 items-center justify-between gap-2 text-xs">
                <span className="flex min-w-0 items-center gap-2">
                  <span className={cn("h-1.5 w-1.5 shrink-0 rounded-full", taskRunDot(item))} />
                  <span className="truncate font-medium text-foreground/85">{taskRunShortLabel(item)}</span>
                </span>
                <span className="shrink-0 tabular-nums text-muted-foreground">{formatTime(new Date(item.finished_at || item.started_at))}</span>
              </div>
              {item.status === "failed" && (item.error_type || item.error_message) ? (
                <div className="mt-1 line-clamp-2 break-words text-xs leading-4 text-muted-foreground">
                  {taskRunErrorTitle(item)}：{taskRunErrorMessage(item)}
                </div>
              ) : null}
            </div>
          ))}
        </div>
      ) : null}
    </div>
  )
}

function MemoryChangeTimelineItem({ change, canUndo, onUndo }: { change: SessionMemoryChange; canUndo: boolean; onUndo: () => void }) {
  const stamp = formatChangeStamp(change.created_at)
  return (
    <div className="grid grid-cols-[3.5rem_minmax(0,1fr)] gap-3">
      <div className="pt-0.5 text-right">
        <div className="text-xs font-medium tabular-nums text-foreground">{stamp.date}</div>
        <div className="mt-0.5 text-xs tabular-nums text-muted-foreground">{stamp.time}</div>
      </div>
      <div className="relative min-w-0 border-l border-border/70 pb-3 pl-3">
        <span className={cn("absolute -left-[5px] top-1 h-2.5 w-2.5 rounded-full ring-4 ring-background", change.source === "compact" ? "bg-amber-400" : change.source === "auto" ? "bg-sky-400" : change.source === "tool" ? "bg-violet-400" : "bg-muted-foreground/60")} />
        <div className="flex min-w-0 items-start justify-between gap-2">
          <div className="min-w-0 break-words text-sm leading-5">{displayChangeSummary(change)}</div>
          <span className={cn("shrink-0 rounded-full px-2 py-0.5 text-xs", changeSourceClass(change.source))}>
            {sourceLabel(change.source)}
          </span>
        </div>
        <div className="mt-1 flex min-w-0 items-center justify-between gap-2 text-xs text-muted-foreground">
          <span className="inline-flex min-w-0 items-center gap-1 truncate">
            <Clock3 className="h-3 w-3 shrink-0" />
            <span className="truncate">{stamp.full}</span>
          </span>
          {canUndo ? (
            <Button type="button" variant="ghost" size="sm" className="h-7 shrink-0 px-2 text-xs" onClick={onUndo}>
              <RotateCcw className="h-3.5 w-3.5" />
              撤销
            </Button>
          ) : null}
        </div>
      </div>
    </div>
  )
}

function changeSourceClass(source: SessionMemoryChange["source"]) {
  if (source === "auto") return "bg-sky-100 text-sky-700 dark:bg-sky-500/20 dark:text-sky-300"
  if (source === "tool") return "bg-violet-100 text-violet-700 dark:bg-violet-500/20 dark:text-violet-300"
  if (source === "compact") return "bg-amber-100 text-amber-700 dark:bg-amber-500/20 dark:text-amber-300"
  if (source === "undo") return "bg-rose-100 text-rose-700 dark:bg-rose-500/20 dark:text-rose-300"
  return "bg-secondary text-secondary-foreground"
}

function isCurrentProgressItem(item: string) {
  const normalized = item.trim().toLowerCase()
  return normalized.startsWith("current:") || normalized.startsWith("当前：") || normalized.startsWith("当前:")
}

function taskRunLabel(run: ModelTaskRun) {
  if (retryAfterDate(run)) return "自动维护冷却中"
  if (run.status === "failed") return "最近维护失败"
  if (run.status === "skipped") return "最近维护跳过"
  return "最近维护成功"
}

function taskRunShortLabel(run: ModelTaskRun) {
  if (run.status === "failed") return `${sourceLabel(run.source)}失败`
  if (run.status === "skipped") return `${sourceLabel(run.source)}跳过`
  return `${sourceLabel(run.source)}成功`
}

function taskRunTone(run: ModelTaskRun) {
  if (retryAfterDate(run)) return "bg-amber-100 text-amber-700 dark:bg-amber-500/20 dark:text-amber-300"
  if (run.status === "failed") return "bg-rose-100 text-rose-700 dark:bg-rose-500/20 dark:text-rose-300"
  if (run.status === "skipped") return "bg-secondary text-secondary-foreground"
  return "bg-emerald-100 text-emerald-700 dark:bg-emerald-500/20 dark:text-emerald-300"
}

function taskRunFrame(run: ModelTaskRun) {
  if (retryAfterDate(run)) return "border-amber-200/80 bg-amber-50/55 text-amber-900 dark:border-amber-400/20 dark:bg-amber-400/10 dark:text-amber-100"
  if (run.status === "failed") return "border-rose-200/80 bg-rose-50/55 text-rose-900 dark:border-rose-400/20 dark:bg-rose-400/10 dark:text-rose-100"
  if (run.status === "skipped") return "border-border/70 bg-background/80 text-foreground"
  return "border-emerald-200/80 bg-emerald-50/55 text-emerald-900 dark:border-emerald-400/20 dark:bg-emerald-400/10 dark:text-emerald-100"
}

function taskRunDot(run: ModelTaskRun) {
  if (retryAfterDate(run)) return "bg-amber-500"
  if (run.status === "failed") return "bg-rose-500"
  if (run.status === "skipped") return "bg-muted-foreground/50"
  return "bg-emerald-500"
}

function taskRunErrorTitle(run: ModelTaskRun) {
  const type = run.error_type?.toLowerCase() || ""
  if (type.includes("deadline") || type.includes("timeout")) return "模型响应超时"
  if (type.includes("cancel")) return "请求已取消"
  if (type === "parse_error") return "返回格式无法解析"
  if (type === "validation_error") return "返回内容未通过校验"
  return "模型调用失败"
}

function taskRunErrorMessage(run: ModelTaskRun) {
  const message = run.error_message?.trim() || ""
  if (/[\u3400-\u9fff]/u.test(message)) return message
  const type = run.error_type?.toLowerCase() || ""
  if (type.includes("deadline") || type.includes("timeout")) return "模型未在限定时间内完成，可稍后重试。"
  if (type.includes("cancel")) return "本次维护已取消，记忆内容没有变化。"
  if (type === "parse_error") return "模型返回格式不完整，记忆内容没有变化。"
  if (type === "validation_error") return "模型返回内容不符合记忆规则，记忆内容没有变化。"
  return "本次维护未写入记忆，可稍后重试。"
}

function retryAfterDate(run: ModelTaskRun) {
  if (!run.retry_after) return null
  const date = new Date(run.retry_after)
  if (Number.isNaN(date.getTime()) || date.getTime() <= Date.now()) return null
  return date
}

function actionLabel(action: SessionMemoryChange["action"]) {
  if (action === "compact") return "整理记忆"
  if (action === "clear") return "清空记忆"
  if (action === "undo") return "撤销变更"
  return "更新记忆"
}

function displayChangeSummary(change: SessionMemoryChange) {
  const summary = change.summary?.trim()
  return summary && /[\u3400-\u9fff]/u.test(summary) ? summary : actionLabel(change.action)
}

function formatDateTime(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return "-"
  return date.toLocaleString("zh-CN", { hour12: false })
}

function formatChangeStamp(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return { date: "-", time: "-", full: "-" }
  }
  return {
    date: date.toLocaleDateString("zh-CN", { month: "numeric", day: "numeric" }),
    time: date.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit", hour12: false }),
    full: date.toLocaleString("zh-CN", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit", hour12: false }),
  }
}

function formatTime(date: Date) {
  return date.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit", hour12: false })
}

function formatDuration(ms: number) {
  if (!ms) return "0s"
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(ms < 10000 ? 1 : 0)}s`
}

function latestAutoChangeId(data: SessionMemoryResponse) {
  return data.changes.find((change) => change.source === "auto")?.id ?? null
}

function latestUndoableChangeId(changes: SessionMemoryChange[]) {
  const change = changes[0]
  if (!change || change.undone_at || change.source !== "compact" || change.action !== "compact") return null
  return change.id
}
