import { describe, expect, it } from "vitest"
import { visibleBlobState } from "@/hooks/useAuthedBlobUrl"

describe("useAuthedBlobUrl ownership", () => {
  it("renders loading immediately for undefined to A", () => {
    const ownerA = { fileId: 7, generation: 1 }

    expect(visibleBlobState(7, ownerA, null)).toEqual({ url: null, loading: true, error: false })
  })

  it("never exposes A success or error while B owns the render", () => {
    const ownerA = { fileId: 7, generation: 1 }
    const ownerB = { fileId: 8, generation: 2 }

    expect(visibleBlobState(8, ownerB, { owner: ownerA, url: "blob:a", loading: false, error: false })).toEqual({ url: null, loading: true, error: false })
    expect(visibleBlobState(8, ownerB, { owner: ownerA, url: null, loading: false, error: true })).toEqual({ url: null, loading: true, error: false })
  })

  it("does not reuse an old A state after A to B to A", () => {
    const firstA = { fileId: 7, generation: 1 }
    const secondA = { fileId: 7, generation: 3 }

    expect(secondA).not.toBe(firstA)
    expect(visibleBlobState(7, secondA, { owner: firstA, url: "blob:revoked-a", loading: false, error: false })).toEqual({ url: null, loading: true, error: false })
  })

  it("shows only state owned by the current generation and returns idle when cleared", () => {
    const owner = { fileId: 7, generation: 1 }
    const success = { owner, url: "blob:a", loading: false, error: false }

    expect(visibleBlobState(7, owner, success)).toBe(success)
    expect(visibleBlobState(undefined, { fileId: undefined, generation: 2 }, success)).toEqual({ url: null, loading: false, error: false })
  })
})
