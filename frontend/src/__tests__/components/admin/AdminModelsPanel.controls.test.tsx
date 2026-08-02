import { renderToStaticMarkup } from "react-dom/server"
import { describe, expect, it } from "vitest"
import { ModelTestStatus } from "@/components/admin/AdminModelsPanel.controls"

describe("ModelTestStatus", () => {
  it("distinguishes an unexpected complete probe response from a transport failure", () => {
    const html = renderToStaticMarkup(
      <ModelTestStatus
        result={{
          ok: false,
          model_id: "fixture-model",
          provider: "fixture-provider",
          code: "model_probe_unexpected_output",
          error: "model probe returned an unexpected response",
          output: "NOT OK",
          duration_ms: 42,
        }}
      />,
    )

    expect(html).toContain("响应不符合探测要求")
    expect(html).toContain("模型返回：")
    expect(html).toContain("NOT OK")
    expect(html).not.toContain(">连通失败<")
  })
})
