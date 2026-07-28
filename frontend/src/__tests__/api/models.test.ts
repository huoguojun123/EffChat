import { describe, expect, it, vi } from "vitest"
import { getModel } from "@/api/models"
import { api } from "@/api/client"

vi.mock("@/api/client", () => ({
  api: {
    get: vi.fn(),
  },
}))

describe("models api paths", () => {
  it("encodes model ids containing slashes", () => {
    getModel("deepseek-ai/DeepSeek-V4-Flash")

    expect(api.get).toHaveBeenCalledWith("/models/deepseek-ai%2FDeepSeek-V4-Flash")
  })
})
