export interface LatestOperation {
  id: number
  signal: AbortSignal
}

// LatestOperationOwner gives one UI action exclusive ownership of its busy and
// error state. Starting or cancelling an operation invalidates every older
// callback, so a late catch/finally cannot overwrite the current action.
export class LatestOperationOwner {
  private sequence = 0
  private controller: AbortController | null = null

  begin(): LatestOperation {
    this.controller?.abort()
    this.controller = new AbortController()
    return { id: ++this.sequence, signal: this.controller.signal }
  }

  owns(operation: LatestOperation): boolean {
    return operation.id === this.sequence
  }

  release(operation: LatestOperation): boolean {
    if (!this.owns(operation)) return false
    this.controller = null
    return true
  }

  cancel() {
    this.sequence += 1
    this.controller?.abort()
    this.controller = null
  }
}
