import { api } from "./client"
import type { Prompt, PromptGroup } from "@/types"
import { collectOffsetPages, type OffsetPage } from "./pagination"

export interface PromptInput {
  title: string
  content: string
  description?: string
  group_id?: number | null
  group_name: string
  tags: string[]
  is_public: boolean
}

export interface PromptUpdate {
  title?: string
  content?: string
  description?: string
  group_id?: number | null
  group_name?: string
  tags?: string[]
  is_public?: boolean
}

export const promptsApi = {
  listMine(limit = 100, offset = 0) {
    return api.get<{ prompts: Prompt[]; total: number; has_more: boolean; next_offset: number }>(`/prompts?limit=${limit}&offset=${offset}`)
  },

  listAllMine() {
    return collectOffsetPages<Prompt>(async (limit, offset): Promise<OffsetPage<Prompt>> => {
      const page = await promptsApi.listMine(limit, offset)
      return { items: page.prompts || [], total: page.total, has_more: page.has_more, next_offset: page.next_offset }
    })
  },

  listPublic(limit = 100, offset = 0) {
    return api.get<{ prompts: Prompt[]; total: number; has_more: boolean; next_offset: number }>(`/prompts/public?limit=${limit}&offset=${offset}`)
  },

  listAllPublic() {
    return collectOffsetPages<Prompt>(async (limit, offset): Promise<OffsetPage<Prompt>> => {
      const page = await promptsApi.listPublic(limit, offset)
      return { items: page.prompts || [], total: page.total, has_more: page.has_more, next_offset: page.next_offset }
    })
  },

  create(data: PromptInput) {
    return api.post<Prompt>("/prompts", data)
  },

  update(id: number, data: PromptUpdate) {
    return api.patch<Prompt>(`/prompts/${id}`, data)
  },

  delete(id: number) {
    return api.delete<{ message: string }>(`/prompts/${id}`)
  },

  listGroups() {
    return api.get<{ groups: PromptGroup[] }>("/prompt-groups")
  },

  createGroup(name: string) {
    return api.post<PromptGroup>("/prompt-groups", { name })
  },

  updateGroup(id: number, name: string) {
    return api.patch<PromptGroup>(`/prompt-groups/${id}`, { name })
  },

  deleteGroup(id: number) {
    return api.delete<{ message: string }>(`/prompt-groups/${id}`)
  },
}
