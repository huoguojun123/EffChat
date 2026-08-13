import { describe, expect, it } from "vitest"
import { FontActionOwnership, FontSlotOwnership } from "@/components/admin/fontSlotOwnership"

describe("FontSlotOwnership", () => {
  it("keeps independent slot requests current when they complete in reverse order", () => {
    const owner = new FontSlotOwnership()
    const chinese = owner.begin("chinese")
    const latin = owner.begin("latin")

    expect(owner.owns("latin", latin)).toBe(true)
    expect(owner.owns("chinese", chinese)).toBe(true)
  })

  it("rejects an older request after the same slot starts again", () => {
    const owner = new FontSlotOwnership()
    const first = owner.begin("code")
    const second = owner.begin("code")

    expect(owner.owns("code", first)).toBe(false)
    expect(owner.owns("code", second)).toBe(true)
  })

  it("fences pending slot responses around a lifecycle mutation without blocking newer intent", () => {
    const owner = new FontSlotOwnership()
    const staleChinese = owner.begin("chinese")
    const lifecycle = owner.invalidateAll()
    const newerLatin = owner.begin("latin")

    expect(owner.owns("chinese", staleChinese)).toBe(false)
    expect(owner.owns("chinese", lifecycle.chinese)).toBe(true)
    expect(owner.owns("latin", lifecycle.latin)).toBe(false)
    expect(owner.owns("latin", newerLatin)).toBe(true)
  })
})

describe("FontActionOwnership", () => {
  it("does not let an older finally release a repeated action", () => {
    const owner = new FontActionOwnership()
    const first = owner.begin("delete-7")
    const second = owner.begin("delete-7")

    expect(owner.owns("delete-7", first)).toBe(false)
    expect(owner.owns("delete-7", second)).toBe(true)
  })

  it("keeps unrelated actions independent", () => {
    const owner = new FontActionOwnership()
    const upload = owner.begin("upload")
    const toggle = owner.begin("toggle-9")

    expect(owner.owns("upload", upload)).toBe(true)
    expect(owner.owns("toggle-9", toggle)).toBe(true)
  })
})
