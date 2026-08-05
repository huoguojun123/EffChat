import { describe, expect, it } from "vitest"
import type { FontAsset } from "@/types"
import { chatFontFaceRule, normalizeChatFonts } from "@/stores/system"

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
  created_at: "2026-08-05T00:00:00Z",
  updated_at: "2026-08-05T00:00:00Z",
}

describe("normalizeChatFonts", () => {
  it("only uses legacy compatibility when a slot field is missing", () => {
    const result = normalizeChatFonts({ chinese: null, latin: undefined, code: null }, legacy)

    expect(result.chinese).toBeNull()
    expect(result.latin).toBe(legacy)
    expect(result.code).toBeNull()
  })

  it("routes CJK and Latin glyphs through disjoint font-face ranges", () => {
    const chinese = chatFontFaceRule("chinese", legacy)
    const latin = chatFontFaceRule("latin", legacy)
    const code = chatFontFaceRule("code", legacy)

    expect(chinese).toContain("U+4E00-9FFF")
    expect(chinese).not.toContain("U+0000-024F")
    expect(latin).toContain("unicode-range: U+0000-024F")
    expect(latin).not.toContain("U+4E00-9FFF")
    expect(code).not.toContain("unicode-range")
  })
})
