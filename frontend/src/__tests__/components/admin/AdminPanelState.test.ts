import { describe, expect, it } from "vitest"
import {
  adminDirtyChanged,
  adminLoadFailed,
  adminLoadStarted,
  adminLoadSucceeded,
  initialAdminPanelState,
} from "@/components/admin/AdminPanelState"

describe("AdminPanelState helpers", () => {
  it("treats first load as loading and later loads as refreshing", () => {
    const first = adminLoadStarted(initialAdminPanelState())
    expect(first.loading).toBe(true)
    expect(first.refreshing).toBe(false)

    const loaded = adminLoadSucceeded(first, 123)
    const refresh = adminLoadStarted(loaded)
    expect(refresh.loading).toBe(false)
    expect(refresh.refreshing).toBe(true)
  })

  it("stores errors without losing dirty state", () => {
    const dirty = adminDirtyChanged(initialAdminPanelState(), true)
    const failed = adminLoadFailed(dirty, "加载失败")

    expect(failed.dirty).toBe(true)
    expect(failed.error).toBe("加载失败")
    expect(failed.loading).toBe(false)
    expect(failed.refreshing).toBe(false)
  })
})
