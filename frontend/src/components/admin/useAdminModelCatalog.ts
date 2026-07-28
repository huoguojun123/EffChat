import { useMemo, useState } from "react"
import { adminApi } from "@/api/admin"
import type { Model } from "@/types"
import { catalogModelPatch, catalogSelectionKey } from "./AdminModelsPanel.helpers"
import type { ModelDraft } from "./AdminModelsPanel.types"

interface UseAdminModelCatalogOptions {
  currentDraft: ModelDraft
  selectedProvider: string
  selectedProviderConfigured: boolean
  models: Model[]
  setError: (error: string) => void
  updateCurrentDraft: (patch: Partial<ModelDraft>) => void
}

export function useAdminModelCatalog({
  currentDraft,
  selectedProvider,
  selectedProviderConfigured,
  models,
  setError,
  updateCurrentDraft,
}: UseAdminModelCatalogOptions) {
  const [fetching, setFetching] = useState<"" | "gateway">("")
  const [loadingMeta, setLoadingMeta] = useState(false)
  const [catalogOpen, setCatalogOpen] = useState(false)
  const [catalogLoading, setCatalogLoading] = useState(false)
  const [catalogModels, setCatalogModels] = useState<Model[]>([])
  const [catalogQuery, setCatalogQuery] = useState("")
  const [selectedCatalogKey, setSelectedCatalogKey] = useState("")
  const [availableModels, setAvailableModels] = useState<Model[]>([])

  const importableItems = useMemo(() => {
    return availableModels.filter((model) => {
      return selectedProviderConfigured && model.provider === selectedProvider && !models.some((item) => item.id === model.id)
    })
  }, [availableModels, models, selectedProvider, selectedProviderConfigured])

  const filteredCatalogModels = useMemo(() => {
    const keyword = catalogQuery.trim().toLowerCase()
    if (!keyword) return []
    return catalogModels
      .filter((model) => {
        if (!keyword) return true
        return [model.id, model.display_name, model.provider].some((value) => value.toLowerCase().includes(keyword))
      })
      .slice(0, 60)
  }, [catalogModels, catalogQuery])

  const selectedCatalogModel = useMemo(() => {
    return catalogModels.find((model) => catalogSelectionKey(model) === selectedCatalogKey) || null
  }, [catalogModels, selectedCatalogKey])

  function clearCatalogMatcher() {
    setCatalogOpen(false)
    setCatalogQuery("")
    setSelectedCatalogKey("")
  }

  async function fetchAvailableModels() {
    if (!selectedProviderConfigured) {
      setError("请先选择一个已配置渠道，再拉取模型")
      return
    }
    setFetching("gateway")
    setError("")
    setAvailableModels([])
    try {
      const res = await adminApi.listAvailableModels(selectedProvider)
      setAvailableModels(res.models || [])
    } catch (err) {
      setError(err instanceof Error ? err.message : "模型拉取失败")
    } finally {
      setFetching("")
    }
  }

  async function loadCatalogMeta() {
    const id = currentDraft.id.trim()
    if (!id) {
      setError("请先填写模型 ID 再从 models.dev 加载")
      return
    }
    setLoadingMeta(true)
    setError("")
    try {
      const { model } = await adminApi.getCatalogModel(id)
      updateCurrentDraft(catalogModelPatch(model))
    } catch (err) {
      setError(err instanceof Error ? `models.dev 目录未找到或加载失败：${err.message}` : "models.dev 目录未找到或加载失败")
    } finally {
      setLoadingMeta(false)
    }
  }

  async function openCatalogMatcher() {
    const nextOpen = !catalogOpen
    setCatalogOpen(nextOpen)
    setError("")
    if (!nextOpen || catalogModels.length > 0 || catalogLoading) return
    setCatalogLoading(true)
    try {
      const res = await adminApi.listCatalogModels()
      setCatalogModels(res.models || [])
    } catch (err) {
      setError(err instanceof Error ? `models.dev 目录加载失败：${err.message}` : "models.dev 目录加载失败")
    } finally {
      setCatalogLoading(false)
    }
  }

  async function applySelectedCatalogModel() {
    if (!selectedCatalogModel) return
    setLoadingMeta(true)
    setError("")
    try {
      const { model } = await adminApi.getCatalogModel(selectedCatalogModel.id, selectedCatalogModel.provider)
      updateCurrentDraft(catalogModelPatch(model))
      clearCatalogMatcher()
    } catch (err) {
      setError(err instanceof Error ? `models.dev 匹配项加载失败：${err.message}` : "models.dev 匹配项加载失败")
    } finally {
      setLoadingMeta(false)
    }
  }

  return {
    fetching,
    loadingMeta,
    catalogLoading,
    catalogOpen,
    catalogQuery,
    setCatalogQuery,
    selectedCatalogKey,
    setSelectedCatalogKey,
    filteredCatalogModels,
    selectedCatalogModel,
    availableModels,
    setAvailableModels,
    importableItems,
    clearCatalogMatcher,
    fetchAvailableModels,
    loadCatalogMeta,
    openCatalogMatcher,
    applySelectedCatalogModel,
  }
}
