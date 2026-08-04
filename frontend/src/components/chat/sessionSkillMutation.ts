import type { Session } from "@/types"
import { getEnabledSkillIds } from "./chatInputMetadata"

interface SkillMutationState {
  desired: string[]
  generation: number
  running: boolean
}

interface SessionSkillMutationCoordinatorOptions {
  update: (sessionId: number, skills: string[]) => Promise<{ skills_enabled: string[] }>
  reload: (sessionId: number) => Promise<string[]>
  applyLocal: (sessionId: number, skills: string[]) => void
  onError?: (message: string) => void
}

/**
 * Serializes same-session Skill writes while coalescing rapid clicks to the
 * latest desired set. The server still owns canonical filtering; this
 * coordinator only prevents an older response or rollback from overwriting a
 * newer user intent or a different session's local state.
 */
export class SessionSkillMutationCoordinator {
  private readonly states = new Map<number, SkillMutationState>()
  private readonly options: SessionSkillMutationCoordinatorOptions

  constructor(options: SessionSkillMutationCoordinatorOptions) {
    this.options = options
  }

  toggle(sessionId: number, currentSkills: string[], skillId: string) {
    const state = this.states.get(sessionId) ?? { desired: [], generation: 0, running: false }
    // Once a session is idle, the store may have been refreshed by another
    // owner (for example a tab reload). Rebase from that canonical snapshot;
    // while a request is running, keep the queued desired set instead.
    if (!state.running) state.desired = [...currentSkills].sort()
    const next = new Set(state.desired)
    if (next.has(skillId)) next.delete(skillId)
    else next.add(skillId)
    state.desired = [...next].sort()
    state.generation += 1
    this.states.set(sessionId, state)
    this.options.applyLocal(sessionId, state.desired)
    if (!state.running) void this.drain(sessionId, state)
  }

  invalidateAll() {
    this.states.clear()
  }

  private async drain(sessionId: number, state: SkillMutationState) {
    state.running = true
    try {
      while (this.states.get(sessionId) === state) {
        const generation = state.generation
        const desired = [...state.desired]
        try {
          const response = await this.options.update(sessionId, desired)
          if (this.states.get(sessionId) !== state) return
          if (state.generation === generation) {
            state.desired = [...response.skills_enabled].sort()
            this.options.applyLocal(sessionId, state.desired)
          }
        } catch {
          if (this.states.get(sessionId) !== state) return
          if (state.generation === generation) {
            try {
              const canonical = await this.options.reload(sessionId)
              if (this.states.get(sessionId) !== state || state.generation !== generation) continue
              state.desired = [...canonical].sort()
              this.options.applyLocal(sessionId, state.desired)
            } catch {
              if (this.states.get(sessionId) !== state || state.generation !== generation) continue
              this.options.onError?.("Skill 更新失败，请稍后重试")
            }
          }
        }
        if (state.generation === generation) break
      }
    } finally {
      if (this.states.get(sessionId) === state) {
        state.running = false
      }
    }
  }
}

export function sessionSkills(session?: Pick<Session, "metadata">) {
  return getEnabledSkillIds(session?.metadata)
}
