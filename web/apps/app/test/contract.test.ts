// What the API actually returns, against what this application says it does.
//
// lib/api.ts writes out the shape of every response rather than importing it
// from the router, for a reason that is written down there: several of those
// procedures return the rows of a hand-written SELECT, which tRPC types as an
// untyped record, so an import would infer `unknown` for exactly the fields a
// page renders.
//
// That trade is only safe with this. It runs the real server against a real
// Postgres, calls each procedure the application calls, and asserts that every
// field named in lib/api.ts came back and came back with the type named. A
// column renamed in a migration, a SELECT list edited, a jsonb value that
// arrives as text: each of them fails here rather than on a page.
//
// It is deliberately about presence and type, not about values. Asserting that
// a row holds a particular environment would be asserting about the fixture.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import postgres from 'postgres'
import { createPool, migrate, type Pool } from '@antifailure/db'
import { createServer } from '@antifailure/api'
import { FakeClock } from '../../api/src/clock.ts'
import { FakeGitHub } from '../../api/src/auth/fakegithub.ts'
import { issueSession } from '../../api/src/auth/session.ts'

const adminUrl =
  process.env.AF_TEST_DATABASE_URL ?? 'postgres://postgres:test@127.0.0.1:55432/antifailure'

async function available(): Promise<boolean> {
  try {
    const probe = postgres(adminUrl, { max: 1, connect_timeout: 3, onnotice: () => {} })
    await probe`SELECT 1`
    await probe.end({ timeout: 2 })
    return true
  } catch {
    return false
  }
}

const hasDatabase = await available()

/** Every field lib/api.ts names, and what it may be.
 *
 * `null` in a list means the field is nullable. A field that is absent is a
 * failure whatever else is true: `undefined` renders as nothing, silently, and
 * that is the failure mode this file exists to catch. */
type Kind = 'string' | 'number' | 'boolean' | 'object' | 'array' | 'null'

const SHAPES: Record<string, Record<string, Kind[]>> = {
  environment: {
    id: ['string'],
    env_id: ['string'],
    branch: ['string'],
    pull_request: ['number', 'null'],
    state: ['string'],
    preview_url: ['string', 'null'],
    runtime: ['string', 'null'],
    golden_version: ['string', 'null'],
    created_at: ['string'],
    updated_at: ['string'],
    expires_at: ['string', 'null'],
    repository: ['string'],
  },
  run: {
    id: ['string'],
    kind: ['string'],
    state: ['string'],
    started_at: ['string', 'null'],
    finished_at: ['string', 'null'],
    created_at: ['string'],
  },
  verdict: {
    workflow: ['string'],
    persona: ['string', 'null'],
    value: ['string'],
    summary: ['string', 'null'],
    steps: ['number'],
    duration_ms: ['number', 'null'],
    // An array, not a string. A jsonb column that arrives JSON-encoded is the
    // defect this row is here for: the page renders escaped quotes and nothing
    // fails.
    reproduction: ['array', 'null'],
  },
  artifact: {
    id: ['string'],
    kind: ['string'],
    step: ['number', 'null'],
    content_type: ['string', 'null'],
    size_bytes: ['number', 'null'],
    sha256: ['string', 'null'],
    retained: ['boolean'],
  },
  auditEntry: {
    seq: ['number'],
    actor_label: ['string'],
    action: ['string'],
    target_type: ['string'],
    target_id: ['string', 'null'],
    origin: ['string'],
    detail: ['object', 'null'],
    occurred_at: ['string'],
  },
}

function kindOf(value: unknown): Kind | 'undefined' {
  if (value === undefined) return 'undefined'
  if (value === null) return 'null'
  if (Array.isArray(value)) return 'array'
  const t = typeof value
  if (t === 'string' || t === 'number' || t === 'boolean' || t === 'object') return t
  return 'undefined'
}

function assertShape(what: string, row: Record<string, unknown>): void {
  const shape = SHAPES[what]!
  for (const [field, allowed] of Object.entries(shape)) {
    const got = kindOf(row[field])
    assert.notEqual(
      got,
      'undefined',
      `${what}.${field} is named in lib/api.ts and did not come back. A page renders it as ` +
        `nothing, and nothing throws.`,
    )
    assert.ok(
      allowed.includes(got as Kind),
      `${what}.${field} came back as ${got}; lib/api.ts says ${allowed.join(' or ')}. ` +
        `Value: ${JSON.stringify(row[field])?.slice(0, 120)}`,
    )
  }
}

describe('the API returns what this application says it does', {
  skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  let admin: postgres.Sql
  let pool: Pool
  let app: ReturnType<typeof createServer>['app']
  let cookie: string
  let orgId: string
  let envId: string
  let runId: string

  before(async () => {
    admin = postgres(adminUrl, { max: 4, connect_timeout: 10, onnotice: () => {} })
    await migrate(admin)
    await admin.unsafe(`ALTER ROLE antifailure_app LOGIN PASSWORD 'app-test-password'`)

    const url = new URL(adminUrl)
    url.username = 'antifailure_app'
    url.password = 'app-test-password'
    pool = createPool({ url: url.toString(), max: 4 })
    const clock = new FakeClock()
    ;({ app } = createServer({
      pool,
      github: new FakeGitHub(clock),
      clock,
      secureCookies: false,
      appBaseUrl: 'http://app.test/',
    }))

    // One organization with one of everything the pages read, written here
    // rather than taken from whatever happens to be in the database, so this
    // suite says the same thing on an empty machine and a used one.
    const slug = `contract-${randomUUID().slice(0, 8)}`
    const [org] = await admin<{ id: string }[]>`
      INSERT INTO organizations (slug, name, github_login) VALUES (${slug}, ${slug}, ${slug})
      RETURNING id`
    orgId = org!.id

    const [repo] = await admin<{ id: string }[]>`
      INSERT INTO repositories (org_id, full_name) VALUES (${orgId}, ${`${slug}/app`}) RETURNING id`

    envId = `af-${slug}`
    const [env] = await admin<{ id: string }[]>`
      INSERT INTO environments
        (org_id, repository_id, env_id, branch, pull_request, state, preview_url, runtime,
         golden_version, expires_at)
      VALUES (${orgId}, ${repo!.id}, ${envId}, 'main', 42, 'running',
              ${'http://localhost:3000'}, 'local', 'g-1', now() + interval '7 days')
      RETURNING id`

    const [run] = await admin<{ id: string }[]>`
      INSERT INTO runs (org_id, environment_id, kind, state, started_at, finished_at)
      VALUES (${orgId}, ${env!.id}, 'test', 'complete', now(), now())
      RETURNING id`
    runId = run!.id

    // sql.json rather than a stringified value with a ::jsonb cast. The second
    // one is what put a JSON string in a jsonb column, and writing it that way
    // here would make this test agree with the bug.
    await admin`
      INSERT INTO verdicts (org_id, run_id, workflow, persona, value, summary, steps, duration_ms, reproduction)
      VALUES (${orgId}, ${runId}, 'sign-in', 'admin', 'fail', 'it did not', 4, 1200,
              ${admin.json(['Open /login', 'Press Send link'])})`

    await admin`
      INSERT INTO artifacts (org_id, run_id, kind, step, storage_key, content_type, size_bytes, sha256)
      VALUES (${orgId}, ${runId}, 'trace', 1, ${`runs/${runId}/trace`}, 'application/zip', 4096, 'abc')`

    await admin`
      INSERT INTO network_rules (org_id, repository_id, host, mode, note, position)
      VALUES (${orgId}, ${repo!.id}, 'api.stripe.com', 'mock', 'the pack answers this', 0)`

    const login = `contract-${randomUUID().slice(0, 6)}`
    const [user] = await admin<{ id: string }[]>`
      INSERT INTO users (github_id, github_login, email, name)
      VALUES (${Math.floor(Math.random() * 1e12)}, ${login}, ${`${login}@example.test`}, 'Contract')
      RETURNING id`
    await admin`
      INSERT INTO members (org_id, user_id, role, source) VALUES (${orgId}, ${user!.id}, 'owner', 'manual')`

    // An audit entry through the application's own path, so `detail` is
    // written the way the product writes it.
    await pool.withTenant({ orgId, userId: user!.id }, async (db) => {
      const { appendAudit } = await import('@antifailure/db')
      await appendAudit(db, {
        orgId,
        actorUserId: user!.id,
        actorLabel: login,
        action: 'network.rule_proposed',
        targetType: 'repository',
        targetId: `${slug}/app`,
        origin: 'web',
        detail: { host: 'api.stripe.com', mode: 'mock' },
      })
    })

    const issued = await issueSession(pool, clock, { userId: user!.id, orgId })
    cookie = `af_session=${issued.token}`
  })

  after(async () => {
    if (orgId) {
      await admin`DELETE FROM audit_entries WHERE org_id = ${orgId}`
      await admin`DELETE FROM organizations WHERE id = ${orgId}`
    }
    await pool.close()
    await admin.end({ timeout: 5 })
  })

  /** Calls a query the way lib/api.ts calls it, and unwraps it the same way. */
  async function query<T>(path: string, input: unknown = {}): Promise<T> {
    const res = await app.fetch(
      new Request(
        `http://api.test/trpc/${path}?input=${encodeURIComponent(JSON.stringify(input))}`,
        { headers: { cookie } },
      ),
    )
    const body = (await res.json()) as { result?: { data?: unknown }; error?: { message?: string } }
    assert.ok(!body.error, `${path} answered with an error: ${body.error?.message}`)
    return body.result!.data as T
  }

  it('describes the session, including which organization it is in', async () => {
    const res = await app.fetch(new Request('http://api.test/auth/session', { headers: { cookie } }))
    const body = (await res.json()) as Record<string, unknown>
    for (const field of ['signedIn', 'label', 'orgId', 'orgSlug', 'role', 'csrfToken']) {
      assert.notEqual(
        body[field],
        undefined,
        `/auth/session did not return ${field}, which the header reads. ` +
          `Without orgSlug the corner of every page shows a UUID.`,
      )
    }
  })

  it('lists environments in the shape the matrix renders', async () => {
    const page = await query<{ environments: Record<string, unknown>[]; nextCursor: unknown }>(
      'environments.list',
      { limit: 50 },
    )
    assert.ok(Array.isArray(page.environments), 'environments.list did not return an array')
    assert.ok(page.environments.length > 0, 'the fixture environment did not come back')
    assert.ok(
      page.nextCursor === null || typeof page.nextCursor === 'string',
      'nextCursor is neither a cursor nor null, so the pagination link is undecidable',
    )
    for (const row of page.environments) assertShape('environment', row)
  })

  it('returns one environment in the same shape as the list does', async () => {
    // Two SELECTs in the router, and they have drifted apart before. A detail
    // page that renders a field the list does not return is a blank field.
    const env = await query<Record<string, unknown>>('environments.get', { envId })
    assertShape('environment', env)
  })

  it('lists runs in the shape the environment page renders', async () => {
    const runs = await query<Record<string, unknown>[]>('runs.list', { envId, limit: 25 })
    assert.ok(runs.length > 0, 'the fixture run did not come back')
    for (const row of runs) assertShape('run', row)
  })

  it('returns verdicts with the reproduction as structure, not as text', async () => {
    const verdicts = await query<Record<string, unknown>[]>('runs.verdicts', { runId })
    assert.ok(verdicts.length > 0)
    for (const row of verdicts) assertShape('verdict', row)

    const failed = verdicts.find((v) => v.value === 'fail')!
    assert.ok(
      Array.isArray(failed.reproduction),
      `reproduction came back as ${typeof failed.reproduction}. A jsonb column that arrives ` +
        `JSON-encoded renders as a wall of escaped quotes and nothing throws.`,
    )
    assert.equal((failed.reproduction as string[]).length, 2)
  })

  it('lists artifacts in the shape the evidence table renders', async () => {
    const artifacts = await query<Record<string, unknown>[]>('runs.artifacts', { runId })
    assert.ok(artifacts.length > 0)
    for (const row of artifacts) assertShape('artifact', row)
  })

  it('returns the effective policy with its rules in order', async () => {
    const policy = await query<{ default: string; rules: Record<string, unknown>[]; hosts: unknown }>(
      'network.effective',
      {},
    )
    assert.equal(typeof policy.default, 'string')
    assert.ok(Array.isArray(policy.rules), 'rules is not an array, so the table renders nothing')
    assert.ok(Array.isArray(policy.hosts))
    for (const rule of policy.rules) {
      assert.equal(typeof rule.host, 'string')
      assert.equal(typeof rule.mode, 'string')
    }
  })

  it('explains one request with a decision and the rules it considered', async () => {
    const explained = await query<{
      decision: Record<string, unknown>
      chain: Record<string, unknown>[]
      inspectsHost: unknown
    }>('network.explain', { host: 'api.stripe.com', method: 'POST', path: '/v1/charges' })

    for (const field of ['mode', 'reason', 'matched', 'allowed', 'ruleHost']) {
      assert.notEqual(
        explained.decision[field],
        undefined,
        `the decision did not carry ${field}, which the explanation renders`,
      )
    }
    assert.ok(Array.isArray(explained.chain))
    assert.equal(typeof explained.inspectsHost, 'boolean')
    for (const match of explained.chain) {
      assert.equal(typeof match.specificity, 'number')
      assert.equal(typeof match.why, 'string')
      assert.equal(typeof (match.rule as { host?: unknown }).host, 'string')
    }
  })

  it('lists audit entries with the detail as structure', async () => {
    const entries = await query<Record<string, unknown>[]>('audit.list', { limit: 100 })
    assert.ok(entries.length > 0, 'the fixture audit entry did not come back')
    for (const row of entries) assertShape('auditEntry', row)
  })

  it('verifies the chain and says how many entries it covered', async () => {
    const report = await query<Record<string, unknown>>('audit.verify')
    assert.equal(typeof report.ok, 'boolean')
    assert.equal(typeof report.entries, 'number')
    assert.ok(Array.isArray(report.problems), 'problems is not an array, so the banner cannot list them')
    assert.ok(report.head === null || typeof report.head === 'string')
  })

  it('returns the organization status the quota tiles render', async () => {
    const status = await query<{
      slug: unknown
      plan: unknown
      suspended: unknown
      quotas: { environments: Record<string, unknown>; goldens: Record<string, unknown> }
    }>('org.status')
    assert.equal(typeof status.slug, 'string')
    assert.equal(typeof status.plan, 'string')
    assert.equal(typeof status.suspended, 'boolean')
    for (const quota of [status.quotas.environments, status.quotas.goldens]) {
      assert.equal(typeof quota.current, 'number')
      assert.equal(typeof quota.limit, 'number')
      assert.equal(typeof quota.allowed, 'boolean')
    }
  })
})
