import { useEffect, useRef, useState, type Dispatch, type SetStateAction } from "react"
import { adminApi, type CreateGroupInput, type UpdateGroupInput } from "@/api/admin"
import type { UserGroup } from "@/types"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { MotionView } from "@/components/ui/motion"
import { ChevronLeft, Plus, Trash2, X } from "lucide-react"
import { BusyOwnership, EditorOwnership } from "./editorOwnership"

interface Props {
  groups: UserGroup[]
  setGroups: Dispatch<SetStateAction<UserGroup[]>>
  setError: (error: string) => void
}

type GroupDraft = CreateGroupInput & { id?: number }

const emptyGroup: GroupDraft = {
  name: "",
  level: 0,
  description: "",
  is_default: false,
  daily_message_limit: 0,
  daily_token_limit: 0,
  concurrent_run_limit: 0,
  daily_tool_call_limit: 0,
  daily_web_search_limit: 0,
  daily_web_extract_limit: 0,
  daily_ocr_file_limit: 0,
  daily_ocr_page_limit: 0,
}

export function AdminGroupsPanel({ groups, setGroups, setError }: Props) {
  const [draft, setDraft] = useState<GroupDraft | null>(null)
  const [saving, setSaving] = useState("")
  const [editorOwner] = useState(() => new EditorOwnership())
  const [busyOwner] = useState(() => new BusyOwnership())
  const mountedRef = useRef(true)
  const activeId = draft?.id

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      editorOwner.invalidate()
    }
  }, [editorOwner])

  function beginBusy(label: string, scope: string) {
    const operationId = busyOwner.begin(label, scope)
    setSaving(label)
    return operationId
  }

  function finishBusy(operationId: number) {
    const remainingLabel = busyOwner.release(operationId)
    if (remainingLabel !== null && mountedRef.current) setSaving(remainingLabel)
  }

  function invalidateBusy(scope: string) {
    setSaving(busyOwner.invalidate(scope))
  }

  function groupKey(id?: number) {
    return id ? `group:${id}` : "new-group"
  }

  function canLeaveGroupEditor(nextKey: string) {
    if (!editorOwner.isDirty()) return true
    if (editorOwner.currentEntityKey() === nextKey) return false
    return window.confirm("放弃当前分组的未保存修改？")
  }

  function activateGroupEditor(nextDraft: GroupDraft) {
    invalidateBusy("group-editor")
    editorOwner.activate(groupKey(nextDraft.id))
    setDraft(nextDraft)
  }

  function changeDraft(update: SetStateAction<GroupDraft | null>) {
    editorOwner.change()
    setDraft(update)
  }

  function closeEditor() {
    if (!canLeaveGroupEditor("")) return
    invalidateBusy("group-editor")
    editorOwner.invalidate()
    setDraft(null)
  }

  function startCreate() {
    if (!canLeaveGroupEditor("new-group")) return
    activateGroupEditor({ ...emptyGroup, level: nextLevel(groups) })
  }

  function startEdit(group: UserGroup) {
    const key = groupKey(group.id)
    if (editorOwner.currentEntityKey() === key && draft?.id === group.id) return
    if (!canLeaveGroupEditor(key)) return
    activateGroupEditor({
      id: group.id,
      name: group.name,
      level: group.level,
      description: group.description || "",
      is_default: group.is_default,
      daily_message_limit: group.daily_message_limit || 0,
      daily_token_limit: group.daily_token_limit || 0,
      concurrent_run_limit: group.concurrent_run_limit || 0,
      daily_tool_call_limit: group.daily_tool_call_limit || 0,
      daily_web_search_limit: group.daily_web_search_limit || 0,
      daily_web_extract_limit: group.daily_web_extract_limit || 0,
      daily_ocr_file_limit: group.daily_ocr_file_limit || 0,
      daily_ocr_page_limit: group.daily_ocr_page_limit || 0,
    })
  }

  function sortGroups(list: UserGroup[]) {
    return [...list].sort((a, b) => a.level - b.level || a.id - b.id)
  }

  async function saveGroup() {
    if (!draft) return
    if (!draft.name.trim()) {
      setError("分组名称不能为空")
      return
    }
    const currentDraft = draft
    const operation = editorOwner.beginOperation()
    const busy = beginBusy(currentDraft.id ? `group-${currentDraft.id}` : "create", "group-editor")
    setError("")
    try {
      if (currentDraft.id) {
        const payload: UpdateGroupInput = {
          name: currentDraft.name,
          level: currentDraft.level,
          description: currentDraft.description || "",
          is_default: currentDraft.is_default,
          daily_message_limit: currentDraft.daily_message_limit || 0,
          daily_token_limit: currentDraft.daily_token_limit || 0,
          concurrent_run_limit: currentDraft.concurrent_run_limit || 0,
          daily_tool_call_limit: currentDraft.daily_tool_call_limit || 0,
          daily_web_search_limit: currentDraft.daily_web_search_limit || 0,
          daily_web_extract_limit: currentDraft.daily_web_extract_limit || 0,
          daily_ocr_file_limit: currentDraft.daily_ocr_file_limit || 0,
          daily_ocr_page_limit: currentDraft.daily_ocr_page_limit || 0,
        }
        const updated = await adminApi.updateGroup(currentDraft.id, payload)
        // A committed mutation must converge the shared catalog after navigation;
        // only editor-local state is fenced by the captured generation/revision.
        setGroups((prev) => sortGroups(prev.map((g) => (g.id === updated.id ? updated : g))))
        if (editorOwner.owns(operation, false)) {
          editorOwner.acknowledge(operation.revision)
          if (editorOwner.owns(operation)) {
            editorOwner.invalidate()
            setDraft(null)
          } else {
            setError("已保存较早版本，当前修改仍未保存")
          }
        }
      } else {
        const created = await adminApi.createGroup(currentDraft)
        setGroups((prev) => sortGroups([...prev, created]))
        if (editorOwner.owns(operation, false)) {
          const unchanged = editorOwner.owns(operation)
          editorOwner.rekey(groupKey(created.id))
          editorOwner.acknowledge(operation.revision)
          if (unchanged) {
            editorOwner.invalidate()
            setDraft(null)
          } else {
            setDraft((prev) => prev ? { ...prev, id: created.id } : prev)
            setError("已保存较早版本，当前修改仍未保存")
          }
        }
      }
    } catch (err) {
      if (editorOwner.owns(operation, false)) {
        setError(err instanceof Error ? err.message : "分组保存失败")
      }
    } finally {
      finishBusy(busy)
    }
  }

  async function deleteGroup(group: UserGroup) {
    if (!window.confirm(`删除分组「${group.name}」？显式绑定该组的用户将继承当前默认组。`)) return
    const operation = editorOwner.beginOperation()
    const busy = beginBusy(`delete-${group.id}`, "group-editor")
    setError("")
    try {
      await adminApi.deleteGroup(group.id)
      setGroups((prev) => prev.filter((g) => g.id !== group.id))
      if (editorOwner.owns(operation, false)) {
        editorOwner.invalidate()
        setDraft(null)
      }
    } catch (err) {
      if (editorOwner.owns(operation, false)) {
        setError(err instanceof Error ? err.message : "分组删除失败")
      }
    } finally {
      finishBusy(busy)
    }
  }

  return (
    <div className="flex h-full min-h-0 overflow-hidden lg:grid lg:grid-cols-[minmax(0,1fr)_390px]">
      <div className={`min-h-0 flex-1 flex-col overflow-hidden border-b border-border/70 lg:flex lg:border-b-0 lg:border-r ${draft ? "hidden lg:flex" : "flex"}`}>
          <div className="flex items-center justify-between border-b border-border/70 px-4 py-3">
            <div className="font-medium">分级组</div>
            <Button size="sm" onClick={startCreate}>
              <Plus className="h-3.5 w-3.5" />
              新建分组
            </Button>
          </div>
          <div className="hidden grid-cols-[minmax(0,1fr)_72px_220px_72px] gap-3 border-b border-border/70 px-4 py-2.5 text-sm text-muted-foreground md:grid">
            <span>名称</span>
            <span>等级</span>
            <span>限额</span>
            <span>默认</span>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto">
            {groups.length === 0 ? (
              <div className="flex h-full items-center justify-center text-sm text-muted-foreground">暂无分级组</div>
            ) : (
              groups.map((group) => (
                <button
                  key={group.id}
                  onClick={() => startEdit(group)}
                  className={`grid w-full grid-cols-[minmax(0,1fr)_72px] items-center gap-3 border-b px-4 py-2.5 text-left transition-colors motion-control last:border-b-0 md:grid-cols-[minmax(0,1fr)_72px_220px_72px] ${
                    activeId === group.id
                      ? "border-l-4 border-l-foreground border-b-border/70 bg-muted/40"
                      : "border-border/60"
                  }`}
                >
                  <div className="min-w-0">
                    <div className="truncate font-medium">{group.name}</div>
                    {group.description && <div className="truncate text-xs text-muted-foreground">{group.description}</div>}
                    <div className="mt-1 truncate text-sm text-muted-foreground md:hidden">{quotaSummary(group)}</div>
                  </div>
                  <span className="text-sm tabular-nums">{group.level}</span>
                  <span className="hidden truncate text-sm text-muted-foreground md:block">
                    {quotaSummary(group)}
                  </span>
                  <span className="hidden text-sm text-muted-foreground md:block">{group.is_default ? "是" : "-"}</span>
                </button>
              ))
            )}
          </div>
      </div>

      <div className={`min-h-0 flex-1 flex-col overflow-hidden lg:flex ${draft ? "flex" : "hidden lg:flex"}`}>
          <div className="flex items-center justify-between border-b border-border/70 px-4 py-3">
            <div className="flex min-w-0 items-center gap-1">
              {draft ? (
                <Button variant="ghost" size="sm" className="h-8 px-1.5 lg:hidden" onClick={closeEditor}>
                  <ChevronLeft className="h-3.5 w-3.5" />
                  返回
                </Button>
              ) : null}
              <div className="truncate font-medium">{draft ? (draft.id ? "编辑分组" : "新建分组") : "分组详情"}</div>
            </div>
            {draft && (
              <Button variant="ghost" size="sm" className="hidden lg:inline-flex" onClick={closeEditor}>
                <X className="h-3.5 w-3.5" />
              </Button>
            )}
          </div>
          <MotionView viewKey={draft ? draft.id || "new" : "empty"} className="flex min-h-0 flex-1 flex-col">
            {draft ? (
              <>
              <div className="min-h-0 flex-1 overflow-y-auto p-4">
                <div className="space-y-3">
                  <Field label="名称">
                    <Input value={draft.name} onChange={(e) => changeDraft((prev) => prev ? { ...prev, name: e.target.value } : prev)} />
                  </Field>
                  <Field label="等级（越大权限越高，模型按此过滤）">
                    <Input type="number" min={0} value={draft.level} onChange={(e) => changeDraft((prev) => prev ? { ...prev, level: Math.max(0, Number(e.target.value) || 0) } : prev)} />
                  </Field>
                  <Field label="描述">
                    <Input value={draft.description || ""} onChange={(e) => changeDraft((prev) => prev ? { ...prev, description: e.target.value } : prev)} />
                  </Field>
                  <div className="border-t border-border/70 pt-3">
                    <div className="mb-2 text-sm font-semibold">对话和模型</div>
                    <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
                    <Field label="每日消息">
                      <Input type="number" min={0} value={draft.daily_message_limit || 0} onChange={(e) => changeDraft((prev) => prev ? { ...prev, daily_message_limit: Math.max(0, Number(e.target.value) || 0) } : prev)} />
                    </Field>
                    <Field label="每日 token">
                      <Input type="number" min={0} value={draft.daily_token_limit || 0} onChange={(e) => changeDraft((prev) => prev ? { ...prev, daily_token_limit: Math.max(0, Number(e.target.value) || 0) } : prev)} />
                    </Field>
                    <Field label="并发 run">
                      <Input type="number" min={0} value={draft.concurrent_run_limit || 0} onChange={(e) => changeDraft((prev) => prev ? { ...prev, concurrent_run_limit: Math.max(0, Number(e.target.value) || 0) } : prev)} />
                    </Field>
                    </div>
                  </div>
                  <div className="border-t border-border/70 pt-3">
                    <div className="mb-2 text-sm font-semibold">工具和联网</div>
                    <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
                      <Field label="每日工具调用">
                        <Input type="number" min={0} value={draft.daily_tool_call_limit || 0} onChange={(e) => changeDraft((prev) => prev ? { ...prev, daily_tool_call_limit: Math.max(0, Number(e.target.value) || 0) } : prev)} />
                      </Field>
                      <Field label="每日搜索">
                        <Input type="number" min={0} value={draft.daily_web_search_limit || 0} onChange={(e) => changeDraft((prev) => prev ? { ...prev, daily_web_search_limit: Math.max(0, Number(e.target.value) || 0) } : prev)} />
                      </Field>
                      <Field label="每日网页提取">
                        <Input type="number" min={0} value={draft.daily_web_extract_limit || 0} onChange={(e) => changeDraft((prev) => prev ? { ...prev, daily_web_extract_limit: Math.max(0, Number(e.target.value) || 0) } : prev)} />
                      </Field>
                      <Field label="每日 OCR 文件">
                        <Input type="number" min={0} value={draft.daily_ocr_file_limit || 0} onChange={(e) => changeDraft((prev) => prev ? { ...prev, daily_ocr_file_limit: Math.max(0, Number(e.target.value) || 0) } : prev)} />
                      </Field>
                      <Field label="每日 OCR 页数">
                        <Input type="number" min={0} value={draft.daily_ocr_page_limit || 0} onChange={(e) => changeDraft((prev) => prev ? { ...prev, daily_ocr_page_limit: Math.max(0, Number(e.target.value) || 0) } : prev)} />
                      </Field>
                    </div>
                  </div>
                  <label className="flex items-center gap-2 text-sm">
                    <input
                      type="checkbox"
                      checked={draft.is_default}
                      onChange={(e) => changeDraft((prev) => prev ? { ...prev, is_default: e.target.checked } : prev)}
                    />
                    设为默认组（未显式分组的用户将动态继承）
                  </label>
                </div>
              </div>
              <div className="flex items-center justify-between border-t border-border/70 px-4 py-3">
                {draft.id ? (
                  <Button
                    variant="ghost"
                    size="sm"
                    className="text-destructive hover:text-destructive"
                    onClick={() => {
                      const group = groups.find((g) => g.id === draft.id)
                      if (group) deleteGroup(group)
                    }}
                    disabled={saving !== ""}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                    删除
                  </Button>
                ) : <span />}
                <Button size="sm" onClick={saveGroup} disabled={saving !== ""}>
                  保存
                </Button>
              </div>
              </>
            ) : (
              <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">左侧选择分组</div>
            )}
          </MotionView>
      </div>
    </div>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="block [&_input]:h-8 [&_select]:h-8">
      <span className="mb-1.5 block text-sm font-medium">{label}</span>
      {children}
    </label>
  )
}

function nextLevel(groups: UserGroup[]) {
  if (groups.length === 0) return 10
  return Math.max(...groups.map((g) => g.level)) + 10
}

function quotaSummary(group: UserGroup) {
  const messages = group.daily_message_limit ? `${group.daily_message_limit} msg` : "msg ∞"
  const tokens = group.daily_token_limit ? `${formatQuotaNumber(group.daily_token_limit)} tok` : "tok ∞"
  const runs = group.concurrent_run_limit ? `${group.concurrent_run_limit} run` : "run ∞"
  const tools = group.daily_tool_call_limit ? `${group.daily_tool_call_limit} tool` : "tool ∞"
  const web = [
    group.daily_web_search_limit ? `${group.daily_web_search_limit} search` : "search ∞",
    group.daily_web_extract_limit ? `${group.daily_web_extract_limit} extract` : "extract ∞",
    group.daily_ocr_file_limit ? `${group.daily_ocr_file_limit} ocr` : "ocr ∞",
  ].join(" / ")
  return `${messages} / ${tokens} / ${runs} / ${tools} / ${web}`
}

function formatQuotaNumber(value: number) {
  if (value >= 1000000) return `${Math.round(value / 1000000)}M`
  if (value >= 1000) return `${Math.round(value / 1000)}K`
  return String(value)
}
