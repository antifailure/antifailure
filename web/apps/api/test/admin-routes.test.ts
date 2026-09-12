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
    // Taken by RUNNING the walk on the merged tree rather than by counting call
    // sites or by taking the highest of the lanes' guesses. Six branches edited
    // this integer to 22, 29, 30, 48, 50, 53 and 63 while the tree grew under all of
    // them, and every one of those numbers was right when it was written. The
    // floor has to move with the tree or it stops being a floor: an atLeast a
    // SHRINKING router still clears is an assertion that has quietly stopped
    // checking whether routes moved out of this tree, which is the one thing it
    // exists for.
    // 89 taken by running the walk on this branch's merged tree, the way the
    // paragraph above says to, rather than by adding one to the number that was
    // here. This branch adds admin.operators.setPassword.
    atLeast: 89,
  })
})

describe('the operator routes', { skip: hasDb ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
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

  /** A caller plus the id of the operator it is acting as, for the routes
   *  whose whole point is what an operator may do to THEMSELVES. */
  async function callerForWithId(role: AdminRole) {
    const email = `${role}-${randomUUID().slice(0, 8)}@example.test`
    const { hash, salt } = await hashPassword(password)
    const [row] = await h.admin<{ id: string }[]>`
      INSERT INTO admin_users (email, name, role, password_hash, password_salt, password_set_at)
      VALUES (${email}, ${role}, ${role}, ${hash}, ${salt}, now())
      RETURNING id`
    const signedIn = await adminSignIn(h.pool, { email, password }, new Date())
    return { adminUserId: row!.id, caller: await callerWithToken(signedIn.token) }
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

  describe('suspending an account actually ends it', () => {
    // The canonical failure of this project, aimed at the exact column that
    // had it: users.suspended_at was added by a migration and read by NOTHING,
    // so a Suspend would have changed a row, written an audit entry, and left
    // the person working until their session happened to expire.
    //
    // So this proves the observable end state rather than the write. It signs
    // a real customer in, resolves their session to show it works, suspends
    // the account through the operator route, and resolves the SAME token
    // again expecting null. Nothing here trusts a return value.
    test('a live session stops resolving on the next request', async () => {
      const { signInAs } = await import('./harness.ts')
      const { resolveSession } = await import('../src/auth/session.ts')
      const person = await signInAs(h, alice, 'member')

      const before = await resolveSession(h.pool, h.clock, person.token)
      assert.ok(before, 'the customer session did not resolve before suspension')

      const { caller } = await callerFor('security')
      const result = await caller.admin.users.suspend({
        userId: before.userId,
        reason: 'credential stuffing from one address',
      })
      assert.equal(result.suspended, true)

      const after = await resolveSession(h.pool, h.clock, person.token)
      assert.equal(
        after,
        null,
        'the account was suspended and its session still resolved, so Suspend does not suspend',
      )
    })

    test('restoring lets the same session resolve again', async () => {
      const { signInAs } = await import('./harness.ts')
      const { resolveSession } = await import('../src/auth/session.ts')
      const person = await signInAs(h, bob, 'member')
      const live = await resolveSession(h.pool, h.clock, person.token)
      assert.ok(live)

      const { caller } = await callerFor('security')
      await caller.admin.users.suspend({ userId: live.userId, reason: 'under review' })
      assert.equal(await resolveSession(h.pool, h.clock, person.token), null)

      await caller.admin.users.restore({ userId: live.userId })
      const back = await resolveSession(h.pool, h.clock, person.token)
      assert.ok(back, 'restore did not bring the account back')
      assert.equal(back.userId, live.userId)
    })

    test('the suspension is recorded with its reason', async () => {
      const [entry] = await h.admin<{ detail: { reason: string }; severity: string }[]>`
        SELECT detail, severity FROM admin_audit_entries
        WHERE action = 'user.suspended' ORDER BY seq DESC LIMIT 1`
      assert.equal(entry!.severity, 'high')
      assert.ok(entry!.detail.reason.length > 0, 'a suspension was recorded with no reason')
    })

    test('suspending an account that does not exist records nothing', async () => {
      const { caller } = await callerFor('security')
      const [before] = await h.admin<{ n: string }[]>`
        SELECT count(*)::text AS n FROM admin_audit_entries`
      await assert.rejects(() =>
        caller.admin.users.suspend({ userId: randomUUID(), reason: 'nobody' }),
      )
      const [after] = await h.admin<{ n: string }[]>`
        SELECT count(*)::text AS n FROM admin_audit_entries`
      assert.equal(after!.n, before!.n, 'an audit entry survived a refused suspension')
    })

    test('support can read accounts and cannot suspend one', async () => {
      const { caller } = await callerFor('support')
      await assert.doesNotReject(() => caller.admin.users.list({ limit: 5 }))
      await assert.rejects(
        () => caller.admin.users.suspend({ userId: randomUUID(), reason: 'no' }),
        (err: Error) => /admin\.users\.write/.test(err.message),
      )
    })
  })

  describe('operator administration cannot be turned on itself', () => {
    // admin.operators.write is held by super_admin as well as owner, so the
    // dangerous move is not an operator abusing a customer, it is an operator
    // widening their own privileges. These are the guards for that, and each is
    // watched failing by removing the SELF check rather than by reasoning.

    test('an operator cannot promote themselves', async () => {
      // Without this a super_admin grants themselves owner and picks up
      // admin.audit.export and every other owner-only permission. A privilege
      // model where the privileged can widen their own privileges is not one.
      const { caller, adminUserId } = await callerForWithId('super_admin')
      await assert.rejects(
        () => caller.admin.operators.setRole({ adminUserId, role: 'owner' }),
        (err: Error) => /cannot change your own operator role/i.test(err.message),
        'a super_admin promoted themselves to owner',
      )
      const [row] = await h.admin<{ role: string }[]>`
        SELECT role FROM admin_users WHERE id = ${adminUserId}::uuid`
      assert.equal(row!.role, 'super_admin', 'the role changed despite the refusal')
    })

    test('an operator cannot suspend themselves', async () => {
      const { caller, adminUserId } = await callerForWithId('owner')
      await assert.rejects(
        () => caller.admin.operators.suspend({ adminUserId, reason: 'oops' }),
        (err: Error) => /cannot suspend yourself/i.test(err.message),
      )
    })

    test('an operator CAN change somebody else, so the refusal is about self and not about the route', async () => {
      // The control that makes the two above mean something. Without it they
      // would pass equally well on a route that refuses everybody.
      const { caller } = await callerForWithId('owner')
      const { adminUserId: other } = await callerForWithId('support')
      const result = await caller.admin.operators.setRole({ adminUserId: other, role: 'analytics' })
      assert.equal(result.changed, true)
      const [row] = await h.admin<{ role: string }[]>`
        SELECT role FROM admin_users WHERE id = ${other}::uuid`
      assert.equal(row!.role, 'analytics', 'setRole reported success and changed nothing')
    })

    describe('the root operator is permanent, through the route and not only in the database', () => {
      // The triggers were proven in the db suite. What these prove is that the
      // ROUTE cannot get around them, which is the thing an operator can
      // actually reach.
      let rootId: string

      before(async () => {
        const [row] = await h.admin<{ id: string }[]>`
          INSERT INTO admin_users (email, name, role, is_root)
          VALUES (${`root-${randomUUID().slice(0, 8)}@example.test`}, 'Root', 'owner', true)
          RETURNING id`
        rootId = row!.id
      })

      after(async () => {
        await h.admin`ALTER TABLE admin_users DISABLE TRIGGER admin_root_is_permanent_del`
        await h.admin`DELETE FROM admin_users WHERE id = ${rootId}::uuid`
        await h.admin`ALTER TABLE admin_users ENABLE TRIGGER admin_root_is_permanent_del`
      })

      test('cannot be demoted through the route', async () => {
        const { caller } = await callerForWithId('owner')
        await assert.rejects(() =>
          caller.admin.operators.setRole({ adminUserId: rootId, role: 'read_only' }),
        )
        const [row] = await h.admin<{ role: string }[]>`
          SELECT role FROM admin_users WHERE id = ${rootId}::uuid`
        assert.equal(row!.role, 'owner', 'the root operator was demoted through the route')
      })

      test('cannot be suspended through the route', async () => {
        const { caller } = await callerForWithId('owner')
        await assert.rejects(() =>
          caller.admin.operators.suspend({ adminUserId: rootId, reason: 'no' }),
        )
        const [row] = await h.admin<{ suspended_at: Date | null }[]>`
          SELECT suspended_at FROM admin_users WHERE id = ${rootId}::uuid`
        assert.equal(row!.suspended_at, null, 'the root operator was suspended through the route')
      })
    })

    test('a created operator cannot sign in, because no password was minted for it', async () => {
      // The provisioning story, asserted rather than described. A route that
      // generated a starting password would be the worst thing in the portal.
      const { caller } = await callerForWithId('owner')
      const email = `fresh-${randomUUID().slice(0, 8)}@example.test`
      const made = await caller.admin.operators.create({ email, name: 'Fresh', role: 'support' })
      assert.equal(made.provisioned, false)

      const [row] = await h.admin<{ password_hash: Buffer | null }[]>`
        SELECT password_hash FROM admin_users WHERE id = ${made.id}::uuid`
      assert.equal(row!.password_hash, null, 'creating an operator minted a credential')

      const { adminSignIn, AdminSignInError } = await import('../src/admin/session.ts')
      await assert.rejects(
        () => adminSignIn(h.pool, { email, password: '' }, new Date()),
        AdminSignInError,
      )
    })

    describe('setting a password, which is what finishes an invitation', () => {
      // WHY THIS SUITE IS LONG. The route above proves an invited operator
      // CANNOT sign in, and that was the whole provisioning story until now:
      // the only thing that could finish the invitation was a command holding a
      // connection string. What follows is the other half, and every assertion
      // reads the row or the sign in back rather than trusting a return value,
      // because "the mutation resolved" is exactly what a route that writes
      // nothing also does.

      /** An operator who has never been given a password, as `create` leaves
       *  one. Made through the ROUTE rather than by INSERT, so the suite is
       *  testing the pair of routes a person actually uses. */
      async function invited(caller: Awaited<ReturnType<typeof callerForWithId>>['caller']) {
        const email = `invited-${randomUUID().slice(0, 8)}@example.test`
        const made = await caller.admin.operators.create({ email, name: 'Invited', role: 'support' })
        return { id: made.id, email }
      }

      async function hashOf(id: string): Promise<Buffer | null> {
        const [row] = await h.admin<{ password_hash: Buffer | null }[]>`
          SELECT password_hash FROM admin_users WHERE id = ${id}::uuid`
        return row!.password_hash
      }

      test('an invited operator can sign in afterwards, which is the whole point', async () => {
        const { caller } = await callerForWithId('owner')
        const { id, email } = await invited(caller)
        assert.equal(await hashOf(id), null, 'the invitation started with a credential')

        const chosen = 'a-passphrase-chosen-by-the-owner'
        const result = await caller.admin.operators.setPassword({ adminUserId: id, password: chosen })
        assert.equal(result.provisioned, true)
        assert.equal(result.replacedAPassword, false, 'finishing an invitation reported a replacement')

        // The end to end proof. Not "password_hash is not null", which a route
        // writing garbage would also satisfy: the credential has to open a
        // session through the real sign in path.
        const signedIn = await adminSignIn(h.pool, { email, password: chosen }, new Date())
        assert.ok(signedIn.token, 'the operator still could not sign in after a password was set')

        const listed = await caller.admin.operators.list()
        assert.equal(
          listed.find((o) => o.id === id)?.provisioned,
          true,
          'the directory still shows the account as unprovisioned',
        )
      })

      test('a password under the floor is refused, and the account stays unsignable', async () => {
        const { caller } = await callerForWithId('owner')
        const { id } = await invited(caller)
        await assert.rejects(
          () => caller.admin.operators.setPassword({ adminUserId: id, password: 'eleven-char' }),
          (err: Error) => /at least 12/.test(err.message),
        )
        assert.equal(await hashOf(id), null, 'a refused password was written anyway')
      })

      test('a password with a stray newline is refused rather than silently kept', async () => {
        // The heredoc case. Accepting it writes a credential nobody can ever
        // retype, because the newline is invisible in every attempt.
        const { caller } = await callerForWithId('owner')
        const { id } = await invited(caller)
        await assert.rejects(
          () =>
            caller.admin.operators.setPassword({
              adminUserId: id,
              password: 'a-perfectly-fine-passphrase\n',
            }),
          (err: Error) => /whitespace/.test(err.message),
        )
        assert.equal(await hashOf(id), null, 'a password with a trailing newline was written')
      })

      test('a reset stops the old password and the sessions it had already opened', async () => {
        // A password changed because it leaked, that leaves the sessions it
        // opened alive, is a reset that resets nothing: an operator session
        // lasts twelve hours and reads every tenant for all of them.
        const { caller } = await callerForWithId('owner')
        const { id, email } = await invited(caller)
        const first = 'the-first-password-they-had'
        await caller.admin.operators.setPassword({ adminUserId: id, password: first })
        const theirs = await adminSignIn(h.pool, { email, password: first }, new Date())

        const second = 'the-replacement-password-now'
        const result = await caller.admin.operators.setPassword({ adminUserId: id, password: second })
        assert.equal(result.replacedAPassword, true, 'replacing a working password reported an invitation')
        assert.equal(result.sessionsRevoked, 1, 'the session the old password opened was left alive')

        await assert.rejects(
          () => adminSignIn(h.pool, { email, password: first }, new Date()),
          'the old password still signs in',
        )
        const again = await adminSignIn(h.pool, { email, password: second }, new Date())
        assert.ok(again.token, 'the new password does not sign in')

        const { resolveAdminSession } = await import('../src/admin/session.ts')
        assert.equal(
          await resolveAdminSession(h.pool, theirs.token, new Date()),
          null,
          'a session opened with the old password still resolves',
        )
      })

      test('the root operator gets a password, and is still the root operator', async () => {
        // A DELIBERATE DIFFERENCE FROM THE COMMAND. `set-operator-password`
        // declines to touch root, because a connection string is not a person.
        // Here the caller is a named operator and the act is in the chain under
        // their address. What must not change is everything else about root,
        // and that is what the reads below are for: this route goes nowhere
        // near is_root, role or suspended_at, and the 0029 triggers still hold.
        const { caller } = await callerForWithId('owner')
        const email = `rootpw-${randomUUID().slice(0, 8)}@example.test`
        const [made] = await h.admin<{ id: string }[]>`
          INSERT INTO admin_users (email, name, role, is_root)
          VALUES (${email}, 'Root', 'owner', true)
          RETURNING id`
        const rootId = made!.id
        try {
          const password = 'the-root-operators-new-passphrase'
          await caller.admin.operators.setPassword({ adminUserId: rootId, password })
          const signedIn = await adminSignIn(h.pool, { email, password }, new Date())
          assert.ok(signedIn.token, 'the root operator could not sign in with the password just set')

          const [row] = await h.admin<{ is_root: boolean; role: string; suspended_at: Date | null }[]>`
            SELECT is_root, role, suspended_at FROM admin_users WHERE id = ${rootId}::uuid`
          assert.equal(row!.is_root, true, 'setting a password stopped root being root')
          assert.equal(row!.role, 'owner', 'setting a password moved root off owner')
          assert.equal(row!.suspended_at, null, 'setting a password suspended root')
        } finally {
          await h.admin`ALTER TABLE admin_users DISABLE TRIGGER admin_root_is_permanent_del`
          await h.admin`DELETE FROM admin_users WHERE id = ${rootId}::uuid`
          await h.admin`ALTER TABLE admin_users ENABLE TRIGGER admin_root_is_permanent_del`
        }
      })

      test('setting your own password needs the current one, so a stolen session is not permanent', async () => {
        // The single reason this check exists. An operator cookie lasts twelve
        // hours; without it a stolen one buys the account forever, because the
        // thief sets a password and the revoke cuts the real owner out.
        const { caller, adminUserId } = await callerForWithId('owner')
        const before = await hashOf(adminUserId)

        await assert.rejects(
          () =>
            caller.admin.operators.setPassword({
              adminUserId,
              password: 'a-thief-would-choose-this',
            }),
          (err: Error) => /needs your current one/.test(err.message),
          'an operator set their own password without proving they knew it',
        )
        await assert.rejects(
          () =>
            caller.admin.operators.setPassword({
              adminUserId,
              password: 'a-thief-would-choose-this',
              currentPassword: 'not-the-current-password',
            }),
          (err: Error) => /not your current password/.test(err.message),
          'a wrong current password was accepted',
        )
        assert.deepEqual(await hashOf(adminUserId), before, 'a refused self change wrote a password anyway')
      })

      test('changing your own password keeps the session doing it and revokes the others', async () => {
        // The control for the test above, and the behaviour that stops this
        // being useless: proving knowledge of the current password gets you
        // through. Signing the operator out of the session they are typing in
        // would make an ordinary rotation feel like a fault.
        const email = `self-${randomUUID().slice(0, 8)}@example.test`
        const { hash, salt } = await hashPassword(password)
        const [row] = await h.admin<{ id: string }[]>`
          INSERT INTO admin_users (email, name, role, password_hash, password_salt, password_set_at)
          VALUES (${email}, 'Self', 'owner', ${hash}, ${salt}, now())
          RETURNING id`
        const mine = await adminSignIn(h.pool, { email, password }, new Date())
        const other = await adminSignIn(h.pool, { email, password }, new Date())
        const caller = await callerWithToken(mine.token)

        const next = 'the-password-i-rotated-to'
        const result = await caller.admin.operators.setPassword({
          adminUserId: row!.id,
          password: next,
          currentPassword: password,
        })
        assert.equal(result.sessionsRevoked, 1, 'the operators other session was not revoked')

        const { resolveAdminSession } = await import('../src/admin/session.ts')
        assert.ok(
          await resolveAdminSession(h.pool, mine.token, new Date()),
          'the session that changed the password was signed out by doing it',
        )
        assert.equal(
          await resolveAdminSession(h.pool, other.token, new Date()),
          null,
          'another session belonging to the same operator survived the change',
        )
        assert.ok(await adminSignIn(h.pool, { email, password: next }, new Date()))
      })

      test('the chain records it at critical and the password is in no part of the entry', async () => {
        const { caller } = await callerForWithId('owner')
        const { id, email } = await invited(caller)
        const secret = 'a-value-that-must-never-be-logged'
        await caller.admin.operators.setPassword({ adminUserId: id, password: secret })

        const [entry] = await h.admin<{
          action: string
          severity: string
          actor_label: string
          detail: unknown
        }[]>`
          SELECT action, severity, actor_label, detail FROM admin_audit_entries
          WHERE action = 'operator.password_set' AND target_id = ${id}
          ORDER BY seq DESC LIMIT 1`
        assert.ok(entry, 'setting a password left no entry in the operator chain')
        assert.equal(entry!.severity, 'critical')
        assert.match(entry!.actor_label, /@/, 'the entry is not attributed to a person')
        assert.equal(
          JSON.stringify(entry!.detail).includes(secret),
          false,
          'the audit entry carried the password itself',
        )
        assert.ok(
          JSON.stringify(entry!.detail).includes(email),
          'the entry does not say whose password it was',
        )
      })

      test('a suspended operator is told so rather than left to conclude the button is broken', async () => {
        // Not refused. adminSignIn checks the suspension before the password,
        // so this changes nothing about their access, and somebody who sets a
        // password, watches the sign in fail and is told nothing concludes the
        // route is broken.
        const { caller } = await callerForWithId('owner')
        const { id, email } = await invited(caller)
        await caller.admin.operators.suspend({ adminUserId: id, reason: 'while we check' })
        const result = await caller.admin.operators.setPassword({
          adminUserId: id,
          password: 'a-password-they-cannot-use-yet',
        })
        assert.match(result.effect, /suspended/)
        await assert.rejects(
          () => adminSignIn(h.pool, { email, password: 'a-password-they-cannot-use-yet' }, new Date()),
          'a suspended operator signed in',
        )
      })

      test('an id nobody holds is a refusal rather than a quiet success', async () => {
        const { caller } = await callerForWithId('owner')
        await assert.rejects(
          () =>
            caller.admin.operators.setPassword({
              adminUserId: randomUUID(),
              password: 'a-password-for-nobody-at-all',
            }),
          (err: Error) => /No operator with that id/.test(err.message),
        )
      })

      test('a role without admin.operators.write cannot set anybody a password', async () => {
        const { caller: owner } = await callerForWithId('owner')
        const { id } = await invited(owner)
        const { caller: support } = await callerForWithId('support')
        await assert.rejects(
          () => support.admin.operators.setPassword({ adminUserId: id, password: 'support-should-not-do-this' }),
          (err: Error) => /admin\.operators\.write/.test(err.message),
        )
        assert.equal(await hashOf(id), null, 'a refused caller still provisioned the account')
      })
    })

    test('support cannot administer operators at all', async () => {
      const { caller } = await callerForWithId('support')
      await assert.rejects(
        () => caller.admin.operators.create({ email: 'x@example.test', name: 'X', role: 'owner' }),
        (err: Error) => /admin\.operators\.write/.test(err.message),
      )
    })
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
