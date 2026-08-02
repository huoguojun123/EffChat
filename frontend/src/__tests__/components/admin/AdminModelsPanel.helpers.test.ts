import { describe, expect, it } from "vitest"
import {
  catalogModelPatch,
  catalogSelectionKey,
  formatContext,
  groupLevelOptions,
  makeEmptyModel,
  markManualCatalogOverride,
  nextSortOrder,
  sortModels,
  toModelDraft,
  toModelPatch,
} from "@/components/admin/AdminModelsPanel.helpers"
import type { Model, UserGroup } from "@/types"

function model(overrides: Partial<Model> = {}): Model {
  return {
    id: "gpt-5",
    display_name: "GPT-5",
    provider: "openai",
    vision: true,
    tool_use: true,
    reasoning: true,
    thinking_format: "auto",
    search_impl: "",
    context_window: 128000,
    max_output: 16000,
    enabled: true,
    min_group_level: 0,
    sort_order: 10,
    catalog_source: "manual",
    lifecycle_status: "unknown",
    ...overrides,
  }
}

describe("AdminModelsPanel helpers", () => {
  it("creates provider defaults without mutating model identity fields", () => {
    const draft = makeEmptyModel("anthropic")

    expect(draft.provider).toBe("anthropic")
    expect(draft.id).toBe("")
    expect(draft.display_name).toBe("")
    expect(draft.search_impl).toBe("tool")
    expect(draft.context_window).toBe(1000000)
  })

  it("maps persisted models to editable drafts and update patches", () => {
    const draft = toModelDraft(model({ thinking_format: "", min_group_level: 2 }))
    const patch = toModelPatch(draft)

    expect(draft.thinking_format).toBe("auto")
    expect(draft.min_group_level).toBe(2)
    expect(patch).not.toHaveProperty("id")
    expect(patch.provider).toBe("openai")
  })

  it("extracts only catalog capability fields", () => {
    const patch = catalogModelPatch(model({ id: "claude-opus-4-1", provider: "anthropic", search_impl: "tool" }))

    expect(patch).toEqual({
      context_window: 128000,
      max_output: 16000,
      vision: true,
      tool_use: true,
      reasoning: true,
      thinking_format: "auto",
      search_impl: "tool",
      catalog_source: "manual",
      catalog_checked_at: null,
      lifecycle_status: "unknown",
    })
  })

  it("marks hand-edited capability fields as administrator overrides", () => {
    expect(markManualCatalogOverride({ max_output: 32000 }, { lifecycle_status: "preview" })).toEqual({
      max_output: 32000,
      catalog_source: "manual",
      catalog_checked_at: null,
      lifecycle_status: "preview",
    })
    expect(markManualCatalogOverride({ max_output: 32000, catalog_source: "models_dev", lifecycle_status: "preview" })).toEqual({
      max_output: 32000,
      catalog_source: "models_dev",
      lifecycle_status: "preview",
    })
  })

  it("keeps catalog selections unique across providers", () => {
    expect(catalogSelectionKey(model({ id: "sonar", provider: "perplexity" }))).not.toBe(catalogSelectionKey(model({ id: "sonar", provider: "openai" })))
  })

  it("sorts models and picks the next sparse sort order", () => {
    const models = [
      model({ id: "z", sort_order: 20 }),
      model({ id: "a", sort_order: 20 }),
      model({ id: "b", sort_order: 10 }),
    ]

    expect(sortModels(models).map((item) => item.id)).toEqual(["b", "a", "z"])
    expect(nextSortOrder(models)).toBe(30)
  })

  it("keeps current custom group levels selectable", () => {
    const groups: UserGroup[] = [
      { id: 1, name: "Member", level: 1, description: "", is_default: true, daily_message_limit: 0, daily_token_limit: 0, concurrent_run_limit: 0, daily_tool_call_limit: 0, daily_web_search_limit: 0, daily_web_extract_limit: 0, daily_ocr_file_limit: 0, daily_ocr_page_limit: 0, created_at: "", updated_at: "" },
      { id: 2, name: "Admin", level: 10, description: "", is_default: false, daily_message_limit: 0, daily_token_limit: 0, concurrent_run_limit: 0, daily_tool_call_limit: 0, daily_web_search_limit: 0, daily_web_extract_limit: 0, daily_ocr_file_limit: 0, daily_ocr_page_limit: 0, created_at: "", updated_at: "" },
    ]

    expect(groupLevelOptions(groups, 5)).toEqual([
      { level: 0, label: "所有人可见 (0)" },
      { level: 1, label: "Member (1)" },
      { level: 5, label: "自定义等级 (5)" },
      { level: 10, label: "Admin (10)" },
    ])
  })

  it("formats compact labels for list rows", () => {
    expect(formatContext(0)).toBe("-")
    expect(formatContext(1000000)).toBe("1M")
  })
})
