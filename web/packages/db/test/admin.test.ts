// The operator boundary, and the four ways it could quietly stop being one.
//
// 0030 adds the only policies in this schema that read ACROSS tenants. Every
// other suite in this directory proves that a tenant cannot reach another
// tenant's rows; this one proves that the escape hatch built for the operator
// portal is not reachable by anybody else, and that it is genuinely reachable
// by an operator, because a boundary that denies everybody is not a boundary,
// it is an outage nobody has noticed yet.
//
// The four failures worth naming, because each one would look like working
// software:
//
//   1. A scope forgets to clear antifailure.admin_session_hash, so a webhook
//      or a sweeper inherits cross-tenant reach. Silent, and it would only
//      appear as "the numbers on this page are wrong" long after the fact.
//   2. The policy predicate degrades to an assertion, so setting the GUC is
//      enough. Every test asserting "an operator can read" still passes.
//   3. An expired, revoked or suspended operator keeps reading. The rows come
//      back, so nothing looks broken.
//   4. The root operator can be deleted, demoted or suspended by another
//      administrator, so the permanent account is not permanent.

import { test, describe, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { createHash, randomBytes, randomUUID } from 'node:crypto'
import { readFile } from 'node:fs/promises'
import { sql } from 'drizzle-orm'
import { available, setup, seedTenant, type Harness, type Fixture } from './harness.ts'

const hasDb = await available()

describe('the operator boundary', { skip: hasDb ? false : 'no database' }, () => {
  let h: Harness
  let alice: Fixture
  let bob: Fixture
  /** A live operator, and the raw token whose hash the scope declares. */
  let operatorToken: Buffer
  let operatorId: string

  /** Creates an operator and a session for them, through the owner connection
   *  so the fixture does not depend on the policies it is used to test. */
  async function seedOperator(
    label: string,
    opts: { role?: string; isRoot?: boolean; expired?: boolean; revoked?: boolean; suspended?: boolean } = {},
  ): Promise<{ id: string; token: Buffer; email: string }> {
    const email = `${label}-${randomUUID().slice(0, 8)}@example.test`
    const [row] = await h.admin<{ id: string }[]>`
      INSERT INTO admin_users (email, name, role, is_root, suspended_at)
      VALUES (${email}, ${label}, ${opts.role ?? 'super_admin'}, ${opts.isRoot ?? false},
              ${opts.suspended ? new Date().toISOString() : null})
      RETURNING id`
    const id = row!.id
    const token = randomBytes(32)
    const hash = createHash('sha256').update(token).digest()
    const expires = opts.expired
      ? new Date(Date.now() - 60_000).toISOString()
      : new Date(Date.now() + 3_600_000).toISOString()
    await h.admin`
      INSERT INTO admin_sessions (token_hash, admin_user_id, expires_at, revoked_at)
      VALUES (${hash}, ${id}, ${expires}, ${opts.revoked ? new Date().toISOString() : null})`
    return { id, token: hash, email }
  }

  before(async () => {
    h = await setup()
    alice = await seedTenant(h.admin, 'alice')
    bob = await seedTenant(h.admin, 'bob')
    const op = await seedOperator('root-operator', { role: 'owner' })
    operatorToken = op.token
    operatorId = op.id
  })

  after(async () => {
    await h?.close()
  })

  // -------------------------------------------------------------------------
  // 1. The widening cannot leak out of its own scope
  // -------------------------------------------------------------------------

  describe('the admin settings do not survive into another scope', () => {
    // What the runtime checks below can and cannot prove, stated plainly
    // because getting this wrong is how a suite ends up guarding nothing.
    //
    // They prove that entering any other scope leaves no operator session on
    // the connection. They do NOT prove that each scope's explicit
    // `'antifailure.admin_session_hash': ''` line is what does it: every
    // setting here is written with set_config(..., true), which is
    // transaction-local, so a scope that omitted the line entirely would still
    // start clean and these assertions would still pass. That was verified by
    // deleting the line from withSweeper and watching all 25 tests stay green.
    //
    // So the explicit clears are checked at the SOURCE level instead, by the
    // test at the end of this block. That is the gate that goes red when a
    // scope added later forgets one, and it is the reason client.ts writes
    // them out longhand rather than relying on transaction locality: the
    // invariant is meant to be checkable by reading the function.
    //
    // Every scope is entered by name rather than by iterating the object,
    // because each takes different arguments and a loop would either skip the
    // ones that need a value or pass a wrong one and prove nothing.
    const entered: Array<[string, (p: Harness['pool'], read: ReadFn) => Promise<Row>]> = [
      ['withTenant', (p, read) => p.withTenant({ orgId: aliceOrg() }, read)],
      ['withoutTenant', (p, read) => p.withoutTenant(read)],
      ['withGitHubAccount', (p, read) => p.withGitHubAccount('some-account', read)],
      ['withStripeCustomer', (p, read) => p.withStripeCustomer('cus_x', read)],
      [
        'withGitHubDelivery',
        (p, read) => p.withGitHubDelivery({ deliveryId: 'delivery-1' }, read),
      ],
      [
        'withPullRequestCallback',
        (p, read) => p.withPullRequestCallback(Buffer.from('abcd', 'hex'), read),
      ],
      ['withSweeper', (p, read) => p.withSweeper(read)],
    ]

    for (const [name, enter] of entered) {
      test(`${name} carries no operator session`, async () => {
        const row = await enter(h.pool, readAdminSettings)
        assert.equal(
          row.session_hash,
          null,
          `${name} left an operator session hash set, so it can read across tenants`,
        )
        assert.ok(
          row.email === null || row.email === '',
          `${name} left the operator sign-in email set, carrying ${row.email}`,
        )
        assert.equal(
          row.who,
          null,
          `${name} resolves to an operator, so its policies read every tenant`,
        )
      })
    }

    test('every scope in the pool clears both admin settings, checked in the source', async () => {
      // Read out of client.ts rather than listed here, for the reason the
      // sibling test in tenancy.test.ts gives: a hand-written list goes stale
      // the next time somebody adds a scope, and the new scope is exactly the
      // one that would carry cross-tenant reach unnoticed.
      const source = await readFile(new URL('../src/client.ts', import.meta.url), 'utf8')
      const scopes = source.match(/return scoped\(/g)?.length ?? 0
      assert.ok(scopes >= 8, `found only ${scopes} scopes in client.ts, so this is not reading it`)

      for (const setting of ['antifailure.admin_session_hash', 'antifailure.admin_email']) {
        const mentions = source.match(new RegExp(`'${setting.replace('.', '\\.')}':`, 'g'))?.length ?? 0
        assert.equal(
          mentions,
          scopes,
          `${scopes} scopes in client.ts but ${setting} is set in ${mentions} of them. ` +
            'A scope that does not name it inherits cross-tenant reach the day somebody ' +
            'changes how these settings are applied.',
        )
      }
    })

    test('the admin scope itself does resolve, so the checks above are not vacuous', async () => {
      // Without this every assertion above passes on a boundary that is simply
      // broken for everybody, which is the failure mode of every test that
      // only ever asserts a denial.
      const row = await h.pool.withPlatformAdmin(operatorToken, readAdminSettings)
      assert.equal(row.who, operatorId, 'a live operator does not resolve inside its own scope')
    })
  })

  // -------------------------------------------------------------------------
  // 2. The predicate is a credential, not a claim
  // -------------------------------------------------------------------------

  describe('a session hash that names no row grants nothing', () => {
    test('a guessed hash reads no organizations', async () => {
      // The failure this guards is a policy written as `current_admin() IS NOT
      // NULL` over a function that only reads the setting. Under that policy
      // this test returns every organization on the instance, and every test
      // asserting "an operator can read" still passes.
      const guess = createHash('sha256').update('not a real token').digest()
      const rows = await h.pool.withPlatformAdmin(guess, async (db) =>
        db.execute<{ n: string }>(sql`SELECT count(*)::text AS n FROM organizations`),
      )
      assert.equal(rows[0]!.n, '0', 'a guessed operator session read organizations')
    })

    test('an expired session grants nothing', async () => {
      const op = await seedOperator('expired', { expired: true })
      const rows = await h.pool.withPlatformAdmin(op.token, async (db) =>
        db.execute<{ n: string }>(sql`SELECT count(*)::text AS n FROM organizations`),
      )
      assert.equal(rows[0]!.n, '0', 'an expired operator session still read organizations')
    })

    test('a revoked session grants nothing', async () => {
      const op = await seedOperator('revoked', { revoked: true })
      const rows = await h.pool.withPlatformAdmin(op.token, async (db) =>
        db.execute<{ n: string }>(sql`SELECT count(*)::text AS n FROM organizations`),
      )
      assert.equal(rows[0]!.n, '0', 'a revoked operator session still read organizations')
    })

    test('a suspended operator stops reading on the next statement, not the next sign-in', async () => {
      // Suspending somebody who is signed in is exactly the case where the
      // delay matters, so current_admin_user() joins admin_users and checks it
      // rather than trusting that the session was ended too.
      const op = await seedOperator('suspended', { suspended: true })
      const rows = await h.pool.withPlatformAdmin(op.token, async (db) =>
        db.execute<{ n: string }>(sql`SELECT count(*)::text AS n FROM organizations`),
      )
      assert.equal(rows[0]!.n, '0', "a suspended operator's live session still read organizations")
    })
  })

  // -------------------------------------------------------------------------
  // 3. A live operator genuinely reads across tenants
  // -------------------------------------------------------------------------

  test('a live operator reads both tenants, which no tenant scope can do', async () => {
    const slugs = await h.pool.withPlatformAdmin(operatorToken, async (db) =>
      db.execute<{ slug: string }>(
        sql`SELECT slug FROM organizations WHERE id IN (${alice.orgId}::uuid, ${bob.orgId}::uuid)`,
      ),
    )
    const found = slugs.map((r) => r.slug).sort()
    assert.deepEqual(
      found,
      [alice.slug, bob.slug].sort(),
      'the operator scope cannot see both tenants, so the portal has nothing to show',
    )

    // The contrast that makes the line above mean something: the same query on
    // a tenant scope sees one row, because that is what every other policy in
    // this schema enforces.
    const asTenant = await h.pool.withTenant({ orgId: alice.orgId }, async (db) =>
      db.execute<{ slug: string }>(
        sql`SELECT slug FROM organizations WHERE id IN (${alice.orgId}::uuid, ${bob.orgId}::uuid)`,
      ),
    )
    assert.deepEqual(asTenant.map((r) => r.slug), [alice.slug])
  })

  test('an operator reads runs and environments across tenants', async () => {
    const rows = await h.pool.withPlatformAdmin(operatorToken, async (db) =>
      db.execute<{ n: string }>(sql`
        SELECT count(DISTINCT org_id)::text AS n FROM environments
        WHERE org_id IN (${alice.orgId}::uuid, ${bob.orgId}::uuid)`),
    )
    assert.equal(rows[0]!.n, '2', 'the operator scope cannot see both tenants environments')
  })

  // -------------------------------------------------------------------------
  // 4. The root operator invariant
  //
  // Four refusals. Each was watched failing before the trigger in 0030
  // existed: with the trigger dropped, every one of these four statements
  // succeeds and this block goes green while the invariant is gone.
  // -------------------------------------------------------------------------

  describe('the root operator is permanent', () => {
    let rootId: string

    before(async () => {
      const [row] = await h.admin<{ id: string }[]>`
        INSERT INTO admin_users (email, name, role, is_root)
        VALUES (${`root-${randomUUID().slice(0, 8)}@example.test`}, 'Root', 'owner', true)
        RETURNING id`
      rootId = row!.id
    })

    after(async () => {
      // Deleting it needs the trigger out of the way, which is also the
      // cleanest proof that the trigger is what refuses.
      await h.admin`ALTER TABLE admin_users DISABLE TRIGGER admin_root_is_permanent_del`
      await h.admin`DELETE FROM admin_users WHERE id = ${rootId}::uuid`
      await h.admin`ALTER TABLE admin_users ENABLE TRIGGER admin_root_is_permanent_del`
    })

    test('cannot be deleted', async () => {
      await assert.rejects(
        () => h.admin`DELETE FROM admin_users WHERE id = ${rootId}::uuid`,
        /cannot be deleted/,
      )
      const [still] = await h.admin<{ n: string }[]>`
        SELECT count(*)::text AS n FROM admin_users WHERE id = ${rootId}::uuid`
      assert.equal(still!.n, '1', 'the root operator row is gone')
    })

    test('cannot be demoted', async () => {
      await assert.rejects(
        () => h.admin`UPDATE admin_users SET role = 'read_only' WHERE id = ${rootId}::uuid`,
        /cannot be demoted/,
      )
      const [row] = await h.admin<{ role: string }[]>`
        SELECT role FROM admin_users WHERE id = ${rootId}::uuid`
      assert.equal(row!.role, 'owner')
    })

    test('cannot be suspended', async () => {
      await assert.rejects(
        () => h.admin`UPDATE admin_users SET suspended_at = now() WHERE id = ${rootId}::uuid`,
        /cannot be suspended/,
      )
      const [row] = await h.admin<{ suspended_at: Date | null }[]>`
        SELECT suspended_at FROM admin_users WHERE id = ${rootId}::uuid`
      assert.equal(row!.suspended_at, null)
    })

    test('cannot stop being the root operator', async () => {
      await assert.rejects(
        () => h.admin`UPDATE admin_users SET is_root = false WHERE id = ${rootId}::uuid`,
        /cannot stop being the root operator/,
      )
    })

    test('cannot be joined by a second root operator', async () => {
      // Two permanent operators is the same hole as none: whichever one is
      // hostile cannot be removed by the other.
      const [other] = await h.admin<{ id: string }[]>`
        INSERT INTO admin_users (email, name, role)
        VALUES (${`other-${randomUUID().slice(0, 8)}@example.test`}, 'Other', 'super_admin')
        RETURNING id`
      await assert.rejects(
        () => h.admin`UPDATE admin_users SET is_root = true WHERE id = ${other!.id}::uuid`,
        /cannot be granted later/,
      )
      await h.admin`DELETE FROM admin_users WHERE id = ${other!.id}::uuid`
    })
  })

  // -------------------------------------------------------------------------
  // 5. What an operator may write, and what the database refuses regardless
  // -------------------------------------------------------------------------

  describe('an operator may change an organization without rewriting it', () => {
    test('may suspend and resume', async () => {
      await h.pool.withPlatformAdmin(operatorToken, async (db) => {
        await db.execute(sql`
          UPDATE organizations SET suspended_at = now(), suspended_reason = 'incident'
          WHERE id = ${bob.orgId}::uuid`)
      })
      // Read back on the TENANT's own scope, so the assertion proves the row
      // really changed rather than that the operator can see its own write.
      const rows = await h.pool.withTenant({ orgId: bob.orgId }, async (db) =>
        db.execute<{ reason: string | null }>(
          sql`SELECT suspended_reason AS reason FROM organizations WHERE id = ${bob.orgId}::uuid`,
        ),
      )
      assert.equal(rows[0]!.reason, 'incident', 'the suspension did not reach the row')

      await h.pool.withPlatformAdmin(operatorToken, async (db) => {
        await db.execute(sql`
          UPDATE organizations SET suspended_at = NULL, suspended_reason = NULL
          WHERE id = ${bob.orgId}::uuid`)
      })
    })

    test('may change the plan, which is what a quota is derived from', async () => {
      await h.pool.withPlatformAdmin(operatorToken, async (db) => {
        await db.execute(sql`UPDATE organizations SET plan = 'team' WHERE id = ${bob.orgId}::uuid`)
      })
      const rows = await h.pool.withTenant({ orgId: bob.orgId }, async (db) =>
        db.execute<{ plan: string }>(
          sql`SELECT plan FROM organizations WHERE id = ${bob.orgId}::uuid`,
        ),
      )
      assert.equal(rows[0]!.plan, 'team')
    })

    test('may not rewrite the slug a licence is issued against', async () => {
      // RLS is row level and cannot restrict a column, and a GRANT cannot help
      // because the operator path and the tenant path are the same database
      // role. So this is a trigger, and this test is what proves the trigger
      // rather than the application is what refuses.
      await assert.rejects(
        () =>
          h.pool.withPlatformAdmin(operatorToken, async (db) => {
            await db.execute(sql`UPDATE organizations SET slug = 'seized' WHERE id = ${bob.orgId}::uuid`)
          }),
        (err: unknown) => /not its identity/.test(reasonFor(err)),
        'the slug was not refused by the trigger',
      )
      const rows = await h.pool.withTenant({ orgId: bob.orgId }, async (db) =>
        db.execute<{ slug: string }>(
          sql`SELECT slug FROM organizations WHERE id = ${bob.orgId}::uuid`,
        ),
      )
      assert.equal(rows[0]!.slug, bob.slug, "the organization's slug was rewritten")
    })

    test('a tenant changing its own name is untouched by that trigger', async () => {
      // The trigger only constrains the operator path. Proving it does not
      // constrain the tenant path matters as much as the refusal above: a
      // guard that also breaks ordinary use gets removed, not fixed.
      await h.pool.withTenant({ orgId: bob.orgId }, async (db) => {
        await db.execute(sql`UPDATE organizations SET name = 'Renamed' WHERE id = ${bob.orgId}::uuid`)
      })
      const rows = await h.pool.withTenant({ orgId: bob.orgId }, async (db) =>
        db.execute<{ name: string }>(
          sql`SELECT name FROM organizations WHERE id = ${bob.orgId}::uuid`,
        ),
      )
      assert.equal(rows[0]!.name, 'Renamed')
    })
  })

  // -------------------------------------------------------------------------
  // 6. The operator audit chain is append-only in the database
  // -------------------------------------------------------------------------

  describe('the operator audit chain', () => {
    test('cannot be updated or deleted by the application role', async () => {
      // The same guarantee 0002 gives audit_entries, and it is a withheld
      // GRANT rather than a policy on purpose: a withheld grant RAISES, while
      // a policy that matches nothing reports success on a statement that
      // changed no rows.
      await h.pool.withPlatformAdmin(operatorToken, async (db) => {
        await db.execute(sql`
          INSERT INTO admin_audit_entries
            (actor_label, action, target_type, origin, entry_hash)
          VALUES ('root', 'admin.signed_in', 'admin_user', 'admin', 'deadbeef')`)
      })

      await assert.rejects(
        () =>
          h.pool.withPlatformAdmin(operatorToken, async (db) => {
            await db.execute(sql`UPDATE admin_audit_entries SET action = 'rewritten'`)
          }),
        (err: unknown) => /permission denied/i.test(reasonFor(err)),
        'an operator could rewrite the operator audit log',
      )
      await assert.rejects(
        () =>
          h.pool.withPlatformAdmin(operatorToken, async (db) => {
            await db.execute(sql`DELETE FROM admin_audit_entries`)
          }),
        (err: unknown) => /permission denied/i.test(reasonFor(err)),
        'an operator could delete from the operator audit log',
      )
    })

    test('is not readable by a tenant', async () => {
      const rows = await h.pool.withTenant({ orgId: alice.orgId }, async (db) =>
        db.execute<{ n: string }>(sql`SELECT count(*)::text AS n FROM admin_audit_entries`),
      )
      assert.equal(rows[0]!.n, '0', "a tenant read the platform's own audit chain")
    })
  })

  function aliceOrg(): string {
    return alice.orgId
  }
})

/** What Postgres actually said.
 *
 * drizzle wraps a failed statement in an Error whose message is "Failed query:
 * <the SQL>" and hangs the driver's error off `cause`. Asserting on the
 * wrapper's message therefore matches the SQL rather than the refusal, and a
 * test written that way passes whether the statement was refused for the
 * reason under test or for any other reason at all. */
function reasonFor(err: unknown): string {
  const cause = (err as { cause?: unknown }).cause
  const inner = cause instanceof Error ? cause.message : ''
  const outer = err instanceof Error ? err.message : String(err)
  return `${inner}\n${outer}`
}

// A type alias rather than an interface, and the difference is load bearing
// here rather than stylistic. db.execute<T> constrains T to Record<string,
// unknown>, and TypeScript gives an object TYPE an implicit index signature
// while an interface gets none, so the interface form does not satisfy the
// constraint. `node --test` strips types without checking them, so this failed
// tsc while every test still ran green.
type Row = {
  session_hash: string | null
  email: string | null
  who: string | null
}

type ReadFn = (db: Parameters<Parameters<Harness['pool']['withoutTenant']>[0]>[0]) => Promise<Row>

/** What the connection is currently carrying, asked of the database rather
 *  than of the pool, because the pool is the thing under test. */
const readAdminSettings: ReadFn = async (db) => {
  const rows = await db.execute<Row>(sql`
    SELECT encode(current_admin_session_hash(), 'hex') AS session_hash,
           current_setting('antifailure.admin_email', true) AS email,
           current_admin_user()::text AS who`)
  return rows[0]!
}
