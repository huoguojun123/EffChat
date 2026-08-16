import { forwardRef, useState, type ButtonHTMLAttributes } from "react"
import { useNavigate } from "react-router"
import { useChatStore } from "@/stores/chat"
import { cn } from "@/lib/utils"
import { Trash2, Pencil, Check, X, Folder, Inbox, FolderPlus, Pin, PinOff } from "lucide-react"
import type { Session } from "@/types"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { MotionView } from "@/components/ui/motion"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { groupSessionsByDate } from "./sessionGroups"

export function SessionList({
  filteredSessions,
  onSelectSession,
}: {
  filteredSessions?: Session[]
  onSelectSession?: () => void
}) {
  const navigate = useNavigate()
  const sessions = useChatStore((s) => s.sessions)
  const activeSessionId = useChatStore((s) => s.activeSessionId)
  const sessionFolders = useChatStore((s) => s.sessionFolders)
  const setMessages = useChatStore((s) => s.setMessages)
  const deleteSession = useChatStore((s) => s.deleteSession)
  const renameSession = useChatStore((s) => s.renameSession)
  const moveSessionToFolder = useChatStore((s) => s.moveSessionToFolder)
  const setSessionPinned = useChatStore((s) => s.setSessionPinned)
  const createSessionFolder = useChatStore((s) => s.createSessionFolder)

  const [editingId, setEditingId] = useState<number | null>(null)
  const [editTitle, setEditTitle] = useState("")
  const [pendingDelete, setPendingDelete] = useState<Session | null>(null)
  const [folderMenuId, setFolderMenuId] = useState<number | null>(null)
  const [creatingFolderForId, setCreatingFolderForId] = useState<number | null>(null)
  const [newFolderName, setNewFolderName] = useState("")
  const [moveFolderError, setMoveFolderError] = useState("")
  const [pinError, setPinError] = useState("")

  const displaySessions = filteredSessions || sessions

  if (displaySessions.length === 0) {
    return (
      <p className="px-3 py-8 text-center text-xs text-muted-foreground">
        暂无对话
      </p>
    )
  }

  function startRename(id: number, title: string) {
    setEditingId(id)
    setEditTitle(title)
  }

  function confirmRename(id: number) {
    if (editTitle.trim()) {
      renameSession(id, editTitle.trim())
    }
    setEditingId(null)
  }

  async function confirmDelete() {
    if (!pendingDelete) return
    const deletingId = pendingDelete.id
    await deleteSession(deletingId)
    setPendingDelete(null)
    if (activeSessionId === deletingId) navigate("/", { replace: true })
  }

  async function createFolderAndMove(sessionId: number) {
    const name = newFolderName.trim()
    if (!name) return
    setMoveFolderError("")
    if (hasFolderName(name, sessionFolders)) {
      setMoveFolderError(`文件夹“${name}”已经存在`)
      return
    }
    try {
      const folder = await createSessionFolder(name)
      await moveSessionToFolder(sessionId, folder.id)
      setNewFolderName("")
      setCreatingFolderForId(null)
      setFolderMenuId(null)
    } catch (err) {
      setMoveFolderError(formatFolderError(err, "创建文件夹失败"))
    }
  }

  function selectSession(session: Session) {
    if (session.id !== activeSessionId) setMessages([])
    navigate(`/chat/${session.id}`, { flushSync: true })
    onSelectSession?.()
  }

  const grouped = groupSessionsByDate(displaySessions)

  return (
    <>
      <div className="space-y-3 py-1">
        {pinError ? (
          <div role="alert" className="rounded-md bg-destructive/10 px-2.5 py-1.5 text-xs text-destructive">
            {pinError}
          </div>
        ) : null}
        {grouped.map(({ label, items }) => (
          <div key={label}>
            {label ? <p className="px-2.5 pb-1 text-xs font-medium uppercase tracking-wider text-muted-foreground/60">
              {label}
            </p> : null}
            <div className="space-y-px">
              {items.map((session) => {
                const active = activeSessionId === session.id
                const editing = editingId === session.id

                return (
                  <div
                    key={session.id}
                    className={cn(
                      "group relative flex items-center gap-2 rounded-lg px-2.5 py-1.5 text-sm cursor-pointer transition-colors motion-control",
                      active
                        ? "bg-sidebar-accent text-sidebar-accent-foreground"
                        : "text-sidebar-foreground/80 hover:bg-sidebar-accent/60 hover:text-sidebar-foreground"
                    )}
                    onClick={() => {
                      if (!editing) selectSession(session)
                    }}
                  >
                    <MotionView viewKey={editing ? "editing" : "view"} className="flex min-w-0 flex-1 items-center gap-2">
                      {editing ? (
                        <div className="flex flex-1 items-center gap-1">
                          <input
                            value={editTitle}
                            onChange={(e) => setEditTitle(e.target.value)}
                            onKeyDown={(e) => {
                              if (e.key === "Enter") confirmRename(session.id)
                              if (e.key === "Escape") setEditingId(null)
                            }}
                            className="flex-1 bg-transparent text-sm outline-none"
                            autoFocus
                            onClick={(e) => e.stopPropagation()}
                          />
                          <IconBtn aria-label={`保存对话标题：${editTitle || session.title || "新对话"}`} onClick={(e) => { e.stopPropagation(); confirmRename(session.id) }}>
                            <Check className="h-3 w-3" />
                          </IconBtn>
                          <IconBtn aria-label="取消重命名对话" onClick={(e) => { e.stopPropagation(); setEditingId(null) }}>
                            <X className="h-3 w-3" />
                          </IconBtn>
                        </div>
                      ) : (
                        <>
                          <button
                            type="button"
                            className="min-w-0 flex-1 truncate text-left"
                            aria-current={active ? "page" : undefined}
                            onClick={(e) => {
                              e.stopPropagation()
                              selectSession(session)
                            }}
                          >
                            {session.title || "新对话"}
                          </button>
                          <div className={cn(
                            "flex items-center gap-0.5 transition-opacity motion-control",
                            active ? "opacity-100" : "opacity-0 group-hover:opacity-100"
                          )}>
                            <Popover
                              open={folderMenuId === session.id}
                              onOpenChange={(open) => {
                                setFolderMenuId(open ? session.id : null)
                                if (!open) {
                                  setCreatingFolderForId(null)
                                  setNewFolderName("")
                                  setMoveFolderError("")
                                }
                              }}
                            >
                              <PopoverTrigger asChild>
                                <IconBtn aria-label={`移动对话到文件夹：${session.title || "新对话"}`} onClick={(e) => e.stopPropagation()} onPointerDown={(e) => e.stopPropagation()}>
                                  <Folder className="h-3 w-3" />
                                </IconBtn>
                              </PopoverTrigger>
                              <PopoverContent side="bottom" align="end" className="w-[min(17rem,calc(100vw-2rem))] p-1.5" onClick={(e) => e.stopPropagation()}>
                                <FolderMoveItem
                                  active={session.folder_id == null}
                                  icon={<Inbox className="h-3.5 w-3.5" />}
                                  label="未分组"
                                  onClick={() => {
                                    moveSessionToFolder(session.id, null)
                                    setFolderMenuId(null)
                                  }}
                                />
                                <div className="my-1 h-px bg-border" />
                                <div className="max-h-56 overflow-y-auto scrollbar-thin">
                                  {sessionFolders.length === 0 ? (
                                    <p className="px-2.5 py-2 text-xs text-muted-foreground">暂无文件夹</p>
                                  ) : (
                                    sessionFolders.map((folder) => (
                                      <FolderMoveItem
                                        key={folder.id}
                                        active={session.folder_id === folder.id}
                                        icon={<Folder className="h-3.5 w-3.5" />}
                                        label={folder.name}
                                        onClick={() => {
                                          moveSessionToFolder(session.id, folder.id)
                                          setFolderMenuId(null)
                                        }}
                                      />
                                    ))
                                  )}
                                </div>
                                <div className="mt-1 border-t border-border/70 pt-1">
                                  {creatingFolderForId === session.id ? (
                                    <div className="flex items-center gap-1 px-1 py-1">
                                      <input
                                        value={newFolderName}
                                        onChange={(e) => {
                                          setNewFolderName(e.target.value)
                                          setMoveFolderError("")
                                        }}
                                        onKeyDown={(e) => {
                                          if (e.key === "Enter") void createFolderAndMove(session.id)
                                          if (e.key === "Escape") {
                                            setCreatingFolderForId(null)
                                            setNewFolderName("")
                                          }
                                        }}
                                        placeholder="新文件夹"
                                        className="h-8 min-w-0 flex-1 rounded-md border border-input bg-background px-2 text-sm outline-none"
                                        autoFocus
                                      />
                                      <IconBtn aria-label="保存新文件夹并移动对话" onClick={(e) => { e.stopPropagation(); void createFolderAndMove(session.id) }}>
                                        <Check className="h-3 w-3" />
                                      </IconBtn>
                                      <IconBtn aria-label="取消新建文件夹" onClick={(e) => { e.stopPropagation(); setCreatingFolderForId(null); setNewFolderName("") }}>
                                        <X className="h-3 w-3" />
                                      </IconBtn>
                                    </div>
                                  ) : (
                                    <FolderMoveItem
                                      active={false}
                                      icon={<FolderPlus className="h-3.5 w-3.5" />}
                                      label="新建文件夹并移动"
                                      onClick={() => {
                                        setCreatingFolderForId(session.id)
                                        setNewFolderName("")
                                      }}
                                    />
                                  )}
                                </div>
                                {moveFolderError ? (
                                  <div role="alert" className="mx-1 mt-1 rounded-md bg-destructive/10 px-2 py-1.5 text-xs text-destructive">
                                    {moveFolderError}
                                  </div>
                                ) : null}
                              </PopoverContent>
                            </Popover>
                            <IconBtn aria-label={`${session.pinned_at ? "取消置顶" : "置顶"}对话：${session.title || "新对话"}`} onClick={(e) => {
                              e.stopPropagation()
                              setPinError("")
                              void setSessionPinned(session.id, !session.pinned_at).catch((err) => setPinError(formatSessionError(err)))
                            }}>
                              {session.pinned_at ? <PinOff className="h-3 w-3" /> : <Pin className="h-3 w-3" />}
                            </IconBtn>
                            <IconBtn aria-label={`重命名对话：${session.title || "新对话"}`} onClick={(e) => { e.stopPropagation(); startRename(session.id, session.title) }}>
                              <Pencil className="h-3 w-3" />
                            </IconBtn>
                            <IconBtn
                              aria-label={`删除对话：${session.title || "新对话"}`}
                              variant="danger"
                              onClick={(e) => {
                                e.stopPropagation()
                                setPendingDelete(session)
                              }}
                            >
                            <Trash2 className="h-3 w-3" />
                            </IconBtn>
                          </div>
                        </>
                      )}
                    </MotionView>
                  </div>
                )
              })}
            </div>
          </div>
        ))}
      </div>

      <Dialog open={!!pendingDelete} onOpenChange={(open) => !open && setPendingDelete(null)}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>删除对话</DialogTitle>
            <DialogDescription>
              确认删除“{pendingDelete?.title || "新对话"}”？删除后无法恢复。
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" size="sm" onClick={() => setPendingDelete(null)}>
              取消
            </Button>
            <Button variant="destructive" size="sm" className="text-white hover:text-white dark:text-white" onClick={confirmDelete}>
              删除
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}

function FolderMoveItem({
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
      className={cn(
        "flex w-full items-center gap-2 rounded-[8px] px-2.5 py-1.5 text-sm font-medium outline-none transition-[background-color,color,box-shadow] duration-200 motion-control",
        active ? "bg-accent text-accent-foreground shadow-[0_1px_2px_rgba(0,0,0,0.03)]" : "text-foreground/80 hover:bg-accent/70 hover:text-foreground"
      )}
    >
      {icon}
      <span className="min-w-0 flex-1 truncate text-left">{label}</span>
      {active && <Check className="h-3.5 w-3.5 shrink-0" />}
    </button>
  )
}

const IconBtn = forwardRef<HTMLButtonElement, ButtonHTMLAttributes<HTMLButtonElement> & { variant?: "default" | "danger" }>(function IconBtn({
  children,
  className,
  type = "button",
  variant = "default",
  ...props
}, ref) {
  return (
    <button
      ref={ref}
      type={type}
      {...props}
      className={cn(
        "flex h-6 w-6 items-center justify-center rounded-[6px] transition-[background-color,color,box-shadow] duration-200 motion-control",
        variant === "danger"
          ? "text-muted-foreground hover:bg-destructive/15 hover:text-destructive-foreground hover:shadow-sm"
          : "text-muted-foreground hover:bg-foreground/10 hover:text-foreground hover:shadow-[0_1px_2px_rgba(0,0,0,0.03)]",
        className
      )}
    >
      {children}
    </button>
  )
})

function hasFolderName(name: string, folders: { id: number; name: string }[]): boolean {
  const normalized = normalizeFolderName(name).toLowerCase()
  return folders.some((folder) => normalizeFolderName(folder.name).toLowerCase() === normalized)
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

function formatSessionError(err: unknown): string {
  if (err instanceof Error && err.message) return err.message
  return "更新对话置顶失败"
}
