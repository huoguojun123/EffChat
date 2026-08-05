import { useMemo, useRef, useState } from "react"
import { adminApi, type SkillImportPreview, type SkillInput } from "@/api/admin"
import type { SkillDefinition, UserGroup } from "@/types"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { useSkillStore } from "@/stores/skills"
import { GitBranch, Upload } from "lucide-react"
import {
  buildClientUpdatePreview,
  compareClientSkillFiles,
  defaultFilesForCandidate,
  defaultSelectedFiles,
  importReportLines,
  mergeSkills,
  upsertSkill,
} from "./AdminSkillsPanel.helpers"
import { SkillImportDialog, SkillUpdateDialog } from "./AdminSkillsDialogs"
import type { ImportDialogState, ImportReport, SkillDraft, UpdateDialogState } from "./AdminSkillsPanel.types"
import { AdminSkillsLibrary } from "./AdminSkillsLibrary"
import { AdminSkillEditor } from "./AdminSkillEditor"
import { AdminSkillFilesPanel } from "./AdminSkillFilesPanel"
import { BusyOwnership, EditorOwnership } from "./editorOwnership"

interface Props {
  skills: SkillDefinition[]
  setSkills: React.Dispatch<React.SetStateAction<SkillDefinition[]>>
  groups: UserGroup[]
  setError: (error: string) => void
}

const emptyDraft: SkillDraft = {
  id: "",
  name: "",
  description: "",
  entry_content: "",
  files: [],
  enabled: true,
  min_group_level: 0,
}

export function AdminSkillsPanel({ skills, setSkills, groups, setError }: Props) {
  const [query, setQuery] = useState("")
  const [draft, setDraft] = useState<SkillDraft | null>(null)
  const [activePath, setActivePath] = useState("SKILL.md")
  const [saving, setSaving] = useState("")
  const [gitUrl, setGitUrl] = useState("")
  const [gitRef, setGitRef] = useState("")
  const [gitBranches, setGitBranches] = useState<string[]>([])
  const [importLog, setImportLog] = useState<string[]>([])
  const [importLogExpanded, setImportLogExpanded] = useState(false)
  const [importDialog, setImportDialog] = useState<ImportDialogState | null>(null)
  const [updateDialog, setUpdateDialog] = useState<UpdateDialogState | null>(null)
  const [mobileDetailOpen, setMobileDetailOpen] = useState(false)
  const [mobilePane, setMobilePane] = useState<"editor" | "files">("editor")
  const [importQuery, setImportQuery] = useState("")
  const zipInputRef = useRef<HTMLInputElement>(null)
  const updateZipInputRef = useRef<HTMLInputElement>(null)
  const editorOwner = useRef(new EditorOwnership()).current
  const sourceOwner = useRef(new EditorOwnership()).current
  const updateOwner = useRef(new EditorOwnership()).current
  const busyOwner = useRef(new BusyOwnership()).current
  const updateCandidateGeneration = useRef(0)
  const refreshUserSkills = useSkillStore((s) => s.refreshSkills)

  const filteredSkills = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return skills
    return skills.filter((skill) =>
      [skill.name, skill.id, skill.description, skill.source_type, String(skill.min_group_level)].some((value) =>
        (value || "").toLowerCase().includes(q)
      )
    )
  }, [skills, query])

  const sortedGroups = useMemo(() => [...groups].sort((a, b) => a.level - b.level), [groups])
  const importReport = importDialog?.report
  const importLines = importReportLines(importReport)
  const visibleImportLines = importLogExpanded ? importLog : importLog.slice(0, 2)
  const editingSkill = draft?.originalId ? skills.find((skill) => skill.id === draft.originalId) : undefined

  function syncSkills() {
    void refreshUserSkills().catch(() => {})
  }

  function beginBusy(label: string, scope: string) {
    const operationId = busyOwner.begin(label, scope)
    setSaving(label)
    return operationId
  }

  function finishBusy(operationId: number) {
    const remainingLabel = busyOwner.release(operationId)
    if (remainingLabel !== null) setSaving(remainingLabel)
  }

  function invalidateBusy(scope: string) {
    setSaving(busyOwner.invalidate(scope))
  }

  function canLeaveDraft(nextEntityKey: string) {
    if (!draft || !editorOwner.isDirty()) return true
    if (editorOwner.currentEntityKey() === nextEntityKey) return false
    return window.confirm("放弃当前 Skill 的未保存修改？")
  }

  function changeDraft(update: React.SetStateAction<SkillDraft | null>) {
    editorOwner.change()
    setDraft(update)
  }

  function closeEditor() {
    if (!canLeaveDraft("")) return
    invalidateBusy("editor")
    editorOwner.invalidate()
    setDraft(null)
    setMobileDetailOpen(false)
    setMobilePane("editor")
  }

  function startCreate() {
    if (!canLeaveDraft("new")) return
    invalidateBusy("editor")
    editorOwner.activate("new")
    setDraft({ ...emptyDraft, files: [] })
    setActivePath("SKILL.md")
    setMobilePane("editor")
    setMobileDetailOpen(true)
  }

  async function startEdit(skill: SkillDefinition) {
    if (editorOwner.currentEntityKey() === skill.id && draft) return
    if (!canLeaveDraft(skill.id)) return
    invalidateBusy("editor")
    editorOwner.activate(skill.id)
    const operation = editorOwner.beginOperation()
    const busy = beginBusy(`load-${skill.id}`, "editor")
    setError("")
    setActivePath("SKILL.md")
    setMobilePane("editor")
    setMobileDetailOpen(true)
    setDraft(null)
    try {
      const files = skill.files?.length ? skill.files : (await adminApi.listSkillFiles(skill.id)).files
      const loaded = await Promise.all(files.map((file) => adminApi.getSkillFileContent(skill.id, file.path)))
      if (!editorOwner.owns(operation)) return
      const entry = loaded.find((item) => item.file.kind === "entry" || item.file.path === "SKILL.md")
      const references = loaded
        .filter((item) => item.file.kind !== "entry" && item.file.path !== "SKILL.md")
        .map((item) => ({ path: item.file.path, content: item.content }))
      setDraft({
        originalId: skill.id,
        id: skill.id,
        name: skill.name,
        description: skill.description,
        entry_content: entry?.content || "",
        files: references,
        enabled: skill.enabled,
        min_group_level: skill.min_group_level ?? 0,
      })
    } catch (err) {
      if (editorOwner.owns(operation, false)) {
        setError(err instanceof Error ? err.message : "加载 Skill 文件失败")
      }
    } finally {
      finishBusy(busy)
    }
  }

  async function saveDraft() {
    if (!draft) return
    const currentDraft = draft
    const operation = editorOwner.beginOperation()
    const busy = beginBusy(draft.originalId || "create", "editor")
    setError("")
    try {
      const payload: SkillInput = {
        id: currentDraft.id,
        name: currentDraft.name,
        description: currentDraft.description,
        entry_content: currentDraft.entry_content,
        files: currentDraft.files,
        enabled: currentDraft.enabled,
        min_group_level: currentDraft.min_group_level ?? 0,
      }
      const saved = currentDraft.originalId
        ? await adminApi.updateSkill(currentDraft.originalId, payload)
        : await adminApi.createSkill(payload)
      // The server mutation remains authoritative even if the user has moved
      // to another editor generation while it was in flight. Ownership only
      // gates draft-local UI changes; the shared catalog must still converge.
      setSkills((prev) => upsertSkill(prev, saved))
      syncSkills()
      if (editorOwner.owns(operation, false)) {
        editorOwner.acknowledge(operation.revision)
        if (editorOwner.owns(operation)) {
          editorOwner.invalidate()
          setDraft(null)
          setActivePath("SKILL.md")
          setMobileDetailOpen(false)
          setMobilePane("editor")
        } else {
          setError("已保存较早版本，当前修改仍未保存")
        }
      }
    } catch (err) {
      if (editorOwner.owns(operation, false)) {
        setError(err instanceof Error ? err.message : "保存 Skill 失败")
      }
    } finally {
      finishBusy(busy)
    }
  }

  async function toggleSkill(skill: SkillDefinition) {
    const busy = beginBusy(skill.id, `catalog:${skill.id}`)
    setError("")
    try {
      const updated = await adminApi.updateSkill(skill.id, { enabled: !skill.enabled })
      setSkills((prev) => upsertSkill(prev, updated))
      syncSkills()
    } catch (err) {
      setError(err instanceof Error ? err.message : "更新 Skill 失败")
    } finally {
      finishBusy(busy)
    }
  }

  async function deleteSkill(skill: SkillDefinition) {
    if (!window.confirm(`确定删除 Skill「${skill.name}」吗？旧文件包也会被清理。`)) return
    const busy = beginBusy(skill.id, `catalog:${skill.id}`)
    setError("")
    try {
      await adminApi.deleteSkill(skill.id)
      setSkills((prev) => prev.filter((item) => item.id !== skill.id))
      if (editorOwner.currentEntityKey() === skill.id) {
        editorOwner.invalidate()
        setDraft(null)
        setMobileDetailOpen(false)
        setMobilePane("editor")
      }
      syncSkills()
    } catch (err) {
      setError(err instanceof Error ? err.message : "删除 Skill 失败")
    } finally {
      finishBusy(busy)
    }
  }

  function applyGovernanceRollback(skillID: string, restored: SkillDefinition | null) {
    setSkills((current) => restored ? upsertSkill(current, restored) : current.filter((skill) => skill.id !== skillID))
    if (!restored && editorOwner.currentEntityKey() === skillID) {
      editorOwner.invalidate()
      setDraft(null)
      setMobileDetailOpen(false)
      setMobilePane("editor")
    }
    syncSkills()
  }

  function updateGitUrl(value: string) {
    invalidateBusy("source")
    sourceOwner.invalidate()
    setGitUrl(value)
    setGitRef("")
    setGitBranches([])
    setImportDialog(null)
    setImportLog([])
  }

  async function previewGit(ref?: string) {
    const url = gitUrl.trim()
    if (!url) return
    invalidateBusy("source")
    sourceOwner.activate(`git:${url}:${ref || ""}`)
    const operation = sourceOwner.beginOperation()
    const busy = beginBusy("git-scan", "source")
    setError("")
    try {
      const result = await adminApi.previewSkillsFromGit(url, ref || undefined)
      if (!sourceOwner.owns(operation)) return
      setGitBranches(result.branches || [])
      setGitRef(result.selected_ref || ref || "")
      setImportLog(importReportLines(result.report))
      setImportLogExpanded(false)
      setImportQuery("")
      setImportDialog({
        source: "git",
        title: "选择 Git Skills",
        url,
        ref: result.selected_ref || ref || "",
        skills: result.skills || [],
        selectedPaths: (result.skills || []).map((skill) => skill.source_path),
        selectedFiles: defaultSelectedFiles(result.skills || []),
        report: result.report,
      })
    } catch (err) {
      if (sourceOwner.owns(operation, false)) {
        setError(err instanceof Error ? err.message : "Git 扫描失败")
      }
    } finally {
      finishBusy(busy)
    }
  }

  async function previewZip(file?: File) {
    if (!file) return
    invalidateBusy("source")
    sourceOwner.activate(`zip:${file.name}:${file.size}:${file.lastModified}`)
    const operation = sourceOwner.beginOperation()
    const busy = beginBusy("zip-scan", "source")
    setError("")
    try {
      const result = await adminApi.previewSkillsFromZip(file)
      if (!sourceOwner.owns(operation)) return
      setImportLog(importReportLines(result.report))
      setImportLogExpanded(false)
      setImportQuery("")
      setImportDialog({
        source: "zip",
        title: "选择 Zip Skills",
        file,
        skills: result.skills || [],
        selectedPaths: (result.skills || []).map((skill) => skill.source_path),
        selectedFiles: defaultSelectedFiles(result.skills || []),
        report: result.report,
      })
    } catch (err) {
      if (sourceOwner.owns(operation, false)) {
        setError(err instanceof Error ? err.message : "Zip 扫描失败")
      }
    } finally {
      finishBusy(busy)
      if (zipInputRef.current) zipInputRef.current.value = ""
    }
  }

  async function importSelectedSkills() {
    if (!importDialog || importDialog.selectedPaths.length === 0) return
    const sourceOperation = sourceOwner.beginOperation()
    const selectedSkills = importDialog.skills.filter((skill) => importDialog.selectedPaths.includes(skill.source_path))
    const updateCandidates = selectedSkills.filter((skill) => skill.existing_skill && skill.default_action === "update")
    if (updateCandidates.length > 0) {
      const first = updateCandidates[0]
      const current = first.existing_skill
      if (!current) return
      const currentEntryPreview = await adminApi.getSkillFileContent(current.id, "SKILL.md")
        .then((loaded) => loaded.content)
        .catch(() => "")
      if (!sourceOwner.owns(sourceOperation, false)) return
      updateOwner.activate(`import:${importDialog.source}:${first.source_path}`)
      updateCandidateGeneration.current += 1
      setUpdateDialog({
        source: importDialog.source,
        title: "确认重复导入",
        file: importDialog.source === "zip" ? importDialog.file : undefined,
        ref: importDialog.source === "git" ? importDialog.ref : undefined,
        preview: buildClientUpdatePreview(current, importDialog.skills, first.source_path, importDialog.report, currentEntryPreview),
        selectedSourcePath: first.source_path,
        selectedFiles: Object.fromEntries(updateCandidates.map((skill) => [
          skill.source_path,
          importDialog.selectedFiles[skill.source_path] || defaultFilesForCandidate(skill.existing_skill || current, skill),
        ])),
        pendingUpdates: updateCandidates,
        pendingCreates: selectedSkills.filter((skill) => !skill.existing_skill || skill.default_action === "review").map((skill) => skill.source_path),
      })
      return
    }
    const busy = beginBusy("import", "source")
    setError("")
    try {
      const result = importDialog.source === "git"
        ? await adminApi.importSkillsFromGit(importDialog.url, importDialog.ref, importDialog.selectedPaths, importDialog.selectedFiles)
        : await adminApi.importSkillsFromZip(importDialog.file, importDialog.selectedPaths, importDialog.selectedFiles)
      // Closing or replacing the preview must not hide a mutation that the
      // server already committed. Only dialog-local feedback is owner-gated.
      setSkills((prev) => mergeSkills(prev, result.skills || []))
      syncSkills()
      if (sourceOwner.owns(sourceOperation, false)) {
        setImportLog(importReportLines(result.report))
        setImportLogExpanded(false)
        setImportDialog(null)
      }
    } catch (err) {
      if (sourceOwner.owns(sourceOperation, false)) {
        setError(err instanceof Error ? err.message : "导入 Skill 失败")
      }
    } finally {
      finishBusy(busy)
    }
  }

  async function previewGitUpdate(skill: SkillDefinition) {
    invalidateBusy("update-preview")
    updateOwner.activate(`git:${skill.id}`)
    const operation = updateOwner.beginOperation()
    const busy = beginBusy(`update-${skill.id}`, "update-preview")
    setError("")
    try {
      const preview = await adminApi.previewSkillGitUpdate(skill.id)
      if (!updateOwner.owns(operation)) return
      updateCandidateGeneration.current += 1
      setUpdateDialog({
        source: "git",
        title: "检查 Git 更新",
        ref: skill.source_ref,
        preview,
        selectedSourcePath: preview.selected_source_path || preview.candidates[0]?.source_path || "",
        selectedFiles: {
          [preview.selected_source_path || preview.candidates[0]?.source_path || ""]: preview.default_selected_files || [],
        },
      })
    } catch (err) {
      if (updateOwner.owns(operation, false)) {
        setError(err instanceof Error ? err.message : "检查 Skill 更新失败")
      }
    } finally {
      finishBusy(busy)
    }
  }

  async function previewZipUpdate(skill: SkillDefinition, file?: File) {
    if (!file) return
    invalidateBusy("update-preview")
    updateOwner.activate(`zip:${skill.id}:${file.name}:${file.size}:${file.lastModified}`)
    const operation = updateOwner.beginOperation()
    const busy = beginBusy(`update-${skill.id}`, "update-preview")
    setError("")
    try {
      const preview = await adminApi.previewSkillZipUpdate(skill.id, file)
      if (!updateOwner.owns(operation)) return
      updateCandidateGeneration.current += 1
      setUpdateDialog({
        source: "zip",
        title: "上传新版 Zip",
        file,
        preview,
        selectedSourcePath: preview.selected_source_path || preview.candidates[0]?.source_path || "",
        selectedFiles: {
          [preview.selected_source_path || preview.candidates[0]?.source_path || ""]: preview.default_selected_files || [],
        },
      })
    } catch (err) {
      if (updateOwner.owns(operation, false)) {
        setError(err instanceof Error ? err.message : "Zip 更新预览失败")
      }
    } finally {
      finishBusy(busy)
      if (updateZipInputRef.current) updateZipInputRef.current.value = ""
    }
  }

  async function applyUpdateDialog() {
    if (!updateDialog || !updateDialog.selectedSourcePath) return
    const operation = updateOwner.beginOperation()
    const updates = updateDialog.pendingUpdates?.length
      ? updateDialog.pendingUpdates
      : [updateDialog.preview.candidates.find((skill) => skill.source_path === updateDialog.selectedSourcePath)].filter(Boolean) as SkillImportPreview[]
    const busy = beginBusy("skill-update", "update-dialog")
    setError("")
    try {
      const updated: SkillDefinition[] = []
      let lastReport: ImportReport | undefined
      for (const candidate of updates) {
        const current = candidate.existing_skill || updateDialog.preview.current
        const selected = updateDialog.selectedFiles[candidate.source_path] || []
        const result = updateDialog.source === "git"
          ? await adminApi.applySkillGitUpdate(current.id, candidate.source_path, selected, updateDialog.ref, importDialog?.source === "git" ? importDialog.url : undefined)
          : updateDialog.file
            ? await adminApi.applySkillZipUpdate(current.id, updateDialog.file, candidate.source_path, selected)
            : undefined
        if (!result) throw new Error("缺少 Zip 文件")
        updated.push(...(result.skills || []))
        lastReport = result.report
      }
      let created: SkillDefinition[] = []
      if (updateDialog.pendingCreates?.length && importDialog) {
        const createResult = importDialog.source === "git"
          ? await adminApi.importSkillsFromGit(importDialog.url, importDialog.ref, updateDialog.pendingCreates, importDialog.selectedFiles)
          : await adminApi.importSkillsFromZip(importDialog.file, updateDialog.pendingCreates, importDialog.selectedFiles)
        created = createResult.skills || []
        lastReport = createResult.report || lastReport
      }
      setSkills((prev) => mergeSkills(prev, [...updated, ...created]))
      syncSkills()
      if (updateOwner.owns(operation, false)) {
        setImportLog(importReportLines(lastReport))
        setImportDialog(null)
        setUpdateDialog(null)
        updateOwner.invalidate()
      }
    } catch (err) {
      if (updateOwner.owns(operation, false)) {
        setError(err instanceof Error ? err.message : "更新 Skill 失败")
      }
    } finally {
      finishBusy(busy)
    }
  }

  function updateImportSelection(paths: string[]) {
    const next = Array.from(new Set(paths))
    setImportDialog((prev) => prev ? { ...prev, selectedPaths: next } : prev)
  }

  function toggleImportPath(path: string) {
    if (!importDialog) return
    const selected = new Set(importDialog.selectedPaths)
    if (selected.has(path)) selected.delete(path)
    else selected.add(path)
    updateImportSelection(Array.from(selected))
  }

  function toggleImportFile(sourcePath: string, filePath: string) {
    if (!importDialog || filePath === "SKILL.md") return
    const current = new Set(importDialog.selectedFiles[sourcePath] || [])
    if (current.has(filePath)) current.delete(filePath)
    else current.add(filePath)
    setImportDialog((prev) => prev ? {
      ...prev,
      selectedFiles: { ...prev.selectedFiles, [sourcePath]: Array.from(current).sort() },
    } : prev)
  }

  async function selectUpdateCandidate(path: string) {
    if (!updateDialog) return
    const generation = ++updateCandidateGeneration.current
    const dialog = updateDialog
    const candidate = updateDialog.preview.candidates.find((skill) => skill.source_path === path)
    const current = candidate?.existing_skill || updateDialog.preview.current
    let currentEntryPreview = updateDialog.preview.current_entry_preview
    let currentEntryTruncated = updateDialog.preview.current_entry_truncated
    if (current.id !== updateDialog.preview.current.id) {
      try {
        const loaded = await adminApi.getSkillFileContent(current.id, "SKILL.md")
        currentEntryPreview = loaded.content.slice(0, 3200)
        currentEntryTruncated = loaded.content.length > 3200
      } catch {
        currentEntryPreview = ""
        currentEntryTruncated = false
      }
    }
    if (generation !== updateCandidateGeneration.current) return
    setUpdateDialog((prev) => {
      if (!prev || prev !== dialog || generation !== updateCandidateGeneration.current) return prev
      return {
        ...prev,
        selectedSourcePath: path,
        selectedFiles: {
          ...prev.selectedFiles,
          [path]: prev.selectedFiles[path] || defaultFilesForCandidate(current, candidate),
        },
        preview: {
          ...prev.preview,
          current,
          current_entry_preview: currentEntryPreview,
          current_entry_truncated: currentEntryTruncated,
          file_changes: compareClientSkillFiles(current.files || [], candidate?.files || []),
          default_selected_files: prev.selectedFiles[path] || defaultFilesForCandidate(current, candidate),
        },
      }
    })
  }

  function setReferenceContent(path: string, content: string) {
    changeDraft((prev) => prev ? { ...prev, files: prev.files.map((file) => file.path === path ? { ...file, content } : file) } : prev)
  }

  function renameReference(oldPath: string, path: string) {
    const clean = path.trim()
    if (!clean) return
    changeDraft((prev) => prev ? { ...prev, files: prev.files.map((file) => file.path === oldPath ? { ...file, path: clean } : file) } : prev)
    setActivePath(clean)
  }

  function addReference() {
    const existing = new Set(draft?.files.map((file) => file.path) || [])
    let index = 1
    let path = "references/note.md"
    while (existing.has(path)) {
      index += 1
      path = `references/note-${index}.md`
    }
    changeDraft((prev) => prev ? { ...prev, files: [...prev.files, { path, content: "" }] } : prev)
    setActivePath(path)
    setMobilePane("editor")
  }

  function removeReference(path: string) {
    changeDraft((prev) => prev ? { ...prev, files: prev.files.filter((file) => file.path !== path) } : prev)
    setActivePath("SKILL.md")
  }

  const activeReference = draft?.files.find((file) => file.path === activePath)
  const activeContent = activePath === "SKILL.md" ? draft?.entry_content || "" : activeReference?.content || ""

  return (
    <div className="flex h-full min-h-0 flex-col overflow-hidden">
      <div className="border-b border-border/70 px-4 py-3">
        <div className="grid gap-2 lg:grid-cols-[minmax(18rem,1fr)_auto_auto_auto] lg:items-center">
          <Input
            value={gitUrl}
            onChange={(e) => updateGitUrl(e.target.value)}
            onBlur={() => {
              if (gitUrl.trim() && gitBranches.length === 0 && saving === "") void previewGit()
            }}
            placeholder="Git 仓库地址"
            className="h-8 text-sm"
          />
          {gitBranches.length > 0 && (
            <select
              value={gitRef}
              onChange={(e) => void previewGit(e.target.value)}
              className="h-8 rounded-md border border-input bg-background px-2 text-sm outline-none"
            >
              {gitBranches.map((branch) => (
                <option key={branch} value={branch}>{branch}</option>
              ))}
              {gitRef && !gitBranches.includes(gitRef) && <option value={gitRef}>{gitRef}</option>}
            </select>
          )}
          <Button size="sm" variant="outline" onClick={() => void previewGit()} disabled={!gitUrl.trim() || saving === "git-scan"}>
            <GitBranch className="h-3.5 w-3.5" />
            {saving === "git-scan" ? "扫描中" : "扫描 Git"}
          </Button>
          <input
            ref={zipInputRef}
            type="file"
            accept=".zip,application/zip"
            className="hidden"
            onChange={(e) => void previewZip(e.target.files?.[0])}
          />
          <Button size="sm" variant="outline" onClick={() => zipInputRef.current?.click()} disabled={saving === "zip-scan"}>
            <Upload className="h-3.5 w-3.5" />
            {saving === "zip-scan" ? "扫描中" : "扫描 Zip"}
          </Button>
        </div>
        {importLog.length > 0 && (
          <button
            type="button"
            onClick={() => setImportLogExpanded((next) => !next)}
            className="mt-2 w-full rounded-md bg-amber-500/10 px-2.5 py-1.5 text-left text-xs leading-5 text-amber-700 transition-colors hover:bg-amber-500/15 dark:text-amber-300"
          >
            导入提示 {importLog.length} 项：{visibleImportLines.join("；")}{!importLogExpanded && importLog.length > 2 ? " …" : ""}
          </button>
        )}
      </div>

      <div className="grid min-h-0 flex-1 overflow-hidden xl:grid-cols-[280px_minmax(0,1fr)_260px]">
        <AdminSkillsLibrary
          skills={filteredSkills}
          query={query}
          saving={saving}
          mobileDetailOpen={mobileDetailOpen}
          onQueryChange={setQuery}
          onCreate={startCreate}
          onEdit={(skill) => void startEdit(skill)}
          onToggle={(skill) => void toggleSkill(skill)}
          onDelete={(skill) => void deleteSkill(skill)}
          onRollback={applyGovernanceRollback}
          setError={setError}
        />

        <AdminSkillEditor
          draft={draft}
          setDraft={changeDraft}
          activePath={activePath}
          activeContent={activeContent}
          mobileDetailOpen={mobileDetailOpen}
          mobilePane={mobilePane}
          sortedGroups={sortedGroups}
          editingSkill={editingSkill}
          saving={saving}
          updateZipInputRef={updateZipInputRef}
          onBack={() => setMobileDetailOpen(false)}
          onClose={closeEditor}
          onMobilePaneChange={setMobilePane}
          onPreviewGitUpdate={(skill) => void previewGitUpdate(skill)}
          onPreviewZipUpdate={(skill, file) => void previewZipUpdate(skill, file)}
          onSetReferenceContent={setReferenceContent}
          onSelectPath={setActivePath}
          onSelectPathAndEdit={(path) => { setActivePath(path); setMobilePane("editor") }}
          onAddReference={addReference}
          onRenameReference={renameReference}
          onRemoveReference={removeReference}
          onSave={() => void saveDraft()}
        />

        <AdminSkillFilesPanel
          draft={draft}
          activePath={activePath}
          className="hidden min-h-0 flex-col overflow-hidden border-t border-border/70 xl:flex xl:border-l xl:border-t-0"
          emptyText="选择 Skill 后查看 SKILL.md 与 references。"
          onAddReference={addReference}
          onSelectPath={setActivePath}
          onRenameReference={renameReference}
          onRemoveReference={removeReference}
        />
      </div>

      <SkillImportDialog
        state={importDialog}
        query={importQuery}
        saving={saving === "import"}
        reportLines={importLines}
        onQueryChange={setImportQuery}
        onOpenChange={(open) => {
          if (!open) {
            invalidateBusy("source")
            sourceOwner.invalidate()
            setImportDialog(null)
          }
        }}
        onTogglePath={toggleImportPath}
        onToggleFile={toggleImportFile}
        onSelectAll={() => importDialog && updateImportSelection(importDialog.skills.map((skill) => skill.source_path))}
        onInvert={() => importDialog && updateImportSelection(importDialog.skills.filter((skill) => !importDialog.selectedPaths.includes(skill.source_path)).map((skill) => skill.source_path))}
        onClear={() => updateImportSelection([])}
        onImport={() => void importSelectedSkills()}
      />
      <SkillUpdateDialog
        state={updateDialog}
        saving={saving === "skill-update"}
        onOpenChange={(open) => {
          if (!open) {
            invalidateBusy("update-preview")
            invalidateBusy("update-dialog")
            updateOwner.invalidate()
            updateCandidateGeneration.current += 1
            setUpdateDialog(null)
          }
        }}
        onSelectCandidate={(path) => void selectUpdateCandidate(path)}
        onToggleFile={(sourcePath, filePath) => {
          if (filePath === "SKILL.md") return
          setUpdateDialog((prev) => {
            if (!prev) return prev
            const current = new Set(prev.selectedFiles[sourcePath] || [])
            if (current.has(filePath)) current.delete(filePath)
            else current.add(filePath)
            return { ...prev, selectedFiles: { ...prev.selectedFiles, [sourcePath]: Array.from(current).sort() } }
          })
        }}
        onApply={() => void applyUpdateDialog()}
      />
    </div>
  )
}
