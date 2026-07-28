import { describe, expect, it } from "vitest"
import { isStreamingAbortable, isStreamingDisplayActive, isStreamingInteractionBusy } from "@/lib/streamingStatus"

describe("streamingStatus helpers", () => {
  it("treats syncing as busy for user actions", () => {
    expect(isStreamingInteractionBusy("syncing")).toBe(true)
    expect(isStreamingInteractionBusy("recovering")).toBe(true)
    expect(isStreamingInteractionBusy("idle")).toBe(false)
  })

  it("keeps finalizing out of the live streaming indicator", () => {
    expect(isStreamingDisplayActive("finalizing")).toBe(false)
    expect(isStreamingDisplayActive("streaming")).toBe(true)
    expect(isStreamingDisplayActive("recovering")).toBe(true)
  })

  it("only exposes stop while an active stream can still be cancelled", () => {
    expect(isStreamingAbortable("sending")).toBe(true)
    expect(isStreamingAbortable("streaming")).toBe(true)
    expect(isStreamingAbortable("recovering")).toBe(true)
    expect(isStreamingAbortable("syncing")).toBe(false)
    expect(isStreamingAbortable("finalizing")).toBe(false)
  })
})
