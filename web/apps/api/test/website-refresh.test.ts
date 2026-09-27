import { after, before, beforeEach, describe, test } from 'node:test'
import assert from 'node:assert/strict'
import { generateKeyPairSync, randomUUID } from 'node:crypto'
import { createServer } from 'node:http'
import { once } from 'node:events'
import { RealGitHubClient } from '../src/auth/github.ts'
import { InstallationTokens } from '../src/github/app.ts'
import { FakeClock } from '../src/clock.ts'
import {
  readWebsiteRevisionMarker, refreshWebsiteOnce, startWebsiteRefreshWorker,
  type WebsiteRefreshDeps,
} from '../src/website-refresh.ts'
import { available, startApi, type ApiHarness } from './harness.ts'

const hash = (revision: number) => revision.toString(16).padStart(64, '0')
const response = (revision = 0, contentHash = hash(revision)) =>
  new Response(JSON.stringify({ revision, contentHash }), { headers: { 'content-type': 'application/json' } })

function gate() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => { resolve = done })
  return { promise, resolve }
}

describe('the actual static revision marker', () => {
  test('uses only the fixed HTTPS marker, bypasses caches, and forbids redirects', async () => {
    const result = await readWebsiteRevisionMarker(async (input, options) => {
      const url = new URL(String(input))
      assert.equal(url.origin, 'https://antifailure.dev')
      assert.equal(url.pathname, '/cms-version.json')
      assert.ok(url.searchParams.get('check'))
      assert.equal(options?.cache, 'no-store')
      assert.equal(options?.redirect, 'error')
      assert.ok(options?.signal)
      return response(9)
    })
    assert.deepEqual(result, { revision: 9, contentHash: hash(9) })
  })

  test('accepts a canonical string revision from a JSON build script', async () => {
    const marker = { revision: '12', contentHash: hash(12) }
    assert.deepEqual(await readWebsiteRevisionMarker(async () => new Response(JSON.stringify(marker), {
      headers: { 'content-type': 'application/json; charset=utf-8' },
    })), { revision: 12, contentHash: hash(12) })
  })

  for (const revision of [null, -1, 1.5, '01', '1e2', '1.0', '9007199254740992', {}, []]) {
    test(`refuses malformed revision ${JSON.stringify(revision)}`, async () => {
      await assert.rejects(readWebsiteRevisionMarker(async () => new Response(
        JSON.stringify({ revision, contentHash: hash(1) }), { headers: { 'content-type': 'application/json' } },
      )), /invalid revision/)
    })
  }

  test('does not accept a website page or a successful status without a bounded valid marker', async () => {
    for (const invalid of [
      new Response('<html>Sign in</html>', { headers: { 'content-type': 'text/html' } }),
      new Response('{}', { status: 503, headers: { 'content-type': 'application/json' } }),
      new Response('x'.repeat(4097), { headers: { 'content-type': 'application/json' } }),
      new Response('{}', { headers: { 'content-type': 'application/json', 'content-length': '100000000' } }),
      new Response('{', { headers: { 'content-type': 'application/json' } }),
      new Response('null', { headers: { 'content-type': 'application/json' } }),
      new Response(JSON.stringify({ revision: 1, contentHash: 'unknown' }), { headers: { 'content-type': 'application/json' } }),
    ]) await assert.rejects(readWebsiteRevisionMarker(async () => invalid))
  })
})

test('aborting token mint closes the actual request and prevents a late workflow dispatch', { timeout: 5000 }, async () => {
  const entered = gate()
  const closed = gate()
  let dispatchRequests = 0
  const server = createServer((request, reply) => {
    if (request.url?.endsWith('/dispatches')) dispatchRequests += 1
    reply.on('close', closed.resolve)
    entered.resolve()
    // Hold the token request open. Cancellation must end this socket and must
    // not merely stop awaiting a token which could dispatch later.
  })
  server.listen(0, '127.0.0.1')
  await once(server, 'listening')
  const address = server.address()
  assert.ok(address && typeof address !== 'string')
  const apiBase = `http://127.0.0.1:${address.port}`
  const { privateKey } = generateKeyPairSync('rsa', { modulusLength: 2048 })
  const tokens = new InstallationTokens({
    appId: '123', webhookSecret: 'test', apiBase,
    privateKey: privateKey.export({ type: 'pkcs8', format: 'pem' }).toString(),
  }, new FakeClock())
  const github = new RealGitHubClient({
    clientId: 'test', clientSecret: 'test', redirectUri: 'http://localhost/callback',
    apiBase, installationTokens: tokens,
  })
  const controller = new AbortController()
  const pending = github.dispatchWorkflow(123, 'antifailure/antifailure', 'deploy.yml', 'main', {}, controller.signal)
  try {
    await entered.promise
    controller.abort()
    await assert.rejects(pending, { name: 'AbortError' })
    await closed.promise
    assert.equal(dispatchRequests, 0)
  } finally {
    server.closeAllConnections()
    await new Promise<void>((resolve) => server.close(() => resolve()))
  }
})

const hasDb = await available()
describe('static refresh persistence and event orderings', {
  skip: hasDb ? false : 'no Postgres at AF_TEST_DATABASE_URL', concurrency: 1,
}, () => {
  let h: ApiHarness
  let clock: FakeClock
  let deps: WebsiteRefreshDeps
  let dispatched: Array<{ installationId: number; repository: string; workflow: string; ref: string; inputs: Record<string, string> }>
  let dispatch: () => Promise<void>
  let marker: () => Promise<Response>
  const document = { schemaVersion: 1, fields: {}, styles: {}, sections: { hidden: [], moves: [], custom: [] }, collections: {} }

  before(async () => { h = await startApi() })
  after(async () => { await h?.close() })
  beforeEach(async () => {
    await h.admin`TRUNCATE website_refresh_jobs, website_history CASCADE`
    await h.admin`UPDATE website_published SET revision = 0, content_hash = ${hash(0)} WHERE id = 'homepage'`
    clock = new FakeClock()
    dispatched = []
    dispatch = async () => {}
    marker = async () => response()
    deps = {
      adminPool: h.adminPool, clock, productionPublishing: true,
      installationTokens: { onRepository: async (repository) => {
        assert.equal(repository, 'antifailure/antifailure')
        return { id: 123, permissions: { actions: 'write' } }
      } },
      github: { dispatchWorkflow: async (installationId, repository, workflow, ref, inputs) => {
        dispatched.push({ installationId, repository, workflow, ref, inputs })
        await dispatch()
      } },
      fetchImpl: async () => marker(),
    }
  })

  async function publish(revision: number) {
    await h.admin.begin(async (db) => {
      await db`INSERT INTO website_history(revision, draft_revision, document, content_hash, created_by)
        VALUES (${revision}, ${revision}, ${db.json(document)}, ${hash(revision)}, 'refresh-test')`
      await db`UPDATE website_published SET revision = ${revision}, document = ${db.json(document)}, content_hash = ${hash(revision)} WHERE id = 'homepage'`
      await db`INSERT INTO website_refresh_jobs(revision, next_attempt_at) VALUES (${revision}, ${clock.now()})`
    })
  }

  async function job(revision: number) {
    const rows = await h.admin`SELECT * FROM website_refresh_jobs WHERE revision = ${revision}`
    assert.ok(rows[0])
    return rows[0]
  }

  test('worker then publish: an empty sweep does not lose a later publication', async () => {
    assert.deepEqual(await refreshWebsiteOnce(deps), { state: 'idle' })
    await publish(1)
    assert.deepEqual(await refreshWebsiteOnce(deps), { state: 'dispatched', revision: 1 })
    assert.equal((await job(1)).status, 'waiting')
    assert.equal((await job(1)).deployed_at, null)
    assert.deepEqual(dispatched, [{
      installationId: 123, repository: 'antifailure/antifailure', workflow: 'deploy.yml', ref: 'main',
      inputs: { cms_revision: '1', cms_content_hash: hash(1) },
    }])
  })

  test('publish then worker: a queued publication survives startup and has one active local pass', async () => {
    await publish(1)
    const waiting = gate()
    dispatch = async () => waiting.promise
    const worker = startWebsiteRefreshWorker(deps)
    const first = worker.runOnce()
    assert.equal(worker.runOnce(), first)
    waiting.resolve()
    assert.equal((await first).state, 'dispatched')
    await worker.stop()
    assert.equal(dispatched.length, 1)
    assert.equal((await worker.runOnce()).state, 'idle')
  })

  test('marker before dispatch: an already served revision is idempotent and does not dispatch', async () => {
    await publish(1)
    marker = async () => response(1)
    assert.equal((await refreshWebsiteOnce(deps)).state, 'deployed')
    assert.equal((await job(1)).status, 'deployed')
    assert.ok((await job(1)).deployed_at)
    assert.equal(dispatched.length, 0)
    assert.equal((await refreshWebsiteOnce(deps)).state, 'idle')
  })

  test('dispatch without completion stays waiting, and revision alone does not prove completion', async () => {
    await publish(1)
    await refreshWebsiteOnce(deps)
    marker = async () => response(1, hash(2))
    clock.advance(15_000)
    assert.equal((await refreshWebsiteOnce(deps)).state, 'waiting')
    assert.equal((await job(1)).deployed_at, null)
    assert.equal(dispatched.length, 1)
    marker = async () => response(1)
    clock.advance(15_000)
    assert.equal((await refreshWebsiteOnce(deps)).state, 'deployed')
  })

  test('accepted dispatch with no eventual completion is retried after the observation window', async () => {
    await publish(1)
    await refreshWebsiteOnce(deps)
    clock.advance(30 * 60_000)
    assert.equal((await refreshWebsiteOnce(deps)).state, 'retrying')
    assert.equal(dispatched.length, 1)
    assert.equal((await job(1)).status, 'queued')
    assert.match((await job(1)).last_error, /30 minutes/)
    clock.advance(30_000)
    assert.equal((await refreshWebsiteOnce(deps)).state, 'dispatched')
    assert.equal(dispatched.length, 2)
  })

  test('transient dispatch failure backs off, retries, and cannot publish a false success', async () => {
    await publish(1)
    dispatch = async () => { throw new Error('remote credential echoed here') }
    assert.equal((await refreshWebsiteOnce(deps)).state, 'retrying')
    const failed = await job(1)
    assert.equal(failed.attempts, 1)
    assert.equal(failed.status, 'queued')
    assert.equal(failed.deployed_at, null)
    assert.doesNotMatch(failed.last_error, /credential echoed/)
    assert.equal((await refreshWebsiteOnce(deps)).state, 'idle')
    clock.advance(30_000)
    dispatch = async () => {}
    assert.equal((await refreshWebsiteOnce(deps)).state, 'dispatched')
    assert.equal((await job(1)).attempts, 2)
  })

  test('missing GitHub configuration is truthful and does not spin on a failed job', async () => {
    await publish(1)
    deps.installationTokens = null
    assert.equal((await refreshWebsiteOnce(deps)).state, 'failed')
    assert.match((await job(1)).last_error, /Configure the App/)
    clock.advance(60 * 60_000)
    assert.equal((await refreshWebsiteOnce(deps)).state, 'failed')
    assert.equal((await job(1)).attempts, 1)
    assert.equal(dispatched.length, 0)
  })

  test('staging and self-hosted instances never reach the production marker or dispatcher', async () => {
    await publish(1)
    deps.productionPublishing = false
    deps.fetchImpl = async () => { assert.fail('non-production instance fetched production marker') }
    deps.installationTokens = { onRepository: async () => { assert.fail('non-production instance contacted GitHub') } }
    assert.equal((await refreshWebsiteOnce(deps)).state, 'failed')
    assert.match((await job(1)).last_error, /only on app\.antifailure\.dev/)
    assert.equal(dispatched.length, 0)
  })

  test('missing Actions permission refuses the dispatch and can be manually retried after correction', async () => {
    await publish(1)
    deps.installationTokens = { onRepository: async () => ({ id: 123, permissions: { actions: 'read' } }) }
    assert.equal((await refreshWebsiteOnce(deps)).state, 'failed')
    assert.match((await job(1)).last_error, /Actions write/)
    assert.equal(dispatched.length, 0)
    deps.installationTokens = { onRepository: async () => ({ id: 123, permissions: { actions: 'write' } }) }
    await h.admin`UPDATE website_refresh_jobs SET status = 'queued', next_attempt_at = ${clock.now()} WHERE revision = 1`
    assert.equal((await refreshWebsiteOnce(deps)).state, 'dispatched')
  })

  test('retry exhaustion stops automatically rather than dispatching forever', async () => {
    await publish(1)
    await h.admin`UPDATE website_refresh_jobs SET attempts = 4 WHERE revision = 1`
    dispatch = async () => { throw new Error('unavailable') }
    assert.equal((await refreshWebsiteOnce(deps)).state, 'failed')
    clock.advance(24 * 60 * 60_000)
    assert.equal((await refreshWebsiteOnce(deps)).state, 'failed')
    assert.equal(dispatched.length, 1)
  })

  test('the final dispatch response is lost, then the build finishes: failure self-resolves without another dispatch', async () => {
    await publish(1)
    await h.admin`UPDATE website_refresh_jobs SET attempts = 4 WHERE revision = 1`
    dispatch = async () => { throw new Error('response lost after acceptance') }
    assert.equal((await refreshWebsiteOnce(deps)).state, 'failed')
    marker = async () => response(1)
    clock.advance(5 * 60_000)
    assert.equal((await refreshWebsiteOnce(deps)).state, 'deployed')
    assert.equal(dispatched.length, 1)
    assert.equal((await job(1)).attempts, 5)
    assert.equal((await job(1)).last_error, null)
  })

  test('two replicas claiming concurrently dispatch only once', async () => {
    await publish(1)
    const results = await Promise.all([refreshWebsiteOnce(deps), refreshWebsiteOnce(deps)])
    assert.deepEqual(results.map((x) => x.state).sort(), ['dispatched', 'idle'])
    assert.equal(dispatched.length, 1)
  })

  test('a crash after dispatch resumes observation after lease expiry instead of dispatching immediately', async () => {
    await publish(1)
    await h.admin`UPDATE website_refresh_jobs SET status = 'dispatching', attempts = 1,
      lease_token = ${randomUUID()}, lease_until = ${new Date(clock.now().getTime() + 120_000)},
      dispatched_at = ${clock.now()} WHERE revision = 1`
    assert.equal((await refreshWebsiteOnce(deps)).state, 'idle')
    clock.advance(120_001)
    assert.equal((await refreshWebsiteOnce(deps)).state, 'waiting')
    assert.equal(dispatched.length, 0)
    marker = async () => response(1)
    clock.advance(15_000)
    assert.equal((await refreshWebsiteOnce(deps)).state, 'deployed')
  })

  test('a stale worker cannot overwrite the replacement lease or its observed result', async () => {
    await publish(1)
    const entered = gate()
    const release = gate()
    dispatch = async () => { entered.resolve(); await release.promise }
    const stale = refreshWebsiteOnce(deps)
    await entered.promise
    clock.advance(120_001)
    marker = async () => response(1)
    assert.equal((await refreshWebsiteOnce(deps)).state, 'deployed')
    release.resolve()
    assert.equal((await stale).state, 'superseded')
    assert.equal((await job(1)).status, 'deployed')
  })

  test('a stale worker cannot write while its replacement still owns a live lease', async () => {
    await publish(1)
    const dispatchEntered = gate()
    const dispatchRelease = gate()
    dispatch = async () => { dispatchEntered.resolve(); await dispatchRelease.promise }
    const stale = refreshWebsiteOnce(deps)
    await dispatchEntered.promise
    clock.advance(120_001)
    const markerEntered = gate()
    const markerRelease = gate()
    marker = async () => { markerEntered.resolve(); await markerRelease.promise; return response(1) }
    const replacement = refreshWebsiteOnce(deps)
    await markerEntered.promise
    dispatchRelease.resolve()
    assert.equal((await stale).state, 'superseded')
    markerRelease.resolve()
    assert.equal((await replacement).state, 'deployed')
    assert.equal((await job(1)).status, 'deployed')
  })

  test('publish B during dispatch A: completion A never marks B live or changes published content', async () => {
    await publish(1)
    const entered = gate()
    const release = gate()
    dispatch = async () => { entered.resolve(); await release.promise }
    const older = refreshWebsiteOnce(deps)
    await entered.promise
    await publish(2)
    dispatch = async () => {}
    assert.equal((await refreshWebsiteOnce(deps)).state, 'dispatched')
    release.resolve()
    assert.equal((await older).state, 'superseded')
    assert.equal((await job(1)).status, 'superseded')
    assert.equal((await job(2)).status, 'waiting')
    const published = await h.admin`SELECT revision, content_hash FROM website_published WHERE id = 'homepage'`
    assert.equal(Number(published[0]?.revision), 2)
    assert.equal(published[0]?.content_hash, hash(2))
  })

  test('B deployed then a late A build: continued reconciliation detects and repairs the regression', async () => {
    await publish(2)
    marker = async () => response(2)
    assert.equal((await refreshWebsiteOnce(deps)).state, 'deployed')
    marker = async () => response(1)
    clock.advance(5 * 60_000)
    assert.equal((await refreshWebsiteOnce(deps)).state, 'retrying')
    assert.equal((await job(2)).deployed_at, null)
    assert.equal((await job(2)).attempts, 0)
    clock.advance(30_000)
    assert.equal((await refreshWebsiteOnce(deps)).state, 'dispatched')
    assert.equal(dispatched[0]?.inputs.cms_revision, '2')
  })

  test('malformed or unreachable marker never marks a revision deployed', async () => {
    await publish(1)
    marker = async () => { throw new Error('network unavailable') }
    assert.equal((await refreshWebsiteOnce(deps)).state, 'dispatched')
    marker = async () => new Response('<html>fallback</html>')
    clock.advance(15_000)
    assert.equal((await refreshWebsiteOnce(deps)).state, 'waiting')
    assert.equal((await job(1)).deployed_at, null)
  })

  test('shutdown waits for the active pass and refuses a later manual tick', async () => {
    await publish(1)
    const entered = gate()
    const release = gate()
    dispatch = async () => { entered.resolve(); await release.promise }
    const worker = startWebsiteRefreshWorker(deps)
    await entered.promise
    let stopped = false
    const stopping = worker.stop().then(() => { stopped = true })
    await Promise.resolve()
    assert.equal(stopped, false)
    release.resolve()
    await stopping
    assert.equal(stopped, true)
    clock.advance(15_000)
    assert.equal((await worker.runOnce()).state, 'idle')
    assert.equal(dispatched.length, 1)
  })

  test('a locked completion is bounded and cannot keep shutdown waiting indefinitely', { timeout: 10_000 }, async () => {
    await publish(1)
    const markerEntered = gate()
    const markerRelease = gate()
    marker = async () => { markerEntered.resolve(); await markerRelease.promise; return response(1) }
    const worker = startWebsiteRefreshWorker(deps)
    await markerEntered.promise
    const lockEntered = gate()
    const lockRelease = gate()
    const lock = h.admin.begin(async (db) => {
      await db`SELECT revision FROM website_refresh_jobs WHERE revision = 1 FOR UPDATE`
      lockEntered.resolve()
      await lockRelease.promise
    })
    try {
      await lockEntered.promise
      const stopped = worker.stop()
      markerRelease.resolve()
      await stopped
      assert.equal((await worker.runOnce()).state, 'idle')
    } finally {
      lockRelease.resolve()
      await lock
    }
  })
})
