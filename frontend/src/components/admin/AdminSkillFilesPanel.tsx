import { FileText, Plus, Trash2 } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import type { SkillDraft } from "./AdminSkillsPanel.types"

interface AdminSkillFilesPanelProps {
  draft: SkillDraft | null
  activePath: string
  className: string
  emptyText: string
  onAddReference: () => void
  onSelectPath: (path: string) => void
  onRenameReference: (oldPath: string, path: string) => void
  onRemoveReference: (path: string) => void
}

export function AdminSkillFilesPanel({
  draft,
  activePath,
  className,
  emptyText,
  onAddReference,
  onSelectPath,
  onRenameReference,
  onRemoveReference,
}: AdminSkillFilesPanelProps) {
  return (
    <div className={className}>
      <div className="flex items-center justify-between border-b border-border/70 px-3 py-2.5">
        <div className="text-sm font-medium">文件包</div>
        {draft && (
          <Button size="sm" variant="outline" onClick={onAddReference}>
            <Plus className="h-3.5 w-3.5" />
            reference
          </Button>
        )}
      </div>
      {draft ? (
        <div className="min-h-0 flex-1 overflow-y-auto p-3 scrollbar-thin">
          <FileButton active={activePath === "SKILL.md"} path="SKILL.md" kind="entry" onClick={() => onSelectPath("SKILL.md")} />
          <div className="mt-3 grid gap-2">
            {draft.files.map((file) => (
              <div key={file.path} className={`rounded-md border p-2 ${activePath === file.path ? "border-foreground/40 bg-muted/60" : "border-border/70"}`}>
                <button className="mb-2 flex w-full items-center gap-2 text-left text-xs font-medium" onClick={() => onSelectPath(file.path)}>
                  <FileText className="h-3.5 w-3.5 shrink-0" />
                  <span className="truncate">{file.path}</span>
                </button>
                <div className="flex gap-1">
                  <Input value={file.path} onChange={(e) => onRenameReference(file.path, e.target.value)} className="h-7 min-w-0 text-xs" />
                  <Button variant="ghost" size="icon" className="h-7 w-7 text-destructive hover:text-destructive" onClick={() => onRemoveReference(file.path)} aria-label={`删除 Skill 文件：${file.path}`}>
                    <Trash2 className="h-3.5 w-3.5" aria-hidden="true" />
                  </Button>
                </div>
              </div>
            ))}
          </div>
        </div>
      ) : (
        <div className="flex flex-1 items-center justify-center px-4 text-center text-sm text-muted-foreground">{emptyText}</div>
      )}
    </div>
  )
}

function FileButton({ active, path, kind, onClick }: { active: boolean; path: string; kind: string; onClick: () => void }) {
  return (
    <button
      className={`flex w-full items-center gap-2 rounded-md border px-2 py-2 text-left text-xs transition-colors ${
        active ? "border-foreground/40 bg-muted/70" : "border-border/70 hover:bg-muted/60"
      }`}
      onClick={onClick}
    >
      <FileText className="h-3.5 w-3.5 shrink-0" />
      <span className="min-w-0 flex-1 truncate">{path}</span>
      <span className="text-xs text-muted-foreground">{kind}</span>
    </button>
  )
}
