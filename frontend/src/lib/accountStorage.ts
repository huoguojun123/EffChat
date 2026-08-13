const accountLocalPrefixes = ["session-memory-seen:", "effchat_thinking_effort:", "effchat:staged-selection:"]
const accountSessionPrefixes = ["effchat:diagram:v1:"]

export function clearAccountScopedStorage() {
  if (typeof localStorage !== "undefined") {
    removeStorageKeys(localStorage, (key) => key === "active_session_id" || accountLocalPrefixes.some((prefix) => key.startsWith(prefix)))
  }
  if (typeof sessionStorage !== "undefined") {
    removeStorageKeys(sessionStorage, (key) => key === "effchat:session-drafts" || accountSessionPrefixes.some((prefix) => key.startsWith(prefix)))
  }
}

function removeStorageKeys(storage: Storage, shouldRemove: (key: string) => boolean) {
  for (let index = storage.length - 1; index >= 0; index -= 1) {
    const key = storage.key(index)
    if (key && shouldRemove(key)) storage.removeItem(key)
  }
}
