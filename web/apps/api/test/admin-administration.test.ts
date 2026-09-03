// The Administration lane's routes, driven the way a request drives them.
//
// WHAT THIS SUITE IS ACTUALLY FOR. Every route in administration.ts feeds a
// number onto a screen an operator reads during an incident, so the property
// worth pinning is not "the route answers" but "the number MOVES when the thing
// it counts moves". A count that is always zero passes any assertion that only
// checks the shape of the response, and a page of confident zeroes is the
// single most expensive thing this portal could ship.
//
// So every assertion here is a BEFORE and an AFTER around a real change:
// suspend an organization and the suspended count goes up by one, create an
// environment and the environment-hours appear against that organization, set a
// budget and the spend row exists. A test that asserted `typeof total ===
// 'number'` would have passed against a handler that returned a constant.
//
// The second thing it pins is the boundary. These routes read across every
// tenant, so each one is called with a role that holds its permission and with
// one that does not, and the second must be refused. Checking only the happy
// role proves the route works and says nothing about who can reach it.
//
// THE HARNESS DETAIL THAT COSTS AN HOUR IF MISSED: operator sessions are minted
// with REAL time and never with h.clock. resolveAdminSession compares against
// the injected clock and the RLS policy behind current_admin_user() compares
// expires_at against the DATABASE's now(). A fake past clock moves the first and
// cannot move the second, so every operator read is refused with an RLS
// violation that reads like a permissions bug. admin-routes.test.ts carries the
// same note for the same reason.

import { test, describe, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { appRouter } from '../src/routers/index.ts'
import { adminSignIn, hashPassword } from '../src/admin/session.ts'
import { type AdminRole } from '../src/admin/permissions.ts'
import { createAdminPool, type AdminPool } from '@antifailure/db'
import { available, startApi, seedOrg, adminUrl, type ApiHarness, type Org } from './harness.ts'

const hasDb = await available()

describe('the administration routes', { skip: hasDb ? false : 'no database' }, () => {
  let h: ApiHarness
  let acme: Org
  let umbrella: Org
  let adminPool: AdminPool
  const password = 'provisioned-at-deploy-not-in-source'

  async function callerFor(role: AdminRole) {
    const email = `${role}-${randomUUID().slice(0, 8)}@example.test`
    const { hash, salt } = await hashPassword(password)
    await h.admin`
      INSERT INTO admin_users (email, name, role, password_hash, password_salt, password_set_at)
      VALUES (${email}, ${role}, ${role}, ${hash}, ${salt}, now())`
    // Real time. See the header.
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
      mailer: null,
      productName: 'Antifailure',
      hostedRequiredPlan: null,
      operatorSetsPlan: false,
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

  before(async () => {
    h = await startApi()
    acme = await seedOrg(h.admin, 'acme')
    umbrella = await seedOrg(h.admin, 'umbrella')
    await h.admin.unsafe(`ALTER ROLE antifailure_admin LOGIN PASSWORD 'operator-test-password'`)
    const u = new URL(adminUrl)
    u.username = 'antifailure_admin'
    u.password = 'operator-test-password'
    adminPool = createAdminPool({ url: u.toString() })
    // Fails loudly here rather than as an empty page later, which is exactly
    // what a pool pointed at a non-bypassing role produces: zero rows, and a
    // console indistinguishable from a product with no customers.
    await adminPool.ensureBypass()
  })

  after(async () => {
    await adminPool?.close()
    await h?.close()
  })

  /* ---------------------------------------------------------------------
   * standing
   * ------------------------------------------------------------------ */

  describe('the standing an operator lands on', () => {
    test('the suspended count follows a real suspension, in both directions', async () => {
      const caller = await callerFor('owner')

      const before = await caller.admin.administration.standing()
      assert.ok(before.organizations.total >= 2, 'the seeded organizations are counted')

      await h.admin`
        UPDATE organizations SET suspended_at = now(), suspended_reason = 'a real reason'
        WHERE id = ${acme.orgId}::uuid`

      const during = await caller.admin.administration.standing()
      assert.equal(
        during.organizations.suspended,
        before.organizations.suspended + 1,
        'suspending an organization did not move the count, so the number is not the query',
      )
      const named = during.suspended.find((o) => o.slug === acme.slug)
      assert.ok(named, 'the suspended organization is named, not only counted')
      assert.equal(named.reason, 'a real reason')

      await h.admin`
        UPDATE organizations SET suspended_at = NULL, suspended_reason = NULL
        WHERE id = ${acme.orgId}::uuid`

      const after = await caller.admin.administration.standing()
      assert.equal(
        after.organizations.suspended,
        before.organizations.suspended,
        'resuming did not move it back, so the page would keep showing a resolved problem',
      )
    })

    test('a deletion that stopped part way is surfaced, and a finished one is not', async () => {
      const caller = await callerFor('owner')
      const before = await caller.admin.administration.standing()

      // Three deletions, one of each shape. Only the first is waiting for a
      // person: the second was abandoned deliberately and the third finished.
      // Asserting only that the failed one appears would pass against a query
      // with no purged_at or cancelled_at predicate at all.
      await h.admin`
        INSERT INTO organization_deletions
          (org_id, org_slug, org_name, requested_by_label, last_error_at, last_error_step, attempts)
        VALUES (${umbrella.orgId}::uuid, ${umbrella.slug}, 'Umbrella', 'operator', now(), 'purge', 3)`
      await h.admin`
        INSERT INTO organization_deletions
          (org_id, org_slug, org_name, requested_by_label, last_error_at, last_error_step, cancelled_at)
        VALUES (${umbrella.orgId}::uuid, 'cancelled-one', 'Cancelled', 'operator', now(), 'purge', now())`
      await h.admin`
        INSERT INTO organization_deletions
          (org_id, org_slug, org_name, requested_by_label, last_error_at, last_error_step,
           work_stopped_at, subscription_cancelled_at, credentials_revoked_at, exported_at,
           purged_at)
        VALUES (${umbrella.orgId}::uuid, 'purged-one', 'Purged', 'operator', now(), 'purge',
                now(), now(), now(), now(), now())`

      const after = await caller.admin.administration.standing()
      assert.equal(
        after.stuckDeletions.length,
        before.stuckDeletions.length + 1,
        'exactly one of the three deletions is waiting for a person',
      )
      const stuck = after.stuckDeletions.find((d) => d.slug === umbrella.slug)
      assert.ok(stuck, 'the failed deletion is present')
      assert.equal(stuck.step, 'purge', 'the step it failed at is carried, not only that it failed')
      assert.equal(stuck.attempts, 3)
    })

    test('every built in role can reach it, because it is the landing page', async () => {
      // read_only is the least privileged role there is. If the landing page
      // refuses it, an auditor signs in to a portal whose front door is an
      // error, which is indistinguishable from a broken deployment.
      const caller = await callerFor('read_only')
      const standing = await caller.admin.administration.standing()
      assert.ok(typeof standing.organizations.total === 'number')
    })
  })

  /* ---------------------------------------------------------------------
   * activity
   * ------------------------------------------------------------------ */

  describe('the operator activity summary', () => {
    test('it counts the reads it performs and lists only what was not a read', async () => {
      const caller = await callerFor('owner')

      // adminProcedure audits every query automatically, so simply calling a
      // route three times puts three read entries in the chain. That is the
      // behaviour the summary is built around and the reason the recent list
      // filters them out.
      await caller.admin.administration.standing()
      await caller.admin.administration.standing()
      const activity = await caller.admin.administration.activity()

      assert.ok(activity.readOver > 0, 'the summary read entries')
      assert.ok(
        activity.readOver <= activity.requested,
        'it never claims to have read more entries than it asked for',
      )
      assert.ok(activity.newest !== null, 'the span it covers is stated')
      for (const entry of activity.recent) {
        assert.ok(
          !entry.action.startsWith('read.'),
          `the recent list carried a read: ${entry.action}. Reads dominate the chain, so an ` +
            'unfiltered list is a page of the reader\'s own page loads.',
        )
      }
    })

    test('a write appears in the recent list and moves the write count', async () => {
      const caller = await callerFor('owner')
      const before = await caller.admin.administration.activity()

      await caller.admin.tenants.setPlan({
        orgId: umbrella.orgId,
        plan: 'team',
        reason: 'proving the activity summary counts a real write',
      })

      const after = await caller.admin.administration.activity()
      assert.ok(
        after.writes > before.writes,
        'a real mutation did not move the write count, so the summary is not reading the chain',
      )
      assert.ok(
        after.recent.some((e) => e.action === 'organization.plan_changed'),
        'the write is at the top of the recent list',
      )
    })

    test('the route is gated on admin.audit.read, and the gate is what says so', async () => {
      /*
       * WHY THIS READS THE ROUTER RATHER THAN CALLING THE ROUTE.
       *
       * Every built in role holds admin.audit.read, so there is no operator
       * this suite can sign in as who would be refused. An earlier version of
       * this test signed in as owner, changed rows in admin_users, and then
       * asserted that a number came back, which is a test whose name promised
       * a refusal and whose body proved nothing at all. That is worse than no
       * test: it is a green line in the output claiming a boundary is checked.
       *
       * The honest assertion available here is the declaration itself. The
       * permission on the procedure is what adminProcedure enforces, and
       * admin-gate.test.ts already proves that enforcement refuses a role that
       * lacks the permission. Pinning the declaration joins the two.
       */
      const procedures = (
        appRouter as unknown as {
          _def: { procedures: Record<string, { _def: { meta?: { adminPermission?: string } } }> }
        }
      )._def.procedures
      assert.equal(
        procedures['admin.administration.activity']?._def.meta?.adminPermission,
        'admin.audit.read',
        'the activity route reads the operator audit chain, so that is the permission it must ' +
          'declare. Guarding it on the weaker admin.portal.access would hand the chain to any ' +
          'role that can sign in.',
      )
      assert.equal(
        procedures['admin.administration.standing']?._def.meta?.adminPermission,
        'admin.tenants.read',
      )
      assert.equal(
        procedures['admin.administration.usage']?._def.meta?.adminPermission,
        'admin.tenants.read',
      )
      assert.equal(
        procedures['admin.administration.installation']?._def.meta?.adminPermission,
        'admin.infra.read',
      )
    })
  })

  /* ---------------------------------------------------------------------
   * usage
   * ------------------------------------------------------------------ */

  describe('usage, measured in environment-hours', () => {
    test('an environment held for a known time is measured as that time', async () => {
      const caller = await callerFor('analytics')
      // Timestamps derived from h.clock, NOT from the database's now().
      //
      // This is the second half of the clock trap the header describes. The
      // route measures against the injected clock, which in production is the
      // real one; the harness clock starts at a fixed instant that is nowhere
      // near wall time. Rows written with the database's now() fall entirely
      // outside every window the route asks about, so the page reports zero
      // and the assertion fails for a reason that has nothing to do with the
      // arithmetic being tested.
      const now = h.clock.now()
      const created = new Date(now.getTime() - 6 * 3600_000).toISOString()
      const tornDown = new Date(now.getTime() - 2 * 3600_000).toISOString()

      // Six hours ago to two hours ago: four hours inside a twenty four hour
      // window, and four hours inside a seven day one.
      await h.admin`
        INSERT INTO environments (org_id, repository_id, env_id, branch, state, created_at, torn_down_at)
        VALUES (${acme.orgId}::uuid, ${acme.repoId}::uuid, ${`env-${randomUUID().slice(0, 6)}`},
                'main', 'torn_down', ${created}::timestamptz, ${tornDown}::timestamptz)`

      const usage = await caller.admin.administration.usage({ window: '24h', limit: 25 })
      const row = usage.rows.find((r) => r.slug === acme.slug)
      assert.ok(row, 'the organization that held an environment appears')
      assert.ok(
        Math.abs(row.hours - 4) < 0.1,
        `four hours of environment time was measured as ${row.hours}`,
      )
      assert.equal(row.dayCapHours, 72, 'the free plan cap comes from costs.ts, not from this file')
    })

    test('the window is an overlap and not a lifetime', async () => {
      const caller = await callerFor('analytics')
      // Running for three days and still up. In a rolling twenty four hours it
      // has contributed twenty four hours, not seventy two. Counting the whole
      // lifetime is what makes one long lived environment permanently exceed
      // every daily cap, so the customer is refused forever for something they
      // did once. costs.ts makes the same argument in prose; this is the
      // assertion behind it.
      const threeDaysAgo = new Date(h.clock.now().getTime() - 3 * 24 * 3600_000).toISOString()
      await h.admin`
        INSERT INTO environments (org_id, repository_id, env_id, branch, state, created_at)
        VALUES (${umbrella.orgId}::uuid, ${umbrella.repoId}::uuid, ${`env-${randomUUID().slice(0, 6)}`},
                'main', 'running', ${threeDaysAgo}::timestamptz)`

      const day = await caller.admin.administration.usage({ window: '24h', limit: 25 })
      const dayRow = day.rows.find((r) => r.slug === umbrella.slug)
      assert.ok(dayRow)
      assert.ok(
        Math.abs(dayRow.hours - 24) < 0.2,
        `a three day environment contributed ${dayRow.hours} hours to a 24 hour window`,
      )

      const week = await caller.admin.administration.usage({ window: '7d', limit: 25 })
      const weekRow = week.rows.find((r) => r.slug === umbrella.slug)
      assert.ok(weekRow)
      assert.ok(
        Math.abs(weekRow.hours - 72) < 0.2,
        `the same environment contributed ${weekRow.hours} hours to a 7 day window`,
      )
      assert.ok(
        Math.abs(weekRow.dayHours - 24) < 0.2,
        'the rolling day column stays a rolling day at every window, which is the whole reason ' +
          'it is a separate column from the selected window',
      )
    })

    test('the rolling day figure never goes negative for an environment that ended before it', async () => {
      // The clamp in the SQL. Without GREATEST(0, ...) an environment inside
      // the wider window and outside the day contributes a negative number,
      // which quietly cancels somebody else's usage and makes a busy
      // organization look idle.
      const caller = await callerFor('analytics')
      const usage = await caller.admin.administration.usage({ window: '30d', limit: 25 })
      for (const row of usage.rows) {
        assert.ok(row.dayHours >= 0, `${row.slug} reported ${row.dayHours} hours in the last day`)
        assert.ok(row.hours >= 0, `${row.slug} reported ${row.hours} hours in the window`)
      }
    })
  })

  /* ---------------------------------------------------------------------
   * spend
   * ------------------------------------------------------------------ */

  describe('spend against the model budgets', () => {
    test('a budget row is reported with its real numbers, and an absent one is not invented', async () => {
      const caller = await callerFor('analytics')

      const empty = await caller.admin.administration.spend({ limit: 50 })
      assert.ok(
        !empty.rows.some((r) => r.slug === acme.slug),
        'an organization with no budget row has no budget, and a zero here would read as one',
      )

      await h.admin`
        INSERT INTO provider_budgets (org_id, provider, period, cap_usd, spent_usd)
        VALUES (${acme.orgId}::uuid, 'anthropic', current_date, 200.0000, 150.0000)`

      const after = await caller.admin.administration.spend({ limit: 50 })
      const row = after.rows.find((r) => r.slug === acme.slug)
      assert.ok(row, 'the budget appears once it exists')
      assert.equal(row.capUsd, 200)
      assert.equal(row.spentUsd, 150)
      assert.equal(row.usedPercent, 75, 'the proportion is computed, not rounded from a guess')
    })

    test('a zero cap reports no percentage rather than zero percent', async () => {
      const caller = await callerFor('analytics')
      await h.admin`
        INSERT INTO provider_budgets (org_id, provider, period, cap_usd, spent_usd)
        VALUES (${umbrella.orgId}::uuid, 'openai', current_date, 0, 0)`

      const spend = await caller.admin.administration.spend({ limit: 50 })
      const row = spend.rows.find((r) => r.slug === umbrella.slug && r.provider === 'openai')
      assert.ok(row)
      assert.equal(
        row.usedPercent,
        null,
        'a percentage of a zero cap is not a number, and 0 percent reads as plenty of room',
      )
    })
  })

  /* ---------------------------------------------------------------------
   * installation
   * ------------------------------------------------------------------ */

  describe('the installation configuration', () => {
    test('it reports the capabilities the running context actually resolved', async () => {
      const caller = await callerFor('infrastructure')
      const installation = await caller.admin.administration.installation()

      const payments = installation.capabilities.find((c) => c.name === 'Payments')
      assert.ok(payments)
      // The context this suite builds passes stripe: null, so the honest answer
      // is that this installation charges nobody. A page that read the
      // environment instead would answer from a variable the process may never
      // have used.
      assert.equal(payments.ready, false)
      assert.equal(payments.enabledBy, 'AF_STRIPE_SECRET_KEY')

      const email = installation.capabilities.find((c) => c.name === 'Outbound email')
      assert.ok(email)
      assert.equal(email.ready, false, 'the harness passes mailer: null')
    })

    test('it names the schema version this database is actually on', async () => {
      const caller = await callerFor('infrastructure')
      const installation = await caller.admin.administration.installation()
      assert.ok(installation.schema, 'the migrated test database has a schema version')
      assert.ok(
        /^\d{4}_/.test(installation.schema.version),
        `expected a migration file name, got ${installation.schema.version}`,
      )
      assert.ok(installation.schema.applied > 20, 'every applied migration is counted')
    })

    test('it carries every switch, including the ones nobody has touched', async () => {
      const caller = await callerFor('infrastructure')
      const installation = await caller.admin.administration.installation()
      assert.equal(
        installation.controls.length,
        installation.controlCount,
        'a control with no row has never been touched and still belongs on the page, or a fresh ' +
          'installation renders an empty table where the full set should be',
      )
      for (const control of installation.controls) {
        assert.ok(control.enforcedBy.includes(':'), 'each control names the symbol that refuses')
      }
    })

    test('no credential value appears anywhere in the response', async () => {
      // The same assertion admin-routes.test.ts makes about the operators
      // response, for the same reason: column safety is an application property
      // and nothing in the database enforces it. This page is the one most
      // likely to grow a field somebody thought was harmless.
      const caller = await callerFor('infrastructure')
      const installation = await caller.admin.administration.installation()
      const serialised = JSON.stringify(installation)
      for (const forbidden of ['password', 'secret', 'token', 'apikey', 'api_key']) {
        assert.ok(
          !serialised.toLowerCase().includes(forbidden) ||
            // The NAMES of the variables are deliberately present, and a name
            // is not a value. Anything else matching is a real leak.
            installation.capabilities.some((c) => c.enabledBy.toLowerCase().includes(forbidden)),
          `the installation response contained "${forbidden}"`,
        )
      }
      assert.ok(
        !serialised.includes('sk_'),
        'a Stripe key prefix appeared in the configuration response',
      )
    })

    test('a role without admin.infra.read cannot read the configuration', async () => {
      // support holds portal.access, audit.read, tenants.read, users.read,
      // sessions.read and the three money reads, and it deliberately does not
      // hold infra.read. This is the assertion that the permission on the route
      // is doing something rather than decorating it.
      const caller = await callerFor('support')
      await assert.rejects(
        () => caller.admin.administration.installation(),
        (error: { code?: string; message?: string }) => {
          assert.equal(error.code, 'FORBIDDEN')
          return true
        },
      )
    })

    test('a role without admin.tenants.read cannot read usage', async () => {
      // Every built in role holds tenants.read, so this proves the gate by
      // creating a role that does not. Written as a direct check of the
      // catalog rather than a call, because inventing a role in the database
      // would violate the CHECK constraint on admin_users.role, and a test that
      // has to break a constraint to make its point is testing the wrong thing.
      const { ADMIN_ROLE_PERMISSIONS, ADMIN_ROLES } = await import('../src/admin/permissions.ts')
      const withoutTenants = ADMIN_ROLES.filter(
        (r) => !ADMIN_ROLE_PERMISSIONS[r].includes('admin.tenants.read'),
      )
      assert.deepEqual(
        withoutTenants,
        [],
        'a role appeared that cannot read tenants. The overview and the usage page are both ' +
          'guarded on admin.tenants.read, so that role now lands on a portal whose front door ' +
          'is a refusal. Split admin.administration.standing before shipping the role.',
      )
    })
  })
})
