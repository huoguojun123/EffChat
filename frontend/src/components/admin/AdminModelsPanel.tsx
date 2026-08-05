import { useEffect, useMemo, useRef, useState } from "react"
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
  markManualCatalogOverride,
  nextSortOrder,
  sortModels,
  toModelDraft,
  toModelPatch,
} from "./AdminModelsPanel.helpers"
import type { AdminModelsPanelProps, ModelDraft } from "./AdminModelsPanel.types"
import { useChatStore } from "@/stores/chat"
import { BusyOwnership, EditorOwnership } from "./editorOwnership"

const unconfiguredProviderKey = "__unconfigured__"
const newChannelKey = "__new_channel__"

export function AdminModelsPanel({ models, setModels, groups, channels = [], setChannels, setError, onDirtyChange }: AdminModelsPanelProps) {
  const loadSessionCreateReadiness = useChatStore((state) => state.loadSessionCreateReadiness)
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
  const [channelDirty, setChannelDirty] = useState(false)
  const testRequestSeq = useRef(0)
  const [editorOwner] = useState(() => new EditorOwnership())
  const [importOwner] = useState(() => new EditorOwnership())
  const [busyOwner] = useState(() => new BusyOwnership())
  const panelDirty = editorOwner.isDirty() || channelDirty

  useEffect(() => onDirtyChange?.(panelDirty), [onDirtyChange, panelDirty])
  useEffect(() => () => onDirtyChange?.(false), [onDirtyChange])
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
    invalidateCatalogRequests,
  } = useAdminModelCatalog({
    currentDraft,
    selectedProvider,
    selectedProviderConfigured,
    models,
    setError,
    updateCurrentDraft,
    editorOwner,
  })

  function beginBusy(label: string, scope: string) {
    const operationId = busyOwner.begin(label, scope)
    setSaving(label)
    return operationId
  }

  function finishBusy(operationId: number) {
    const remainingLabel = busyOwner.release(operationId)
    if (remainingLabel !== null) setSaving(remainingLabel)
  }

  function invalidateBusy(scope: string) {
    setSaving(busyOwner.invalidate(scope))
  }

  function canLeaveModelEditor(nextEntityKey: string) {
    if (!editorOwner.isDirty()) return true
    if (editorOwner.currentEntityKey() === nextEntityKey) return false
    return window.confirm("放弃当前模型的未保存修改？")
  }

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
    if (!canLeaveModelEditor(`new:${targetProvider}`)) return
    invalidateBusy("model-editor")
    invalidateCatalogRequests()
    editorOwner.activate(`new:${targetProvider}`)
    setCreating(true)
    setEditingId("")
    setDraft(makeEmptyModel(targetProvider))
    invalidateModelTest()
    clearCatalogMatcher()
    setMobileDetailOpen(true)
  }

  function startEdit(model: Model) {
    if (editorOwner.currentEntityKey() === model.id && editingId === model.id && !creating) return
    if (!canLeaveModelEditor(model.id)) return
    invalidateBusy("model-editor")
    invalidateCatalogRequests()
    editorOwner.activate(model.id)
    setCreating(false)
    setEditingId(model.id)
    setDraft(toModelDraft(model))
    invalidateModelTest()
    clearCatalogMatcher()
    setMobileDetailOpen(true)
  }

  function changeProvider(provider: string, allowDirty = false) {
    if (modelManagementOpen && !canLeaveModelEditor(`provider:${provider}`)) return
    if (!modelManagementOpen && channelDirty && provider !== selectedProvider && !allowDirty && !window.confirm("放弃当前渠道的未保存修改？")) return
    invalidateBusy("model-editor")
    invalidateCatalogRequests()
    editorOwner.invalidate()
    setChannelDirty(false)
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
      editorOwner.activate(`new:${provider}`)
      setDraft((prev) => ({
        ...prev,
        provider,
        ...(providerDefaults[provider] || {}),
      }))
      return
    }
    const nextItems = models.filter((model) => model.provider === provider)
    if (nextItems[0]) {
      editorOwner.activate(nextItems[0].id)
      setEditingId(nextItems[0].id)
      setDraft(toModelDraft(nextItems[0]))
    }
    setMobileDetailOpen(false)
  }

  function updateCurrentDraft(patch: Partial<ModelDraft>) {
    patch = markManualCatalogOverride(patch, currentDraft)
    if (patch.id !== undefined || patch.provider !== undefined || patch.reasoning !== undefined || patch.thinking_format !== undefined) {
      invalidateModelTest()
    }
    if (!creating && currentModel && editingId !== currentModel.id) {
      editorOwner.activate(currentModel.id)
      editorOwner.change()
      setEditingId(currentModel.id)
      setDraft({ ...toModelDraft(currentModel), ...patch })
      return
    }
    if (!editorOwner.currentEntityKey()) {
      editorOwner.activate(creating ? `new:${currentDraft.provider}` : currentModel?.id || editingId)
    }
    editorOwner.change()
    setDraft((prev) => ({ ...prev, ...patch }))
  }

  async function toggleEnabled(model: Model) {
    await savePatch(model.id, { enabled: !model.enabled })
  }

  async function savePatch(id: string, patch: UpdateModelInput) {
    const busy = beginBusy(`model-${id}`, `model-patch:${id}`)
    setError("")
    try {
      const updated = await adminApi.updateModel(id, patch)
      setModels((prev) => sortModels(prev.map((item) => (item.id === id ? updated : item))))
      void loadSessionCreateReadiness(true)
      if (editorOwner.currentEntityKey() === id && !editorOwner.isDirty()) {
        setDraft(toModelDraft(updated))
        editorOwner.activate(id)
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "模型保存失败")
    } finally {
      finishBusy(busy)
    }
  }

  async function saveDraft() {
    const currentCreating = creating
    const current = currentDraft
    const currentID = currentModel?.id || editingId
    const operation = editorOwner.beginOperation()
    const busy = beginBusy(currentCreating ? "create" : `model-${currentID}`, "model-editor")
    setError("")
    try {
      if (currentCreating) {
        const created = await adminApi.createModel(current)
        setModels((prev) => sortModels([...prev, created]))
        void loadSessionCreateReadiness(true)
        if (editorOwner.owns(operation, false)) {
          const unchanged = editorOwner.owns(operation)
          setCreating(false)
          setEditingId(created.id)
          setActiveProvider(created.provider)
          editorOwner.rekey(created.id)
          editorOwner.acknowledge(operation.revision)
          if (unchanged) {
            setDraft(toModelDraft(created))
          } else {
            setError("已保存较早版本，当前修改仍未保存")
          }
        }
      } else if (currentID) {
        const updated = await adminApi.updateModel(currentID, toModelPatch(current))
        setModels((prev) => sortModels(prev.map((item) => (item.id === currentID ? updated : item))))
        void loadSessionCreateReadiness(true)
        if (editorOwner.owns(operation, false)) {
          editorOwner.acknowledge(operation.revision)
          if (editorOwner.owns(operation)) {
            setDraft(toModelDraft(updated))
          } else {
            setError("已保存较早版本，当前修改仍未保存")
          }
        }
      }
    } catch (err) {
      if (editorOwner.owns(operation, false)) {
        setError(err instanceof Error ? err.message : "模型保存失败")
      }
    } finally {
      finishBusy(busy)
    }
  }

  async function runModelTest(
    id: string,
    provider: string,
    temperaturePolicy?: Model["temperature_policy"],
    temperatureValue?: number | null,
    openAIRequestProfile?: Model["openai_request_profile"],
  ) {
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
        temperature_policy: temperaturePolicy,
        temperature_value: temperaturePolicy === "fixed" ? temperatureValue : null,
        openai_request_profile: openAIRequestProfile,
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
    await runModelTest(currentDraft.id, currentDraft.provider, currentDraft.temperature_policy, currentDraft.temperature_value, currentDraft.openai_request_profile)
  }

  async function testModel(model: Model) {
    await runModelTest(model.id, model.provider, model.temperature_policy, model.temperature_value, model.openai_request_profile)
  }

  function openModelManager(model?: Model) {
    if (channelDirty && !window.confirm("放弃当前渠道的未保存修改？")) return
    setChannelDirty(false)
    setMobileWorkspaceOpen(true)
    setModelManagementOpen(true)
    if (model) {
      startEdit(model)
    }
  }

  function prepareImportModel(model: Model) {
    if (!canLeaveModelEditor(`new:${model.provider}`)) return
    invalidateBusy("model-editor")
    invalidateCatalogRequests()
    editorOwner.activate(`new:${model.provider}`)
    setCreating(true)
    setEditingId("")
    setDraft({ ...toModelDraft(model), enabled: true, sort_order: nextSortOrder(models) })
    setMobileDetailOpen(true)
  }

  async function importModel(model: Model) {
    importOwner.activate(`import:${model.provider}:${model.id}`)
    const operation = importOwner.beginOperation()
    const editorOperation = editorOwner.beginOperation()
    const busy = beginBusy(`import-${model.id}`, "model-import")
    setError("")
    try {
      const created = await adminApi.createModel({
        ...toModelDraft(model),
        enabled: true,
        sort_order: nextSortOrder(models),
      })
      setModels((prev) => sortModels([...prev, created]))
      void loadSessionCreateReadiness(true)
      if (importOwner.owns(operation) && editorOwner.owns(editorOperation) && !editorOwner.isDirty()) {
        editorOwner.activate(created.id)
        setEditingId(created.id)
        setDraft(toModelDraft(created))
        setCreating(false)
        setMobileDetailOpen(true)
      }
    } catch (err) {
      if (importOwner.owns(operation, false)) {
        setError(err instanceof Error ? err.message : "模型导入失败")
      }
    } finally {
      finishBusy(busy)
    }
  }

  async function deleteModel(model: Model) {
    const operation = editorOwner.beginOperation()
    const busy = beginBusy(`delete-${model.id}`, "model-editor")
    setError("")
    try {
      await adminApi.deleteModel(model.id)
      setModels((prev) => prev.filter((item) => item.id !== model.id))
      void loadSessionCreateReadiness(true)
      if (editorOwner.owns(operation, false) && editorOwner.currentEntityKey() === model.id) {
        editorOwner.invalidate()
        setEditingId("")
        setCreating(false)
        setMobileDetailOpen(false)
      }
    } catch (err) {
      if (editorOwner.owns(operation, false)) {
        setError(err instanceof Error ? err.message : "模型删除失败")
      }
    } finally {
      finishBusy(busy)
    }
  }

  function leaveModelManager() {
    if (!canLeaveModelEditor("channel-overview")) return
    invalidateBusy("model-editor")
    invalidateCatalogRequests()
    editorOwner.invalidate()
    setModelManagementOpen(false)
    setMobileDetailOpen(false)
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
            onSaved={(key) => changeProvider(key, true)}
            onDeleted={() => changeProvider(channels.find((channel) => channel.key !== selectedProvider)?.key || unconfiguredProviderKey, true)}
            onDirtyChange={setChannelDirty}
            onFetchAvailableModels={fetchAvailableModels}
            onOpenModelManager={() => openModelManager()}
            onStartEdit={(model) => openModelManager(model)}
            onToggleEnabled={toggleEnabled}
            onTestModel={testModel}
          />
        ) : (
          <>
            <div className="flex shrink-0 items-center justify-between gap-3 border-b border-border/70 px-3 py-2">
              <Button variant="ghost" size="sm" onClick={leaveModelManager}>
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
