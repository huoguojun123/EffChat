import { describe, expect, it } from "vitest"
import { defaultThinkingEffort, normalizeThinkingEffort, resolvedThinkingFormat, thinkingEffortOptions } from "@/lib/thinking"
import type { Model } from "@/types"

function model(patch: Partial<Model>): Model {
  return {
    id: "gpt-4o",
    display_name: "model",
    provider: "openai",
    vision: false,
    tool_use: true,
    reasoning: false,
    thinking_format: "auto",
    search_impl: "",
    context_window: 0,
    max_output: 0,
    enabled: true,
    min_group_level: 0,
    sort_order: 0,
    catalog_source: "manual",
    lifecycle_status: "unknown",
    temperature_policy: "configurable",
    ...patch,
  }
}

describe("thinking model metadata helpers", () => {
  it("uses backend resolved format instead of inferring in the browser", () => {
    expect(resolvedThinkingFormat(model({ resolved_thinking_format: "deepseek_v4" }))).toBe("deepseek_v4")
    expect(resolvedThinkingFormat(model({ id: "deepseek-v4-flash", resolved_thinking_format: undefined }))).toBe("none")
  })

  it("prefers backend runtime profile when present", () => {
    const m = model({
      resolved_thinking_format: "none",
      default_thinking_effort: "",
      runtime_profile: {
        family: "deepseek",
        wire_protocol: "openai-compatible",
        thinking_format: "deepseek_v4",
        default_thinking_effort: "high",
        supports_vision: false,
        supports_tools: true,
        search_impl: "",
        temperature_policy: "configurable",
        thinking_effort_options: [
          { value: "high", label: "High", desc: "default" },
          { value: "max", label: "Max", desc: "strongest" },
        ],
      },
    })

    expect(resolvedThinkingFormat(m)).toBe("deepseek_v4")
    expect(defaultThinkingEffort(m)).toBe("high")
    expect(thinkingEffortOptions(m).map((item) => item.value)).toEqual(["high", "max"])
  })

  it("returns backend effort options directly", () => {
    const m = model({
      thinking_effort_options: [
        { value: "high", label: "High", desc: "default" },
        { value: "max", label: "Max", desc: "strongest" },
      ],
    })
    expect(thinkingEffortOptions(m).map((item) => item.value)).toEqual(["high", "max"])
  })

  it("normalizes effort against backend options and backend default", () => {
    const m = model({
      default_thinking_effort: "high",
      thinking_effort_options: [
        { value: "high", label: "High", desc: "default" },
        { value: "max", label: "Max", desc: "strongest" },
      ],
    })
    expect(defaultThinkingEffort(m)).toBe("high")
    expect(normalizeThinkingEffort(m, "max")).toBe("max")
    expect(normalizeThinkingEffort(m, "low")).toBe("high")
  })

  it("accepts the complete GPT-5.6 effort range from backend metadata", () => {
    const options = ["none", "low", "medium", "high", "xhigh", "max"].map((value) => ({ value, label: value, desc: "" }))
    const m = model({ id: "gpt-5.6", default_thinking_effort: "medium", thinking_effort_options: options })

    expect(defaultThinkingEffort(m)).toBe("medium")
    expect(normalizeThinkingEffort(m, "none")).toBe("none")
    expect(normalizeThinkingEffort(m, "xhigh")).toBe("xhigh")
    expect(normalizeThinkingEffort(m, "max")).toBe("max")
  })
})
