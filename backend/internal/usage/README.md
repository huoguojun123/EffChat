# Usage Package Boundary

`internal/usage` records model API calls and lightweight tool usage facts for admin observability. It is intentionally not a billing, quota, or cost calculation system.

## What Counts

Events should be created only for calls that pass through an Eino classic `ChatModel` or typed `AgenticModel`:

- normal chat responses
- retry/regenerate calls
- title generation
- conversation compression
- small model calls inside tool chains, such as extraction or summarization helpers

The wrappers live at the model boundary (`WrapChatModel` / `WrapAgenticModel`) so callers do not have to remember to write usage events in every handler or service. Both share the stream observation and event projection implementation; classic compression-instruction inference and typed explicit call metadata remain separate.

## Tool Usage Stream

Non-model tools are stored in `tool_usage_events`, not in `model_usage_events`.

This stream records only operational facts needed for self-hosted anti-abuse:

- user/session/run/tool identity
- success or failure
- duration
- whether the tool result was truncated by the tool or a future compression policy
- estimated tool-result context tokens for observability only

It must not store raw tool arguments, API keys, or full tool results. Tool quotas are enforced by the Agent tool middleware using user-group limits. Calls that reserve a quota slot and then fail are still recorded; quota-blocked calls return structured errors without inserting another usage row.

## What Does Not Count

The current scope excludes non-model services:

- web search or web extraction vendors
- Python extractor work
- local file parsing, indexing, or cleanup
- future memory storage or search backends

Those services are not model API usage. Their current observability lives in `tool_usage_events`; future richer vendor telemetry should extend that stream or add a new one instead of mixing it into `model_usage_events`.

## Event Semantics

- Usage events are append-only facts for calls that happen after the migration exists; historical messages and historical tool results are not backfilled.
- Missing provider/model values normalize to `unknown`.
- Negative token or duration values normalize to zero.
- Failure events keep a short `error_type` and truncated `error_message`, not raw request or response payloads.
- Recording is best-effort and must never change the user-visible success or failure of the model call.
- Streams forward chunks unchanged and record once at termination, using the last cumulative usage frame rather than summing frames. A later error retains any observed usage; closing the consumer is classified as cancellation when the next chunk is forwarded. The caller must still cancel the provider context to interrupt a blocked upstream read.
- Missing usage currently yields zero-valued token fields. This storage contract does not distinguish unreported usage from measured zero, and does not persist cache-write usage; those require a separate adapter/storage change.

## Future Cost Or Quota Work

If cost display becomes necessary, add a `pricing_snapshot` or separate pricing table keyed by provider/model/time. Do not multiply old usage rows by current prices; provider pricing changes over time, and NewAPI/native routing can change the real billing path.

Quota policy lives in `internal/service.QuotaService`; this package records and aggregates facts. The tool middleware reserves a tool call through `QuotaService` before executing the tool, so concurrent calls cannot race past the same daily count limit. Tool result token estimates are displayed as context pressure, not used as a hard quota.

For the self-hosted alpha, user groups directly carry daily limits. A future resource-plan layer may be built above those fields if offline allocation becomes more formal. Payment, orders, recharge balance, invoices, refunds, and automatic renewal belong in a future billing module, not in `internal/usage`.
