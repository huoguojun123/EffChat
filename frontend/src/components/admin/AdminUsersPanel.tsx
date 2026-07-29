import { useMemo, useState } from "react"
import { adminApi, type AdminUser, type CreateAdminUserInput, type UpdateAdminUserInput } from "@/api/admin"
import type { UserGroup } from "@/types"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { MotionView } from "@/components/ui/motion"
import { ChevronLeft, ChevronRight, Plus, Search, Shield, X } from "lucide-react"

const PAGE_SIZE = 15

interface Props {
  users: AdminUser[]
  setUsers: React.Dispatch<React.SetStateAction<AdminUser[]>>
  groups: UserGroup[]
  setError: (error: string) => void
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

export function AdminUsersPanel({ users, setUsers, groups, setError }: Props) {
  const [draft, setDraft] = useState<UserDraft | null>(null)
  const [saving, setSaving] = useState("")
  const [resetPassword, setResetPassword] = useState("")
  const [resetPasswordOpen, setResetPasswordOpen] = useState(false)
  const [resetPasswordDirty, setResetPasswordDirty] = useState(false)
  const [query, setQuery] = useState("")
  const [page, setPage] = useState(1)
  const activeUserId = draft?.id
  const canResetPassword = resetPasswordOpen && resetPasswordDirty && resetPassword.length >= 6

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

  // 设置用户分级组（group_id 为 null 时清空）。
  async function setUserGroup(userId: number, groupId: number | null) {
    setSaving(`group-${userId}`)
    setError("")
    try {
      const updated = await adminApi.setUserGroup(userId, groupId)
      setUsers((prev) => prev.map((u) => (u.id === userId ? updated : u)))
    } catch (err) {
      setError(err instanceof Error ? err.message : "用户分组失败")
    } finally {
      setSaving("")
    }
  }

  function startCreate() {
    setDraft(emptyUser)
    setResetPassword("")
    setResetPasswordOpen(false)
    setResetPasswordDirty(false)
  }

  function startEdit(user: AdminUser) {
    setDraft({
      id: user.id,
      username: user.username,
      password: "",
      email: user.email || "",
      nickname: user.nickname || "",
      role: user.role,
      is_active: user.is_active,
    })
    setResetPassword("")
    setResetPasswordOpen(false)
    setResetPasswordDirty(false)
  }

  async function saveUser() {
    if (!draft) return
    setSaving(draft.id ? `user-${draft.id}` : "create")
    setError("")
    try {
      if (draft.id) {
        const payload: UpdateAdminUserInput = {
          email: draft.email || "",
          nickname: draft.nickname || "",
          role: draft.role,
          is_active: draft.is_active,
        }
        const updated = await adminApi.updateUser(draft.id, payload)
        setUsers((prev) => prev.map((user) => (user.id === updated.id ? updated : user)))
      } else {
        const created = await adminApi.createUser(draft)
        setUsers((prev) => [created, ...prev])
      }
      setDraft(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : "用户保存失败")
    } finally {
      setSaving("")
    }
  }

  async function quickPatch(user: AdminUser, patch: UpdateAdminUserInput) {
    setSaving(`user-${user.id}`)
    setError("")
    try {
      const updated = await adminApi.updateUser(user.id, patch)
      setUsers((prev) => prev.map((item) => (item.id === user.id ? updated : item)))
      setDraft((prev) => {
        if (prev?.id !== user.id) return prev
        return {
          ...prev,
          ...(patch.role !== undefined ? { role: updated.role } : {}),
          ...(patch.is_active !== undefined ? { is_active: updated.is_active } : {}),
        }
      })
    } catch (err) {
      setError(err instanceof Error ? err.message : "用户更新失败")
    } finally {
      setSaving("")
    }
  }

  async function savePassword() {
    if (!draft?.id || !canResetPassword) return
    setSaving(`password-${draft.id}`)
    setError("")
    try {
      await adminApi.resetUserPassword(draft.id, resetPassword)
      setResetPassword("")
      setResetPasswordOpen(false)
      setResetPasswordDirty(false)
    } catch (err) {
      setError(err instanceof Error ? err.message : "密码重置失败")
    } finally {
      setSaving("")
    }
  }

  function closePasswordReset() {
    setResetPassword("")
    setResetPasswordOpen(false)
    setResetPasswordDirty(false)
  }

  return (
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
                className="h-8 pl-8 text-sm"
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
                <button className="min-w-0 text-left" onClick={() => startEdit(user)}>
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
              <button
                onClick={() => setPage((p) => Math.max(1, p - 1))}
                disabled={safePage <= 1}
                className="flex h-7 w-7 items-center justify-center rounded-lg border border-border/60 bg-background shadow-sm transition-[background-color,border-color,color] motion-control hover:border-border hover:bg-muted/80 disabled:cursor-not-allowed disabled:opacity-40"
              >
                <ChevronLeft className="h-3.5 w-3.5" />
              </button>
              <span className="tabular-nums">{safePage} / {totalPages}</span>
              <button
                onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
                disabled={safePage >= totalPages}
                className="flex h-7 w-7 items-center justify-center rounded-lg border border-border/60 bg-background shadow-sm transition-[background-color,border-color,color] motion-control hover:border-border hover:bg-muted/80 disabled:cursor-not-allowed disabled:opacity-40"
              >
                <ChevronRight className="h-3.5 w-3.5" />
              </button>
            </div>
          </div>
      </div>

      <div className={`min-h-0 flex-1 flex-col overflow-hidden lg:flex ${draft ? "flex" : "hidden lg:flex"}`}>
          <div className="flex items-center justify-between border-b border-border/70 px-4 py-3">
            <div className="flex min-w-0 items-center gap-1">
              {draft ? (
                <Button variant="ghost" size="sm" className="h-8 px-1.5 lg:hidden" onClick={() => setDraft(null)}>
                  <ChevronLeft className="h-3.5 w-3.5" />
                  返回
                </Button>
              ) : null}
              <div className="truncate font-medium">{draft ? (draft.id ? "编辑用户" : "新建用户") : "用户详情"}</div>
            </div>
            {draft && (
              <Button variant="ghost" size="sm" className="hidden lg:inline-flex" onClick={() => setDraft(null)}>
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
                      onChange={(e) => setDraft((prev) => prev ? { ...prev, username: e.target.value } : prev)}
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
                        onChange={(e) => setDraft((prev) => prev ? { ...prev, password: e.target.value } : prev)}
                      />
                    </Field>
                  )}
                  <Field label="昵称">
                    <Input
                      name={`admin-managed-nickname-${draft.id || "new"}`}
                      autoComplete="off"
                      value={draft.nickname || ""}
                      onChange={(e) => setDraft((prev) => prev ? { ...prev, nickname: e.target.value } : prev)}
                    />
                  </Field>
                  <Field label="邮箱">
                    <Input
                      name={`admin-managed-email-${draft.id || "new"}`}
                      autoComplete="off"
                      value={draft.email || ""}
                      onChange={(e) => setDraft((prev) => prev ? { ...prev, email: e.target.value } : prev)}
                    />
                  </Field>
                  <div className="grid grid-cols-2 gap-3">
                    <Field label="角色">
                      <select value={draft.role} onChange={(e) => setDraft((prev) => prev ? { ...prev, role: e.target.value as "admin" | "user" } : prev)} className="h-8 w-full rounded-md border border-input bg-background px-3 text-sm">
                        <option value="admin">管理员</option>
                        <option value="user">用户</option>
                      </select>
                    </Field>
                    <Field label="状态">
                      <StatusButton active={draft.is_active} onClick={() => setDraft((prev) => prev ? { ...prev, is_active: !prev.is_active } : prev)} className="h-8 w-full justify-center rounded-md text-sm" />
                    </Field>
                  </div>
                  {draft.id && (
                    <Field label="分级组">
                      <select
                        value={users.find((u) => u.id === draft.id)?.group_id ?? ""}
                        onChange={(e) => draft.id && setUserGroup(draft.id, e.target.value === "" ? null : Number(e.target.value))}
                        className="h-8 w-full rounded-md border border-input bg-background px-3 text-sm"
                        disabled={saving === `group-${draft.id}`}
                      >
                        <option value="">默认（最低级）</option>
                        {groups.map((g) => (
                          <option key={g.id} value={g.id}>{g.name}（等级 {g.level}）</option>
                        ))}
                      </select>
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
