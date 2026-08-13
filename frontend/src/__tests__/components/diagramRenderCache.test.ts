import { afterEach, describe, expect, it } from "vitest"
import { clearDiagramRenderCache, readDiagramRenderCache, writeDiagramRenderCache } from "@/components/workspace/diagramRenderCache"

const diagram = (id: number) => ({ svg: `<svg id="${id}" />`, width: 100, height: 100, maxTextSize: 14 })

class MemoryStorage implements Storage {
  private readonly values = new Map<string, string>()

  get length() {
    return this.values.size
  }

  clear() { this.values.clear() }
  getItem(key: string) { return this.values.get(key) ?? null }
  key(index: number) { return [...this.values.keys()][index] ?? null }
  removeItem(key: string) { this.values.delete(key) }
  setItem(key: string, value: string) { this.values.set(key, value) }
}

Object.defineProperty(globalThis, "sessionStorage", {
  value: new MemoryStorage(),
  configurable: true,
})

afterEach(() => {
  clearDiagramRenderCache()
  sessionStorage.clear()
})

describe("diagram render cache", () => {
  it("migrates legacy cache entries without dropping sibling diagrams", () => {
    sessionStorage.setItem(legacyKey("mermaid", "first"), JSON.stringify(diagram(1)))
    sessionStorage.setItem(legacyKey("mermaid", "second"), JSON.stringify(diagram(2)))

    expect(readDiagramRenderCache("mermaid", "first")?.svg).toContain('id="1"')
    expect(readDiagramRenderCache("mermaid", "second")?.svg).toContain('id="2"')
  })

  it("keeps only the most recent bounded number of diagrams", () => {
    for (let index = 0; index < 27; index += 1) {
      writeDiagramRenderCache("mermaid", `diagram-${index}`, diagram(index))
    }

    expect(readDiagramRenderCache("mermaid", "diagram-0")).toBeNull()
    expect(readDiagramRenderCache("mermaid", "diagram-26")?.svg).toContain("26")
  })

  it("keeps storage in the same recent-use order as memory", () => {
    for (let index = 0; index < 24; index += 1) {
      writeDiagramRenderCache("graphviz", `diagram-${index}`, diagram(index))
    }

    expect(readDiagramRenderCache("graphviz", "diagram-0")?.svg).toContain("0")
    writeDiagramRenderCache("graphviz", "diagram-24", diagram(24))

    expect(readDiagramRenderCache("graphviz", "diagram-0")?.svg).toContain("0")
    expect(readDiagramRenderCache("graphviz", "diagram-1")).toBeNull()
    expect(sessionStorage.length).toBe(25)
  })
})

function legacyKey(kind: "mermaid" | "graphviz", source: string) {
  let hash = 5381
  for (let index = 0; index < source.length; index += 1) {
    hash = ((hash << 5) + hash) ^ source.charCodeAt(index)
  }
  return `effchat:diagram:v1:${kind}:${source.length}:${(hash >>> 0).toString(36)}`
}
