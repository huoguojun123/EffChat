import { useState } from "react"
import { History, Plus, Search, Trash2 } from "lucide-react"
import type { SkillDefinition } from "@/types"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { sourceLabel } from "./AdminSkillsPanel.helpers"
import { AdminSkillHistoryPanel } from "./AdminSkillHistoryPanel"

interface AdminSkillsLibraryProps {
  skills: SkillDefinition[]
  query: string
  saving: string
  mobileDetailOpen: boolean
  onQueryChange: (value: string) => void
  onCreate: () => void
  onEdit: (skill: SkillDefinition) => void
  onToggle: (skill: SkillDefinition) => void
  onDelete: (skill: SkillDefinition) => void
  onRollback: (skillID: string, restored: SkillDefinition | null) => void
  setError: (error: string) => void
}

export function AdminSkillsLibrary({
  skills,
  query,
  saving,
  mobileDetailOpen,
  onQueryChange,
  onCreate,
  onEdit,
  onToggle,
  onDelete,
  onRollback,
  setError,
}: AdminSkillsLibraryProps) {
  const [historySkillID, setHistorySkillID] = useState("")

  return (
    <div className={`${mobileDetailOpen ? "hidden xl:flex" : "flex"} min-h-0 flex-col overflow-hidden border-b border-border/70 xl:border-b-0 xl:border-r`}>
      <div className="flex items-center justify-between border-b border-border/70 px-3 py-2.5">
        <div className="text-sm font-medium">Skill 库</div>
        <Button size="sm" onClick={onCreate}>
          <Plus className="h-3.5 w-3.5" />
          新建
        </Button>
      </div>
      <div className="border-b border-border/70 px-3 py-2">
        <div className="relative">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input value={query} onChange={(e) => onQueryChange(e.target.value)} placeholder="搜索 Skill" aria-label="搜索 Skill" className="h-11 pl-8 text-sm sm:h-8" />
        </div>
      </div>
      <div className="min-h-[220px] flex-1 overflow-y-auto scrollbar-thin">
        {skills.length === 0 ? (
          <div className="flex h-full items-center justify-center text-sm text-muted-foreground">暂无 Skill</div>
        ) : skills.map((skill) => (
          <div key={skill.id} className="grid min-h-11 grid-cols-[minmax(0,1fr)_auto] items-center gap-2 border-b border-border/60 px-3 py-1.5 last:border-b-0">
            <button className="min-w-0 rounded-md text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50" onClick={() => onEdit(skill)}>
              <div className="flex min-w-0 items-center gap-2">
                <span className="truncate text-sm font-medium">{skill.name}</span>
                <span className="shrink-0 text-[11px] text-muted-foreground">L{skill.min_group_level ?? 0}</span>
              </div>
              <div className="truncate text-[11px] text-muted-foreground">
                {sourceLabel(skill.source_type)} · {skill.files?.length || 0} files · {skill.id}
              </div>
            </button>
            <div className="flex items-center justify-end gap-1">
              <button
                type="button"
                onClick={() => onToggle(skill)}
                disabled={saving === skill.id}
                data-enabled={skill.enabled}
                className="group inline-flex h-11 w-11 items-center justify-center rounded-md transition-colors motion-control focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
                aria-label={`${skill.enabled ? "停用" : "启用"} Skill：${skill.name}`}
                title={skill.enabled ? "停用" : "启用"}
              >
                <span className="flex h-6 w-10 items-center rounded-full border border-border bg-muted px-0.5 transition-colors motion-control group-data-[enabled=true]:bg-foreground">
                  <span className="h-[18px] w-[18px] rounded-full bg-background shadow-sm transition-transform motion-control group-data-[enabled=true]:translate-x-4" />
                </span>
              </button>
              <Button
                variant="ghost"
                size="icon"
                className="h-11 w-11 sm:h-8 sm:w-8"
                onClick={() => setHistorySkillID((current) => current === skill.id ? "" : skill.id)}
                aria-label={`查看 Skill 变更历史：${skill.name}`}
                aria-expanded={historySkillID === skill.id}
                title="变更历史"
              >
                <History className="h-3.5 w-3.5" aria-hidden="true" />
              </Button>
              <Button variant="ghost" size="icon" className="h-11 w-11 text-destructive hover:text-destructive sm:h-8 sm:w-8" onClick={() => onDelete(skill)} aria-label={`删除 Skill：${skill.name}`} title="删除">
                <Trash2 className="h-3.5 w-3.5" aria-hidden="true" />
              </Button>
            </div>
            {historySkillID === skill.id ? (
              <div className="col-span-2 border-t border-border/60 bg-muted/20 px-1 py-2">
                <AdminSkillHistoryPanel skill={skill} onRollback={onRollback} setError={setError} />
              </div>
            ) : null}
          </div>
        ))}
      </div>
    </div>
  )
}
