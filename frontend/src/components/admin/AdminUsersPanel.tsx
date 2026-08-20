import { useEffect, useMemo, useRef, useState, type MouseEvent, type SetStateAction } from "react"
import { adminApi, type AdminUser, type CreateAdminUserInput, type UpdateAdminUserInput } from "@/api/admin"
import type { UserGroup } from "@/types"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { MotionView } from "@/components/ui/motion"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { ChevronLeft, ChevronRight, Plus, Search, Shield, X } from "lucide-react"
import { BusyOwnership, EditorOwnership } from "./editorOwnership"

const PAGE_SIZE = 15

interface Props {
  users: AdminUser[]
  setUsers: React.Dispatch<React.SetStateAction<AdminUser[]>>
  groups: UserGroup[]
  setError: (error: string) => void
  onDirtyChange?: (dirty: boolean) => void
}

type UserDraft = CreateAdminUserInput & { id?: number }

const emptyUser: UserDraft = {
  username: "",
  password: "",
  email: "",
  nickname: "",
  role: "user",
  is_active: true,
}

export function AdminUsersPanel({ users, setUsers, groups, setError, onDirtyChange }: Props) {
  const [draft, setDraft] = useState<UserDraft | null>(null)
  const [saving, setSaving] = useState("")
  const [resetPassword, setResetPassword] = useState("")
  const [resetPasswordOpen, setResetPasswordOpen] = useState(false)
  const [resetPasswordDirty, setResetPasswordDirty] = useState(false)
  const [pendingDiscard, setPendingDiscard] = useState<{ proceed: () => void } | null>(null)
  const [pendingPasswordDiscard, setPendingPasswordDiscard] = useState(false)
  const [query, setQuery] = useState("")
  const [page, setPage] = useState(1)
  const [editorOwner] = useState(() => new EditorOwnership())
  const [passwordOwner] = useState(() => new EditorOwnership())
  const [busyOwner] = useState(() => new BusyOwnership())
  const mountedRef = useRef(true)
  const discardTriggerRef = useRef<HTMLButtonElement | null>(null)
  const passwordDiscardTriggerRef = useRef<HTMLButtonElement | null>(null)
  const activeUserId = draft?.id
  const editedUser = activeUserId ? users.find((user) => user.id === activeUserId) : undefined
  const defaultGroup = groups.find((group) => group.is_default)
  const assignedGroup = editedUser?.group_id == null ? undefined : groups.find((group) => group.id === editedUser.group_id)
  // Group mutations update the shared group list before Users is reloaded. Derive
  // the visible effective group from that current list so default switches and
  // ON DELETE SET NULL semantics are not temporarily represented by stale wire data.
  const visibleEffectiveGroup = assignedGroup || defaultGroup || editedUser?.effective_group
  const visibleGroupID = assignedGroup?.id ?? ""
  const canResetPassword = resetPasswordOpen && resetPasswordDirty && resetPassword.length >= 6
  const panelDirty = editorOwner.isDirty() || passwordOwner.isDirty()

  useEffect(() => onDirtyChange?.(panelDirty), [onDirtyChange, panelDirty])
  useEffect(() => () => onDirtyChange?.(false), [onDirtyChange])

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      editorOwner.invalidate()
      passwordOwner.invalidate()
    }
  }, [editorOwner, passwordOwner])

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

  function userKey(id?: number) {
    return id ? `user:${id}` : "new-user"
  }

  function canLeaveUserEditor(nextKey: string, trigger: HTMLButtonElement | null, proceed: () => void) {
    if (!editorOwner.isDirty() && !passwordOwner.isDirty()) return true
    if (editorOwner.currentEntityKey() === nextKey) return false
    discardTriggerRef.current = trigger
    setPendingDiscard({ proceed })
    return false
  }

  function activateUserEditor(nextDraft: UserDraft) {
    const key = userKey(nextDraft.id)
    invalidateBusy("user-editor")
    invalidateBusy("password-editor")
    editorOwner.activate(key)
    passwordOwner.activate(key)
    setDraft(nextDraft)
    setResetPassword("")
    setResetPasswordOpen(false)
    setResetPasswordDirty(false)
  }

  function changeDraft(update: SetStateAction<UserDraft | null>) {
    editorOwner.change()
    setDraft(update)
  }

  function closeEditor(event?: MouseEvent<HTMLButtonElement>) {
    if (!canLeaveUserEditor("", event?.currentTarget || null, () => closeEditor())) return
    invalidateBusy("user-editor")
    invalidateBusy("password-editor")
    editorOwner.invalidate()
    passwordOwner.invalidate()
    setDraft(null)
    setResetPassword("")
    setResetPasswordOpen(false)
    setResetPasswordDirty(false)
  }

  // 模糊搜索：账号/昵称/邮箱任一命中即可（大小写不敏感）。
  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return users
    return users.filter((u) =>
      [u.username, u.nickname, u.email].some((f) => (f || "").toLowerCase().includes(q))
    )
  }, [users, query])

  const totalPages = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE))
  const safePage = Math.min(page, totalPages)
  const pageUsers = useMemo(
    () => filtered.slice((safePage - 1) * PAGE_SIZE, safePage * PAGE_SIZE),
    [filtered, safePage]
  )

  // group_id 只表示显式绑定；null 会在后端动态解析为当前默认组。
  async function setUserGroup(userId: number, groupId: number | null) {
    const editorOperation = editorOwner.beginOperation()
    const busy = beginBusy(`group-${userId}`, `user-group:${userId}`)
    setError("")
    try {
      const updated = await adminApi.setUserGroup(userId, groupId)
      setUsers((prev) => prev.map((u) => (u.id === userId ? updated : u)))
    } catch (err) {
      if (editorOwner.owns(editorOperation, false)) {
        setError(err instanceof Error ? err.message : "用户分组失败")
      }
    } finally {
      finishBusy(busy)
    }
  }

  function startCreate(event?: MouseEvent<HTMLButtonElement>) {
    if (!canLeaveUserEditor("new-user", event?.currentTarget || null, () => startCreate())) return
    activateUserEditor({ ...emptyUser })
  }

  function startEdit(user: AdminUser, event?: MouseEvent<HTMLButtonElement>) {
    const key = userKey(user.id)
    if (editorOwner.currentEntityKey() === key && draft?.id === user.id) return
    if (!canLeaveUserEditor(key, event?.currentTarget || null, () => startEdit(user))) return
    activateUserEditor({
      id: user.id,
      username: user.username,
      password: "",
      email: user.email || "",
      nickname: user.nickname || "",
      role: user.role,
      is_active: user.is_active,
    })
  }

  async function saveUser() {
    if (!draft) return
    const currentDraft = draft
    const operation = editorOwner.beginOperation()
    const busy = beginBusy(currentDraft.id ? `user-${currentDraft.id}` : "create", "user-editor")
    setError("")
    try {
      if (currentDraft.id) {
        const payload: UpdateAdminUserInput = {
          email: currentDraft.email || "",
          nickname: currentDraft.nickname || "",
          role: currentDraft.role,
          is_active: currentDraft.is_active,
        }
        const updated = await adminApi.updateUser(currentDraft.id, payload)
        // A committed mutation must converge the shared catalog after navigation;
        // only editor-local state is fenced by the captured generation/revision.
        setUsers((prev) => prev.map((user) => (user.id === updated.id ? updated : user)))
        if (editorOwner.owns(operation, false)) {
          editorOwner.acknowledge(operation.revision)
          if (editorOwner.owns(operation) && !passwordOwner.isDirty()) {
            editorOwner.invalidate()
            passwordOwner.invalidate()
            setDraft(null)
          } else {
            setError(passwordOwner.isDirty()
              ? "用户资料已保存，当前密码输入仍未保存"
              : "已保存较早版本，当前修改仍未保存")
          }
        }
      } else {
        const created = await adminApi.createUser(currentDraft)
        setUsers((prev) => [created, ...prev])
        if (editorOwner.owns(operation, false)) {
          const unchanged = editorOwner.owns(operation)
          editorOwner.rekey(userKey(created.id))
          passwordOwner.rekey(userKey(created.id))
          editorOwner.acknowledge(operation.revision)
          if (unchanged) {
            editorOwner.invalidate()
            passwordOwner.invalidate()
            setDraft(null)
          } else {
            setDraft((prev) => prev ? { ...prev, id: created.id, password: "" } : prev)
            setError("已保存较早版本，当前修改仍未保存")
          }
        }
      }
    } catch (err) {
      if (editorOwner.owns(operation, false)) {
        setError(err instanceof Error ? err.message : "用户保存失败")
      }
    } finally {
      finishBusy(busy)
    }
  }

  async function quickPatch(user: AdminUser, patch: UpdateAdminUserInput) {
    const editorOperation = editorOwner.beginOperation()
    const busy = beginBusy(`user-${user.id}`, `user-patch:${user.id}`)
    setError("")
    try {
      const updated = await adminApi.updateUser(user.id, patch)
      setUsers((prev) => prev.map((item) => (item.id === user.id ? updated : item)))
      if (editorOwner.owns(editorOperation)) {
        setDraft((prev) => {
          if (prev?.id !== user.id) return prev
          return {
            ...prev,
            ...(patch.role !== undefined ? { role: updated.role } : {}),
            ...(patch.is_active !== undefined ? { is_active: updated.is_active } : {}),
          }
        })
      }
    } catch (err) {
      if (editorOwner.owns(editorOperation, false)) {
        setError(err instanceof Error ? err.message : "用户更新失败")
      }
    } finally {
      finishBusy(busy)
    }
  }

  async function savePassword() {
    if (!draft?.id || !canResetPassword) return
    const password = resetPassword
    const operation = passwordOwner.beginOperation()
    const busy = beginBusy(`password-${draft.id}`, "password-editor")
    setError("")
    try {
      await adminApi.resetUserPassword(draft.id, password)
      if (passwordOwner.owns(operation, false)) {
        passwordOwner.acknowledge(operation.revision)
        if (passwordOwner.owns(operation)) {
          setResetPassword("")
          setResetPasswordOpen(false)
          setResetPasswordDirty(false)
        } else {
          setError("已提交较早的新密码，当前输入仍未保存")
        }
      }
    } catch (err) {
      if (passwordOwner.owns(operation, false)) {
        setError(err instanceof Error ? err.message : "密码重置失败")
      }
    } finally {
      finishBusy(busy)
    }
  }

  function closePasswordReset(event?: MouseEvent<HTMLButtonElement>) {
    if (passwordOwner.isDirty()) {
      passwordDiscardTriggerRef.current = event?.currentTarget || null
      setPendingPasswordDiscard(true)
      return
    }
    invalidateBusy("password-editor")
    passwordOwner.activate(editorOwner.currentEntityKey())
    setResetPassword("")
    setResetPasswordOpen(false)
    setResetPasswordDirty(false)
  }

  return (
    <>
    <div className="flex h-full min-h-0 overflow-hidden lg:grid lg:grid-cols-[minmax(0,1fr)_380px]">
      <div className={`min-h-0 flex-1 flex-col overflow-hidden border-b border-border/70 lg:flex lg:border-b-0 lg:border-r ${draft ? "hidden lg:flex" : "flex"}`}>
          <div className="flex items-center justify-between border-b border-border/70 px-4 py-3">
            <div className="font-medium">用户</div>
            <Button size="sm" onClick={startCreate}>
              <Plus className="h-3.5 w-3.5" />
              新建用户
            </Button>
          </div>
          <div className="border-b border-border/70 px-4 py-2.5">
            <div className="relative">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input
                type="search"
                name="effchat-user-search"
                autoComplete="off"
                autoCorrect="off"
                spellCheck={false}
                value={query}
                onChange={(e) => { setQuery(e.target.value); setPage(1) }}
                placeholder="搜索账号 / 昵称 / 邮箱"
                aria-label="搜索用户"
                className="h-11 pl-8 text-sm sm:h-8"
              />
            </div>
          </div>
          <div className="hidden grid-cols-[minmax(0,1.45fr)_116px_108px_128px] gap-3 border-b border-border/70 px-4 py-2.5 text-xs font-medium text-muted-foreground/70 md:grid">
            <span>用户</span>
            <span>角色</span>
            <span>状态</span>
            <span>最近登录</span>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto scrollbar-thin">
            {pageUsers.length === 0 ? (
              <div className="flex h-full items-center justify-center py-6 text-sm text-muted-foreground">
                {query ? "没有匹配的用户" : "暂无用户"}
              </div>
            ) : pageUsers.map((user) => (
              <div
                key={user.id}
                className={`grid grid-cols-[minmax(0,1fr)_auto] items-center gap-2 border-b px-3 py-2.5 transition-colors motion-control last:border-b-0 md:grid-cols-[minmax(0,1.45fr)_116px_108px_128px] md:gap-3 md:px-4 ${
                  activeUserId === user.id
                    ? "border-l-2 border-l-primary border-b-border/60 bg-accent/60"
                    : "border-border/60 hover:bg-muted/30"
                }`}
              >
                <button className="min-w-0 text-left" onClick={(event) => startEdit(user, event)}>
                  <div className="flex min-w-0 items-center gap-2">
                    <span className="truncate font-medium">{user.nickname || user.username}</span>
                    {user.role === "admin" && <Shield className="h-3.5 w-3.5 text-amber-500" />}
                  </div>
                  <div className="truncate text-xs text-muted-foreground">{user.username}{user.email ? ` / ${user.email}` : ""}</div>
                </button>
                <select
                  value={user.role}
                  onChange={(e) => quickPatch(user, { role: e.target.value as "admin" | "user" })}
                  className="h-8 w-24 rounded-md border border-input bg-background px-2 text-sm"
                  disabled={saving === `user-${user.id}`}
                >
                  <option value="admin">管理员</option>
                  <option value="user">用户</option>
                </select>
                <StatusButton active={user.is_active} onClick={() => quickPatch(user, { is_active: !user.is_active })} disabled={saving === `user-${user.id}`} />
                <span className="justify-self-end text-xs text-muted-foreground md:justify-self-start md:pl-2">{formatDate(user.last_login_at)}</span>
              </div>
            ))}
          </div>
          <div className="flex items-center justify-between border-t border-border/70 px-4 py-2.5 text-xs text-muted-foreground">
            <span>共 {filtered.length} 人{query ? `（自 ${users.length} 人筛选）` : ""}</span>
            <div className="flex items-center gap-2">
              <Tooltip>
                <TooltipTrigger asChild>
                  <button
                    type="button"
                    onClick={() => setPage((p) => Math.max(1, p - 1))}
                    disabled={safePage <= 1}
                    className="flex h-11 w-11 items-center justify-center rounded-lg border border-border/60 bg-background shadow-sm transition-[background-color,border-color,color] motion-control hover:border-border hover:bg-muted/80 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-40 sm:h-8 sm:w-8"
                    aria-label="上一页用户"
                  >
                    <ChevronLeft className="h-3.5 w-3.5" />
                  </button>
                </TooltipTrigger>
                <TooltipContent>上一页用户</TooltipContent>
              </Tooltip>
              <span className="tabular-nums">{safePage} / {totalPages}</span>
              <Tooltip>
                <TooltipTrigger asChild>
                  <button
                    type="button"
                    onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
                    disabled={safePage >= totalPages}
                    className="flex h-11 w-11 items-center justify-center rounded-lg border border-border/60 bg-background shadow-sm transition-[background-color,border-color,color] motion-control hover:border-border hover:bg-muted/80 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-40 sm:h-8 sm:w-8"
                    aria-label="下一页用户"
                  >
                    <ChevronRight className="h-3.5 w-3.5" />
                  </button>
                </TooltipTrigger>
                <TooltipContent>下一页用户</TooltipContent>
              </Tooltip>
            </div>
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
              <div className="truncate font-medium">{draft ? (draft.id ? "编辑用户" : "新建用户") : "用户详情"}</div>
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
                  <Field label="账号">
                    <Input
                      name={`admin-managed-username-${draft.id || "new"}`}
                      autoComplete="off"
                      value={draft.username}
                      onChange={(e) => changeDraft((prev) => prev ? { ...prev, username: e.target.value } : prev)}
                      disabled={!!draft.id}
                    />
                  </Field>
                  {!draft.id && (
                    <Field label="初始密码">
                      <Input
                        type="password"
                        name="admin-new-user-initial-password"
                        autoComplete="new-password"
                        placeholder="至少 6 位"
                        value={draft.password}
                        onChange={(e) => changeDraft((prev) => prev ? { ...prev, password: e.target.value } : prev)}
                      />
                    </Field>
                  )}
                  <Field label="昵称">
                    <Input
                      name={`admin-managed-nickname-${draft.id || "new"}`}
                      autoComplete="off"
                      value={draft.nickname || ""}
                      onChange={(e) => changeDraft((prev) => prev ? { ...prev, nickname: e.target.value } : prev)}
                    />
                  </Field>
                  <Field label="邮箱">
                    <Input
                      name={`admin-managed-email-${draft.id || "new"}`}
                      autoComplete="off"
                      value={draft.email || ""}
                      onChange={(e) => changeDraft((prev) => prev ? { ...prev, email: e.target.value } : prev)}
                    />
                  </Field>
                  <div className="grid grid-cols-2 gap-3">
                    <Field label="角色">
                      <select value={draft.role} onChange={(e) => changeDraft((prev) => prev ? { ...prev, role: e.target.value as "admin" | "user" } : prev)} className="h-8 w-full rounded-md border border-input bg-background px-3 text-sm">
                        <option value="admin">管理员</option>
                        <option value="user">用户</option>
                      </select>
                    </Field>
                    <Field label="状态">
                      <StatusButton active={draft.is_active} onClick={() => changeDraft((prev) => prev ? { ...prev, is_active: !prev.is_active } : prev)} className="h-8 w-full justify-center rounded-md text-sm" />
                    </Field>
                  </div>
                  {draft.id && (
                    <Field label="分级组">
                      <select
                        value={visibleGroupID}
                        onChange={(e) => draft.id && setUserGroup(draft.id, e.target.value === "" ? null : Number(e.target.value))}
                        className="h-8 w-full rounded-md border border-input bg-background px-3 text-sm"
                        disabled={saving === `group-${draft.id}`}
                      >
                        <option value="">
                          {defaultGroup
                            ? `继承默认组 ${defaultGroup.name}（等级 ${defaultGroup.level}）`
                            : "继承默认组"}
                        </option>
                        {groups.map((g) => (
                          <option key={g.id} value={g.id}>{g.name}（等级 {g.level}）</option>
                        ))}
                      </select>
                      {visibleEffectiveGroup && (
                        <span className="mt-1.5 block text-xs text-muted-foreground">
                          当前生效：{assignedGroup ? "显式组 " : "继承默认组 "}
                          {visibleEffectiveGroup.name}（等级 {visibleEffectiveGroup.level}）
                        </span>
                      )}
                    </Field>
                  )}
                  {draft.id && (
                    <div className="space-y-3 border-t border-border/70 pt-4">
                      <div className="rounded-md border border-sky-200/70 bg-sky-50/70 px-3 py-2 text-xs leading-5 text-sky-900 dark:border-sky-500/25 dark:bg-sky-500/10 dark:text-sky-100">
                        普通保存不会修改密码。需要改密码时，单独打开这里设置新密码。
                      </div>
                      {resetPasswordOpen ? (
                        <>
                          <Field label="新密码">
                            <Input
                              type="password"
                              name={`admin-reset-password-${draft.id}`}
                              autoComplete="new-password"
                              placeholder="至少 6 位"
                              value={resetPassword}
                              onChange={(e) => {
                                passwordOwner.change()
                                setResetPasswordDirty(true)
                                setResetPassword(e.target.value)
                              }}
                            />
                          </Field>
                          <div className="flex flex-wrap items-center gap-2">
                            <Button type="button" size="sm" variant="outline" onClick={savePassword} disabled={!canResetPassword || saving === `password-${draft.id}`}>
                              确认重置
                            </Button>
                            <Button type="button" size="sm" variant="ghost" onClick={closePasswordReset}>
                              取消
                            </Button>
                            {resetPasswordDirty && resetPassword.length > 0 && resetPassword.length < 6 && (
                              <span className="text-xs text-muted-foreground">至少 6 位</span>
                            )}
                          </div>
                        </>
                      ) : (
                        <Button
                          type="button"
                          size="sm"
                          variant="outline"
                          onClick={() => {
                            setResetPassword("")
                            setResetPasswordDirty(false)
                            setResetPasswordOpen(true)
                          }}
                        >
                          设置新密码
                        </Button>
                      )}
                    </div>
                  )}
                </div>
              </div>
              <div className="flex items-center justify-end border-t border-border/70 px-4 py-3">
                <Button type="button" size="sm" onClick={saveUser} disabled={saving !== ""}>
                  保存
                </Button>
              </div>
              </>
            ) : (
              <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">左侧选择用户</div>
            )}
          </MotionView>
      </div>
    </div>
    <Dialog open={!!pendingDiscard} onOpenChange={(nextOpen) => !nextOpen && setPendingDiscard(null)}>
      <DialogContent
        className="max-w-[calc(100vw-1.5rem)] sm:max-w-md"
        onCloseAutoFocus={(event) => {
          const trigger = discardTriggerRef.current
          if (!trigger?.isConnected) return
          event.preventDefault()
          trigger.focus()
        }}
      >
        <DialogHeader>
          <DialogTitle>放弃未保存修改？</DialogTitle>
          <DialogDescription>当前用户资料或密码修改还没有保存，继续操作会丢失这些内容。</DialogDescription>
        </DialogHeader>
        <div className="flex justify-end gap-2">
          <Button type="button" variant="secondary" onClick={() => setPendingDiscard(null)}>继续编辑</Button>
          <Button type="button" variant="destructive" onClick={() => {
            const pending = pendingDiscard
            setPendingDiscard(null)
            editorOwner.invalidate()
            passwordOwner.invalidate()
            pending?.proceed()
          }}>放弃修改</Button>
        </div>
      </DialogContent>
    </Dialog>
    <Dialog open={pendingPasswordDiscard} onOpenChange={(nextOpen) => !nextOpen && setPendingPasswordDiscard(false)}>
      <DialogContent
        className="max-w-[calc(100vw-1.5rem)] sm:max-w-md"
        onCloseAutoFocus={(event) => {
          const trigger = passwordDiscardTriggerRef.current
          if (!trigger?.isConnected) return
          event.preventDefault()
          trigger.focus()
        }}
      >
        <DialogHeader>
          <DialogTitle>放弃未保存的新密码？</DialogTitle>
          <DialogDescription>当前输入的新密码还没有提交，取消会清除这次输入。</DialogDescription>
        </DialogHeader>
        <div className="flex justify-end gap-2">
          <Button type="button" variant="secondary" onClick={() => setPendingPasswordDiscard(false)}>继续编辑</Button>
          <Button type="button" variant="destructive" onClick={() => {
            setPendingPasswordDiscard(false)
            passwordOwner.invalidate()
            invalidateBusy("password-editor")
            setResetPassword("")
            setResetPasswordOpen(false)
            setResetPasswordDirty(false)
          }}>放弃密码</Button>
        </div>
      </DialogContent>
    </Dialog>
    </>
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

function formatDate(value?: string) {
  if (!value) return "-"
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return "-"
  return date.toLocaleDateString("zh-CN", { month: "2-digit", day: "2-digit" })
}

function StatusButton({
  active,
  onClick,
  disabled,
  className = "",
}: {
  active: boolean
  onClick: () => void
  disabled?: boolean
  className?: string
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      className={`inline-flex h-7 items-center rounded-full border px-3 text-xs transition-colors motion-control ${
        active
          ? "border-emerald-600 bg-emerald-50 text-emerald-700 dark:border-emerald-500/30 dark:bg-emerald-500/10 dark:text-emerald-400"
          : "border-rose-300 bg-rose-50 text-rose-700 dark:border-rose-500/30 dark:bg-rose-500/10 dark:text-rose-400"
      } ${className}`}
    >
      {active ? "已启用" : "已停用"}
    </button>
  )
}
