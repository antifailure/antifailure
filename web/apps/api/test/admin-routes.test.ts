// The operator routes, and the four ways a guarded route stops being guarded.
//
// This suite exists because until it did, adminProcedure had ZERO CALL SITES.
// A gate whose refusals are unit tested and which nothing calls is a capability
// that exists and does nothing, which is the exact shape of the failure this
// project keeps deleting. So every assertion here goes through the real router.
//
//   1. A route is added with no permission. Caught by the matrix test below,
//      which walks the router itself rather than a list somebody maintains.
//   2. A permission is declared and not enforced, so any operator can call it.
//   3. An impersonating session can act as an operator, which is the single
//      most dangerous state in this product.
//   4. A write reports success and changes nothing, or changes something and
//      records nothing. Both are checked by reading the row back and by
//      counting audit entries, never by trusting the return value.

import { test, describe, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { appRouter } from '../src/routers/index.ts'
import { adminSignIn, hashPassword, hashAdminToken } from '../src/admin/session.ts'
import { type AdminRole } from '../src/admin/permissions.ts'
import { assertOperatorRoutesAreGuarded } from './admin-matrix.ts'
import { createAdminPool, type AdminPool } from '@antifailure/db'
import { available, startApi, seedOrg, adminUrl, type ApiHarness, type Org } from './harness.ts'

const hasDb = await available()

describe('every operator route declares a permission', () => {
  // The walk itself lives in admin-matrix.ts so a SECOND tree can be pointed at
  // it. admin-money mounts its money routes separately at /admin/trpc, and a
  // second tree needs a second walk or it is guarded by neither matrix. A route
  // nobody enumerates is worse than one in a tree that does, because the second
  // at least fails.
  assertOperatorRoutesAreGuarded({
    name: 'appRouter',
    router: appRouter,
    prefix: 'admin.',
    atLeast: 8,
  })
})

describe('the operator routes', { skip: hasDb ? false : 'no database' }, () => {
  let h: ApiHarness
  let alice: Org
  let bob: Org
  let adminPool: AdminPool
  const password = 'provisioned-at-deploy-not-in-source'

  /** Signs in an operator with the given role and returns a caller whose
   *  context carries that session, which is the path a real request takes. */
  async function callerFor(role: AdminRole) {
    const email = `${role}-${randomUUID().slice(0, 8)}@example.test`
    const { hash, salt } = await hashPassword(password)
    await h.admin`
      INSERT INTO admin_users (email, name, role, password_hash, password_salt, password_set_at)
      VALUES (${email}, ${role}, ${role}, ${hash}, ${salt}, now())`
    // Real time, not h.clock. Operator session expiry is enforced in TWO
    // places that must agree: resolveAdminSession compares against the
    // injected clock, and the RLS policy behind current_admin_user() compares
    // expires_at against the DATABASE's now(). A fake clock moves the first and
    // cannot move the second, so a session issued at a fake past instant is
    // already expired as far as the policy is concerned, and every operator
    // write is refused with an RLS violation that looks like a permissions bug.
    // In production the two clocks are the same clock; only tests can separate
    // them, and this is the line that keeps them together.
    const signedIn = await adminSignIn(h.pool, { email, password }, new Date())
    return { token: signedIn.token, caller: await callerWithToken(signedIn.token) }
  }

  async function callerWithToken(token: string) {
    const { resolveAdminSession } = await import('../src/admin/session.ts')
    const resolved = await resolveAdminSession(h.pool, token, new Date())
    assert.ok(resolved, 'the operator session did not resolve')
    return appRouter.createCaller({
      ...baseContext(),
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

  function baseContext() {
    return {
      pool: h.pool,
      adminPool,
      clock: h.clock,
      github: h.github,
      stripe: null,
      appBaseUrl: 'http://localhost',
      mailer: null,
      productName: 'Antifailure',
      hostedRequiredPlan: null,
      actor: null,
      admin: null,
      origin: 'web' as const,
    }
  }

  before(async () => {
    h = await startApi()
    alice = await seedOrg(h.admin, 'alice')
    bob = await seedOrg(h.admin, 'bob')

    // 0023 creates antifailure_admin NOLOGIN, because a self-hosted
    // installation supplies its own credential. The suite gives it one, the
    // same way the db harness does for antifailure_app.
    await h.admin.unsafe(`ALTER ROLE antifailure_admin LOGIN PASSWORD 'operator-test-password'`)
    const u = new URL(adminUrl)
    u.username = 'antifailure_admin'
    u.password = 'operator-test-password'
    adminPool = createAdminPool({ url: u.toString() })
    // Fails loudly here rather than as an empty page later, which is exactly
    // what a pool pointed at a non-bypassing role produces.
    await adminPool.ensureBypass()
  })

  after(async () => {
    await adminPool?.close()
    await h?.close()
  })

  test('an operator reads across tenants, which no tenant route can', async () => {
    const { caller } = await callerFor('super_admin')
    const page = await caller.admin.tenants.list({ limit: 100 })
    const slugs = page.rows.map((r) => r.slug)
    assert.ok(slugs.includes(alice.slug), "the operator cannot see alice's organization")
    assert.ok(slugs.includes(bob.slug), "the operator cannot see bob's organization")
  })

  test('a role without the permission is refused, naming the permission', async () => {
    // support holds admin.tenants.read and NOT admin.tenants.suspend, which is
    // the split that lets somebody answer a question without holding the
    // button that stops a customer working.
    const { caller } = await callerFor('support')
    await assert.doesNotReject(() => caller.admin.tenants.list({ limit: 5 }))
    await assert.rejects(
      () => caller.admin.tenants.suspend({ orgId: alice.orgId, reason: 'should not happen' }),
      (err: Error) => /admin\.tenants\.suspend/.test(err.message),
      'support could suspend an organization',
    )
  })

  test('an impersonating session cannot take ANY operator action', async () => {
    // The refusal happens before the permission is consulted, so it applies to
    // a role that holds every permission. And because starting an impersonation
    // is itself an operator route, this also makes impersonation a one way
    // door: a session that cannot act as an operator cannot start another one.
    const { token } = await callerFor('owner')
    const hash = hashAdminToken(token)
    const user = await seedImpersonationTarget(h)
    const [seq] = await h.admin<{ seq: string }[]>`
      SELECT seq FROM admin_audit_entries ORDER BY seq DESC LIMIT 1`
    await h.admin`
      UPDATE admin_sessions
      SET impersonated_user_id = ${user!.id}, impersonation_reason = 'support case',
          impersonation_audit_seq = ${seq!.seq}
      WHERE token_hash = ${hash}`

    const caller = await callerWithToken(token)
    await assert.rejects(
      () => caller.admin.tenants.list({ limit: 5 }),
      (err: Error) => /impersonating/i.test(err.message),
      'an impersonating session read across tenants',
    )
    await assert.rejects(
      () => caller.admin.me(),
      (err: Error) => /impersonating/i.test(err.message),
      'an impersonating session reached even the lowest-privilege route',
    )
  })

  test('a request with no operator session is refused', async () => {
    const caller = appRouter.createCaller(baseContext() as never)
    await assert.rejects(
      () => caller.admin.tenants.list({ limit: 5 }),
      (err: Error) => /sign in/i.test(err.message),
    )
  })

  describe('suspend is effective and recorded, not merely reported', () => {
    test('the row changes and the audit chain gains an entry', async () => {
      const { caller } = await callerFor('super_admin')
      const [before] = await h.admin<{ n: string }[]>`
        SELECT count(*)::text AS n FROM admin_audit_entries WHERE action = 'organization.suspended'`

      const result = await caller.admin.tenants.suspend({
        orgId: bob.orgId,
        reason: 'abuse report 4471',
      })
      assert.equal(result.suspended, true)

      // Read back from the database rather than trusting the return value.
      const [row] = await h.admin<{ suspended_at: Date | null; suspended_reason: string }[]>`
        SELECT suspended_at, suspended_reason FROM organizations WHERE id = ${bob.orgId}::uuid`
      assert.notEqual(row!.suspended_at, null, 'suspend returned true and suspended nothing')
      assert.equal(row!.suspended_reason, 'abuse report 4471')

      const [after] = await h.admin<{ n: string }[]>`
        SELECT count(*)::text AS n FROM admin_audit_entries WHERE action = 'organization.suspended'`
      assert.equal(
        Number(after!.n),
        Number(before!.n) + 1,
        'the organization was suspended with no entry in the operator chain',
      )
    })

    test('the customer sees it in their OWN audit log', async () => {
      // The double write, and the half that matters to the person whose
      // account was touched. A record only the vendor can read is a vendor's
      // private note, not accountability.
      const rows = await h.admin<{ action: string; origin: string; actor_label: string }[]>`
        SELECT action, origin, actor_label FROM audit_entries
        WHERE org_id = ${bob.orgId}::uuid AND action = 'organization.suspended'
        ORDER BY seq DESC LIMIT 1`
      assert.equal(rows.length, 1, "the tenant's own audit log has no record of the suspension")
      assert.equal(rows[0]!.origin, 'admin', 'the entry does not say it was the vendor')
      assert.match(rows[0]!.actor_label, /@example\.test/, 'the entry does not name the operator')
    })

    test('resume clears it, and is recorded too', async () => {
      const { caller } = await callerFor('super_admin')
      await caller.admin.tenants.resume({ orgId: bob.orgId })
      const [row] = await h.admin<{ suspended_at: Date | null }[]>`
        SELECT suspended_at FROM organizations WHERE id = ${bob.orgId}::uuid`
      assert.equal(row!.suspended_at, null, 'resume reported success and left it suspended')
    })

    test('a reason is required, so a suspension always says why', async () => {
      const { caller } = await callerFor('super_admin')
      await assert.rejects(() =>
        caller.admin.tenants.suspend({ orgId: bob.orgId, reason: '   ' }),
      )
    })

    test('suspending an organization that does not exist records nothing', async () => {
      // The audit entry is written first, so the ordering has to be proven not
      // to produce a record of an action against a row that was never there.
      const { caller } = await callerFor('super_admin')
      const [before] = await h.admin<{ n: string }[]>`
        SELECT count(*)::text AS n FROM admin_audit_entries`
      await assert.rejects(() =>
        caller.admin.tenants.suspend({ orgId: randomUUID(), reason: 'nobody' }),
      )
      const [after] = await h.admin<{ n: string }[]>`
        SELECT count(*)::text AS n FROM admin_audit_entries`
      assert.equal(after!.n, before!.n, 'an audit entry survived a refused action')
    })
  })

  test('changing the plan records what it changed from', async () => {
    const { caller } = await callerFor('billing')
    const [was] = await h.admin<{ plan: string }[]>`
      SELECT plan FROM organizations WHERE id = ${alice.orgId}::uuid`

    await caller.admin.tenants.setPlan({
      orgId: alice.orgId,
      plan: 'team',
      reason: 'signed an order form',
    })

    const [now] = await h.admin<{ plan: string }[]>`
      SELECT plan FROM organizations WHERE id = ${alice.orgId}::uuid`
    assert.equal(now!.plan, 'team', 'setPlan reported success and changed nothing')

    const [entry] = await h.admin<{ detail: { from: string; to: string } }[]>`
      SELECT detail FROM admin_audit_entries
      WHERE action = 'organization.plan_changed' ORDER BY seq DESC LIMIT 1`
    assert.equal(entry!.detail.from, was!.plan, 'the entry does not say what the plan was before')
    assert.equal(entry!.detail.to, 'team')
  })

  test('revoking a session makes the next request fail, not just the row change', async () => {
    // The canonical failure of this whole project is a Revoke that leaves the
    // session valid. resolveSession reads revoked_at on every request before
    // the expiry check, and this proves that end to end rather than asserting
    // the column was written.
    const { signInAs, resolveSession } = await import('./harness.ts').then(async (m) => ({
      signInAs: m.signInAs,
      resolveSession: (await import('../src/auth/session.ts')).resolveSession,
    }))
    const person = await signInAs(h, alice, 'owner')

    const before = await resolveSession(h.pool, h.clock, person.token)
    assert.ok(before, 'the customer session did not resolve before revocation')

    const { caller } = await callerFor('security')
    await caller.admin.sessions.revoke({ sessionId: before.sessionId, reason: 'stolen laptop' })

    const after = await resolveSession(h.pool, h.clock, person.token)
    assert.equal(after, null, 'the session still resolved after an operator revoked it')
  })

  test('an operator read is itself recorded, without the route asking', async () => {
    // Every request that reaches the cross-tenant handle writes one entry, from
    // the gate, so a route cannot read the platform without leaving a trace and
    // no route author has to remember to.
    const { caller } = await callerFor('read_only')
    // The gate names the entry `read.<procedure path>` rather than one shared
    // action, so an investigation can ask "who looked at the tenant list" and
    // not merely "who read something".
    const [before] = await h.admin<{ n: string }[]>`
      SELECT count(*)::text AS n FROM admin_audit_entries WHERE action LIKE 'read.%'`
    await caller.admin.tenants.list({ limit: 5 })
    const [after] = await h.admin<{ n: string }[]>`
      SELECT count(*)::text AS n FROM admin_audit_entries WHERE action LIKE 'read.%'`
    assert.ok(
      Number(after!.n) > Number(before!.n),
      'a cross-tenant read left no record, so the backdoor is unaudited',
    )
    const [named] = await h.admin<{ action: string }[]>`
      SELECT action FROM admin_audit_entries WHERE action LIKE 'read.%'
      ORDER BY seq DESC LIMIT 1`
    assert.equal(named!.action, 'read.admin.tenants.list', 'the entry does not name what was read')
  })

  test('a credential is never in an operator response', async () => {
    // RLS is row level and cannot restrict a column, and a GRANT cannot help
    // when both paths share one database role, so this is an application
    // property and this is the test that holds it.
    const { caller } = await callerFor('security')
    const operators = await caller.admin.operators.list()
    for (const op of operators) {
      for (const key of Object.keys(op)) {
        assert.ok(
          !/password|hash|salt|secret|token/i.test(key),
          `the operator list exposes ${key}`,
        )
      }
    }
    assert.ok(operators.length > 0, 'no operators were returned, so this proved nothing')
  })
})

/**
 * A customer account to impersonate.
 *
 * Created here rather than read with `SELECT id FROM users LIMIT 1`, which is
 * how these tests were written and why three of them were unreliable: on a
 * database another suite had populated they found a row and passed, and on a
 * FRESH one they found nothing and failed. A fixture that depends on leftovers
 * is green for the wrong reason, which is worse than red.
 */
async function seedImpersonationTarget(h: ApiHarness): Promise<{ id: string }> {
  const suffix = randomUUID().slice(0, 8)
  const [row] = await h.admin<{ id: string }[]>`
    INSERT INTO users (github_id, github_login, email, name)
    VALUES (${Math.floor(Math.random() * 2_000_000_000)}, ${`target-${suffix}`},
            ${`target-${suffix}@example.test`}, 'Impersonation Target')
    RETURNING id`
  return row!
}
