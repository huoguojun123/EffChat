import { describe, expect, it } from "vitest"
import { nextStreamPresentationUnit, presentationDelayMs } from "@/lib/streamPresentation"

describe("stream presentation", () => {
  it("keeps a complete prose prefix hidden until a safe or timed boundary", () => {
    const target = "没有标点的短语"
    expect(nextStreamPresentationUnit(target, 0)).toBeNull()
    expect(nextStreamPresentationUnit(target, 0, true)).toMatchObject({ end: target.length })
  })

  it("releases punctuation and newline boundaries without dropping Unicode text", () => {
    const target = "第一句。第二句\n第三句"
    const first = nextStreamPresentationUnit(target, 0)
    const second = nextStreamPresentationUnit(target, first?.end || 0)

    expect(target.slice(0, first?.end)).toBe("第一句。")
    expect(target.slice(first?.end, second?.end)).toBe("第二句\n")
  })

  it("waits for a complete fenced-code line instead of emitting code characters", () => {
    expect(nextStreamPresentationUnit("```ts\nconst value", 0)?.end).toBe(6)
    expect(nextStreamPresentationUnit("```ts\nconst value", 6)).toBeNull()
    const completed = "```ts\nconst value\n"
    expect(completed.slice(6, nextStreamPresentationUnit(completed, 6)?.end)).toBe("const value\n")
  })

  it("uses a bounded visual pace with a minimum overlap-friendly gap", () => {
    expect(presentationDelayMs(1)).toBe(60)
    expect(presentationDelayMs(15)).toBe(250)
  })
})
