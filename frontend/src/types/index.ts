export interface User {
  id: number
  username: string
  email?: string
  nickname?: string
  avatar_url?: string
  role: "admin" | "user"
  is_active: boolean
  created_at: string
  updated_at: string
}

export interface Session {
  id: number
  user_id: number
  folder_id?: number | null
  pinned_at?: string | null
  title: string
  title_generated: boolean
  model_id: string
  provider: string
  system_prompt?: string
  temperature?: number
  max_tokens?: number
  message_format?: string
  search_mode?: "off" | "auto"
  memory_enabled?: boolean
  metadata?: Record<string, unknown>
  created_at: string
  updated_at: string
}

export interface SessionFolder {
  id: number
  user_id: number
  name: string
  pinned_at?: string | null
  created_at: string
  updated_at: string
}

export interface AttachmentMeta {
  file_id: number
  filename: string
  file_type: string
  size: number
  token_estimate?: number
  unavailable?: boolean
}

export interface MessageData {
  role: "user" | "assistant" | "system" | "tool"
  content: string
  thinking?: string
  reasoning_content?: string
  tool_calls?: ToolCall[]
  tool_call_id?: string
  tool_name?: string
  response_meta?: ResponseMeta
  runtime?: RuntimeMeta
  segments?: AssistantSegment[]
  attachments?: AttachmentMeta[]
  metadata?: Record<string, unknown>
}

export type LocalMessageState = "pending" | "streaming" | "syncing" | "finalizing" | "failed_local" | "persisted"

export type StreamLifecycleState = "idle" | "sending" | "streaming" | "recovering" | "syncing" | "finalizing" | "failed_local"

export interface ModelRetryTrace {
  attempt: number
  maxAttempts: number
  delayMs: number
  category: string
}

export interface AssistantSegment {
  content?: string
  thinking?: string
  tool_calls?: ToolCall[]
}

export interface StreamingSegment extends AssistantSegment {
  type: "content" | "tool"
}

export interface Message {
  id: number
  session_id: number
  schema_version: string
  message_data: MessageData
  role: string
  has_tool_calls: boolean
  has_reasoning: boolean
  answer_attempt_id?: number
  answer_navigation?: AnswerAttemptNavigation
  created_at: string
  local_state?: LocalMessageState
  local_request_id?: string
  local_error?: string
  is_local?: boolean
}

export interface AnswerAttemptNavigation {
  attempt_id: number
  attempt_number: number
  attempt_count: number
  previous_attempt_id?: number
  next_attempt_id?: number
  can_switch: boolean
}

export interface ToolCall {
  id: string
  name?: string
  tool_name?: string
  function?: {
    name?: string
    arguments?: string
  }
  arguments?: string
  result?: string
  status?: "running" | "done" | "error"
  children?: ToolCall[]
}

export interface ActiveRunSnapshot {
  run_id: string
  session_id: number
  user_message_id: number
  kind?: "chat" | "compaction"
  status: "running" | "completed" | "failed" | "canceled"
  cursor: number
  content: string
  thinking?: string
  tool_calls?: ToolCall[]
  segments?: StreamingSegment[]
  error?: string
  error_code?: string
  request_id?: string
  replay_from?: number
  output_truncated?: boolean
}

export interface DurableRunStatus {
  run_id: string
  session_id: number
  kind: "chat" | "compaction"
  status: "running" | "completed" | "failed" | "canceled"
  user_message_id?: number
  terminal_message_id?: number
  error_code?: string
  error?: string
}

export interface Usage {
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  cached_tokens?: number
  reasoning_tokens?: number
  prompt_token_details?: {
    cached_tokens?: number
  }
  completion_token_details?: {
    reasoning_tokens?: number
  }
}

export interface ResponseMeta {
  finish_reason?: string
  raw_finish_reason?: string
  usage?: Usage
}

export interface RuntimeMeta {
  duration_ms?: number
  tokens_per_second?: number
}

export type CodeBlockMode = "preview" | "source"

export type ReasoningStateSource = "user" | "system"

export interface ReasoningViewState {
  open: boolean
  touchedByUser: boolean
  autoCollapsing?: boolean
}

export interface CodeBlockViewState {
  mode: CodeBlockMode
  expanded: boolean
}

export interface Model {
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
  resolved_thinking_format?: string
  default_thinking_effort?: string
  thinking_effort_options?: ThinkingEffortOption[]
  runtime_profile?: ModelRuntimeProfile
  channel_display_name?: string
  channel_adapter?: string
  channel_enabled?: boolean
  channel_configured?: boolean
}

export interface ThinkingEffortOption {
  value: string
  label: string
  desc: string
}

export interface ModelRuntimeProfile {
  family: string
  wire_protocol: string
  thinking_format: string
  default_thinking_effort?: string
  thinking_effort_options?: ThinkingEffortOption[]
  supports_vision: boolean
  supports_tools: boolean
  search_impl: string
}

// UserGroup 用户分级组，level 越大权限越高
export interface UserGroup {
  id: number
  name: string
  level: number
  description: string
  is_default: boolean
  daily_message_limit: number
  daily_token_limit: number
  concurrent_run_limit: number
  daily_tool_call_limit: number
  daily_web_search_limit: number
  daily_web_extract_limit: number
  daily_ocr_file_limit: number
  daily_ocr_page_limit: number
  created_at: string
  updated_at: string
}

export interface Prompt {
  id: number
  user_id?: number
  title: string
  content: string
  description?: string
  tags: string[]
  group_id?: number | null
  group_name: string
  is_public: boolean
  use_count: number
  created_at: string
  updated_at: string
}

export interface PromptGroup {
  id: number
  user_id: number
  name: string
  created_at: string
  updated_at: string
}

export interface SkillDefinition {
  id: string
  name: string
  description: string
  source_type: "builtin" | "manual" | "git" | "zip"
  source_url?: string
  source_ref?: string
  source_path?: string
  checksum: string
  package_checksum: string
  entry_path: "SKILL.md"
  min_group_level: number
  files: SkillFileSummary[]
  enabled: boolean
  is_builtin: boolean
  authorized: boolean
  created_by?: number
  created_at: string
  updated_at: string
}

export interface SkillFileSummary {
  path: string
  kind: "entry" | "reference"
  size: number
  checksum: string
  reason?: "entry" | "explicit" | "candidate" | "selected"
  selected_default?: boolean
}

export interface FontAsset {
  id: number
  display_name: string
  family_name: string
  file_name: string
  file_url?: string
  mime_type: string
  file_size: number
  checksum: string
  weight: number
  style: "normal" | "italic" | "oblique"
  enabled: boolean
  created_by?: number
  created_at: string
  updated_at: string
}

export interface AuthResponse {
  token: string
  user: User
}

export interface RegisterResponse {
  approved: boolean
  message: string
  token?: string
  user?: User
}

export interface SSEEvent {
  event: "message_start" | "content_delta" | "thinking_delta" | "assistant_attempt_reset" | "model_retry" | "tool_call_start" | "tool_call_result" | "message_complete" | "error" | "ping"
  data: Record<string, unknown>
}
