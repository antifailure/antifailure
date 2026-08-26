// The concrete sinks, tested for the two things that are easy to get wrong:
// what goes on the wire, and which failures stop the retries.

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import {
  SplunkSink, EventHubsSink, ObjectStoreSink, WebhookSink, verifyWebhook,
  PermanentError, sign, type Batch, type Entry, type Fetcher,
} from '../src/index.ts'

function entry(seq: number): Entry {
  return {
    seq, orgId: 'acme', actor: 'ada', action: 'environment.created',
    targetType: 'environment', targetId: `af-${seq}`, origin: 'web',
    detail: { branch: 'main' }, occurredAt: '2026-01-01T00:00:00.000Z',
    entryHash: `hash-${seq}`,
  }
}

function batch(n = 2): Batch {
  const entries = Array.from({ length: n }, (_, i) => entry(i + 1))
  return { entries, manifest: sign(entries, 'k') }
}

interface Capture {
  url: string
  init: RequestInit
}

function capturing(status = 200, body = ''): { fetch: Fetcher; calls: Capture[] } {
  const calls: Capture[] = []
  return {
    calls,
    fetch: async (url, init) => {
      calls.push({ url, init })
      return new Response(body, { status })
    },
  }
}

describe('splunk', () => {
  it('sends newline-delimited events, not an array', async () => {
    // HEC takes objects one per line. An array is accepted and indexed as a
    // single event, which looks like it worked.
    const { fetch, calls } = capturing()
    await new SplunkSink({ url: 'https://splunk.test/services/collector', token: 't', fetch })
      .deliver(batch(2))

    const lines = String(calls[0]!.init.body).split('\n')
    assert.equal(lines.length, 2)
    const first = JSON.parse(lines[0]!)
    assert.equal(first.event.seq, 1)
    // Seconds. Milliseconds are accepted and silently read as a date in the
    // year 56000.
    assert.equal(first.time, Date.parse('2026-01-01T00:00:00.000Z') / 1000)
  })

  it('honours the index and sourcetype, so entries land where searches look', async () => {
    const { fetch, calls } = capturing()
    await new SplunkSink({
      url: 'https://splunk.test/services/collector', token: 't', fetch,
      index: 'security', sourcetype: 'acme:audit',
    }).deliver(batch(1))

    const event = JSON.parse(String(calls[0]!.init.body))
    assert.equal(event.index, 'security')
    assert.equal(event.sourcetype, 'acme:audit')
  })

  it('carries the manifest so a batch can still be checked', async () => {
    const { fetch, calls } = capturing()
    const b = batch(2)
    await new SplunkSink({ url: 'https://splunk.test/x', token: 't', fetch }).deliver(b)

    const headers = calls[0]!.init.headers as Record<string, string>
    assert.deepEqual(JSON.parse(headers['x-antifailure-manifest']!), b.manifest)
  })
})

describe('event hubs', () => {
  it('sends a JSON array of records', async () => {
    const { fetch, calls } = capturing(201)
    await new EventHubsSink({
      url: 'https://ns.servicebus.windows.net/hub/messages',
      authorization: 'SharedAccessSignature sr=...',
      fetch,
    }).deliver(batch(2))

    const records = JSON.parse(String(calls[0]!.init.body))
    assert.equal(records.length, 2)
    assert.equal(records[0].Body.seq, 1)
    const headers = calls[0]!.init.headers as Record<string, string>
    assert.match(headers['content-type']!, /servicebus/)
  })
})

describe('failures', () => {
  it('stops retrying on a credential or a request that will never work', async () => {
    // One batch an endpoint will never accept would otherwise block every entry
    // behind it forever.
    for (const status of [400, 401, 403, 404, 413]) {
      const { fetch } = capturing(status, 'no')
      await assert.rejects(
        new SplunkSink({ url: 'https://splunk.test/x', token: 't', fetch }).deliver(batch(1)),
        PermanentError,
        `${status} should stop the retries`,
      )
    }
  })

  it('keeps retrying on throttling and server errors', async () => {
    // 429 and every 5xx are temporary, and the queue's backoff handles them.
    for (const status of [429, 500, 502, 503, 504]) {
      const { fetch } = capturing(status)
      const err = await new SplunkSink({ url: 'https://splunk.test/x', token: 't', fetch })
        .deliver(batch(1))
        .then(() => null, (e: unknown) => e)
      assert.ok(err instanceof Error, `${status} should fail`)
      assert.ok(!(err instanceof PermanentError), `${status} should be retried, not given up on`)
    }
  })

  it('treats a transport failure as temporary', async () => {
    // A hostname that does not resolve looks the same as one briefly
    // unreachable, and guessing permanent would discard entries over a blip.
    const fetch: Fetcher = async () => {
      throw new Error('getaddrinfo ENOTFOUND')
    }
    const err = await new SplunkSink({ url: 'https://splunk.test/x', token: 't', fetch })
      .deliver(batch(1))
      .then(() => null, (e: unknown) => e)
    assert.ok(err instanceof Error)
    assert.ok(!(err instanceof PermanentError))
  })

  it('does not quote an unbounded body in an error', async () => {
    const { fetch } = capturing(500, 'x'.repeat(100_000))
    const err = await new SplunkSink({ url: 'https://splunk.test/x', token: 't', fetch })
      .deliver(batch(1))
      .then(() => null, (e: unknown) => e)
    assert.ok((err as Error).message.length < 700)
  })
})

describe('object store', () => {
  it('writes the batch and its manifest as separate objects', async () => {
    // The batch file stays exactly the newline-delimited JSON every log tool
    // reads, and the signature is still there for anybody who wants it.
    const written: { key: string; body: string; type: string }[] = []
    await new ObjectStoreSink({
      prefix: 'audit/',
      put: async (key, body, type) => {
        written.push({ key, body, type })
      },
    }).deliver(batch(2))

    assert.equal(written.length, 2)
    assert.match(written[0]!.key, /\.ndjson$/)
    assert.equal(written[0]!.type, 'application/x-ndjson')
    assert.equal(written[0]!.body.split('\n').length, 2)
    assert.match(written[1]!.key, /\.manifest\.json$/)
  })

  it('keys objects so a listing sorts chronologically', async () => {
    // Zero padded sequence numbers. A timestamp sorts too and collides when two
    // batches land in the same millisecond.
    const keys: string[] = []
    const sink = new ObjectStoreSink({ put: async (key) => { keys.push(key) } })

    await sink.deliver({ entries: [entry(9)], manifest: sign([entry(9)], 'k') })
    await sink.deliver({ entries: [entry(100)], manifest: sign([entry(100)], 'k') })

    const ndjson = keys.filter((k) => k.endsWith('.ndjson'))
    assert.deepEqual([...ndjson].sort(), ndjson, 'keys do not sort in sequence order')
    assert.match(ndjson[0]!, /acme\/000000000009-000000000009/)
  })

  it('writes nothing for an empty batch', async () => {
    let calls = 0
    await new ObjectStoreSink({ put: async () => { calls += 1 } })
      .deliver({ entries: [], manifest: sign([], 'k') })
    assert.equal(calls, 0)
  })
})

describe('webhook', () => {
  it('signs the body with a timestamp outside it', async () => {
    // A signature over the body alone is replayable forever, and a receiver
    // cannot reject an old delivery without parsing it first.
    const { fetch, calls } = capturing()
    await new WebhookSink({ url: 'https://acme.test/hook', secret: 's', fetch }).deliver(batch(1))

    const headers = calls[0]!.init.headers as Record<string, string>
    const timestamp = headers['x-antifailure-timestamp']!
    const signature = headers['x-antifailure-signature']!
    assert.ok(timestamp)
    assert.match(signature, /^sha256=/)
    assert.equal(verifyWebhook('s', timestamp, String(calls[0]!.init.body), signature), true)
  })

  it('does not verify under another secret, or with the body changed', async () => {
    const { fetch, calls } = capturing()
    await new WebhookSink({ url: 'https://acme.test/hook', secret: 's', fetch }).deliver(batch(1))
    const headers = calls[0]!.init.headers as Record<string, string>
    const body = String(calls[0]!.init.body)

    assert.equal(verifyWebhook('other', headers['x-antifailure-timestamp']!, body,
      headers['x-antifailure-signature']!), false)
    assert.equal(verifyWebhook('s', headers['x-antifailure-timestamp']!, body + ' ',
      headers['x-antifailure-signature']!), false)
    assert.equal(verifyWebhook('s', headers['x-antifailure-timestamp']!, body, 'sha256=short'), false)
  })

  it('signs a redelivery of the same batch identically', async () => {
    // So a receiver deduplicating on the signature sees one delivery rather
    // than two.
    const { fetch, calls } = capturing()
    const sink = new WebhookSink({ url: 'https://acme.test/hook', secret: 's', fetch })
    const b = batch(2)
    await sink.deliver(b)
    await sink.deliver(b)

    const first = (calls[0]!.init.headers as Record<string, string>)['x-antifailure-signature']
    const second = (calls[1]!.init.headers as Record<string, string>)['x-antifailure-signature']
    assert.equal(first, second)
  })
})
