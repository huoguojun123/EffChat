import { describe, expect, it } from "vitest"
import type { FontAsset } from "@/types"
import { normalizeChatFonts } from "@/stores/system"

const legacy: FontAsset = {
  id: 1,
  display_name: "Legacy",
  family_name: "Legacy",
  file_name: "legacy.woff2",
  file_url: "/fonts/legacy.woff2",
  mime_type: "font/woff2",
  file_size: 1,
  checksum: "legacy",
  weight: 400,
  style: "normal",
  enabled: true,
}

describe("normalizeChatFonts", () => {
  it("only uses legacy compatibility when a slot field is missing", () => {
    const result = normalizeChatFonts({ chinese: null, latin: undefined, code: null }, legacy)

    expect(result.chinese).toBeNull()
    expect(result.latin).toBe(legacy)
    expect(result.code).toBeNull()
  })
})
