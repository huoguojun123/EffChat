import type { Model } from "@/types"
import { Button } from "@/components/ui/button"
import { MotionView } from "@/components/ui/motion"
import { Cloud, Cpu, Plus } from "lucide-react"
import { providerLabels } from "./AdminModelsPanel.constants"
import { CapabilityDots, SwitchButton } from "./AdminModelsPanel.controls"
import { formatContext, lifecycleStatusLabel } from "./AdminModelsPanel.helpers"

interface AdminModelsListPaneProps {
  activeProvider: string
  currentItems: Model[]
  importableItems: Model[]
  currentModel: Model | null
  channelLabels: Record<string, string>
  providerConfigured: boolean
  saving: string
  fetching: "" | "gateway"
  mobileDetailOpen: boolean
  onFetchAvailableModels: () => void | Promise<void>
  onStartCreate: (provider: string) => void
  onStartEdit: (model: Model) => void
  onToggleEnabled: (model: Model) => void | Promise<void>
  onPrepareImportModel: (model: Model) => void
  onImportModel: (model: Model) => void | Promise<void>
}

export function AdminModelsListPane({
  activeProvider,
  currentItems,
  importableItems,
  currentModel,
  channelLabels,
  providerConfigured,
  saving,
  fetching,
  mobileDetailOpen,
  onFetchAvailableModels,
  onStartCreate,
  onStartEdit,
  onToggleEnabled,
  onPrepareImportModel,
  onImportModel,
}: AdminModelsListPaneProps) {
  return (
    <div className={`min-h-0 flex-col overflow-hidden border-b border-border/70 lg:flex lg:border-b-0 lg:border-r ${mobileDetailOpen ? "hidden lg:flex" : "flex"}`}>
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border/70 px-3 py-2.5 lg:px-4 lg:py-3">
        <div className="flex min-w-0 items-center gap-2 text-sm font-medium">
          <Cpu className="h-4 w-4" />
          <span className="truncate">{channelLabels[activeProvider] || providerLabels[activeProvider] || activeProvider}</span>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button size="sm" variant="outline" onClick={() => void onFetchAvailableModels()} disabled={fetching !== "" || !providerConfigured} title={providerConfigured ? "从当前渠道拉取模型列表" : "先添加或选择一个已配置渠道"}>
            <Cloud className="h-3.5 w-3.5" />
            {fetching === "gateway" ? "拉取中" : "拉取模型"}
          </Button>
          <Button size="sm" onClick={() => onStartCreate(activeProvider)} disabled={!providerConfigured} title={providerConfigured ? "在当前渠道下添加模型" : "先添加或选择一个已配置渠道"}>
            <Plus className="h-3.5 w-3.5" />
            添加模型
          </Button>
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <MotionView viewKey={activeProvider} className="min-h-full">
          {currentItems.map((model) => (
            <div
              key={model.id}
              className={`border-b px-4 py-2.5 transition-colors motion-control last:border-b-0 ${
                currentModel?.id === model.id
                  ? "border-l-2 border-l-primary border-b-border/60 bg-accent/60"
                  : "border-border/60 hover:bg-muted/30"
              }`}
            >
              <div className="flex items-start justify-between gap-4">
                <button className="min-w-0 flex-1 text-left outline-none" onClick={() => onStartEdit(model)}>
                  <div className="truncate text-sm font-medium leading-5">{model.display_name}</div>
                  <div className="mt-0.5 truncate text-xs text-muted-foreground">{model.id}</div>
                </button>
                <SwitchButton checked={model.enabled} onClick={() => void onToggleEnabled(model)} disabled={saving === `model-${model.id}`} />
              </div>
              <button className="mt-1.5 flex w-full items-center gap-3 text-left text-xs text-muted-foreground" onClick={() => onStartEdit(model)}>
                <span className="shrink-0">{formatContext(model.context_window)}</span>
                <CapabilityDots model={model} />
                <span className="ml-auto shrink-0">{lifecycleStatusLabel(model.lifecycle_status)}</span>
              </button>
            </div>
          ))}
          {importableItems.length > 0 && (
            <div className="border-b border-border/70 bg-muted/20 px-5 py-2 text-xs text-muted-foreground">可导入</div>
          )}
          {importableItems.map((model) => (
            <div key={`available-${model.id}`} className="border-b border-border/60 px-4 py-2.5 last:border-b-0">
              <div className="flex items-start justify-between gap-4">
                <button className="min-w-0 flex-1 text-left" onClick={() => onPrepareImportModel(model)}>
                  <div className="truncate text-sm font-medium leading-5">{model.display_name}</div>
                  <div className="mt-0.5 truncate text-xs text-muted-foreground">{model.id}</div>
                </button>
                <Button size="sm" variant="outline" onClick={() => void onImportModel(model)} disabled={saving === `import-${model.id}`}>
                  导入
                </Button>
              </div>
              <div className="mt-2 flex items-center gap-3 text-xs text-muted-foreground">
                <span>{formatContext(model.context_window)}</span>
                <CapabilityDots model={model} />
                <span className="ml-auto">{lifecycleStatusLabel(model.lifecycle_status)}</span>
              </div>
            </div>
          ))}
          {currentItems.length === 0 && importableItems.length === 0 && (
            <div className="flex h-full min-h-[180px] items-center justify-center text-sm text-muted-foreground">暂无模型</div>
          )}
        </MotionView>
      </div>
    </div>
  )
}
