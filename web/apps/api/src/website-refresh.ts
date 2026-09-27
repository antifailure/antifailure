// Publishing makes the runtime document live in the same transaction as its
// history entry. This worker refreshes the static HTML independently. A GitHub
// dispatch is only an acknowledgement; only the served revision marker proves
// that the static website caught up.

import { randomUUID } from 'node:crypto'
import type { AdminPool } from '@antifailure/db'
import type { GitHubClient } from './auth/github.ts'
import type { InstallationTokens } from './github/app.ts'
import type { Clock } from './clock.ts'

const REPOSITORY = 'antifailure/antifailure'
const WORKFLOW = 'deploy.yml'
const REF = 'main'
const MARKER_URL = 'https://antifailure.dev/cms-version.json'
const POLL_MS = 15_000
const LEASE_MS = 120_000
const OBSERVE_MS = 30 * 60_000
const RECONCILE_MS = 5 * 60_000
const MAX_ATTEMPTS = 5
const EXTERNAL_TIMEOUT_MS = 20_000
const MAX_MARKER_BYTES = 4096

export interface WebsiteRefreshDeps {
  adminPool: AdminPool
  clock: Clock
  /** Enabled only by the production control plane's trusted configuration.
   * Staging and self-hosted databases must never dispatch the production site. */
  productionPublishing?: boolean
  github: Pick<GitHubClient, 'dispatchWorkflow'>
  installationTokens?: Pick<InstallationTokens, 'onRepository'> | null
  /** Transport injection, never a caller-controlled destination. */
  fetchImpl?: typeof fetch
  log?: (message: string, error?: unknown) => void
}

export interface RefreshSweepResult {
  state: 'idle' | 'dispatched' | 'waiting' | 'deployed' | 'retrying' | 'failed' | 'superseded'
  revision?: number
}

interface ClaimedJob {
  revision: number
  contentHash: string
  token: string
  attempts: number
  dispatchedAt: Date | null
  sendDispatch: boolean
  wasDeployed: boolean
  wasFailed: boolean
  lastError: string | null
}

export interface WebsiteRevisionMarker {
  revision: number
  contentHash: string
}

/** Read the real static artifact, without following redirects or trusting an
 * unbounded JSON response. A login page or an old CDN document is not success. */
export async function readWebsiteRevisionMarker(
  fetchImpl: typeof fetch = fetch,
  signal?: AbortSignal,
): Promise<WebsiteRevisionMarker> {
  const url = new URL(MARKER_URL)
  url.searchParams.set('check', randomUUID())
  const response = await fetchImpl(url, {
    cache: 'no-store',
    redirect: 'error',
    headers: { accept: 'application/json', 'cache-control': 'no-cache' },
    signal: signal ?? AbortSignal.timeout(EXTERNAL_TIMEOUT_MS),
  })
  if (response.status !== 200) throw new Error(`Static revision check returned ${response.status}.`)
  if (!/^application\/json(?:\s*;|$)/i.test(response.headers.get('content-type') ?? '')) {
    throw new Error('Static revision check did not return JSON.')
  }
  const length = response.headers.get('content-length')
  if (length !== null && (!/^\d+$/.test(length) || Number(length) > MAX_MARKER_BYTES)) {
    await response.body?.cancel()
    throw new Error('Static revision marker exceeded its size limit.')
  }
  const reader = response.body?.getReader()
  if (!reader) throw new Error('Static revision marker was empty.')
  const chunks: Uint8Array[] = []
  let size = 0
  try {
    while (true) {
      const chunk = await reader.read()
      if (chunk.done) break
      size += chunk.value.byteLength
      if (size > MAX_MARKER_BYTES) throw new Error('Static revision marker exceeded its size limit.')
      chunks.push(chunk.value)
    }
  } finally {
    await reader.cancel().catch(() => {})
    reader.releaseLock()
  }
  let value: unknown
  try {
    value = JSON.parse(Buffer.concat(chunks).toString('utf8'))
  } catch {
    throw new Error('Static revision marker contained invalid JSON.')
  }
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error('Static revision marker had an invalid shape.')
  }
  const marker = value as Record<string, unknown>
  const revision = typeof marker.revision === 'string' && /^(0|[1-9]\d*)$/.test(marker.revision)
    ? Number(marker.revision)
    : marker.revision
  if (typeof revision !== 'number' || !Number.isSafeInteger(revision) || revision < 0 ||
      typeof marker.contentHash !== 'string' || !/^[0-9a-f]{64}$/.test(marker.contentHash)) {
    throw new Error('Static revision marker had an invalid revision or content hash.')
  }
  return { revision, contentHash: marker.contentHash }
}

async function bounded<T>(operation: (signal: AbortSignal) => Promise<T>): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined
  const controller = new AbortController()
  try {
    return await Promise.race([
      operation(controller.signal),
      new Promise<never>((_, reject) => {
        timer = setTimeout(() => {
          const error = new Error('The website refresh request timed out.')
          controller.abort(error)
          reject(error)
        }, EXTERNAL_TIMEOUT_MS)
        timer.unref()
      }),
    ])
  } finally {
    if (timer) clearTimeout(timer)
  }
}

function backoff(attempts: number): number {
  return Math.min(30_000 * 2 ** Math.max(0, attempts - 1), 15 * 60_000)
}

async function claim(deps: WebsiteRefreshDeps): Promise<ClaimedJob | null> {
  await deps.adminPool.ensureBypass()
  const now = deps.clock.now()
  const token = randomUUID()
  // This is a system continuation of an already audited publish, not a new
  // operator action. Use the separate operator credential without inventing a
  // human admin identity. No transaction stays open during a network request.
  return await deps.adminPool.sql.begin(async (db) => {
    await db`SET LOCAL statement_timeout = '10s'`
    await db`SET LOCAL lock_timeout = '3s'`
    await db`
      UPDATE website_refresh_jobs AS j
      SET status = 'superseded', lease_token = NULL, lease_until = NULL
      WHERE j.status IN ('queued', 'dispatching', 'waiting', 'failed')
        AND EXISTS (SELECT 1 FROM website_published p WHERE p.id = 'homepage' AND p.revision <> j.revision)`
    const rows = await db<{
      revision: string; status: string; attempts: number; dispatched_at: Date | string | null; content_hash: string; last_error: string | null
    }[]>`
      SELECT j.revision, j.status, j.attempts, j.dispatched_at, p.content_hash, j.last_error
      FROM website_refresh_jobs j
      JOIN website_published p ON p.id = 'homepage' AND p.revision = j.revision
      WHERE j.status IN ('queued', 'dispatching', 'waiting', 'deployed', 'failed')
        AND j.next_attempt_at <= ${now.toISOString()}
        AND (j.lease_until IS NULL OR j.lease_until <= ${now.toISOString()})
      ORDER BY j.revision DESC
      LIMIT 1 FOR UPDATE OF j SKIP LOCKED`
    const row = rows[0]
    if (!row) return null
    const revision = Number(row.revision)
    if (!Number.isSafeInteger(revision) || revision <= 0 || !/^[0-9a-f]{64}$/.test(row.content_hash)) {
      await db`UPDATE website_refresh_jobs SET status = 'failed', last_error = 'Stored website revision is invalid.' WHERE revision = ${row.revision}`
      return null
    }
    const sendDispatch = row.status === 'queued'
    const attempts = row.attempts + (sendDispatch ? 1 : 0)
    const dispatchedAt = sendDispatch ? now : row.dispatched_at === null ? null : new Date(row.dispatched_at)
    await db`
      UPDATE website_refresh_jobs
      SET lease_token = ${token}, lease_until = ${new Date(now.getTime() + LEASE_MS).toISOString()},
          status = ${sendDispatch ? 'dispatching' : row.status}, attempts = ${attempts},
          dispatched_at = ${dispatchedAt?.toISOString() ?? null}
      WHERE revision = ${row.revision}`
    return {
      revision, contentHash: row.content_hash, token, attempts, dispatchedAt,
      sendDispatch, wasDeployed: row.status === 'deployed', wasFailed: row.status === 'failed', lastError: row.last_error,
    }
  }) as ClaimedJob | null
}

/** Every completion compares the lease AND the current published pointer.
 * A worker returning after a newer publication or lease takeover has no write. */
async function complete(
  deps: WebsiteRefreshDeps,
  job: ClaimedJob,
  status: 'queued' | 'waiting' | 'deployed' | 'failed',
  delay: number,
  error: string | null = null,
  resetAttempts = false,
): Promise<boolean> {
  const now = deps.clock.now()
  const result = await deps.adminPool.sql.begin(async (db) => {
    await db`SET LOCAL statement_timeout = '10s'`
    await db`SET LOCAL lock_timeout = '3s'`
    return await db`
    UPDATE website_refresh_jobs j
    SET status = ${status}, next_attempt_at = ${new Date(now.getTime() + delay).toISOString()},
        lease_token = NULL, lease_until = NULL, last_error = ${error},
        attempts = CASE WHEN ${resetAttempts} THEN 0 ELSE attempts END,
        deployed_at = CASE WHEN ${status} = 'deployed' THEN COALESCE(deployed_at, ${now.toISOString()}::timestamptz)
                           WHEN ${job.wasDeployed} THEN NULL ELSE deployed_at END
    WHERE j.revision = ${job.revision} AND j.lease_token = ${job.token}
      AND j.lease_until > ${now.toISOString()}
      AND EXISTS (SELECT 1 FROM website_published p
                  WHERE p.id = 'homepage' AND p.revision = j.revision AND p.content_hash = ${job.contentHash})
    RETURNING revision`
  })
  return result.length === 1
}

async function stillCurrent(deps: WebsiteRefreshDeps, job: ClaimedJob): Promise<boolean> {
  const rows = await deps.adminPool.sql.begin(async (db) => {
    await db`SET LOCAL statement_timeout = '10s'`
    await db`SET LOCAL lock_timeout = '3s'`
    return await db`
    SELECT 1 FROM website_refresh_jobs j
    JOIN website_published p ON p.id = 'homepage' AND p.revision = j.revision
    WHERE j.revision = ${job.revision} AND j.lease_token = ${job.token}
      AND j.lease_until > ${deps.clock.now().toISOString()} AND p.content_hash = ${job.contentHash}`
  })
  return rows.length === 1
}

async function retry(deps: WebsiteRefreshDeps, job: ClaimedJob, error: string, terminal = false): Promise<RefreshSweepResult> {
  const failed = terminal || job.attempts >= MAX_ATTEMPTS
  const changed = await complete(deps, job, failed ? 'failed' : 'queued', failed ? RECONCILE_MS : backoff(job.attempts), error)
  return { state: changed ? (failed ? 'failed' : 'retrying') : 'superseded', revision: job.revision }
}

/** One bounded pass, exported for the worker and behavioral database tests. */
export async function refreshWebsiteOnce(deps: WebsiteRefreshDeps): Promise<RefreshSweepResult> {
  const job = await claim(deps)
  if (!job) return { state: 'idle' }
  if (!deps.productionPublishing) {
    return retry(deps, job, 'Static publishing is enabled only on app.antifailure.dev. This instance can save drafts and previews, but cannot refresh the production website.', true)
  }
  let marker: WebsiteRevisionMarker | null = null
  try {
    marker = await bounded((signal) => readWebsiteRevisionMarker(deps.fetchImpl, signal))
  } catch {
    // A missing marker is expected before the first CMS-enabled website build.
    // The accepted-dispatch observation window still bounds subsequent checks.
  }
  if (marker?.revision === job.revision && marker.contentHash === job.contentHash) {
    const changed = await complete(deps, job, 'deployed', RECONCILE_MS)
    return { state: changed ? 'deployed' : 'superseded', revision: job.revision }
  }
  if (job.wasFailed) {
    // A final dispatch response can be lost after GitHub accepted it. Continue
    // observing, without dispatching again, so a late successful build resolves
    // the error without requiring somebody to press Retry.
    const changed = await complete(deps, job, 'failed', RECONCILE_MS, job.lastError)
    return { state: changed ? 'failed' : 'superseded', revision: job.revision }
  }
  if (job.wasDeployed) {
    const changed = await complete(deps, job, 'queued', 30_000,
      marker ? 'The static site serves an older or different revision. A refresh is queued.'
        : 'The static revision could not be confirmed. A refresh check is queued.', true)
    return { state: changed ? 'retrying' : 'superseded', revision: job.revision }
  }
  if (!job.sendDispatch) {
    if (!job.dispatchedAt || deps.clock.now().getTime() - job.dispatchedAt.getTime() >= OBSERVE_MS) {
      return retry(deps, job, 'The website did not serve this revision within 30 minutes. Check the Deploy workflow in GitHub Actions.')
    }
    const changed = await complete(deps, job, 'waiting', POLL_MS)
    return { state: changed ? 'waiting' : 'superseded', revision: job.revision }
  }
  if (!deps.installationTokens) {
    return retry(deps, job, 'Static refresh needs the Antifailure GitHub App. Configure the App on this control plane, then retry.', true)
  }
  try {
    const tokens = deps.installationTokens
    const installed = await bounded((signal) => tokens.onRepository(REPOSITORY, signal))
    if (!installed || !Number.isSafeInteger(installed.id) || installed.id <= 0) {
      return retry(deps, job, 'The Antifailure GitHub App is not installed on antifailure/antifailure. Add the repository, then retry.', true)
    }
    if (installed.permissions.actions !== 'write') {
      return retry(deps, job, 'The Antifailure GitHub App needs Actions write on antifailure/antifailure. Approve that permission, then retry.', true)
    }
    if (!(await stillCurrent(deps, job))) return { state: 'superseded', revision: job.revision }
    await bounded((signal) => deps.github.dispatchWorkflow(installed.id, REPOSITORY, WORKFLOW, REF, {
      cms_revision: String(job.revision), cms_content_hash: job.contentHash,
    }, signal))
    const changed = await complete(deps, job, 'waiting', POLL_MS)
    return { state: changed ? 'dispatched' : 'superseded', revision: job.revision }
  } catch {
    // External response bodies are deliberately not copied into operator UI or
    // logs: a remote service can echo credentials or arbitrary HTML there.
    return retry(deps, job, 'GitHub could not start the website refresh. Check the App permissions and Deploy workflow.')
  }
}

/** Starts immediately so a publication queued before restart is not forgotten.
 * Multiple replicas are safe through leases; one replica never overlaps itself. */
export function startWebsiteRefreshWorker(deps: WebsiteRefreshDeps): {
  runOnce(): Promise<RefreshSweepResult>
  stop(): Promise<void>
} {
  let stopped = false
  let active: Promise<RefreshSweepResult> | null = null
  const runOnce = (): Promise<RefreshSweepResult> => {
    if (stopped) return Promise.resolve({ state: 'idle' })
    if (active) return active
    active = refreshWebsiteOnce(deps).finally(() => { active = null })
    return active
  }
  const tick = () => {
    void runOnce().catch((error) => deps.log?.('Website static refresh worker could not finish its pass.', error))
  }
  const timer = setInterval(tick, POLL_MS)
  timer.unref()
  tick()
  return {
    runOnce,
    async stop() {
      stopped = true
      clearInterval(timer)
      await active?.catch(() => {})
    },
  }
}
