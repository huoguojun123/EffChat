import { beforeEach, describe, expect, it, vi } from "vitest"
import { clearAccountScopedStorage } from "@/lib/accountStorage"

const local = new Map<string, string>()
const session = new Map<string, string>()

function storage(map: Map<string, string>) {
  return {
    get length() { return map.size },
    key: (index: number) => [...map.keys()][index] ?? null,
    getItem: (key: string) => map.get(key) ?? null,
    setItem: (key: string, value: string) => { map.set(key, value) },
    removeItem: (key: string) => { map.delete(key) },
  }
}

vi.stubGlobal("localStorage", storage(local))
vi.stubGlobal("sessionStorage", storage(session))

beforeEach(() => {
  local.clear()
  session.clear()
})

describe("clearAccountScopedStorage", () => {
  it("removes account state without removing appearance preferences", () => {
    local.set("active_session_id", "42")
    local.set("session-memory-seen:42", "9")
    local.set("fchat_thinking_effort:gpt-test", "high")
    local.set("fchat:staged-selection:42", JSON.stringify({ manual: true, ids: [7] }))
    local.set("theme", "dark")
    local.set("chat_font_scale", "1.1")
    session.set("fchat:session-drafts", JSON.stringify({ 42: "private draft" }))
    session.set("fchat:diagram:v1:mermaid:1:abc", "private diagram")
    session.set("unrelated", "keep")

    clearAccountScopedStorage()

    expect(local.get("active_session_id")).toBeUndefined()
    expect(local.get("session-memory-seen:42")).toBeUndefined()
    expect(local.get("fchat_thinking_effort:gpt-test")).toBeUndefined()
    expect(local.get("fchat:staged-selection:42")).toBeUndefined()
    expect(local.get("theme")).toBe("dark")
    expect(local.get("chat_font_scale")).toBe("1.1")
    expect(session.get("fchat:session-drafts")).toBeUndefined()
    expect(session.get("fchat:diagram:v1:mermaid:1:abc")).toBeUndefined()
    expect(session.get("unrelated")).toBe("keep")
  })
})
