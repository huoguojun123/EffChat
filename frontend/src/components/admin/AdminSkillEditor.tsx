import type { Dispatch, ReactNode, RefObject, SetStateAction } from "react"
import { ArrowLeft, RefreshCw, Save, Upload, X } from "lucide-react"
import type { SkillDefinition, UserGroup } from "@/types"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { MotionView } from "@/components/ui/motion"
import type { SkillDraft } from "./AdminSkillsPanel.types"
import { AdminSkillFilesPanel } from "./AdminSkillFilesPanel"

interface AdminSkillEditorProps {
  draft: SkillDraft | null
  setDraft: Dispatch<SetStateAction<SkillDraft | null>>
  activePath: string
  activeContent: string
  mobileDetailOpen: boolean
  mobilePane: "editor" | "files"
  sortedGroups: UserGroup[]
  editingSkill?: SkillDefinition
  saving: string
  updateZipInputRef: RefObject<HTMLInputElement | null>
  onBack: () => void
  onClose: () => void
  onMobilePaneChange: (pane: "editor" | "files") => void
  onPreviewGitUpdate: (skill: SkillDefinition) => void
  onPreviewZipUpdate: (skill: SkillDefinition, file?: File) => void
  onSetReferenceContent: (path: string, content: string) => void
  onSelectPath: (path: string) => void
  onSelectPathAndEdit: (path: string) => void
  onAddReference: () => void
  onRenameReference: (oldPath: string, path: string) => void
  onRemoveReference: (path: string) => void
  onSave: () => void
}

export function AdminSkillEditor({
  draft,
  setDraft,
  activePath,
  activeContent,
  mobileDetailOpen,
  mobilePane,
  sortedGroups,
  editingSkill,
  saving,
  updateZipInputRef,
  onBack,
  onClose,
  onMobilePaneChange,
  onPreviewGitUpdate,
  onPreviewZipUpdate,
  onSetReferenceContent,
  onSelectPathAndEdit,
  onAddReference,
  onRenameReference,
  onRemoveReference,
  onSave,
}: AdminSkillEditorProps) {
  return (
    <div className={`${mobileDetailOpen ? "flex" : "hidden xl:flex"} min-h-0 flex-col overflow-hidden`}>
      <div className="flex items-center justify-between border-b border-border/70 px-4 py-3">
        <div className="flex min-w-0 items-center gap-2">
          <Button variant="ghost" size="sm" className="h-8 px-2 xl:hidden" onClick={onBack}>
            <ArrowLeft className="h-3.5 w-3.5" />
            返回
          </Button>
          <div className="truncate text-sm font-medium">{draft ? (draft.originalId ? "编辑 Skill" : "新建 Skill") : "Skill 详情"}</div>
        </div>
        {draft && (
          <div className="flex min-w-0 items-center gap-2">
            {editingSkill?.source_type === "git" && (
              <Button variant="outline" size="sm" className="hidden h-8 px-3 xl:inline-flex" onClick={() => onPreviewGitUpdate(editingSkill)} disabled={saving !== ""} title="检查更新">
                <RefreshCw className="h-3.5 w-3.5" />
                检查更新
              </Button>
            )}
            {editingSkill?.source_type === "zip" && (
              <>
                <input
                  ref={updateZipInputRef}
                  type="file"
                  accept=".zip,application/zip"
                  className="hidden"
                  onChange={(e) => onPreviewZipUpdate(editingSkill, e.target.files?.[0])}
                />
                <Button variant="outline" size="sm" className="hidden h-8 px-3 xl:inline-flex" onClick={() => updateZipInputRef.current?.click()} disabled={saving !== ""} title="上传新版">
                  <Upload className="h-3.5 w-3.5" />
                  上传新版
                </Button>
              </>
            )}
            <label className="flex items-center gap-2 text-sm text-muted-foreground">
              <input
                type="checkbox"
                checked={draft.enabled ?? true}
                onChange={(e) => setDraft((prev) => prev ? { ...prev, enabled: e.target.checked } : prev)}
              />
              启用
            </label>
            <Button variant="ghost" size="sm" onClick={onClose}>
              <X className="h-3.5 w-3.5" />
            </Button>
          </div>
        )}
      </div>
      <MotionView viewKey={draft ? draft.originalId || "new" : "empty"} className="flex min-h-0 flex-1 flex-col">
        {draft ? (
          <>
            <div className="grid grid-cols-2 gap-1 border-b border-border/70 p-2 xl:hidden">
              <button
                type="button"
                data-active={mobilePane === "editor"}
                onClick={() => onMobilePaneChange("editor")}
                className="h-8 rounded-md text-sm transition-colors motion-control data-[active=true]:bg-foreground data-[active=true]:text-background data-[active=false]:text-muted-foreground"
              >
                编辑
              </button>
              <button
                type="button"
                data-active={mobilePane === "files"}
                onClick={() => onMobilePaneChange("files")}
                className="h-8 rounded-md text-sm transition-colors motion-control data-[active=true]:bg-foreground data-[active=true]:text-background data-[active=false]:text-muted-foreground"
              >
                文件包
              </button>
            </div>
            <div className={`${mobilePane === "files" ? "hidden xl:grid" : "grid"} gap-3 border-b border-border/70 p-4 lg:grid-cols-[minmax(10rem,0.6fr)_minmax(12rem,1fr)_minmax(12rem,0.8fr)]`}>
              <Field label="ID">
                <Input value={draft.id || ""} onChange={(e) => setDraft((prev) => prev ? { ...prev, id: e.target.value } : prev)} disabled={!!draft.originalId} />
              </Field>
              <Field label="名称">
                <Input value={draft.name} onChange={(e) => setDraft((prev) => prev ? { ...prev, name: e.target.value } : prev)} />
              </Field>
              <Field label="最低分级组">
                <select
                  value={draft.min_group_level ?? 0}
                  onChange={(e) => setDraft((prev) => prev ? { ...prev, min_group_level: Number(e.target.value) } : prev)}
                  className="w-full rounded-md border border-input bg-background px-2 text-sm"
                >
                  <option value={0}>所有人 · L0</option>
                  {sortedGroups.map((group) => (
                    <option key={group.id} value={group.level}>{group.name} · L{group.level}</option>
                  ))}
                </select>
              </Field>
              <Field label="描述" className="lg:col-span-3">
                <Input value={draft.description || ""} onChange={(e) => setDraft((prev) => prev ? { ...prev, description: e.target.value } : prev)} />
              </Field>
            </div>
            <div className={`${mobilePane === "files" ? "hidden xl:flex" : "flex"} min-h-0 flex-1 flex-col p-4`}>
              <div className="mb-2 flex items-center justify-between">
                <div className="text-sm font-medium">{activePath}</div>
                {saving.startsWith("load-") && <span className="text-xs text-muted-foreground">加载文件中…</span>}
              </div>
              <textarea
                value={activeContent}
                onChange={(e) => {
                  if (activePath === "SKILL.md") {
                    setDraft((prev) => prev ? { ...prev, entry_content: e.target.value } : prev)
                  } else {
                    onSetReferenceContent(activePath, e.target.value)
                  }
                }}
                className="min-h-[320px] flex-1 resize-none rounded-md border border-input bg-background px-3 py-2 font-mono text-sm leading-5 outline-none focus:border-ring/50"
              />
            </div>
            <AdminSkillFilesPanel
              draft={draft}
              activePath={activePath}
              className={`${mobilePane === "files" ? "flex xl:hidden" : "hidden"} min-h-0 flex-1 flex-col overflow-hidden`}
              emptyText="选择 Skill 后查看 SKILL.md 与 references。"
              onAddReference={onAddReference}
              onSelectPath={onSelectPathAndEdit}
              onRenameReference={onRenameReference}
              onRemoveReference={onRemoveReference}
            />
            <div className={`${mobilePane === "files" ? "hidden xl:flex" : "flex"} justify-end border-t border-border/70 px-4 py-3`}>
              <Button size="sm" onClick={onSave} disabled={saving !== "" || !draft.name.trim() || !draft.entry_content.trim()}>
                <Save className="h-3.5 w-3.5" />
                保存
              </Button>
            </div>
          </>
        ) : (
          <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">左侧选择 Skill</div>
        )}
      </MotionView>
    </div>
  )
}

function Field({ label, children, className = "" }: { label: string; children: ReactNode; className?: string }) {
  return (
    <label className={`block [&_input]:h-8 [&_select]:h-8 ${className}`}>
      <span className="mb-1.5 block text-sm font-medium">{label}</span>
      {children}
    </label>
  )
}
