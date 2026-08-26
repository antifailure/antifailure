// Audit forwarding, tested against the failures a log aggregator actually has.

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import {
  Queue, PermanentError, sign, verify,
  type Batch, type Entry, type QueueOptions, type Sink,
} from '../src/index.ts'

const clock = { now: () => new Date('2026-01-01T00:00:00Z') }
const KEY = 'a-signing-key'

function entry(seq: number, over: Partial<Entry> = {}): Entry {
  return {
    seq, orgId: 'org-1', actor: 'ada', action: 'environment.created',
    targetType: 'environment', targetId: `af-${seq}`, origin: 'web',
    detail: { branch: 'main' }, occurredAt: '2026-01-01T00:00:00Z',
    entryHash: `hash-${seq}`, ...over,
  }
}

class Recorder implements Sink {
  batches: Batch[] = []
  failures = 0
  permanent = false
  name(): string { return 'recorder' }
  async deliver(batch: Batch): Promise<void> {
    if (this.failures > 0) {
      this.failures -= 1
      throw this.permanent ? new PermanentError('the bucket does not exist') : new Error('timed out')
    }
    this.batches.push(batch)
  }
}

function queue(sink: Sink, over: Partial<QueueOptions> = {}): Queue {
  return new Queue({ sink, clock, key: KEY, batchSize: 2, capacity: 5, ...over })
}

describe('forwarding', () => {
  it('delivers in order, in batches', async () => {
    // Sequence numbers exist so nobody has to sort an audit trail before
    // reading it, which means arriving in order is the point.
    const sink = new Recorder()
    const q = queue(sink)
    for (let i = 1; i <= 5; i += 1) q.enqueue(entry(i))

    assert.equal(await q.flush(), 5)
    assert.deepEqual(sink.batches.map((b) => b.entries.map((e) => e.seq)), [[1, 2], [3, 4], [5]])
    assert.deepEqual(q.state, { state: 'idle' })
  })

  it('enqueueing never throws, whatever the sink is doing', async () => {
    // The action being audited must not be able to fail because somebody
    // else's log aggregator is down.
    const sink = new Recorder()
    sink.failures = Number.MAX_SAFE_INTEGER
    const q = queue(sink)
    for (let i = 1; i <= 100; i += 1) q.enqueue(entry(i))
    assert.equal(q.depth, 5)
  })

  it('drops the oldest when full and counts it', async () => {
    // The primary log still holds every entry, and a forwarder that grows
    // without bound takes the process down with it.
    const sink = new Recorder()
    const q = queue(sink, { capacity: 3, batchSize: 10 })
    for (let i = 1; i <= 6; i += 1) q.enqueue(entry(i))

    assert.equal(q.dropped, 3)
    await q.flush()
    assert.deepEqual(sink.batches[0]!.entries.map((e) => e.seq), [4, 5, 6])
  })

  it('keeps a batch that failed and retries it', async () => {
    const sink = new Recorder()
    sink.failures = 1
    const q = queue(sink, { batchSize: 10 })
    for (let i = 1; i <= 3; i += 1) q.enqueue(entry(i))

    assert.equal(await q.flush(), 0)
    assert.equal(q.depth, 3, 'a failed delivery discarded the entries')
    assert.equal(q.state.state, 'retrying')
    assert.match((q.state as { reason: string }).reason, /timed out/)

    assert.equal(await q.flush(), 3)
    assert.equal(q.state.state, 'idle')
  })

  it('backs off, and does not back off forever', async () => {
    // A sink down for an hour should be tried every few minutes, not every few
    // milliseconds and not once a day.
    const sink = new Recorder()
    const q = queue(sink, { maxAttempts: 100 })
    q.enqueue(entry(1))
    assert.equal(q.backoffMs(), 0)

    let previous = 0
    for (let i = 0; i < 20; i += 1) {
      sink.failures = 1
      await q.flush()
      const wait = q.backoffMs()
      assert.ok(wait >= previous, 'the backoff went down')
      assert.ok(wait <= 5 * 60 * 1000, `the backoff reached ${wait}ms`)
      previous = wait
    }
    assert.equal(previous, 5 * 60 * 1000)
  })

  it('gives up on a batch it will never deliver, and keeps going', async () => {
    // One entry the endpoint will never accept would otherwise stop every entry
    // behind it, turning a misconfiguration into total loss of forwarding.
    const sink = new Recorder()
    sink.permanent = true
    sink.failures = 1
    const q = queue(sink, { batchSize: 1 })
    q.enqueue(entry(1))
    q.enqueue(entry(2))

    const delivered = await q.flush()
    assert.equal(delivered, 1, 'the entry behind the rejected one was not delivered')
    assert.equal(q.dropped, 1)
    assert.deepEqual(sink.batches[0]!.entries.map((e) => e.seq), [2])
  })

  it('gives up after the attempt limit rather than retrying forever', async () => {
    const sink = new Recorder()
    sink.failures = Number.MAX_SAFE_INTEGER
    const q = queue(sink, { maxAttempts: 3, batchSize: 1 })
    q.enqueue(entry(1))

    for (let i = 0; i < 3; i += 1) await q.flush()
    assert.equal(q.state.state, 'failed')
    assert.match((q.state as { reason: string }).reason, /gave up after 3 attempts/)
    assert.equal(q.dropped, 1)
  })

  it('reports its state, because a sink that is quietly failing looks like one that works', async () => {
    const sink = new Recorder()
    const q = queue(sink)
    assert.deepEqual(q.state, { state: 'idle' })

    sink.failures = 1
    q.enqueue(entry(1))
    await q.flush()
    assert.equal(q.state.state, 'retrying')
    assert.equal((q.state as { pending: number }).pending, 1)
  })
})

describe('batch manifests', () => {
  it('verify under the right key', () => {
    const entries = [entry(1), entry(2)]
    const batch: Batch = { entries, manifest: sign(entries, KEY) }
    assert.deepEqual(verify(batch, KEY), { ok: true, problem: '' })
  })

  it('carry the head hash, so a run of batches is verifiable end to end', () => {
    const entries = [entry(1), entry(2), entry(3)]
    const manifest = sign(entries, KEY)
    assert.equal(manifest.headHash, 'hash-3')
    assert.equal(manifest.firstSeq, 1)
    assert.equal(manifest.lastSeq, 3)
    assert.equal(manifest.count, 3)
  })

  it('do not verify under another key', () => {
    const entries = [entry(1)]
    const batch: Batch = { entries, manifest: sign(entries, KEY) }
    assert.equal(verify(batch, 'a-different-key').ok, false)
  })

  it('detect an altered entry', () => {
    const entries = [entry(1), entry(2)]
    const batch: Batch = { entries, manifest: sign(entries, KEY) }
    batch.entries[0]!.action = 'nothing.happened'
    const result = verify(batch, KEY)
    assert.equal(result.ok, false)
    assert.match(result.problem, /do not match/)
  })

  it('detect a removed entry', () => {
    const entries = [entry(1), entry(2), entry(3)]
    const batch: Batch = { entries: [...entries], manifest: sign(entries, KEY) }
    batch.entries.splice(1, 1)
    assert.equal(verify(batch, KEY).ok, false)
  })

  it('detect entries out of order', () => {
    const entries = [entry(1), entry(2)]
    const reordered = [entries[1]!, entries[0]!]
    const batch: Batch = { entries: reordered, manifest: sign(reordered, KEY) }
    const result = verify(batch, KEY)
    assert.equal(result.ok, false)
    assert.match(result.problem, /out of order/)
  })

  it('cannot be collided by moving a character between fields', () => {
    // Length prefixes. Without them an actor named "ab" doing ".c" hashes the
    // same as one named "a" doing "b.c", and whoever chooses one field chooses
    // the digest.
    const a = sign([entry(1, { actor: 'ab', action: '.c' })], KEY)
    const b = sign([entry(1, { actor: 'a', action: 'b.c' })], KEY)
    assert.notEqual(a.digest, b.digest)
  })

  it('do not depend on the order keys were written in', () => {
    const a = sign([entry(1, { detail: { b: 1, a: 2 } })], KEY)
    const b = sign([entry(1, { detail: { a: 2, b: 1 } })], KEY)
    assert.equal(a.digest, b.digest)
  })

  it('an empty batch signs without throwing', () => {
    const manifest = sign([], KEY)
    assert.equal(manifest.count, 0)
    assert.equal(verify({ entries: [], manifest }, KEY).ok, true)
  })
})
