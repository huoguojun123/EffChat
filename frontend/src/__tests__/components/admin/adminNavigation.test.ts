import { describe, expect, it } from "vitest"
import { ADMIN_TABS, adminTab, isAdminTabKey, isConfigTab } from "@/components/admin/adminNavigation"

describe("admin navigation", () => {
  it("maps each supported route section to one visible tab", () => {
    expect(ADMIN_TABS.map((item) => item.key)).toContain("models")
    expect(ADMIN_TABS.map((item) => item.key)).toContain("status")
    expect(adminTab("models").label).toBe("模型")
    expect(isAdminTabKey("channels")).toBe(true)
    expect(isAdminTabKey("unknown")).toBe(false)
  })

  it("marks only editable configuration sections as dirty-navigation guarded", () => {
    expect(isConfigTab("config")).toBe(true)
    expect(isConfigTab("systemPrompt")).toBe(true)
    expect(isConfigTab("models")).toBe(false)
  })
})
