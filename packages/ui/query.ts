// Keep a response only while it belongs to the current view/mutation epoch.
// Invalidating permits an immediate fresh read without waiting for a stale one.
export class LatestQuery<T> {
  private revision = 0
  private pending: Promise<void> | undefined
  private controller: AbortController | undefined
  private load: (signal: AbortSignal) => Promise<T>
  private accept: (value: T) => void
  constructor(load: (signal: AbortSignal) => Promise<T>, accept: (value: T) => void) { this.load = load; this.accept = accept }
  invalidate() { this.revision++; this.controller?.abort(); this.controller = undefined; this.pending = undefined }
  run(): Promise<void> {
    if (this.pending) return this.pending
    const revision = this.revision
    const controller = this.controller = new AbortController()
    const pending = Promise.resolve().then(() => { controller.signal.throwIfAborted(); return this.load(controller.signal) }).then(value => {
      if (revision === this.revision) this.accept(value)
    }, error => {
      if (revision === this.revision) throw error
    }).finally(() => { if (this.pending === pending) { this.pending = undefined; this.controller = undefined } })
    this.pending = pending
    return pending
  }
}
