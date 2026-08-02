import { api } from "./client"
import type { FontAsset, Model, Prompt, SkillDefinition, SkillFileSummary, UserGroup } from "@/types"
import type { PromptInput, PromptUpdate } from "./prompts"

export interface AdminUser {
  id: number
  username: string
  email?: string
  nickname?: string
  role: "admin" | "user"
  group_id?: number
  permissions?: Record<string, unknown>
  is_active: boolean
  created_at: string
  last_login_at?: string
}

export interface ConfigOption {
  value: number
  label: string
}

export interface ConfigItem {
  key: string
  value: unknown
  default?: unknown
  description?: string
  config_type: string
  display_name?: string
  category?: string
  sort_order?: number
  options?: ConfigOption[]
}

export interface CreateAdminUserInput {
  username: string
  password: string
  email?: string
  nickname?: string
  role: "admin" | "user"
  is_active: boolean
}

export interface UpdateAdminUserInput {
  email?: string
  nickname?: string
  role?: "admin" | "user"
  is_active?: boolean
}

export interface SkillInput {
  id?: string
  name: string
  description?: string
  entry_content: string
  files?: { path: string; content: string }[]
  enabled?: boolean
  min_group_level?: number
}

export interface SkillImportResult {
  skills: SkillDefinition[]
  report: {
    imported: number
    skipped?: string[]
    deduped?: string[]
    details?: string[]
  }
}

export interface SkillImportPreview {
  id: string
  name: string
  description: string
  source_path: string
  checksum: string
  dependencies?: string[]
  files?: SkillFileSummary[]
  entry_preview?: string
  entry_truncated?: boolean
  existing_skill?: SkillDefinition
  match_type?: "id" | "name" | "path"
  default_action?: "create" | "update" | "review"
}

export interface SkillGitPreviewResult {
  branches: string[]
  selected_ref: string
  skills: SkillImportPreview[]
  report: {
    imported: number
    skipped?: string[]
    deduped?: string[]
    details?: string[]
  }
}

export interface SkillZipPreviewResult {
  skills: SkillImportPreview[]
  report: {
    imported: number
    skipped?: string[]
    deduped?: string[]
    details?: string[]
  }
}

export interface SkillFileChange {
  path: string
  kind: "entry" | "reference"
  status: "entry" | "unchanged" | "modified" | "same_path" | "added" | "missing"
  old_checksum?: string
  new_checksum?: string
  old_size?: number
  new_size?: number
  reason?: "entry" | "explicit" | "candidate" | "selected"
  selected_default?: boolean
}

export interface SkillUpdatePreviewResult {
  current: SkillDefinition
  candidates: SkillImportPreview[]
  selected_source_path: string
  match_type?: "id" | "name" | "path"
  default_selected_files: string[]
  file_changes: SkillFileChange[]
  current_entry_preview: string
  current_entry_truncated?: boolean
  report: SkillImportResult["report"]
}

export interface SkillImportRecord {
  id: number
  skill_id: string
  action: "create" | "update"
  source_type: "manual" | "git" | "zip"
  source_url?: string
  source_ref?: string
  source_path: string
  upstream_skill_id: string
  upstream_name: string
  package_checksum: string
  selected_files: unknown
  file_manifest: unknown
  import_report: unknown
  created_by?: number
  created_at: string
}

export interface FontInput {
  display_name?: string
  family_name?: string
  weight?: number
  style?: "normal" | "italic" | "oblique"
  enabled?: boolean
}

export interface ChatFontSelection {
  chinese?: number | null
  latin?: number | null
  code?: number | null
}

export type UsageRange = "today" | "7d" | "30d"
export type UsageQuery = UsageRange | { start_at: string; end_at: string }

export interface UsageTotals {
  requests: number
  successes: number
  failures: number
  canceled: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  cached_tokens: number
  reasoning_tokens: number
  tool_calls?: number
  web_search_calls?: number
  web_extract_calls?: number
  ocr_files?: number
  ocr_pages?: number
  ocr_failures?: number
  tool_context_tokens?: number
  avg_duration_ms: number
  last_called_at?: string
}

export interface RunUsageTotals {
  runs: number
  running: number
  completed: number
  failed: number
  canceled: number
  user_stopped: number
  system_canceled: number
  avg_duration_ms: number
  last_accepted_at?: string
}

export interface UsageByUser extends UsageTotals {
  user_id?: number
  username: string
}

export interface UsageByModel extends UsageTotals {
  provider: string
  model_id: string
}

export interface UsageByKind extends UsageTotals {
  kind: "chat" | "retry" | "title" | "compression" | "tool_chain"
}

export interface ToolUsageTotals {
  calls: number
  successes: number
  failures: number
  degraded: number
  web_search_calls: number
  web_extract_calls: number
  context_tokens: number
  truncated: number
  avg_duration_ms: number
  last_called_at?: string
}

export interface UsageByTool extends ToolUsageTotals {
  tool_key: string
}

export interface QuotaUserUsage {
  user_id: number
  username: string
  group_id?: number
  group_name: string
  daily_message_limit: number
  daily_token_limit: number
  concurrent_run_limit: number
  daily_tool_call_limit: number
  daily_web_search_limit: number
  daily_web_extract_limit: number
  daily_ocr_file_limit: number
  daily_ocr_page_limit: number
  daily_messages: number
  daily_model_tokens: number
  daily_tool_calls: number
  daily_web_searches: number
  daily_web_extracts: number
  daily_ocr_files: number
  daily_ocr_pages: number
  reset_at: string
}

export interface AdminUsageResponse {
  totals: UsageTotals
  run_totals: RunUsageTotals
  by_user: UsageByUser[]
  by_model: UsageByModel[]
  by_kind: UsageByKind[]
  tool_totals: ToolUsageTotals
  by_tool: UsageByTool[]
  quota_users: QuotaUserUsage[]
}

export interface AdminSystemStatus {
  version: string
  build_ref: string
  schema_version: string
  started_at: string
  uptime_seconds: number
  runtime: {
    go_version: string
    cpu_count: number
    goroutines: number
    heap_alloc_bytes: number
    container_memory_used_bytes?: number
    container_memory_limit_bytes?: number
  }
  storage: {
    total_bytes: number
    free_bytes: number
  }
  database: {
    ok: boolean
    latency_ms: number
    open_connections: number
    in_use_connections: number
    idle_connections: number
  }
  extractor: {
    enabled: boolean
    ok: boolean
    latency_ms: number
  }
}

export interface ModelTestInput {
  id: string
  provider: string
}

export interface ModelTestResponse {
	ok: boolean
	model_id: string
	provider: string
	scope?: "minimal_chat_connectivity"
	duration_ms?: number
  output?: string
  error?: string
}

export type AIChannelAdapter = "openai_compatible" | "openai_responses" | "anthropic" | "google"

export interface AIChannel {
  id: number
  key: string
  display_name: string
  adapter: AIChannelAdapter
  base_url: string
  api_key_set: boolean
  enabled: boolean
  sort_order: number
  created_at: string
  updated_at: string
}

export interface AIChannelInput {
  key: string
  display_name: string
  adapter: AIChannelAdapter
  base_url: string
  api_key?: string
  enabled: boolean
  sort_order: number
}

export type ExternalServiceKind = "search" | "crawler" | "ocr"

export interface ExternalService {
  id: number
  key: string
  display_name: string
  kind: ExternalServiceKind
  base_url: string
  api_key_set: boolean
  enabled: boolean
  sort_order: number
  max_concurrency: number
  created_at: string
  updated_at: string
}

export interface ExternalServiceInput {
  key: string
  display_name: string
  kind: ExternalServiceKind
  base_url: string
  api_key?: string
  enabled: boolean
  sort_order: number
  max_concurrency?: number
}

export interface ExternalServiceTestResult {
  ok: boolean
  duration_ms?: number
  error?: string
}

export interface ToolConfig {
  id: number
  key: string
  display_name: string
  enabled: boolean
  timeout_seconds: number
  sort_order: number
  created_at: string
  updated_at: string
}

export interface ToolConfigInput {
  key: string
  display_name: string
  enabled: boolean
  timeout_seconds: number
  sort_order: number
}

export interface CleanupOrphanFilesResponse {
  marked: number
  removed: number
  failed: number
  failures: Array<{ file_id: number; error: string }>
  skipped_referenced: number
  ocr_expired_count?: number
  ocr_expired_removed?: number
  older_than_hours: number
}

export interface CreateModelInput {
  id: string
  display_name: string
  provider: string
  vision: boolean
  tool_use: boolean
  reasoning: boolean
  thinking_format: string
  search_impl: string
  context_window: number
  max_output: number
  enabled: boolean
  min_group_level: number
  sort_order: number
  catalog_source?: Model["catalog_source"]
  catalog_checked_at?: string
  lifecycle_status?: Model["lifecycle_status"]
}

export interface UpdateModelInput {
  display_name?: string
  provider?: string
  vision?: boolean
  tool_use?: boolean
  reasoning?: boolean
  thinking_format?: string
  search_impl?: string
  context_window?: number
  max_output?: number
  enabled?: boolean
  min_group_level?: number
  sort_order?: number
  catalog_source?: Model["catalog_source"]
  catalog_checked_at?: string
  lifecycle_status?: Model["lifecycle_status"]
}

export interface CreateGroupInput {
  name: string
  level: number
  description?: string
  is_default?: boolean
  daily_message_limit?: number
  daily_token_limit?: number
  concurrent_run_limit?: number
  daily_tool_call_limit?: number
  daily_web_search_limit?: number
  daily_web_extract_limit?: number
  daily_ocr_file_limit?: number
  daily_ocr_page_limit?: number
}

export interface UpdateGroupInput {
  name?: string
  level?: number
  description?: string
  is_default?: boolean
  daily_message_limit?: number
  daily_token_limit?: number
  concurrent_run_limit?: number
  daily_tool_call_limit?: number
  daily_web_search_limit?: number
  daily_web_extract_limit?: number
  daily_ocr_file_limit?: number
  daily_ocr_page_limit?: number
}

export const adminApi = {
  listUsers(limit = 100, offset = 0) {
    return api.get<{ users: AdminUser[]; total: number }>(`/admin/users?limit=${limit}&offset=${offset}`)
  },

  updateUser(id: number, data: UpdateAdminUserInput) {
    return api.patch<AdminUser>(`/admin/users/${id}`, data)
  },

  createUser(data: CreateAdminUserInput) {
    return api.post<AdminUser>("/admin/users", data)
  },

  resetUserPassword(id: number, password: string) {
    return api.put<{ message: string }>(`/admin/users/${id}/password`, { password })
  },

  setUserGroup(id: number, groupId: number | null) {
    return api.put<AdminUser>(`/admin/users/${id}/group`, { group_id: groupId })
  },

  listGroups() {
    return api.get<{ groups: UserGroup[]; total: number }>("/admin/groups")
  },

  createGroup(data: CreateGroupInput) {
    return api.post<UserGroup>("/admin/groups", data)
  },

  updateGroup(id: number, data: UpdateGroupInput) {
    return api.patch<UserGroup>(`/admin/groups/${id}`, data)
  },

  deleteGroup(id: number) {
    return api.delete<{ message: string }>(`/admin/groups/${id}`)
  },

  listModels() {
    return api.get<{ models: Model[]; total: number }>("/models")
  },

  listAvailableModels(provider?: string) {
    const query = provider ? `?provider=${encodeURIComponent(provider)}` : ""
    return api.get<{ models: Model[]; total: number }>(`/admin/models/available${query}`)
  },

  listCatalogModels() {
    return api.get<{ models: Model[]; total: number }>("/admin/models/catalog")
  },

  // 按 ID 拉取 models.dev 的原始能力，用于编辑面板刷新字段（不回显 DB 记录）。
  getCatalogModel(id: string, provider?: string) {
    const query = provider ? `?provider=${encodeURIComponent(provider)}` : ""
    return api.get<{ model: Model }>(`/admin/models/catalog/${encodeURIComponent(id)}${query}`)
  },

  createModel(data: CreateModelInput) {
    return api.post<Model>("/admin/models", data)
  },

  updateModel(id: string, data: UpdateModelInput) {
    return api.patch<Model>(`/admin/models/${encodeURIComponent(id)}`, data)
  },

  deleteModel(id: string) {
    return api.delete<{ message: string }>(`/admin/models/${encodeURIComponent(id)}`)
  },

  testModel(data: ModelTestInput) {
    return api.post<ModelTestResponse>("/admin/models/test", data)
  },

  listChannels() {
    return api.get<{ channels: AIChannel[] }>("/admin/channels")
  },

  saveChannel(data: AIChannelInput) {
    return api.post<AIChannel>("/admin/channels", data)
  },

  deleteChannel(key: string) {
    return api.delete<{ message: string }>(`/admin/channels/${encodeURIComponent(key)}`)
  },

  listExternalServices() {
    return api.get<{ services: ExternalService[] }>("/admin/external-services")
  },

  saveExternalService(data: ExternalServiceInput) {
    return api.post<ExternalService>("/admin/external-services", data)
  },

  reorderExternalServices(kind: Exclude<ExternalServiceKind, "ocr">, keys: string[]) {
    return api.put<{ services: ExternalService[] }>("/admin/external-services/order", { kind, keys })
  },

  testExternalService(data: ExternalServiceInput) {
    return api.post<ExternalServiceTestResult>("/admin/external-services/test", data)
  },

  deleteExternalService(key: string) {
    return api.delete<{ message: string }>(`/admin/external-services/${encodeURIComponent(key)}`)
  },

  listToolConfigs() {
    return api.get<{ tools: ToolConfig[] }>("/admin/tools")
  },

  saveToolConfig(data: ToolConfigInput) {
    return api.post<ToolConfig>("/admin/tools", data)
  },

  cleanupOrphanFiles(params?: { older_than_hours?: number; limit?: number }) {
    const search = new URLSearchParams()
    if (params?.older_than_hours) search.set("older_than_hours", String(params.older_than_hours))
    if (params?.limit) search.set("limit", String(params.limit))
    const query = search.toString()
    return api.post<CleanupOrphanFilesResponse>(`/admin/files/cleanup-orphans${query ? `?${query}` : ""}`)
  },

  getUsage(query: UsageQuery = "7d") {
    const params = new URLSearchParams()
    if (typeof query === "string") params.set("range", query)
    else {
      params.set("start_at", query.start_at)
      params.set("end_at", query.end_at)
    }
    return api.get<AdminUsageResponse>(`/admin/usage?${params.toString()}`)
  },

  getSystemStatus() {
    return api.get<AdminSystemStatus>("/admin/system/status")
  },

  listConfig() {
    return api.get<{ config: ConfigItem[] }>("/admin/config")
  },

  updateConfig(key: string, value: unknown) {
    return api.patch<{ message: string; key: string }>(`/admin/config/${key}`, { value })
  },

  updateConfigs(updates: Record<string, unknown>) {
    return api.patch<{ message: string; updated: number }>("/admin/config", { updates })
  },

  listPrompts(limit = 100, offset = 0) {
    return api.get<{ prompts: Prompt[]; total: number }>(`/admin/prompts?limit=${limit}&offset=${offset}`)
  },

  createPrompt(data: PromptInput) {
    return api.post<Prompt>("/admin/prompts", data)
  },

  updatePrompt(id: number, data: PromptUpdate) {
    return api.patch<Prompt>(`/admin/prompts/${id}`, data)
  },

  deletePrompt(id: number) {
    return api.delete<{ message: string }>(`/admin/prompts/${id}`)
  },

  listSkills() {
    return api.get<{ skills: SkillDefinition[] }>("/admin/skills")
  },

  createSkill(data: SkillInput) {
    return api.post<SkillDefinition>("/admin/skills", data)
  },

  updateSkill(id: string, data: Partial<SkillInput>) {
    return api.patch<SkillDefinition>(`/admin/skills/${encodeURIComponent(id)}`, data)
  },

  deleteSkill(id: string) {
    return api.delete<{ message: string }>(`/admin/skills/${encodeURIComponent(id)}`)
  },

  listSkillFiles(id: string) {
    return api.get<{ files: SkillFileSummary[] }>(`/admin/skills/${encodeURIComponent(id)}/files`)
  },

  getSkillFileContent(id: string, path: string) {
    return api.get<{ file: SkillFileSummary; content: string }>(
      `/admin/skills/${encodeURIComponent(id)}/files/content?path=${encodeURIComponent(path)}`
    )
  },

  listSkillImportRecords(id: string) {
    return api.get<{ records: SkillImportRecord[] }>(`/admin/skills/${encodeURIComponent(id)}/import-records`)
  },

  previewSkillGitUpdate(id: string, ref?: string) {
    return api.post<SkillUpdatePreviewResult>(`/admin/skills/${encodeURIComponent(id)}/update/git/preview`, { ref })
  },

  applySkillGitUpdate(id: string, sourcePath: string, selectedFiles: string[], ref?: string, url?: string) {
    return api.post<SkillImportResult>(`/admin/skills/${encodeURIComponent(id)}/update/git`, {
      source_path: sourcePath,
      selected_files: selectedFiles,
      ref,
      url,
    })
  },

  previewSkillZipUpdate(id: string, file: File) {
    const form = new FormData()
    form.append("file", file)
    return api.upload<SkillUpdatePreviewResult>(`/admin/skills/${encodeURIComponent(id)}/update/zip/preview`, form)
  },

  applySkillZipUpdate(id: string, file: File, sourcePath: string, selectedFiles: string[]) {
    const form = new FormData()
    form.append("file", file)
    form.append("source_path", sourcePath)
    form.append("selected_files", JSON.stringify(selectedFiles))
    return api.upload<SkillImportResult>(`/admin/skills/${encodeURIComponent(id)}/update/zip`, form)
  },

  importSkillsFromGit(url: string, ref?: string, selectedPaths?: string[], selectedFiles?: Record<string, string[]>) {
    return api.post<SkillImportResult>("/admin/skills/import/git", { url, ref, selected_paths: selectedPaths, selected_files: selectedFiles })
  },

  previewSkillsFromGit(url: string, ref?: string) {
    return api.post<SkillGitPreviewResult>("/admin/skills/import/git/preview", { url, ref })
  },

  previewSkillsFromZip(file: File) {
    const form = new FormData()
    form.append("file", file)
    return api.upload<SkillZipPreviewResult>("/admin/skills/import/zip/preview", form)
  },

  importSkillsFromZip(file: File, selectedPaths?: string[], selectedFiles?: Record<string, string[]>) {
    const form = new FormData()
    form.append("file", file)
    if (selectedPaths) form.append("selected_paths", JSON.stringify(selectedPaths))
    if (selectedFiles) form.append("selected_files", JSON.stringify(selectedFiles))
    return api.upload<SkillImportResult>("/admin/skills/import/zip", form)
  },

  listFonts() {
    return api.get<{ fonts: FontAsset[]; selected_font_id?: number | null; selected_font_ids?: ChatFontSelection }>("/admin/fonts")
  },

  uploadFont(file: File, data: FontInput & { make_current?: boolean }) {
    const form = new FormData()
    form.append("file", file)
    if (data.display_name) form.append("display_name", data.display_name)
    if (data.family_name) form.append("family_name", data.family_name)
    if (data.weight) form.append("weight", String(data.weight))
    if (data.style) form.append("style", data.style)
    if (data.make_current) form.append("make_current", "true")
    return api.upload<FontAsset>("/admin/fonts", form)
  },

  updateFont(id: number, data: FontInput) {
    return api.patch<FontAsset>(`/admin/fonts/${id}`, data)
  },

  selectFont(id: number | null, slot?: keyof ChatFontSelection) {
    return api.put<{ selected_font_id?: number | null; selected_font_ids?: ChatFontSelection }>("/admin/fonts/selected", { font_id: id, slot })
  },

  deleteFont(id: number) {
    return api.delete<{ message: string }>(`/admin/fonts/${id}`)
  },
}
