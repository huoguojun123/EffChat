import type { PreparedDiagramSvg } from "./diagramSvg"

type DiagramKind = "mermaid" | "graphviz"

const prefix = "effchat:diagram:v1:"
const indexKey = `${prefix}index`
const maxEntries = 24
const memoryCache = new Map<string, PreparedDiagramSvg>()

export function readDiagramRenderCache(kind: DiagramKind, source: string): PreparedDiagramSvg | null {
  const key = cacheKey(kind, source)
  const memoryValue = memoryCache.get(key)
  if (memoryValue) {
    touchMemory(key, memoryValue)
    touchStorage(key)
    return memoryValue
  }
  if (typeof sessionStorage === "undefined") return null

  try {
    const value: unknown = JSON.parse(sessionStorage.getItem(key) || "null")
    if (!isPreparedDiagramSvg(value)) return null
    touchMemory(key, value)
    touchStorage(key)
    return value
  } catch {
    return null
  }
}

export function writeDiagramRenderCache(kind: DiagramKind, source: string, diagram: PreparedDiagramSvg) {
  const key = cacheKey(kind, source)
  touchMemory(key, diagram)
  if (typeof sessionStorage === "undefined") return

  try {
    sessionStorage.setItem(key, JSON.stringify(diagram))
    touchStorage(key)
  } catch {
    return
  }
}

export function clearDiagramRenderCache() {
  memoryCache.clear()
  if (typeof sessionStorage === "undefined") return
  try {
    for (const key of storageKeys()) {
      if (key.startsWith(prefix)) sessionStorage.removeItem(key)
    }
  } catch {
    return
  }
}

function touchMemory(key: string, diagram: PreparedDiagramSvg) {
  memoryCache.delete(key)
  memoryCache.set(key, diagram)
  while (memoryCache.size > maxEntries) memoryCache.delete(memoryCache.keys().next().value as string)
}

function touchStorage(key: string) {
  if (typeof sessionStorage === "undefined") return
  try {
    const previous = storageIndexOrLegacyEntries().filter((item) => item !== key)
    const next = [...previous, key]
    while (next.length > maxEntries) {
      const expired = next.shift()
      if (expired) sessionStorage.removeItem(expired)
    }
    sessionStorage.setItem(indexKey, JSON.stringify(next))
  } catch {
    return
  }
}

function storageIndexOrLegacyEntries(): string[] {
  if (sessionStorage.getItem(indexKey) !== null) return readStorageIndex()

  const legacyEntries = storageKeys().filter((key) => {
    if (!key.startsWith(prefix) || key === indexKey) return false
    try {
      return isPreparedDiagramSvg(JSON.parse(sessionStorage.getItem(key) || "null"))
    } catch {
      return false
    }
  })
  while (legacyEntries.length > maxEntries) {
    const expired = legacyEntries.shift()
    if (expired) sessionStorage.removeItem(expired)
  }
  return legacyEntries
}

function storageKeys() {
  return Array.from({ length: sessionStorage.length }, (_, index) => sessionStorage.key(index)).filter((key): key is string => key !== null)
}

function readStorageIndex(): string[] {
  try {
    const value: unknown = JSON.parse(sessionStorage.getItem(indexKey) || "[]")
    return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string" && item.startsWith(prefix) && item !== indexKey) : []
  } catch {
    return []
  }
}

function isPreparedDiagramSvg(value: unknown): value is PreparedDiagramSvg {
  if (!value || typeof value !== "object") return false
  const diagram = value as Partial<PreparedDiagramSvg>
  return typeof diagram.svg === "string" &&
    Number.isFinite(diagram.width) &&
    Number.isFinite(diagram.height) &&
    Number.isFinite(diagram.maxTextSize) &&
    (!diagram.initialFocus || (
      Number.isFinite(diagram.initialFocus.x) &&
      Number.isFinite(diagram.initialFocus.y)
    ))
}

function cacheKey(kind: DiagramKind, source: string) {
  return `${prefix}${kind}:${source.length}:${hash(source)}`
}

function hash(value: string) {
  let result = 5381
  for (let index = 0; index < value.length; index += 1) {
    result = ((result << 5) + result) ^ value.charCodeAt(index)
  }
  return (result >>> 0).toString(36)
}
