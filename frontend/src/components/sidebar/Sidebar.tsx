import { useEffect, useMemo, useRef, useState } from "react"
import { useNavigate } from "react-router"
import { useChatStore } from "@/stores/chat"
import { useUIStore } from "@/stores/ui"
import { useSystemStore } from "@/stores/system"
import { SessionList } from "./SessionList"
import { UserMenu } from "./UserMenu"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { Check, Folder, FolderPlus, Inbox, Loader2, MessagesSquare, MessageSquareText, Pencil, Pin, PinOff, Plus, Search, Trash2, X } from "lucide-react"
import { AppLogo } from "@/components/AppLogo"
import { cn } from "@/lib/utils"
import type { SessionFolder } from "@/types"
import { searchConversations, type ConversationSearchResult } from "@/api/sessions"
import { useAuthStore } from "@/stores/auth"

export function Sidebar() {
  const navigate = useNavigate()
  const createSession = useChatStore((s) => s.createSession)
  const sessionCreateReadiness = useChatStore((s) => s.sessionCreateReadiness)
  const isLoadingSessionCreateReadiness = useChatStore((s) => s.isLoadingSessionCreateReadiness)
  const sessionCreateReadinessError = useChatStore((s) => s.sessionCreateReadinessError)
  const isCreatingSession = useChatStore((s) => s.isCreatingSession)
  const sessionCreateError = useChatStore((s) => s.sessionCreateError)
  const loadSessionCreateReadiness = useChatStore((s) => s.loadSessionCreateReadiness)
  const user = useAuthStore((s) => s.user)
  const sessions = useChatStore((s) => s.sessions)
  const sessionFolders = useChatStore((s) => s.sessionFolders)
  const activeFolderId = useChatStore((s) => s.activeFolderId)
  const setActiveFolder = useChatStore((s) => s.setActiveFolder)
  const createSessionFolder = useChatStore((s) => s.createSessionFolder)
  const renameSessionFolder = useChatStore((s) => s.renameSessionFolder)
  const setSessionFolderPinned = useChatStore((s) => s.setSessionFolderPinned)
  const deleteSessionFolder = useChatStore((s) => s.deleteSessionFolder)
  const loadMoreSessions = useChatStore((s) => s.loadMoreSessions)
  const hasMoreSessions = useChatStore((s) => s.hasMoreSessions)
  const isLoadingMoreSessions = useChatStore((s) => s.isLoadingMoreSessions)
  const setSidebarOpen = useUIStore((s) => s.setSidebarOpen)
  const systemName = useSystemStore((s) => s.systemName)
  const [query, setQuery] = useState("")
  const [creatingFolder, setCreatingFolder] = useState(false)
  const [folderName, setFolderName] = useState("")
  const [editingFolderId, setEditingFolderId] = useState<number | null>(null)
  const [editingFolderName, setEditingFolderName] = useState("")
  const [folderError, setFolderError] = useState("")
  const [pendingDeleteFolder, setPendingDeleteFolder] = useState<SessionFolder | null>(null)
  const [deletingFolderId, setDeletingFolderId] = useState<number | null>(null)
  const [searchAll, setSearchAll] = useState(false)
  const [searchResults, setSearchResults] = useState<ConversationSearchResult[]>([])
  const [searchLoading, setSearchLoading] = useState(false)
  const [searchError, setSearchError] = useState("")
  const searchRequestRef = useRef(0)

  function closeOnMobile() {
    if (window.matchMedia("(max-width: 768px)").matches) {
      setSidebarOpen(false, false)
    }
  }

  async function handleNewChat() {
    try {
      const session = await createSession()
      navigate(`/chat/${session.id}`)
      closeOnMobile()
    } catch {
      // The shared store renders the failure under both creation entries.
    }
  }

  async function confirmCreateFolder() {
    const name = folderName.trim()
    if (!name) return
    setFolderError("")
    if (hasFolderName(name, sessionFolders)) {
      setFolderError(`文件夹“${name}”已经存在`)
      return
    }
    try {
      const folder = await createSessionFolder(name)
      setFolderName("")
      setCreatingFolder(false)
      setActiveFolder(folder.id)
    } catch (err) {
      setFolderError(formatFolderError(err, "创建文件夹失败"))
    }
  }

  async function confirmRenameFolder(id: number) {
    const name = editingFolderName.trim()
    if (!name) {
      setEditingFolderId(null)
      setEditingFolderName("")
      return
    }
    setFolderError("")
    if (hasFolderName(name, sessionFolders, id)) {
      setFolderError(`文件夹“${name}”已经存在`)
      return
    }
    try {
      await renameSessionFolder(id, name)
      setEditingFolderId(null)
      setEditingFolderName("")
    } catch (err) {
      setFolderError(formatFolderError(err, "重命名文件夹失败"))
    }
  }

  async function confirmDeleteFolder() {
    if (!pendingDeleteFolder) return
    setFolderError("")
    setDeletingFolderId(pendingDeleteFolder.id)
    try {
      await deleteSessionFolder(pendingDeleteFolder.id)
      setPendingDeleteFolder(null)
    } catch (err) {
      setFolderError(formatFolderError(err, "删除文件夹失败"))
    } finally {
      setDeletingFolderId(null)
    }
  }

  function handleSessionScroll(e: React.UIEvent<HTMLDivElement>) {
    if (query || !hasMoreSessions || isLoadingMoreSessions) return
    const target = e.currentTarget
    if (target.scrollHeight - target.scrollTop - target.clientHeight < 180) {
      loadMoreSessions()
    }
  }

  const normalizedQuery = query.trim()
  const globalSearchActive = Array.from(normalizedQuery).length >= 2

  useEffect(() => {
    const request = ++searchRequestRef.current
    if (!globalSearchActive) {
      queueMicrotask(() => {
        if (request !== searchRequestRef.current) return
        setSearchResults([])
        setSearchLoading(false)
        setSearchError("")
        setSearchAll(false)
      })
      return
    }
    const timer = window.setTimeout(() => {
      setSearchLoading(true)
      setSearchError("")
      void searchConversations(normalizedQuery, activeFolderId, searchAll).then((res) => {
        if (request === searchRequestRef.current) setSearchResults(res.results || [])
      }).catch((err) => {
        if (request === searchRequestRef.current) setSearchError(err instanceof Error ? err.message : "搜索失败")
      }).finally(() => {
        if (request === searchRequestRef.current) setSearchLoading(false)
      })
    }, 250)
    return () => window.clearTimeout(timer)
  }, [activeFolderId, globalSearchActive, normalizedQuery, searchAll])

  function selectSearchResult(result: ConversationSearchResult) {
    const turn = result.kind === "message" && result.turn_id ? `?turn=${result.turn_id}` : ""
    navigate(`/chat/${result.session_id}${turn}`, { flushSync: true })
    setQuery("")
    closeOnMobile()
  }

  const localQuery = normalizedQuery.toLowerCase()
  const filtered = useMemo(() => sessions.filter((session) => (
    (session.pinned_at || sessionMatchesFolder(session.folder_id, activeFolderId)) &&
    (!localQuery || session.title.toLowerCase().includes(localQuery))
  )), [activeFolderId, localQuery, sessions])

  return (
    <div className="flex h-full flex-col bg-sidebar">
      <div className="flex h-12 items-center gap-2 px-3 shrink-0">
        <AppLogo className="h-5 w-5" />
        <span className="text-sm font-semibold text-sidebar-foreground tracking-tight">
          {systemName}
        </span>
      </div>

      <div className="px-2.5 pb-2 space-y-1.5">
        <button
          onClick={handleNewChat}
          disabled={isLoadingSessionCreateReadiness || !sessionCreateReadiness?.ready || isCreatingSession}
          className="flex w-full items-center gap-2 rounded-lg border border-sidebar-border/60 bg-sidebar px-2.5 py-2 text-sm font-medium text-sidebar-foreground shadow-[0_1px_3px_rgba(0,0,0,0.05)] transition-[background-color,border-color,box-shadow] motion-control hover:border-sidebar-border hover:bg-sidebar-accent/80 hover:shadow-[0_2px_8px_rgba(0,0,0,0.08)]"
          data-testid="new-chat"
        >
          {isCreatingSession ? <Loader2 className="h-4 w-4 animate-spin" /> : <Plus className="h-4 w-4" />}
          {isCreatingSession ? "正在创建" : "新对话"}
        </button>
        {sessionCreateReadinessError ? (
          <div role="alert" className="rounded-md bg-destructive/10 px-2.5 py-2 text-xs text-destructive">
            <p>{sessionCreateReadinessError}</p>
            <button type="button" className="mt-1 underline" onClick={() => void loadSessionCreateReadiness(true)}>重新检查</button>
          </div>
        ) : !isLoadingSessionCreateReadiness && !sessionCreateReadiness?.ready ? (
          <div role="status" className="rounded-md bg-sidebar-accent/60 px-2.5 py-2 text-xs text-muted-foreground">
            <p>{user?.role === "admin" ? "请先选择全局默认模型" : "请联系管理员配置默认模型"}</p>
            {user?.role === "admin" ? <button type="button" className="mt-1 underline" onClick={() => navigate("/admin/models")}>配置模型</button> : null}
            {sessionCreateReadiness?.retryable ? <button type="button" className="ml-2 mt-1 underline" onClick={() => void loadSessionCreateReadiness(true)}>重新检查</button> : null}
          </div>
        ) : sessionCreateError ? (
          <div role="alert" className="rounded-md bg-destructive/10 px-2.5 py-2 text-xs text-destructive">{sessionCreateError}</div>
        ) : null}

        <div className="relative">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground" />
          <input
            type="search"
            name="effchat-session-search"
            aria-label="搜索对话"
            autoComplete="off"
            autoCorrect="off"
            spellCheck={false}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="搜索对话"
            className="h-9 w-full rounded-lg border border-transparent bg-sidebar-accent/40 pl-8 pr-7 text-sm outline-none transition-[background-color,border-color,box-shadow] motion-control placeholder:text-muted-foreground/60 focus:border-sidebar-border/60 focus:bg-sidebar-accent focus:shadow-[0_0_0_2px_rgba(0,0,0,0.05)]"
          />
          {query && (
            <button
              onClick={() => setQuery("")}
              className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
              aria-label="清空搜索"
            >
              <X className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
        {globalSearchActive && activeFolderId !== "all" ? (
          <div className="flex items-center gap-1 text-xs text-muted-foreground">
            <button type="button" onClick={() => setSearchAll(false)} className={cn("rounded px-1.5 py-0.5", !searchAll && "bg-sidebar-accent text-sidebar-foreground")}>当前范围</button>
            <button type="button" onClick={() => setSearchAll(true)} className={cn("rounded px-1.5 py-0.5", searchAll && "bg-sidebar-accent text-sidebar-foreground")}>全部对话</button>
          </div>
        ) : null}
      </div>

      <div className={cn("shrink-0 px-2 pb-2", globalSearchActive && "hidden")}>
        <div className="space-y-0.5">
          <FolderNavButton
            active={activeFolderId === "all"}
            icon={<MessagesSquare className="h-3.5 w-3.5" />}
            label="全部话题"
            onClick={() => setActiveFolder("all")}
          />
          <FolderNavButton
            active={activeFolderId === "unfiled"}
            icon={<Inbox className="h-3.5 w-3.5" />}
            label="未分组"
            onClick={() => setActiveFolder("unfiled")}
          />
          <div className="max-h-40 overflow-y-auto scrollbar-thin">
            {sessionFolders.map((folder) => (
              <div
                key={folder.id}
                className={cn(
                  "group flex cursor-pointer items-center gap-1.5 rounded-lg px-2.5 py-2 text-sm transition-[background-color,color,box-shadow] motion-control",
                  activeFolderId === folder.id
                    ? "bg-sidebar-accent text-sidebar-accent-foreground shadow-[0_1px_2px_rgba(0,0,0,0.03)]"
                    : "text-sidebar-foreground/80 hover:bg-sidebar-accent/50 hover:text-sidebar-foreground"
                )}
              >
                <Folder className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                {editingFolderId === folder.id ? (
                  <>
                    <input
                      value={editingFolderName}
                      onChange={(e) => {
                        setEditingFolderName(e.target.value)
                        setFolderError("")
                      }}
                      onKeyDown={(e) => {
                        if (e.key === "Enter") confirmRenameFolder(folder.id)
                        if (e.key === "Escape") setEditingFolderId(null)
                      }}
                      className="min-w-0 flex-1 bg-transparent text-sm outline-none"
                      autoFocus
                    />
                    <FolderIconButton ariaLabel={`保存文件夹名称：${editingFolderName || folder.name}`} onClick={() => confirmRenameFolder(folder.id)}>
                      <Check className="h-3 w-3" />
                    </FolderIconButton>
                    <FolderIconButton ariaLabel="取消重命名文件夹" onClick={() => setEditingFolderId(null)}>
                      <X className="h-3 w-3" />
                    </FolderIconButton>
                  </>
                ) : (
                  <>
                    <button
                      className="min-w-0 flex-1 truncate text-left"
                      onClick={() => setActiveFolder(folder.id)}
                      aria-current={activeFolderId === folder.id ? "true" : undefined}
                    >
                      {folder.name}
                    </button>
                    <div className={cn("flex items-center gap-0.5 transition-opacity motion-control", activeFolderId === folder.id ? "opacity-100" : "opacity-0 group-hover:opacity-100")}>
                      <FolderIconButton
                        ariaLabel={`${folder.pinned_at ? "取消置顶" : "置顶"}文件夹：${folder.name}`}
                        onClick={() => void setSessionFolderPinned(folder.id, !folder.pinned_at).catch((err) => setFolderError(formatFolderError(err, "更新文件夹置顶失败")))}
                      >
                        {folder.pinned_at ? <PinOff className="h-3 w-3" /> : <Pin className="h-3 w-3" />}
                      </FolderIconButton>
                      <FolderIconButton
                        ariaLabel={`重命名文件夹：${folder.name}`}
                        onClick={() => {
                          setEditingFolderId(folder.id)
                          setEditingFolderName(folder.name)
                        }}
                      >
                        <Pencil className="h-3 w-3" />
                      </FolderIconButton>
                      <FolderIconButton
                        ariaLabel={`删除文件夹：${folder.name}`}
                        onClick={() => {
                          setFolderError("")
                          setPendingDeleteFolder(folder)
                        }}
                        danger
                      >
                        <Trash2 className="h-3 w-3" />
                      </FolderIconButton>
                    </div>
                  </>
                )}
              </div>
            ))}
          </div>

          {creatingFolder ? (
            <div className="flex items-center gap-1 rounded-md px-2 py-1.5 text-sm text-sidebar-foreground">
              <Folder className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
              <input
                value={folderName}
                onChange={(e) => {
                  setFolderName(e.target.value)
                  setFolderError("")
                }}
                onKeyDown={(e) => {
                  if (e.key === "Enter") confirmCreateFolder()
                  if (e.key === "Escape") setCreatingFolder(false)
                }}
                placeholder="新文件夹"
                className="min-w-0 flex-1 bg-transparent text-sm outline-none"
                autoFocus
              />
              <FolderIconButton ariaLabel="保存新文件夹" onClick={confirmCreateFolder}>
                <Check className="h-3 w-3" />
              </FolderIconButton>
              <FolderIconButton ariaLabel="取消新建文件夹" onClick={() => setCreatingFolder(false)}>
                <X className="h-3 w-3" />
              </FolderIconButton>
            </div>
          ) : (
            <button
              onClick={() => {
                setCreatingFolder(true)
                setFolderName("")
                setFolderError("")
              }}
              className="flex w-full cursor-pointer items-center gap-2 rounded-lg px-2.5 py-2 text-sm text-muted-foreground transition-colors motion-control hover:bg-sidebar-accent/50 hover:text-sidebar-foreground"
            >
              <FolderPlus className="h-3.5 w-3.5" />
              新建文件夹
            </button>
          )}
          {folderError ? (
            <div role="alert" className="rounded-md bg-destructive/10 px-2.5 py-1.5 text-xs text-destructive">
              {folderError}
            </div>
          ) : null}
        </div>
      </div>

      <div onScroll={handleSessionScroll} className="min-h-0 flex-1 overflow-y-auto overscroll-contain scrollbar-thin px-2">
        {globalSearchActive ? (
          <ConversationSearchResults results={searchResults} loading={searchLoading} error={searchError} onSelect={selectSearchResult} />
        ) : (
          <SessionList filteredSessions={filtered} onSelectSession={closeOnMobile} />
        )}
        {isLoadingMoreSessions && (
          <div className="px-3 py-3 text-center text-xs text-muted-foreground">
            加载更多...
          </div>
        )}
      </div>

      <div className="p-2">
        <UserMenu />
      </div>
      <Dialog open={!!pendingDeleteFolder} onOpenChange={(open) => !open && setPendingDeleteFolder(null)}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>删除文件夹</DialogTitle>
            <DialogDescription>
              确认删除“{pendingDeleteFolder?.name}”？里面的话题不会删除，只会移回未分组。
            </DialogDescription>
          </DialogHeader>
          {folderError ? (
            <div role="alert" className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
              {folderError}
            </div>
          ) : null}
          <DialogFooter>
            <Button variant="outline" size="sm" onClick={() => setPendingDeleteFolder(null)} disabled={deletingFolderId != null}>
              取消
            </Button>
            <Button
              variant="destructive"
              size="sm"
              className="text-white hover:text-white dark:text-white"
              onClick={confirmDeleteFolder}
              disabled={deletingFolderId != null}
            >
              删除
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}

function ConversationSearchResults({ results, loading, error, onSelect }: { results: ConversationSearchResult[]; loading: boolean; error: string; onSelect: (result: ConversationSearchResult) => void }) {
  if (loading && results.length === 0) return <div className="flex h-24 items-center justify-center text-xs text-muted-foreground"><Loader2 className="mr-2 h-3.5 w-3.5 animate-spin" />搜索中</div>
  if (error) return <div role="alert" className="px-2 py-3 text-xs text-destructive">{error}</div>
  if (!loading && results.length === 0) return <div className="px-2 py-8 text-center text-xs text-muted-foreground">没有找到相关对话</div>
  return (
    <div className="space-y-0.5 py-1" aria-busy={loading}>
      {results.map((result, index) => (
        <button
          key={`${result.kind}-${result.session_id}-${result.message_id || index}`}
          type="button"
          onClick={() => onSelect(result)}
          className="group flex w-full gap-2 rounded-md px-2.5 py-2 text-left transition-colors motion-control hover:bg-sidebar-accent/60"
        >
          {result.kind === "session" ? <MessagesSquare className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" /> : <MessageSquareText className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" />}
          <span className="min-w-0 flex-1">
            <span className="block truncate text-sm font-medium text-sidebar-foreground">{result.session_title || "新对话"}</span>
            {result.kind === "message" ? <span className="mt-0.5 line-clamp-2 block text-xs leading-4 text-muted-foreground">{result.role === "user" ? "你：" : "助手："}{result.snippet}</span> : null}
          </span>
        </button>
      ))}
    </div>
  )
}

function sessionMatchesFolder(folderId: number | null | undefined, activeFolderId: "all" | "unfiled" | number) {
  if (activeFolderId === "all") return true
  if (activeFolderId === "unfiled") return folderId == null
  return folderId === activeFolderId
}

function FolderNavButton({
  active,
  icon,
  label,
  onClick,
}: {
  active: boolean
  icon: React.ReactNode
  label: string
  onClick: () => void
}) {
  return (
    <button
      onClick={onClick}
      aria-current={active ? "true" : undefined}
      className={cn(
        "flex w-full cursor-pointer items-center gap-2 rounded-lg px-2.5 py-2 text-sm transition-[background-color,color,box-shadow] motion-control",
        active
          ? "bg-sidebar-accent text-sidebar-accent-foreground shadow-[0_1px_2px_rgba(0,0,0,0.03)]"
          : "text-sidebar-foreground/80 hover:bg-sidebar-accent/50 hover:text-sidebar-foreground"
      )}
    >
      {icon}
      <span className="truncate">{label}</span>
    </button>
  )
}

function FolderIconButton({
  children,
  onClick,
  ariaLabel,
  danger = false,
}: {
  children: React.ReactNode
  onClick: () => void
  ariaLabel: string
  danger?: boolean
}) {
  return (
    <button
      onClick={(e) => {
        e.stopPropagation()
        onClick()
      }}
      aria-label={ariaLabel}
      className={cn(
        "flex h-5 w-5 shrink-0 items-center justify-center rounded transition-colors motion-control",
        danger
          ? "text-muted-foreground hover:bg-destructive/15 hover:text-destructive-foreground"
          : "text-muted-foreground hover:bg-foreground/10 hover:text-foreground"
      )}
    >
      {children}
    </button>
  )
}

function hasFolderName(name: string, folders: SessionFolder[], exceptId?: number): boolean {
  const normalized = normalizeFolderName(name).toLowerCase()
  return folders.some((folder) => folder.id !== exceptId && normalizeFolderName(folder.name).toLowerCase() === normalized)
}

function normalizeFolderName(name: string): string {
  return name.trim().replace(/\s+/g, " ")
}

function formatFolderError(err: unknown, fallback: string): string {
  if (err instanceof Error && err.message) {
    if (/duplicate|unique|session_folders_user_name/i.test(err.message)) return "同名文件夹已经存在"
    return err.message
  }
  return fallback
}
