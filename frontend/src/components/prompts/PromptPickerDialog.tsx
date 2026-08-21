import { useEffect, useMemo, useRef, useState } from "react"
import { promptsApi } from "@/api/prompts"
import * as sessionsApi from "@/api/sessions"
import { useChatStore } from "@/stores/chat"
import { useAuthStore } from "@/stores/auth"
import type { Prompt } from "@/types"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { MotionView } from "@/components/ui/motion"
import { cn } from "@/lib/utils"
import { ArrowLeft, Check, Search } from "lucide-react"

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function PromptPickerDialog({ open, onOpenChange }: Props) {
  const activeSessionId = useChatStore((s) => s.activeSessionId)
  const sessions = useChatStore((s) => s.sessions)
  const updateSessionLocal = useChatStore((s) => s.updateSessionLocal)
  const userID = useAuthStore((s) => s.user?.id)
  const [prompts, setPrompts] = useState<Prompt[]>([])
  const [selected, setSelected] = useState<Prompt | null>(null)
  const [query, setQuery] = useState("")
  const [saving, setSaving] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")
  const [mobilePreviewOpen, setMobilePreviewOpen] = useState(false)
  const loadRequestRef = useRef(0)

  const activeSession = sessions.find((item) => item.id === activeSessionId)
  const filtered = useMemo(() => {
    const keyword = query.trim().toLowerCase()
    if (!keyword) return prompts
    return prompts.filter((item) => `${item.group_name} ${item.title} ${item.content}`.toLowerCase().includes(keyword))
  }, [prompts, query])
  const grouped = useMemo(() => {
    const map = new Map<string, Prompt[]>()
    for (const prompt of filtered) {
      const groupName = prompt.group_name || "默认分组"
      const bucket = map.get(groupName) || []
      bucket.push(prompt)
      map.set(groupName, bucket)
    }
    return Array.from(map.entries()).map(([groupName, items]) => ({ groupName, items }))
  }, [filtered])

  useEffect(() => {
    // Each open/account pair owns both catalog sources. Cleanup advances the
    // generation so a late source cannot replace a later window's state.
    const requestID = loadRequestRef.current + 1
    loadRequestRef.current = requestID
    if (!open) return
    void Promise.resolve().then(() => {
      if (requestID !== loadRequestRef.current) return
      setPrompts([])
      setSelected(null)
      setMobilePreviewOpen(false)
      setLoading(true)
      setError("")
      void Promise.all([promptsApi.listAllMine(), promptsApi.listAllPublic()])
        .then(([mine, pub]) => {
          if (requestID !== loadRequestRef.current) return
          const map = new Map<number, Prompt>()
          for (const item of [...mine, ...pub]) map.set(item.id, item)
          const list = Array.from(map.values())
          setPrompts(list)
          setSelected(list[0] || null)
        })
        .catch((err) => {
          if (requestID === loadRequestRef.current) {
            setError(err instanceof Error ? err.message : "加载提示词失败")
          }
        })
        .finally(() => {
          if (requestID === loadRequestRef.current) setLoading(false)
        })
    })
    return () => {
      if (requestID === loadRequestRef.current) loadRequestRef.current += 1
    }
  }, [open, userID])

  async function applyPrompt(content: string | null) {
    if (!activeSessionId) return
    setSaving(true)
    try {
      await sessionsApi.updateSession(activeSessionId, { system_prompt: content || "" })
      updateSessionLocal(activeSessionId, { system_prompt: content || undefined })
      onOpenChange(false)
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex h-[calc(100dvh-1rem)] max-h-[calc(100dvh-1rem)] max-w-[calc(100vw-1rem)] flex-col gap-0 overflow-hidden p-0 md:h-auto md:max-h-none md:max-w-[880px] md:rounded-lg">
        <DialogHeader className="shrink-0 border-b border-border py-4 pl-4 pr-14 md:px-5">
          <DialogTitle>选择系统提示词</DialogTitle>
          <DialogDescription className="sr-only">搜索、预览并为当前会话选择系统提示词。</DialogDescription>
        </DialogHeader>
        <div className="flex min-h-0 flex-1 md:grid md:h-[560px] md:flex-none md:grid-cols-[260px_minmax(0,1fr)]">
          <div className={cn("min-h-0 flex-1 flex-col md:border-r md:border-border", mobilePreviewOpen ? "hidden md:flex" : "flex")}>
            <div className="shrink-0 border-b border-border p-3">
              <div className="relative">
                <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
                <input
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  placeholder="搜索提示词"
                  aria-label="搜索提示词"
                  className="h-11 w-full rounded-md border border-border bg-background pl-9 pr-3 text-sm outline-none focus:border-foreground focus-visible:ring-2 focus-visible:ring-ring/50 md:h-9"
                />
              </div>
            </div>
            <div className="min-h-0 flex-1 overflow-y-auto p-2">
              {loading && <div className="px-2 py-2 text-xs text-muted-foreground">正在加载完整提示词列表…</div>}
              {error && <div role="alert" className="px-2 py-2 text-xs text-destructive">{error}</div>}
              <div className="space-y-2">
                {grouped.map((group) => (
                  <section key={group.groupName} className="border-b border-border/60 pb-1 last:border-b-0">
                    <div className="px-1 py-1 text-xs font-medium text-muted-foreground">
                      {group.groupName}
                    </div>
                    {group.items.map((prompt) => (
                      <button
                        key={prompt.id}
                        onClick={() => {
                          setSelected(prompt)
                          setMobilePreviewOpen(true)
                        }}
                        className={`w-full rounded-md px-3 py-2 text-left transition-colors motion-control focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/50 ${
                          selected?.id === prompt.id ? "bg-muted text-foreground" : "hover:bg-muted"
                        }`}
                      >
                        <div className="truncate text-sm font-medium">{prompt.title}</div>
                        <div className="mt-1 truncate text-xs opacity-60">{prompt.is_public ? "系统提示词" : "我的提示词"}</div>
                      </button>
                    ))}
                  </section>
                ))}
              </div>
            </div>
            <div className="flex shrink-0 justify-start border-t border-border px-4 py-3 md:hidden">
              <Button className="h-11" variant="ghost" size="sm" onClick={() => applyPrompt(null)} disabled={saving || !activeSessionId}>
                清空
              </Button>
            </div>
          </div>
          <div className={cn("min-h-0 min-w-0 flex-1 flex-col", mobilePreviewOpen ? "flex" : "hidden md:flex")}>
            <div className="min-h-0 flex-1 overflow-y-auto p-4 md:p-5">
              <MotionView viewKey={selected?.id || "empty"} className="min-h-full">
                {selected ? (
                  <>
                    <div className="mb-3 flex min-w-0 items-start gap-1 md:items-center md:gap-2">
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        className="-ml-2 h-11 w-11 shrink-0 md:hidden"
                        aria-label="返回提示词列表"
                        onClick={() => setMobilePreviewOpen(false)}
                      >
                        <ArrowLeft className="h-4 w-4" aria-hidden="true" />
                      </Button>
                      <div className="min-w-0 flex-1 md:flex md:items-center md:gap-2">
                        <h3 className="break-words text-base font-semibold md:truncate">{selected.title}</h3>
                        <div className="mt-2 flex flex-wrap items-center gap-2 md:mt-0 md:flex-nowrap">
                          <span className="rounded-md border px-2 py-0.5 text-xs text-muted-foreground">{selected.group_name || "默认分组"}</span>
                          {activeSession?.system_prompt === selected.content && (
                            <span className="inline-flex items-center gap-1 rounded-md border border-emerald-500/30 px-2 py-0.5 text-xs text-emerald-600">
                              <Check className="h-3 w-3" />
                              已使用
                            </span>
                          )}
                        </div>
                      </div>
                    </div>
                    <pre className="whitespace-pre-wrap rounded-md border border-border bg-muted/30 p-4 text-sm leading-7">{selected.content}</pre>
                  </>
                ) : (
                  <div className="flex h-full items-center justify-center text-sm text-muted-foreground">暂无提示词</div>
                )}
              </MotionView>
            </div>
            <div className="flex shrink-0 items-center justify-between border-t border-border px-4 py-3 md:px-5 md:py-4">
              <Button className="h-11 md:h-8" variant="ghost" size="sm" onClick={() => applyPrompt(null)} disabled={saving || !activeSessionId}>
                清空
              </Button>
              <Button className="h-11 md:h-8" size="sm" onClick={() => selected && applyPrompt(selected.content)} disabled={saving || !selected || !activeSessionId}>
                {saving ? "应用中" : "应用"}
              </Button>
            </div>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
