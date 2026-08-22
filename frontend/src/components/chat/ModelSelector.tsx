import { useEffect, useState } from "react"
import { useLocation, useNavigate } from "react-router"
import { useChatStore } from "@/stores/chat"
import { useModelStore } from "@/stores/models"
import { useAuthStore } from "@/stores/auth"
import * as sessionsApi from "@/api/sessions"
import type { Model } from "@/types"
import { ChevronDown, Sparkles, Wrench as ToolIcon, Eye, Globe, Check, Settings2 } from "lucide-react"
import { Popover, PopoverTrigger, PopoverContent } from "@/components/ui/popover"
import { Button } from "@/components/ui/button"
import { navigateWithFade } from "@/lib/navigation"
import { chatSurfaceControlClass } from "./ChatInput.constants"

export function ModelSelector() {
  const [open, setOpen] = useState(false)
  const models = useModelStore((s) => s.models)
  const loadModels = useModelStore((s) => s.loadModels)
  const user = useAuthStore((s) => s.user)
  const navigate = useNavigate()
  const location = useLocation()
  const sessions = useChatStore((s) => s.sessions)
  const activeSessionId = useChatStore((s) => s.activeSessionId)
  const updateSessionLocal = useChatStore((s) => s.updateSessionLocal)

  const activeSession = sessions.find((s) => s.id === activeSessionId)
  const currentModelId = activeSession?.model_id || ""
  const currentProvider = activeSession?.provider || ""
  const currentModel = models.find((m) => m.id === currentModelId && m.provider === currentProvider)

  useEffect(() => {
    loadModels().catch(() => {})
  }, [loadModels])

  if (!activeSessionId) return null

  async function handleSelect(modelId: string) {
    setOpen(false)
    if (!activeSessionId) return
    const nextModel = models.find((m) => modelSelectionKey(m) === modelId)
    if (!nextModel) return
    if (nextModel.id === currentModelId && nextModel.provider === currentProvider) return
    await sessionsApi.updateSession(activeSessionId, {
      model_id: nextModel.id,
      provider: nextModel?.provider,
    })
    updateSessionLocal(activeSessionId, {
      model_id: nextModel.id,
      provider: nextModel.provider,
      updated_at: new Date().toISOString(),
    })
  }

  const enabledModels = models.filter((m) => m.enabled)
  const listboxId = "model-selector-listbox"
  const triggerLabel = `当前模型：${formatModelName(currentModel, currentModelId)}${currentModel ? `，渠道：${formatChannelName(currentModel)}` : ""}`

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          className={`flex h-11 min-w-11 max-w-[42vw] items-center gap-1 px-2 text-sm font-medium focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/45 sm:gap-1.5 sm:px-2.5 md:h-8 md:min-w-0 md:max-w-[180px] md:py-1.5 ${chatSurfaceControlClass}`}
          role="combobox"
          aria-label={triggerLabel}
          aria-haspopup="listbox"
          aria-expanded={open}
          aria-controls={listboxId}
        >
          <span className="min-w-0 max-w-[92px] truncate sm:max-w-[180px]">
            {formatModelName(currentModel, currentModelId)}
          </span>
          {currentModel ? (
            <span className="hidden shrink-0 rounded-lg bg-muted/60 px-1.5 py-0.5 text-xs font-medium leading-none text-muted-foreground/80 sm:inline">
              {formatChannelName(currentModel)}
            </span>
          ) : currentModelId ? (
            <span className="shrink-0 rounded-lg bg-amber-500/10 px-1.5 py-0.5 text-xs font-medium leading-none text-amber-700 dark:text-amber-300">
              模型不可用
            </span>
          ) : null}
          <ChevronDown className="h-3.5 w-3.5 text-muted-foreground" />
        </button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-72 p-1.5">
        <p className="px-2.5 py-1 text-xs font-medium text-muted-foreground uppercase tracking-wider">
          模型
        </p>
        <div id={listboxId} role="listbox" className="max-h-[340px] overflow-y-auto scrollbar-thin space-y-0.5">
          {enabledModels.map((model) => {
            const active = model.id === currentModelId && model.provider === currentProvider
            return (
              <button
                key={modelSelectionKey(model)}
                role="option"
                aria-selected={active}
                className={`flex w-full items-start gap-2.5 rounded-[8px] px-3 py-2 text-left outline-none transition-[background-color,color,box-shadow] duration-200 motion-control focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/50 ${
                  active ? "bg-accent/80 shadow-[0_1px_2px_rgba(0,0,0,0.03)]" : "hover:bg-accent/50"
                }`}
                onClick={() => handleSelect(modelSelectionKey(model))}
              >
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-1.5">
                    <p className="text-sm font-medium truncate">{model.display_name}</p>
                    {active && <Check className="h-3 w-3 text-foreground/60 shrink-0" />}
                  </div>
                  <p className="text-xs text-muted-foreground mt-0.5 truncate">
                    {formatChannelName(model)} · {formatContextWindow(model.context_window)}
                  </p>
                </div>
                <div className="flex shrink-0 items-center gap-1 mt-0.5">
                  {model.reasoning && <CapBadge icon={Sparkles} color="violet" title="推理" />}
                  {model.tool_use && <CapBadge icon={ToolIcon} color="blue" title="工具" />}
                  {model.vision && <CapBadge icon={Eye} color="amber" title="视觉" />}
                  {model.search_impl && <CapBadge icon={Globe} color="green" title="搜索" />}
                </div>
              </button>
            )
          })}
          {enabledModels.length === 0 && (
            <div className="px-3 py-4 text-center">
              <p className="text-sm font-medium text-foreground">暂无可用模型</p>
              <p className="mt-1 text-xs leading-5 text-muted-foreground">
                {user?.role === "admin" ? "先添加渠道并启用一个模型。" : "请联系管理员完成模型配置。"}
              </p>
              {user?.role === "admin" ? (
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  className="mt-3 h-8"
                  onClick={() => {
                    setOpen(false)
                    navigateWithFade(navigate, "/admin/models", { state: { from: `${location.pathname}${location.search}` } })
                  }}
                >
                  <Settings2 className="h-3.5 w-3.5" />
                  配置模型
                </Button>
              ) : null}
            </div>
          )}
        </div>
      </PopoverContent>
    </Popover>
  )
}

function CapBadge({
  icon: Icon,
  color,
  title,
}: {
  icon: React.ComponentType<{ className?: string }>
  color: string
  title: string
}) {
  const colors: Record<string, string> = {
    violet: "text-violet-500",
    blue: "text-blue-500",
    amber: "text-amber-500",
    green: "text-emerald-500",
  }
  return <Icon className={`h-3.5 w-3.5 ${colors[color] || "text-muted-foreground"}`} aria-label={title} />
}

function formatContextWindow(tokens: number): string {
  if (tokens >= 1000000) return `${(tokens / 1000000).toFixed(0)}M`
  return `${(tokens / 1000).toFixed(0)}K`
}

function formatModelName(model: { display_name?: string } | undefined, fallback: string) {
  return model?.display_name || fallback || "选择模型"
}

function modelSelectionKey(model: Pick<Model, "provider" | "id">) {
  return `${model.provider}:${model.id}`
}

function formatChannelName(model: Pick<Model, "channel_display_name" | "provider">) {
  return model.channel_display_name?.trim() || model.provider
}
