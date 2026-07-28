import { create } from "zustand"
import * as modelsApi from "@/api/models"
import type { Model } from "@/types"

interface ModelState {
  models: Model[]
  loading: boolean
  loaded: boolean
  loadModels: (force?: boolean) => Promise<void>
  setModels: (models: Model[] | ((models: Model[]) => Model[])) => void
  upsertModel: (model: Model) => void
  removeModel: (id: string) => void
  resetAccountState: () => void
}

function sortModels(models: Model[]) {
  return [...models].sort((a, b) => a.sort_order - b.sort_order || a.id.localeCompare(b.id))
}

let latestModelsRequest = 0

export const useModelStore = create<ModelState>()((set, get) => ({
  models: [],
  loading: false,
  loaded: false,

  loadModels: async (force = false) => {
    if (!force && (get().loaded || get().loading)) return
    const requestId = ++latestModelsRequest
    set({ loading: true })
    try {
      const res = await modelsApi.listModels()
      if (requestId !== latestModelsRequest) return
      set({ models: sortModels(res.models || []), loaded: true })
    } finally {
      if (requestId === latestModelsRequest) set({ loading: false })
    }
  },

  setModels: (models) => set((state) => ({
    models: sortModels(typeof models === "function" ? models(state.models) : models),
    loaded: true,
  })),

  upsertModel: (model) => set((state) => ({
    models: sortModels(state.models.some((item) => item.id === model.id)
      ? state.models.map((item) => (item.id === model.id ? model : item))
      : [...state.models, model]),
    loaded: true,
  })),

  removeModel: (id) => set((state) => ({
    models: state.models.filter((item) => item.id !== id),
    loaded: true,
  })),

  resetAccountState: () => {
    latestModelsRequest += 1
    set({ models: [], loading: false, loaded: false })
  },
}))
