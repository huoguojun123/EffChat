export function getEnabledSkillIds(metadata?: Record<string, unknown>) {
  const raw = metadata?.skills_enabled
  if (!Array.isArray(raw)) return []
  return raw.filter((item): item is string => typeof item === "string")
}
