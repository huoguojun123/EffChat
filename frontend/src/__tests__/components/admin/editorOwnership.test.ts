import { describe, expect, it } from "vitest"
import { BusyOwnership, EditorOwnership } from "@/components/admin/editorOwnership"

describe("EditorOwnership", () => {
  it("rejects a late response after the editor changes A to B to A", () => {
    const owner = new EditorOwnership()
    owner.activate("skill-a")
    const oldA = owner.beginOperation()
    owner.activate("skill-b")
    owner.activate("skill-a")

    expect(owner.owns(oldA, false)).toBe(false)
  })

  it("acknowledges an older saved revision without hiding newer edits", () => {
    const owner = new EditorOwnership()
    owner.activate("skill-a")
    owner.change()
    const save = owner.beginOperation()
    owner.change()

    expect(owner.owns(save)).toBe(false)
    expect(owner.owns(save, false)).toBe(true)
    owner.acknowledge(save.revision)
    expect(owner.isDirty()).toBe(true)
  })

  it("marks an unchanged successful revision clean", () => {
    const owner = new EditorOwnership()
    owner.activate("skill-a")
    owner.change()
    const save = owner.beginOperation()

    expect(owner.owns(save)).toBe(true)
    owner.acknowledge(save.revision)
    expect(owner.isDirty()).toBe(false)
  })

  it("keeps newer draft revisions when a created entity receives its id", () => {
    const owner = new EditorOwnership()
    owner.activate("new:provider-a")
    owner.change()
    const create = owner.beginOperation()
    owner.change()

    owner.rekey("model-a")
    owner.acknowledge(create.revision)

    expect(owner.currentEntityKey()).toBe("model-a")
    expect(owner.isDirty()).toBe(true)
  })
})

describe("BusyOwnership", () => {
  it("does not let an old finally release a newer operation", () => {
    const owner = new BusyOwnership()
    const first = owner.begin("load-a", "editor-a")
    const second = owner.begin("save-b", "editor-b")

    expect(owner.release(first)).toBe("save-b")
    expect(owner.release(second)).toBe("")
  })

  it("restores an older in-flight label when the newer operation finishes first", () => {
    const owner = new BusyOwnership()
    const first = owner.begin("load-a", "editor-a")
    const second = owner.begin("save-b", "editor-b")

    expect(owner.release(second)).toBe("load-a")
    expect(owner.release(first)).toBe("")
  })

  it("invalidates only the stale scope", () => {
    const owner = new BusyOwnership()
    owner.begin("load-a", "editor")
    owner.begin("zip-scan", "source")

    expect(owner.invalidate("editor")).toBe("zip-scan")
  })
})
