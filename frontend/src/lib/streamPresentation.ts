// Keep each DOM commit small enough that every visible tail gets its own
// animation lifetime. The budget is intentionally slower than transport.
const STREAM_VISUAL_RATE = 30
const STREAM_MIN_UNIT_CHARS = 6
const STREAM_MAX_PROSE_CHARS = 12
const STREAM_MIN_RELEASE_GAP_MS = 120

export interface StreamPresentationUnit {
  end: number
  visualCost: number
}

// A visual unit is deliberately smaller than a Markdown paragraph. It lets
// the presentation layer smooth bursty transport without inventing a second
// Markdown parser or relying on viewport-specific line measurements.
export function nextStreamPresentationUnit(
  target: string,
  offset: number,
  allowPartial = false,
): StreamPresentationUnit | null {
  if (offset >= target.length) return null
  const suffix = target.slice(offset)
  const chars = Array.from(suffix)
  if (chars.length === 0) return null

  const lineEnd = chars.indexOf("\n")
  if (insideFence(target.slice(0, offset)) || startsFence(suffix)) {
    if (lineEnd < 0) return null
    return createUnit(chars, lineEnd + 1, offset)
  }

  const limit = Math.min(chars.length, STREAM_MAX_PROSE_CHARS)
  let softBoundary = -1
  for (let index = 0; index < limit; index++) {
    const char = chars[index]
    if ("。！？!?".includes(char)) return createUnit(chars, index + 1, offset)
    if (char === "\n") return createUnit(chars, index + 1, offset)
    if (/\s/u.test(char) && index + 1 >= STREAM_MIN_UNIT_CHARS) softBoundary = index + 1
  }

  if (chars.length <= STREAM_MAX_PROSE_CHARS && !allowPartial) return null
  const end = softBoundary > 0 ? softBoundary : limit
  return createUnit(chars, end, offset)
}

export function presentationDelayMs(visualCost: number) {
  return Math.max(STREAM_MIN_RELEASE_GAP_MS, Math.ceil((visualCost / STREAM_VISUAL_RATE) * 1000))
}

function createUnit(chars: string[], end: number, offset: number): StreamPresentationUnit {
  const text = chars.slice(0, end).join("")
  return {
    end: offset + text.length,
    visualCost: visualCost(text),
  }
}

function startsFence(value: string) {
  return /^(?: {0,3})(?:```|~~~)/u.test(value)
}

function insideFence(value: string) {
  return (value.match(/^(?: {0,3})(?:```|~~~)/gmu) || []).length % 2 === 1
}

function visualCost(value: string) {
  let cost = 0
  let latinWordLength = 0
  const commitLatinWord = () => {
    if (latinWordLength > 0) cost += Math.ceil(latinWordLength / 4)
    latinWordLength = 0
  }

  for (const char of Array.from(value)) {
    if (/[A-Za-z0-9_]/u.test(char)) {
      latinWordLength++
      continue
    }
    commitLatinWord()
    if (/\s/u.test(char)) continue
    cost += /[\p{P}\p{S}]/u.test(char) ? 0.25 : 1
  }
  commitLatinWord()
  return Math.max(1, Math.ceil(cost))
}
