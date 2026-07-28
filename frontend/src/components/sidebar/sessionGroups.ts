import type { Session } from "@/types"

export function groupSessionsByDate(sessions: Session[]) {
  const now = new Date()
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate())
  const yesterday = new Date(today.getTime() - 86400000)
  const lastWeek = new Date(today.getTime() - 7 * 86400000)

  const pinned = sessions.filter((session) => Boolean(session.pinned_at))
  const todayItems: Session[] = []
  const yesterdayItems: Session[] = []
  const recentItems: Session[] = []
  const olderItems: Session[] = []

  for (const session of sessions) {
    if (session.pinned_at) continue
    const date = new Date(session.created_at)
    if (date >= today) todayItems.push(session)
    else if (date >= yesterday) yesterdayItems.push(session)
    else if (date >= lastWeek) recentItems.push(session)
    else olderItems.push(session)
  }

  return [
    { label: "", items: pinned },
    { label: "今天", items: todayItems },
    { label: "昨天", items: yesterdayItems },
    { label: "最近 7 天", items: recentItems },
    { label: "更早", items: olderItems },
  ].filter((group) => group.items.length > 0)
}
