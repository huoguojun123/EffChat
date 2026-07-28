import { beforeEach, describe, expect, it, vi } from "vitest"
import type { Model } from "@/types"
import { useModelStore } from "@/stores/models"
import * as modelsApi from "@/api/models"

vi.mock("@/api/models", () => ({
  listModels: vi.fn(),
}))

const listModelsMock = vi.mocked(modelsApi.listModels)

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((res) => {
    resolve = res
  })
  return { promise, resolve }
}

function model(id: string, sortOrder = 0): Model {
  return {
    id,
    display_name: id,
    provider: "openai",
    vision: false,
    tool_use: false,
    reasoning: false,
    thinking_format: "none",
    search_impl: "",
    context_window: 0,
    max_output: 0,
    sort_order: sortOrder,
    min_group_level: 0,
    enabled: true,
  }
}

beforeEach(() => {
  vi.clearAllMocks()
  useModelStore.setState({
    models: [],
    loading: false,
    loaded: false,
  })
})

describe("useModelStore", () => {
  it("强制刷新时旧响应不能覆盖新模型列表", async () => {
    const stale = deferred<{ models: Model[] }>()
    const fresh = deferred<{ models: Model[] }>()
    listModelsMock
      .mockReturnValueOnce(stale.promise)
      .mockReturnValueOnce(fresh.promise)

    const first = useModelStore.getState().loadModels()
    const second = useModelStore.getState().loadModels(true)

    fresh.resolve({ models: [model("fresh")] })
    await second
    stale.resolve({ models: [model("stale")] })
    await first

    expect(useModelStore.getState().models.map((item) => item.id)).toEqual(["fresh"])
    expect(useModelStore.getState().loading).toBe(false)
    expect(useModelStore.getState().loaded).toBe(true)
  })
})
