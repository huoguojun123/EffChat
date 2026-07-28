import { api } from "./client"
import type { SkillDefinition, SkillFileSummary } from "@/types"

export function listSkills() {
  return api.get<{ skills: SkillDefinition[] }>("/skills")
}

export function updateSessionSkills(sessionId: number, skills: string[]) {
  return api.put<{ skills_enabled: string[] }>(`/sessions/${sessionId}/skills`, { skills })
}

export function listSkillFiles(skillId: string) {
  return api.get<{ files: SkillFileSummary[] }>(`/skills/${encodeURIComponent(skillId)}/files`)
}

export function getSkillFileContent(skillId: string, path: string) {
  return api.get<{ file: SkillFileSummary; content: string }>(
    `/skills/${encodeURIComponent(skillId)}/files/content?path=${encodeURIComponent(path)}`
  )
}
