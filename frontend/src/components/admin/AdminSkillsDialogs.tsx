import { useMemo } from "react"
import { AlertTriangle, CheckSquare, Download, MinusSquare, RefreshCw, Search, Square } from "lucide-react"
import { Button } from "@/components/ui/button"
import { DialogFooter } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { WorkspaceWindow } from "@/components/ui/workspace-window"
import {
  compareClientSkillFiles,
  fileReasonLabel,
  formatBytes,
  statusClass,
  statusLabel,
} from "./AdminSkillsPanel.helpers"
import type { ImportDialogState, UpdateDialogState } from "./AdminSkillsPanel.types"

export function SkillImportDialog({
  state,
  query,
  saving,
  reportLines,
  onQueryChange,
  onOpenChange,
  onTogglePath,
  onToggleFile,
  onSelectAll,
  onInvert,
  onClear,
  onImport,
}: {
  state: ImportDialogState | null
  query: string
  saving: boolean
  reportLines: string[]
  onQueryChange: (value: string) => void
  onOpenChange: (open: boolean) => void
  onTogglePath: (path: string) => void
  onToggleFile: (sourcePath: string, filePath: string) => void
  onSelectAll: () => void
  onInvert: () => void
  onClear: () => void
  onImport: () => void
}) {
  const selected = new Set(state?.selectedPaths || [])
  const duplicateIds = useMemo(() => {
    const counts = new Map<string, number>()
    for (const skill of state?.skills || []) counts.set(skill.id, (counts.get(skill.id) || 0) + 1)
    return counts
  }, [state?.skills])
  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!state) return []
    if (!q) return state.skills
    return state.skills.filter((skill) =>
      [skill.name, skill.id, skill.description, skill.source_path].some((value) => value.toLowerCase().includes(q))
    )
  }, [query, state])

  return (
    <WorkspaceWindow
      open={!!state}
      onOpenChange={onOpenChange}
      title={state?.title || "选择 Skills"}
      defaultWidth={900}
      defaultHeight={720}
      contentClassName="flex flex-col"
    >
        <div className="grid gap-2 border-b border-border/70 px-4 py-3 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center">
          <div className="relative">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input value={query} onChange={(e) => onQueryChange(e.target.value)} placeholder="搜索名称、ID、路径" aria-label="搜索可导入 Skill" className="h-11 pl-8 text-sm sm:h-8" />
          </div>
          <div className="flex flex-wrap gap-1">
            <Button size="sm" variant="outline" onClick={onSelectAll}>
              <CheckSquare className="h-3.5 w-3.5" />
              全选
            </Button>
            <Button size="sm" variant="outline" onClick={onInvert}>
              <MinusSquare className="h-3.5 w-3.5" />
              反选
            </Button>
            <Button size="sm" variant="outline" onClick={onClear}>
              <Square className="h-3.5 w-3.5" />
              清空
            </Button>
          </div>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto px-4 py-2 scrollbar-thin">
          {filtered.length === 0 ? (
            <div className="py-12 text-center text-sm text-muted-foreground">没有可导入的 SKILL.md</div>
          ) : filtered.map((skill) => {
            const checked = selected.has(skill.source_path)
            const duplicated = (duplicateIds.get(skill.id) || 0) > 1
            const selectedFiles = new Set(state?.selectedFiles[skill.source_path] || [])
            const skillFiles = skill.files || []
            return (
              <div key={skill.source_path} className="grid grid-cols-[auto_minmax(0,1fr)] gap-3 border-b border-border/60 py-2.5 last:border-b-0">
                <input
                  type="checkbox"
                  checked={checked}
                  onChange={() => onTogglePath(skill.source_path)}
                  className="mt-1"
                />
                <div className="min-w-0">
                  <span className="flex min-w-0 items-center gap-2">
                    <span className="truncate text-sm font-medium">{skill.name}</span>
                    {duplicated && <span className="rounded bg-amber-500/15 px-1.5 py-0.5 text-xs text-amber-700 dark:text-amber-300">重复 ID</span>}
                    {skill.default_action === "update" && <span className="rounded bg-sky-500/15 px-1.5 py-0.5 text-xs text-sky-700 dark:text-sky-300">已存在，将更新</span>}
                    {skill.default_action === "review" && <span className="rounded bg-amber-500/15 px-1.5 py-0.5 text-xs text-amber-700 dark:text-amber-300">疑似重复</span>}
                  </span>
                  <span className="mt-0.5 block truncate text-xs text-muted-foreground">{skill.id} · {skill.source_path}</span>
                  {skill.description && <span className="mt-1 line-clamp-2 block text-xs leading-5 text-muted-foreground">{skill.description}</span>}
                  {skillFiles.length > 0 && (
                    <div className="mt-2 grid gap-1 rounded-md bg-muted/45 p-2">
                      {skillFiles.map((file) => {
                        const entry = file.kind === "entry" || file.path === "SKILL.md"
                        const fileChecked = entry || selectedFiles.has(file.path)
                        return (
                          <label key={file.path} className="flex min-w-0 items-center gap-2 text-xs text-muted-foreground">
                            <input
                              type="checkbox"
                              checked={fileChecked}
                              disabled={entry || !checked}
                              onChange={() => onToggleFile(skill.source_path, file.path)}
                            />
                            <span className="truncate">{file.path}</span>
                            <span className="shrink-0 rounded bg-background/80 px-1 py-0.5">{fileReasonLabel(file.reason)}</span>
                          </label>
                        )
                      })}
                    </div>
                  )}
                </div>
              </div>
            )
          })}
        </div>
        {reportLines.length > 0 && (
          <div className="border-t border-border/70 px-4 py-2 text-xs text-muted-foreground">
            {reportLines.slice(0, 3).join("；")}{reportLines.length > 3 ? " …" : ""}
          </div>
        )}
        <DialogFooter className="border-t border-border/70 px-4 py-3">
          <div className="mr-auto text-xs text-muted-foreground">已选 {selected.size} / {state?.skills.length || 0}</div>
          <Button variant="outline" size="sm" onClick={() => onOpenChange(false)}>取消</Button>
          <Button size="sm" onClick={onImport} disabled={saving || selected.size === 0}>
            <Download className="h-3.5 w-3.5" />
            {saving ? "导入中" : "导入选中项"}
          </Button>
        </DialogFooter>
    </WorkspaceWindow>
  )
}

export function SkillUpdateDialog({
  state,
  saving,
  onOpenChange,
  onSelectCandidate,
  onToggleFile,
  onApply,
}: {
  state: UpdateDialogState | null
  saving: boolean
  onOpenChange: (open: boolean) => void
  onSelectCandidate: (path: string) => void
  onToggleFile: (sourcePath: string, filePath: string) => void
  onApply: () => void
}) {
  const selectedCandidate = state?.preview.candidates.find((skill) => skill.source_path === state.selectedSourcePath)
  const changes = state && selectedCandidate ? compareClientSkillFiles(state.preview.current.files || [], selectedCandidate.files || []) : []
  const selectedFiles = new Set(state?.selectedFiles[state?.selectedSourcePath || ""] || [])
  const pendingCount = state?.pendingUpdates?.length || 0

  return (
    <WorkspaceWindow
      open={!!state}
      onOpenChange={onOpenChange}
      title={state?.title || "更新 Skill"}
      defaultWidth={1180}
      defaultHeight={820}
      contentClassName="flex flex-col"
    >
        {state && (
          <div className="grid min-h-0 flex-1 overflow-hidden lg:grid-cols-[240px_minmax(0,1fr)]">
            <div className="min-h-0 overflow-y-auto border-b border-border/70 p-3 lg:border-b-0 lg:border-r">
              <div className="mb-2 text-xs text-muted-foreground">
                {pendingCount > 1 ? `本次将更新 ${pendingCount} 个重复 Skill` : "选择新版入口"}
              </div>
              <div className="grid gap-1.5">
                {state.preview.candidates.map((candidate) => (
                  <button
                    key={candidate.source_path}
                    type="button"
                    onClick={() => onSelectCandidate(candidate.source_path)}
                    data-active={candidate.source_path === state.selectedSourcePath}
                    className="rounded-md border border-border/70 px-2.5 py-2 text-left text-xs transition-colors hover:bg-muted/60 data-[active=true]:border-foreground/40 data-[active=true]:bg-muted/70"
                  >
                    <div className="flex min-w-0 items-center gap-2">
                      <span className="truncate font-medium">{candidate.name}</span>
                      <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground">{candidate.match_type || "候选"}</span>
                    </div>
                    <div className="mt-1 truncate text-xs text-muted-foreground">{candidate.id} · {candidate.source_path}</div>
                  </button>
                ))}
              </div>
            </div>

            <div className="flex min-h-0 flex-col overflow-hidden">
              <div className="grid gap-3 border-b border-border/70 p-4 md:grid-cols-2">
                <VersionPanel
                  title="当前版本"
                  name={state.preview.current.name}
                  id={state.preview.current.id}
                  path={state.preview.current.source_path || "SKILL.md"}
                  content={state.preview.current_entry_preview}
                  truncated={state.preview.current_entry_truncated}
                />
                <VersionPanel
                  title="新版候选"
                  name={selectedCandidate?.name || "未选择"}
                  id={selectedCandidate?.id || ""}
                  path={selectedCandidate?.source_path || ""}
                  content={selectedCandidate?.entry_preview || ""}
                  truncated={selectedCandidate?.entry_truncated}
                />
              </div>

              <div className="min-h-0 flex-1 overflow-y-auto p-4 scrollbar-thin">
                {selectedCandidate?.default_action === "review" && (
                  <div className="mb-3 flex items-start gap-2 rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-700 dark:text-amber-300">
                    <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                    同名但 ID 不同，请确认这是要覆盖当前 Skill 的新版入口。
                  </div>
                )}
                <div className="overflow-hidden rounded-md border border-border/70">
                  <div className="grid grid-cols-[2rem_minmax(0,1fr)_5rem_5rem_5rem] gap-2 border-b border-border/70 bg-muted/45 px-3 py-2 text-xs text-muted-foreground">
                    <span />
                    <span>文件</span>
                    <span>状态</span>
                    <span>旧大小</span>
                    <span>新大小</span>
                  </div>
                  {changes.map((file) => {
                    const entry = file.kind === "entry" || file.path === "SKILL.md"
                    const disabled = entry || file.status === "missing"
                    const checked = entry || selectedFiles.has(file.path)
                    return (
                      <label key={`${file.status}-${file.path}`} className="grid grid-cols-[2rem_minmax(0,1fr)_5rem_5rem_5rem] items-center gap-2 border-b border-border/50 px-3 py-2 text-xs last:border-b-0">
                        <input
                          type="checkbox"
                          checked={checked}
                          disabled={disabled}
                          onChange={() => selectedCandidate && onToggleFile(selectedCandidate.source_path, file.path)}
                        />
                        <span className="min-w-0 truncate">{file.path}</span>
                        <span className={statusClass(file.status)}>{statusLabel(file.status)}</span>
                        <span className="text-muted-foreground">{file.old_size ? formatBytes(file.old_size) : "-"}</span>
                        <span className="text-muted-foreground">{file.new_size ? formatBytes(file.new_size) : "-"}</span>
                      </label>
                    )
                  })}
                </div>
              </div>
            </div>
          </div>
        )}
        <DialogFooter className="border-t border-border/70 px-4 py-3">
          <Button variant="outline" size="sm" onClick={() => onOpenChange(false)}>取消</Button>
          <Button size="sm" onClick={onApply} disabled={saving || !state?.selectedSourcePath}>
            <RefreshCw className="h-3.5 w-3.5" />
            {saving ? "更新中" : "确认覆盖"}
          </Button>
        </DialogFooter>
    </WorkspaceWindow>
  )
}

function VersionPanel({ title, name, id, path, content, truncated }: { title: string; name: string; id: string; path: string; content: string; truncated?: boolean }) {
  return (
    <div className="min-w-0 rounded-md border border-border/70 bg-background">
      <div className="border-b border-border/70 px-3 py-2">
        <div className="text-xs font-medium">{title}</div>
        <div className="mt-1 truncate text-xs text-muted-foreground">{name} · {id} · {path}</div>
      </div>
      <pre className="max-h-44 overflow-auto whitespace-pre-wrap px-3 py-2 font-mono text-xs leading-5 scrollbar-thin">
        {content || "暂无预览"}
        {truncated ? "\n\n……预览已截断" : ""}
      </pre>
    </div>
  )
}
