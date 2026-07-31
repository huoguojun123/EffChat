import { describe, expect, it } from "vitest"
import { consumeCompactionSSE, consumeMemoryMaintenanceSSE, createStreamHTTPError, formatErrorDiagnostic, parseUsage, readSSEEvents, trimTrailingCodePoints } from "@/lib/sseProtocol"

function streamedResponse(chunks: Uint8Array[]) {
  return new Response(new ReadableStream({
    start(controller) {
      chunks.forEach((chunk) => controller.enqueue(chunk))
      controller.close()
    },
  }))
}

describe("SSE protocol helpers", () => {
  it("parses split UTF-8 frames and joins multiple data lines", async () => {
    const bytes = new TextEncoder().encode("event: content_delta\ndata: 你\ndata: 好\n\nevent: ping\ndata: {}\n\n")
    const events: Array<[string, string]> = []

    await readSSEEvents(streamedResponse([bytes.slice(0, 30), bytes.slice(30, 33), bytes.slice(33)]), (event, data) => {
      events.push([event, data])
    })

    expect(events).toEqual([["content_delta", "你\n好"], ["ping", "{}"]])
  })

  it("keeps Unicode code-point trimming and usage normalization stable", () => {
    expect(trimTrailingCodePoints("A😀中文", 2)).toBe("A😀")
    expect(parseUsage({
      prompt_tokens: 12,
      completion_tokens: 3,
      total_tokens: 15,
      prompt_token_details: { cached_tokens: 4 },
      completion_token_details: { reasoning_tokens: 2 },
    })).toEqual({
      prompt_tokens: 12,
      completion_tokens: 3,
      total_tokens: 15,
      cached_tokens: 4,
      reasoning_tokens: 2,
    })
  })

  it("preserves quota metadata on normalized HTTP errors", async () => {
    const error = await createStreamHTTPError(new Response(JSON.stringify({
      error: "已达到使用限额",
      code: "daily_message_limit",
      used: 10,
      limit: 10,
      reset_at: "2026-07-28T00:00:00Z",
      retryable: false,
      preserve_message_id: 42,
    }), { status: 429, headers: { "Content-Type": "application/json" } }))

    expect(error.message).toContain("10/10")
    expect(error.code).toBe("daily_message_limit")
    expect(error.retryable).toBe(false)
    expect(error.preserveMessageId).toBe(42)
  })

  it("keeps only the server-provided safe diagnostic summary", () => {
    expect(formatErrorDiagnostic({
      error: "上游模型额度不足",
      diagnostic: "HTTP 403 · 上游额度不足 · 请求 ID req-123456",
    })).toBe("HTTP 403 · 上游额度不足 · 请求 ID req-123456")
    expect(formatErrorDiagnostic({ diagnostic: { raw: "hidden" } })).toBe("")
  })

  it("requires an explicit compaction terminal event", async () => {
    const complete = streamedResponse([new TextEncoder().encode("event: compaction_complete\ndata: {}\n\n")])
    await expect(consumeCompactionSSE(complete)).resolves.toBe("complete")

    const missing = streamedResponse([new TextEncoder().encode("event: ping\ndata: {}\n\n")])
    await expect(consumeCompactionSSE(missing)).rejects.toThrow("压缩结果未确认")
  })

  it("requires an explicit memory maintenance terminal event", async () => {
    const complete = streamedResponse([new TextEncoder().encode("event: memory_maintenance_complete\ndata: {\"updated\":true}\n\n")])
    await expect(consumeMemoryMaintenanceSSE(complete)).resolves.toBeUndefined()

    const missing = streamedResponse([new TextEncoder().encode("event: memory_maintenance_start\ndata: {}\n\n")])
    await expect(consumeMemoryMaintenanceSSE(missing)).rejects.toThrow("记忆维护结果未确认")
  })
})
