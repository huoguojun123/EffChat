import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useLocation, useNavigate, useParams, useBlocker } from "react-router"
import { ArrowLeft, Menu, RefreshCw, X } from "lucide-react"
import { adminApi, type AdminUser, type AIChannel, type ChatFontSelection, type ConfigItem, type ExternalService, type ToolConfig } from "@/api/admin"
import type { FontAsset, SkillDefinition, UserGroup } from "@/types"
import { useChatStore } from "@/stores/chat"
import { useModelStore } from "@/stores/models"
import { navigateWithFade } from "@/lib/navigation"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { MotionView } from "@/components/ui/motion"
import { LoadingIndicator } from "@/components/ui/loading-indicator"
import { PromptManager } from "@/components/prompts/PromptManager"
import { AdminChannelsPanel } from "./AdminChannelsPanel"
import { AdminConfigPanel } from "./AdminConfigPanel"
import { AdminFontsPanel } from "./AdminFontsPanel"
import { AdminGroupsPanel } from "./AdminGroupsPanel"
import { AdminModelsPanel } from "./AdminModelsPanel"
import { AdminSkillsPanel } from "./AdminSkillsPanel"
import { AdminStatusPanel } from "./AdminStatusPanel"
import { AdminToolsPanel } from "./AdminToolsPanel"
import { AdminUsagePanel } from "./AdminUsagePanel"
import { AdminUsersPanel } from "./AdminUsersPanel"
import { adminDirtyChanged, adminLoadFailed, adminLoadStarted, adminLoadSucceeded, initialAdminPanelState } from "./AdminPanelState"
import { ADMIN_NAV, adminTab, isAdminTabKey, type AdminTabKey } from "./adminNavigation"

type AdminResource = "channels" | "config" | "fonts" | "groups" | "models" | "services" | "skills" | "tools" | "users"

const tabResources: Record<AdminTabKey, AdminResource[]> = {
  models: ["models", "channels", "groups"],
  channels: ["services"],
  usage: [],
  groups: ["groups"],
  users: ["users", "groups"],
  tools: ["tools"],
  systemPrompt: ["config", "models"],
  prompts: [],
  skills: ["skills", "groups"],
  config: ["config", "models"],
  fonts: ["fonts"],
  status: [],
}

export function AdminPage() {
  const { section } = useParams()
  const navigate = useNavigate()
  const location = useLocation()
  const tab = isAdminTabKey(section) ? section : "models"
  const currentTab = adminTab(tab)
  const models = useModelStore((state) => state.models)
  const setModels = useModelStore((state) => state.setModels)
  const activeSessionId = useChatStore((state) => state.activeSessionId)
  const [users, setUsers] = useState<AdminUser[]>([])
  const [groups, setGroups] = useState<UserGroup[]>([])
  const [configItems, setConfigItems] = useState<ConfigItem[]>([])
  const [skills, setSkills] = useState<SkillDefinition[]>([])
  const [fonts, setFonts] = useState<FontAsset[]>([])
  const [channels, setChannels] = useState<AIChannel[]>([])
  const [externalServices, setExternalServices] = useState<ExternalService[]>([])
  const [tools, setTools] = useState<ToolConfig[]>([])
  const [selectedFontIds, setSelectedFontIds] = useState<ChatFontSelection>({})
  const [panelState, setPanelState] = useState(initialAdminPanelState)
  const [panelDirty, setPanelDirtyState] = useState(false)
  const [configResetSignal, setConfigResetSignal] = useState(0)
  const [mobileNavOpen, setMobileNavOpen] = useState(false)
  const [statusRefreshSignal, setStatusRefreshSignal] = useState(0)
  const [tabDirection, setTabDirection] = useState<"forward" | "back">("forward")
  const loadedResourcesRef = useRef(new Set<AdminResource>())
  const loadRequestRef = useRef(0)
  const previousTabRef = useRef(tab)

  const blocker = useBlocker(useCallback(() => panelDirty, [panelDirty]))

  const loadResources = useCallback(async (resources: AdminResource[], force = false) => {
    const required = resources.filter((resource) => force || !loadedResourcesRef.current.has(resource))
    if (!required.length) return

    const requestId = loadRequestRef.current + 1
    loadRequestRef.current = requestId
    setPanelState((previous) => adminLoadStarted(previous))

    try {
      await Promise.all(required.map(async (resource) => {
        if (resource === "users") {
          const result = await adminApi.listAllUsers()
          if (requestId === loadRequestRef.current) setUsers(result)
        }
        if (resource === "models") {
          const result = await adminApi.listModels()
          if (requestId === loadRequestRef.current) setModels((result.models || []).sort((a, b) => a.sort_order - b.sort_order))
        }
        if (resource === "config") {
          const result = await adminApi.listConfig()
          if (requestId === loadRequestRef.current) setConfigItems((result.config || []).sort((a, b) => (a.sort_order || 0) - (b.sort_order || 0)))
        }
        if (resource === "groups") {
          const result = await adminApi.listGroups()
          if (requestId === loadRequestRef.current) setGroups((result.groups || []).sort((a, b) => a.level - b.level))
        }
        if (resource === "skills") {
          const result = await adminApi.listSkills()
          if (requestId === loadRequestRef.current) setSkills((result.skills || []).sort((a, b) => a.name.localeCompare(b.name)))
        }
        if (resource === "fonts") {
          const result = await adminApi.listFonts()
          if (requestId === loadRequestRef.current) {
            setFonts(result.fonts || [])
            setSelectedFontIds(result.selected_font_ids || {
              chinese: result.selected_font_id ?? null,
              latin: result.selected_font_id ?? null,
              code: result.selected_font_id ?? null,
            })
          }
        }
        if (resource === "channels") {
          const result = await adminApi.listChannels()
          if (requestId === loadRequestRef.current) setChannels((result.channels || []).sort((a, b) => a.sort_order - b.sort_order || a.key.localeCompare(b.key)))
        }
        if (resource === "services") {
          const result = await adminApi.listExternalServices()
          if (requestId === loadRequestRef.current) setExternalServices((result.services || []).sort((a, b) => a.sort_order - b.sort_order || a.key.localeCompare(b.key)))
        }
        if (resource === "tools") {
          const result = await adminApi.listToolConfigs()
          if (requestId === loadRequestRef.current) setTools((result.tools || []).sort((a, b) => a.sort_order - b.sort_order || a.key.localeCompare(b.key)))
        }
      }))

      if (requestId !== loadRequestRef.current) return
      required.forEach((resource) => loadedResourcesRef.current.add(resource))
      setPanelState((previous) => adminLoadSucceeded(previous))
    } catch (error) {
      if (requestId !== loadRequestRef.current) return
      setPanelState((previous) => adminLoadFailed(previous, error instanceof Error ? error.message : "加载失败"))
    }
  }, [setModels])

  useEffect(() => {
    if (!isAdminTabKey(section)) {
      navigate("/admin/models", { replace: true })
      return
    }
    const previous = previousTabRef.current
    if (previous !== tab) {
      const previousIndex = ADMIN_NAV.flatMap((group) => group.tabs).findIndex((item) => item.key === previous)
      const nextIndex = ADMIN_NAV.flatMap((group) => group.tabs).findIndex((item) => item.key === tab)
      setTabDirection(nextIndex > previousIndex ? "forward" : "back")
      previousTabRef.current = tab
    }
    void loadResources(tabResources[tab])
  }, [loadResources, navigate, section, tab])

  useEffect(() => {
    if (!panelDirty) return
    const handleBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault()
      event.returnValue = ""
    }
    window.addEventListener("beforeunload", handleBeforeUnload)
    return () => window.removeEventListener("beforeunload", handleBeforeUnload)
  }, [panelDirty])

  const setPanelError = useCallback((error: string) => {
    setPanelState((previous) => ({ ...previous, error }))
  }, [])

  const setPanelDirty = useCallback((dirty: boolean) => {
    setPanelDirtyState(dirty)
    setPanelState((previous) => adminDirtyChanged(previous, dirty))
  }, [])

  const navigateToTab = useCallback((nextTab: AdminTabKey) => {
    setMobileNavOpen(false)
    if (nextTab !== tab) navigate(`/admin/${nextTab}`)
  }, [navigate, tab])

  const refreshCurrent = useCallback(() => {
    if (tab === "status") {
      setStatusRefreshSignal((value) => value + 1)
      return
    }
    void loadResources(tabResources[tab], true)
  }, [loadResources, tab])

  const returnToChat = useCallback(() => {
    const source = typeof location.state?.from === "string" && !location.state.from.startsWith("/admin")
      ? location.state.from
      : activeSessionId ? `/chat/${activeSessionId}` : "/"
    navigateWithFade(navigate, source)
  }, [activeSessionId, location.state, navigate])

  const discardBlockedNavigation = useCallback(() => {
    setPanelDirtyState(false)
    setConfigResetSignal((value) => value + 1)
    setPanelState((previous) => adminDirtyChanged(previous, false))
    blocker.proceed?.()
  }, [blocker])

  const renderPanel = useMemo(() => {
    if (tab === "channels") return <AdminChannelsPanel services={externalServices} setServices={setExternalServices} setError={setPanelError} onDirtyChange={setPanelDirty} />
    if (tab === "models") return <AdminModelsPanel models={models} setModels={setModels} groups={groups} channels={channels} setChannels={setChannels} setError={setPanelError} onDirtyChange={setPanelDirty} />
    if (tab === "tools") return <AdminToolsPanel tools={tools} setTools={setTools} setError={setPanelError} />
    if (tab === "usage") return <AdminUsagePanel setError={setPanelError} />
    if (tab === "status") return <AdminStatusPanel refreshSignal={statusRefreshSignal} setError={setPanelError} />
    if (tab === "users") return <AdminUsersPanel users={users} setUsers={setUsers} groups={groups} setError={setPanelError} onDirtyChange={setPanelDirty} />
    if (tab === "groups") return <AdminGroupsPanel groups={groups} setGroups={setGroups} setError={setPanelError} onDirtyChange={setPanelDirty} />
    if (tab === "systemPrompt") {
      return <AdminConfigPanel key={`systemPrompt-${configResetSignal}`} configItems={configItems} setConfigItems={setConfigItems} models={models} setError={setPanelError} includeKeys={["system_prompt_template"]} onDirtyChange={setPanelDirty} />
    }
    if (tab === "config") {
      return <AdminConfigPanel key={`config-${configResetSignal}`} configItems={configItems} setConfigItems={setConfigItems} models={models} setError={setPanelError} excludeKeys={["system_prompt_template"]} onDirtyChange={setPanelDirty} />
    }
    if (tab === "prompts") return <PromptManager scope="admin" onDirtyChange={setPanelDirty} />
    if (tab === "fonts") return <AdminFontsPanel fonts={fonts} selectedFontIds={selectedFontIds} setFonts={setFonts} setSelectedFontIds={setSelectedFontIds} setError={setPanelError} />
    return <AdminSkillsPanel skills={skills} setSkills={setSkills} groups={groups} setError={setPanelError} onDirtyChange={setPanelDirty} />
  }, [channels, configItems, configResetSignal, externalServices, fonts, groups, models, selectedFontIds, setModels, setPanelDirty, setPanelError, skills, statusRefreshSignal, tab, tools, users])

  const busy = panelState.loading || panelState.refreshing

  return (
    <div className="flex h-[100dvh] min-w-0 flex-col overflow-hidden bg-background text-foreground">
      <header className="flex h-14 shrink-0 items-center gap-2 border-b border-border/70 px-3 sm:px-5">
        <Button type="button" variant="ghost" size="icon" className="h-9 w-9" onClick={returnToChat} aria-label="返回聊天" title="返回聊天">
          <ArrowLeft className="h-4 w-4" />
        </Button>
        <div className="min-w-0 flex-1">
          <h1 className="truncate text-sm font-semibold">{currentTab.label}</h1>
        </div>
        <Button type="button" variant="ghost" size="icon" className="h-9 w-9" onClick={refreshCurrent} disabled={busy} aria-label="刷新当前页面" title="刷新当前页面">
          <RefreshCw className={`h-4 w-4 ${busy ? "animate-spin motion-reduce:animate-none" : ""}`} />
        </Button>
        <Button type="button" variant="ghost" size="icon" className="h-9 w-9 lg:hidden" onClick={() => setMobileNavOpen(true)} aria-label="打开管理导航" title="管理导航">
          <Menu className="h-4 w-4" />
        </Button>
      </header>

      <div className="grid min-h-0 flex-1 lg:grid-cols-[var(--admin-nav-width)_minmax(0,1fr)]">
        <nav className="hidden min-h-0 overflow-y-auto border-r border-border/70 px-3 py-4 lg:block" aria-label="管理后台导航">
          {ADMIN_NAV.map((group) => (
            <section key={group.key} className="mb-5 last:mb-0">
              <h2 className="mb-1 px-2.5 text-sm font-medium text-muted-foreground">{group.label}</h2>
              <div className="space-y-0.5">
                {group.tabs.map((item) => <AdminNavButton key={item.key} item={item} active={item.key === tab} onClick={() => navigateToTab(item.key)} />)}
              </div>
            </section>
          ))}
        </nav>

        <main className="min-h-0 min-w-0 overflow-y-auto overscroll-contain lg:overflow-hidden">
          {panelState.error ? <div role="alert" className="border-b border-destructive/20 bg-destructive/10 px-4 py-2 text-sm text-destructive">{panelState.error}</div> : null}
          <MotionView viewKey={tab} direction={tabDirection} className="min-h-full min-w-0 lg:h-full lg:min-h-0">
            <div className="min-h-full min-w-0 px-0 py-0 sm:px-5 sm:py-5 lg:h-full lg:min-h-0" aria-busy={panelState.loading}>
              {panelState.loading ? <LoadingIndicator label="正在加载管理页面" className="h-full min-h-32" /> : renderPanel}
            </div>
          </MotionView>
        </main>
      </div>

      <Dialog open={mobileNavOpen} onOpenChange={setMobileNavOpen}>
        <DialogContent showClose={false} className="bottom-0 left-0 top-auto w-full max-w-none translate-x-0 translate-y-0 gap-0 rounded-b-none rounded-t-xl border-x-0 border-b-0 p-0 pb-[max(0.75rem,env(safe-area-inset-bottom))] sm:max-w-none">
          <DialogHeader className="flex-row items-center border-b border-border/70 px-4 py-3">
            <DialogTitle className="text-sm">管理后台</DialogTitle>
            <DialogDescription className="sr-only">选择管理后台栏目。</DialogDescription>
            <Button type="button" variant="ghost" size="icon" className="ml-auto h-9 w-9" onClick={() => setMobileNavOpen(false)} aria-label="关闭管理导航">
              <X className="h-4 w-4" />
            </Button>
          </DialogHeader>
          <nav className="max-h-[min(78dvh,640px)] overflow-y-auto p-2" aria-label="管理后台导航">
            {ADMIN_NAV.map((group) => (
              <section key={group.key} className="py-2">
                <h2 className="px-2.5 pb-1 text-sm font-medium text-muted-foreground">{group.label}</h2>
                <div className="space-y-0.5">
                  {group.tabs.map((item) => <AdminNavButton key={item.key} item={item} active={item.key === tab} onClick={() => navigateToTab(item.key)} />)}
                </div>
              </section>
            ))}
          </nav>
        </DialogContent>
      </Dialog>

      <Dialog open={blocker.state === "blocked"} onOpenChange={(open) => !open && blocker.reset?.()}>
        <DialogContent className="gap-4" showClose={false}>
          <DialogHeader>
            <DialogTitle>放弃未保存修改？</DialogTitle>
            <DialogDescription>配置尚未保存，离开后这些修改会丢失。</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => blocker.reset?.()}>继续编辑</Button>
            <Button type="button" variant="destructive" onClick={discardBlockedNavigation}>放弃修改</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}

function AdminNavButton({ item, active, onClick }: { item: ReturnType<typeof adminTab>; active: boolean; onClick: () => void }) {
  return (
    <button type="button" onClick={onClick} aria-current={active ? "page" : undefined} className={`flex h-10 w-full items-center gap-2.5 rounded-[8px] px-2.5 text-left text-sm font-medium transition-[background-color,color,box-shadow] duration-200 motion-control ${active ? "bg-accent text-accent-foreground shadow-[0_1px_2px_rgba(0,0,0,0.03)]" : "text-muted-foreground hover:bg-muted hover:text-foreground"}`}>
      {item.icon}
      <span className="truncate">{item.label}</span>
    </button>
  )
}
