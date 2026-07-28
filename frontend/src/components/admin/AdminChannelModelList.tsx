import type { Model } from "@/types"
import { Button } from "@/components/ui/button"
import type { ModelTestResponse } from "@/api/admin"
import { CapabilityDots, ModelTestStatus, SwitchButton } from "./AdminModelsPanel.controls"
import { formatContext } from "./AdminModelsPanel.helpers"

interface AdminChannelModelListProps {
  models: Model[]
  saving: string
  testingModel: boolean
  testResult?: ModelTestResponse | null
  onStartEdit: (model: Model) => void
  onToggleEnabled: (model: Model) => void | Promise<void>
  onTestModel: (model: Model) => void | Promise<void>
}

export function AdminChannelModelList({
  models,
  saving,
  testingModel,
  testResult,
  onStartEdit,
  onToggleEnabled,
  onTestModel,
}: AdminChannelModelListProps) {
  return (
    <>
      {testResult ? (
        <div className="border-b border-border/70 px-3 py-2">
          <ModelTestStatus result={testResult} />
        </div>
      ) : null}
      <div className="min-h-0 flex-1 overflow-y-auto">
        {models.map((model) => (
          <div key={model.id} className="grid gap-2 border-b border-border/60 px-3 py-2.5 sm:grid-cols-[minmax(0,1fr)_auto]">
            <button className="min-w-0 text-left" onClick={() => onStartEdit(model)}>
              <div className="truncate text-sm font-medium">{model.display_name}</div>
              <div className="mt-0.5 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
                <span className="truncate">{model.id}</span>
                <span>{formatContext(model.context_window)}</span>
                <CapabilityDots model={model} />
              </div>
            </button>
            <div className="flex items-center gap-2 sm:justify-end">
              <Button variant="outline" size="sm" onClick={() => void onTestModel(model)} disabled={testingModel}>
                测活
              </Button>
              <SwitchButton checked={model.enabled} onClick={() => void onToggleEnabled(model)} disabled={saving === `model-${model.id}`} />
            </div>
          </div>
        ))}
        {models.length === 0 ? (
          <div className="flex h-full min-h-[180px] items-center justify-center text-sm text-muted-foreground">暂无模型</div>
        ) : null}
      </div>
    </>
  )
}
