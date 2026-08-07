export interface UsageQuerySnapshot {
  generation: number
  queryKey: string
}

// Usage requests share one owner because a manual refresh and a range effect
// can overlap. The generation fences same-query refreshes; queryKey fences a
// response after the selected range or custom dates change.
export class UsageQueryOwnership {
  private generation = 0
  private queryKey = ""

  activate(queryKey: string) {
    this.generation += 1
    this.queryKey = queryKey
  }

  begin(queryKey: string): UsageQuerySnapshot {
    this.activate(queryKey)
    return { generation: this.generation, queryKey }
  }

  owns(snapshot: UsageQuerySnapshot) {
    return snapshot.generation === this.generation && snapshot.queryKey === this.queryKey
  }
}
