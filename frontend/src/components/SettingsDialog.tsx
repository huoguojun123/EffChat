import { useCallback, useEffect, useRef, useState } from "react"
import { useAuthStore } from "@/stores/auth"
import { usersApi } from "@/api/users"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { MotionView } from "@/components/ui/motion"
import { useUIStore } from "@/stores/ui"
import { ACCENTS, COLOR_THEMES, THEME_PREVIEW_COLORS, accentPreviewColor, type ColorThemeId } from "@/lib/themes"
import { cn } from "@/lib/utils"
import { UserAvatar } from "@/components/UserAvatar"
import { EditorOwnership } from "@/components/admin/editorOwnership"
import { User as UserIcon, Lock, Check, AlertCircle, Camera, FileText, LoaderCircle, Monitor, Moon, Palette, Sun, Trash2 } from "lucide-react"

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  onOpenPromptManager: () => void
}

function useDelayedAction(action: () => void, delay: number) {
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const actionRef = useRef(action)

  useEffect(() => {
    actionRef.current = action
  }, [action])

  useEffect(() => () => {
    if (timerRef.current) clearTimeout(timerRef.current)
  }, [])

  return (shouldRun: () => boolean = () => true) => {
    if (timerRef.current) clearTimeout(timerRef.current)
    timerRef.current = setTimeout(() => {
      timerRef.current = null
      if (shouldRun()) actionRef.current()
    }, delay)
  }
}

export function SettingsDialog({ open, onOpenChange, onOpenPromptManager }: Props) {
  const [tab, setTab] = useState<"profile" | "password" | "appearance">("profile")
  const [direction, setDirection] = useState<"forward" | "back">("forward")
  const [dirty, setDirty] = useState(false)

  const leaveCurrentForm = useCallback((leave: () => void) => {
    if (dirty && !window.confirm("放弃当前设置中未保存的修改？")) return
    setDirty(false)
    leave()
  }, [dirty])

  const close = useCallback(() => {
    leaveCurrentForm(() => onOpenChange(false))
  }, [leaveCurrentForm, onOpenChange])

  function switchTab(next: "profile" | "password" | "appearance") {
    if (next === tab) return
    leaveCurrentForm(() => {
      const order = ["profile", "password", "appearance"]
      setDirection(order.indexOf(next) > order.indexOf(tab) ? "forward" : "back")
      setTab(next)
    })
  }

  return (
    <Dialog open={open} onOpenChange={(next) => next ? onOpenChange(true) : close()}>
      <DialogContent className="max-h-[min(90dvh,680px)] max-w-[var(--settings-dialog-width)] overflow-hidden p-0">
        <DialogHeader>
          <DialogTitle className="border-b border-border px-5 py-4">设置</DialogTitle>
          <DialogDescription className="sr-only">配置个人资料、密码和界面外观。</DialogDescription>
        </DialogHeader>

        <div className="flex items-center gap-2 px-5">
          <div className="flex flex-1 gap-1 rounded-lg bg-muted/60 p-1">
            <TabBtn active={tab === "profile"} onClick={() => switchTab("profile")} icon={<UserIcon className="h-3.5 w-3.5" />}>
              资料
            </TabBtn>
            <TabBtn active={tab === "password"} onClick={() => switchTab("password")} icon={<Lock className="h-3.5 w-3.5" />}>
              密码
            </TabBtn>
            <TabBtn active={tab === "appearance"} onClick={() => switchTab("appearance")} icon={<Palette className="h-3.5 w-3.5" />}>
              外观
            </TabBtn>
          </div>
          <Button
            variant="outline"
            size="sm"
            className="shrink-0 rounded-lg text-sm"
            onClick={() => {
              leaveCurrentForm(() => {
                onOpenChange(false)
                onOpenPromptManager()
              })
            }}
          >
            <FileText className="h-3.5 w-3.5" />
            提示词
          </Button>
        </div>

        <div className="min-h-0 overflow-y-auto px-5 pb-5">
          <MotionView viewKey={tab} direction={direction}>
            {tab === "profile" ? <ProfileForm onDone={close} onDirtyChange={setDirty} /> : null}
            {tab === "password" ? <PasswordForm onDone={close} onDirtyChange={setDirty} /> : null}
            {tab === "appearance" ? <AppearanceSettings /> : null}
          </MotionView>
        </div>
      </DialogContent>
    </Dialog>
  )
}

function AppearanceSettings() {
  const mode = useUIStore((s) => s.theme)
  const lightTheme = useUIStore((s) => s.lightColorTheme)
  const darkTheme = useUIStore((s) => s.darkColorTheme)
  const accent = useUIStore((s) => s.accentColor)
  const setMode = useUIStore((s) => s.setTheme)
  const setLightTheme = useUIStore((s) => s.setLightColorTheme)
  const setDarkTheme = useUIStore((s) => s.setDarkColorTheme)
  const setAccent = useUIStore((s) => s.setAccentColor)

  return (
    <div className="divide-y divide-border">
      <AppearanceRow label="明暗模式">
        <div className="grid grid-cols-3 rounded-lg bg-muted p-1">
          <ModeButton active={mode === "light"} icon={<Sun />} label="亮色" onClick={() => setMode("light")} />
          <ModeButton active={mode === "dark"} icon={<Moon />} label="暗色" onClick={() => setMode("dark")} />
          <ModeButton active={mode === "system"} icon={<Monitor />} label="系统" onClick={() => setMode("system")} />
        </div>
      </AppearanceRow>
      <AppearanceRow label="浅色主题">
        <ThemeSelect value={lightTheme} mode="light" onChange={setLightTheme} />
      </AppearanceRow>
      <AppearanceRow label="深色主题">
        <ThemeSelect value={darkTheme} mode="dark" onChange={setDarkTheme} />
      </AppearanceRow>
      <AppearanceRow label="强调色">
        <div className="flex flex-wrap justify-end gap-2">
          {ACCENTS.map((item) => (
            <button
              key={item.id}
              type="button"
              className={cn(
                "relative h-7 w-7 rounded-full ring-offset-2 ring-offset-background transition-transform motion-control hover:scale-105 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                accent === item.id && "ring-2 ring-foreground/70"
              )}
              style={{ background: accentPreviewColor(item.id) }}
              title={item.label}
              aria-label={`强调色：${item.label}`}
              aria-pressed={accent === item.id}
              onClick={() => setAccent(item.id)}
            >
              {accent === item.id ? <Check className="absolute inset-0 m-auto h-3.5 w-3.5 text-white drop-shadow" /> : null}
            </button>
          ))}
        </div>
      </AppearanceRow>
    </div>
  )
}

function AppearanceRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid min-h-16 grid-cols-[7rem_minmax(0,1fr)] items-center gap-4 py-3 sm:grid-cols-[8rem_minmax(0,1fr)]">
      <div className="text-sm font-medium">{label}</div>
      <div className="min-w-0 justify-self-stretch">{children}</div>
    </div>
  )
}

function ModeButton({ active, icon, label, onClick }: { active: boolean; icon: React.ReactElement; label: string; onClick: () => void }) {
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onClick}
      className={cn(
        "flex h-8 items-center justify-center gap-1.5 rounded-lg text-sm font-medium text-muted-foreground transition-[background-color,color,box-shadow] motion-control focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring hover:bg-background/50",
        active && "bg-background text-foreground shadow-[0_1px_3px_rgba(0,0,0,0.05)] hover:bg-background"
      )}
    >
      <span className="[&>svg]:h-3.5 [&>svg]:w-3.5">{icon}</span>
      {label}
    </button>
  )
}

function ThemeSelect({ value, mode, onChange }: { value: ColorThemeId; mode: "light" | "dark"; onChange: (value: ColorThemeId) => void }) {
  return (
    <label className="relative flex h-9 items-center rounded-lg border border-input bg-popover pl-2 pr-1 shadow-[0_1px_2px_rgba(0,0,0,0.03)] transition-[border-color,box-shadow,background-color] motion-control hover:border-input/80 focus-within:border-input/80 focus-within:ring-2 focus-within:ring-ring/60">
      <span
        data-color-theme={value}
        className={cn("theme-preview mr-2 flex overflow-hidden rounded-sm border border-black/10", mode === "dark" && "dark border-white/10")}
        aria-hidden="true"
      >
        {THEME_PREVIEW_COLORS.map((color) => <span key={color} className="h-4 w-3" style={{ backgroundColor: color }} />)}
      </span>
      <select
        value={value}
        onChange={(event) => onChange(event.target.value as ColorThemeId)}
        className="h-full min-w-0 flex-1 appearance-none bg-transparent pr-7 text-sm outline-none"
        aria-label={mode === "light" ? "浅色主题" : "深色主题"}
      >
        {COLOR_THEMES.map((theme) => <option key={theme.id} value={theme.id}>{theme.label}</option>)}
      </select>
      <span className="pointer-events-none absolute right-3 text-xs text-muted-foreground">⌄</span>
    </label>
  )
}

function TabBtn({
  active,
  onClick,
  icon,
  children,
}: {
  active: boolean
  onClick: () => void
  icon: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <button
      onClick={onClick}
      className={`flex flex-1 items-center justify-center gap-1.5 rounded-lg px-2 py-1.5 text-sm font-medium transition-[background-color,color,box-shadow] motion-control hover:bg-background/50 ${
        active ? "bg-background text-foreground shadow-[0_1px_3px_rgba(0,0,0,0.05)] hover:bg-background" : "text-muted-foreground hover:text-foreground"
      }`}
    >
      {icon}
      {children}
    </button>
  )
}

function ProfileForm({ onDone, onDirtyChange }: { onDone: () => void; onDirtyChange: (dirty: boolean) => void }) {
  const user = useAuthStore((s) => s.user)
  const setUser = useAuthStore((s) => s.setUser)
  const [owner] = useState(() => {
    const value = new EditorOwnership()
    value.activate("settings-profile")
    return value
  })
  const avatarInputRef = useRef<HTMLInputElement>(null)
  const [nickname, setNickname] = useState(user?.nickname || "")
  const [email, setEmail] = useState(user?.email || "")
  const [saving, setSaving] = useState(false)
  const [avatarBusy, setAvatarBusy] = useState(false)
  const [avatarPreview, setAvatarPreview] = useState<string | null>(null)
  const [feedback, setFeedback] = useState<{ type: "success" | "error"; msg: string } | null>(null)
  const closeAfterSuccess = useDelayedAction(onDone, 800)

  useEffect(() => () => owner.invalidate(), [owner])

  useEffect(() => () => {
    if (avatarPreview) URL.revokeObjectURL(avatarPreview)
  }, [avatarPreview])

  if (!user) return null

  async function handleAvatar(file?: File) {
    if (!file) return
    if (!["image/jpeg", "image/png", "image/gif"].includes(file.type)) {
      setFeedback({ type: "error", msg: "请选择 JPEG、PNG 或 GIF 图片" })
      return
    }
    if (file.size > 10 * 1024 * 1024) {
      setFeedback({ type: "error", msg: "头像文件不能超过 10 MiB" })
      return
    }
    const preview = URL.createObjectURL(file)
    setAvatarPreview(preview)
    setAvatarBusy(true)
    setFeedback(null)
    try {
      const updated = await usersApi.uploadAvatar(file)
      setUser(updated)
      setFeedback({ type: "success", msg: "头像已更新" })
    } catch (e) {
      setFeedback({ type: "error", msg: e instanceof Error ? e.message : "头像上传失败" })
    } finally {
      setAvatarPreview(null)
      setAvatarBusy(false)
      if (avatarInputRef.current) avatarInputRef.current.value = ""
    }
  }

  async function handleAvatarDelete() {
    setAvatarBusy(true)
    setFeedback(null)
    try {
      const updated = await usersApi.deleteAvatar()
      setUser(updated)
      setFeedback({ type: "success", msg: "头像已移除" })
    } catch (e) {
      setFeedback({ type: "error", msg: e instanceof Error ? e.message : "移除头像失败" })
    } finally {
      setAvatarBusy(false)
    }
  }

  async function handleSave() {
    const operation = owner.beginOperation()
    const payload = {
      nickname: nickname.trim(),
      email: email.trim(),
    }
    setSaving(true)
    setFeedback(null)
    try {
      const updated = await usersApi.updateMe(payload)
      // A committed profile mutation updates shared auth state even if the
      // initiating dialog has since closed; draft UI remains generation-fenced.
      setUser(updated)
      if (owner.owns(operation, false)) {
        owner.acknowledge(operation.revision)
        onDirtyChange(owner.isDirty())
        if (owner.owns(operation)) {
          setNickname(updated.nickname || "")
          setEmail(updated.email || "")
          setFeedback({ type: "success", msg: "已保存" })
          closeAfterSuccess(() => owner.owns(operation))
        } else {
          setFeedback({ type: "success", msg: "已保存较早版本，当前修改仍未保存" })
        }
      }
    } catch (e) {
      if (owner.owns(operation, false)) {
        setFeedback({ type: "error", msg: e instanceof Error ? e.message : "保存失败" })
      }
    } finally {
      if (owner.owns(operation, false)) setSaving(false)
    }
  }

  function changeDraft(update: () => void) {
    owner.change()
    update()
    onDirtyChange(true)
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-3 pb-1">
        <button
          type="button"
          className="group relative rounded-full outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
          onClick={() => avatarInputRef.current?.click()}
          disabled={avatarBusy}
          aria-label="更换头像"
        >
          <UserAvatar
            src={avatarPreview || user.avatar_url}
            name={user.nickname || user.username}
            className="h-18 w-18 rounded-full text-xl ring-1 ring-border"
          />
          <span className="absolute bottom-0 right-0 flex h-6 w-6 items-center justify-center rounded-full border border-bg bg-foreground text-bg shadow-sm transition-transform motion-control group-hover:scale-105">
            {avatarBusy ? <LoaderCircle className="h-3.5 w-3.5 animate-spin" /> : <Camera className="h-3.5 w-3.5" />}
          </span>
        </button>
        <input
          ref={avatarInputRef}
          type="file"
          accept="image/jpeg,image/png,image/gif"
          className="sr-only"
          onChange={(event) => void handleAvatar(event.target.files?.[0])}
        />
        <div className="min-w-0">
          <p className="truncate text-sm font-medium">{user.nickname || user.username}</p>
          <button
            type="button"
            className="mt-1 inline-flex items-center gap-1 text-xs text-muted-foreground transition-colors hover:text-destructive-foreground disabled:opacity-50"
            onClick={() => void handleAvatarDelete()}
            disabled={avatarBusy || !user.avatar_url}
          >
            <Trash2 className="h-3 w-3" />
            移除头像
          </button>
        </div>
      </div>
      <Field label="账号">
        <div className="form-readonly" aria-label="账号">{user.username}</div>
      </Field>
      <Field label="邮箱">
        <input
          type="email"
          name="effchat-profile-email"
          autoComplete="off"
          value={email}
          onChange={(e) => changeDraft(() => setEmail(e.target.value))}
          placeholder="用于联系和找回账号"
          className="form-input"
        />
      </Field>
      <Field label="昵称">
        <input
          name="effchat-profile-nickname"
          autoComplete="off"
          value={nickname}
          onChange={(e) => changeDraft(() => setNickname(e.target.value))}
          placeholder="显示名称"
          className="form-input"
        />
      </Field>

      {feedback && <FeedbackLine {...feedback} />}

      <div className="flex justify-end gap-2 pt-1">
        <Button variant="ghost" size="sm" onClick={onDone}>取消</Button>
        <Button size="sm" onClick={handleSave} disabled={saving}>
          {saving ? "保存中" : "保存"}
        </Button>
      </div>

      <style>{`.form-input,.form-readonly{width:100%;height:34px;padding:0 12px;border-radius:8px;background:var(--muted);border:1px solid transparent;outline:none;font-size:var(--text-sm);transition:border-color var(--motion-duration-control) var(--motion-ease-standard),background-color var(--motion-duration-control) var(--motion-ease-standard)}.form-input:focus{border-color:var(--ring);background:var(--bg)}.form-readonly{display:flex;align-items:center;color:var(--muted-fg)}`}</style>
    </div>
  )
}

function PasswordForm({ onDone, onDirtyChange }: { onDone: () => void; onDirtyChange: (dirty: boolean) => void }) {
  const username = useAuthStore((s) => s.user?.username || "")
  const [owner] = useState(() => {
    const value = new EditorOwnership()
    value.activate("settings-password")
    return value
  })
  const [oldPwd, setOldPwd] = useState("")
  const [newPwd, setNewPwd] = useState("")
  const [confirm, setConfirm] = useState("")
  const [saving, setSaving] = useState(false)
  const [feedback, setFeedback] = useState<{ type: "success" | "error"; msg: string } | null>(null)
  const closeAfterSuccess = useDelayedAction(onDone, 800)

  useEffect(() => () => owner.invalidate(), [owner])

  async function handleSave() {
    if (newPwd !== confirm) {
      setFeedback({ type: "error", msg: "两次密码不一致" })
      return
    }
    if (newPwd.length < 6) {
      setFeedback({ type: "error", msg: "密码至少 6 位" })
      return
    }
    const operation = owner.beginOperation()
    const payload = { old_password: oldPwd, new_password: newPwd }
    setSaving(true)
    setFeedback(null)
    try {
      await usersApi.changePassword(payload)
      if (owner.owns(operation, false)) {
        owner.acknowledge(operation.revision)
        onDirtyChange(owner.isDirty())
        if (owner.owns(operation)) {
          setOldPwd("")
          setNewPwd("")
          setConfirm("")
          setFeedback({ type: "success", msg: "密码已更新" })
          closeAfterSuccess(() => owner.owns(operation))
        } else {
          setFeedback({ type: "success", msg: "已提交较早的新密码，当前输入仍未保存" })
        }
      }
    } catch (e) {
      if (owner.owns(operation, false)) {
        setFeedback({ type: "error", msg: e instanceof Error ? e.message : "更新失败" })
      }
    } finally {
      if (owner.owns(operation, false)) setSaving(false)
    }
  }

  function changeDraft(update: () => void) {
    owner.change()
    update()
    onDirtyChange(true)
  }

  return (
    <form
      className="space-y-3"
      autoComplete="on"
      onSubmit={(event) => {
        event.preventDefault()
        void handleSave()
      }}
    >
      <input className="sr-only" type="text" name="username" autoComplete="username" value={username} readOnly tabIndex={-1} />
      <Field label="当前密码">
        <input type="password" name="effchat-current-password" autoComplete="current-password" value={oldPwd} onChange={(e) => changeDraft(() => setOldPwd(e.target.value))} className="form-input" />
      </Field>
      <Field label="新密码">
        <input type="password" name="effchat-new-password" autoComplete="new-password" value={newPwd} onChange={(e) => changeDraft(() => setNewPwd(e.target.value))} className="form-input" />
      </Field>
      <Field label="确认密码">
        <input type="password" name="effchat-confirm-password" autoComplete="new-password" value={confirm} onChange={(e) => changeDraft(() => setConfirm(e.target.value))} className="form-input" />
      </Field>

      {feedback && <FeedbackLine {...feedback} />}

      <div className="flex justify-end gap-2 pt-1">
        <Button type="button" variant="ghost" size="sm" onClick={onDone}>取消</Button>
        <Button type="submit" size="sm" disabled={saving || !oldPwd || !newPwd}>
          {saving ? "更新中" : "更新密码"}
        </Button>
      </div>

      <style>{`.form-input{width:100%;height:34px;padding:0 12px;border-radius:8px;background:var(--muted);border:1px solid transparent;outline:none;font-size:var(--text-sm);transition:border-color var(--motion-duration-control) var(--motion-ease-standard),background-color var(--motion-duration-control) var(--motion-ease-standard)}.form-input:focus{border-color:var(--ring);background:var(--bg)}`}</style>
    </form>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="block">
      <span className="mb-1.5 block text-xs font-medium text-muted-foreground">{label}</span>
      {children}
    </label>
  )
}

function FeedbackLine({ type, msg }: { type: "success" | "error"; msg: string }) {
  return (
    <div
      className={`flex items-center gap-1.5 text-xs ${
        type === "success" ? "text-emerald-600 dark:text-emerald-400" : "text-red-600 dark:text-red-400"
      }`}
    >
      {type === "success" ? <Check className="h-3.5 w-3.5" /> : <AlertCircle className="h-3.5 w-3.5" />}
      {msg}
    </div>
  )
}
