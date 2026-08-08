import { describe, expect, it } from "vitest"
import { ACCENTS, COLOR_THEMES, THEME_PREVIEW_COLORS, accentPreviewColor, colorTheme, normalizeAccent, normalizeColorTheme } from "@/lib/themes"

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
      expect(theme.shikiLight).toBeTruthy()
      expect(theme.shikiDark).toBeTruthy()
    }
    expect(THEME_PREVIEW_COLORS).toEqual([
      "var(--theme-bg)",
      "var(--theme-surface)",
      "var(--theme-default-accent)",
    ])
    expect(accentPreviewColor("default")).toBe("var(--theme-default-accent)")
    expect(accentPreviewColor("blue")).toBe("var(--accent-blue)")
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
