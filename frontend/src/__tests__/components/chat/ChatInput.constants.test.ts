import { describe, expect, it } from "vitest"
import {
  COMPOSER_TEXTAREA_DESKTOP_MAX_HEIGHT,
  COMPOSER_TEXTAREA_MIN_HEIGHT,
  COMPOSER_TEXTAREA_MOBILE_MAX_HEIGHT,
  getComposerTextareaMaxHeight,
} from "@/components/chat/ChatInput.constants"

describe("getComposerTextareaMaxHeight", () => {
  it("starts with a compact two-line writing area", () => {
    expect(COMPOSER_TEXTAREA_MIN_HEIGHT).toBe(54)
  })

  it("keeps the desktop limit stable", () => {
    expect(getComposerTextareaMaxHeight(1440)).toBe(COMPOSER_TEXTAREA_DESKTOP_MAX_HEIGHT)
  })

  it("keeps mobile growth restrained", () => {
    expect(getComposerTextareaMaxHeight(390)).toBe(COMPOSER_TEXTAREA_MOBILE_MAX_HEIGHT)
  })
})
