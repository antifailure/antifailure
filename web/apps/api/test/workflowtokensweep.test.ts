// The credentials nobody swept, and the four kinds of row the sweep must not
// touch.
//
// THE DEFECT. Every engine session in a GitHub Actions job trades the job's
// workflow identity for a fifteen minute engine token, and `af ci` opened eight
// sessions, so one continuous integration run left eight `oidc` rows in
// engine_tokens. That eight is three now, since 1026d0f2 made the credential
// lazy, and it changes nothing here: the rows still arrive per run, they still
// expire in fifteen minutes, and nothing removed one. On the hosted
// installation three
// days of this repository's own pull requests had put roughly seven hundred
// dead credentials on /cli, which is the only screen in the product that says
// what can act as an organization from outside a browser, and the reader's own
// signed in terminal was near the bottom of them.
//
// WHY THE ROWS BELOW ARE MINTED THROUGH THE REAL EXCHANGE. A test that inserted
// an `oidc` row would prove the sweep removes rows this test knows how to
// write. What has to be true is that it removes the rows exchange.ts actually
// writes: it keys on `kind`, on `revoked_at` and on `expires_at`, and all three
// are decided there. An exchange that started writing a different kind would
// leave the sweep deleting nothing, silently, and a suite built on its own
// inserts would stay green through it.
//
// WHERE THIS SUITE IS DELIBERATELY NOT REALISTIC, and it matters because the
// two clocks in this design are the whole safety argument. The harness clock
// starts in January 2026 and the database's now() is whenever this runs, so
// every row the exchange mints here is already ancient by the database's
// reckoning and the POLICY in 0041 would admit all of them. What the tests
// below prove with exchanged rows is therefore the APPLICATION half: the cutoff
// this process computes. The database half, which is the half no argument
// passed from the application can get past, is proved separately by the two
// tests that write their timestamps against now() rather than against the
// harness clock, and those say so where they stand.

import { test, describe, before, after, beforeEach } from 'node:test'
import assert from 'node:assert/strict'
import { createSign, generateKeyPairSync, randomUUID } from 'node:crypto'
import { sql } from 'drizzle-orm'
import { ACTIONS_ISSUER, CALLBACK_AUDIENCE } from '../src/github/oidc.ts'
import { OIDC_TOKEN_TTL_MS } from '../src/github/exchange.ts'
import { FakeClock } from '../src/clock.ts'
import { CSRF_HEADER } from '../src/auth/session.ts'
import { DEVICE_POLL_INTERVAL_SECONDS } from '../src/auth/device.ts'
import { WORKFLOW_TOKEN_GRACE_MS, sweepExpiredWorkflowTokens } from '../src/tokens.ts'
import {
  available,
  dropOrg,
  seedOrg,
  signInAs,
  startApi,
  type ApiHarness,
  type Org,
} from './harness.ts'

const hasDatabase = await available()

const identityKey = generateKeyPairSync('rsa', { modulusLength: 2048 })
const IDENTITY_KID = 'workflow-token-sweep-key'

function jwks(): string {
  const jwk = identityKey.publicKey.export({ format: 'jwk' }) as Record<string, unknown>
  return JSON.stringify({ keys: [{ ...jwk, kid: IDENTITY_KID, use: 'sig', alg: 'RS256' }] })
}

function base64url(value: string): string {
  return Buffer.from(value, 'utf8').toString('base64url')
}

/** A workflow identity token, signed for real, for one run of one repository. */
function identityToken(repository: string, runId: number, now: Date): string {
  const header = { alg: 'RS256', typ: 'JWT', kid: IDENTITY_KID }
  const seconds = Math.floor(now.getTime() / 1000)
  const payload = {
    iss: ACTIONS_ISSUER,
    aud: CALLBACK_AUDIENCE,
    iat: seconds - 10,
    exp: seconds + 600,
    repository,
    repository_owner: repository.split('/')[0],
    run_id: String(runId),
    run_attempt: '1',
    ref: 'refs/heads/main',
    event_name: 'pull_request',
    job_workflow_ref: `${repository}/.github/workflows/dogfood.yml@refs/heads/main`,
    sha: 'a'.repeat(40),
  }
  const signingInput = `${base64url(JSON.stringify(header))}.${base64url(JSON.stringify(payload))}`
  const signature = createSign('RSA-SHA256').update(signingInput).sign(identityKey.privateKey)
  return `${signingInput}.${signature.toString('base64url')}`
}

/**
 * The Postgres error underneath whatever the query builder wrapped it in.
 *
 * Drizzle reports a failure as "Failed query: <sql>" and hangs the driver's
 * error off cause, so asserting on the outer message would pass for any failure
 * at all, including a typo in this file's own SQL. 42501 is
 * insufficient_privilege, which is what a missing column grant produces.
 */
function sqlState(err: unknown): { code?: string; message: string } {
  let cur: unknown = err
  for (let depth = 0; depth < 8 && cur; depth += 1) {
    const e = cur as { code?: string; message?: string; cause?: unknown }
    if (typeof e.code === 'string' && /^[0-9A-Z]{5}$/.test(e.code)) {
      return { code: e.code, message: e.message ?? '' }
    }
    cur = e.cause
  }
  return { message: err instanceof Error ? err.message : String(err) }
}

/**
 * How many exchanges this file makes for one run id.
 *
 * Eight, because eight is what filled the page: `af ci` opened eight engine
 * sessions and every one of them minted before it knew whether it would send
 * anything. 1026d0f2 fixed that: the credential is obtained on the first
 * request rather than at construction, so a real run now leaves three.
 *
 * This constant does NOT track that, and it should not. What is under test here
 * is a sweep that has to cope with however many credentials one run left, and
 * the case worth keeping is the crowded one: a fixture that followed the engine
 * down to three would quietly stop exercising the grouping and the counting
 * that the crowded case is what made necessary, and a self hosted installation
 * on an older engine still sends eight. Eight is a run's worth as observed, not
 * a claim about what today's engine does.
 */
const SESSIONS_PER_RUN = 8

describe(
  'sweeping the credentials a continuous integration run leaves behind',
  { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' },
  () => {
    let api: ApiHarness
    let org: Org
    let other: Org
    let repository: string
    let bindingId: string
    let nextRunId = 8_100_000

    before(async () => {
      api = await startApi({ actionsJwks: jwks })
      org = await seedOrg(api.admin, 'sweep-tokens')
      other = await seedOrg(api.admin, 'sweep-other')
      repository = `${org.slug}/app`
      await api.admin`
        INSERT INTO github_installations (org_id, installation_id, account_login, account_type)
        VALUES (${org.orgId}, ${installationId()}, ${org.slug}, 'Organization')`
    })

    after(async () => {
      await dropOrg(api.admin, org.orgId)
      await dropOrg(api.admin, other.orgId)
      await api.close()
    })

    beforeEach(async () => {
      // Every test starts from no credentials and a fresh claim of its own, so
      // one test's rows cannot be what makes the next one pass or fail.
      //
      // THE CLAIM IS REMADE HERE AND NOT ONCE IN before(), which is a defect
      // this file had and a mutation found. The revocation test withdraws the
      // claim, because withdrawing it is how a person revokes what it issued,
      // and it used to remake the claim on its own last line. So the two tests
      // after it could only exchange anything if that test had reached its
      // last line, and a mutation that made the revocation test fail early
      // took both of them down with it. A test that fails because an earlier
      // test failed is a test reporting on somebody else's subject.
      await api.admin`
        DELETE FROM engine_tokens WHERE org_id IN (${org.orgId}, ${other.orgId})`
      await api.admin`
        DELETE FROM oidc_repository_bindings WHERE org_id IN (${org.orgId}, ${other.orgId})`
      // The per-repository limiter is a token bucket on the harness clock, and
      // eight exchanges a test would otherwise empty it part way through the
      // file.
      api.clock.advance(60_000)
      const claimed = await call(await cliToken('owner', ['tokens.manage']), 'POST',
        '/v1/oidc/bindings', { repository })
      assert.equal(claimed.status, 201, JSON.stringify(claimed.json))
      const [row] = await api.admin<{ id: string }[]>`
        SELECT id FROM oidc_repository_bindings
        WHERE org_id = ${org.orgId} AND repository = ${repository} AND revoked_at IS NULL`
      bindingId = row!.id
    })

    function installationId(): number {
      return 960_000_000 + Number(BigInt('0x' + randomUUID().slice(0, 8)) % 100_000_000n)
    }

    async function call(
      token: string | null,
      method: string,
      path: string,
      body?: unknown,
    ): Promise<{ status: number; json: Record<string, unknown> }> {
      const headers: Record<string, string> = {}
      if (token) headers.authorization = `Bearer ${token}`
      if (body !== undefined) headers['content-type'] = 'application/json'
      const res = await api.fetch(path, {
        method,
        headers,
        body: body === undefined ? undefined : JSON.stringify(body),
      })
      const text = await res.text()
      let parsed: Record<string, unknown> = {}
      try {
        parsed = JSON.parse(text) as Record<string, unknown>
      } catch {
        // A route answering with something other than JSON is a finding, and
        // the status is asserted on instead.
      }
      return { status: res.status, json: parsed }
    }

    /** A real CLI token, obtained the way `af login` obtains one. */
    async function cliToken(role: 'owner' | 'admin', scopes: string[]): Promise<string> {
      const started = await api.fetch('/auth/device/code', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ clientLabel: 'a test terminal', scopes }),
      })
      assert.equal(started.status, 200)
      const codes = (await started.json()) as { device_code: string; user_code: string }
      const person = await signInAs(api, org, role, `sweep-${role}-${randomUUID().slice(0, 6)}`)
      const approved = await api.fetch('/auth/device/approve', {
        method: 'POST',
        headers: {
          'content-type': 'application/json',
          cookie: person.cookie,
          [CSRF_HEADER]: person.csrfToken,
        },
        body: JSON.stringify({ user_code: codes.user_code }),
      })
      assert.equal(approved.status, 200)
      api.clock.advance(DEVICE_POLL_INTERVAL_SECONDS * 1000 + 1000)
      const granted = await api.fetch('/auth/device/token', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ device_code: codes.device_code }),
      })
      assert.equal(granted.status, 200)
      return ((await granted.json()) as { access_token: string }).access_token
    }

    /** One continuous integration run, through the route a runner calls. */
    async function oneRun(sessions = SESSIONS_PER_RUN): Promise<number> {
      const runId = (nextRunId += 1)
      for (let i = 0; i < sessions; i += 1) {
        const res = await call(null, 'POST', '/v1/auth/github-oidc', {
          token: identityToken(repository, runId, api.clock.now()),
        })
        assert.equal(res.status, 200, JSON.stringify(res.json))
      }
      return runId
    }

    /** How many rows of a kind this organization holds right now. */
    async function count(where: ReturnType<typeof api.admin> | string): Promise<number> {
      const [row] = await api.admin<{ n: number }[]>`
        SELECT count(*)::int AS n FROM engine_tokens
        WHERE org_id = ${org.orgId} AND ${api.admin.unsafe(where as string)}`
      return row!.n
    }

    /** The sweep, run at a moment of this test's choosing. */
    async function sweepAt(now: Date): Promise<number> {
      return sweepExpiredWorkflowTokens(api.pool, new FakeClock(now))
    }

    // -----------------------------------------------------------------------
    // The defect
    // -----------------------------------------------------------------------

    test('every session that exchanges leaves a credential behind', async () => {
      // The fixture, asserted rather than assumed. Every test below deletes
      // rows or refuses to, and a run that quietly minted nothing would make
      // all of them pass over an empty table.
      //
      // One credential per exchange is the property, and it is the engine's
      // session count rather than this that decides how many exchanges a run
      // makes. So this asserts the ratio and not the eight.
      await oneRun()
      assert.equal(
        await count(`kind = 'oidc'`),
        SESSIONS_PER_RUN,
        'the exchange did not mint one credential per exchange, so this suite is not testing ' +
          'the rows the page was full of',
      )
    })

    // -----------------------------------------------------------------------
    // What it removes
    // -----------------------------------------------------------------------

    test('removes a run whose credentials expired more than the grace ago', async () => {
      await oneRun()
      const removed = await sweepAt(
        new Date(api.clock.now().getTime() + OIDC_TOKEN_TTL_MS + WORKFLOW_TOKEN_GRACE_MS + 1000),
      )
      assert.equal(removed, SESSIONS_PER_RUN, `the sweep reported removing ${removed}`)
      assert.equal(await count(`kind = 'oidc'`), 0, 'an expired workflow identity survived')
    })

    test('leaves a run that has only just expired, so today is still readable', async () => {
      // The grace period, which is the difference between a page that still
      // shows this morning's runs and one that forgets them the moment the
      // credential dies. Expired by fifteen minutes and one second; nowhere
      // near a day.
      await oneRun()
      const removed = await sweepAt(new Date(api.clock.now().getTime() + OIDC_TOKEN_TTL_MS + 1000))
      assert.equal(removed, 0, `the sweep removed ${removed} credentials inside the grace period`)
      assert.equal(await count(`kind = 'oidc'`), SESSIONS_PER_RUN, 'a credential inside the grace period was removed')
    })

    // -----------------------------------------------------------------------
    // What it must never remove. Five rows, five reasons.
    // -----------------------------------------------------------------------

    test('never a live credential, however far ahead the cutoff it is given', async () => {
      // THE DATABASE'S CLOCK, not this process's, and this is the test that
      // proves it. The row is written against now() so it is genuinely live by
      // the database's reckoning, and the sweep is then handed a cutoff a
      // thousand years in the future. The application's WHERE clause admits it
      // and the policy in 0041 refuses, which is the property that makes a
      // wrong or hostile cutoff harmless.
      await api.admin`
        INSERT INTO engine_tokens (org_id, name, token_hash, prefix, kind, expires_at, binding_id)
        VALUES (${org.orgId}, ${'a live workflow identity'}, ${Buffer.from(randomUUID())},
                'aft_live', 'oidc', now() + interval '1 hour', ${bindingId})`
      const removed = await sweepAt(new Date('3026-01-01T00:00:00.000Z'))
      assert.equal(removed, 0, `a cutoff in the year 3026 removed ${removed} live credentials`)
      assert.equal(
        await count(`kind = 'oidc' AND prefix = 'aft_live'`),
        1,
        'the sweep deleted a credential that had not expired',
      )
    })

    test('and it does remove one written the same way that really has expired', async () => {
      // The control for the test above. Both rows are inserted here rather
      // than exchanged, both are measured against the database's clock, and
      // the only difference between them is the expiry. Without this, a policy
      // that refused everything would satisfy the assertion above perfectly.
      await api.admin`
        INSERT INTO engine_tokens (org_id, name, token_hash, prefix, kind, expires_at, binding_id)
        VALUES (${org.orgId}, ${'a dead workflow identity'}, ${Buffer.from(randomUUID())},
                'aft_dead', 'oidc', now() - interval '2 days', ${bindingId})`
      const removed = await sweepAt(new Date('3026-01-01T00:00:00.000Z'))
      assert.equal(removed, 1, `the sweep reported removing ${removed}`)
      assert.equal(
        await count(`kind = 'oidc' AND prefix = 'aft_dead'`),
        0,
        'a workflow identity dead for two days survived',
      )
    })

    test('never a revoked one, because the revocation is the record of it', async () => {
      // 0033 withheld DELETE on this table from the operator and said why: a
      // revoked credential records what was allowed to act as an organization
      // and when that stopped. Withdrawing the claim revokes every credential
      // it issued, and those rows are the evidence of the incident that made
      // somebody withdraw it.
      await oneRun()
      const withdrawn = await call(await cliToken('owner', ['tokens.manage']), 'DELETE',
        `/v1/oidc/bindings/${repository}`)
      assert.equal(withdrawn.status, 200, JSON.stringify(withdrawn.json))
      assert.equal(await count(`kind = 'oidc' AND revoked_at IS NOT NULL`), SESSIONS_PER_RUN)

      const removed = await sweepAt(
        new Date(api.clock.now().getTime() + OIDC_TOKEN_TTL_MS + WORKFLOW_TOKEN_GRACE_MS + 1000),
      )
      assert.equal(removed, 0, `the sweep removed ${removed} revoked credentials`)
      assert.equal(
        await count(`kind = 'oidc' AND revoked_at IS NOT NULL`),
        SESSIONS_PER_RUN,
        'a revoked workflow identity was swept away, taking the record of it',
      )

    })

    test("never a person's terminal, even long past its ninety days", async () => {
      // A `cli` row carries a user_id and is the only record that this person's
      // laptop was signed in to this organization at all. It expires like a
      // workflow identity does and it is nothing like one.
      await cliToken('admin', [])
      // Counted rather than fixed at one. Claiming the repository in beforeEach
      // signs a terminal in as well, so the number here is not the property
      // under test; the property is that the sweep does not change it.
      const before = await count(`kind = 'cli'`)
      assert.ok(before >= 1, 'the device grant minted no terminal')
      const removed = await sweepAt(new Date('3026-01-01T00:00:00.000Z'))
      assert.equal(removed, 0, `the sweep removed ${removed} rows`)
      assert.equal(
        await count(`kind = 'cli'`),
        before,
        "a person's signed in terminal was swept away",
      )
    })

    test('never an engine token somebody pasted into a build machine', async () => {
      // It has no expiry at all, so the cutoff can never reach it. Asserted
      // anyway, because "the WHERE clause cannot match it" is a fact about
      // today's statement and this is a fact about the row.
      const minted = await call(await cliToken('owner', ['tokens.manage']), 'POST', '/v1/tokens',
        { name: 'ci' })
      assert.equal(minted.status, 201, JSON.stringify(minted.json))
      const removed = await sweepAt(new Date('3026-01-01T00:00:00.000Z'))
      assert.equal(removed, 0, `the sweep removed ${removed} rows`)
      assert.equal(await count(`kind = 'engine'`), 1, 'a pasted engine token was swept away')
    })

    // -----------------------------------------------------------------------
    // What the sweeper may see at all
    // -----------------------------------------------------------------------

    test('cannot read the credential, the organization, or the repository in the name', async () => {
      // Row level security restricts rows and has no way to restrict a column,
      // so the policy alone would leave this role able to read the hash and the
      // tenant of every expired row. The column GRANT in 0041 is what says it,
      // and this is the assertion that the grant is still narrow. A refusal
      // rather than an empty result: a refusal says so, an empty result looks
      // like an empty table.
      for (const column of ['token_hash', 'org_id', 'prefix', 'name', 'id', '*']) {
        const err = await api.pool
          .withExpirySweeper(async (db) =>
            db.execute(sql`SELECT ${sql.raw(column)} FROM engine_tokens LIMIT 1`),
          )
          .then(
            () => null,
            (e: unknown) => sqlState(e),
          )
        assert.ok(err, `the sweeper read engine_tokens.${column}`)
        assert.equal(
          err.code,
          '42501',
          `expected reading ${column} to be refused for insufficient privilege, ` +
            `got ${err.code}: ${err.message}`,
        )
      }
    })

    test('did not widen what one tenant can reach in another', async () => {
      // The reason this is a role and not a policy on antifailure_app.
      // Permissive policies are OR'd, so a policy admitting expired rows with
      // no tenant named would widen every other policy on this table for every
      // ordinary request. If that ever happens, it fails here rather than
      // looking like the sweep starting to work.
      await oneRun()
      const seen = await api.pool.withTenant({ orgId: other.orgId }, async (db) =>
        db.execute<{ n: string }>(sql`
          SELECT count(*) AS n FROM engine_tokens WHERE expires_at IS NOT NULL`),
      )
      assert.equal(Number(seen[0]!.n), 0, "a tenant read another organization's token rows")

      const deleted = await api.pool.withTenant({ orgId: other.orgId }, async (db) =>
        db.execute<{ n: string }>(sql`
          WITH gone AS (
            DELETE FROM engine_tokens WHERE kind = 'oidc' RETURNING 1
          ) SELECT count(*) AS n FROM gone`),
      )
      assert.equal(Number(deleted[0]!.n), 0, "a tenant deleted another organization's credentials")
      assert.equal(await count(`kind = 'oidc'`), SESSIONS_PER_RUN)
    })

    test('the old path still deletes nothing, which is the defect being fixed', async () => {
      // Kept as a live assertion rather than a comment. If some later change
      // makes withoutTenant able to delete these rows, that is a policy
      // admitting an unauthenticated connection to the credentials table, and
      // it should fail here rather than look like the sweeper starting to work.
      await oneRun()
      const deleted = await api.pool.withoutTenant(async (db) =>
        db.execute<{ n: string }>(sql`
          WITH gone AS (
            DELETE FROM engine_tokens WHERE expires_at <= now() RETURNING 1
          ) SELECT count(*) AS n FROM gone`),
      )
      assert.equal(Number(deleted[0]!.n), 0, 'a connection with no tenant deleted token rows')
    })

    test('and the console list is what actually changes, which is the point', async () => {
      // DEFINED, WIRED, THEN EFFECTIVE. Every assertion above this one is about
      // rows in a table. This one is about the thing a person opens: the same
      // procedure /cli calls, through the session cookie it calls it with,
      // before and after. A sweep that removed rows the console read from
      // somewhere else would satisfy all of them and change nothing on the
      // screen.
      await oneRun()
      const person = await signInAs(api, org, 'owner', `sweep-list-${randomUUID().slice(0, 6)}`)
      const list = async (): Promise<{ kind: string; name: string }[]> => {
        const res = await api.fetch('/trpc/tokens.list', { headers: { cookie: person.cookie } })
        assert.equal(res.status, 200)
        const body = (await res.json()) as { result: { data: { kind: string; name: string }[] } }
        return body.result.data
      }

      const before = await list()
      // The exact field names /cli decodes, pinned here because nothing else
      // holds the two together. console/lib/tokens.ts declares TokenRow by
      // hand, the page reads `kind`, `expires_at` and `revoked_at` to decide
      // what is live, and a rename on either side would compile on both: the
      // page would read undefined, call every credential live, and put the
      // whole seven hundred back at the top of the screen rather than at the
      // bottom of it.
      assert.deepEqual(
        Object.keys(before[0] as Record<string, unknown>).sort(),
        [
          'created_at',
          'expires_at',
          'id',
          'kind',
          'last_used_at',
          'name',
          'prefix',
          'revoked_at',
        ],
        'tokens.list no longer answers with the fields console/lib/tokens.ts decodes',
      )
      assert.equal(
        before.filter((t) => t.kind === 'oidc').length,
        SESSIONS_PER_RUN,
        'the console list does not show the credentials the run left',
      )
      const terminals = before.filter((t) => t.kind === 'cli').length
      assert.ok(terminals >= 1, 'the console list shows no signed in terminal to keep')

      await sweepAt(
        new Date(api.clock.now().getTime() + OIDC_TOKEN_TTL_MS + WORKFLOW_TOKEN_GRACE_MS + 1000),
      )

      const after = await list()
      assert.equal(
        after.filter((t) => t.kind === 'oidc').length,
        0,
        'the console list still shows the expired workflow identities after the sweep',
      )
      assert.equal(
        after.filter((t) => t.kind === 'cli').length,
        terminals,
        'the sweep took a signed in terminal off the console list',
      )
    })

    test('reverts the role at the end of the transaction', async () => {
      // SET LOCAL, for the same reason every setting in client.ts is local: a
      // pooled connection returned still acting as the sweeper is read by
      // whoever borrows it next, and on this table that connection would then
      // be unable to see the credential it was asked to authenticate.
      await sweepAt(api.clock.now())
      const rows = await api.pool.withTenant({ orgId: org.orgId }, async (db) =>
        db.execute<{ who: string }>(sql`SELECT current_user AS who`),
      )
      assert.equal(rows[0]!.who, 'antifailure_app')
    })
  },
)
