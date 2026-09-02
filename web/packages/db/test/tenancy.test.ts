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
import { readdir, readFile } from 'node:fs/promises'
import { join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
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
      [
        'platform_controls',
        'configuration for the installation rather than data belonging to a tenant, so there ' +
          'is no org_id to key a policy on. Reading is open to the application role on purpose: ' +
          'every request has to be able to learn that the installation is paused. Writing is ' +
          'not granted to the application role at all, only to antifailure_admin, so a tenant ' +
          'route that reached this table raises permission denied rather than writing nothing ' +
          'and reporting success. See migrations/0032.',
      ],
      [
        'admin_notes',
        'an operator\'s words about a customer rather than the customer\'s data; ' +
          'the application role holds no grant on it at all, which the test below proves',
      ],
      [
        'admin_users',
        'an operator is not a tenant. The row is the platform\'s own identity, deliberately ' +
          'unrelated to users, and it is reachable only by declaring the email being signed in ' +
          'as or by holding a live operator session',
      ],
      [
        'admin_sessions',
        'belongs to an operator, not an organization; reachable by presenting the hash of the ' +
          'cookie it was issued as, the same shape as the policy on sessions',
      ],
      [
        'admin_audit_entries',
        'the platform\'s own chain. It carries subject_org_id rather than org_id ON PURPOSE: ' +
          'the row records what an operator did and belongs to the platform, so a column named ' +
          'org_id would claim a tenancy it does not have and would put this table into the ' +
          "loops below, which demand an isolation it is not supposed to have",
      ],
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

  /**
   * The claim the classification above makes, actually tested.
   *
   * Listing admin_notes as "deliberately global" is a sentence in a test file,
   * and a sentence is not an isolation mechanism. What keeps an operator's
   * private note about a customer away from that customer is that the
   * application role holds no grant on the table, so every statement it can
   * construct is refused by the database before any policy is consulted.
   *
   * Asserted as 42501 specifically. A SELECT that merely returns no rows would
   * pass a weaker assertion and would mean something completely different: it
   * would mean the table IS reachable and simply happened to be empty, which
   * is the state this suite exists to distinguish from isolation.
   */
  it('the application role cannot read or write an operator\'s notes', async () => {
    const [note] = await h.admin<{ id: string }[]>`
      INSERT INTO admin_notes (subject_type, subject_id, body, author_label)
      VALUES ('user', ${alice.userId}, 'a note the tenant must never see', 'the fixture')
      RETURNING id`

    // Read, on a connection that has a tenant, which is the strongest position
    // the application is ever in.
    const read = await h.pool
      .withTenant({ orgId: alice.orgId, userId: alice.userId }, async (db) => {
        await db.execute(sql`SELECT body FROM admin_notes`)
      })
      .then(() => null, (e: unknown) => pgError(e))
    assert.equal(
      read?.code,
      '42501',
      'the application role could reach admin_notes; an operator note is not tenant data',
    )

    const write = await h.pool
      .withTenant({ orgId: alice.orgId, userId: alice.userId }, async (db) => {
        await db.execute(sql`DELETE FROM admin_notes WHERE id = ${note!.id}::uuid`)
      })
      .then(() => null, (e: unknown) => pgError(e))
    assert.equal(write?.code, '42501', 'the application role could delete an operator note')

    // Still there, so the refusals above were refusals rather than statements
    // that quietly matched nothing.
    const [left] = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM admin_notes WHERE id = ${note!.id}::uuid`
    assert.equal(Number(left!.n), 1)
    await h.admin`DELETE FROM admin_notes WHERE id = ${note!.id}::uuid`
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

  /**
   * The admin portal's backdoor, and the wall around it.
   *
   * Migration 0023 creates a role that CAN read every tenant, which is a
   * deliberate hole and is the only way to answer a support question without
   * asking the customer to paste their data into a ticket. The property that
   * makes it a bounded hole rather than an unbounded one is that the
   * application cannot climb into it: the request path holds antifailure_app's
   * credential, and reaching antifailure_admin would mean either being a
   * member of that role or opening a second connection with a password this
   * process is not given.
   *
   * SET ROLE is the specific attack. It needs no password and no new
   * connection, so if antifailure_admin were ever granted to antifailure_app,
   * which is one careless GRANT away and would look tidy in a migration, then
   * every handler in the product would be one statement from reading the
   * whole database. Nothing else in this suite would notice, because every
   * other assertion here is about policies, and a role with BYPASSRLS is not
   * subject to policies at all.
   */
  it('the application role cannot become the admin role', async () => {
    const [admin] = await h.admin<{ rolbypassrls: boolean; rolcanlogin: boolean }[]>`
      SELECT rolbypassrls, rolcanlogin FROM pg_roles WHERE rolname = 'antifailure_admin'`
    assert.ok(admin, 'migration 0023 did not create antifailure_admin')
    // If this is ever false the admin portal reads zero rows and every page
    // renders an empty state that looks like a product with no customers.
    assert.equal(admin.rolbypassrls, true, 'the admin role cannot actually bypass row-level security')

    // No membership. This is the grant that must never be written.
    const members = await h.admin<{ member: string }[]>`
      SELECT m.rolname AS member
      FROM pg_auth_members am
      JOIN pg_roles r ON r.oid = am.roleid
      JOIN pg_roles m ON m.oid = am.member
      WHERE r.rolname = 'antifailure_admin'`
    assert.deepEqual(
      members.map((r) => r.member),
      [],
      'something has been granted the admin role; the application must never be able to SET ROLE into it',
    )

    // And the statement itself, refused, rather than only the catalog being
    // the right shape. A catalog assertion proves the grant is absent today;
    // this proves what happens when somebody tries.
    const escalated = await h.pool
      .withTenant({ orgId: bob.orgId, userId: bob.userId }, async (db) => {
        await db.execute(sql`SET ROLE antifailure_admin`)
      })
      .then(() => null, (e: unknown) => pgError(e))
    assert.ok(escalated, 'the application role became the admin role')
    assert.equal(
      escalated.code,
      '42501',
      `expected SET ROLE to be refused, got ${escalated.code}: ${escalated.message}`,
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


// ---------------------------------------------------------------------------
// The convention that stands in for a privilege we cannot express
// ---------------------------------------------------------------------------
//
// Deliberately outside the describe above, and with no database skip. This one
// reads source text rather than rows, so a machine with no Postgres must still
// run it. A suite that skips reads as a suite that passed.

/** Where the reader looks. A statement outside these trees is invisible to it. */
const SOURCE_ROOTS = ['web', 'console', 'ee']
const SOURCE_SKIP = new Set(['node_modules', '.next', 'dist', '.git', 'coverage', '.astro'])

async function typescriptFiles(dir: string): Promise<string[]> {
  const found: string[] = []
  let entries
  try {
    entries = await readdir(dir, { withFileTypes: true })
  } catch {
    return found
  }
  for (const entry of entries) {
    if (SOURCE_SKIP.has(entry.name)) continue
    const full = join(dir, entry.name)
    if (entry.isDirectory()) found.push(...(await typescriptFiles(full)))
    else if (/\.tsx?$/.test(entry.name)) found.push(full)
  }
  return found
}

/**
 * Replaces every `${...}` with one placeholder, counting braces so an object
 * literal inside an interpolation does not end it early.
 *
 * This is what separates the two cases. `SET plan = ${input.plan}` becomes
 * `SET plan = @`, which still names its column. `SET ${assignments}` becomes
 * `SET @`, which names nothing, and that is the shape being refused.
 */
function withoutInterpolations(text: string): string {
  let out = ''
  for (let i = 0; i < text.length; i += 1) {
    if (text[i] === '$' && text[i + 1] === '{') {
      let depth = 1
      let j = i + 2
      while (j < text.length && depth > 0) {
        if (text[j] === '{') depth += 1
        else if (text[j] === '}') depth -= 1
        j += 1
      }
      out += '@'
      i = j - 1
    } else {
      out += text[i]
    }
  }
  return out
}

// An UPDATE naming the table directly, with an optional alias. The alias group
// refuses to match the SET keyword itself, or a statement with no alias would be
// read as one aliased to that keyword and its clause would be missed.
//
// This comment is worded around the statement shape rather than quoting it, and
// that is the gate working rather than a workaround. The first run of this test
// refused the sentence that used to sit here, which quoted the shape to explain
// it. Rewording costs one sentence; excluding this file would have cost the
// ability to catch a test that writes the table unqualified.
const PLAIN_UPDATE = /\bUPDATE\s+(?:ONLY\s+)?organizations\b(?:\s+(?!SET\b)(?:AS\s+)?[a-z_]\w*)?\s+SET\b/gi
// The upsert, which is an UPDATE on this table whose name sits next to INSERT
// rather than next to UPDATE. Missing this shape would miss the GitHub
// installation path, which is one of the five.
const UPSERT_UPDATE =
  /\bINSERT\s+INTO\s+organizations\b[\s\S]{0,600}?\bON\s+CONFLICT\b[\s\S]{0,200}?\bDO\s+UPDATE\s+SET\b/gi
// The query builder takes an object, so what it writes is decided at run time
// and cannot be read here at all. There are none today. One appearing is the
// thing this test exists to notice, so it is refused rather than parsed.
const BUILDER_UPDATE = /\.update\(\s*organizations\b/g

interface OrgUpdate {
  where: string
  columns: string[]
  clause: string
}

describe('every UPDATE on organizations names its columns', () => {
  it('refuses a statement whose SET clause is assembled rather than written', async () => {
    // WHY THIS IS A GATE AND NOT A COMMENT. Row-level security admits a whole
    // ROW, so inside a withStripeCustomer transaction the application role may
    // write every column of the organization a delivery names, `suspended_at`
    // included, which is the kill switch from 0010. See the comment beside the
    // SELECT policy test above for why a column privilege cannot express that
    // and what actually would. Until one of those lands, the only thing
    // standing between a payment provider's callback and the kill switch is
    // that every statement writing this table names the columns it writes.
    // That was a convention holding by care, and the next statement somebody
    // writes does not inherit care.
    //
    // WHAT THIS CANNOT SEE, because a gate that certifies less than its name
    // suggests is worse than none:
    //
    //   - It enforces that columns are NAMED, not WHICH columns. A statement on
    //     the delivery path naming `suspended_at` passes this and still clears
    //     the kill switch. Only the role split or the trigger closes that.
    //   - It reads source text, not the SQL Postgres runs. A statement built at
    //     run time out of strings, or reaching the database by some route other
    //     than a tagged template or the query builder, is invisible to it.
    //   - A statement whose TABLE name is interpolated, the shape
    //     `sql.identifier(table)` used elsewhere in this suite, is invisible for
    //     the same reason.
    //   - It covers `organizations` alone. That is deliberate: this table is
    //     where the row the policy admits carries the kill switch. Every other
    //     table is a larger conversation and a larger false positive surface.
    //   - It cannot tell code from a comment, so prose containing this exact
    //     statement shape would be refused. That direction is chosen: a false
    //     positive costs somebody a rewording, and a false negative costs the
    //     thing the test is for.
    const root = fileURLToPath(new URL('../../../../', import.meta.url))

    const updates: OrgUpdate[] = []
    let sawPlain = false
    let sawUpsert = false

    for (const dir of SOURCE_ROOTS) {
      for (const file of await typescriptFiles(join(root, dir))) {
        const text = await readFile(file, 'utf8')
        const where = (index: number) =>
          `${relative(root, file)}:${text.slice(0, index).split('\n').length}`

        for (const pattern of [PLAIN_UPDATE, UPSERT_UPDATE]) {
          pattern.lastIndex = 0
          let match: RegExpExecArray | null
          while ((match = pattern.exec(text)) !== null) {
            if (pattern === PLAIN_UPDATE) sawPlain = true
            else sawUpsert = true
            const after = text.slice(match.index + match[0].length)
            // The clause ends at the next keyword that cannot be part of it, or
            // at the end of the template literal.
            const end = after.search(/\b(WHERE|RETURNING|FROM)\b|`|;/i)
            const clause = withoutInterpolations(after.slice(0, end < 0 ? 200 : end))
            const columns: string[] = []
            let named = true
            for (const assignment of clause.split(',')) {
              const trimmed = assignment.trim()
              if (!trimmed) continue
              const column = /^"?([a-z_][a-z0-9_]*)"?\s*=/i.exec(trimmed)
              if (column) columns.push(column[1]!)
              else named = false
            }
            if (columns.length === 0) named = false
            if (!named) {
              updates.push({
                where: where(match.index),
                columns,
                clause: clause.trim().replace(/\s+/g, ' ').slice(0, 80),
              })
            }
          }
        }

        BUILDER_UPDATE.lastIndex = 0
        let builder: RegExpExecArray | null
        while ((builder = BUILDER_UPDATE.exec(text)) !== null) {
          updates.push({
            where: where(builder.index),
            columns: [],
            clause: 'the query builder, whose set() takes an object decided at run time',
          })
        }
      }
    }

    // Both readers still match something. Without this the test passes when a
    // regex stops matching, a walker root is renamed, or the extension filter
    // changes, which is a green over a subject it never examined. Asserted as
    // "each shape occurs" rather than as a count, so adding a sixth legitimate
    // statement does not go red.
    assert.ok(sawPlain, 'found no UPDATE on organizations at all; this reader has stopped reading')
    assert.ok(sawUpsert, 'found no upsert on organizations; the ON CONFLICT reader has stopped reading')

    assert.deepEqual(
      updates.map((u) => `${u.where}  ${u.clause}`),
      [],
      'these statements write organizations without naming their columns, so what they write ' +
        'is decided at run time and the kill switch is reachable from any path that reaches them',
    )
  })
})
