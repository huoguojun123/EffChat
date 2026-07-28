import { describe, expect, it } from "vitest"
import { ACCENTS, COLOR_THEMES, colorTheme, normalizeAccent, normalizeColorTheme } from "@/lib/themes"

describe("color themes", () => {
  it("ships seven complete themes and six constrained accents", () => {
    expect(COLOR_THEMES.map((theme) => theme.id)).toEqual([
      "codex",
      "parchment",
      "github",
      "catppuccin",
      "everforest",
      "gruvbox",
      "one",
    ])
    expect(ACCENTS.map((accent) => accent.id)).toEqual(["default", "blue", "purple", "green", "amber", "rose"])
    for (const theme of COLOR_THEMES) {
      expect(theme.lightSwatches).toHaveLength(3)
      expect(theme.darkSwatches).toHaveLength(3)
      expect(theme.shikiLight).toBeTruthy()
      expect(theme.shikiDark).toBeTruthy()
    }
    expect(new Set(COLOR_THEMES.map((theme) => theme.lightSwatches.join(":"))).size).toBe(COLOR_THEMES.length)
    expect(new Set(COLOR_THEMES.map((theme) => theme.darkSwatches.join(":"))).size).toBe(COLOR_THEMES.length)
  })

  it("normalizes stale local preferences to stable defaults", () => {
    expect(normalizeColorTheme("gruvbox")).toBe("gruvbox")
    expect(normalizeColorTheme("missing")).toBe("codex")
    expect(normalizeAccent("rose")).toBe("rose")
    expect(normalizeAccent("custom-red")).toBe("default")
    expect(colorTheme("parchment").label).toBe("Parchment")
    expect(colorTheme("one").label).toBe("One")
  })
})
