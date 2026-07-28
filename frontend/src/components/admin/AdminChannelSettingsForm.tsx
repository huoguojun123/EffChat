import { useState, type Dispatch, type SetStateAction } from "react"
import { adminApi, type AIChannel, type AIChannelInput } from "@/api/admin"
import type { Model } from "@/types"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { useModelStore } from "@/stores/models"
import { Save, Trash2 } from "lucide-react"
import { adapterOptions } from "./AdminChannelsPanel.constants"
import { Field, Select, Toggle } from "./AdminModelsPanel.controls"

interface Props {
  channel: AIChannel | null
  isNew: boolean
  models: Model[]
  setChannels: Dispatch<SetStateAction<AIChannel[]>>
  setError: (error: string) => void
  onSaved: (key: string) => void
  onDeleted: () => void
}

const channelDefaults: Record<AIChannelInput["adapter"], { key: string; displayName: string; baseURL: string; sortOrder: number }> = {
  openai_compatible: {
    key: "openai",
    displayName: "OpenAI",
    baseURL: "https://api.openai.com/v1",
    sortOrder: 10,
  },
  anthropic: {
    key: "anthropic",
    displayName: "Anthropic",
    baseURL: "https://api.anthropic.com",
    sortOrder: 20,
  },
  google: {
    key: "google",
    displayName: "Google Gemini",
    baseURL: "https://generativelanguage.googleapis.com",
    sortOrder: 30,
  },
}

function emptyChannelDraft(): AIChannelInput {
  const defaults = channelDefaults.openai_compatible
  return {
    key: defaults.key,
    display_name: defaults.displayName,
    adapter: "openai_compatible",
    base_url: defaults.baseURL,
    api_key: "",
    enabled: true,
    sort_order: defaults.sortOrder,
  }
}

function channelKeyFromName(name: string, fallbackKey = "custom-channel") {
  return name
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "") || fallbackKey
}

function channelDraftFrom(item: AIChannel | null): AIChannelInput {
  if (!item) return emptyChannelDraft()
  return {
    key: item.key,
    display_name: item.display_name,
    adapter: item.adapter,
    base_url: item.base_url,
    api_key: "",
    enabled: item.enabled,
    sort_order: item.sort_order,
  }
}

export function AdminChannelSettingsForm({ channel, isNew, models, setChannels, setError, onSaved, onDeleted }: Props) {
  const [fallbackKey] = useState(() => `custom-${Date.now().toString(36)}`)
  const [draft, setDraft] = useState<AIChannelInput>(() => channelDraftFrom(channel))
  const [saving, setSaving] = useState("")
  const loadModels = useModelStore((s) => s.loadModels)

  function updateDisplayName(displayName: string) {
    setDraft((prev) => ({
      ...prev,
      display_name: displayName,
      key: isNew ? channelKeyFromName(displayName, fallbackKey) : prev.key,
    }))
  }

  function updateAdapter(adapter: AIChannelInput["adapter"]) {
    setDraft((prev) => {
      const currentDefaults = channelDefaults[prev.adapter]
      const nextDefaults = channelDefaults[adapter]
      const baseURL = !prev.base_url.trim() || prev.base_url === currentDefaults.baseURL ? nextDefaults.baseURL : prev.base_url
      if (!isNew) {
        return { ...prev, adapter, base_url: baseURL }
      }
      const displayNameUsesDefault = !prev.display_name.trim() || prev.display_name === currentDefaults.displayName
      const keyUsesDefault = !prev.key.trim() || prev.key === currentDefaults.key
      const key = displayNameUsesDefault && keyUsesDefault ? nextDefaults.key : prev.key || channelKeyFromName(prev.display_name, fallbackKey)
      const displayName = displayNameUsesDefault ? nextDefaults.displayName : prev.display_name
      return {
        ...prev,
        adapter,
        key,
        display_name: displayName,
        base_url: baseURL,
        sort_order: prev.sort_order === currentDefaults.sortOrder ? nextDefaults.sortOrder : prev.sort_order,
      }
    })
  }

  async function saveChannel() {
    setSaving("save")
    setError("")
    try {
      const saved = await adminApi.saveChannel({ ...draft, api_key: draft.api_key?.trim() || undefined })
      setChannels((prev) => {
        const exists = prev.some((item) => item.key === saved.key)
        const next = exists ? prev.map((item) => (item.key === saved.key ? saved : item)) : [...prev, saved]
        return next.sort((a, b) => a.sort_order - b.sort_order || a.key.localeCompare(b.key))
      })
      setDraft(channelDraftFrom(saved))
      await loadModels(true)
      onSaved(saved.key)
    } catch (err) {
      setError(err instanceof Error ? err.message : "渠道保存失败")
    } finally {
      setSaving("")
    }
  }

  async function deleteChannel() {
    if (!channel) return
    const affected = models.filter((model) => model.provider === channel.key).length
    const detail = affected > 0
      ? `删除渠道 ${channel.key} 后，${affected} 个使用该渠道的模型会保留，但在重新分配渠道前无法调用。`
      : `删除渠道 ${channel.key} 后，新请求将不能再使用这个渠道。`
    if (!window.confirm(`${detail}\n\n确定继续？`)) return

    setSaving("delete")
    setError("")
    try {
      await adminApi.deleteChannel(channel.key)
      setChannels((prev) => prev.filter((item) => item.key !== channel.key))
      onDeleted()
    } catch (err) {
      setError(err instanceof Error ? err.message : "渠道删除失败")
    } finally {
      setSaving("")
    }
  }

  return (
    <div className="border-b border-border/70 p-3">
      <div className="mb-2 flex items-center justify-between gap-3 lg:mb-3">
        <div className="min-w-0">
          <div className="truncate text-sm font-medium">{isNew ? "新建渠道" : channel?.display_name || "渠道配置"}</div>
          <div className="truncate text-sm text-muted-foreground">{isNew ? "保存后可添加模型" : channel?.key}</div>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          {!isNew && channel ? (
            <Button variant="ghost" size="sm" className="text-destructive hover:text-destructive" onClick={() => void deleteChannel()} disabled={saving !== ""}>
              <Trash2 className="h-3.5 w-3.5" />
              删除
            </Button>
          ) : null}
          <Button size="sm" onClick={() => void saveChannel()} disabled={saving !== ""}>
            <Save className="h-3.5 w-3.5" />
            保存
          </Button>
        </div>
      </div>
      <div className="grid gap-3 md:grid-cols-2">
        <Field label="渠道名">
          <Input value={draft.display_name} onChange={(e) => updateDisplayName(e.target.value)} />
        </Field>
        <Field label="协议">
          <Select value={draft.adapter} onChange={(adapter) => updateAdapter(adapter as AIChannelInput["adapter"])}>
            {adapterOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
          </Select>
        </Field>
        <Field label="Base URL">
          <Input value={draft.base_url} onChange={(e) => setDraft((prev) => ({ ...prev, base_url: e.target.value }))} />
        </Field>
        <Field label="API key">
          <Input type="password" value={draft.api_key || ""} placeholder={isNew ? "" : "留空保留已保存 Key"} onChange={(e) => setDraft((prev) => ({ ...prev, api_key: e.target.value }))} />
        </Field>
        <Toggle label="启用" checked={draft.enabled} onChange={(enabled) => setDraft((prev) => ({ ...prev, enabled }))} />
      </div>
    </div>
  )
}
