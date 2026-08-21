import { useRef, useState, type MouseEvent } from "react"
import { adminApi, type ChatFontSelection } from "@/api/admin"
import { useSystemStore } from "@/stores/system"
import type { FontAsset } from "@/types"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { FontActionOwnership, FontSlotOwnership, type FontSlot, type FontSlotGenerations } from "@/components/admin/fontSlotOwnership"
import { Code2, Languages, LetterText, RotateCcw, Trash2, Upload } from "lucide-react"

interface Props {
  fonts: FontAsset[]
  selectedFontIds: ChatFontSelection
  setFonts: React.Dispatch<React.SetStateAction<FontAsset[]>>
  setSelectedFontIds: React.Dispatch<React.SetStateAction<ChatFontSelection>>
  setError: (error: string) => void
}

const fontSlots: Array<{ key: FontSlot; label: string; icon: React.ReactNode }> = [
  { key: "chinese", label: "中文字体", icon: <Languages className="h-3.5 w-3.5" /> },
  { key: "latin", label: "英文字体", icon: <LetterText className="h-3.5 w-3.5" /> },
  { key: "code", label: "代码字体", icon: <Code2 className="h-3.5 w-3.5" /> },
]

export function AdminFontsPanel({ fonts, selectedFontIds, setFonts, setSelectedFontIds, setError }: Props) {
  const reloadSystem = useSystemStore((s) => s.load)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const slotOwnershipRef = useRef(new FontSlotOwnership())
  const actionOwnershipRef = useRef(new FontActionOwnership())
  const canonicalReloadRef = useRef(0)
  const errorOwnerRef = useRef(0)
  const [file, setFile] = useState<File | null>(null)
  const [displayName, setDisplayName] = useState("")
  const [busyActions, setBusyActions] = useState<Set<string>>(() => new Set())
  const [pendingSlots, setPendingSlots] = useState<Partial<Record<FontSlot, number>>>({})
  const [pendingDelete, setPendingDelete] = useState<FontAsset | null>(null)
  const deleteTriggerRef = useRef<HTMLButtonElement | null>(null)

  function beginErrorOwner() {
    errorOwnerRef.current += 1
    setError("")
    return errorOwnerRef.current
  }

  function setOwnedError(owner: number, error: unknown, fallback: string) {
    if (errorOwnerRef.current === owner) setError(error instanceof Error ? error.message : fallback)
  }

  function setActionBusy(action: string, busy: boolean) {
    setBusyActions((current) => {
      const next = new Set(current)
      if (busy) next.add(action)
      else next.delete(action)
      return next
    })
  }

  function mergeOwnedSelection(selection: ChatFontSelection, generations: FontSlotGenerations) {
    setSelectedFontIds((current) => {
      const next = { ...current }
      for (const slot of fontSlots) {
        if (slotOwnershipRef.current.owns(slot.key, generations[slot.key])) {
          next[slot.key] = selection[slot.key] ?? null
        }
      }
      return next
    })
  }

  async function reloadCanonical(generations: FontSlotGenerations) {
    const reloadGeneration = ++canonicalReloadRef.current
    const result = await adminApi.listFonts()
    if (canonicalReloadRef.current !== reloadGeneration) return
    setFonts(result.fonts || [])
    mergeOwnedSelection(normalizeSelection(result), generations)
  }

  function pickFile(nextFile?: File) {
    if (!nextFile) return
    setFile(nextFile)
    const baseName = nextFile.name.replace(/\.[^.]+$/, "")
    setDisplayName((current) => current || baseName)
  }

  async function uploadFont() {
    if (!file) return
    const errorOwner = beginErrorOwner()
    const actionGeneration = actionOwnershipRef.current.begin("upload")
    setActionBusy("upload", true)
    try {
      const uploaded = await adminApi.uploadFont(file, {
        display_name: displayName.trim() || undefined,
        weight: 400,
        style: "normal",
      })
      setFonts((prev) => [uploaded, ...prev.filter((item) => item.id !== uploaded.id)])
      setFile(null)
      setDisplayName("")
      if (fileInputRef.current) fileInputRef.current.value = ""
    } catch (err) {
      setOwnedError(errorOwner, err, "字体上传失败")
    } finally {
      if (actionOwnershipRef.current.owns("upload", actionGeneration)) setActionBusy("upload", false)
    }
  }

  async function selectFont(slot: FontSlot, font: FontAsset | null) {
    const generation = slotOwnershipRef.current.begin(slot)
    const errorOwner = beginErrorOwner()
    setPendingSlots((current) => ({ ...current, [slot]: generation }))
    try {
      const res = await adminApi.selectFont(font?.id ?? null, slot)
      if (slotOwnershipRef.current.owns(slot, generation)) {
        const selectedID = res.selected_font_ids ? res.selected_font_ids[slot] ?? null : font?.id ?? null
        setSelectedFontIds((prev) => ({ ...prev, [slot]: selectedID }))
        void reloadSystem()
      }
    } catch (err) {
      try {
        const result = await adminApi.listFonts()
        if (slotOwnershipRef.current.owns(slot, generation)) {
          setFonts(result.fonts || [])
          const canonical = normalizeSelection(result)
          setSelectedFontIds((current) => ({ ...current, [slot]: canonical[slot] ?? null }))
          void reloadSystem()
        }
      } catch {
        // Preserve the original mutation error; the next Admin reload remains canonical.
      }
      if (slotOwnershipRef.current.owns(slot, generation)) setOwnedError(errorOwner, err, "字体设置失败")
    } finally {
      if (slotOwnershipRef.current.owns(slot, generation)) {
        setPendingSlots((current) => {
          const next = { ...current }
          delete next[slot]
          return next
        })
      }
    }
  }

  async function toggleFont(font: FontAsset) {
    const action = `toggle-${font.id}`
    const errorOwner = beginErrorOwner()
    const actionGeneration = actionOwnershipRef.current.begin(action)
    const generations = font.enabled ? slotOwnershipRef.current.invalidateAll() : null
    if (generations) setPendingSlots({})
    setActionBusy(action, true)
    try {
      const result = await adminApi.updateFont(font.id, { enabled: !font.enabled })
      const updated = result.font
      setFonts((prev) => prev.map((item) => (item.id === updated.id ? updated : item)))
      if (generations) {
        mergeOwnedSelection(result.selected_font_ids, generations)
        void reloadSystem()
      }
    } catch (err) {
      if (generations) {
        try {
          await reloadCanonical(generations)
          void reloadSystem()
        } catch {
          // Preserve the mutation error; a later Admin reload will reconcile state.
        }
      }
      setOwnedError(errorOwner, err, "字体状态更新失败")
    } finally {
      if (actionOwnershipRef.current.owns(action, actionGeneration)) setActionBusy(action, false)
    }
  }

  async function deleteFont(font: FontAsset) {
    const action = `delete-${font.id}`
    const errorOwner = beginErrorOwner()
    const actionGeneration = actionOwnershipRef.current.begin(action)
    const generations = slotOwnershipRef.current.invalidateAll()
    setPendingSlots({})
    setActionBusy(action, true)
    try {
      const result = await adminApi.deleteFont(font.id)
      setFonts((prev) => prev.filter((item) => item.id !== font.id))
      mergeOwnedSelection(result.selected_font_ids, generations)
      void reloadSystem()
    } catch (err) {
      try {
        await reloadCanonical(generations)
        void reloadSystem()
      } catch {
        // Preserve the mutation error; a later Admin reload will reconcile state.
      }
      setOwnedError(errorOwner, err, "字体删除失败")
    } finally {
      if (actionOwnershipRef.current.owns(action, actionGeneration)) setActionBusy(action, false)
    }
  }

  function requestDelete(font: FontAsset, event: MouseEvent<HTMLButtonElement>) {
    deleteTriggerRef.current = event.currentTarget
    setPendingDelete(font)
  }

  return (
    <div className="flex h-full min-h-0 flex-col overflow-hidden">
      <div className="grid gap-3 border-b border-border/70 px-4 py-3 lg:grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)_auto] lg:items-end">
        <div className="min-w-0">
          <div className="mb-1.5 text-sm font-medium">字体文件</div>
          <input
            ref={fileInputRef}
            type="file"
            accept=".woff2,.woff,.ttf,.otf,font/woff2,font/woff,font/ttf,font/otf"
            className="hidden"
            onChange={(e) => pickFile(e.target.files?.[0])}
          />
          <button
            type="button"
            onClick={() => fileInputRef.current?.click()}
            className="flex h-8 w-full items-center gap-2 rounded-md border border-input bg-background px-3 text-left text-sm transition-colors motion-control hover:bg-muted"
          >
            <Upload className="h-3.5 w-3.5 shrink-0" />
            <span className="min-w-0 flex-1 truncate">{file ? file.name : "选择 .woff2 / .woff / .ttf / .otf"}</span>
          </button>
        </div>
        <Field label="显示名称">
          <Input value={displayName} onChange={(e) => setDisplayName(e.target.value)} placeholder="例如：霞鹜文楷" className="h-8" />
        </Field>
        <Button size="sm" className="h-8" onClick={uploadFont} disabled={!file || busyActions.has("upload")}>
          <Upload className="h-3.5 w-3.5" />
          上传
        </Button>
      </div>

      <div className="grid gap-2 border-b border-border/70 px-4 py-3 lg:grid-cols-3">
        {fontSlots.map((slot) => {
          const selected = findFont(fonts, selectedFontIds[slot.key])
          return (
            <div key={slot.key} className="flex min-w-0 items-center gap-2">
              <div className="flex min-w-[72px] items-center gap-1.5 text-sm font-medium">
                {slot.icon}
                {slot.label}
              </div>
              <select
                className="h-8 min-w-0 flex-1 rounded-md border border-input bg-background px-2 text-sm"
                value={selected?.id ?? ""}
                onChange={(e) => void selectFont(slot.key, findFont(fonts, Number(e.target.value)) || null)}
                disabled={pendingSlots[slot.key] !== undefined}
              >
                <option value="">系统默认</option>
                {fonts.filter((font) => font.enabled).map((font) => (
                  <option key={font.id} value={font.id}>{font.display_name}</option>
                ))}
              </select>
              <Button size="icon" variant="ghost" className="h-8 w-8" onClick={() => void selectFont(slot.key, null)} disabled={!selected || pendingSlots[slot.key] !== undefined} aria-label={`恢复${slot.label}系统字体`} title="恢复系统字体">
                <RotateCcw className="h-3.5 w-3.5" aria-hidden="true" />
              </Button>
            </div>
          )
        })}
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto scrollbar-thin">
        {fonts.length === 0 ? (
          <div className="flex h-full items-center justify-center text-sm text-muted-foreground">暂无字体</div>
        ) : fonts.map((font) => {
          const selectedSlots = fontSlots.filter((slot) => selectedFontIds[slot.key] === font.id).map((slot) => slot.label)
          return (
            <div key={font.id} className="grid gap-3 border-b border-border/60 px-4 py-3 last:border-b-0 sm:grid-cols-[minmax(0,1fr)_auto_auto] sm:items-center">
              <div className="min-w-0">
                <div className="flex min-w-0 flex-wrap items-center gap-2">
                  <span className="truncate font-medium">{font.display_name}</span>
                  {selectedSlots.map((slot) => <span key={slot} className="rounded-sm bg-foreground px-1.5 py-0.5 text-xs text-background">{slot}</span>)}
                </div>
                <div className="mt-0.5 text-sm text-muted-foreground">{selectedSlots.length > 0 ? `用于 ${selectedSlots.join("、")}` : "未用于槽位"}</div>
              </div>
              <button
                type="button"
                onClick={() => void toggleFont(font)}
                disabled={busyActions.has(`toggle-${font.id}`)}
                className="group inline-flex h-7 w-12 items-center rounded-full border border-border bg-muted px-0.5 transition-colors motion-control hover:border-foreground/30 disabled:cursor-not-allowed disabled:opacity-60 data-[enabled=true]:bg-foreground"
                data-enabled={font.enabled}
                aria-label={font.enabled ? "停用字体" : "启用字体"}
                title={font.enabled ? "已启用" : "已停用"}
              >
                <span className="h-5 w-5 rounded-full bg-background shadow-sm transition-transform motion-control group-data-[enabled=true]:translate-x-5" />
              </button>
              <div className="flex justify-end">
                <Button ref={deleteTriggerRef} variant="ghost" size="icon" className="h-8 w-8 text-destructive hover:text-destructive" onClick={(event) => requestDelete(font, event)} disabled={busyActions.has(`delete-${font.id}`)} aria-label={`删除字体：${font.display_name}`} title="删除">
                  <Trash2 className="h-3.5 w-3.5" aria-hidden="true" />
                </Button>
              </div>
            </div>
          )
        })}
      </div>
      <Dialog open={!!pendingDelete} onOpenChange={(nextOpen) => !nextOpen && setPendingDelete(null)}>
        <DialogContent
          className="max-w-[calc(100vw-1.5rem)] sm:max-w-md"
          onCloseAutoFocus={(event) => {
            const trigger = deleteTriggerRef.current
            if (!trigger?.isConnected) return
            event.preventDefault()
            trigger.focus()
          }}
        >
          <DialogHeader>
            <DialogTitle>删除字体？</DialogTitle>
            <DialogDescription>
              “{pendingDelete?.display_name}”的字体文件会从上传目录永久移除，无法恢复。
              {pendingDelete && fontSlots.some((slot) => selectedFontIds[slot.key] === pendingDelete.id)
                ? ` 当前用于：${fontSlots.filter((slot) => selectedFontIds[slot.key] === pendingDelete.id).map((slot) => slot.label).join("、")}；删除后这些槽位会恢复系统默认。`
                : ""}
            </DialogDescription>
          </DialogHeader>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="secondary" onClick={() => setPendingDelete(null)}>取消</Button>
            <Button type="button" variant="destructive" onClick={() => {
              const font = pendingDelete
              setPendingDelete(null)
              if (font) void deleteFont(font)
            }}>删除字体</Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  )
}

function findFont(fonts: FontAsset[], id?: number | null) {
  if (!id) return null
  return fonts.find((font) => font.id === id) || null
}

function normalizeSelection(result: Awaited<ReturnType<typeof adminApi.listFonts>>): ChatFontSelection {
  return result.selected_font_ids || {
    chinese: result.selected_font_id ?? null,
    latin: result.selected_font_id ?? null,
    code: result.selected_font_id ?? null,
  }
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="block">
      <span className="mb-1.5 block text-sm font-medium">{label}</span>
      {children}
    </label>
  )
}
