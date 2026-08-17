import type { ModelTestResponse } from "@/api/admin"
import type { Model, UserGroup } from "@/types"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { MotionView } from "@/components/ui/motion"
import { Database, Link2, PlugZap, Trash2 } from "lucide-react"
import { AdminModelCatalogMatcher } from "./AdminModelCatalogMatcher"
import { thinkingFormatOptions } from "./AdminModelsPanel.constants"
import { Field, ModelTestStatus, Select, Toggle } from "./AdminModelsPanel.controls"
import { catalogSourceLabel, formatCatalogCheckedAt, groupLevelOptions, lifecycleStatusLabel, thinkingFormatLabel } from "./AdminModelsPanel.helpers"
import type { ModelDraft } from "./AdminModelsPanel.types"

interface AdminModelEditorProps {
  creating: boolean
  currentModel: Model | null
  currentDraft: ModelDraft
  groups: UserGroup[]
  mobileDetailOpen: boolean
  testResult: ModelTestResponse | null
  testingModel: boolean
  loadingMeta: boolean
  catalogLoading: boolean
  catalogOpen: boolean
  catalogQuery: string
  selectedCatalogKey: string
  filteredCatalogModels: Model[]
  selectedCatalogModel: Model | null
  saving: string
  setCatalogQuery: (value: string) => void
  setSelectedCatalogKey: (value: string) => void
  updateCurrentDraft: (patch: Partial<ModelDraft>) => void
  onBack: () => void
  onTestCurrentModel: () => void | Promise<void>
  onLoadCatalogMeta: () => void | Promise<void>
  onOpenCatalogMatcher: () => void | Promise<void>
  onClearCatalogMatcher: () => void
  onApplySelectedCatalogModel: () => void | Promise<void>
  onDeleteModel: (model: Model) => void | Promise<void>
  onSaveDraft: () => void | Promise<void>
}

export function AdminModelEditor({
  creating,
  currentModel,
  currentDraft,
  groups,
  mobileDetailOpen,
  testResult,
  testingModel,
  loadingMeta,
  catalogLoading,
  catalogOpen,
  catalogQuery,
  selectedCatalogKey,
  filteredCatalogModels,
  selectedCatalogModel,
  saving,
  setCatalogQuery,
  setSelectedCatalogKey,
  updateCurrentDraft,
  onBack,
  onTestCurrentModel,
  onLoadCatalogMeta,
  onOpenCatalogMatcher,
  onClearCatalogMatcher,
  onApplySelectedCatalogModel,
  onDeleteModel,
  onSaveDraft,
}: AdminModelEditorProps) {
  const draftThinkingFormat = currentDraft.thinking_format || "auto"
  const persistedThinkingFormat = currentModel?.thinking_format || "auto"
  const showThinkingFallback = !creating &&
    currentModel &&
    draftThinkingFormat === persistedThinkingFormat &&
    persistedThinkingFormat !== "auto" &&
    currentModel.resolved_thinking_format &&
    currentModel.resolved_thinking_format !== persistedThinkingFormat
  const thinkingFallbackFormat = showThinkingFallback ? currentModel?.resolved_thinking_format || "" : ""
  const runtimeProfile = currentModel?.runtime_profile
  const runtimeThinkingOptions = runtimeProfile?.thinking_effort_options || currentModel?.thinking_effort_options || []
  const showOpenAIRequestProfile = currentModel?.channel_adapter === "openai_compatible"

  return (
    <div className={`min-h-0 flex-col overflow-hidden lg:flex ${mobileDetailOpen ? "flex" : "hidden lg:flex"}`}>
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border/70 px-4 py-3">
        <div className="flex min-w-0 items-center gap-2">
          <Button variant="ghost" size="sm" className="h-8 px-2 lg:hidden" onClick={onBack}>
            返回
          </Button>
          <div className="truncate font-medium">{creating ? "添加模型" : currentModel ? "模型参数" : "选择模型"}</div>
        </div>
        {(creating || currentModel) ? (
          <Button
            variant="outline"
            size="sm"
            onClick={() => void onTestCurrentModel()}
            disabled={testingModel || !currentDraft.id.trim() || !currentDraft.provider.trim()}
            title="验证最小文本对话连通性"
          >
            <PlugZap className="h-3.5 w-3.5" />
            {testingModel ? "检测中" : "连通检测"}
          </Button>
        ) : null}
      </div>

      <MotionView viewKey={creating ? "create" : currentModel?.id || "empty"} className="flex min-h-0 flex-1 flex-col">
        {creating || currentModel ? (
          <>
            <div className="min-h-0 flex-1 overflow-y-auto p-4">
              <div className="space-y-3">
                {testResult ? <ModelTestStatus result={testResult} /> : null}
                <Field label="模型 ID">
                  <Input name="effchat-model-id" autoComplete="off" value={currentDraft.id} onChange={(e) => updateCurrentDraft({ id: e.target.value })} disabled={!creating} />
                </Field>
                <Field label="显示名称">
                  <Input name="effchat-model-display-name" autoComplete="off" value={currentDraft.display_name} onChange={(e) => updateCurrentDraft({ display_name: e.target.value })} />
                </Field>
                <div className="grid gap-3 border-y border-border/60 py-3 sm:grid-cols-2">
                  <div className="min-w-0 text-xs text-muted-foreground">
                    <div className="text-foreground/80">能力来源：{catalogSourceLabel(currentDraft.catalog_source || "manual")}</div>
                    <div className="mt-1 truncate" title={currentDraft.catalog_checked_at || undefined}>核对时间：{formatCatalogCheckedAt(currentDraft.catalog_checked_at)}</div>
                  </div>
                  <Field label="生命周期">
                    <Select value={currentDraft.lifecycle_status || "unknown"} onChange={(lifecycle_status) => updateCurrentDraft({ lifecycle_status: lifecycle_status as Model["lifecycle_status"] })}>
                      {(["unknown", "active", "preview", "deprecated", "retired"] as const).map((status) => (
                        <option key={status} value={status}>{lifecycleStatusLabel(status)}</option>
                      ))}
                    </Select>
                  </Field>
                </div>
                <div className="grid grid-cols-2 gap-3">
                  <Field label="上下文">
                    <Input type="number" value={currentDraft.context_window} onChange={(e) => updateCurrentDraft({ context_window: Number(e.target.value) || 0 })} />
                  </Field>
                  <Field label="最大输出">
                    <Input type="number" value={currentDraft.max_output} onChange={(e) => updateCurrentDraft({ max_output: Number(e.target.value) || 0 })} />
                  </Field>
                </div>
                <div className="grid grid-cols-2 gap-3">
                  <Field label="模型排序">
                    <Input type="number" value={currentDraft.sort_order} onChange={(e) => updateCurrentDraft({ sort_order: Number(e.target.value) || 0 })} />
                  </Field>
                  <Field label="最低可见组等级">
                    <Select
                      value={String(currentDraft.min_group_level)}
                      onChange={(v) => updateCurrentDraft({ min_group_level: Math.max(0, Number(v) || 0) })}
                    >
                      {groupLevelOptions(groups, currentDraft.min_group_level).map((opt) => (
                        <option key={opt.level} value={String(opt.level)}>{opt.label}</option>
                      ))}
                    </Select>
                  </Field>
                </div>
                <div className="border-t border-border/70 pt-3">
                  <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
                    <div className="text-sm font-semibold">能力</div>
                    <div className="flex flex-wrap gap-2">
                      <Button
                        variant="outline"
                        size="sm"
                        className="h-8"
                        onClick={() => void onLoadCatalogMeta()}
                        disabled={loadingMeta || !currentDraft.id.trim()}
                        title="按当前模型 ID 从 models.dev 拉取能力并填入下方字段"
                      >
                        <Database className="h-3.5 w-3.5" />
                        {loadingMeta ? "加载中" : "补能力"}
                      </Button>
                      <Button
                        variant="outline"
                        size="sm"
                        className="h-8"
                        onClick={() => void onOpenCatalogMatcher()}
                        disabled={catalogLoading}
                        title="手动选择一个 models.dev 官方条目，只应用能力字段，不改当前模型 ID 和渠道"
                      >
                        <Link2 className="h-3.5 w-3.5" />
                        {catalogLoading ? "加载中" : "匹配"}
                      </Button>
                    </div>
                  </div>
                  {catalogOpen ? (
                    <AdminModelCatalogMatcher
                      catalogQuery={catalogQuery}
                      setCatalogQuery={setCatalogQuery}
                      selectedCatalogKey={selectedCatalogKey}
                      setSelectedCatalogKey={setSelectedCatalogKey}
                      filteredCatalogModels={filteredCatalogModels}
                      selectedCatalogModel={selectedCatalogModel}
                      loadingMeta={loadingMeta}
                      onApply={() => void onApplySelectedCatalogModel()}
                      onClose={onClearCatalogMatcher}
                    />
                  ) : null}
                  <div className="grid grid-cols-2 gap-2">
                    <Toggle label="视觉" checked={currentDraft.vision} onChange={(vision) => updateCurrentDraft({ vision })} />
                    <Toggle label="工具" checked={currentDraft.tool_use} onChange={(tool_use) => updateCurrentDraft({ tool_use })} />
                    <Toggle label="推理" checked={currentDraft.reasoning} onChange={(reasoning) => updateCurrentDraft({ reasoning })} />
                    <Toggle label="启用" checked={currentDraft.enabled} onChange={(enabled) => updateCurrentDraft({ enabled })} />
                  </div>
                </div>
                <div className="grid gap-3">
                  <div className="grid gap-3 sm:grid-cols-2">
                    <Field label="温度策略">
                      <Select value={currentDraft.temperature_policy || "configurable"} onChange={(temperature_policy) => updateCurrentDraft({
                        temperature_policy: temperature_policy as Model["temperature_policy"],
                        temperature_value: temperature_policy === "fixed" ? (currentDraft.temperature_value ?? 1) : null,
                      })}>
                        <option value="configurable">会话可配置</option>
                        <option value="omit">不发送温度</option>
                        <option value="fixed">固定温度</option>
                      </Select>
                    </Field>
                    {currentDraft.temperature_policy === "fixed" ? (
                      <Field label="固定温度">
                        <Input type="number" min="0" max="2" step="0.1" value={currentDraft.temperature_value ?? 1} onChange={(e) => updateCurrentDraft({ temperature_value: Number(e.target.value) })} />
                      </Field>
                    ) : (
                      <div className="self-end pb-2 text-xs leading-snug text-muted-foreground">
                        {currentDraft.temperature_policy === "omit" ? "请求中完全省略 temperature。" : "沿用会话设置；未设置时由模型服务决定。"}
                      </div>
                    )}
                  </div>
                  {showOpenAIRequestProfile ? (
                    <div className="border-t border-border/60 pt-3">
                      <div className="mb-2 text-sm font-semibold">OpenAI-compatible 固定参数</div>
                      <div className="grid gap-3 sm:grid-cols-2">
                        <Field label="Top P">
                          <Input
                            type="number"
                            min="0"
                            max="1"
                            step="0.1"
                            value={currentDraft.openai_request_profile?.top_p ?? ""}
                            onChange={(e) => updateCurrentDraft({ openai_request_profile: {
                              ...currentDraft.openai_request_profile,
                              top_p: e.target.value === "" ? null : Number(e.target.value),
                            } })}
                          />
                        </Field>
                        <Field label="候选数量 (n)">
                          <Input
                            type="number"
                            min="1"
                            step="1"
                            value={currentDraft.openai_request_profile?.n ?? ""}
                            onChange={(e) => updateCurrentDraft({ openai_request_profile: {
                              ...currentDraft.openai_request_profile,
                              n: e.target.value === "" ? null : Number(e.target.value),
                            } })}
                          />
                        </Field>
                        <Field label="Presence penalty">
                          <Input
                            type="number"
                            min="-2"
                            max="2"
                            step="0.1"
                            value={currentDraft.openai_request_profile?.presence_penalty ?? ""}
                            onChange={(e) => updateCurrentDraft({ openai_request_profile: {
                              ...currentDraft.openai_request_profile,
                              presence_penalty: e.target.value === "" ? null : Number(e.target.value),
                            } })}
                          />
                        </Field>
                        <Field label="Frequency penalty">
                          <Input
                            type="number"
                            min="-2"
                            max="2"
                            step="0.1"
                            value={currentDraft.openai_request_profile?.frequency_penalty ?? ""}
                            onChange={(e) => updateCurrentDraft({ openai_request_profile: {
                              ...currentDraft.openai_request_profile,
                              frequency_penalty: e.target.value === "" ? null : Number(e.target.value),
                            } })}
                          />
                        </Field>
                      </div>
                      <p className="mt-2 text-xs leading-snug text-muted-foreground">
                        留空时不发送对应字段；仅用于需要显式固定采样参数的 OpenAI-compatible 模型。
                      </p>
                    </div>
                  ) : null}
                  <Field label="思考方式">
                    <Select value={draftThinkingFormat} onChange={(thinking_format) => updateCurrentDraft({ thinking_format })}>
                      {thinkingFormatOptions.map((item) => (
                        <option key={item.value} value={item.value}>{item.label}</option>
                      ))}
                    </Select>
                    <p className="mt-1 text-xs leading-snug text-muted-foreground">
                      {thinkingFormatOptions.find((item) => item.value === draftThinkingFormat)?.hint}
                    </p>
                    {showThinkingFallback ? (
                      <p className="mt-1 text-xs leading-snug text-amber-600 dark:text-amber-300">
                        已退回 {thinkingFormatLabel(thinkingFallbackFormat)}；当前选择与模型/通道不匹配。
                      </p>
                    ) : null}
                    {runtimeProfile ? (
                      <div className="mt-2 border-t border-border/60 pt-2">
                        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
                          <span>运行时：{thinkingFormatLabel(runtimeProfile.thinking_format)}</span>
                          {runtimeProfile.default_thinking_effort ? <span>默认 {runtimeThinkingOptions.find((item) => item.value === runtimeProfile.default_thinking_effort)?.label || runtimeProfile.default_thinking_effort}</span> : null}
                        </div>
                        {runtimeThinkingOptions.length > 0 ? (
                          <div className="mt-1.5 flex flex-wrap gap-1.5">
                            {runtimeThinkingOptions.map((option) => (
                              <span
                                key={option.value}
                                title={option.desc}
                                className="rounded border border-border/80 px-1.5 py-0.5 text-xs text-foreground/80"
                              >
                                {option.label}
                              </span>
                            ))}
                          </div>
                        ) : runtimeProfile.thinking_format === "minimax_thinking" || runtimeProfile.thinking_format === "kimi_thinking" ? (
                          <p className="mt-1.5 text-xs text-muted-foreground">该模型固定开启思考</p>
                        ) : null}
                      </div>
                    ) : null}
                  </Field>
                </div>
              </div>
            </div>
            <div className="flex items-center justify-between border-t border-border/70 px-4 py-3">
              {!creating && currentModel ? (
                <Button variant="ghost" size="sm" className="text-destructive hover:text-destructive" onClick={() => void onDeleteModel(currentModel)} disabled={saving === `delete-${currentModel.id}`}>
                  <Trash2 className="h-3.5 w-3.5" />
                  删除
                </Button>
              ) : <span />}
              <Button size="sm" onClick={() => void onSaveDraft()} disabled={saving !== ""}>
                {creating ? "创建" : "保存"}
              </Button>
            </div>
          </>
        ) : (
          <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">暂无模型</div>
        )}
      </MotionView>
    </div>
  )
}
