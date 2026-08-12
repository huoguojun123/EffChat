import { describe, expect, it } from "vitest"
import { AttachmentQueueOwnership } from "@/components/chat/attachmentQueueOwnership"

describe("AttachmentQueueOwnership", () => {
  it("rejects a list snapshot after an upload or delete mutation", () => {
    const owner = new AttachmentQueueOwnership()
    owner.activate(11)
    const snapshot = owner.beginList(11)

    owner.mutate()

    expect(owner.ownsList(snapshot)).toBe(false)
  })

  it("allows only the latest same-session refresh", () => {
    const owner = new AttachmentQueueOwnership()
    owner.activate(11)
    const older = owner.beginList(11)
    const current = owner.beginList(11)

    expect(owner.ownsList(older)).toBe(false)
    expect(owner.ownsList(current)).toBe(true)
  })

  it("rejects an A response after A to B to A navigation", () => {
    const owner = new AttachmentQueueOwnership()
    owner.activate(11)
    const oldA = owner.beginList(11)
    owner.activate(12)
    owner.activate(11)

    expect(owner.ownsList(oldA)).toBe(false)
    expect(owner.ownsSession(11, owner.currentEpoch())).toBe(true)
  })

  it("lets only the current error timer clear feedback", () => {
    const owner = new AttachmentQueueOwnership()
    owner.activate(11)
    const older = owner.beginError()
    const current = owner.beginError()

    expect(owner.ownsError(older)).toBe(false)
    expect(owner.ownsError(current)).toBe(true)

    owner.activate(12)
    expect(owner.ownsError(current)).toBe(false)
  })
})
