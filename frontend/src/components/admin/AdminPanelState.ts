export interface AdminPanelState {
  loading: boolean
  refreshing: boolean
  error: string
  dirty: boolean
  lastLoadedAt: number | null
}

export function initialAdminPanelState(): AdminPanelState {
  return {
    loading: false,
    refreshing: false,
    error: "",
    dirty: false,
    lastLoadedAt: null,
  }
}

export function adminLoadStarted(prev: AdminPanelState): AdminPanelState {
  return {
    ...prev,
    loading: prev.lastLoadedAt == null,
    refreshing: prev.lastLoadedAt != null,
    error: "",
  }
}

export function adminLoadSucceeded(prev: AdminPanelState, loadedAt = Date.now()): AdminPanelState {
  return {
    ...prev,
    loading: false,
    refreshing: false,
    error: "",
    lastLoadedAt: loadedAt,
  }
}

export function adminLoadFailed(prev: AdminPanelState, error: string): AdminPanelState {
  return {
    ...prev,
    loading: false,
    refreshing: false,
    error,
  }
}

export function adminDirtyChanged(prev: AdminPanelState, dirty: boolean): AdminPanelState {
  return {
    ...prev,
    dirty,
  }
}
