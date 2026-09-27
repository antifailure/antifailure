import assert from 'node:assert/strict'
import { test } from 'node:test'
import { setTimeout as delay } from 'node:timers/promises'
import { startControlPlane, type ControlPlane } from '../src/boot.ts'
import { adminUrl, appUrl, available, startApi } from './harness.ts'

const hasDatabase = await available()

test('boot starts website refresh and close drains it before closing the operator pool', {
  skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL', timeout: 60_000,
}, async () => {
  const h = await startApi()
  const saved = await h.admin`SELECT * FROM website_published WHERE id = 'homepage'`
  const revision = 9_000_001
  const contentHash = 'c'.repeat(64)
  const environment = { ...process.env }
  const fetchOriginal = globalThis.fetch
  const signals = new Map((['SIGINT', 'SIGTERM'] as const).map((name) => [name, new Set(process.listeners(name))] as const))
  let plane: ControlPlane | undefined
  let releaseMarker!: () => void
  let markerStarted!: () => void
  const markerGate = new Promise<void>((resolve) => { releaseMarker = resolve })
  const started = new Promise<void>((resolve) => { markerStarted = resolve })

  try {
    await h.admin`INSERT INTO website_history(revision, draft_revision, document, content_hash, created_by)
      VALUES (${revision}, 0, ${h.admin.json(saved[0]!.document)}, ${contentHash}, 'boot-test')`
    await h.admin`UPDATE website_published SET revision = ${revision}, content_hash = ${contentHash} WHERE id = 'homepage'`
    await h.admin`INSERT INTO website_refresh_jobs(revision) VALUES (${revision})`

    // This boots the real entry point with only local credentials. Inherited
    // vendor settings must never turn a lifecycle test into an external action.
    for (const key of Object.keys(process.env)) if (key.startsWith('AF_')) delete process.env[key]
    const operatorUrl = new URL(adminUrl)
    operatorUrl.username = 'antifailure_admin'
    operatorUrl.password = 'admin-test-password'
    Object.assign(process.env, {
      AF_DATABASE_URL: appUrl(), AF_ADMIN_DATABASE_URL: operatorUrl.toString(),
      AF_PORT: '0', AF_APP_BASE_URL: 'https://APP.antifailure.dev/', AF_INSECURE_COOKIES: '1',
      AF_GITHUB_CLIENT_ID: 'website-boot-test', AF_GITHUB_CLIENT_SECRET: 'not-a-real-secret',
      AF_GITHUB_REDIRECT_URI: 'http://app.test/auth/github/callback',
      AF_SITE_ORIGIN: 'http://localhost:4330',
    })
    globalThis.fetch = async (input) => {
      const url = new URL(input instanceof Request ? input.url : String(input))
      assert.equal(url.origin, 'https://antifailure.dev')
      assert.equal(url.pathname, '/cms-version.json')
      markerStarted()
      await markerGate
      return Response.json({ revision, contentHash })
    }
    plane = await startControlPlane()
    await started
    const live = await fetchOriginal(`http://127.0.0.1:${plane.port}/v1/website/published`)
    assert.equal(live.status, 200)
    assert.equal(((await live.json()) as { revision: number }).revision, revision)

    let closed = false
    const closing = plane.close().then(() => { closed = true })
    await delay(40)
    assert.equal(closed, false, 'close completed while the refresh still held work')
    releaseMarker()
    await closing
    plane = undefined
    const jobs = await h.admin`SELECT status FROM website_refresh_jobs WHERE revision = ${revision}`
    assert.equal(jobs[0]?.status, 'deployed', 'the real startup worker never reconciled its queued revision')

    // The staging database is allowed to publish its own runtime document,
    // but it must never dispatch a production website rebuild or query the
    // production marker on behalf of that local content.
    await h.admin`UPDATE website_refresh_jobs SET status = 'queued', next_attempt_at = now(), dispatched_at = NULL WHERE revision = ${revision}`
    process.env.AF_APP_BASE_URL = 'https://app.dev.antifailure.dev'
    let stagingFetches = 0
    globalThis.fetch = async () => { stagingFetches++; throw new Error('Staging reached a production service') }
    plane = await startControlPlane()
    await plane.close()
    plane = undefined
    const staging = await h.admin`SELECT status, last_error FROM website_refresh_jobs WHERE revision = ${revision}`
    assert.equal(staging[0]?.status, 'failed')
    assert.match(staging[0]?.last_error, /only on app\.antifailure\.dev/)
    assert.equal(stagingFetches, 0, 'staging consulted or dispatched production')
  } finally {
    releaseMarker()
    await plane?.close()
    globalThis.fetch = fetchOriginal
    for (const key of Object.keys(process.env)) if (!(key in environment)) delete process.env[key]
    Object.assign(process.env, environment)
    for (const [name, existing] of signals) {
      for (const listener of process.listeners(name)) if (!existing.has(listener)) process.removeListener(name, listener)
    }
    await h.admin`DELETE FROM website_refresh_jobs WHERE revision = ${revision}`
    await h.admin`DELETE FROM website_history WHERE revision = ${revision}`
    await h.admin`UPDATE website_published SET revision = ${saved[0]!.revision}, content_hash = ${saved[0]!.content_hash}, document = ${h.admin.json(saved[0]!.document)} WHERE id = 'homepage'`
    await h.close()
  }
})
