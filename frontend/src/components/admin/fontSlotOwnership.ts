import type { ChatFontSelection } from "@/api/admin"

export type FontSlot = keyof ChatFontSelection
export type FontSlotGenerations = Record<FontSlot, number>

const slots: FontSlot[] = ["chinese", "latin", "code"]

export class FontSlotOwnership {
  private generations: FontSlotGenerations = { chinese: 0, latin: 0, code: 0 }

  begin(slot: FontSlot) {
    this.generations[slot] += 1
    return this.generations[slot]
  }

  invalidateAll(): FontSlotGenerations {
    for (const slot of slots) this.generations[slot] += 1
    return { ...this.generations }
  }

  owns(slot: FontSlot, generation: number) {
    return this.generations[slot] === generation
  }
}

export class FontActionOwnership {
  private generations = new Map<string, number>()

  begin(action: string) {
    const generation = (this.generations.get(action) || 0) + 1
    this.generations.set(action, generation)
    return generation
  }

  owns(action: string, generation: number) {
    return this.generations.get(action) === generation
  }
}
