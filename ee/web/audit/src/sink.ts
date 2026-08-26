// Streaming audit entries to where a security team already looks.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// The community edition writes an audit log that cannot be rewritten and
// carries a hash chain anyone can verify. What a security team asks for next is
// not a better log; it is the same log arriving in Splunk, or Event Hubs, or an
// S3 bucket, because that is where their alerts already run and a log nobody
// looks at is not a control.
//
// One rule governs everything here, and it is the one that makes this safe to
// add: the primary log is written whatever a sink does. Forwarding is a copy.
// A sink that is unreachable, misconfigured, throttled, or slow loses forwarding
// and never loses an entry, and it must never be able to slow down or fail the
// action being audited. The alternative, where an audit sink can fail a write,
// turns an outage at somebody else's log aggregator into an outage here.
//
// So: a bounded queue, delivery in order, retries with backoff, and a state
// the dashboard can show. When the queue fills, the oldest are dropped and the
// count is reported, because the primary log still has every entry and a
// forwarder that grows without bound takes the process down with it.

import { createHmac, createHash } from 'node:crypto'

/** One entry as a sink receives it. Names, actions, and targets: the same
 *  fields the primary log holds, which carry no data by design. */
export interface Entry {
  seq: number
  orgId: string
  actor: string
  action: string
  targetType: string
  targetId: string | null
  origin: string
  detail: Record<string, unknown>
  occurredAt: string
  entryHash: string
}

export interface Batch {
  entries: Entry[]
  /** Signed, so a batch that arrives in an object store can be checked without
   *  reaching back to the control plane that produced it. */
  manifest: Manifest
}

export interface Manifest {
  org: string
  count: number
  firstSeq: number
  lastSeq: number
  /** The chain hash of the last entry, which is what makes a sequence of
   *  batches verifiable end to end rather than one batch at a time. */
  headHash: string
  /** sha256 over the canonical batch body. */
  digest: string
  /** HMAC of the digest under the sink's key. */
  signature: string
}

export interface Sink {
  name(): string
  /** Delivers a batch. Throwing means "try again later", and the queue decides
   *  how often. A sink that cannot ever succeed should say so by throwing a
   *  PermanentError, which stops the retries rather than repeating them
   *  forever against a misconfiguration. */
  deliver(batch: Batch): Promise<void>
}

/** A failure retrying will not fix: a bad credential, a bucket that does not
 *  exist, a payload the endpoint refuses. Retrying those forever is how a
 *  queue fills with one entry nobody will ever accept. */
export class PermanentError extends Error {}

export interface Clock {
  now(): Date
}

export interface QueueOptions {
  sink: Sink
  clock: Clock
  /** Entries per batch. */
  batchSize?: number
  /** How many entries may wait. Beyond it the oldest are dropped, because the
   *  primary log still holds them and an unbounded forwarder takes the process
   *  down with it. */
  capacity?: number
  /** Signing key for batch manifests. */
  key: string
  maxAttempts?: number
}

export type SinkState =
  | { state: 'idle' }
  | { state: 'delivering'; pending: number }
  | { state: 'retrying'; pending: number; attempt: number; reason: string }
  | { state: 'failed'; pending: number; reason: string }

/**
 * Buffers entries and delivers them in order.
 *
 * In order, because an audit trail that arrives out of order is one somebody
 * has to sort before they can read it, and the sequence numbers exist precisely
 * so nobody has to.
 */
export class Queue {
  private readonly opts: Required<Omit<QueueOptions, 'sink' | 'clock' | 'key'>> &
    Pick<QueueOptions, 'sink' | 'clock' | 'key'>
  private pending: Entry[] = []
  private droppedCount = 0
  private status: SinkState = { state: 'idle' }
  private attempt = 0

  constructor(options: QueueOptions) {
    this.opts = {
      batchSize: options.batchSize ?? 100,
      capacity: options.capacity ?? 10_000,
      maxAttempts: options.maxAttempts ?? 6,
      sink: options.sink,
      clock: options.clock,
      key: options.key,
    }
  }

  /** Never throws and never blocks. The action being audited must not be able
   *  to fail because somebody else's log aggregator is down. */
  enqueue(entry: Entry): void {
    if (this.pending.length >= this.opts.capacity) {
      this.pending.shift()
      this.droppedCount += 1
    }
    this.pending.push(entry)
  }

  get depth(): number {
    return this.pending.length
  }

  get dropped(): number {
    return this.droppedCount
  }

  /** What the dashboard shows. A sink that is quietly failing is a sink
   *  somebody believes is working. */
  get state(): SinkState {
    return this.status
  }

  /**
   * Delivers what is waiting, one batch at a time.
   *
   * Returns how many entries were delivered. A failure leaves the batch at the
   * front of the queue and reports the state; it does not throw, because the
   * caller is a timer and there is nothing useful for it to do with an error.
   */
  async flush(): Promise<number> {
    let delivered = 0
    // Whether anything was given up on during this flush. Without it, giving
    // up on a batch and then draining the rest leaves the state at idle, and a
    // sink that has just dropped entries reporting itself healthy is exactly
    // the failure the state exists to make visible.
    let gaveUp: string | null = null
    while (this.pending.length > 0) {
      const entries = this.pending.slice(0, this.opts.batchSize)
      this.status = { state: 'delivering', pending: this.pending.length }

      try {
        await this.opts.sink.deliver({
          entries,
          manifest: sign(entries, this.opts.key),
        })
      } catch (err) {
        const reason = err instanceof Error ? err.message : String(err)
        if (err instanceof PermanentError) {
          // Dropped rather than retried forever. One entry the endpoint will
          // never accept would otherwise stop every entry behind it, which
          // turns a misconfiguration into total loss of forwarding.
          this.pending = this.pending.slice(entries.length)
          this.droppedCount += entries.length
          this.attempt = 0
          gaveUp = reason
          this.status = { state: 'failed', pending: this.pending.length, reason }
          continue
        }
        this.attempt += 1
        if (this.attempt >= this.opts.maxAttempts) {
          this.pending = this.pending.slice(entries.length)
          this.droppedCount += entries.length
          this.attempt = 0
          gaveUp = `gave up after ${this.opts.maxAttempts} attempts: ${reason}`
          this.status = { state: 'failed', pending: this.pending.length, reason: gaveUp }
          continue
        }
        this.status = { state: 'retrying', pending: this.pending.length, attempt: this.attempt, reason }
        return delivered
      }

      this.pending = this.pending.slice(entries.length)
      delivered += entries.length
      this.attempt = 0
    }
    // Only idle when nothing was lost. A dropped batch stays visible until a
    // later flush succeeds without giving up on anything.
    this.status = gaveUp === null
      ? { state: 'idle' }
      : { state: 'failed', pending: 0, reason: gaveUp }
    return delivered
  }

  /** How long to wait before the next attempt, given the attempts so far.
   *  Exponential with a ceiling: a sink that has been down for an hour should
   *  be tried every few minutes, not every few milliseconds and not once a day. */
  backoffMs(): number {
    if (this.attempt === 0) return 0
    return Math.min(2 ** this.attempt * 1000, 5 * 60 * 1000)
  }
}

/**
 * Signs a batch.
 *
 * The manifest travels with the batch so that a file sitting in an object store
 * can be checked without reaching back to the control plane that wrote it,
 * which is the situation an auditor is usually in.
 */
export function sign(entries: Entry[], key: string): Manifest {
  const body = canonical(entries)
  const digest = createHash('sha256').update(body).digest('hex')
  return {
    org: entries[0]?.orgId ?? '',
    count: entries.length,
    firstSeq: entries[0]?.seq ?? 0,
    lastSeq: entries[entries.length - 1]?.seq ?? 0,
    headHash: entries[entries.length - 1]?.entryHash ?? '',
    digest,
    signature: createHmac('sha256', key).update(digest).digest('hex'),
  }
}

/** Checks a batch against its manifest. Ships so that anybody can run it: a
 *  tamper-evidence scheme only the vendor can check is not evidence. */
export function verify(batch: Batch, key: string): { ok: boolean; problem: string } {
  const expected = sign(batch.entries, key)
  if (expected.digest !== batch.manifest.digest) {
    return { ok: false, problem: 'the entries do not match the manifest digest' }
  }
  if (expected.signature !== batch.manifest.signature) {
    return { ok: false, problem: 'the signature does not verify under this key' }
  }
  for (let i = 1; i < batch.entries.length; i += 1) {
    if (batch.entries[i]!.seq <= batch.entries[i - 1]!.seq) {
      return { ok: false, problem: `entries ${i - 1} and ${i} are out of order` }
    }
  }
  return { ok: true, problem: '' }
}

/** Field order fixed and lengths prefixed, so that two encoders cannot produce
 *  different digests for the same batch and no field can be shifted into
 *  another. */
function canonical(entries: Entry[]): string {
  return entries
    .map((e) =>
      [
        String(e.seq), e.orgId, e.actor, e.action, e.targetType,
        e.targetId ?? '', e.origin, JSON.stringify(sortKeys(e.detail)),
        e.occurredAt, e.entryHash,
      ]
        .map((part) => `${Buffer.byteLength(part, 'utf8')}:${part}`)
        .join(''),
    )
    .join('\n')
}

function sortKeys(value: Record<string, unknown>): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const key of Object.keys(value).sort()) out[key] = value[key]
  return out
}
