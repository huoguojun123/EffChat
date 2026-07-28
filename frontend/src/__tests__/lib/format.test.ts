import { describe, expect, it } from "vitest"
import { formatBytes } from "@/lib/format"

describe("formatBytes", () => {
  it("keeps attachment-sized values in their existing units", () => {
    expect(formatBytes(512)).toBe("512B")
    expect(formatBytes(32 * 1024)).toBe("32KB")
    expect(formatBytes(25 * 1024 * 1024)).toBe("25.0MB")
  })

  it("uses readable units for deployment storage capacity", () => {
    expect(formatBytes(494_332_366_848)).toBe("460.4GB")
    expect(formatBytes(2 * 1024 ** 4)).toBe("2.0TB")
  })
})
