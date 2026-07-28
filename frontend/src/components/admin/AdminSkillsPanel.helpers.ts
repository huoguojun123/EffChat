import type { SkillFileChange, SkillImportPreview, SkillImportResult, SkillUpdatePreviewResult } from "@/api/admin"
import type { SkillDefinition } from "@/types"

type ImportReport = SkillImportResult["report"]

export function sourceLabel(source: SkillDefinition["source_type"]) {
  switch (source) {
    case "builtin":
      return "内置"
    case "git":
      return "Git"
    case "zip":
      return "Zip"
    default:
      return "手动"
  }
}

export function buildClientUpdatePreview(current: SkillDefinition, candidates: SkillImportPreview[], selectedSourcePath: string, report: ImportReport, currentEntryPreview = ""): SkillUpdatePreviewResult {
  const selected = candidates.find((skill) => skill.source_path === selectedSourcePath)
  return {
    current,
    candidates,
    selected_source_path: selectedSourcePath,
    match_type: selected?.match_type,
    default_selected_files: defaultFilesForCandidate(current, selected),
    file_changes: compareClientSkillFiles(current.files || [], selected?.files || []),
    current_entry_preview: currentEntryPreview.slice(0, 3200),
    current_entry_truncated: currentEntryPreview.length > 3200,
    report,
  }
}

export function defaultFilesForCandidate(current: SkillDefinition, candidate?: SkillImportPreview) {
  if (!candidate) return []
  const currentPaths = new Set((current.files || []).filter((file) => file.kind !== "entry" && file.path !== "SKILL.md").map((file) => file.path))
  return (candidate.files || [])
    .filter((file) => file.kind !== "entry" && file.path !== "SKILL.md" && currentPaths.has(file.path))
    .map((file) => file.path)
    .sort()
}

export function compareClientSkillFiles(current: SkillDefinition["files"], candidate: SkillImportPreview["files"] = []): SkillFileChange[] {
  const currentByPath = new Map((current || []).map((file) => [file.path, file]))
  const seen = new Set<string>()
  const changes: SkillFileChange[] = []
  for (const file of candidate || []) {
    seen.add(file.path)
    const old = currentByPath.get(file.path)
    if (file.kind === "entry" || file.path === "SKILL.md") {
      changes.push({
        path: file.path,
        kind: file.kind,
        status: "entry",
        old_checksum: old?.checksum,
        new_checksum: file.checksum,
        old_size: old?.size,
        new_size: file.size,
        reason: file.reason,
        selected_default: true,
      })
      continue
    }
    changes.push({
      path: file.path,
      kind: file.kind,
      status: old ? (file.checksum && old.checksum === file.checksum ? "unchanged" : file.checksum ? "modified" : "same_path") : "added",
      old_checksum: old?.checksum,
      new_checksum: file.checksum,
      old_size: old?.size,
      new_size: file.size,
      reason: file.reason,
      selected_default: !!old,
    })
  }
  for (const old of current || []) {
    if (old.kind === "entry" || old.path === "SKILL.md" || seen.has(old.path)) continue
    changes.push({
      path: old.path,
      kind: old.kind,
      status: "missing",
      old_checksum: old.checksum,
      old_size: old.size,
    })
  }
  return changes.sort((a, b) => {
    if (a.kind !== b.kind) return a.kind === "entry" ? -1 : 1
    return a.path.localeCompare(b.path)
  })
}

export function statusLabel(status: SkillFileChange["status"]) {
  switch (status) {
    case "entry":
      return "入口"
    case "unchanged":
      return "未变"
    case "modified":
      return "已修改"
    case "same_path":
      return "同路径"
    case "added":
      return "新增"
    case "missing":
      return "缺失"
    default:
      return status
  }
}

export function statusClass(status: SkillFileChange["status"]) {
  if (status === "modified" || status === "same_path") return "text-amber-600 dark:text-amber-300"
  if (status === "added") return "text-emerald-600 dark:text-emerald-300"
  if (status === "missing") return "text-destructive"
  return "text-muted-foreground"
}

export function formatBytes(size: number) {
  if (size < 1024) return `${size}B`
  if (size < 1024 * 1024) return `${Math.round(size / 102.4) / 10}KB`
  return `${Math.round(size / 1024 / 102.4) / 10}MB`
}

export function upsertSkill(list: SkillDefinition[], skill: SkillDefinition) {
  const exists = list.some((item) => item.id === skill.id)
  const next = exists ? list.map((item) => (item.id === skill.id ? skill : item)) : [skill, ...list]
  return next.sort((a, b) => a.name.localeCompare(b.name))
}

export function mergeSkills(list: SkillDefinition[], incoming: SkillDefinition[]) {
  return incoming.reduce((acc, skill) => upsertSkill(acc, skill), list)
}

export function importReportLines(report?: { skipped?: string[]; deduped?: string[]; details?: string[] }) {
  return [...(report?.details || []), ...(report?.skipped || []), ...(report?.deduped || [])]
}

export function defaultSelectedFiles(skills: SkillImportPreview[]) {
  const out: Record<string, string[]> = {}
  for (const skill of skills) {
    out[skill.source_path] = []
  }
  return out
}

export function fileReasonLabel(reason?: string) {
  switch (reason) {
    case "entry":
      return "入口"
    case "explicit":
      return "引用"
    case "candidate":
      return "可选"
    case "selected":
      return "选中"
    default:
      return "文件"
  }
}
