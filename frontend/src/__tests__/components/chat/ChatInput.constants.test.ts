import { describe, expect, it } from "vitest"
import {
  COMPOSER_TEXTAREA_DESKTOP_MAX_HEIGHT,
  COMPOSER_TEXTAREA_MOBILE_MAX_HEIGHT,
  getComposerTextareaMinHeight,
  getComposerTextareaMaxHeight,
} from "@/components/chat/ChatInput.constants"

describe("getComposerTextareaMaxHeight", () => {
  it("uses the responsive composer token with a safe fallback", () => {
    const ownerDocument = {
      documentElement: {},
      defaultView: {
        getComputedStyle: () => ({ getPropertyValue: () => "50px" }),
      },
    } as unknown as Document

    expect(getComposerTextareaMinHeight(ownerDocument)).toBe(50)
    expect(getComposerTextareaMinHeight()).toBe(54)
  })

  it("keeps the desktop limit stable", () => {
    expect(getComposerTextareaMaxHeight(1440)).toBe(COMPOSER_TEXTAREA_DESKTOP_MAX_HEIGHT)
  })

  it("keeps mobile growth restrained", () => {
    expect(getComposerTextareaMaxHeight(390)).toBe(COMPOSER_TEXTAREA_MOBILE_MAX_HEIGHT)
  })
})
