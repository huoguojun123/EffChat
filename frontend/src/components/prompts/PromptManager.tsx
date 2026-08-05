import { useCallback, useEffect, useMemo, useRef, useState, type SetStateAction } from "react"
import { adminApi } from "@/api/admin"
import { promptsApi, type PromptInput } from "@/api/prompts"
import { useAuthStore } from "@/stores/auth"
import type { Prompt, PromptGroup } from "@/types"
import { Button } from "@/components/ui/button"
import { MotionView } from "@/components/ui/motion"
import { Check, ChevronLeft, CopyPlus, Folder, FolderPlus, Globe, Lock, Pencil, Plus, Trash2, X } from "lucide-react"
import { BusyOwnership, EditorOwnership } from "@/components/admin/editorOwnership"

interface Props {
  scope: "user" | "admin"
}

const defaultGroupName = "默认分组"

const emptyDraft: PromptInput = {
  title: "",
  content: "",
  description: "",
  group_id: null,
  group_name: defaultGroupName,
  tags: [],
  is_public: false,
}

export function PromptManager({ scope }: Props) {
  const user = useAuthStore((s) => s.user)
  const [prompts, setPrompts] = useState<Prompt[]>([])
  const [groups, setGroups] = useState<PromptGroup[]>([])
  const [selectedId, setSelectedId] = useState<number | "new">("new")
  const [draft, setDraft] = useState<PromptInput>(emptyDraft)
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [feedback, setFeedback] = useState("")
  const [mobileListOpen, setMobileListOpen] = useState(true)
  const [editorOwner] = useState(() => new EditorOwnership())
  const [loadOwner] = useState(() => new EditorOwnership())
  const [busyOwner] = useState(() => new BusyOwnership())
  const mountedRef = useRef(true)

  const selected = useMemo(
    () => prompts.find((item) => item.id === selectedId),
    [prompts, selectedId]
  )
  const canEdit = scope === "admin" || selectedId === "new" || Boolean(selected && selected.user_id === user?.id && !selected.is_public)
  const currentUserGroupIds = useMemo(
    () => new Set(groups.filter((group) => group.user_id === user?.id).map((group) => group.id)),
    [groups, user?.id]
  )
  const editableGroups = useMemo(() => groups, [groups])

  const grouped = useMemo(() => {
    const map = new Map<string, { groupId: number | null; groupName: string; items: Prompt[] }>()
    for (const prompt of prompts) {
      const groupId = prompt.group_id ?? null
      const groupName = prompt.group_name || defaultGroupName
      const key = groupId == null ? `name:${groupName}` : `id:${groupId}`
      const bucket = map.get(key) || { groupId, groupName, items: [] }
      bucket.items.push(prompt)
      map.set(key, bucket)
    }
    return Array.from(map.values())
      .map((group) => ({
        ...group,
        items: group.items.sort((a, b) => new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime()),
      }))
      .sort((a, b) => a.groupName.localeCompare(b.groupName, "zh-CN"))
  }, [prompts])

  function beginBusy(scopeName: string) {
    const operationId = busyOwner.begin("saving", scopeName)
    setSaving(true)
    return operationId
  }

  function finishBusy(operationId: number) {
    const label = busyOwner.release(operationId)
    if (label !== null && mountedRef.current) setSaving(label !== "")
  }

  function invalidateBusy(scopeName: string) {
    setSaving(busyOwner.invalidate(scopeName) !== "")
  }

  function promptKey(id: number | "new") {
    return id === "new" ? "new-prompt" : `prompt:${id}`
  }

  function canLeavePrompt(nextKey: string) {
    if (!editorOwner.isDirty()) return true
    if (editorOwner.currentEntityKey() === nextKey) return false
    return window.confirm("放弃当前提示词的未保存修改？")
  }

  function changeDraft(update: SetStateAction<PromptInput>) {
    editorOwner.change()
    setFeedback("")
    setDraft(update)
  }

  function selectPrompt(prompt: Prompt) {
    const key = promptKey(prompt.id)
    if (editorOwner.currentEntityKey() === key && selectedId === prompt.id) return
    if (!canLeavePrompt(key)) return
    invalidateBusy("prompt-editor")
    invalidateBusy("prompt-group")
    editorOwner.activate(key)
    setSelectedId(prompt.id)
    setDraft({
      title: prompt.title,
      content: prompt.content,
      description: prompt.description || "",
      group_id: prompt.group_id ?? null,
      group_name: prompt.group_name || defaultGroupName,
      tags: [],
      is_public: scope === "admin",
    })
    setFeedback("")
    setMobileListOpen(false)
  }

  function startNew(base?: Prompt) {
    const key = "new-prompt"
    if (!base && editorOwner.currentEntityKey() === key && selectedId === "new") return
    if (!canLeavePrompt(key)) return
    const reusableGroupId = base?.group_id && currentUserGroupIds.has(base.group_id) ? base.group_id : null
    const reusableGroup = reusableGroupId ? groups.find((group) => group.id === reusableGroupId) : null
    invalidateBusy("prompt-editor")
    invalidateBusy("prompt-group")
    editorOwner.activate(key)
    if (base) editorOwner.change()
    setSelectedId("new")
    setMobileListOpen(false)
    setDraft(base ? {
      title: `${base.title} - 我的版本`,
      content: base.content,
      description: base.description || "",
      group_id: reusableGroup?.id ?? null,
      group_name: reusableGroup?.name ?? defaultGroupName,
      tags: [],
      is_public: false,
    } : { ...emptyDraft })
    setFeedback("")
  }

  const loadData = useCallback(async () => {
    loadOwner.activate(scope)
    const operation = loadOwner.beginOperation()
    setLoading(true)
    setFeedback("")
    try {
      if (scope === "admin") {
        const promptRes = await adminApi.listPrompts()
        if (loadOwner.owns(operation)) {
          setPrompts(promptRes.prompts || [])
          setGroups([])
        }
        return
      }
      const [mine, pub, groupRes] = await Promise.all([
        promptsApi.listMine(),
        promptsApi.listPublic(),
        promptsApi.listGroups(),
      ])
      const map = new Map<number, Prompt>()
      for (const item of [...(mine.prompts || []), ...(pub.prompts || [])]) {
        map.set(item.id, item)
      }
      if (loadOwner.owns(operation)) {
        setPrompts(Array.from(map.values()))
        setGroups(sortGroups(groupRes.groups || []))
      }
    } finally {
      if (mountedRef.current && loadOwner.owns(operation, false)) setLoading(false)
    }
  }, [loadOwner, scope])

  useEffect(() => {
    mountedRef.current = true
    void Promise.resolve().then(loadData)
    return () => {
      mountedRef.current = false
      editorOwner.invalidate()
      loadOwner.invalidate()
    }
  }, [editorOwner, loadData, loadOwner])

  async function handleSave() {
    if (!draft.title.trim() || !draft.content.trim()) return
    const currentDraft = draft
    const currentSelectedID = selectedId
    const operation = editorOwner.beginOperation()
    const busy = beginBusy("prompt-editor")
    setFeedback("")
    try {
      const group = scope === "admin" ? null : currentDraft.group_id == null ? null : groups.find((item) => item.id === currentDraft.group_id) || null
      const payload = {
        ...currentDraft,
        title: currentDraft.title.trim(),
        content: currentDraft.content.trim(),
        description: currentDraft.description?.trim() || undefined,
        group_id: group?.id ?? null,
        group_name: group?.name ?? defaultGroupName,
        is_public: scope === "admin",
      }
      if (currentSelectedID === "new") {
        const created = scope === "admin" ? await adminApi.createPrompt(payload) : await promptsApi.create(payload)
        // Server commits always converge the shared catalog. Selection, draft,
        // feedback, and busy state remain fenced to the initiating editor.
        setPrompts((prev) => [created, ...prev])
        if (editorOwner.owns(operation, false)) {
          const unchanged = editorOwner.owns(operation)
          editorOwner.rekey(promptKey(created.id))
          editorOwner.acknowledge(operation.revision)
          setSelectedId(created.id)
          if (unchanged) {
            setDraft({ ...payload, title: created.title, content: created.content, description: created.description || "" })
            setFeedback("已保存")
          } else {
            setFeedback("已保存较早版本，当前修改仍未保存")
          }
        }
      } else {
        const updated = scope === "admin"
          ? await adminApi.updatePrompt(currentSelectedID, payload)
          : await promptsApi.update(currentSelectedID, payload)
        setPrompts((prev) => prev.map((item) => (item.id === updated.id ? updated : item)))
        if (editorOwner.owns(operation, false)) {
          editorOwner.acknowledge(operation.revision)
          if (editorOwner.owns(operation)) {
            setDraft({ ...payload, title: updated.title, content: updated.content, description: updated.description || "" })
            setFeedback("已保存")
          } else {
            setFeedback("已保存较早版本，当前修改仍未保存")
          }
        }
      }
    } catch (err) {
      if (editorOwner.owns(operation, false)) {
        setFeedback(err instanceof Error ? err.message : "保存失败")
      }
    } finally {
      finishBusy(busy)
    }
  }

  async function handleDelete() {
    if (selectedId === "new") return
    const targetID = selectedId
    const operation = editorOwner.beginOperation()
    const busy = beginBusy("prompt-editor")
    setFeedback("")
    try {
      if (scope === "admin") {
        await adminApi.deletePrompt(targetID)
      } else {
        await promptsApi.delete(targetID)
      }
      setPrompts((prev) => prev.filter((item) => item.id !== targetID))
      if (editorOwner.owns(operation, false)) {
        editorOwner.activate("new-prompt")
        setSelectedId("new")
        setDraft({ ...emptyDraft })
        setFeedback("已删除")
      }
    } catch (err) {
      if (editorOwner.owns(operation, false)) {
        setFeedback(err instanceof Error ? err.message : "删除失败")
      }
    } finally {
      finishBusy(busy)
    }
  }

  async function createGroup() {
    const name = window.prompt("输入新分组名称")
    if (!name?.trim()) return
    const operation = editorOwner.beginOperation()
    const busy = beginBusy("prompt-group")
    setFeedback("")
    try {
      if (scope === "admin") return
      const group = await promptsApi.createGroup(name.trim())
      setGroups((prev) => sortGroups([...prev, group]))
      if (editorOwner.owns(operation)) {
        editorOwner.change()
        setDraft((prev) => ({ ...prev, group_id: group.id, group_name: group.name }))
        setFeedback("分组已创建")
      }
    } catch (err) {
      if (editorOwner.owns(operation, false)) {
        setFeedback(err instanceof Error ? err.message : "创建分组失败")
      }
    } finally {
      finishBusy(busy)
    }
  }

  async function renameGroup() {
    const groupID = draft.group_id
    if (groupID == null) return
    const group = groups.find((item) => item.id === groupID)
    if (!group) return
    const name = window.prompt("输入新的分组名称", group.name)
    if (!name?.trim() || name.trim() === group.name) return
    const operation = editorOwner.beginOperation()
    const busy = beginBusy("prompt-group")
    setFeedback("")
    try {
      if (scope === "admin") return
      const updated = await promptsApi.updateGroup(groupID, name.trim())
      setGroups((prev) => sortGroups(prev.map((item) => (item.id === groupID ? updated : item))))
      setPrompts((prev) => prev.map((item) => (item.group_id === groupID ? { ...item, group_name: updated.name } : item)))
      if (editorOwner.owns(operation)) {
        setDraft((prev) => ({ ...prev, group_name: updated.name }))
        setFeedback("分组已重命名")
      }
    } catch (err) {
      if (editorOwner.owns(operation, false)) {
        setFeedback(err instanceof Error ? err.message : "重命名分组失败")
      }
    } finally {
      finishBusy(busy)
    }
  }

  async function deleteGroup() {
    const groupID = draft.group_id
    if (groupID == null) return
    const group = groups.find((item) => item.id === groupID)
    if (!group || !window.confirm(`删除分组“${group.name}”？提示词会移动到默认分组。`)) return
    const operation = editorOwner.beginOperation()
    const busy = beginBusy("prompt-group")
    setFeedback("")
    try {
      if (scope === "admin") return
      await promptsApi.deleteGroup(groupID)
      setGroups((prev) => prev.filter((item) => item.id !== groupID))
      setPrompts((prev) => prev.map((item) => (
        item.group_id === groupID ? { ...item, group_id: null, group_name: defaultGroupName } : item
      )))
      if (editorOwner.owns(operation)) {
        setDraft((prev) => ({ ...prev, group_id: null, group_name: defaultGroupName }))
        setFeedback("分组已删除")
      }
    } catch (err) {
      if (editorOwner.owns(operation, false)) {
        setFeedback(err instanceof Error ? err.message : "删除分组失败")
      }
    } finally {
      finishBusy(busy)
    }
  }

  return (
    <div className="h-full min-h-0">
      <div className="grid h-full min-h-0 lg:grid-cols-[232px_minmax(0,1fr)]">
        <div className={`min-h-0 overflow-hidden border-b border-border/70 lg:border-b-0 lg:border-r ${mobileListOpen ? "block" : "hidden lg:block"}`}>
          <div className="border-b border-border/70 px-3.5 py-3">
            <div className="flex items-center gap-3">
              <div className="text-sm font-medium">提示词列表</div>
              <Button size="sm" className="ml-auto h-8 rounded-lg px-3" onClick={() => startNew()}>
                <Plus className="h-3.5 w-3.5" />
                新建
              </Button>
              <Button variant="ghost" size="sm" className="h-8 rounded-lg px-2.5" onClick={loadData} disabled={loading}>
                刷新
              </Button>
            </div>
          </div>
          <div className="h-[calc(100%-62px)] overflow-y-auto px-2 py-2">
            <div className="space-y-3">
              {grouped.map((group) => {
                return (
                  <section key={`${group.groupId ?? "legacy"}-${group.groupName}`} className="border-b border-border/60 pb-1 last:border-b-0">
                    <div className="flex items-center gap-2 px-2 py-1.5">
                      <Folder className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                      <div className="min-w-0 flex-1 truncate text-xs font-medium text-muted-foreground">
                        {group.groupName}
                      </div>
                      <span className="text-[11px] tabular-nums text-muted-foreground">{group.items.length}</span>
                    </div>
                    <div>
                      {group.items.map((prompt) => {
                        const readonly = scope === "user" && (prompt.is_public || prompt.user_id !== user?.id)
                        return (
                          <button
                            key={prompt.id}
                            onClick={() => selectPrompt(prompt)}
                            className={`w-full px-3 py-2 text-left transition-colors motion-control ${
                              selectedId === prompt.id ? "bg-muted text-foreground" : "hover:bg-muted/70"
                            }`}
                          >
                            <div className="flex items-start gap-3">
                              <div className="min-w-0 flex-1 truncate text-sm font-medium">{prompt.title}</div>
                              <div className="flex shrink-0 items-center gap-1.5 pt-0.5">
                                {readonly ? <Lock className="h-3.5 w-3.5 opacity-70" /> : null}
                                {prompt.is_public ? <Globe className="h-3.5 w-3.5 opacity-70" /> : null}
                              </div>
                            </div>
                          </button>
                        )
                      })}
                    </div>
                  </section>
                )
              })}
              {!loading && prompts.length === 0 ? (
                <div className="px-4 py-8 text-center text-sm text-muted-foreground">
                  暂无提示词
                </div>
              ) : null}
            </div>
          </div>
        </div>

        <div className={`flex min-h-0 flex-col overflow-hidden ${mobileListOpen ? "hidden lg:flex" : "flex"}`}>
          <div className="border-b border-border/70 px-4 py-3.5">
            <div className="flex items-start justify-between gap-4">
              <div className="flex items-center gap-1 text-sm font-semibold">
                <Button variant="ghost" size="icon" className="-ml-1 h-8 w-8 lg:hidden" onClick={() => setMobileListOpen(true)} aria-label="返回提示词列表">
                  <ChevronLeft className="h-4 w-4" />
                </Button>
                {selectedId === "new" ? "新建提示词" : "编辑提示词"}
              </div>
              {!canEdit && selected ? (
                <Button variant="outline" size="sm" className="rounded-lg" onClick={() => startNew(selected)}>
                  <CopyPlus className="h-3.5 w-3.5" />
                  基于此新建
                </Button>
              ) : null}
            </div>
          </div>

          <div className="min-h-0 flex-1 overflow-y-auto px-4 py-3.5">
            <MotionView viewKey={selectedId} className="space-y-3.5">
              <div className="grid gap-3 xl:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
                {scope === "user" ? <Field label="分组" className="mb-0">
                  <div className="flex gap-2">
                    <select
                      value={draft.group_id ?? ""}
                      onChange={(e) => {
                        const groupId = e.target.value ? Number(e.target.value) : null
                        const group = groupId == null ? null : groups.find((item) => item.id === groupId) || null
                        changeDraft((prev) => ({ ...prev, group_id: group?.id ?? null, group_name: group?.name ?? defaultGroupName }))
                      }}
                      disabled={!canEdit}
                      className="prompt-input"
                    >
                      <option value="">默认分组</option>
                      {editableGroups.map((group) => (
                        <option key={group.id} value={group.id}>{group.name}</option>
                      ))}
                      {draft.group_id != null && !editableGroups.some((group) => group.id === draft.group_id) ? (
                        <option value={draft.group_id}>{draft.group_name}</option>
                      ) : null}
                    </select>
                    {canEdit ? (
                      <div className="flex shrink-0 gap-1">
                        <IconButton title="新建分组" onClick={createGroup}><FolderPlus className="h-3.5 w-3.5" /></IconButton>
                        <IconButton title="重命名分组" onClick={renameGroup} disabled={draft.group_id == null}><Pencil className="h-3.5 w-3.5" /></IconButton>
                        <IconButton title="删除分组" onClick={deleteGroup} disabled={draft.group_id == null}><X className="h-3.5 w-3.5" /></IconButton>
                      </div>
                    ) : null}
                  </div>
                </Field> : null}
                <Field label="名称" className="mb-0">
                  <input
                    value={draft.title}
                    onChange={(e) => changeDraft((prev) => ({ ...prev, title: e.target.value }))}
                    disabled={!canEdit}
                    className="prompt-input"
                    placeholder="例如：Go 专家"
                  />
                </Field>
              </div>

              <Field label="内容" className="mb-0">
                <textarea
                  value={draft.content}
                  onChange={(e) => changeDraft((prev) => ({ ...prev, content: e.target.value }))}
                  disabled={!canEdit}
                  className="prompt-textarea min-h-[190px]"
                />
              </Field>

              <Field label="备注" className="mb-0">
                <textarea
                  value={draft.description}
                  onChange={(e) => changeDraft((prev) => ({ ...prev, description: e.target.value }))}
                  disabled={!canEdit}
                  className="prompt-textarea min-h-[72px]"
                  placeholder="写给自己看的说明"
                />
              </Field>
            </MotionView>
          </div>

          <div className="flex shrink-0 items-center gap-2.5 border-t border-border/70 px-4 py-3">
            {scope === "admin" ? <span className="text-sm text-muted-foreground">共享提示词</span> : null}
            {feedback ? <span className="flex items-center gap-1 text-sm text-muted-foreground"><Check className="h-3.5 w-3.5" />{feedback}</span> : null}
            <div className="ml-auto flex shrink-0 gap-2">
              {selectedId !== "new" && canEdit ? (
                <Button variant="ghost" size="sm" className="rounded-lg" onClick={handleDelete} disabled={saving}>
                  <Trash2 className="h-3.5 w-3.5" />
                  删除
                </Button>
              ) : null}
              <Button size="sm" className="rounded-lg px-3.5" onClick={handleSave} disabled={saving || !canEdit || !draft.title.trim() || !draft.content.trim()}>
                {saving ? "保存中" : "保存"}
              </Button>
            </div>
          </div>
        </div>
      </div>

      <style>{`
        .prompt-input{height:36px;width:100%;border-radius:8px;border:1px solid var(--border);background:var(--background);padding:0 11px;font-size:14px;outline:none}
        .prompt-input:focus,.prompt-textarea:focus{border-color:var(--foreground)}
        .prompt-textarea{width:100%;resize:vertical;border-radius:8px;border:1px solid var(--border);background:var(--background);padding:10px 12px;font-size:14px;line-height:1.7;outline:none}
        .prompt-input:disabled,.prompt-textarea:disabled{opacity:.72}
      `}</style>
    </div>
  )
}

function Field({ label, children, className = "" }: { label: string; children: React.ReactNode; className?: string }) {
  return (
    <label className={`mb-3 block ${className}`}>
      <span className="mb-1.5 block text-sm font-medium">{label}</span>
      {children}
    </label>
  )
}

function IconButton({
  title,
  children,
  disabled,
  onClick,
}: {
  title: string
  children: React.ReactNode
  disabled?: boolean
  onClick: () => void
}) {
  return (
    <button
      type="button"
      title={title}
      disabled={disabled}
      onClick={onClick}
      className="flex h-9 w-9 items-center justify-center rounded-lg border border-border text-muted-foreground transition-colors motion-control hover:bg-muted hover:text-foreground disabled:cursor-not-allowed disabled:opacity-40"
    >
      {children}
    </button>
  )
}

function sortGroups(groups: PromptGroup[]) {
  return [...groups].sort((a, b) => a.name.localeCompare(b.name, "zh-CN") || a.id - b.id)
}
