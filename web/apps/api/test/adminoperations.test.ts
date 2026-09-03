// The failure explorer and the mail surface, driven through the real router.
//
// Four things are asserted here and each one is a defect that has shipped
// somewhere before:
//
//   1. A COLUMN THAT MUST NEVER LEAVE THE PROCESS DOES. `events.payload` is
//      the customer's data and `email_signin_tokens.token_hash` is a
//      credential's shadow. Both live one `SELECT *` away from a browser, and
//      RLS cannot help: it is row level, and the operator pool bypasses it
//      anyway. So the boundary is the column list, and the only way to know it
//      held is to seed a recognisable value and assert the response does not
//      contain it anywhere.
//
//   2. A KEYSET PAGE REPEATS OR DROPS A ROW. Ordering by a timestamp alone is
//      the usual cause and it is invisible: the page still looks full. Every
//      row here is seeded at the SAME instant on purpose, which is the case
//      that breaks a one-column cursor, and the test walks the whole list one
//      row at a time and asserts it saw each id exactly once.
//
//   3. A COUNT THAT QUIETLY MEANS SOMETHING NARROWER THAN ITS LABEL. A failure
//      list that counts only `failed` and not `timed_out` or `abandoned`
//      reports a smaller, calmer, wrong number, and nobody checks a number
//      that looks reasonable.
//
//   4. A PAGE THAT SAYS EMAIL WORKS WHEN NOTHING IS SENDING IT. The token row
//      is written whether or not a mailer exists, so no query can tell the two
//      apart. `canSend` reads the context, and it is asserted in both
//      directions, because a flag only ever seen true has not been shown to be
//      able to say no.

import { test, describe, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { createHash, randomUUID } from 'node:crypto'
import { createAdminPool, type AdminPool } from '@antifailure/db'
import { appRouter } from '../src/routers/index.ts'
import { adminSignIn, hashPassword } from '../src/admin/session.ts'
import type { AdminRole } from '../src/admin/permissions.ts'
import { RecordingMailer } from '../src/auth/mail.ts'
import { standingOf } from '../src/admin/operations.ts'
import { available, startApi, seedOrg, adminUrl, type ApiHarness, type Org } from './harness.ts'

const hasDb = await available()

/**
 * One instant for every seeded row, so the keyset test exercises the case a
 * single-column cursor gets wrong rather than the case it happens to survive.
 *
 * Taken from the harness clock rather than written as a literal. The routes
 * compute their window from `ctx.clock`, which starts in the past, so a
 * hardcoded date would put every fixture in the future: the lower bound would
 * still match it, and the lag and the standing would both be nonsense.
 */
let SAME_INSTANT: Date

/** A string that appears in no schema, no fixture and no other test, so
 *  finding it in a response means it came out of the column under test and
 *  from nowhere else. */
const CANARY = 'canary-6f2b1d9e-must-not-leave-the-process'

/**
 * A tag on every fixture this run creates.
 *
 * Not decoration. These routes read ACROSS tenants by design, so a fixture
 * named the same thing as a previous run's fixture is counted alongside it and
 * the assertion fails on a number that is right about the database and wrong
 * about this test. Borrowing a row is the same defect the harness wrote
 * seedImpersonationTarget to avoid, and it shows up here as a count that drifts
 * upward every time the suite is run.
 */
const RUN = randomUUID().slice(0, 8)
const FAILURE_CODE = `af-e-refused-${RUN}`
const WORKFLOW = `checkout-${RUN}`
const EVENT_TYPE = `run.started.${RUN}`
const PAGING_TYPE = `paging.probe.${RUN}`
/** A real sha256 hex string. `workload_versions_digest_shape` refuses anything
 *  else, which is the constraint doing its job. */
const DIGEST = createHash('sha256').update(RUN).digest('hex')

describe('the operations routes', { skip: hasDb ? false : 'no database' }, () => {
  let h: ApiHarness
  let org: Org
  let other: Org
  let adminPool: AdminPool
  const password = 'provisioned-at-deploy-not-in-source'

  async function callerFor(role: AdminRole, mailer: unknown = null) {
    const email = `${role}-${randomUUID().slice(0, 8)}@example.test`
    const { hash, salt } = await hashPassword(password)
    await h.admin`
      INSERT INTO admin_users (email, name, role, password_hash, password_salt, password_set_at)
      VALUES (${email}, ${role}, ${role}, ${hash}, ${salt}, now())`
    // Real time, never h.clock. resolveAdminSession compares against the
    // injected clock and the RLS policy behind current_admin_user() compares
    // expires_at against the DATABASE's now(); a fake past clock moves one and
    // not the other, and every operator read fails as an RLS violation that
    // reads like a permissions bug.
    const signedIn = await adminSignIn(h.pool, { email, password }, new Date())
    const { resolveAdminSession } = await import('../src/admin/session.ts')
    const resolved = await resolveAdminSession(h.pool, signedIn.token, new Date())
    assert.ok(resolved, 'the operator session did not resolve')
    return appRouter.createCaller({
      pool: h.pool,
      adminPool,
      clock: h.clock,
      github: h.github,
      stripe: null,
      appBaseUrl: 'http://localhost',
      mailer,
      productName: 'Antifailure',
      hostedRequiredPlan: null,
      actor: null,
      origin: 'web' as const,
      admin: {
        adminUserId: resolved.adminUserId,
        label: resolved.label,
        email: resolved.email,
        role: resolved.role,
        sessionId: resolved.sessionId,
        sessionHash: resolved.sessionHash,
        impersonating: resolved.impersonating,
        impersonatedUserId: resolved.impersonatedUserId,
      },
    } as never)
  }

  /** Seeds one workload run in a terminal state, with a failure code. */
  async function seedFailedRun(
    o: Org,
    state: string,
    failureCode: string | null,
    detail: string | null,
  ): Promise<string> {
    const [workload] = await h.admin<{ id: string }[]>`
      INSERT INTO workloads (org_id, repository_id, slug, name, kind)
      VALUES (${o.orgId}, ${o.repoId}, ${`w-${randomUUID().slice(0, 8)}`}, 'w', 'http_scenario')
      RETURNING id`
    const [version] = await h.admin<{ id: string }[]>`
      INSERT INTO workload_versions (org_id, workload_id, version, body, body_digest, source)
      VALUES (${o.orgId}, ${workload!.id}, 1, ${h.admin.json({})}, ${DIGEST}, 'authored')
      RETURNING id`
    const [env] = await h.admin<{ id: string }[]>`
      SELECT id FROM environments WHERE org_id = ${o.orgId} LIMIT 1`
    const [run] = await h.admin<{ id: string }[]>`
      INSERT INTO workload_runs (
        org_id, workload_id, workload_version_id, environment_id, state,
        request_key, repository, git_ref, deadline_at, finished_at, failure_code, detail,
        created_at, updated_at)
      VALUES (
        ${o.orgId}, ${workload!.id}, ${version!.id}, ${env!.id}, ${state}::workload_run_state,
        ${randomUUID()}, ${o.repository}, 'main', ${SAME_INSTANT},
        -- workload_runs_terminal_has_an_end. A terminal state without one is
        -- refused, which is the constraint keeping a finished run out of every
        -- duration computed from these columns.
        ${SAME_INSTANT}, ${failureCode}, ${detail}, ${SAME_INSTANT}, ${SAME_INSTANT})
      RETURNING id`
    return run!.id
  }

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'ops')
    other = await seedOrg(h.admin, 'opstwo')

    await h.admin.unsafe(`ALTER ROLE antifailure_admin LOGIN PASSWORD 'operator-test-password'`)
    const u = new URL(adminUrl)
    u.username = 'antifailure_admin'
    u.password = 'operator-test-password'
    adminPool = createAdminPool({ url: u.toString() })
    // Loudly here rather than as an empty page later, which is what a pool
    // pointed at a non-bypassing role produces.
    await adminPool.ensureBypass()

    // Fixtures land at the clock's own instant and then the clock moves one
    // minute, so every row is a minute old and sits inside the shortest window
    // the routes offer.
    SAME_INSTANT = h.clock.now()
    h.clock.advance(60_000)
  })

  after(async () => {
    await adminPool?.close()
    await h?.close()
  })

  /* ---------------------------------------------------------------------
   * Failures
   * ------------------------------------------------------------------ */

  test('a timed out run and an abandoned one are failures, not just a failed one', async () => {
    await seedFailedRun(org, 'failed', FAILURE_CODE, 'the target refused the connection')
    await seedFailedRun(org, 'timed_out', FAILURE_CODE, 'ran past its deadline')
    await seedFailedRun(other, 'abandoned', FAILURE_CODE, 'the engine went away')
    // Not a failure. Present so the counts below are counts of something and
    // not counts of everything.
    await seedFailedRun(org, 'succeeded', null, null)

    const caller = await callerFor('infrastructure')
    const view = await caller.admin.operations.logs.overview({ hours: 24 })

    const codes = view.failures.filter((f) => f.failureCode === FAILURE_CODE)
    assert.equal(codes.length, 3, 'each terminal state is its own group')
    const states = codes.map((c) => c.state).sort()
    assert.deepEqual(states, ['abandoned', 'failed', 'timed_out'])

    const totalRuns = codes.reduce((n, c) => n + c.runs, 0)
    assert.equal(totalRuns, 3, 'a failure list that counts only `failed` reports a wrong number')

    const orgsReached = new Set(codes.flatMap((c) => Array(c.organizations).fill(c.failureCode)))
    assert.ok(orgsReached.size > 0)
    const abandoned = codes.find((c) => c.state === 'abandoned')!
    assert.equal(abandoned.organizations, 1)

    assert.ok(
      codes.every((c) => c.latestRunId && c.firstSeen && c.lastSeen),
      'a group with no run to open is a count nobody can act on',
    )
  })

  test('a run that failed with no code is its own group rather than a hidden row', async () => {
    const caller = await callerFor('infrastructure')
    const uncodedIn = (view: { failures: { failureCode: string | null; state: string }[] }) =>
      view.failures.filter((f) => f.failureCode === null && f.state === 'failed').length

    // Counted before and after rather than asserted absolutely: a null failure
    // code carries no per-run tag, so the only honest question is whether this
    // row made the group appear or grow.
    const before = uncodedIn(await caller.admin.operations.logs.overview({ hours: 24 }))
    await seedFailedRun(org, 'failed', null, 'something went wrong and said nothing')
    const view = await caller.admin.operations.logs.overview({ hours: 24 })
    const uncoded = view.failures.find((f) => f.failureCode === null && f.state === 'failed')
    assert.ok(
      uncoded,
      'a failure with no code is a gap in the engine, and dropping the row hides the gap',
    )
    assert.ok(uncoded.runs >= 1)
    assert.ok(uncodedIn(view) >= Math.max(before, 1))
  })

  test('the window is a filter and not decoration', async () => {
    const caller = await callerFor('infrastructure')
    // Every seeded run is one minute old, so an hour holds them and a window
    // that ended before they happened must not.
    const anHour = await caller.admin.operations.logs.overview({ hours: 1 })
    assert.ok(anHour.failures.length > 0)

    const eightDays = 8 * 24 * 60 * 60 * 1000
    h.clock.advance(eightDays)
    try {
      const stale = await caller.admin.operations.logs.overview({ hours: 1 })
      assert.equal(stale.failures.length, 0, 'rows older than the window came back inside it')
    } finally {
      // In a finally, because this clock is shared with every test after this
      // one. Without it a single failed assertion here left the clock eight
      // days ahead and took seven unrelated tests down with it, which buries
      // the one real failure under a screen of consequences.
      h.clock.rollBack(eightDays)
    }
  })

  test('a row stamped ahead of the clock is outside the window, not inside every one', async () => {
    // Unbounded above, a row from the future sits in the last hour, the last
    // day and the last week at once, forever, and never ages out. It is clock
    // skew between this process and the database rather than a real failure,
    // and the list an operator reads during an incident is the wrong place for
    // it to be permanently pinned to the top.
    const caller = await callerFor('infrastructure')
    const code = `af-e-from-the-future-${RUN}`
    const ahead = new Date(h.clock.now().getTime() + 3 * 60 * 60 * 1000)
    const [w] = await h.admin<{ id: string }[]>`
      INSERT INTO workloads (org_id, repository_id, slug, name, kind)
      VALUES (${org.orgId}, ${org.repoId}, ${`fut-${randomUUID().slice(0, 8)}`}, 'w', 'http_scenario')
      RETURNING id`
    const [v] = await h.admin<{ id: string }[]>`
      INSERT INTO workload_versions (org_id, workload_id, version, body, body_digest, source)
      VALUES (${org.orgId}, ${w!.id}, 1, ${h.admin.json({})}, ${DIGEST}, 'authored')
      RETURNING id`
    const [env] = await h.admin<{ id: string }[]>`
      SELECT id FROM environments WHERE org_id = ${org.orgId} LIMIT 1`
    await h.admin`
      INSERT INTO workload_runs (org_id, workload_id, workload_version_id, environment_id, state,
        request_key, repository, git_ref, deadline_at, finished_at, failure_code, created_at, updated_at)
      VALUES (${org.orgId}, ${w!.id}, ${v!.id}, ${env!.id}, 'failed'::workload_run_state,
        ${randomUUID()}, ${org.repository}, 'main', ${ahead}, ${ahead}, ${code}, ${ahead}, ${ahead})`

    const view = await caller.admin.operations.logs.overview({ hours: 168 })
    assert.equal(
      view.failures.filter((f) => f.failureCode === code).length,
      0,
      'a run stamped three hours ahead of the clock was counted as having already failed',
    )
  })

  test('a failing workflow is grouped by name across organizations', async () => {
    const [runA] = await h.admin<{ id: string }[]>`
      INSERT INTO runs (org_id, environment_id, kind, state)
      VALUES (${org.orgId},
              (SELECT id FROM environments WHERE org_id = ${org.orgId} LIMIT 1),
              'agent', 'failed')
      RETURNING id`
    const [runB] = await h.admin<{ id: string }[]>`
      INSERT INTO runs (org_id, environment_id, kind, state)
      VALUES (${other.orgId},
              (SELECT id FROM environments WHERE org_id = ${other.orgId} LIMIT 1),
              'agent', 'failed')
      RETURNING id`
    for (const [o, run] of [
      [org, runA!.id],
      [other, runB!.id],
    ] as const) {
      await h.admin`
        INSERT INTO verdicts (org_id, run_id, workflow, value, summary, created_at)
        VALUES (${o.orgId}, ${run}, ${WORKFLOW}, 'fail', 'the basket was empty at step 4',
                ${SAME_INSTANT})`
    }

    const caller = await callerFor('infrastructure')
    const view = await caller.admin.operations.logs.overview({ hours: 24 })
    const checkout = view.workflows.find((w) => w.workflow === WORKFLOW && w.value === 'fail')
    assert.ok(checkout, 'the failing workflow did not appear')
    assert.equal(checkout.runs, 2)
    assert.equal(checkout.organizations, 2, 'one workflow failing for two tenants is the signal')
    assert.match(checkout.latestSummary ?? '', /basket/)
  })

  /* ---------------------------------------------------------------------
   * The event stream, and the column boundary
   * ------------------------------------------------------------------ */

  test('the event stream returns shape and timing and never a payload value', async () => {
    for (let i = 0; i < 3; i++) {
      await h.admin`
        INSERT INTO events (org_id, idempotency_key, env_id, type, payload,
                            occurred_at, received_at)
        VALUES (${org.orgId}, ${randomUUID()}, ${org.envId}, ${EVENT_TYPE},
                ${h.admin.json({ secretField: CANARY, another: 1 })},
                ${SAME_INSTANT}, ${new Date(SAME_INSTANT.getTime() + 5000)})`
    }

    const caller = await callerFor('infrastructure')
    // Filtered to this run's own type. Unfiltered, the first page is whatever
    // else is in the database and the assertions below would be about somebody
    // else's rows.
    const page = await caller.admin.operations.logs.events({ hours: 24, type: EVENT_TYPE, limit: 50 })
    assert.equal(page.rows.length, 3)

    const serialised = JSON.stringify(page)
    assert.ok(
      !serialised.includes(CANARY),
      'an event payload value reached the response. The column list is the only boundary here: ' +
        'RLS is row level and the operator pool bypasses it.',
    )

    const row = page.rows.find((r) => r.type === EVENT_TYPE)
    assert.ok(row)
    assert.deepEqual(
      row.payloadKeys.slice().sort(),
      ['another', 'secretField'],
      'the key names are what makes the stream diagnostic without being a data leak',
    )
    assert.ok(row.payloadBytes > 0, 'the size says whether anything is in there')
    assert.equal(row.orgSlug, org.slug)
    assert.equal(row.envId, org.envId)
  })

  test('the event type summary reports the lag between engine and control plane', async () => {
    const caller = await callerFor('infrastructure')
    const view = await caller.admin.operations.logs.overview({ hours: 24 })
    const started = view.eventTypes.find((t) => t.type === EVENT_TYPE)
    assert.ok(started, 'the seeded event type is missing from the summary')
    assert.equal(started.lagSeconds, 5, 'occurred_at and received_at were seeded five apart')
    assert.ok(started.events >= 3)
  })

  test('paging the event stream sees every row exactly once', async () => {
    // Ten rows sharing one instant. A cursor on the timestamp alone repeats
    // rows and drops others here, and the page still looks full while it does
    // it, which is why this walks the list rather than checking a count.
    const seeded: string[] = []
    for (let i = 0; i < 10; i++) {
      const [row] = await h.admin<{ id: string }[]>`
        INSERT INTO events (org_id, idempotency_key, env_id, type, payload,
                            occurred_at, received_at)
        VALUES (${other.orgId}, ${randomUUID()}, ${other.envId}, ${PAGING_TYPE},
                ${h.admin.json({})}, ${SAME_INSTANT}, ${SAME_INSTANT})
        RETURNING id`
      seeded.push(row!.id)
    }

    const caller = await callerFor('infrastructure')
    const seen: string[] = []
    let cursor: string | null = null
    for (let guard = 0; guard < 20; guard++) {
      const page = await caller.admin.operations.logs.events({
        hours: 24,
        type: PAGING_TYPE,
        limit: 3,
        cursor,
      })
      seen.push(...page.rows.map((r) => r.id))
      cursor = page.nextCursor
      if (cursor === null) break
    }
    assert.equal(cursor, null, 'the walk did not reach the end of the list')
    assert.equal(seen.length, new Set(seen).size, 'a row came back on two pages')
    assert.deepEqual(seen.slice().sort(), seeded.slice().sort(), 'a row was never returned')
  })

  test('the stream filters by organization, which is what makes it usable in an incident',
    async () => {
      const caller = await callerFor('infrastructure')
      const mine = await caller.admin.operations.logs.events({ hours: 24, orgId: org.orgId, limit: 100 })
      assert.ok(mine.rows.length > 0)
      assert.ok(
        mine.rows.every((r) => r.orgSlug === org.slug),
        'the filter let another tenant through',
      )
    })

  /* ---------------------------------------------------------------------
   * Email
   * ------------------------------------------------------------------ */

  test('canSend answers no when nothing is configured and yes when something is', async () => {
    const blind = await callerFor('support', null)
    const off = await blind.admin.operations.email.status({ hours: 168 })
    assert.equal(off.canSend, false, 'an installation with no mailer must say so')
    assert.equal(off.provider, null)

    const wired = await callerFor('support', new RecordingMailer())
    const on = await wired.admin.operations.email.status({ hours: 168 })
    assert.equal(on.canSend, true)
    assert.equal(on.provider, 'recording')
    assert.equal(
      on.recordingOnly,
      true,
      'a recording mailer looks identical to a working one from every angle except this flag',
    )
  })

  test('a sign-in link is counted by what became of it, and the hash never leaves', async () => {
    const used = `used-${randomUUID().slice(0, 6)}@example.test`
    const live = `live-${randomUUID().slice(0, 6)}@example.test`
    const dead = `dead-${randomUUID().slice(0, 6)}@example.test`
    const now = h.clock.now()
    const rows: [string, Date, Date | null][] = [
      [used, new Date(now.getTime() + 600_000), now],
      [live, new Date(now.getTime() + 600_000), null],
      [dead, new Date(now.getTime() - 600_000), null],
    ]
    for (const [email, expires, consumed] of rows) {
      await h.admin`
        INSERT INTO email_signin_tokens (token_hash, email, expires_at, consumed_at, created_at)
        VALUES (${Buffer.from(`${CANARY}-${email}`)}, ${email}, ${expires}, ${consumed},
                ${SAME_INSTANT})`
    }

    const caller = await callerFor('support', new RecordingMailer())
    const status = await caller.admin.operations.email.status({ hours: 168 })
    assert.ok(status.reach.linksIssued >= 3)
    assert.ok(status.reach.linksUsed >= 1)
    assert.ok(status.reach.linksLive >= 1)
    assert.ok(
      status.reach.linksUnused >= 1,
      'an issued link that expired unused is the only trace a delivery failure leaves here',
    )

    const page = await caller.admin.operations.email.signInLinks({ hours: 168, limit: 100 })
    assert.ok(
      !JSON.stringify(page).includes(CANARY),
      'token_hash reached the response. Name the columns; never SELECT *.',
    )
    const byEmail = new Map(page.rows.map((r) => [r.email, r]))
    assert.equal(byEmail.get(used)?.standing, 'used')
    assert.equal(byEmail.get(live)?.standing, 'live')
    assert.equal(byEmail.get(dead)?.standing, 'unused')
  })

  test('the standing filter returns only that standing', async () => {
    const caller = await callerFor('support', new RecordingMailer())
    for (const standing of ['used', 'live', 'unused'] as const) {
      const page = await caller.admin.operations.email.signInLinks({ hours: 168, standing, limit: 100 })
      assert.ok(page.rows.length > 0, `nothing came back for ${standing}`)
      assert.ok(
        page.rows.every((r) => r.standing === standing),
        `the ${standing} filter returned something else`,
      )
    }
  })

  /* ---------------------------------------------------------------------
   * The gate
   * ------------------------------------------------------------------ */

  test('a role without the permission is refused rather than answered', async () => {
    const caller = await callerFor('analytics')
    await assert.rejects(
      () => caller.admin.operations.logs.overview({ hours: 24 }),
      (err: { message?: string }) => /admin\.logs\.read/.test(err.message ?? ''),
      'analytics holds neither permission and must be told which one it lacks',
    )
    await assert.rejects(
      () => caller.admin.operations.email.status({ hours: 168 }),
      (err: { message?: string }) => /admin\.email\.read/.test(err.message ?? ''),
    )
  })

  test('reading is recorded, because a read of every tenant is an event', async () => {
    const caller = await callerFor('infrastructure')
    const [before] = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM admin_audit_entries WHERE action = 'read.admin.operations.logs.overview'`
    await caller.admin.operations.logs.overview({ hours: 24 })
    const [after] = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM admin_audit_entries WHERE action = 'read.admin.operations.logs.overview'`
    assert.equal(
      Number(after!.n),
      Number(before!.n) + 1,
      'a cross-tenant read that leaves no record is a read nobody can account for',
    )
  })
})

describe('a sign-in link standing', () => {
  const now = new Date('2026-04-02T09:00:00.000Z')

  test('is used the moment it is consumed, whatever the expiry says', () => {
    // Both halves matter. A consumed link past its expiry is still used, and
    // reading the expiry first would report it as a delivery failure.
    assert.equal(
      standingOf({ consumed_at: now, expires_at: new Date(now.getTime() - 1) }, now),
      'used',
    )
  })

  test('is live while it can still be clicked and unused once it cannot', () => {
    assert.equal(
      standingOf({ consumed_at: null, expires_at: new Date(now.getTime() + 1000) }, now),
      'live',
    )
    assert.equal(
      standingOf({ consumed_at: null, expires_at: new Date(now.getTime() - 1000) }, now),
      'unused',
    )
  })

  test('accepts the string a driver returns as readily as a Date', () => {
    // postgres.js hands back a Date, and a JSON round trip hands back a
    // string. Both reach this function, and a comparison against a string
    // would be a silent false rather than a type error.
    assert.equal(
      standingOf({ consumed_at: null, expires_at: new Date(now.getTime() + 1000).toISOString() }, now),
      'live',
    )
  })
})
