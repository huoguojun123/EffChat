import type { StreamLifecycleState } from "@/types"

export function isStreamingInteractionBusy(status: StreamLifecycleState) {
  return status === "sending" || status === "streaming" || status === "recovering" || status === "syncing" || status === "finalizing"
}

export function isStreamingAbortable(status: StreamLifecycleState) {
  return status === "sending" || status === "streaming" || status === "recovering"
}

export function isStreamingDisplayActive(status: StreamLifecycleState) {
  return status === "sending" || status === "streaming" || status === "recovering" || status === "syncing"
}
