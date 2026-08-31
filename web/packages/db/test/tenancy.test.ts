// The cross-tenant suite.
//
// Phase 8.1's exit criterion is that isolation is proven for every table, and
// "every table" is the part that is easy to get wrong: a table added later,
// with no policy, is readable by everyone and nothing complains. So the suite
// does not carry a hand-written list of tables to check. It asks the database
// which tables exist, and fails on any that is not covered.
//
// Every case is written as an attempt by tenant B to reach tenant A's row, run
// on a connection scoped to B. A test that only checked "A can see A's row"
// would pass just as well with the policies removed.

import { after, before, describe, it } from 'node:test'
import { readFile } from 'node:fs/promises'
import assert from 'node:assert/strict'
import { sql } from 'drizzle-orm'
import { createPool } from '../src/client.ts'
import {
  available,
  appUrl,
  connectTimeoutSeconds,
  setup,
  seedTenant,
  dropTenant,
  pgError,
  type Harness,
  type Fixture,
} from './harness.ts'

const hasDatabase = await available()

describe('cross-tenant isolation', { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let h: Harness
  let alice: Fixture
  let bob: Fixture

  before(async () => {
    h = await setup()
    alice = await seedTenant(h.admin, 'alice')
    bob = await seedTenant(h.admin, 'bob')
  })

  after(async () => {
    await dropTenant(h.admin, alice.orgId)
    await dropTenant(h.admin, bob.orgId)
    await h.close()
  })

  // Tables isolated by the plain org_id policy, asked of the database rather
  // than written down here. A table added later with an org_id column and no
  // policy is readable by every tenant and nothing would complain, so the list
  // that drives these loops has to come from what is actually installed, and a
  // separate test proves nothing fell out of it.
  const isolatedByUser = new Map<string, string>([
    ['sessions', 'belongs to a user, not an organization; covered by its own test below'],
  ])

  // relkind r and p. A partitioned table's parent is p, not r, so a filter on
  // r alone would drop it out of this suite entirely the moment any table here
  // is partitioned, and nothing would say so. Nothing is partitioned today;
  // this is here so that the day one is, it stays covered.
  async function withOrgIdColumn(): Promise<string[]> {
    const rows = await h.admin<{ table_name: string }[]>`
      SELECT c.relname AS table_name
      FROM pg_class c
      JOIN pg_namespace n ON n.oid = c.relnamespace
      JOIN pg_attribute a ON a.attrelid = c.oid AND a.attname = 'org_id' AND a.attnum > 0
      WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p') AND NOT c.relispartition
      ORDER BY c.relname`
    return rows.map((r) => r.table_name)
  }

  async function orgScopedTables(): Promise<string[]> {
    const all = await withOrgIdColumn()
    return all.filter((t) => !isolatedByUser.has(t))
  }

  it('every table in the database is classified, so none is silently unprotected', async () => {
    // A table with no org_id needs a reason to exist without one. Naming it
    // here is that reason, and a new table nobody classified fails this test
    // rather than shipping with no policy.
    const deliberatelyGlobal = new Map<string, string>([
      ['users', 'identity is global; visibility is by shared membership'],
      ['organizations', 'isolated by id rather than by org_id'],
      ['oauth_states', 'no tenant exists yet; rows are single-use secrets deleted on use'],
      [
        'email_signin_tokens',
        'no tenant exists yet; a row is reachable only by presenting the hash of the token it was sent in',
      ],
      [
        'device_authorizations',
        'no tenant until somebody approves it; a row is reachable only by the device code ' +
          'or the user code the caller already holds, the same shape as oauth_states',
      ],
      ['schema_migrations', "the schema's own bookkeeping, not tenant data"],
    ])

    // Partitions are excluded because a partition is storage for its parent
    // rather than a table anybody classifies: it holds the parent's rows and
    // reading through the parent applies the parent's policy. They are not
    // unchecked, though. The test below walks every one of them and demands
    // the same isolation the parent has, which is stronger than letting them
    // pass through this classification list.
    const all = await h.admin<{ table_name: string }[]>`
      SELECT c.relname AS table_name
      FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
      WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p') AND NOT c.relispartition
      ORDER BY c.relname`

    const scoped = new Set(await orgScopedTables())
    const unclassified = all
      .map((r) => r.table_name)
      .filter((t) => !scoped.has(t) && !deliberatelyGlobal.has(t) && !isolatedByUser.has(t))

    assert.deepEqual(
      unclassified,
      [],
      `these tables are isolated by nothing this suite knows about:\n  ${unclassified.join('\n  ')}\n` +
        'Give the table an org_id, or record why it does not need one.',
    )
  })

  it('every tenant-scoped table has row-level security enabled and a policy', async () => {
    const tables = await orgScopedTables()
    assert.ok(tables.length >= 13, `expected the full schema, found ${tables.length} tables`)

    for (const table of tables) {
      const [row] = await h.admin<{ relrowsecurity: boolean; relforcerowsecurity: boolean }[]>`
        SELECT relrowsecurity, relforcerowsecurity FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE n.nspname = 'public' AND c.relname = ${table}`
      assert.equal(row?.relrowsecurity, true, `${table} does not have row-level security enabled`)
      assert.equal(row?.relforcerowsecurity, true, `${table} does not force row-level security`)

      const policies = await h.admin<{ polname: string }[]>`
        SELECT polname FROM pg_policy p
        JOIN pg_class c ON c.oid = p.polrelid
        WHERE c.relname = ${table}`
      assert.ok(policies.length > 0, `${table} has row-level security on but no policy, so it denies everything`)
    }
  })

  it('every partition of a tenant-scoped table is isolated in its own right', async () => {
    // Reading through the parent applies the parent's policy and never a
    // partition's, so on the application's path this is belt and braces. It
    // matters for the path where somebody names a partition directly: a
    // maintenance script, an archival job, a person at a psql prompt with the
    // application role. A partition created without this is a copy of the
    // table with no policy on it.
    const parents = await orgScopedTables()

    const partitions = await h.admin<{ name: string; parent: string }[]>`
      SELECT c.relname AS name, p.relname AS parent
      FROM pg_inherits i
      JOIN pg_class c ON c.oid = i.inhrelid
      JOIN pg_class p ON p.oid = i.inhparent
      JOIN pg_namespace n ON n.oid = c.relnamespace
      WHERE n.nspname = 'public' AND p.relname = ANY(${parents})
      ORDER BY c.relname`

    // A negative control on the query itself. Nothing here is partitioned
    // unless events is, and if this suite ever finds zero partitions it is
    // because the query stopped matching, not because the risk went away.
    assert.ok(
      partitions.length > 0,
      'found no partitions at all; either events stopped being partitioned or this query no longer matches',
    )

    for (const { name, parent } of partitions) {
      const [row] = await h.admin<{ relrowsecurity: boolean; relforcerowsecurity: boolean }[]>`
        SELECT relrowsecurity, relforcerowsecurity FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE n.nspname = 'public' AND c.relname = ${name}`
      assert.equal(
        row?.relrowsecurity,
        true,
        `${name}, a partition of ${parent}, does not have row-level security enabled`,
      )
      assert.equal(row?.relforcerowsecurity, true, `${name} does not force row-level security`)

      const policies = await h.admin<{ polname: string }[]>`
        SELECT polname FROM pg_policy p
        JOIN pg_class c ON c.oid = p.polrelid
        WHERE c.relname = ${name}`
      assert.ok(policies.length > 0, `${name} has row-level security on but no policy`)
    }
  })

  it("reads of another tenant's rows return nothing, for every tenant-scoped table", async () => {
    const tables = await orgScopedTables()

    for (const table of tables) {
      // Proves the row is there before proving it is invisible. Without this,
      // a query that returns nothing because the fixture failed to insert
      // would look exactly like isolation working.
      const asOwner = await h.admin<{ n: string }[]>`
        SELECT count(*) AS n FROM ${h.admin(table)} WHERE org_id = ${alice.orgId}`
      assert.ok(Number(asOwner[0]!.n) > 0, `fixture is missing: no ${table} row for alice`)

      const visible = await h.pool.withTenant({ orgId: bob.orgId, userId: bob.userId }, async (db) =>
        db.execute<{ n: string }>(sql`
          SELECT count(*) AS n FROM ${sql.identifier(table)} WHERE org_id = ${alice.orgId}`),
      )
      assert.equal(
        Number(visible[0]!.n),
        0,
        `bob can read ${Number(visible[0]!.n)} of alice's ${table} rows`,
      )

      // And the unqualified read, which is how a forgotten WHERE clause looks.
      const unqualified = await h.pool.withTenant({ orgId: bob.orgId, userId: bob.userId }, async (db) =>
        db.execute<{ org_id: string }>(sql`SELECT org_id FROM ${sql.identifier(table)}`),
      )
      for (const row of unqualified) {
        assert.equal(row.org_id, bob.orgId, `an unqualified select on ${table} returned another tenant's row`)
      }
    }
  })

  it("writes into another tenant's rows are refused, for every tenant-scoped table", async () => {
    // Which verbs the application role holds per table, asked of the database
    // rather than written down. Not every table grants all four: audit_entries
    // is append-only, and so are the billing tables, because a cancelled
    // subscription is a row whose status is canceled and an invoice that was
    // issued stays issued.
    //
    // This used to exclude audit_entries by name and run a bare UPDATE and
    // DELETE against everything else, which meant a table without those grants
    // could only be added to this suite by adding it to the exclusion, where it
    // would then be skipped entirely. Asking the grant table instead asserts
    // BOTH outcomes: where the verb is granted the policy has to be what stops
    // the write, and where it is not the database has to refuse the statement.
    const granted = await h.admin<{ table_name: string; privilege_type: string }[]>`
      SELECT table_name, privilege_type FROM information_schema.role_table_grants
      WHERE grantee = 'antifailure_app' AND table_schema = 'public'
        AND privilege_type IN ('UPDATE', 'DELETE')`
    const holds = (table: string, verb: string): boolean =>
      granted.some((g) => g.table_name === table && g.privilege_type === verb)

    const tables = await orgScopedTables()
    assert.ok(
      tables.some((t) => !holds(t, 'DELETE')),
      'every table grants DELETE, so the append-only half of this test proves nothing',
    )

    // One attempt, and what it must have done. A granted verb has to be stopped
    // by the policy, which shows up as a statement that raises nothing and
    // changes nothing; a withheld verb has to be refused outright.
    async function attempt(table: string, verb: 'UPDATE' | 'DELETE'): Promise<void> {
      const failure = await h.pool
        .withTenant({ orgId: bob.orgId, userId: bob.userId }, async (db) => {
          await db.execute(
            verb === 'UPDATE'
              ? sql`UPDATE ${sql.identifier(table)} SET org_id = ${bob.orgId} WHERE org_id = ${alice.orgId}`
              : sql`DELETE FROM ${sql.identifier(table)} WHERE org_id = ${alice.orgId}`,
          )
        })
        .then(
          () => null,
          (e: unknown) => pgError(e),
        )
      if (holds(table, verb)) {
        // An UPDATE or DELETE that matches no visible row changes nothing
        // rather than raising, so the assertion is on the row still being
        // alice's afterwards.
        assert.equal(failure, null, `${verb} on ${table} raised: ${failure?.message}`)
      } else {
        assert.equal(
          failure?.code,
          '42501',
          `${table} grants no ${verb} but the statement was not refused for insufficient privilege`,
        )
      }
      const [left] = await h.admin<{ n: string }[]>`
        SELECT count(*) AS n FROM ${h.admin(table)} WHERE org_id = ${alice.orgId}`
      assert.ok(Number(left!.n) > 0, `bob's ${verb} reached alice's ${table} rows`)
    }

    for (const table of tables) {
      await attempt(table, 'UPDATE')
      await attempt(table, 'DELETE')
    }
  })

  it('a tenant cannot insert a row belonging to another tenant', async () => {
    const err = await h.pool
      .withTenant({ orgId: bob.orgId, userId: bob.userId }, async (db) => {
        await db.execute(sql`
          INSERT INTO repositories (org_id, full_name) VALUES (${alice.orgId}, 'stolen/repo')`)
      })
      .then(
        () => null,
        (e: unknown) => pgError(e),
      )
    assert.ok(err, 'bob was allowed to create a repository inside another organization')
    assert.equal(
      err.code,
      '42501',
      `expected the write to be refused for insufficient privilege, got ${err.code}: ${err.message}`,
    )
  })

  it('a connection with no tenant set sees nothing rather than everything', async () => {
    // The failure mode this guards is a query that escaped withTenant. It must
    // return an empty result, never the whole table.
    const rows = await h.pool.withoutTenant(async (db) =>
      db.execute<{ n: string }>(sql`SELECT count(*) AS n FROM environments`),
    )
    assert.equal(Number(rows[0]!.n), 0, 'a query with no tenant set read the environments table')
  })

  it('the application role cannot bypass row-level security or become the owner', async () => {
    const [role] = await h.admin<{ rolbypassrls: boolean; rolsuper: boolean }[]>`
      SELECT rolbypassrls, rolsuper FROM pg_roles WHERE rolname = 'antifailure_app'`
    assert.equal(role?.rolbypassrls, false, 'the application role can bypass row-level security')
    assert.equal(role?.rolsuper, false, 'the application role is a superuser')

    const err = await h.pool
      .withTenant({ orgId: bob.orgId }, async (db) => {
        await db.execute(sql`ALTER TABLE environments DISABLE ROW LEVEL SECURITY`)
      })
      .then(
        () => null,
        (e: unknown) => pgError(e),
      )
    assert.ok(err, 'the application role turned the policies off')
    assert.equal(
      err.code,
      '42501',
      `expected ownership to be refused, got ${err.code}: ${err.message}`,
    )
  })

  it('organizations are visible only to a session scoped to them', async () => {
    const rows = await h.pool.withTenant({ orgId: bob.orgId, userId: bob.userId }, async (db) =>
      db.execute<{ slug: string }>(sql`SELECT slug FROM organizations`),
    )
    assert.deepEqual(rows.map((r) => r.slug), [bob.slug])
  })

  it('a tenant cannot enumerate users outside its own membership', async () => {
    const rows = await h.pool.withTenant({ orgId: bob.orgId, userId: bob.userId }, async (db) =>
      db.execute<{ email: string }>(sql`SELECT email FROM users`),
    )
    const emails = rows.map((r) => r.email)
    assert.ok(
      !emails.includes(`${alice.slug}@example.test`),
      'bob can read the email address of a user in another organization',
    )
    assert.ok(emails.includes(`${bob.slug}@example.test`), 'bob cannot read his own user row')
  })

  it('a declared identity does not survive its transaction onto the next borrower', async () => {
    // The whole of the webhook design rests on this one property. The policies
    // in 0013 and 0020 do not key on the tenant, they key on a name the caller
    // declares, so a declared name left behind on a pooled connection is read
    // by whoever borrows it next and it reaches another company's rows. Proven
    // in this repository rather than assumed: `lock_timeout` and
    // `statement_timeout` were found leaking off a migration connection the
    // same night this suite was written, so the failure mode is live here.
    //
    // max: 1 is the point of the test. With a larger pool the second query can
    // land on a connection that never carried the setting, and the assertion
    // passes for the wrong reason.
    const one = createPool({ url: appUrl(), max: 1, connectTimeoutSeconds })
    try {
      await one.withTenant({ orgId: alice.orgId, userId: alice.userId }, async (db) =>
        db.execute(sql`SELECT 1`),
      )
      await one.withGitHubAccount('some-account', async (db) => db.execute(sql`SELECT 1`))
      await one.withStripeCustomer('cus_declared_by_a_delivery', async (db) =>
        db.execute(sql`SELECT 1`),
      )

      // Every setting the pool declares, read out of the pool's own source
      // rather than listed here. A hand-written list goes stale the next time a
      // policy keys on something new, and the new one is exactly the one that
      // would leak unnoticed.
      //
      // pg_settings is NOT the way to ask this and looked like it was. A custom
      // GUC set through set_config is a placeholder and never appears there, so
      // the query returned nothing whether or not anything had leaked: a green
      // check over a subject it never examined.
      const source = await readFile(new URL('../src/client.ts', import.meta.url), 'utf8')
      const declared = [...new Set(source.match(/'antifailure\.[a-z_]+'/g) ?? [])].map((q) =>
        q.slice(1, -1),
      )
      assert.ok(declared.length >= 10, `found only ${declared.length} settings in client.ts`)

      for (const name of declared) {
        const [row] = await one.sql<{ left: string | null }[]>`
          SELECT current_setting(${name}, true) AS left`
        assert.ok(
          row?.left === null || row?.left === '',
          `${name} outlived its transaction on a pooled connection, carrying ${row?.left}`,
        )
      }

      // The timeouts too, for the same reason and because that is the shape the
      // leak actually took here.
      const [timeout] = await one.sql<{ statement_timeout: string }[]>`SHOW statement_timeout`
      assert.equal(
        timeout?.statement_timeout,
        '0',
        'statement_timeout outlived its transaction on a pooled connection',
      )
    } finally {
      await one.close()
    }
  })

  it("an update's new row must satisfy the SELECT policies, not only the WITH CHECK", async () => {
    // Written here because it could not be written where it belongs. The right
    // place is a comment beside `stripe_delivery_moves_plan` in 0020, and a
    // migration that has already been applied cannot be edited: the runner
    // compares a digest and refuses, because databases that ran the old text
    // would never receive the new text.
    //
    // What the next person to edit that policy needs to know: Postgres applies
    // the table's SELECT policies to the NEW row of an UPDATE as well, with no
    // RETURNING clause needed. So on `organizations`, where the delivery has a
    // SELECT policy keyed on the same customer, weakening the UPDATE policy's
    // WITH CHECK removes a real defence and breaks nothing visible, because the
    // read half goes on catching the escape. Establishing that took a table of
    // its own: rewriting the real policy with `WITH CHECK (true)` and watching
    // the escape still fail proves only that something stopped it.
    //
    // It is asserted rather than described because the day it stops being true
    // is a Postgres upgrade, and the only warning would be this going red.
    //
    // The other half of what that policy reaches, recorded here because there is
    // nowhere else and because the fact belongs in the repository rather than in
    // a review. Row-level security cannot restrict a COLUMN, so inside a
    // withStripeCustomer transaction the application role may write every column
    // of the organization the delivery names, including `suspended_at`, the kill
    // switch from 0010. Nothing in the database prevents that. What prevents it
    // is that all five UPDATE statements on `organizations` name their columns
    // explicitly, and the sixth one somebody writes will not inherit that care.
    //
    // Column level UPDATE privileges look like the fix and are not, measured
    // rather than assumed. `GRANT UPDATE (plan, updated_at)` takes twenty api
    // tests red, because the same role runs the GitHub installation upsert,
    // which writes `github_login`, and both kill switch routes, which write
    // `suspended_at`, `suspended_reason` and `suspended_by`. The narrowest grant
    // that keeps every path working is the union of all five statements, and it
    // is green, and it still admits the kill switch: it buys `name`, `slug`,
    // `id` and `created_at` and not the column anybody was worried about. A
    // privilege is granted to a ROLE and a policy admits a ROW, and Postgres has
    // no way to say "this column, but only on the path that policy admitted".
    //
    // What would work is a separate role for the delivery path, reached with SET
    // LOCAL ROLE so it needs no second credential. That is not a grant, it is a
    // migration: a role that is not `antifailure_app` matches none of these
    // policies and sees nothing at all under FORCE ROW LEVEL SECURITY, checked
    // rather than assumed, so every policy a delivery relies on has to be
    // re-targeted with it. Worth doing, too large to bolt onto a review.
    //
    // A BEFORE UPDATE trigger comparing OLD and NEW would also work, and it is
    // the cheaper of the two, because a trigger sees the old row and a WITH
    // CHECK never does. Whichever is chosen, it wants reviewing on its own
    // rather than slipping in: there is not one user trigger anywhere in this
    // schema today, and not one column level grant either, so either would be
    // the first of its kind here.
    await h.admin`CREATE TABLE update_sees_select_policies (id int PRIMARY KEY, v text)`
    try {
      await h.admin`INSERT INTO update_sees_select_policies VALUES (1, 'visible')`
      await h.admin`ALTER TABLE update_sees_select_policies ENABLE ROW LEVEL SECURITY`
      await h.admin`ALTER TABLE update_sees_select_policies FORCE ROW LEVEL SECURITY`
      await h.admin`GRANT SELECT, UPDATE ON update_sees_select_policies TO antifailure_app`
      await h.admin`
        CREATE POLICY reads ON update_sees_select_policies
          FOR SELECT TO antifailure_app USING (v = 'visible')`
      // Deliberately the widest an UPDATE policy can be. Nothing in this policy
      // refuses anything, so whatever refuses the write below is the other half.
      //
      // FOR UPDATE and not FOR ALL, which is the shape `stripe_delivery_moves_plan`
      // has and is load bearing here: FOR ALL is a SELECT policy too, so writing
      // it that way would widen the read half to `true` as well and the defence
      // this test is about would disappear. The first version of this test did
      // exactly that and went red, which is how the dependency was found.
      await h.admin`
        CREATE POLICY writes ON update_sees_select_policies
          FOR UPDATE TO antifailure_app USING (true) WITH CHECK (true)`

      const err = await h.pool
        .withTenant({ orgId: bob.orgId, userId: bob.userId }, async (db) => {
          await db.execute(sql`UPDATE update_sees_select_policies SET v = 'hidden' WHERE id = 1`)
        })
        .then(
          () => null,
          (e: unknown) => pgError(e),
        )
      assert.ok(
        err,
        'an update moved a row out of what the SELECT policy admits, so that defence is gone',
      )
      assert.equal(err.code, '42501', `expected a policy refusal, got ${err.code}: ${err.message}`)

      // The negative control, so this is a property of the SELECT policy rather
      // than of the table. With the read half open, the same write succeeds.
      await h.admin`DROP POLICY reads ON update_sees_select_policies`
      await h.admin`
        CREATE POLICY reads ON update_sees_select_policies
          FOR SELECT TO antifailure_app USING (true)`
      await h.pool.withTenant({ orgId: bob.orgId, userId: bob.userId }, async (db) => {
        await db.execute(sql`UPDATE update_sees_select_policies SET v = 'hidden' WHERE id = 1`)
      })
      const [row] = await h.admin<{ v: string }[]>`
        SELECT v FROM update_sees_select_policies WHERE id = 1`
      assert.equal(row?.v, 'hidden', 'the control write was refused too, so the test proves nothing')
    } finally {
      await h.admin`DROP TABLE IF EXISTS update_sees_select_policies`
    }
  })

  it('a tenant cannot record a delivery in the billing ledger', async () => {
    // billing_events grants the tenant SELECT and UPDATE and deliberately no
    // INSERT policy, because the primary key is the payment provider's own
    // event id: a tenant able to insert could claim the id of an event that has
    // not arrived yet, and the real delivery would then look like a retry and
    // be dropped. Narrowing the verb is what makes that impossible rather than
    // unlikely, and nothing else in the suite would notice it widening.
    const err = await h.pool
      .withTenant({ orgId: bob.orgId, userId: bob.userId }, async (db) => {
        await db.execute(sql`
          INSERT INTO billing_events (
            stripe_event_id, org_id, stripe_customer_id, type, event_created_at)
          VALUES ('evt_claimed_before_it_arrived', ${bob.orgId}, 'cus_bob', 'invoice.paid', now())`)
      })
      .then(
        () => null,
        (e: unknown) => pgError(e),
      )
    assert.ok(err, 'a tenant wrote a row into the billing ledger')
    assert.equal(
      err.code,
      '42501',
      `expected the insert to be refused for insufficient privilege, got ${err.code}: ${err.message}`,
    )
  })

  it('a session is readable only by the user who owns it, or by presenting its token', async () => {
    const token = Buffer.from('a'.repeat(64), 'hex')
    await h.admin`
      INSERT INTO sessions (token_hash, user_id, org_id, expires_at)
      VALUES (${token}, ${alice.userId}, ${alice.orgId}, now() + interval '1 day')`

    // Bob, authenticated, cannot see it.
    const asBob = await h.pool.withTenant({ orgId: bob.orgId, userId: bob.userId }, async (db) =>
      db.execute<{ n: string }>(sql`SELECT count(*) AS n FROM sessions`),
    )
    assert.equal(Number(asBob[0]!.n), 0, "bob can read another user's sessions")

    // An unauthenticated connection cannot list them either. This is the case
    // a policy of "allow when no actor is set" would have opened up.
    const anonymous = await h.pool.withoutTenant(async (db) =>
      db.execute<{ n: string }>(sql`SELECT count(*) AS n FROM sessions`),
    )
    assert.equal(Number(anonymous[0]!.n), 0, 'an unauthenticated connection listed every session')

    // Presenting the hash returns exactly that row.
    const resolved = await h.pool.withoutTenant(
      async (db) => db.execute<{ user_id: string }>(sql`SELECT user_id FROM sessions`),
      { sessionHash: token },
    )
    assert.equal(resolved.length, 1)
    assert.equal(resolved[0]!.user_id, alice.userId)

    await h.admin`DELETE FROM sessions WHERE token_hash = ${token}`
  })
})
