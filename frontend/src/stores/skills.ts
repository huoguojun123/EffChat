import { create } from "zustand"
import { listSkills } from "@/api/skills"
import type { SkillDefinition } from "@/types"

interface SkillState {
  skills: SkillDefinition[]
  loaded: boolean
  isLoading: boolean
  loadSkills: (force?: boolean) => Promise<void>
  refreshSkills: () => Promise<void>
  resetAccountState: () => void
}

let inflight: Promise<void> | null = null
let latestSkillsRequest = 0

export const useSkillStore = create<SkillState>()((set, get) => ({
  skills: [],
  loaded: false,
  isLoading: false,

  loadSkills: async (force = false) => {
    if (!force && get().loaded) return
    if (!force && inflight) return inflight
    const requestId = ++latestSkillsRequest
    const request = listSkills()
      .then((res) => {
        if (requestId !== latestSkillsRequest) return
        set({
          skills: (res.skills || []).filter((skill) => skill.enabled && skill.authorized),
          loaded: true,
          isLoading: false,
        })
      })
      .catch((err) => {
        if (requestId === latestSkillsRequest) set({ isLoading: false })
        throw err
      })
      .finally(() => {
        if (inflight === request) inflight = null
      })
    inflight = request
    set({ isLoading: true })
    return request
  },

  refreshSkills: async () => {
    await get().loadSkills(true)
  },

  resetAccountState: () => {
    latestSkillsRequest += 1
    inflight = null
    set({ skills: [], loaded: false, isLoading: false })
  },
}))
