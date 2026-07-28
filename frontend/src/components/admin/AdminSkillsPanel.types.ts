import type { SkillImportPreview, SkillImportResult, SkillInput, SkillUpdatePreviewResult } from "@/api/admin"

export type ReferenceDraft = { path: string; content: string }
export type SkillDraft = SkillInput & { originalId?: string; files: ReferenceDraft[] }
export type ImportReport = SkillImportResult["report"]

export type ImportDialogState =
  | {
      source: "git"
      title: string
      url: string
      ref: string
      skills: SkillImportPreview[]
      selectedPaths: string[]
      selectedFiles: Record<string, string[]>
      report: ImportReport
    }
  | {
      source: "zip"
      title: string
      file: File
      skills: SkillImportPreview[]
      selectedPaths: string[]
      selectedFiles: Record<string, string[]>
      report: ImportReport
    }

export type UpdateDialogState = {
  source: "git" | "zip"
  title: string
  file?: File
  ref?: string
  preview: SkillUpdatePreviewResult
  selectedSourcePath: string
  selectedFiles: Record<string, string[]>
  pendingUpdates?: SkillImportPreview[]
  pendingCreates?: string[]
}
