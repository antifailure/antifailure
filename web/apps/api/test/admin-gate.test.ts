// The operator gate, tested through a router built for the purpose.
//
// WHY A ROUTER OF ITS OWN rather than the real admin routes. There are no real
// admin routes yet: they belong to the three lanes building on this. A gate
// tested only through routes that do not exist is a gate nobody has run, and
// the whole point of this file is that adminProcedure REFUSES correctly before
// anybody writes a route that depends on it refusing.
//
// WHAT IS ACTUALLY BEING PROVED, and it is the ordering rather than the
// outcome. An impersonating operator must be refused BEFORE the permission
// check, so the two cases that distinguish the orderings are both here:
//
//   impersonating AND holding the permission        -> refused for impersonating
//   impersonating AND NOT holding the permission    -> refused for impersonating
//
// A gate that checked the permission first would pass the first case and give
// the wrong reason for the second. Only the pair pins the order down.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import postgres from 'postgres'
import { createAdminPool, type AdminPool } from '@antifailure/db'
import { adminProcedure, type AdminActor } from '../src/admin/trpc.ts'
import { router, type Context } from '../src/trpc.ts'
import { adminUrl, available } from './harness.ts'

const hasDatabase = await available()
const OPERATOR_PASSWORD = 'operator-test-password'

function operatorUrl(): string {
  const u = new URL(adminUrl)
  u.username = 'antifailure_admin'
  u.password = OPERATOR_PASSWORD
  return u.toString()
}

/** The router under test. Two permissions, deliberately: one the role below
 *  holds and one it does not, so the impersonation refusal can be shown to
 *  preempt both. */
const testRouter = router({
  reads: adminProcedure('admin.tenants.read').query(() => 'the answer'),
  writesOperators: adminProcedure('admin.operators.write').mutation(() => 'changed'),
})

/**
 * A real operator row, and an actor pointing at it.
 *
 * A fabricated uuid was the first version and it does not work, for a reason
 * worth keeping: admin_audit_entries.admin_user_id REFERENCES admin_users(id),
 * so an audit append naming an operator who does not exist violates the key and
 * the gate reports INTERNAL_SERVER_ERROR instead of its refusal. The database
 * refuses to record an action by nobody, which is correct, and it means a test
 * of the gate has to seed the operator it claims to be.
 *
 * `support` is the role throughout: it holds admin.tenants.read and does NOT
 * hold admin.operators.write, which is what makes the two routes above able to
 * tell the orderings apart.
 */
async function seedOperator(
  admin: postgres.Sql,
  overrides: Partial<AdminActor> = {},
): Promise<AdminActor> {
  const email = `operator-${crypto.randomUUID().slice(0, 8)}@example.com`
  const [row] = await admin<{ id: string }[]>`
    INSERT INTO admin_users (email, name, role)
    VALUES (${email}, ${'An Operator'}, ${'support'})
    RETURNING id`
  return {
    adminUserId: row!.id,
    label: email,
    role: 'support',
    sessionId: crypto.randomUUID(),
    impersonating: false,
    ...overrides,
  }
}

function contextWith(admin: AdminActor | null, adminPool: AdminPool | null): Context {
  // Only the fields the gate reads are real. The rest of the tenant context is
  // not consulted by any path under test, and filling it with fakes would
  // suggest it was.
  return {
    admin,
    adminPool,
    actor: null,
    origin: 'web',
    ip: '203.0.113.7',
    clock: { now: () => new Date('2026-09-01T00:00:00Z') },
  } as unknown as Context
}

async function refusal(fn: () => Promise<unknown>): Promise<{ code: string; message: string }> {
  try {
    await fn()
  } catch (err) {
    const e = err as { code?: string; message: string }
    return { code: e.code ?? 'NONE', message: e.message }
  }
  throw new Error('expected a refusal and the call succeeded')
}

describe(
  'the operator gate',
  { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' },
  () => {
    let admin: postgres.Sql
    let operator: AdminPool

    before(async () => {
      admin = postgres(adminUrl, { max: 2, onnotice: () => {} })
      await admin.unsafe(`
        DO $$ BEGIN
          IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'antifailure_admin') THEN
            CREATE ROLE antifailure_admin NOLOGIN BYPASSRLS;
          ELSE ALTER ROLE antifailure_admin BYPASSRLS; END IF;
        END $$;
        ALTER ROLE antifailure_admin LOGIN PASSWORD '${OPERATOR_PASSWORD}';
        GRANT USAGE ON SCHEMA public TO antifailure_admin;
        GRANT SELECT ON ALL TABLES IN SCHEMA public TO antifailure_admin;
        GRANT SELECT, INSERT ON admin_audit_entries TO antifailure_admin;
        GRANT USAGE, SELECT ON SEQUENCE admin_audit_entries_seq_seq TO antifailure_admin;
      `)
      operator = createAdminPool({ url: operatorUrl(), max: 2 })
    })

    after(async () => {
      await operator.close()
      await admin.end({ timeout: 5 })
    })

    it('refuses a request carrying no operator session', async () => {
      const caller = testRouter.createCaller(contextWith(null, operator))
      const r = await refusal(() => caller.reads())
      assert.equal(r.code, 'UNAUTHORIZED')
    })

    it('names the missing variable when no operator credential is configured', async () => {
      // Not an empty portal. A deployment that never set the variable gets a
      // sentence naming it, because an operator page rendering its empty state
      // is indistinguishable from a platform with no customers.
      const caller = testRouter.createCaller(contextWith(await seedOperator(admin), null))
      const r = await refusal(() => caller.reads())
      assert.equal(r.code, 'PRECONDITION_FAILED')
      assert.match(r.message, /AF_ADMIN_DATABASE_URL/)
    })

    it('refuses an impersonating operator ON A ROUTE THEY DO HOLD', async () => {
      // The case that pins the ordering. `support` holds admin.tenants.read, so
      // a gate that checked the permission first would let this through.
      const caller = testRouter.createCaller(
        contextWith(await seedOperator(admin, { impersonating: true }), operator),
      )
      const r = await refusal(() => caller.reads())
      assert.equal(r.code, 'FORBIDDEN')
      assert.match(r.message, /impersonating a customer/)
      assert.doesNotMatch(
        r.message,
        /permission/,
        'the reason must be the impersonation, not a permission the operator actually holds',
      )
    })

    it('refuses an impersonating operator on a route they do NOT hold, for the impersonation', async () => {
      // The other half. Both orderings refuse here, so only the REASON
      // distinguishes them, and the reason is what a person acts on.
      const caller = testRouter.createCaller(
        contextWith(await seedOperator(admin, { impersonating: true }), operator),
      )
      const r = await refusal(() => caller.writesOperators())
      assert.equal(r.code, 'FORBIDDEN')
      assert.match(
        r.message,
        /impersonating a customer/,
        'the impersonation must be reported ahead of the missing permission',
      )
    })

    it('refuses a permission the operator role does not hold, naming it', async () => {
      const caller = testRouter.createCaller(contextWith(await seedOperator(admin), operator))
      const r = await refusal(() => caller.writesOperators())
      assert.equal(r.code, 'FORBIDDEN')
      assert.match(r.message, /admin\.operators\.write/)
    })

    it('lets a held permission through and records the read without being asked', async () => {
      const who = await seedOperator(admin)
      const caller = testRouter.createCaller(contextWith(who, operator))
      const before = await entriesFor(admin, who.adminUserId)
      assert.equal(await caller.reads(), 'the answer')

      // Automatic per request read auditing. Nothing in the router above asks
      // for this, which is the point: a route cannot forget to audit a read
      // because the route is not what does it.
      const rows = await admin<{ action: string; severity: string; target_id: string }[]>`
        SELECT action, severity, target_id FROM admin_audit_entries
        WHERE admin_user_id = ${who.adminUserId} ORDER BY seq DESC LIMIT 1`
      assert.equal(await entriesFor(admin, who.adminUserId), before + 1)
      assert.equal(rows[0]?.action, 'read.reads')
      assert.equal(rows[0]?.severity, 'info')
    })

    it('records both refusals, because a refusal is the line most worth having', async () => {
      const impersonator = await seedOperator(admin, { impersonating: true })
      const underprivileged = await seedOperator(admin)
      await refusal(() =>
        testRouter.createCaller(contextWith(impersonator, operator)).reads(),
      )
      await refusal(() =>
        testRouter.createCaller(contextWith(underprivileged, operator)).writesOperators(),
      )

      const [imp] = await admin<{ action: string; severity: string }[]>`
        SELECT action, severity FROM admin_audit_entries
        WHERE admin_user_id = ${impersonator.adminUserId} ORDER BY seq DESC LIMIT 1`
      assert.equal(imp?.action, 'refused.reads')
      assert.equal(imp?.severity, 'high', 'an impersonating operator reaching the portal is not routine')

      const [under] = await admin<{ action: string; severity: string }[]>`
        SELECT action, severity FROM admin_audit_entries
        WHERE admin_user_id = ${underprivileged.adminUserId} ORDER BY seq DESC LIMIT 1`
      assert.equal(under?.action, 'refused.writesOperators')
      assert.equal(under?.severity, 'notice')
    })
  },
)

async function entriesFor(admin: postgres.Sql, adminUserId: string): Promise<number> {
  const [row] = await admin<{ n: string }[]>`
    SELECT count(*) AS n FROM admin_audit_entries WHERE admin_user_id = ${adminUserId}`
  return Number(row!.n)
}
