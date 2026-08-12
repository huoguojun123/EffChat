export interface AttachmentListSnapshot {
  sessionId: number
  sessionEpoch: number
  listGeneration: number
  attachmentRevision: number
}

// AttachmentQueueOwnership is the single commit boundary for staged-list
// responses. A response may replace the visible list only when it is the newest
// request for the same session lifecycle and no local attachment mutation has
// happened since it started.
export class AttachmentQueueOwnership {
  private sessionId: number | null = null
  private sessionEpoch = 0
  private listGeneration = 0
  private attachmentRevision = 0
  private errorGeneration = 0

  activate(sessionId: number | null) {
    this.sessionId = sessionId
    this.sessionEpoch += 1
    this.listGeneration = 0
    this.attachmentRevision = 0
    this.errorGeneration += 1
  }

  beginList(sessionId: number): AttachmentListSnapshot {
    return {
      sessionId,
      sessionEpoch: this.sessionEpoch,
      listGeneration: ++this.listGeneration,
      attachmentRevision: this.attachmentRevision,
    }
  }

  ownsList(snapshot: AttachmentListSnapshot): boolean {
    return this.sessionId === snapshot.sessionId
      && this.sessionEpoch === snapshot.sessionEpoch
      && this.listGeneration === snapshot.listGeneration
      && this.attachmentRevision === snapshot.attachmentRevision
  }

  mutate() {
    this.attachmentRevision += 1
  }

  beginError() {
    return ++this.errorGeneration
  }

  ownsError(errorGeneration: number) {
    return this.errorGeneration === errorGeneration
  }

  currentEpoch() {
    return this.sessionEpoch
  }

  ownsSession(sessionId: number, sessionEpoch: number) {
    return this.sessionId === sessionId && this.sessionEpoch === sessionEpoch
  }
}
