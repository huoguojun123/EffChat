import { Cloud, ListChecks } from "lucide-react"
import type { Dispatch, SetStateAction } from "react"
import { Button } from "@/components/ui/button"
import type { AIChannel, ModelTestResponse } from "@/api/admin"
import type { Model } from "@/types"
import { AdminChannelModelList } from "./AdminChannelModelList"
import { AdminChannelSettingsForm } from "./AdminChannelSettingsForm"

interface Props {
  channel: AIChannel | null
  isNew: boolean
  isUnconfigured: boolean
  models: Model[]
  currentItems: Model[]
  availableModels: Model[]
  fetching: "" | "gateway"
  saving: string
  testingModel: boolean
  testResult: ModelTestResponse | null
  setChannels: Dispatch<SetStateAction<AIChannel[]>>
  setError: (error: string) => void
  onSaved: (key: string) => void
  onDeleted: () => void
  onFetchAvailableModels: () => void | Promise<void>
  onOpenModelManager: () => void
  onStartEdit: (model: Model) => void
  onToggleEnabled: (model: Model) => void | Promise<void>
  onTestModel: (model: Model) => void | Promise<void>
}

export function AdminChannelOverviewPanel({
  channel,
  isNew,
  isUnconfigured,
  models,
  currentItems,
  availableModels,
  fetching,
  saving,
  testingModel,
  testResult,
  setChannels,
  setError,
  onSaved,
  onDeleted,
  onFetchAvailableModels,
  onOpenModelManager,
  onStartEdit,
  onToggleEnabled,
  onTestModel,
}: Props) {
  if (isUnconfigured) {
    return (
      <div className="flex h-full min-h-0 flex-col overflow-hidden">
        <ChannelModelsHeader
          title="未配置渠道的模型"
          count={currentItems.length}
          enabledCount={currentItems.filter((item) => item.enabled).length}
          onOpenModelManager={onOpenModelManager}
        />
        <AdminChannelModelList
          models={currentItems}
          saving={saving}
          testingModel={testingModel}
          testResult={testResult}
          onStartEdit={onStartEdit}
          onToggleEnabled={onToggleEnabled}
          onTestModel={onTestModel}
        />
      </div>
    )
  }

  return (
    <div className="flex h-full min-h-0 flex-col overflow-hidden">
      <div className="shrink-0">
        <AdminChannelSettingsForm
          channel={channel}
          isNew={isNew}
          models={models}
          setChannels={setChannels}
          setError={setError}
          onSaved={onSaved}
          onDeleted={onDeleted}
        />
      </div>

      {!isNew ? (
        <>
          <div className="flex shrink-0 flex-wrap items-center justify-between gap-2 border-b border-border/70 px-3 py-2.5 lg:gap-3 lg:py-3">
            <div>
              <div className="text-sm font-medium">模型列表</div>
              <div className="text-xs text-muted-foreground">
                {currentItems.filter((item) => item.enabled).length}/{currentItems.length} 启用
              </div>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <Button variant="outline" size="sm" onClick={() => void onFetchAvailableModels()} disabled={fetching !== ""}>
                <Cloud className="h-3.5 w-3.5" />
                {fetching === "gateway" ? "检测中" : "检测连接"}
              </Button>
              <Button size="sm" onClick={onOpenModelManager}>
                <ListChecks className="h-3.5 w-3.5" />
                模型管理
              </Button>
            </div>
          </div>
          {availableModels.length > 0 ? (
            <div className="border-b border-border/70 px-3 py-2 text-xs text-emerald-700 dark:text-emerald-300">
              上游连接可用，返回 {availableModels.length} 个模型
            </div>
          ) : null}
          <AdminChannelModelList
            models={currentItems}
            saving={saving}
            testingModel={testingModel}
            testResult={testResult}
            onStartEdit={onStartEdit}
            onToggleEnabled={onToggleEnabled}
            onTestModel={onTestModel}
          />
        </>
      ) : (
        <div className="min-h-0 flex-1" />
      )}
    </div>
  )
}

function ChannelModelsHeader({
  title,
  count,
  enabledCount,
  onOpenModelManager,
}: {
  title: string
  count: number
  enabledCount: number
  onOpenModelManager: () => void
}) {
  return (
    <div className="flex shrink-0 items-center justify-between gap-3 border-b border-border/70 px-3 py-2.5 lg:py-3">
      <div>
        <div className="text-sm font-medium">{title}</div>
        <div className="text-xs text-muted-foreground">{enabledCount}/{count} 启用</div>
      </div>
      <Button size="sm" onClick={onOpenModelManager}>
        <ListChecks className="h-3.5 w-3.5" />
        模型管理
      </Button>
    </div>
  )
}
