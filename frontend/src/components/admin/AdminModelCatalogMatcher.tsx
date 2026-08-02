import type { Model } from "@/types"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { providerLabels } from "./AdminModelsPanel.constants"
import { Select } from "./AdminModelsPanel.controls"
import { catalogSelectionKey, lifecycleStatusLabel } from "./AdminModelsPanel.helpers"

interface AdminModelCatalogMatcherProps {
  catalogQuery: string
  setCatalogQuery: (value: string) => void
  selectedCatalogKey: string
  setSelectedCatalogKey: (value: string) => void
  filteredCatalogModels: Model[]
  selectedCatalogModel: Model | null
  loadingMeta: boolean
  onApply: () => void
  onClose: () => void
}

export function AdminModelCatalogMatcher({
  catalogQuery,
  setCatalogQuery,
  selectedCatalogKey,
  setSelectedCatalogKey,
  filteredCatalogModels,
  selectedCatalogModel,
  loadingMeta,
  onApply,
  onClose,
}: AdminModelCatalogMatcherProps) {
  return (
    <div className="rounded-md border border-border/70 bg-muted/20 p-3">
      <div className="mb-2 flex items-center justify-between gap-2">
        <div className="text-sm font-medium">models.dev 手动匹配</div>
        <Button variant="ghost" size="sm" className="h-7 px-2" onClick={onClose}>
          收起
        </Button>
      </div>
      <div className="grid gap-2 md:grid-cols-[minmax(0,0.8fr)_minmax(0,1.2fr)_auto]">
        <Input type="search" name="effchat-model-catalog-search" autoComplete="off" autoCorrect="off" spellCheck={false} value={catalogQuery} onChange={(e) => setCatalogQuery(e.target.value)} placeholder="搜索官方模型" />
        <Select value={selectedCatalogKey} onChange={setSelectedCatalogKey}>
          <option value="">不匹配</option>
          {filteredCatalogModels.map((model) => (
            <option key={`${model.provider}:${model.id}`} value={catalogSelectionKey(model)}>
              {providerLabels[model.provider] || model.provider} · {model.display_name || model.id} · {lifecycleStatusLabel(model.lifecycle_status)} · {model.id}
            </option>
          ))}
        </Select>
        <Button size="sm" className="h-8" disabled={!selectedCatalogModel || loadingMeta} onClick={onApply}>
          {loadingMeta ? "应用中" : "应用能力"}
        </Button>
      </div>
      {catalogQuery.trim() && filteredCatalogModels.length === 0 ? <p className="mt-2 text-xs text-muted-foreground">未找到匹配模型</p> : null}
    </div>
  )
}
