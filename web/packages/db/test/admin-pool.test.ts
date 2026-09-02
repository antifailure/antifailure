// The operator pool, tested against a real Postgres because the property under
// test is one only Postgres can enforce.
//
// WHAT THIS SUITE IS ACTUALLY ASSERTING. Not "createAdminPool returns an
// object". Three things that a mock cannot tell you:
//
//   a connection whose role holds BYPASSRLS reads across tenants, and the
//   application's own connection, on the same database and the same instant,
//   reads exactly one,
//
//   a connection whose role LACKS the attribute is refused loudly instead of
//   returning zero rows, because zero rows is the failure that looks like a
//   working product with no customers on it,
//
//   and BYPASSRLS is not something the application role can pick up by being
//   granted membership, which is the difference between a credential and a
//   claim and the reason this is a second pool.
//
// WHY THE ROLE IS STILL TOUCHED HERE, now that 0023 creates it. admin-ops's
// migration has landed on main, so antifailure_admin and its BYPASSRLS exist
// after a plain migrate and this suite no longer stands in for them. What it
// still does is give the role LOGIN and a password, because 0023 deliberately
// creates it NOLOGIN so that a self-hosted installation supplies its own
// credential rather than inheriting one written down in a public repository.
// A test needs to connect as it, so the test is the installation here.
//
// The grants below are 0023's and 0030's, restated. They are asserted rather
// than assumed by the fresh-migration check in the report, and if this block
// and the migrations ever disagree it is the migrations that are right.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { sql } from 'drizzle-orm'
import postgres from 'postgres'
import { createAdminPool, type AdminPool } from '../src/admin-pool.ts'
import { appendAdminAudit } from '../src/admin-audit.ts'
import {
  adminUrl,
  appUrl,
  available,
  connectTimeoutSeconds,
  seedTenant,
  setup,
  type Fixture,
  type Harness,
} from './harness.ts'

const hasDatabase = await available()

const OPERATOR_PASSWORD = 'operator-test-password'

/** The operator connection string: a DIFFERENT role from appUrl(). */
function operatorUrl(base = adminUrl): string {
  const u = new URL(base)
  u.username = 'antifailure_admin'
  u.password = OPERATOR_PASSWORD
  return u.toString()
}

/**
 * What 0023 already did, minus the one thing a test has to supply.
 *
 * 0023 creates antifailure_admin and 0030 grants on the chains, so a migrated
 * database needs nothing from this function except a way to CONNECT as the
 * role: 0023 deliberately creates it NOLOGIN so a self-hosted installation
 * supplies its own credential rather than inheriting one written down in a
 * public repository. The suite is that installation.
 *
 * The grants are restated rather than assumed, so this suite still runs against
 * a database built before 0030 landed. If this block and the migrations ever
 * disagree, the migrations are right: the report checks the real privileges by
 * asking has_table_privilege rather than by reading either.
 */
async function createOperatorRole(admin: postgres.Sql): Promise<void> {
  await admin.unsafe(`
    DO $$
    BEGIN
      IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'antifailure_admin') THEN
        CREATE ROLE antifailure_admin NOLOGIN BYPASSRLS;
      ELSE
        ALTER ROLE antifailure_admin BYPASSRLS;
      END IF;
    END
    $$;
    ALTER ROLE antifailure_admin LOGIN PASSWORD '${OPERATOR_PASSWORD}';
    GRANT USAGE ON SCHEMA public TO antifailure_admin;
    GRANT SELECT ON ALL TABLES IN SCHEMA public TO antifailure_admin;
    GRANT SELECT, INSERT ON audit_entries TO antifailure_admin;
    GRANT USAGE, SELECT ON SEQUENCE audit_entries_seq_seq TO antifailure_admin;
    GRANT SELECT, INSERT ON admin_audit_entries TO antifailure_admin;
    GRANT USAGE, SELECT ON SEQUENCE admin_audit_entries_seq_seq TO antifailure_admin;
  `)
}

describe(
  'the operator pool',
  { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' },
  () => {
    let h: Harness
    let one: Fixture
    let two: Fixture
    let operator: AdminPool

    before(async () => {
      h = await setup()
      await createOperatorRole(h.admin)
      one = await seedTenant(h.admin, 'pool-one')
      two = await seedTenant(h.admin, 'pool-two')
      operator = createAdminPool({ url: operatorUrl(), max: 2, connectTimeoutSeconds })
    })

    after(async () => {
      await operator.close()
    })

    it('reads across tenants where the application reads one', async () => {
      // The application's own connection, scoped to one tenant, on the same
      // database. This half is the control: without it, "the operator saw two"
      // could just mean the policies were never on.
      const seenByApp = await h.pool.withTenant({ orgId: one.orgId }, async (db) => {
        const rows = await db.execute<{ id: string }>(sql`SELECT id FROM organizations`)
        return rows.map((r) => r.id)
      })
      assert.deepEqual(seenByApp, [one.orgId], 'the application must see exactly its own tenant')

      const seenByOperator = await operator.withOperator(
        { adminUserId: crypto.randomUUID(), label: 'root@example.com' },
        async (db) => {
          const rows = await db.execute<{ id: string }>(
            sql`SELECT id FROM organizations WHERE id IN (${one.orgId}, ${two.orgId})`,
          )
          return rows.map((r) => r.id).sort()
        },
      )
      assert.deepEqual(
        seenByOperator,
        [one.orgId, two.orgId].sort(),
        'the operator pool must see both tenants',
      )
    })

    it('refuses a role that does not hold BYPASSRLS, rather than reading nothing', async () => {
      // Break the guard on purpose, on a role created for the purpose.
      //
      // NOT by toggling antifailure_admin off and on again, which was the first
      // version of this test. Roles are cluster wide, not per database, so that
      // version opened a window in which every other suite running against this
      // instance would have seen the operator role lose its attribute. A test
      // that breaks a guard must break it somewhere nobody else is standing.
      await h.admin.unsafe(`
        DO $$ BEGIN
          IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'antifailure_admin_crippled') THEN
            CREATE ROLE antifailure_admin_crippled NOLOGIN;
          END IF;
        END $$;
        ALTER ROLE antifailure_admin_crippled NOBYPASSRLS LOGIN PASSWORD '${OPERATOR_PASSWORD}';
        GRANT USAGE ON SCHEMA public TO antifailure_admin_crippled;
        GRANT SELECT ON ALL TABLES IN SCHEMA public TO antifailure_admin_crippled;
      `)
      const url = new URL(adminUrl)
      url.username = 'antifailure_admin_crippled'
      url.password = OPERATOR_PASSWORD
      const crippled = createAdminPool({ url: url.toString(), max: 1, connectTimeoutSeconds })
      try {
        await assert.rejects(
          () => crippled.withOperator({ adminUserId: crypto.randomUUID(), label: 'x' }, async () => 1),
          /does not hold BYPASSRLS/,
          'a role without the attribute must be refused by name',
        )

        // And prove the thing the refusal protects against is real: with the
        // attribute absent the same query returns nothing rather than raising,
        // so silence was genuinely the alternative to this check.
        const direct = postgres(url.toString(), {
          max: 1,
          connect_timeout: connectTimeoutSeconds,
          onnotice: () => {},
        })
        try {
          const rows = await direct`SELECT id FROM organizations`
          assert.equal(rows.length, 0, 'without BYPASSRLS the operator role reads zero rows in silence')
        } finally {
          await direct.end({ timeout: 5 })
        }
      } finally {
        await crippled.close()
      }
    })

    it('refuses to be pointed at the application role', async () => {
      // The other half of the same mistake: someone reuses the application URL
      // for the operator pool. antifailure_app has no BYPASSRLS either, so the
      // generic message would be technically true and useless. This names it.
      const wrong = createAdminPool({ url: appUrl(), max: 1, connectTimeoutSeconds })
      try {
        await assert.rejects(
          () => wrong.withOperator({ adminUserId: crypto.randomUUID(), label: 'x' }, async () => 1),
          /application's own role/,
          "pointing the operator pool at antifailure_app must be named, not merely rejected",
        )
      } finally {
        await wrong.close()
      }
    })

    it('does not let the application role acquire BYPASSRLS through membership', async () => {
      // The claim the whole design rests on, measured rather than asserted:
      // role ATTRIBUTES are not inherited, so even a mistaken GRANT of the
      // operator role to the application role widens nothing without an
      // explicit SET ROLE. If this ever fails, the separate-pool boundary is
      // decorative and the portal has to be redesigned.
      await h.admin.unsafe(`GRANT antifailure_admin TO antifailure_app`)
      try {
        const seen = await h.pool.withTenant({ orgId: one.orgId }, async (db) => {
          const rows = await db.execute<{ id: string }>(sql`SELECT id FROM organizations`)
          return rows.map((r) => r.id)
        })
        assert.deepEqual(
          seen,
          [one.orgId],
          'membership in the operator role must not widen what the application reads',
        )
      } finally {
        await h.admin.unsafe(`REVOKE antifailure_admin FROM antifailure_app`)
      }
    })

    it('carries the operator only for the length of one transaction', async () => {
      // A ONE CONNECTION pool, deliberately, and the test reads the setting
      // from OUTSIDE any transaction.
      //
      // The first version of this test set an operator, then set a different
      // one, and asserted the two differed. That passes whether or not the
      // setting is transaction local, because the second scope assigns its own
      // value either way: it proved nothing, and it kept passing when the
      // guard was broken on purpose. This version asks the connection what it
      // is still carrying once the transaction has committed, which is the
      // only thing that distinguishes the two cases.
      const solo = createAdminPool({ url: operatorUrl(), max: 1, connectTimeoutSeconds })
      try {
        const who = crypto.randomUUID()
        const inside = await solo.withOperator({ adminUserId: who, label: 'root@example.com' }, async (db) => {
          const rows = await db.execute<{ v: string }>(
            sql`SELECT current_setting('antifailure.admin_user_id', true) AS v`,
          )
          return rows[0]!.v
        })
        assert.equal(inside, who, 'the operator must be readable inside its own transaction')

        // Same pool, same single connection, no transaction. A pooled
        // connection handed on still carrying an operator's name is the failure
        // client.ts calls out for tenants, and set_config's third argument is
        // the only thing preventing it.
        const [after] = await solo.sql<{ v: string | null }[]>`
          SELECT current_setting('antifailure.admin_user_id', true) AS v`
        assert.ok(
          after!.v === null || after!.v === '',
          `the operator must not survive the transaction, but the connection still carries ${after!.v}`,
        )
      } finally {
        await solo.close()
      }
    })

    it('refuses an operator scope with nobody in it', async () => {
      await assert.rejects(
        () => operator.withOperator({ adminUserId: '', label: 'nobody' }, async () => 1),
        /no anonymous admin scope/,
      )
    })
  },
)

describe(
  'the double write',
  { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' },
  () => {
    let h: Harness
    let subject: Fixture
    let operator: AdminPool

    before(async () => {
      h = await setup()
      await createOperatorRole(h.admin)
      subject = await seedTenant(h.admin, 'double-write')
      operator = createAdminPool({ url: operatorUrl(), max: 2, connectTimeoutSeconds })
    })

    after(async () => {
      await operator.close()
      // The harness pool too, and only here: setup() is cached and shared with
      // the suite above, so closing it there would leave this one connecting to
      // a closed pool. Without this the process keeps a postgres socket open,
      // node --test never exits, and the run reports no exit code at all, which
      // is indistinguishable from a hang in CI.
      await h.close()
    })

    it("puts an operator action in the customer's own log", async () => {
      const who = crypto.randomUUID()
      await operator.withOperator({ adminUserId: who, label: 'root@example.com' }, async (db) => {
        await appendAdminAudit(db, {
          adminUserId: null,
          actorLabel: 'root@example.com',
          action: 'organization.suspended',
          targetType: 'organization',
          targetId: subject.orgId,
          subjectOrgId: subject.orgId,
          subjectOrgLabel: subject.slug,
          origin: 'admin',
          severity: 'high',
          detail: { reason: 'non payment' },
        })
      })

      // The operator's chain.
      const platform = await h.admin<{ action: string; severity: string }[]>`
        SELECT action, severity FROM admin_audit_entries
        WHERE subject_org_id = ${subject.orgId} ORDER BY seq DESC LIMIT 1`
      assert.equal(platform[0]?.action, 'organization.suspended')
      assert.equal(platform[0]?.severity, 'high')

      // The customer's chain. This is the half that makes it accountability
      // rather than a vendor's private note.
      const tenant = await h.admin<
        { action: string; origin: string; actor_user_id: string | null; actor_label: string }[]
      >`
        SELECT action, origin, actor_user_id, actor_label FROM audit_entries
        WHERE org_id = ${subject.orgId} ORDER BY seq DESC LIMIT 1`
      assert.equal(tenant[0]?.action, 'organization.suspended', 'the customer must see the action')
      assert.equal(
        tenant[0]?.origin,
        'admin',
        'the customer must be able to tell a vendor action from one of their own',
      )
      assert.equal(
        tenant[0]?.actor_user_id,
        null,
        'actor_user_id has a foreign key to users(id) and an operator is not a user',
      )
      assert.equal(tenant[0]?.actor_label, 'root@example.com')
    })

    it('writes both halves in one transaction or neither', async () => {
      const before = await counts(h, subject.orgId)
      await assert.rejects(
        operator.withOperator({ adminUserId: crypto.randomUUID(), label: 'root@example.com' }, async (db) => {
          await appendAdminAudit(db, {
            adminUserId: null,
            actorLabel: 'root@example.com',
            action: 'organization.plan_changed',
            targetType: 'organization',
            targetId: subject.orgId,
            subjectOrgId: subject.orgId,
            origin: 'admin',
            severity: 'notice',
          })
          // The action the entry describes fails after the entry is written.
          throw new Error('the change failed')
        }),
        /the change failed/,
      )
      const after = await counts(h, subject.orgId)
      assert.deepEqual(
        after,
        before,
        'an audit entry that survived a rolled back action would record something that did not happen',
      )
    })

    it('leaves the customer out of it only when a caller says so', async () => {
      const before = await counts(h, subject.orgId)
      await operator.withOperator({ adminUserId: crypto.randomUUID(), label: 'root@example.com' }, async (db) => {
        await appendAdminAudit(db, {
          adminUserId: null,
          actorLabel: 'root@example.com',
          action: 'read.admin.tenants.get',
          targetType: 'route',
          subjectOrgId: subject.orgId,
          origin: 'admin',
          severity: 'info',
          tenantCopy: false,
        })
      })
      const after = await counts(h, subject.orgId)
      assert.equal(after.platform, before.platform + 1, 'the operator chain always gets the entry')
      assert.equal(after.tenant, before.tenant, 'tenantCopy false must skip the customer copy')
    })
  },
)

async function counts(h: Harness, orgId: string): Promise<{ platform: number; tenant: number }> {
  const [p] = await h.admin<{ n: string }[]>`
    SELECT count(*) AS n FROM admin_audit_entries WHERE subject_org_id = ${orgId}`
  const [t] = await h.admin<{ n: string }[]>`
    SELECT count(*) AS n FROM audit_entries WHERE org_id = ${orgId}`
  return { platform: Number(p!.n), tenant: Number(t!.n) }
}
