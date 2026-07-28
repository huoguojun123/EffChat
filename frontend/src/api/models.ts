import { api } from "./client"
import type { Model } from "@/types"

export function listModels() {
  return api.get<{ models: Model[] }>("/models")
}

export function getModel(id: string) {
  return api.get<Model>(`/models/${encodeURIComponent(id)}`)
}
