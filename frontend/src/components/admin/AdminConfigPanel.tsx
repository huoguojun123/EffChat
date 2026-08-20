import { useEffect, useMemo, useRef, useState } from "react"
import { adminApi, type ConfigItem } from "@/api/admin"
import type { Model } from "@/types"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Save } from "lucide-react"
import { useChatStore } from "@/stores/chat"

interface Props {
  configItems: ConfigItem[]
  setConfigItems: React.Dispatch<React.SetStateAction<ConfigItem[]>>
  models: Model[]
  setError: (error: string) => void
  includeKeys?: string[]
  excludeKeys?: string[]
  onDirtyChange?: (dirty: boolean) => void
}

const categoryOrder = ["提示词", "基础", "系统小模型", "会话记忆", "会话压缩", "联网与提取", "文件"]
const hiddenConfigKeys = new Set(["compression_model", "memory_maintenance_model"])
const modelConfigKeys = new Set(["default_model_id", "title_generation_model", "extract_summary_model"])
const utilityConfigHelp: Record<string, string> = {
  title_generation_model: "用户第二轮后生成会话标题，适合便宜、快、短输出稳定的模型。",
  extract_summary_model: "仅在网页正文超限时，对本地筛选后的候选正文做快速提炼；关闭或失败时仍返回相关原文。",
  title_generation_trigger: "达到第几条用户消息后自动生成标题；0 表示关闭自动标题。",
}
const utilityConfigOrder = ["title_generation_model", "extract_summary_model", "title_generation_trigger"]
const uploadTypeOptions = [
  { label: "PNG", value: "image/png" },
  { label: "JPEG", value: "image/jpeg" },
  { label: "GIF", value: "image/gif" },
  { label: "WebP", value: "image/webp" },
  { label: "PDF", value: "application/pdf" },
  { label: "纯文本", value: "text/plain" },
  { label: "Markdown", value: "text/markdown" },
  { label: "Word", value: "application/vnd.openxmlformats-officedocument.wordprocessingml.document" },
  { label: "表格", value: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" },
  { label: "CSV", value: "text/csv" },
]
const supportedImageTypes = uploadTypeOptions.slice(0, 4).map((item) => item.value)
// 与后端 prompt_builder.go 的占位符表保持一致：缺一个都会让用户以为某能力不可用。
const promptPlaceholders = [
  { label: "系统名称", token: "{{system_name}}" },
  { label: "日期", token: "{{date}}" },
  { label: "时间", token: "{{time}}" },
  { label: "日期时间", token: "{{datetime}}" },
  { label: "时区", token: "{{timezone}}" },
  { label: "用户名", token: "{{user_name}}" },
  { label: "昵称", token: "{{user_nickname}}" },
  { label: "显示名", token: "{{user_display_name}}" },
  { label: "角色", token: "{{user_role}}" },
  { label: "用户信息", token: "{{user_info}}" },
  { label: "用户偏好", token: "{{user_preferences}}" },
  { label: "会话标题", token: "{{session_title}}" },
  { label: "模型 ID", token: "{{model_id}}" },
  { label: "提供商", token: "{{provider}}" },
  { label: "消息格式", token: "{{message_format}}" },
  { label: "温度", token: "{{temperature}}" },
  { label: "最大输出", token: "{{max_tokens}}" },
  { label: "搜索模式", token: "{{search_mode}}" },
  { label: "会话信息", token: "{{session_info}}" },
  { label: "会话偏好", token: "{{session_preferences}}" },
  { label: "会话要求", token: "{{session_prompt}}" },
  { label: "模型能力", token: "{{capabilities}}" },
]

type ConfigConfirmation = {
  title: string
  description: string
  confirmLabel: string
  action: () => void
}

export function AdminConfigPanel({
  configItems,
  setConfigItems,
  models,
  setError,
  includeKeys,
  excludeKeys,
  onDirtyChange,
}: Props) {
  const loadSessionCreateReadiness = useChatStore((state) => state.loadSessionCreateReadiness)
  const [drafts, setDrafts] = useState<Record<string, string>>({})
  const [saving, setSaving] = useState(false)
  const [cleanupSaving, setCleanupSaving] = useState(false)
  const [cleanupResult, setCleanupResult] = useState("")
  const [confirmation, setConfirmation] = useState<ConfigConfirmation | null>(null)
  const confirmationTriggerRef = useRef<HTMLElement | null>(null)

  const visibleItems = useMemo(() => configItems.filter((item) => {
    if (hiddenConfigKeys.has(item.key)) return false
    if (includeKeys) return includeKeys.includes(item.key)
    if (excludeKeys) return !excludeKeys.includes(item.key)
    return true
  }), [configItems, excludeKeys, includeKeys])

  const grouped = useMemo(() => {
    return categoryOrder
      .map((category) => ({
        category,
        items: visibleItems.filter((item) => item.category === category),
      }))
      .filter((group) => group.items.length > 0)
  }, [visibleItems])

  const changedKeys = useMemo(() => visibleItems
    .filter((item) => drafts[item.key] !== undefined && drafts[item.key] !== formatConfigValue(item.value))
    .map((item) => item.key), [drafts, visibleItems])

  const dirty = changedKeys.length > 0

  useEffect(() => {
    onDirtyChange?.(dirty)
  }, [dirty, onDirtyChange])

  function valueOf(item: ConfigItem) {
    return drafts[item.key] ?? formatConfigValue(item.value)
  }

  function setValue(key: string, value: string) {
    setDrafts((prev) => ({ ...prev, [key]: value }))
  }

  function requestConfirmation(action: () => void, title: string, description: string, confirmLabel: string) {
    confirmationTriggerRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
    setConfirmation({ title, description, confirmLabel, action })
  }

  function cancelConfirmation() {
    setConfirmation(null)
    const trigger = confirmationTriggerRef.current
    confirmationTriggerRef.current = null
    window.requestAnimationFrame(() => {
      if (trigger?.isConnected) trigger.focus()
    })
  }

  function confirmPendingAction() {
    const pending = confirmation
    setConfirmation(null)
    confirmationTriggerRef.current = null
    pending?.action()
  }

  async function saveAll() {
    if (!changedKeys.length) return
    setSaving(true)
    setError("")
    try {
      const updates = visibleItems.filter((item) => changedKeys.includes(item.key))
      const nextValues = new Map<string, unknown>()
      for (const item of updates) {
        const nextValue = parseConfigValue(valueOf(item), item.config_type)
        nextValues.set(item.key, nextValue)
      }
      await adminApi.updateConfigs(Object.fromEntries(nextValues))
      if (nextValues.has("default_model_id")) void loadSessionCreateReadiness(true)
      setConfigItems((prev) => prev.map((entry) => (
        nextValues.has(entry.key) ? { ...entry, value: nextValues.get(entry.key) } : entry
      )))
      setDrafts((prev) => {
        const next = { ...prev }
        for (const key of changedKeys) delete next[key]
        return next
      })
    } catch (err) {
      setError(err instanceof Error ? err.message : "配置保存失败")
    } finally {
      setSaving(false)
    }
  }

  async function cleanupOrphans() {
    setCleanupSaving(true)
    setCleanupResult("")
    setError("")
    try {
      const res = await adminApi.cleanupOrphanFiles({ older_than_hours: 24, limit: 100 })
      setCleanupResult(`清理遗留文件：标记 ${res.marked}，磁盘删除 ${res.removed}${res.ocr_expired_count ? `，超期 OCR 原件 ${res.ocr_expired_removed ?? res.ocr_expired_count}` : ""}，失败 ${res.failed}，保留历史附件 ${res.skipped_referenced}。`)
    } catch (err) {
      setError(err instanceof Error ? err.message : "孤儿文件清理失败")
    } finally {
      setCleanupSaving(false)
    }
  }

  function requestCleanupConfirmation() {
    requestConfirmation(
      () => void cleanupOrphans(),
      "清理暂存附件？",
      "将清理 24 小时前、尚未发送且未被消息引用的暂存附件，磁盘文件也会删除。历史消息引用的附件会保留。",
      "清理文件",
    )
  }

  function requestTemplateFill(item: ConfigItem, apply: () => void) {
    const template = formatConfigValue(item.default)
    if (!template || !valueOf(item).trim() || valueOf(item) === template) {
      apply()
      return
    }
    requestConfirmation(
      apply,
      "覆盖当前提示词？",
      "当前内容尚未保存。填入推荐模板后，现有内容会被覆盖，但不会立即保存到服务器。",
      "覆盖内容",
    )
  }

  return (
    <div className="h-full min-h-0 overflow-y-auto pr-1" aria-busy={saving}>
      <div className="mb-2 flex items-center justify-end gap-2">
        {dirty ? <span className="text-sm text-amber-600 dark:text-amber-400">有未保存修改</span> : null}
        <Button size="sm" className="h-8" onClick={saveAll} disabled={!dirty || saving}>
          <Save className="h-3.5 w-3.5" />
          {saving ? "保存中" : "保存全部"}
        </Button>
      </div>
      <div className="grid gap-3 xl:grid-cols-2">
        {grouped.map((group) => (
          <section key={group.category} className={group.category === "提示词" || group.category === "系统小模型" ? "xl:col-span-2" : ""}>
            <div className="flex items-center justify-between gap-3 border-b border-border/70 px-1 pb-2">
              <div className="text-sm font-semibold">{group.category === "提示词" ? "系统底层提示词模板" : group.category}</div>
              {group.category === "文件" ? (
                <div className="flex flex-wrap items-center justify-end gap-2">
                  {cleanupResult ? <span className="text-sm text-muted-foreground">{cleanupResult}</span> : null}
                  <Button type="button" size="sm" variant="outline" className="h-8" onClick={requestCleanupConfirmation} disabled={cleanupSaving}>
                    {cleanupSaving ? "清理中" : "清理遗留文件"}
                  </Button>
                </div>
              ) : null}
            </div>
            {group.category === "系统小模型" ? (
              <UtilityModelConfigGrid items={group.items} valueOf={valueOf} models={models} onChange={setValue} disabled={saving} />
            ) : (
              <div className="divide-y divide-border/60">
                {group.items.map((item) => (
                  <div
                    key={item.key}
                    className={item.key === "system_prompt_template"
                      ? "py-2"
                      : "grid gap-2 py-2.5 md:grid-cols-[168px_minmax(0,1fr)] md:items-center"
                    }
                  >
                    <div className="text-sm font-medium">{item.display_name || item.key}</div>
                    <ConfigInput item={item} value={valueOf(item)} models={models} onChange={(value) => setValue(item.key, value)} disabled={saving} onRequestTemplateFill={requestTemplateFill} />
                  </div>
                ))}
              </div>
            )}
          </section>
        ))}
      </div>
      <Dialog open={confirmation !== null} onOpenChange={(open) => { if (!open) cancelConfirmation() }}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>{confirmation?.title}</DialogTitle>
            <DialogDescription>{confirmation?.description}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={cancelConfirmation}>取消</Button>
            <Button type="button" variant={confirmation?.confirmLabel === "清理文件" ? "destructive" : "default"} onClick={confirmPendingAction} disabled={cleanupSaving}>{confirmation?.confirmLabel}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}

function UtilityModelConfigGrid({
  items,
  valueOf,
  models,
  onChange,
  disabled,
}: {
  items: ConfigItem[]
  valueOf: (item: ConfigItem) => string
  models: Model[]
  onChange: (key: string, value: string) => void
  disabled: boolean
}) {
  const ordered = [...items].sort((a, b) => {
    const ai = utilityConfigOrder.indexOf(a.key)
    const bi = utilityConfigOrder.indexOf(b.key)
    return (ai === -1 ? 999 : ai) - (bi === -1 ? 999 : bi)
  })

  return (
    <div className="grid gap-2 py-2 md:grid-cols-2">
      {ordered.map((item) => (
        <div key={item.key} className="grid gap-2 rounded-md border border-border/70 bg-muted/20 p-3 sm:grid-cols-[180px_minmax(0,1fr)] sm:items-center">
          <div className="min-w-0">
            <div className="text-sm font-medium">{item.display_name || item.key}</div>
            <div className="mt-1 text-xs leading-5 text-muted-foreground">{utilityConfigHelp[item.key]}</div>
          </div>
          <ConfigInput item={item} value={valueOf(item)} models={models} onChange={(value) => onChange(item.key, value)} disabled={disabled} />
        </div>
      ))}
    </div>
  )
}

function ConfigInput({
  item,
  value,
  models,
  onChange,
  disabled,
  onRequestTemplateFill,
}: {
  item: ConfigItem
  value: string
  models: Model[]
  onChange: (value: string) => void
  disabled: boolean
  onRequestTemplateFill?: (item: ConfigItem, apply: () => void) => void
}) {
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const [selectedPlaceholder, setSelectedPlaceholder] = useState(promptPlaceholders[0].token)

  if (modelConfigKeys.has(item.key)) {
    return (
      <select className="h-8 w-full rounded-md border border-input bg-background px-2.5 text-sm" value={value} onChange={(e) => onChange(e.target.value)} disabled={disabled}>
        <option value="">未设置</option>
        {models.filter((model) => model.enabled).map((model) => (
          <option key={model.id} value={model.id}>{formatModelOptionLabel(model)}</option>
        ))}
      </select>
    )
  }

  if (item.key === "file_upload_allowed_types") {
    return <UploadTypeSelect value={value} onChange={onChange} disabled={disabled} />
  }

  if (item.key === "system_prompt_template") {
    function insertPlaceholder(token: string) {
      const element = textareaRef.current
      if (!element) {
        onChange(`${value}${value.endsWith("\n") || !value ? "" : "\n"}${token}`)
        return
      }
      const start = element.selectionStart ?? value.length
      const end = element.selectionEnd ?? value.length
      const next = `${value.slice(0, start)}${token}${value.slice(end)}`
      onChange(next)
      requestAnimationFrame(() => {
        element.focus()
        element.setSelectionRange(start + token.length, start + token.length)
      })
    }

    function fillDefaultTemplate() {
      const template = formatConfigValue(item.default)
      if (!template) return
      const applyTemplate = () => {
        onChange(template)
        requestAnimationFrame(() => textareaRef.current?.focus())
      }
      if (onRequestTemplateFill) onRequestTemplateFill(item, applyTemplate)
      else applyTemplate()
    }

    return (
      <div className="space-y-2">
        <div className="flex flex-wrap items-center gap-2">
          <Button type="button" size="sm" className="h-8" variant="outline" onClick={fillDefaultTemplate} disabled={disabled || item.default === undefined}>
            填入推荐模板
          </Button>
          <select
            value={selectedPlaceholder}
            onChange={(e) => setSelectedPlaceholder(e.target.value)}
            className="h-8 min-w-[220px] rounded-md border border-input bg-background px-2.5 text-sm"
            disabled={disabled}
          >
            {promptPlaceholders.map((placeholder) => (
              <option key={placeholder.token} value={placeholder.token}>
                {placeholder.label} {placeholder.token}
              </option>
            ))}
          </select>
          <Button type="button" size="sm" className="h-8" variant="outline" onClick={() => insertPlaceholder(selectedPlaceholder)} disabled={disabled}>
            插入变量
          </Button>
        </div>
        <textarea
          ref={textareaRef}
          aria-label={item.display_name || item.key}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          disabled={disabled}
          className="min-h-[420px] w-full resize-y rounded-md border border-input bg-background px-3 py-2 font-mono text-sm leading-5 outline-none focus:border-foreground"
        />
      </div>
    )
  }

  if (item.config_type === "select" && item.options?.length) {
    return (
      <select
        className="h-8 w-full rounded-md border border-input bg-background px-2.5 text-sm"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
      >
        {item.options.map((opt) => (
          <option key={opt.value} value={String(opt.value)}>{opt.label}</option>
        ))}
      </select>
    )
  }

  if (item.config_type === "boolean") {
    const checked = value === "true"
    return (
      <button
        type="button"
        onClick={() => onChange(checked ? "false" : "true")}
        disabled={disabled}
        className={`h-7 rounded-md border px-2.5 text-sm transition-colors motion-control disabled:cursor-not-allowed disabled:opacity-50 ${
          checked
            ? "border-emerald-600 bg-emerald-50 text-emerald-700 dark:border-emerald-500/40 dark:bg-emerald-500/15 dark:text-emerald-300"
            : "border-border/70 text-muted-foreground hover:bg-muted"
        }`}
      >
        {checked ? "已开启" : "已关闭"}
      </button>
    )
  }

  if (item.config_type === "number") {
    return <Input type="number" className="h-8" value={value} onChange={(e) => onChange(e.target.value)} disabled={disabled} />
  }

  return <Input className="h-8" value={value} onChange={(e) => onChange(e.target.value)} disabled={disabled} />
}

function formatModelOptionLabel(model: Model) {
  const name = model.display_name || model.id
  return model.provider ? `${name}（${model.provider}）` : name
}

function formatConfigValue(value: unknown): string {
  if (value === undefined || value === null) return ""
  if (typeof value === "string") return value
  if (typeof value === "number" || typeof value === "boolean") return String(value)
  return JSON.stringify(value) || ""
}

function parseConfigValue(raw: string, type: string): unknown {
  if (type === "number" || type === "select") {
    const value = Number(raw)
    if (Number.isNaN(value)) throw new Error("数字格式不正确")
    return value
  }
  if (type === "boolean") return raw === "true"
  if (type === "json") return JSON.parse(raw)
  return raw
}

function UploadTypeSelect({ value, onChange, disabled }: { value: string; onChange: (value: string) => void; disabled: boolean }) {
  const selected = parseStringArray(value).flatMap((item) => item === "image/*" ? supportedImageTypes : [item])
  function toggle(nextValue: string) {
    const next = selected.includes(nextValue)
      ? selected.filter((item) => item !== nextValue)
      : [...selected, nextValue]
    onChange(JSON.stringify(next))
  }

  return (
    <div className="flex flex-wrap gap-1.5">
      {uploadTypeOptions.map((item) => {
        const active = selected.includes(item.value)
        return (
          <button
            key={item.value}
            type="button"
            onClick={() => toggle(item.value)}
            disabled={disabled}
            className={`rounded-md border px-2.5 py-1 text-sm transition-colors motion-control disabled:cursor-not-allowed disabled:opacity-50 ${
              active ? "border-foreground bg-foreground text-background" : "border-border/70 hover:bg-muted"
            }`}
          >
            {item.label}
          </button>
        )
      })}
    </div>
  )
}

function parseStringArray(value: string) {
  try {
    const parsed = JSON.parse(value)
    if (Array.isArray(parsed)) return parsed.filter((item): item is string => typeof item === "string")
  } catch {
    return []
  }
  return []
}
