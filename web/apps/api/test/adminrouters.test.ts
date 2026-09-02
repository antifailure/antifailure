// The operator routes, exercised through the real procedures.
//
// `createCaller` rather than HTTP, and the difference is worth stating because
// it decides what these tests prove and what they do not.
//
// What they DO prove: the real `adminProcedure` middleware runs, so the
// permission check, the impersonation refusal and the automatic per request
// read auditing all happen; the real operator pool is used, so every read and
// write crosses the BYPASSRLS credential; the real ledger and the real Stripe
// client are behind the money routes. A route that forgot its permission, or
// named one no role holds, fails here.
//
// What they do NOT prove: the HTTP layer. These routes are composed into
// `adminRouter`, which is served on /trpc, and the cross-site guard for the
// operator cookie is asserted in server.ts's own tests rather than here. The
// split is deliberate: this file is about whether a route does the right thing
// for the right operator, and duplicating a transport assertion in it would be
// a second, weaker copy of somebody else's test.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { createHash, randomBytes, randomUUID } from 'node:crypto'
import postgres from 'postgres'
import { sql } from '@antifailure/db'
import { adminRouter } from '../src/admin/router.ts'
import { declaredAdminPermissions } from '../src/trpc.ts'
import { ADMIN_PERMISSIONS } from '../src/admin/permissions.ts'
import { hashPassword } from '../src/admin/session.ts'
import type { AdminContext } from '../src/admin/trpc.ts'
import type { AdminRole } from '../src/admin/permissions.ts'
import { adminUrl, available, dropOrg, seedOrg, startApi, stripeAgainstMockPack, type ApiHarness, type Org } from './harness.ts'


/** Asked once, at module scope, rather than inside each describe.
 *
 *  A `describe(async () => {...})` that awaits before registering its tests
 *  registers them after the runner has moved on, and every one of them is
 *  reported as "did not finish before its parent and was cancelled" rather than
 *  as a failure anybody can read. One await here and the skip option below is
 *  the shape that does not have that trap. */
const hasDatabase = await available()

describe('the operator money routes', { skip: hasDatabase ? false : 'no database' }, () => {
  let h: ApiHarness
  let org: Org
  let operatorId: string

  before(async () => {
    h = await startApi({ stripe: (await stripeAgainstMockPack()).billing })

    org = await seedOrg(h.admin, 'routes')
    const [row] = await h.admin<{ id: string }[]>`
      INSERT INTO admin_users (email, name, role)
      VALUES (${`routes-${randomUUID().slice(0, 8)}@example.test`}, 'Routes operator', 'owner')
      RETURNING id`
    operatorId = row!.id
  })

  after(async () => {
    await h.admin`DELETE FROM entitlement_overrides WHERE org_id = ${org.orgId}`
    await h.admin`DELETE FROM feature_flag_targets WHERE org_id = ${org.orgId}`
    await h.admin`DELETE FROM feature_flags WHERE key LIKE 'routes.%'`
    await h.admin`DELETE FROM admin_operations WHERE org_id = ${org.orgId}`
    await h.admin`DELETE FROM admin_audit_entries WHERE subject_org_id = ${org.orgId}`
    await dropOrg(h.admin, org.orgId)
    await h.close()
  })

  /** A caller as an operator of a given role, with the context the mount would
   *  build. Impersonating is false; the gate's refusal for a true one is
   *  admin-boundary.test.ts's, and duplicating it here would be a second,
   *  weaker copy of somebody else's assertion. */
  function callerAs(role: AdminRole, label = 'ops@antifailure.test') {
    const ctx = {
      pool: h.pool,
      adminPool: h.adminPool,
      admin: {
        adminUserId: operatorId,
        label,
        // The pool records `email` on the connection, not `label`. They are the
        // same string for a provisioned operator and the two fields exist
        // because one survives the row being deleted.
        email: label,
        role,
        sessionId: randomUUID(),
        impersonating: false,
        impersonatedUserId: null,
      },
      adminDb: <T,>(fn: (db: never) => Promise<T>) =>
        h.adminPool.withOperator({ adminUserId: operatorId, label }, fn as never),
      clock: h.clock,
      github: h.github,
      stripe: null,
      appBaseUrl: 'http://app.test/',
      mailer: null,
      productName: 'Antifailure',
      hostedRequiredPlan: null,
      actor: null,
      origin: 'admin',
      ip: '203.0.113.10',
    } as unknown as AdminContext
    return adminRouter.createCaller(ctx as never)
  }

  // -------------------------------------------------------------------------

  it('refuses a role that does not hold the permission, and says which one', async () => {
    // `analytics` holds portal access and reads nothing of this lane's.
    await assert.rejects(
      () => callerAs('analytics').entitlements.forOrganization({ orgId: org.orgId }),
      /needs the admin.entitlements.read permission/,
    )
  })

  it('lets a support operator READ billing and refuses the refund button', async () => {
    // The whole reason read is split from write. An on-call engineer answering
    // "why was this customer charged twice" must be able to look, and must not
    // be able to move money.
    const support = callerAs('support')
    const seen = await support.billing.customer({ orgId: org.orgId })
    assert.equal(seen.org.slug, org.slug)
    await assert.rejects(
      () =>
        support.billing.refund({
          orgId: org.orgId, chargeId: 'ch_1', reason: 'should never happen',
        }),
      /needs the admin.billing.write permission/,
    )
  })

  it('reports an organization that has never paid as such, rather than blank', async () => {
    const seen = await callerAs('owner').billing.customer({ orgId: org.orgId })
    assert.equal(seen.customer, null)
    assert.deepEqual(seen.invoices, [])
    // A screen that rendered nothing here would read as a failed load. The
    // organization and its plan are still returned so it can say so.
    assert.equal(seen.org.plan, 'free')
    // And the half of the answer that exists before anybody has checked out.
    // This is the route's only call site for getOrgBillingSummary, so an
    // assertion here is what keeps that function from being defined, exported,
    // tested and reached by nothing.
    assert.ok(seen.summary, 'the local billing summary is missing')
    assert.equal(seen.summary.plan, 'free')
    assert.equal(seen.summary.subscription, null)
    assert.equal(seen.summary.seats.limit, 5)
  })

  it('a grant changes what the entitlement route reports, and records who and why', async () => {
    const admin = callerAs('billing')
    const before = await admin.entitlements.forOrganization({ orgId: org.orgId })
    const envsBefore = before.entitlements.find((e) => e.key === 'environments')!
    assert.equal(envsBefore.value, 3)
    assert.equal(envsBefore.override, null)

    const granted = await admin.entitlements.grant({
      scope: 'organization', scopeId: org.orgId, orgId: org.orgId,
      feature: 'environments', value: 40,
      reason: 'Sold 40 on the bespoke 2026 contract', ticket: 'AF-118',
      expiresAt: null,
    })
    assert.equal(granted.replaced, null)

    const after = await admin.entitlements.forOrganization({ orgId: org.orgId })
    const envs = after.entitlements.find((e) => e.key === 'environments')!
    assert.equal(envs.value, 40)
    assert.equal(envs.planValue, 3)
    assert.equal(envs.override!.reason, 'Sold 40 on the bespoke 2026 contract')
    assert.equal(envs.override!.ticket, 'AF-118')

    // In BOTH chains, from the one adminAudit call: the operator's, and the
    // customer's own log, because capacity sold is something that happened to
    // them.
    const [operatorEntry] = await h.admin<{ severity: string; detail: Record<string, unknown> }[]>`
      SELECT severity, detail FROM admin_audit_entries
      WHERE subject_org_id = ${org.orgId} AND action = 'entitlement.granted'`
    assert.equal(operatorEntry!.severity, 'high')
    assert.equal(operatorEntry!.detail.value, 40)
    const [tenantEntry] = await h.admin<{ origin: string }[]>`
      SELECT origin FROM audit_entries
      WHERE org_id = ${org.orgId} AND action = 'entitlement.granted'`
    assert.equal(tenantEntry!.origin, 'admin')
  })

  it('replacing a grant revokes the old one rather than shadowing it', async () => {
    const admin = callerAs('billing')
    const again = await admin.entitlements.grant({
      scope: 'organization', scopeId: org.orgId, orgId: org.orgId,
      feature: 'environments', value: 60,
      reason: 'Expanded again at renewal', expiresAt: null,
    })
    // The point of REVOKE then INSERT rather than an upsert: "what was it
    // before, and who changed it" has an answer.
    assert.ok(again.replaced, 'the previous grant was not revoked')
    const rows = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM entitlement_overrides
      WHERE org_id = ${org.orgId} AND feature = 'environments' AND revoked_at IS NULL`
    assert.equal(Number(rows[0]!.n), 1, 'two live grants for one entitlement')
  })

  it('refuses a grant for an entitlement nothing reads, and one of the wrong type', async () => {
    const admin = callerAs('billing')
    await assert.rejects(
      () => admin.entitlements.grant({
        scope: 'global', scopeId: null, orgId: null,
        feature: 'quantum_environments', value: 9, reason: 'a limit nobody reads',
        expiresAt: null,
      }),
      /no entitlement called quantum_environments/,
    )
    await assert.rejects(
      () => admin.entitlements.grant({
        scope: 'organization', scopeId: org.orgId, orgId: org.orgId,
        feature: 'environments', value: true, reason: 'the wrong shape entirely',
        expiresAt: null,
      }),
      /environments is a number/,
    )
  })

  it('revoking says so when there was nothing to revoke', async () => {
    // Not a silent success. An operator who believes they just removed capacity
    // has to be told they did not.
    await assert.rejects(
      () => callerAs('billing').entitlements.revoke({
        id: randomUUID(), reason: 'nothing to remove',
      }),
      /already revoked or does not exist/,
    )
  })

  it('a flag says whether anything actually reads it', async () => {
    const admin = callerAs('billing')
    await admin.flags.set({
      key: 'routes.unread', description: 'A flag with no call site anywhere.',
      state: 'on', rolloutPercent: 0, internalOnly: false,
    })
    const { flags } = await admin.flags.list()
    const unread = flags.find((f) => f.key === 'routes.unread')!
    // The screen has to say this. A flag with no call site is a switch that
    // looks like a control, and finding that out during an incident, by
    // flipping it and watching nothing happen, is the worst possible moment.
    assert.equal(unread.known, false)
    assert.equal(unread.checkedAt, null)

    const known = flags.find((f) => f.key === 'billing.checkout')
    if (known) assert.ok(known.checkedAt, 'a known flag reported no call site')
  })

  it('killing a flag is a different event from turning it off, and turning it on clears the kill', async () => {
    const admin = callerAs('billing')
    await admin.flags.set({
      key: 'routes.killable', description: 'A flag to kill and revive.',
      state: 'on', rolloutPercent: 0, internalOnly: false,
    })
    await admin.flags.kill({ key: 'routes.killable', reason: 'Payments incident AF-990' })

    const [killed] = await h.admin<{ state: string; killed_reason: string; killed_by_label: string }[]>`
      SELECT state, killed_reason, killed_by_label FROM feature_flags WHERE key = 'routes.killable'`
    assert.equal(killed!.state, 'off')
    assert.equal(killed!.killed_reason, 'Payments incident AF-990')
    assert.equal(killed!.killed_by_label, 'ops@antifailure.test')

    const [entry] = await h.admin<{ severity: string }[]>`
      SELECT severity FROM admin_audit_entries WHERE action = 'flag.killed'
      ORDER BY seq DESC LIMIT 1`
    assert.equal(entry!.severity, 'high', 'a kill was recorded as routine')

    // Turning it back on clears the kill, so those three columns describe the
    // kill in force rather than the last one there ever was.
    await admin.flags.set({
      key: 'routes.killable', description: 'A flag to kill and revive.',
      state: 'on', rolloutPercent: 0, internalOnly: false,
    })
    const [revived] = await h.admin<{ killed_at: Date | null; killed_reason: string | null }[]>`
      SELECT killed_at, killed_reason FROM feature_flags WHERE key = 'routes.killable'`
    assert.equal(revived!.killed_at, null)
    assert.equal(revived!.killed_reason, null)
  })

  it('a deny target is recorded at a higher severity than an allow', async () => {
    const admin = callerAs('billing')
    // A key unique to this run. The assertion below reads audit entries by
    // target_id, and audit entries are append only by design, so a fixed key
    // makes the second run in the same database read the first run's rows and
    // fail on a list that is correct twice over. The suite must not depend on
    // the database being empty when it starts.
    const flag = `routes.rollout-${randomUUID().slice(0, 6)}`
    await admin.flags.set({
      key: flag, description: 'A flag to target.',
      state: 'targeted', rolloutPercent: 10, internalOnly: false,
    })
    await admin.flags.target({
      flagKey: flag, kind: 'organization', value: org.orgId,
      allow: true, orgId: org.orgId, reason: 'Design partner for the beta',
    })
    await admin.flags.target({
      flagKey: flag, kind: 'organization', value: org.orgId,
      allow: false, orgId: org.orgId, reason: 'Pulled out after a support escalation',
    })
    const rows = await h.admin<{ action: string; severity: string }[]>`
      SELECT action, severity FROM admin_audit_entries
      WHERE target_id = ${flag} ORDER BY seq ASC`
    assert.deepEqual(
      rows.map((r) => `${r.action}:${r.severity}`),
      ['flag.set:notice', 'flag.targeted:notice', 'flag.denied:high'],
    )
    // Upserted rather than duplicated, so one subject has one answer.
    const targets = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM feature_flag_targets WHERE flag_key = ${flag}`
    assert.equal(Number(targets[0]!.n), 1)
  })

  it('every money route refuses a reason that says nothing', async () => {
    // The ledger refuses an empty reason too. This one refuses a short one, at
    // the edge, so the operator is told which FIELD is wrong rather than which
    // action failed.
    await assert.rejects(
      () => callerAs('billing').billing.refund({
        orgId: org.orgId, chargeId: 'ch_1', reason: 'because',
      }),
      /too small|at least 8/i,
    )
  })

  it('says so when the installation takes no money, rather than failing obscurely', async () => {
    await assert.rejects(
      () => callerAs('billing').billing.refund({
        orgId: org.orgId, chargeId: 'ch_1', reason: 'A refund on an installation with no Stripe',
      }),
      /no Stripe configuration/,
    )
  })
})

// ---------------------------------------------------------------------------
// This lane's routes, in the tree that is actually served
// ---------------------------------------------------------------------------

describe('every money route is guarded, and none of them has quietly left the tree', () => {
  // admin-routes.test.ts already walks the whole operator tree and asserts no
  // route is unguarded. This is the part that walk cannot do for me: its
  // non-vacuity number is `atLeast: 18`, which is satisfied by admin-portal's
  // own routes alone. If all seventeen of mine were composed out of the tree
  // tomorrow, that assertion would still pass and my routes would be gone with
  // nothing red.
  //
  // So this counts MINE. The number is exact rather than a floor, because a
  // route appearing here that I did not add is as much worth knowing about as
  // one disappearing.
  const MINE = /^admin\.(billing|entitlements|flags)\./

  it('all seventeen are mounted, guarded, and under this lane\'s prefixes', () => {
    const operator = declaredAdminPermissions()
    const paths = [...operator.keys()].filter((p) => MINE.test(p)).sort()

    assert.equal(
      paths.length,
      17,
      `expected seventeen money routes in the served tree, found ${paths.length}: ${paths.join(', ')}`,
    )

    for (const path of paths) {
      const permission = operator.get(path)!
      // In the catalog, not merely a plausible string. A permission no role
      // holds refuses everybody, which reads as a broken page rather than as a
      // typo, and is the failure a matrix exists to catch.
      assert.ok(
        (ADMIN_PERMISSIONS as readonly string[]).includes(permission),
        `${path} declares ${permission}, which is not in ADMIN_PERMISSIONS`,
      )
      assert.ok(
        permission.startsWith('admin.billing.') ||
          permission.startsWith('admin.entitlements.') ||
          permission.startsWith('admin.flags.'),
        `${path} declares ${permission}, which belongs to another lane`,
      )
    }
  })

  it('the write half is separate from the read half on all three surfaces', () => {
    // The split that lets an on-call engineer read a customer's billing without
    // holding the refund button. Asserted structurally rather than trusted,
    // because a route added later with the read permission by mistake is
    // exactly how that split quietly stops existing.
    const operator = declaredAdminPermissions()
    const writes = [...operator.entries()].filter(
      ([p, perm]) => MINE.test(p) && perm.endsWith('.write'),
    )
    const reads = [...operator.entries()].filter(
      ([p, perm]) => MINE.test(p) && perm.endsWith('.read'),
    )
    assert.ok(writes.length >= 9, `expected at least the nine money writes, found ${writes.length}`)
    assert.ok(reads.length >= 3, `expected a read on each surface, found ${reads.length}`)

    // Every route that MOVES MONEY holds the billing write permission and no
    // weaker one.
    for (const name of ['refund', 'credit', 'changePlan', 'extendTrial', 'cancel',
                        'reactivate', 'discount', 'retryPayment', 'resendInvoice']) {
      assert.equal(
        operator.get(`admin.billing.${name}`),
        'admin.billing.write',
        `admin.billing.${name} does not require admin.billing.write`,
      )
    }
  })
})
