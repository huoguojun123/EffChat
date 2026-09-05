import type { StreamLifecycleState } from "@/types"

export function isStreamingInteractionBusy(status: StreamLifecycleState) {
  return status === "sending" || status === "streaming" || status === "recovering" || status === "syncing" || status === "finalizing"
}

export function isStreamingAbortable(status: StreamLifecycleState) {
  return status === "sending" || status === "streaming" || status === "recovering"
}

export function isStreamingDisplayActive(status: StreamLifecycleState) {
  // Before admission, the composer owns the visible "sending" state. Showing
  // an assistant slot before the accepted user turn makes the conversation
  // appear out of order and misrepresents a request that may still fail.
  return status === "streaming" || status === "recovering" || status === "syncing"
}
