const accountLocalPrefixes = ["session-memory-seen:", "fchat_thinking_effort:", "fchat:staged-selection:"]
const accountSessionPrefixes = ["fchat:diagram:v1:"]

export function clearAccountScopedStorage() {
  if (typeof localStorage !== "undefined") {
    removeStorageKeys(localStorage, (key) => key === "active_session_id" || accountLocalPrefixes.some((prefix) => key.startsWith(prefix)))
  }
  if (typeof sessionStorage !== "undefined") {
    removeStorageKeys(sessionStorage, (key) => key === "fchat:session-drafts" || accountSessionPrefixes.some((prefix) => key.startsWith(prefix)))
  }
}

function removeStorageKeys(storage: Storage, shouldRemove: (key: string) => boolean) {
  for (let index = storage.length - 1; index >= 0; index -= 1) {
    const key = storage.key(index)
    if (key && shouldRemove(key)) storage.removeItem(key)
  }
}
