export interface OffsetPage<T> {
  items: T[]
  total: number
  has_more: boolean
  next_offset: number
}

export async function collectOffsetPages<T>(load: (limit: number, offset: number) => Promise<OffsetPage<T>>, limit = 100) {
  const items: T[] = []
  let offset = 0
  while (true) {
    const page = await load(limit, offset)
    items.push(...page.items)
    if (!page.has_more) return items
    if (page.next_offset <= offset) throw new Error("分页响应未推进")
    offset = page.next_offset
  }
}
