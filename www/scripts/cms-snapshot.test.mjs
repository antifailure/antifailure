import assert from 'node:assert/strict'
import { afterEach, describe, it } from 'node:test'
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { createServer } from 'node:http'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { emptyWebsiteDocument } from '@antifailure/website'
import {
  checkCurrentSnapshot, defaultSnapshot, documentHash, fetchPublishedSnapshot,
  isSnapshotCurrent, MAX_SNAPSHOT_BYTES, parseSnapshot, prepareSnapshot, verifyLiveMarker,
} from './cms-snapshot.mjs'

const temporary = []
afterEach(async () => { await Promise.all(temporary.splice(0).map((path) => rm(path, { recursive: true, force: true }))) })
async function files() {
  const root = await mkdtemp(join(tmpdir(), 'af-cms-snapshot-'))
  temporary.push(root)
  return { snapshotPath: join(root, 'generated.json'), markerPath: join(root, 'marker.json') }
}
function snapshot(revision = 4, title = 'Test the change before you ship.') {
  const document = emptyWebsiteDocument()
  document.fields['hero.title'] = title
  return { revision, contentHash: documentHash(document), document }
}
function json(value, status = 200, headers = {}) {
  return new Response(JSON.stringify(value), { status, headers: { 'content-type': 'application/json', ...headers } })
}

describe('CMS static snapshot validation', () => {
  it('uses the same stable hash irrespective of JSON object key order', () => {
    const value = snapshot()
    value.document.fields['hero.subtitle'] = 'Give your agent a production twin.'
    value.contentHash = documentHash(value.document)
    const reordered = { ...value, document: { ...value.document, fields: {
      'hero.subtitle': value.document.fields['hero.subtitle'], 'hero.title': value.document.fields['hero.title'],
    } } }
    assert.equal(parseSnapshot(reordered).contentHash, value.contentHash)
  })

  it('rejects a malformed individual override instead of changing a signed published document', () => {
    const value = snapshot()
    value.document.fields['hero.subtitle'] = { unexpected: 'object' }
    value.contentHash = documentHash(value.document)
    assert.throws(() => parseSnapshot(value), /document is invalid/)
  })

  it('rejects a correct document with a different content hash', () => {
    const value = snapshot()
    value.document.fields['hero.title'] = 'Changed after hashing'
    assert.throws(() => parseSnapshot(value), /content hash/)
  })

  it('rejects invalid revision and hash shapes, including numeric strings', () => {
    for (const revision of ['4', -1, 1.5, Number.MAX_SAFE_INTEGER + 1, null]) {
      assert.throws(() => parseSnapshot({ ...snapshot(), revision }), /marker is invalid/)
    }
    for (const contentHash of ['', 'a'.repeat(63), 'A'.repeat(64), null]) {
      assert.throws(() => parseSnapshot({ ...snapshot(), contentHash }), /marker is invalid/)
    }
  })

  it('gives revision zero a real content hash', () => {
    const value = defaultSnapshot()
    assert.equal(value.revision, 0)
    assert.match(value.contentHash, /^[a-f0-9]{64}$/)
    assert.deepEqual(parseSnapshot(value), value)
  })
})

describe('fetching the public snapshot', () => {
  it('fetches only public JSON with no credentials and refuses redirects', async () => {
    let called = false
    const expected = snapshot()
    const actual = await fetchPublishedSnapshot('https://app.antifailure.dev/v1/website/published', {
      fetchImpl: async (url, options) => {
        called = true
        assert.equal(url.href, 'https://app.antifailure.dev/v1/website/published')
        assert.equal(options.redirect, 'error')
        assert.equal(options.cache, 'no-store')
        assert.equal(options.headers.Authorization, undefined)
        assert.equal(options.headers.Cookie, undefined)
        assert.ok(options.signal instanceof AbortSignal)
        return json(expected)
      },
    })
    assert.equal(called, true)
    assert.deepEqual(actual, expected)
  })

  it('does not substitute defaults after network failure or error statuses', async () => {
    await assert.rejects(fetchPublishedSnapshot('https://example.test/published', {
      fetchImpl: async () => { throw new Error('network unavailable') },
    }), /network unavailable/)
    for (const status of [401, 404, 429, 500, 503]) {
      await assert.rejects(fetchPublishedSnapshot('https://example.test/published', {
        fetchImpl: async () => json({}, status),
      }), new RegExp(`HTTP ${status}`))
    }
  })

  it('requires explicit bootstrap plus proof that no CMS marker has shipped', async () => {
    const calls = []
    const result = await fetchPublishedSnapshot('https://example.test/published', {
      allowBootstrap404: true, liveMarkerUrl: 'https://site.test/cms-version.json',
      fetchImpl: async (url) => { calls.push(url.href); return json({}, 404) },
    })
    assert.deepEqual(result, defaultSnapshot())
    assert.deepEqual(calls, ['https://example.test/published', 'https://site.test/cms-version.json'])
    await assert.rejects(fetchPublishedSnapshot('https://example.test/published', {
      allowBootstrap404: true, fetchImpl: async () => json({}, 404),
    }), /requires the live site marker/)
  })

  it('cannot bootstrap over an existing revision-zero marker or an unknown marker state', async () => {
    for (const status of [200, 403, 500, 503]) {
      await assert.rejects(fetchPublishedSnapshot('https://example.test/published', {
        allowBootstrap404: true, liveMarkerUrl: 'https://site.test/cms-version.json',
        fetchImpl: async (url) => url.hostname === 'example.test' ? json({}, 404) : json(defaultSnapshot(), status),
      }), /bootstrap is refused/)
    }
    await assert.rejects(fetchPublishedSnapshot('https://example.test/published', {
      allowBootstrap404: true, liveMarkerUrl: 'https://site.test/cms-version.json',
      fetchImpl: async (url) => { if (url.hostname === 'example.test') return json({}, 404); throw new Error('unreachable marker') },
    }), /unreachable marker/)
  })

  it('does not use bootstrap for a server failure', async () => {
    let calls = 0
    await assert.rejects(fetchPublishedSnapshot('https://example.test/published', {
      allowBootstrap404: true, liveMarkerUrl: 'https://site.test/cms-version.json',
      fetchImpl: async () => { calls++; return json({}, 503) },
    }), /HTTP 503/)
    assert.equal(calls, 1)
  })

  it('rejects non-JSON, invalid JSON, and dishonest declared lengths', async () => {
    for (const response of [
      new Response('<html>Maintenance</html>', { headers: { 'content-type': 'text/html' } }),
      new Response('{bad json}', { headers: { 'content-type': 'application/json' } }),
      json(snapshot(), 200, { 'content-length': String(MAX_SNAPSHOT_BYTES + 1) }),
      json(snapshot(), 200, { 'content-length': 'unknown' }),
    ]) {
      await assert.rejects(fetchPublishedSnapshot('https://example.test/published', { fetchImpl: async () => response }))
    }
  })

  it('limits the streamed body even when Content-Length says one byte', async () => {
    let cancelled = false
    const body = new ReadableStream({
      start(controller) { controller.enqueue(new Uint8Array(40)); controller.enqueue(new Uint8Array(40)) },
      cancel() { cancelled = true },
    })
    await assert.rejects(fetchPublishedSnapshot('https://example.test/published', {
      maxBytes: 64,
      fetchImpl: async () => new Response(body, { headers: { 'content-type': 'application/json', 'content-length': '1' } }),
    }), /exceeds the allowed size/)
    assert.equal(cancelled, true)
  })

  it('actually aborts a live HTTP connection whose body never finishes', async () => {
    const server = createServer((_request, response) => {
      response.writeHead(200, { 'content-type': 'application/json' })
      response.write('{')
    })
    await new Promise((done) => server.listen(0, '127.0.0.1', done))
    try {
      await assert.rejects(fetchPublishedSnapshot(`http://127.0.0.1:${server.address().port}/published`, { timeoutMs: 30 }), /timed out|abort/i)
    } finally {
      server.closeAllConnections()
      await new Promise((done) => server.close(done))
    }
  })

  it('refuses a URL with credentials before making any request', async () => {
    let calls = 0
    await assert.rejects(fetchPublishedSnapshot('https://user:password@example.test/published', {
      fetchImpl: async () => { calls++; return json(snapshot()) },
    }), /without credentials/)
    assert.equal(calls, 0)
  })
})

describe('build output and deployment ordering', () => {
  it('writes the validated snapshot and matching public marker, then resets an ordinary build to source defaults', async () => {
    const paths = await files()
    const expected = snapshot()
    await prepareSnapshot({ ...paths, publishedUrl: 'https://example.test/published', fetchImpl: async () => json(expected) })
    assert.deepEqual(JSON.parse(await readFile(paths.snapshotPath, 'utf8')), expected)
    assert.deepEqual(JSON.parse(await readFile(paths.markerPath, 'utf8')), { revision: expected.revision, contentHash: expected.contentHash })
    await prepareSnapshot(paths)
    assert.deepEqual(JSON.parse(await readFile(paths.snapshotPath, 'utf8')), defaultSnapshot())
  })

  it('keeps the last generated output intact when a production fetch fails, and fails the build', async () => {
    const paths = await files()
    await prepareSnapshot(paths)
    const before = await readFile(paths.snapshotPath, 'utf8')
    await assert.rejects(prepareSnapshot({ ...paths, publishedUrl: 'https://example.test/published', fetchImpl: async () => json({}, 503) }), /HTTP 503/)
    assert.equal(await readFile(paths.snapshotPath, 'utf8'), before)
  })

  it('supports an explicit local fixture and refuses an ambiguous source', async () => {
    const paths = await files()
    const fixturePath = join(dirname(paths.snapshotPath), 'fixture.json')
    const expected = snapshot()
    await writeFile(fixturePath, JSON.stringify(expected))
    assert.deepEqual(await prepareSnapshot({ ...paths, fixturePath }), expected)
    await assert.rejects(prepareSnapshot({ ...paths, fixturePath, publishedUrl: 'https://example.test/published' }), /not both/)
  })

  it('compares both revision and hash, refuses rollback, and identifies a newer publication', () => {
    assert.equal(isSnapshotCurrent(snapshot(4), snapshot(4)), true)
    assert.equal(isSnapshotCurrent(snapshot(4), snapshot(5)), false)
    assert.throws(() => isSnapshotCurrent(snapshot(5), snapshot(4)), /moved backwards/)
    assert.throws(() => isSnapshotCurrent(snapshot(4), snapshot(4, 'Different content')), /two different content hashes/)
  })

  it('rechecks the endpoint instead of trusting a dispatch revision hint or the previous fetch', async () => {
    const paths = await files()
    let live = snapshot(4)
    const options = { ...paths, publishedUrl: 'https://example.test/published', fetchImpl: async () => json(live) }
    await prepareSnapshot(options)
    assert.equal((await checkCurrentSnapshot(options)).current, true)
    live = snapshot(6, 'A newer published edit')
    const result = await checkCurrentSnapshot(options)
    assert.equal(result.current, false)
    assert.equal(result.built.revision, 4)
    assert.equal(result.latest.revision, 6)
    await prepareSnapshot(options)
    assert.equal((await checkCurrentSnapshot(options)).current, true)
    assert.equal(JSON.parse(await readFile(paths.snapshotPath, 'utf8')).revision, 6)
  })

  it('waits for exact live bytes, tolerating propagation and refusing success on a stale 200', async () => {
    const paths = await files()
    await prepareSnapshot({ ...paths, publishedUrl: 'https://example.test/published', fetchImpl: async () => json(snapshot()) })
    let calls = 0
    const delays = []
    const result = await verifyLiveMarker({ ...paths, liveMarkerUrl: 'https://site.test/cms-version.json', attempts: 3, delayMs: 1,
      wait: async (delay) => { delays.push(delay) },
      fetchImpl: async (url) => {
        assert.ok(url.searchParams.get('cms_verify'))
        calls++
        return calls === 1 ? json({}, 404) : calls === 2 ? json(snapshot(3)) : json(snapshot())
      },
    })
    assert.equal(result.revision, 4)
    assert.equal(calls, 3)
    assert.deepEqual(delays, [1, 1])
    await assert.rejects(verifyLiveMarker({ ...paths, liveMarkerUrl: 'https://site.test/cms-version.json', attempts: 2,
      wait: async () => {}, fetchImpl: async () => json(snapshot(4, 'Different content')),
    }), /exact content hash/)
  })
})
