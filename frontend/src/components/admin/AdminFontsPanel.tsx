import { useRef, useState } from "react"
import { adminApi, type ChatFontSelection } from "@/api/admin"
import { useSystemStore } from "@/stores/system"
import type { FontAsset } from "@/types"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Code2, Languages, LetterText, RotateCcw, Trash2, Upload } from "lucide-react"

interface Props {
  fonts: FontAsset[]
  selectedFontIds: ChatFontSelection
  setFonts: React.Dispatch<React.SetStateAction<FontAsset[]>>
  setSelectedFontIds: React.Dispatch<React.SetStateAction<ChatFontSelection>>
  setError: (error: string) => void
}

type FontSlot = keyof ChatFontSelection

const fontSlots: Array<{ key: FontSlot; label: string; icon: React.ReactNode }> = [
  { key: "chinese", label: "中文字体", icon: <Languages className="h-3.5 w-3.5" /> },
  { key: "latin", label: "英文字体", icon: <LetterText className="h-3.5 w-3.5" /> },
  { key: "code", label: "代码字体", icon: <Code2 className="h-3.5 w-3.5" /> },
]

export function AdminFontsPanel({ fonts, selectedFontIds, setFonts, setSelectedFontIds, setError }: Props) {
  const reloadSystem = useSystemStore((s) => s.load)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [file, setFile] = useState<File | null>(null)
  const [displayName, setDisplayName] = useState("")
  const [saving, setSaving] = useState("")

  function pickFile(nextFile?: File) {
    if (!nextFile) return
    setFile(nextFile)
    const baseName = nextFile.name.replace(/\.[^.]+$/, "")
    setDisplayName((current) => current || baseName)
  }

  async function uploadFont() {
    if (!file) return
    setSaving("upload")
    setError("")
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
      setError(err instanceof Error ? err.message : "字体上传失败")
    } finally {
      setSaving("")
    }
  }

  async function selectFont(slot: FontSlot, font: FontAsset | null) {
    setSaving(font ? `select-${slot}-${font.id}` : `reset-${slot}`)
    setError("")
    try {
      const res = await adminApi.selectFont(font?.id ?? null, slot)
      if (res.selected_font_ids) {
        setSelectedFontIds(res.selected_font_ids)
      } else {
        setSelectedFontIds((prev) => ({ ...prev, [slot]: font?.id ?? null }))
      }
      void reloadSystem()
    } catch (err) {
      setError(err instanceof Error ? err.message : "字体设置失败")
    } finally {
      setSaving("")
    }
  }

  async function toggleFont(font: FontAsset) {
    setSaving(`toggle-${font.id}`)
    setError("")
    try {
      const updated = await adminApi.updateFont(font.id, { enabled: !font.enabled })
      setFonts((prev) => prev.map((item) => (item.id === updated.id ? updated : item)))
      if (!updated.enabled && isSelectedFont(updated.id, selectedFontIds)) {
        setSelectedFontIds((prev) => clearFontFromSelection(prev, updated.id))
        void reloadSystem()
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "字体状态更新失败")
    } finally {
      setSaving("")
    }
  }

  async function deleteFont(font: FontAsset) {
    const selectedSlots = fontSlots.filter((slot) => selectedFontIds[slot.key] === font.id).map((slot) => slot.label)
    const slotText = selectedSlots.length > 0 ? `\n\n它正在用于：${selectedSlots.join("、")}。删除后这些槽位会恢复系统默认。` : ""
    if (!window.confirm(`删除字体「${font.display_name}」？字体文件也会从上传目录移除。${slotText}`)) return
    setSaving(`delete-${font.id}`)
    setError("")
    try {
      await adminApi.deleteFont(font.id)
      setFonts((prev) => prev.filter((item) => item.id !== font.id))
      if (isSelectedFont(font.id, selectedFontIds)) {
        setSelectedFontIds((prev) => clearFontFromSelection(prev, font.id))
        void reloadSystem()
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "字体删除失败")
    } finally {
      setSaving("")
    }
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
        <Button size="sm" className="h-8" onClick={uploadFont} disabled={!file || saving === "upload"}>
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
                disabled={saving.startsWith(`select-${slot.key}`) || saving === `reset-${slot.key}`}
              >
                <option value="">系统默认</option>
                {fonts.filter((font) => font.enabled).map((font) => (
                  <option key={font.id} value={font.id}>{font.display_name}</option>
                ))}
              </select>
              <Button size="icon" variant="ghost" className="h-8 w-8" onClick={() => void selectFont(slot.key, null)} disabled={!selected || saving === `reset-${slot.key}`} aria-label={`恢复${slot.label}系统字体`} title="恢复系统字体">
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
                  {selectedSlots.map((slot) => <span key={slot} className="rounded-sm bg-foreground px-1.5 py-0.5 text-[11px] text-background">{slot}</span>)}
                </div>
                <div className="mt-0.5 text-sm text-muted-foreground">{selectedSlots.length > 0 ? `用于 ${selectedSlots.join("、")}` : "未用于槽位"}</div>
              </div>
              <button
                type="button"
                onClick={() => void toggleFont(font)}
                disabled={saving === `toggle-${font.id}`}
                className="group inline-flex h-7 w-12 items-center rounded-full border border-border bg-muted px-0.5 transition-colors motion-control hover:border-foreground/30 disabled:cursor-not-allowed disabled:opacity-60 data-[enabled=true]:bg-foreground"
                data-enabled={font.enabled}
                aria-label={font.enabled ? "停用字体" : "启用字体"}
                title={font.enabled ? "已启用" : "已停用"}
              >
                <span className="h-5 w-5 rounded-full bg-background shadow-sm transition-transform motion-control group-data-[enabled=true]:translate-x-5" />
              </button>
              <div className="flex justify-end">
                <Button variant="ghost" size="icon" className="h-8 w-8 text-destructive hover:text-destructive" onClick={() => void deleteFont(font)} disabled={saving === `delete-${font.id}`} aria-label={`删除字体：${font.display_name}`} title="删除">
                  <Trash2 className="h-3.5 w-3.5" aria-hidden="true" />
                </Button>
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}

function findFont(fonts: FontAsset[], id?: number | null) {
  if (!id) return null
  return fonts.find((font) => font.id === id) || null
}

function isSelectedFont(id: number, selection: ChatFontSelection) {
  return selection.chinese === id || selection.latin === id || selection.code === id
}

function clearFontFromSelection(selection: ChatFontSelection, id: number): ChatFontSelection {
  return {
    chinese: selection.chinese === id ? null : selection.chinese,
    latin: selection.latin === id ? null : selection.latin,
    code: selection.code === id ? null : selection.code,
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
