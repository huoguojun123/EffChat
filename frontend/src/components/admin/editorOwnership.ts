export interface EditorSnapshot {
  entityKey: string
  generation: number
  revision: number
}

// EditorOwnership is intentionally local and framework-free. It gives an
// asynchronous editor response enough identity to prove that it still owns the
// same entity and draft revision before mutating UI state.
export class EditorOwnership {
  private entityKey = ""
  private generation = 0
  private revision = 0
  private baselineRevision = 0

  activate(entityKey: string) {
    this.generation += 1
    this.entityKey = entityKey
    this.revision = 0
    this.baselineRevision = 0
  }

  invalidate() {
    this.activate("")
  }

  change() {
    this.revision += 1
  }

  beginOperation(): EditorSnapshot {
    return this.snapshot()
  }

  owns(operation: EditorSnapshot, requireRevision = true) {
    return operation.entityKey === this.entityKey
      && operation.generation === this.generation
      && (!requireRevision || operation.revision === this.revision)
  }

  acknowledge(revision: number) {
    if (revision > this.baselineRevision && revision <= this.revision) {
      this.baselineRevision = revision
    }
  }

  isDirty() {
    return this.revision !== this.baselineRevision
  }

  currentEntityKey() {
    return this.entityKey
  }

  private snapshot(): EditorSnapshot {
    return {
      entityKey: this.entityKey,
      generation: this.generation,
      revision: this.revision,
    }
  }
}

// BusyOwnership prevents an older request's finally block from clearing the
// busy state that belongs to a newer load, save, scan, or import operation.
export class BusyOwnership {
  private sequence = 0
  private active = new Map<number, { label: string; scope: string }>()

  begin(label: string, scope: string) {
    const operationId = ++this.sequence
    this.active.set(operationId, { label, scope })
    return operationId
  }

  release(operationId: number) {
    if (!this.active.delete(operationId)) return null
    return this.currentLabel()
  }

  invalidate(scope: string) {
    for (const [operationId, operation] of this.active) {
      if (operation.scope === scope) this.active.delete(operationId)
    }
    return this.currentLabel()
  }

  private currentLabel() {
    const remaining = Array.from(this.active.values())
    return remaining.at(-1)?.label || ""
  }
}
