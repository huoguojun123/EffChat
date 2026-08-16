import { lazy, useState } from "react"
import { useLocation, useNavigate } from "react-router"
import { useAuthStore } from "@/stores/auth"
import { useUIStore, CHAT_FONT_STEPS } from "@/stores/ui"
import { useSystemStore } from "@/stores/system"
import { navigateWithFade } from "@/lib/navigation"
import { Popover, PopoverTrigger, PopoverContent } from "@/components/ui/popover"
import { ChevronUp, LogOut, Settings, Moon, Sun, Monitor, Shield, Type } from "lucide-react"
import { LazyBoundary } from "@/components/LazyBoundary"
import { UserAvatar } from "@/components/UserAvatar"

const SettingsDialog = lazy(() => import("@/components/SettingsDialog").then((module) => ({ default: module.SettingsDialog })))
const UserPromptDialog = lazy(() => import("@/components/prompts/UserPromptDialog").then((module) => ({ default: module.UserPromptDialog })))

export function UserMenu() {
  const user = useAuthStore((s) => s.user)
  const logout = useAuthStore((s) => s.logout)
  const theme = useUIStore((s) => s.theme)
  const setTheme = useUIStore((s) => s.setTheme)
  const chatFontScale = useUIStore((s) => s.chatFontScale)
  const setChatFontScale = useUIStore((s) => s.setChatFontScale)
  const systemVersion = useSystemStore((s) => s.systemVersion)
  const navigate = useNavigate()
  const location = useLocation()
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [promptDialogOpen, setPromptDialogOpen] = useState(false)
  const [popoverOpen, setPopoverOpen] = useState(false)

  if (!user) return null

  return (
    <>
      <Popover open={popoverOpen} onOpenChange={setPopoverOpen}>
        <PopoverTrigger asChild>
          <button className="flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left transition-[background-color,box-shadow] motion-control hover:bg-sidebar-accent/80 hover:shadow-[0_1px_2px_rgba(0,0,0,0.03)]">
            <UserAvatar
              src={user.avatar_url}
              name={user.nickname || user.username}
              className="h-8 w-8 rounded-[8px] text-sm"
            />
            <div className="min-w-0 flex-1">
              <p className="truncate text-sm font-medium text-sidebar-foreground">
                {user.nickname || user.username}
              </p>
            </div>
            <ChevronUp className="h-4 w-4 text-muted-foreground shrink-0" />
          </button>
        </PopoverTrigger>
        <PopoverContent side="top" align="start" className="w-56 p-1.5">
          <div className="px-2.5 py-2 mb-1">
            <p className="text-sm font-medium truncate">{user.nickname || user.username}</p>
            <p className="text-xs text-muted-foreground truncate">{user.email || user.role}</p>
          </div>

          <div className="h-px bg-border mx-1 my-1" />

          <div className="px-1">
            <p className="px-2 py-1 text-xs font-medium text-muted-foreground">主题</p>
            <div className="flex gap-0.5 px-1 pb-1">
              <ThemeBtn active={theme === "light"} onClick={() => setTheme("light")} icon={<Sun className="h-3.5 w-3.5" />} label="亮色" />
              <ThemeBtn active={theme === "dark"} onClick={() => setTheme("dark")} icon={<Moon className="h-3.5 w-3.5" />} label="暗色" />
              <ThemeBtn active={theme === "system"} onClick={() => setTheme("system")} icon={<Monitor className="h-3.5 w-3.5" />} label="系统" />
            </div>
          </div>

          <div className="h-px bg-border mx-1 my-1" />

          <div className="px-1">
            <div className="flex items-center justify-between px-2 py-1">
              <p className="text-xs font-medium text-muted-foreground">对话字号</p>
              <span className="text-xs tabular-nums text-muted-foreground">
                {Math.round(chatFontScale * 100)}%
              </span>
            </div>
            <div className="flex items-center gap-2 px-2 pb-2">
              <Type className="h-3 w-3 shrink-0 text-muted-foreground" />
              <input
                type="range"
                aria-label="对话字号"
                min={0}
                max={CHAT_FONT_STEPS.length - 1}
                step={1}
                value={Math.max(0, CHAT_FONT_STEPS.indexOf(chatFontScale))}
                onChange={(e) => setChatFontScale(CHAT_FONT_STEPS[Number(e.target.value)])}
                className="font-range h-1 flex-1 cursor-pointer appearance-none rounded-full bg-border"
              />
              <Type className="h-4 w-4 shrink-0 text-muted-foreground" />
            </div>
          </div>

          <div className="h-px bg-border mx-1 my-1" />

          <button
            onClick={() => {
              setPopoverOpen(false)
              setSettingsOpen(true)
            }}
            className="flex w-full items-center gap-2 rounded-[8px] px-2.5 py-2 text-sm font-medium text-foreground/80 transition-[background-color,color,box-shadow] duration-200 motion-control hover:bg-accent/70 hover:text-foreground hover:shadow-[0_1px_2px_rgba(0,0,0,0.03)]"
          >
            <Settings className="h-4 w-4" />
            设置
          </button>
          {user.role === "admin" && (
            <button
              onClick={() => {
                setPopoverOpen(false)
                navigateWithFade(navigate, "/admin/models", { state: { from: `${location.pathname}${location.search}` } })
              }}
              className="flex w-full items-center gap-2 rounded-[8px] px-2.5 py-2 text-sm font-medium text-foreground/80 transition-[background-color,color,box-shadow] duration-200 motion-control hover:bg-accent/70 hover:text-foreground hover:shadow-[0_1px_2px_rgba(0,0,0,0.03)]"
            >
              <Shield className="h-4 w-4" />
              管理后台
            </button>
          )}
          <button
            onClick={logout}
            className="flex w-full items-center gap-2 rounded-[8px] px-2.5 py-2 text-sm font-medium text-foreground/80 transition-[background-color,color,box-shadow] duration-200 motion-control hover:bg-destructive/15 hover:text-destructive-foreground hover:shadow-sm"
          >
            <LogOut className="h-4 w-4" />
            退出登录
          </button>
          <div className="mx-1 mt-1 border-t border-border px-2.5 pt-2 pb-1 text-xs text-muted-foreground">
            {systemVersion}
          </div>
        </PopoverContent>
      </Popover>

      {settingsOpen ? (
        <LazyBoundary label="设置" variant="dialog">
          <SettingsDialog
            open
            onOpenChange={setSettingsOpen}
            onOpenPromptManager={() => setPromptDialogOpen(true)}
          />
        </LazyBoundary>
      ) : null}
      {promptDialogOpen ? (
        <LazyBoundary label="提示词管理" variant="dialog">
          <UserPromptDialog open onOpenChange={setPromptDialogOpen} />
        </LazyBoundary>
      ) : null}
    </>
  )
}

function ThemeBtn({ active, onClick, icon, label }: { active: boolean; onClick: () => void; icon: React.ReactNode; label: string }) {
  return (
    <button
      onClick={onClick}
      className={`flex flex-1 flex-col items-center gap-1 rounded-md py-1.5 text-xs transition-colors motion-control ${
        active ? "bg-accent text-accent-foreground" : "text-muted-foreground hover:text-foreground"
      }`}
    >
      {icon}
      {label}
    </button>
  )
}
