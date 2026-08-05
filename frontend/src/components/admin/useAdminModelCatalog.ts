import { useMemo, useRef, useState } from "react"
import { adminApi } from "@/api/admin"
import type { Model } from "@/types"
import { catalogModelPatch, catalogSelectionKey } from "./AdminModelsPanel.helpers"
import type { ModelDraft } from "./AdminModelsPanel.types"
import type { EditorOwnership } from "./editorOwnership"

interface UseAdminModelCatalogOptions {
  currentDraft: ModelDraft
  selectedProvider: string
  selectedProviderConfigured: boolean
  models: Model[]
  setError: (error: string) => void
  updateCurrentDraft: (patch: Partial<ModelDraft>) => void
  editorOwner: EditorOwnership
}

export function useAdminModelCatalog({
  currentDraft,
  selectedProvider,
  selectedProviderConfigured,
  models,
  setError,
  updateCurrentDraft,
  editorOwner,
}: UseAdminModelCatalogOptions) {
  const [fetching, setFetching] = useState<"" | "gateway">("")
  const [loadingMeta, setLoadingMeta] = useState(false)
  const [catalogOpen, setCatalogOpen] = useState(false)
  const [catalogLoading, setCatalogLoading] = useState(false)
  const [catalogModels, setCatalogModels] = useState<Model[]>([])
  const [catalogQuery, setCatalogQuery] = useState("")
  const [selectedCatalogKey, setSelectedCatalogKey] = useState("")
  const [availableModels, setAvailableModels] = useState<Model[]>([])
  const requestSequence = useRef({ available: 0, meta: 0, catalog: 0 })

  function nextRequest(kind: keyof typeof requestSequence.current) {
    requestSequence.current[kind] += 1
    return requestSequence.current[kind]
  }

  function isCurrentRequest(kind: keyof typeof requestSequence.current, request: number) {
    return requestSequence.current[kind] === request
  }

  function invalidateCatalogRequests() {
    requestSequence.current.available += 1
    requestSequence.current.meta += 1
    requestSequence.current.catalog += 1
    setFetching("")
    setLoadingMeta(false)
    setCatalogLoading(false)
  }

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
    requestSequence.current.catalog += 1
    setCatalogLoading(false)
    setCatalogOpen(false)
    setCatalogQuery("")
    setSelectedCatalogKey("")
  }

  async function fetchAvailableModels() {
    if (!selectedProviderConfigured) {
      setError("请先选择一个已配置渠道，再拉取模型")
      return
    }
    const request = nextRequest("available")
    const provider = selectedProvider
    setFetching("gateway")
    setError("")
    setAvailableModels([])
    try {
      const res = await adminApi.listAvailableModels(provider)
      if (isCurrentRequest("available", request)) setAvailableModels(res.models || [])
    } catch (err) {
      if (isCurrentRequest("available", request)) setError(err instanceof Error ? err.message : "模型拉取失败")
    } finally {
      if (isCurrentRequest("available", request)) setFetching("")
    }
  }

  async function loadCatalogMeta() {
    const id = currentDraft.id.trim()
    if (!id) {
      setError("请先填写模型 ID 再从 models.dev 加载")
      return
    }
    const request = nextRequest("meta")
    const operation = editorOwner.beginOperation()
    setLoadingMeta(true)
    setError("")
    try {
      const { model } = await adminApi.getCatalogModel(id)
      if (isCurrentRequest("meta", request) && editorOwner.owns(operation)) {
        updateCurrentDraft(catalogModelPatch(model))
      }
    } catch (err) {
      if (isCurrentRequest("meta", request) && editorOwner.owns(operation, false)) {
        setError(err instanceof Error ? `models.dev 目录未找到或加载失败：${err.message}` : "models.dev 目录未找到或加载失败")
      }
    } finally {
      if (isCurrentRequest("meta", request)) setLoadingMeta(false)
    }
  }

  async function openCatalogMatcher() {
    const nextOpen = !catalogOpen
    setCatalogOpen(nextOpen)
    setError("")
    if (!nextOpen) {
      requestSequence.current.catalog += 1
      setCatalogLoading(false)
      return
    }
    if (catalogModels.length > 0 || catalogLoading) return
    const request = nextRequest("catalog")
    setCatalogLoading(true)
    try {
      const res = await adminApi.listCatalogModels()
      if (isCurrentRequest("catalog", request)) setCatalogModels(res.models || [])
    } catch (err) {
      if (isCurrentRequest("catalog", request)) setError(err instanceof Error ? `models.dev 目录加载失败：${err.message}` : "models.dev 目录加载失败")
    } finally {
      if (isCurrentRequest("catalog", request)) setCatalogLoading(false)
    }
  }

  async function applySelectedCatalogModel() {
    if (!selectedCatalogModel) return
    const request = nextRequest("meta")
    const operation = editorOwner.beginOperation()
    const selected = selectedCatalogModel
    setLoadingMeta(true)
    setError("")
    try {
      const { model } = await adminApi.getCatalogModel(selected.id, selected.provider)
      if (isCurrentRequest("meta", request) && editorOwner.owns(operation)) {
        updateCurrentDraft(catalogModelPatch(model))
        clearCatalogMatcher()
      }
    } catch (err) {
      if (isCurrentRequest("meta", request) && editorOwner.owns(operation, false)) {
        setError(err instanceof Error ? `models.dev 匹配项加载失败：${err.message}` : "models.dev 匹配项加载失败")
      }
    } finally {
      if (isCurrentRequest("meta", request)) setLoadingMeta(false)
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
    invalidateCatalogRequests,
  }
}
