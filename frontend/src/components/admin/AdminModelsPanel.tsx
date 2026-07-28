import { useMemo, useRef, useState } from "react"
import { adminApi, type ModelTestResponse, type UpdateModelInput } from "@/api/admin"
import type { Model } from "@/types"
import { Button } from "@/components/ui/button"
import { ArrowLeft } from "lucide-react"
import { AdminChannelOverviewPanel } from "./AdminChannelOverviewPanel"
import { AdminModelChannelSidebar } from "./AdminModelChannelSidebar"
import { AdminModelChannelWorkspace } from "./AdminModelChannelWorkspace"
import { AdminModelEditor } from "./AdminModelEditor"
import { AdminModelsListPane } from "./AdminModelsListPane"
import { providerDefaults } from "./AdminModelsPanel.constants"
import { useAdminModelCatalog } from "./useAdminModelCatalog"
import { useAdminModelSelection } from "./useAdminModelSelection"
import {
  makeEmptyModel,
  nextSortOrder,
  sortModels,
  toModelDraft,
  toModelPatch,
} from "./AdminModelsPanel.helpers"
import type { AdminModelsPanelProps, ModelDraft } from "./AdminModelsPanel.types"

const unconfiguredProviderKey = "__unconfigured__"
const newChannelKey = "__new_channel__"

export function AdminModelsPanel({ models, setModels, groups, channels = [], setChannels, setError }: AdminModelsPanelProps) {
  const {
    query,
    setQuery,
    activeProvider,
    setActiveProvider,
    editingId,
    setEditingId,
    creating,
    setCreating,
    mobileWorkspaceOpen,
    setMobileWorkspaceOpen,
    mobileDetailOpen,
    setMobileDetailOpen,
    modelManagementOpen,
    setModelManagementOpen,
  } = useAdminModelSelection({ initialProvider: channels[0]?.key || "", fallbackProvider: unconfiguredProviderKey })
  const [draft, setDraft] = useState<ModelDraft>(makeEmptyModel())
  const [saving, setSaving] = useState("")
  const [testingModel, setTestingModel] = useState(false)
  const [testResult, setTestResult] = useState<ModelTestResponse | null>(null)
  const testRequestSeq = useRef(0)
  const configuredChannelKeys = useMemo(() => new Set(channels.map((channel) => channel.key)), [channels])
  const channelLabels = useMemo(() => {
    const labels: Record<string, string> = { [unconfiguredProviderKey]: "未配置渠道" }
    for (const channel of channels) {
      labels[channel.key] = channel.display_name || channel.key
    }
    return labels
  }, [channels])
  const providerOptions = useMemo(() => {
    const options = channels.map((channel) => channel.key)
    if (models.some((model) => !configuredChannelKeys.has(model.provider))) {
      options.push(unconfiguredProviderKey)
    }
    return options.length > 0 ? options : [unconfiguredProviderKey]
  }, [channels, configuredChannelKeys, models])

  const selectedProvider = activeProvider === newChannelKey ? newChannelKey : providerOptions.includes(activeProvider) ? activeProvider : providerOptions[0] || unconfiguredProviderKey
  const selectedProviderConfigured = selectedProvider !== unconfiguredProviderKey && configuredChannelKeys.has(selectedProvider)
  const selectedChannel = channels.find((channel) => channel.key === selectedProvider) || null

  const grouped = useMemo(() => {
    const keyword = query.trim().toLowerCase()
    return providerOptions.map((provider) => {
      const items = models.filter((model) => {
        if (provider === unconfiguredProviderKey) {
          if (configuredChannelKeys.has(model.provider)) return false
        } else if (model.provider !== provider) {
          return false
        }
        if (!keyword) return true
        return [model.id, model.display_name, model.provider].some((value) => value.toLowerCase().includes(keyword))
      })
      return { provider, items }
    })
  }, [configuredChannelKeys, models, providerOptions, query])

  const currentItems = useMemo(() => {
    if (selectedProvider === newChannelKey) return []
    return grouped.find((group) => group.provider === selectedProvider)?.items || []
  }, [grouped, selectedProvider])
  const currentModel = useMemo(() => {
    if (creating) return null
    return currentItems.find((model) => model.id === editingId) || currentItems[0] || null
  }, [creating, currentItems, editingId])
  const currentDraft = creating
    ? draft
    : currentModel
      ? editingId === currentModel.id
        ? draft
        : toModelDraft(currentModel)
      : draft
  const {
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
  } = useAdminModelCatalog({
    currentDraft,
    selectedProvider,
    selectedProviderConfigured,
    models,
    setError,
    updateCurrentDraft,
  })

  function invalidateModelTest() {
    testRequestSeq.current += 1
    setTestingModel(false)
    setTestResult(null)
  }

  function isCurrentModelTest(seq: number) {
    return testRequestSeq.current === seq
  }

  function startCreate(provider = selectedProvider) {
    const targetProvider = provider === unconfiguredProviderKey || provider === newChannelKey ? channels[0]?.key || "" : provider
    if (!targetProvider) {
      setError("请先添加一个模型渠道，再添加模型")
      return
    }
    setCreating(true)
    setEditingId("")
    setDraft(makeEmptyModel(targetProvider))
    invalidateModelTest()
    clearCatalogMatcher()
    setMobileDetailOpen(true)
  }

  function startEdit(model: Model) {
    setCreating(false)
    setEditingId(model.id)
    setDraft(toModelDraft(model))
    invalidateModelTest()
    clearCatalogMatcher()
    setMobileDetailOpen(true)
  }

  function changeProvider(provider: string) {
    setActiveProvider(provider)
    setMobileWorkspaceOpen(true)
    setModelManagementOpen(false)
    setAvailableModels([])
    invalidateModelTest()
    clearCatalogMatcher()
    if (provider === newChannelKey) {
      setCreating(false)
      setEditingId("")
      setMobileDetailOpen(false)
      return
    }
    if (creating) {
      if (provider === unconfiguredProviderKey) {
        setCreating(false)
        setMobileDetailOpen(false)
        return
      }
      setDraft((prev) => ({
        ...prev,
        provider,
        ...(providerDefaults[provider] || {}),
      }))
      return
    }
    const nextItems = models.filter((model) => model.provider === provider)
    if (nextItems[0]) {
      setEditingId(nextItems[0].id)
      setDraft(toModelDraft(nextItems[0]))
    }
    setMobileDetailOpen(false)
  }

  function updateCurrentDraft(patch: Partial<ModelDraft>) {
    if (patch.id !== undefined || patch.provider !== undefined || patch.reasoning !== undefined || patch.thinking_format !== undefined) {
      invalidateModelTest()
    }
    if (!creating && currentModel && editingId !== currentModel.id) {
      setEditingId(currentModel.id)
      setDraft({ ...toModelDraft(currentModel), ...patch })
      return
    }
    setDraft((prev) => ({ ...prev, ...patch }))
  }

  async function toggleEnabled(model: Model) {
    startEdit(model)
    await savePatch(model.id, { enabled: !model.enabled })
  }

  async function savePatch(id: string, patch: UpdateModelInput) {
    setSaving(`model-${id}`)
    setError("")
    try {
      const updated = await adminApi.updateModel(id, patch)
      setModels((prev) => sortModels(prev.map((item) => (item.id === id ? updated : item))))
      if (currentModel?.id === id) setDraft(toModelDraft(updated))
    } catch (err) {
      setError(err instanceof Error ? err.message : "模型保存失败")
    } finally {
      setSaving("")
    }
  }

  async function saveDraft() {
    setSaving(creating ? "create" : `model-${editingId}`)
    setError("")
    try {
      if (creating) {
        const created = await adminApi.createModel(draft)
        setModels((prev) => sortModels([...prev, created]))
        setCreating(false)
        setEditingId(created.id)
        setActiveProvider(created.provider)
        setDraft(toModelDraft(created))
      } else if (currentModel) {
        const updated = await adminApi.updateModel(currentModel.id, toModelPatch(currentDraft))
        setModels((prev) => sortModels(prev.map((item) => (item.id === currentModel.id ? updated : item))))
        setDraft(toModelDraft(updated))
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "模型保存失败")
    } finally {
      setSaving("")
    }
  }

  async function runModelTest(id: string, provider: string) {
    id = id.trim()
    provider = provider.trim()
    if (!id || !provider) {
      setError("请先填写模型 ID 和渠道")
      return
    }
    const seq = testRequestSeq.current + 1
    testRequestSeq.current = seq
    setTestingModel(true)
    setTestResult(null)
    setError("")
    try {
      const result = await adminApi.testModel({
        id,
        provider,
      })
      if (isCurrentModelTest(seq)) {
        setTestResult(result)
      }
    } catch (err) {
      if (isCurrentModelTest(seq)) {
        setTestResult({
          ok: false,
          model_id: id,
          provider,
          error: err instanceof Error ? err.message : "模型检测失败",
        })
      }
    } finally {
      if (testRequestSeq.current === seq) {
        setTestingModel(false)
      }
    }
  }

  async function testCurrentModel() {
    await runModelTest(currentDraft.id, currentDraft.provider)
  }

  async function testModel(model: Model) {
    await runModelTest(model.id, model.provider)
  }

  function openModelManager(model?: Model) {
    setMobileWorkspaceOpen(true)
    setModelManagementOpen(true)
    if (model) {
      startEdit(model)
    }
  }

  function prepareImportModel(model: Model) {
    setCreating(true)
    setEditingId("")
    setDraft({ ...toModelDraft(model), enabled: true, sort_order: nextSortOrder(models) })
    setMobileDetailOpen(true)
  }

  async function importModel(model: Model) {
    setSaving(`import-${model.id}`)
    setError("")
    try {
      const created = await adminApi.createModel({
        ...toModelDraft(model),
        enabled: true,
        sort_order: nextSortOrder(models),
      })
      setModels((prev) => sortModels([...prev, created]))
      setEditingId(created.id)
      setDraft(toModelDraft(created))
      setCreating(false)
      setMobileDetailOpen(true)
    } catch (err) {
      setError(err instanceof Error ? err.message : "模型导入失败")
    } finally {
      setSaving("")
    }
  }

  async function deleteModel(model: Model) {
    setSaving(`delete-${model.id}`)
    setError("")
    try {
      await adminApi.deleteModel(model.id)
      setModels((prev) => prev.filter((item) => item.id !== model.id))
      if (currentModel?.id === model.id) {
        setEditingId("")
        setCreating(false)
        setMobileDetailOpen(false)
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "模型删除失败")
    } finally {
      setSaving("")
    }
  }

  return (
    <div className="grid h-full min-h-0 overflow-hidden lg:grid-cols-[260px_minmax(0,1fr)]">
      <AdminModelChannelSidebar
        visible={!mobileWorkspaceOpen}
        grouped={grouped}
        selectedProvider={selectedProvider}
        unconfiguredProviderKey={unconfiguredProviderKey}
        channelLabels={channelLabels}
        query={query}
        onQueryChange={setQuery}
        onSelectProvider={changeProvider}
        onCreateChannel={() => changeProvider(newChannelKey)}
      />

      <AdminModelChannelWorkspace
        visible={mobileWorkspaceOpen}
        title={channelLabels[selectedProvider] || selectedProvider}
        onBackToChannels={() => {
          setMobileWorkspaceOpen(false)
          setMobileDetailOpen(false)
        }}
      >
        {setChannels && !modelManagementOpen ? (
          <AdminChannelOverviewPanel
            key={selectedProvider}
            channel={selectedChannel}
            isNew={selectedProvider === newChannelKey}
            isUnconfigured={selectedProvider === unconfiguredProviderKey}
            models={models}
            currentItems={currentItems}
            availableModels={availableModels}
            fetching={fetching}
            saving={saving}
            testingModel={testingModel}
            testResult={testResult}
            setChannels={setChannels}
            setError={setError}
            onSaved={changeProvider}
            onDeleted={() => changeProvider(channels.find((channel) => channel.key !== selectedProvider)?.key || unconfiguredProviderKey)}
            onFetchAvailableModels={fetchAvailableModels}
            onOpenModelManager={() => openModelManager()}
            onStartEdit={(model) => openModelManager(model)}
            onToggleEnabled={toggleEnabled}
            onTestModel={testModel}
          />
        ) : (
          <>
            <div className="flex shrink-0 items-center justify-between gap-3 border-b border-border/70 px-3 py-2">
              <Button variant="ghost" size="sm" onClick={() => setModelManagementOpen(false)}>
                <ArrowLeft className="h-3.5 w-3.5" />
                返回渠道
              </Button>
              <div className="min-w-0 truncate text-sm font-medium">{channelLabels[selectedProvider] || selectedProvider}</div>
            </div>
            <div className="grid min-h-0 flex-1 overflow-hidden lg:grid-cols-[minmax(220px,300px)_minmax(0,1fr)] xl:grid-cols-[320px_minmax(0,1fr)]">
              <AdminModelsListPane
                activeProvider={selectedProvider}
                currentItems={currentItems}
                importableItems={importableItems}
                currentModel={currentModel}
                channelLabels={channelLabels}
                providerConfigured={selectedProviderConfigured}
                saving={saving}
                fetching={fetching}
                mobileDetailOpen={mobileDetailOpen}
                onFetchAvailableModels={fetchAvailableModels}
                onStartCreate={startCreate}
                onStartEdit={startEdit}
                onToggleEnabled={toggleEnabled}
                onPrepareImportModel={prepareImportModel}
                onImportModel={importModel}
              />

              <AdminModelEditor
                creating={creating}
                currentModel={currentModel}
                currentDraft={currentDraft}
                groups={groups}
                mobileDetailOpen={mobileDetailOpen}
                testResult={testResult}
                testingModel={testingModel}
                loadingMeta={loadingMeta}
                catalogLoading={catalogLoading}
                catalogOpen={catalogOpen}
                catalogQuery={catalogQuery}
                selectedCatalogKey={selectedCatalogKey}
                filteredCatalogModels={filteredCatalogModels}
                selectedCatalogModel={selectedCatalogModel}
                saving={saving}
                setCatalogQuery={setCatalogQuery}
                setSelectedCatalogKey={setSelectedCatalogKey}
                updateCurrentDraft={updateCurrentDraft}
                onBack={() => setMobileDetailOpen(false)}
                onTestCurrentModel={testCurrentModel}
                onLoadCatalogMeta={loadCatalogMeta}
                onOpenCatalogMatcher={openCatalogMatcher}
                onClearCatalogMatcher={clearCatalogMatcher}
                onApplySelectedCatalogModel={applySelectedCatalogModel}
                onDeleteModel={deleteModel}
                onSaveDraft={saveDraft}
              />
            </div>
          </>
        )}
      </AdminModelChannelWorkspace>
    </div>
  )
}
