import type { UpdateModelInput } from "@/api/admin"
import type { Model, UserGroup } from "@/types"
import { providerDefaults, thinkingFormatOptions } from "./AdminModelsPanel.constants"
import type { GroupLevelOption, ModelDraft } from "./AdminModelsPanel.types"

export function thinkingFormatLabel(value: string) {
  return thinkingFormatOptions.find((item) => item.value === value)?.label || value || "默认"
}

// Keep new-model defaults in one place so the create form, provider switch, and gateway import share the same baseline.
export function makeEmptyModel(provider = "openai"): ModelDraft {
  return {
    id: "",
    display_name: "",
    provider,
    vision: false,
    tool_use: true,
    reasoning: false,
    thinking_format: "auto",
    search_impl: "",
    context_window: 0,
    max_output: 0,
    enabled: true,
    min_group_level: 0,
    sort_order: 0,
    catalog_source: "manual",
    catalog_checked_at: null,
    lifecycle_status: "unknown",
    ...providerDefaults[provider],
  }
}

// Convert API models into a form draft. Runtime-only fields stay out of the draft to avoid accidental writes.
export function toModelDraft(model: Model): ModelDraft {
  return {
    id: model.id,
    display_name: model.display_name,
    provider: model.provider,
    vision: model.vision,
    tool_use: model.tool_use,
    reasoning: model.reasoning,
    thinking_format: model.thinking_format || "auto",
    search_impl: model.search_impl,
    context_window: model.context_window,
    max_output: model.max_output,
    enabled: model.enabled,
    min_group_level: model.min_group_level ?? 0,
    sort_order: model.sort_order,
    catalog_source: model.catalog_source || "manual",
    catalog_checked_at: model.catalog_checked_at || null,
    lifecycle_status: model.lifecycle_status || "unknown",
  }
}

// Update requests intentionally omit id; changing model identity must go through create/delete rather than patch.
export function toModelPatch(draft: ModelDraft): UpdateModelInput {
  return {
    display_name: draft.display_name,
    provider: draft.provider,
    vision: draft.vision,
    tool_use: draft.tool_use,
    reasoning: draft.reasoning,
    thinking_format: draft.thinking_format || "auto",
    search_impl: draft.search_impl,
    context_window: draft.context_window,
    max_output: draft.max_output,
    enabled: draft.enabled,
    min_group_level: draft.min_group_level,
    sort_order: draft.sort_order,
    catalog_source: draft.catalog_source,
    catalog_checked_at: draft.catalog_checked_at,
    lifecycle_status: draft.lifecycle_status,
  }
}

// Catalog matching only fills capability fields. Display name, id, provider, visibility, and ordering remain admin-owned.
export function catalogModelPatch(model: Model): Partial<ModelDraft> {
  return {
    context_window: model.context_window,
    max_output: model.max_output,
    vision: model.vision,
    tool_use: model.tool_use,
    reasoning: model.reasoning,
    thinking_format: model.thinking_format || "auto",
    search_impl: model.search_impl,
    catalog_source: model.catalog_source,
    catalog_checked_at: model.catalog_checked_at || null,
    lifecycle_status: model.lifecycle_status,
  }
}

const catalogOwnedFields: Array<keyof ModelDraft> = [
  "context_window", "max_output", "vision", "tool_use", "reasoning",
  "thinking_format", "search_impl", "lifecycle_status",
]

// Manual capability edits override imported directory evidence. Catalog
// workflows carry catalog_source explicitly and therefore bypass this step.
export function markManualCatalogOverride(patch: Partial<ModelDraft>, current?: Pick<ModelDraft, "lifecycle_status">): Partial<ModelDraft> {
  if (patch.catalog_source !== undefined || !catalogOwnedFields.some((field) => patch[field] !== undefined)) {
    return patch
  }
  return {
    ...patch,
    catalog_source: "manual",
    catalog_checked_at: null,
    lifecycle_status: patch.lifecycle_status ?? current?.lifecycle_status ?? "unknown",
  }
}

export function catalogSelectionKey(model: Pick<Model, "provider" | "id">) {
  return JSON.stringify([model.provider, model.id])
}

export function sortModels(models: Model[]) {
  return [...models].sort((a, b) => a.sort_order - b.sort_order || a.id.localeCompare(b.id))
}

// 0 means every user can see the model; existing custom levels stay visible even if no group currently owns them.
export function groupLevelOptions(groups: UserGroup[], current: number): GroupLevelOption[] {
  const byLevel = new Map<number, string>()
  byLevel.set(0, "所有人可见 (0)")
  for (const g of groups) {
    byLevel.set(g.level, `${g.name} (${g.level})`)
  }
  if (!byLevel.has(current)) {
    byLevel.set(current, `自定义等级 (${current})`)
  }
  return [...byLevel.entries()]
    .sort((a, b) => a[0] - b[0])
    .map(([level, label]) => ({ level, label }))
}

export function nextSortOrder(models: Model[]) {
  if (models.length === 0) return 0
  return Math.max(...models.map((model) => model.sort_order)) + 10
}

export function formatContext(value: number) {
  if (!value) return "-"
  if (value >= 1000000) return `${Math.round(value / 1000000)}M`
  if (value >= 1000) return `${Math.round(value / 1000)}K`
  return String(value)
}

export function formatDuration(ms: number) {
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

export function catalogSourceLabel(source: Model["catalog_source"]) {
  if (source === "models_dev") return "models.dev"
  if (source === "channel") return "渠道目录"
  if (source === "builtin") return "内置兜底"
  if (source === "manual") return "管理员"
  return "未知来源"
}

export function lifecycleStatusLabel(status: Model["lifecycle_status"]) {
  if (status === "active") return "可用"
  if (status === "preview") return "预览"
  if (status === "deprecated") return "弃用中"
  if (status === "retired") return "已退役"
  return "待核对"
}

export function formatCatalogCheckedAt(value?: string | null) {
  if (!value) return "未核对"
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return "未核对"
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit",
  }).format(date)
}
