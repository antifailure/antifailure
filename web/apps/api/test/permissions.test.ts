// The permission matrix: every route, against every role.
//
// Phase 8.2's exit criterion is that this is green for every route, and the
// part that makes it worth anything is that the route list is not written here.
// It is read out of the router at load time, so a route added tomorrow is in
// the matrix tomorrow whether or not anybody remembered.
//
// Two failures are checked for, and they are different:
//
// A role that should be refused and is allowed is a privilege escalation.
//
// A role that should be allowed and is refused is a broken product, and it is
// the one a matrix test usually misses, because a test written as "assert
// forbidden" passes just as well when the endpoint is broken for everyone.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { listProcedures } from '../src/openapi.ts'
import { declaredAdminPermissions, declaredPermissions } from '../src/trpc.ts'
import { ADMIN_PERMISSIONS } from '../src/admin/permissions.ts'
import {
  PERMISSIONS, ROLES, ROLE_PERMISSIONS, roleHas, type Permission, type Role,
} from '../src/permissions.ts'
import {
  available, startApi, seedOrg, signInAs, callProcedure, errorCode, dropOrg,
  type ApiHarness, type Org, type SignedIn,
} from './harness.ts'

const hasDatabase = await available()

/**
 * Sample input per procedure, so that a route is reached rather than rejected
 * by its own validation before the permission check runs.
 *
 * Missing entries are a failure, not a skip: a route with no sample here would
 * silently drop out of the matrix, which is exactly the hole the matrix exists
 * to close.
 */
function inputsFor(org: Org): Record<string, unknown> {
  return {
    'health': {},
    'permissions': {},
    'repositories.list': { includeArchived: false },
    'environments.list': { limit: 10 },
    'environments.get': { envId: org.envId },
    'environments.teardown': { envId: org.envId },
    // Named rather than left to its default, because a route with no entry here
    // drops out of the matrix, which is the hole this file exists to close. The
    // window is bounded at 720 hours by the schema; 24 is the default and the
    // only value the console asks for.
    'environments.costs': { hours: 24 },
    // The dispatch verbs. This fixture has no GitHub App installation, so a
    // role that holds the permission reaches the handler and gets
    // PRECONDITION_FAILED, which is what the matrix accepts and what proves the
    // gate let the call through rather than what GitHub then made of it.
    'environments.create': { repository: org.repository, branch: 'main' },
    // Guarded by environments.create, because it answers one question about
    // that one route: whether pressing it would work. It refuses nothing, so
    // an allowed role gets an answer here rather than PRECONDITION_FAILED.
    'environments.readiness': { repository: org.repository },
    'agents.run': { envId: org.envId },
    'load.run': { envId: org.envId },
    'runs.list': { envId: org.envId },
    'runs.recent': { limit: 10 },
    'runs.get': { runId: '00000000-0000-0000-0000-000000000000' },
    'runs.verdicts': { runId: '00000000-0000-0000-0000-000000000000' },
    'runs.artifacts': { runId: '00000000-0000-0000-0000-000000000000' },
    'network.effective': { repository: org.repository },
    'network.explain': { host: 'api.stripe.com', tls: true, path: '/v1/charges' },
    'network.decisions': { limit: 10 },
    'network.propose': { repository: org.repository, host: 'api.example.test', mode: 'block' },
    'network.pending': { repository: org.repository },
    // A rule id that is deliberately not a pending rule. An admin reaches the
    // handler and gets NOT_FOUND, so the matrix proves the gate without
    // approving an egress change as a side effect of running the tests.
    'network.approve': { ruleId: '00000000-0000-0000-0000-000000000000' },
    'masking.rules': { repository: org.repository },
    'masking.attestations': { repository: org.repository },
    'masking.propose': { repository: org.repository, table: 'users', column: 'email', transform: 'email' },
    'masking.approve': { repository: org.repository, table: 'users', column: 'email' },
    'audit.list': { limit: 10 },
    'audit.verify': {},
    'audit.export': { format: 'json' },
    'members.list': {},
    'members.sync': {},
    'members.setRole': { githubLogin: 'nobody-here', role: 'member' },
    'runtimes.list': { includeRemoved: false },
    // Two roles hold runtimes.manage, so the second one to run gets
    // BAD_REQUEST for a name that is already registered. The matrix accepts
    // that as the gate having let the call through, which is all it claims to
    // test; what the route actually does is proved in verbs.test.ts.
    'runtimes.register': { name: 'matrix', provider: 'local', labels: [] },
    'runtimes.tag': { name: 'nothing-registered-here', labels: [] },
    'runtimes.remove': { name: 'nothing-registered-here' },
    // The dashboard, which is guarded twice: by analytics.read here, and again
    // by membership of the organization that operates the installation. This
    // fixture is not that organization, so a role that HOLDS the permission
    // reaches the handler and gets PRECONDITION_FAILED or FORBIDDEN, which is
    // what the matrix accepts and what proves the permission gate let the call
    // through. The second gate has its own tests, in analytics-dashboard.test.ts.
    'analytics.overview': { days: 28 },
    'analytics.series': { days: 28, name: 'site.page_viewed' },
    'analytics.catalog': {},
    'billing.get': {},
    // The plan the fixture already has, so the matrix cannot change what an
    // organization is on as a side effect: `set` treats that as a no-op.
    'billing.set': { plan: 'free' },
    'tokens.list': {},
    'tokens.revoke': { id: '00000000-0000-0000-0000-000000000000' },
    'org.status': {},
    'subscriptions.current': {},
    'subscriptions.invoices': { limit: 10 },
    'subscriptions.checkout': {
      plan: 'team', seats: 1,
      successUrl: 'https://app.test/billing/done',
      cancelUrl: 'https://app.test/billing',
    },
    'subscriptions.portal': { returnUrl: 'https://app.test/billing' },
    'subscriptions.cancel': { reason: 'testing the matrix' },
    'subscriptions.reconcile': {},
    'org.suspend': { reason: 'testing the matrix' },
    'org.resume': {},

    // Running the organization.
    //
    // Every input below is chosen so that a role holding the permission
    // REACHES the handler and then gets a refusal that is about the input
    // rather than about the role: a name that is already the fixture's name, an
    // identifier that is deliberately not a row, a confirmation string that is
    // deliberately wrong. The matrix accepts NOT_FOUND, BAD_REQUEST and
    // PRECONDITION_FAILED as the gate having let the call through, which is all
    // it claims to test, and this way running the tests cannot rename the
    // fixture, delete it, or close the four accounts it signs in as.
    'org.settings': {},
    'org.rename': { name: 'matrix' },
    'org.billingContact': {},
    'org.setBillingContact': { email: 'finance@matrix.test' },
    'invitations.list': {},
    // Two roles hold members.manage, so the second one to run finds an open
    // invitation for the same address and gets BAD_REQUEST. That is the gate
    // having let it through; what the route actually does is proved in
    // enterprise.test.ts.
    'invitations.create': { email: 'matrix-invite@example.test', role: 'member' },
    'invitations.resend': { id: '00000000-0000-0000-0000-000000000000' },
    'invitations.revoke': { id: '00000000-0000-0000-0000-000000000000' },
    'sessions.list': { includeRevoked: false },
    'sessions.revoke': { id: '00000000-0000-0000-0000-000000000000' },
    // A login that is deliberately nobody, so the matrix cannot sign its own
    // fixtures out halfway through its own run.
    'sessions.revokeForPerson': { githubLogin: 'nobody-here' },
    'members.remove': { githubLogin: 'nobody-here' },
    'exports.organization': {},
    'deletion.status': {},
    // Deliberately not the slug, so the confirmation refuses and the matrix
    // cannot start deleting the organization it is running against.
    'deletion.request': { confirm: 'not-the-slug', reason: 'testing the matrix' },
    'deletion.advance': {},
    'deletion.cancel': {},
    'deletion.destroyExport': {},
    'account.context': {},
    // Deliberately not anybody's login, for the same reason.
    'account.close': { confirm: 'not-your-login' },
    // Studio. Every read and every write here names something that is
    // deliberately not in this fixture, so the handler is reached and answers
    // NOT_FOUND, which is what the matrix accepts as proof that the permission
    // gate let the call through. What each route then does is proved in
    // workloads.test.ts against a fixture built for it.
    'workloads.list': { limit: 10, includeArchived: false },
    'workloads.get': { slug: 'nothing-defined-here' },
    'workloads.runs': { limit: 10 },
    'workloads.inspect': { runId: '00000000-0000-0000-0000-000000000000' },
    'workloads.addVersion': { slug: 'nothing-defined-here', body: { select: [] } },
    'workloads.archive': { slug: 'nothing-defined-here' },
    'workloads.start': { slug: 'nothing-defined-here', envId: org.envId },
    'workloads.cancel': { runId: '00000000-0000-0000-0000-000000000000' },
    'workloads.retry': { runId: '00000000-0000-0000-0000-000000000000' },
    // Three roles hold workloads.edit, so the second and third to run get
    // BAD_REQUEST for a slug that is already taken. The matrix accepts that as
    // the gate having let the call through, which is all it claims to test.
    'workloads.create': {
      repository: org.repository,
      slug: 'matrix',
      name: 'Matrix',
      kind: 'browser_workflow',
      body: { select: ['sign-up'] },
    },
    // A real exploration document, so the compiler runs rather than the route
    // refusing at its own boundary. A repeat is the same digest and answers
    // created: false, which is a 200 and not a side effect.
    'workloads.promote': {
      repository: org.repository,
      exploration: {
        name: 'matrix promotion',
        goal: 'reach the billing page',
        seed: 'matrix',
        reached: true,
        journey: [{ kind: 'goto', url: 'http://env.test/pricing' }],
        findings: [],
        missing: [],
      },
    },
  }
}

/** Routes deliberately reachable with no session, each with a reason. */
const PUBLIC_ROUTES = new Map<string, string>([
  ['health', 'a liveness probe cannot hold a session'],
  ['permissions', 'describes the product, not any tenant; the docs table is built from it'],
])

/**
 * The instrument, before its answer.
 *
 * EVERY ASSERTION IN THIS FILE IS SATISFIED BY FINDING NOTHING, and that is not
 * a figure of speech about one of them. The matrix itself is BUILT from
 * `listProcedures()`: the cells below are generated inside
 * `for (const { path } of listProcedures())`, so a route list that came back
 * empty would not fail this file, it would EMIT NO TESTS AT ALL. Zero cells,
 * zero refusals checked, zero escalations found, and a green run reporting that
 * the permission matrix passed.
 *
 * The three scans above the matrix fail the same way and more quietly, because
 * they at least run. "Every route declares a permission" filters an empty list
 * and finds nothing undeclared. "Every route has a sample input" filters an
 * empty list and finds nothing missing. Both are the assertion this file exists
 * to make, and both are satisfied by a router that handed over nothing.
 *
 * The header of this file says the matrix is worth something precisely because
 * the route list is not written here but read out of the router. That is true,
 * and it is exactly why the read has to be checked: a list nobody wrote is a
 * list nobody notices the absence of.
 *
 * Deliberately OUTSIDE the matrix's describe, so it is not skipped when there
 * is no Postgres. None of it touches a database, and an instrument check that
 * only runs on the machines that already run everything is not much of a check.
 */
describe('the matrix is generated from a route list, so the route list has to be there', () => {
  it('the router hands over a plausible number of routes', () => {
    const routes = listProcedures()
    assert.ok(
      routes.length >= 60,
      `listProcedures() returned ${routes.length} routes. The matrix below is generated from ` +
        'this list, so a short one does not fail it, it silently shrinks it. Every assertion ' +
        'in this file is then satisfied by having examined nothing.',
    )
  })

  it('it names routes this file is certain exist', () => {
    // Named rather than only counted, because a list of the right length made
    // of the wrong things passes a count. One route per shape the matrix
    // depends on: a query, a mutation, one guarded by a permission only an
    // owner holds, and one deliberately public.
    const paths = new Set(listProcedures().map((r) => r.path))
    for (const path of ['environments.list', 'environments.teardown', 'billing.set', 'health']) {
      assert.ok(paths.has(path), `listProcedures() no longer names ${path}, so the scan is wrong`)
    }
  })

  it('every route it names carries the type the matrix calls it with', () => {
    // The matrix calls each route as `type`, and a route arriving with neither
    // 'query' nor 'mutation' would be called wrongly and refused for a reason
    // that has nothing to do with permissions, which reads as a passing cell.
    const wrong = listProcedures().filter((r) => r.type !== 'query' && r.type !== 'mutation')
    assert.deepEqual(wrong.map((r) => `${r.path}: ${r.type}`), [])
  })

  it('the roles and permissions it crosses the routes with are not empty either', () => {
    // ROLES is the outer loop of the matrix and PERMISSIONS is what the catalog
    // assertions are made against. Either one empty is the same defect from a
    // different direction.
    assert.ok(ROLES.length >= 4, `only ${ROLES.length} roles, so the matrix has almost no rows`)
    assert.ok(
      PERMISSIONS.length >= 20,
      `only ${PERMISSIONS.length} permissions, so the catalog assertions check almost nothing`,
    )
    assert.ok(
      declaredPermissions().size >= 60,
      `only ${declaredPermissions().size} routes declare a permission; the matrix reads this map ` +
        'to decide what each cell should expect, and an empty one expects nothing of anybody',
    )
  })
})

describe('permission matrix', { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let h: ApiHarness
  let org: Org
  const sessions = new Map<Role, SignedIn>()

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'matrix')
    for (const role of ROLES) {
      sessions.set(role, await signInAs(h, org, role))
    }
  })

  after(async () => {
    await dropOrg(h.admin, org.orgId)
    await h.close()
  })

  it('every route declares a permission or is deliberately public', () => {
    // Operator routes declare a PLATFORM permission and are walked by their own
    // matrix in admin-routes.test.ts, which asserts the same three properties
    // against the platform catalog.
    //
    // Skipped by DECLARATION and never by path prefix. A prefix skip would let
    // a route named `admin.something` that declares nothing at all fall through
    // this test AND be absent from the platform one, guarded by neither and
    // visible to no test, which is worse than the problem it solves.
    const declared = declaredPermissions()
    const operatorRoutes = declaredAdminPermissions()
    const undeclared = listProcedures()
      .map(({ path }) => path)
      .filter(
        (path) =>
          !declared.has(path) && !PUBLIC_ROUTES.has(path) && !operatorRoutes.has(path),
      )

    assert.deepEqual(
      undeclared,
      [],
      `these routes are reachable with no permission check:\n  ${undeclared.join('\n  ')}\n` +
        'Build them with orgProcedure(permission) or adminProcedure(adminPermission), ' +
        'or add them to PUBLIC_ROUTES with the reason.',
    )
  })

  it('every operator route declares a real platform permission', () => {
    // The other half. A route could declare `adminPermission` as any string
    // and satisfy the skip above; this requires the string to be one the
    // platform catalog actually knows, so a typo is a failure rather than a
    // silently unguarded operator route.
    const known = new Set<string>(ADMIN_PERMISSIONS)
    const bogus = [...declaredAdminPermissions().entries()].filter(
      ([, permission]) => !known.has(permission),
    )
    assert.deepEqual(
      bogus.map(([path, permission]) => `${path} declares ${permission}`),
      [],
      'these operator routes declare a permission the platform catalog does not contain',
    )
    // And every operator route must be under the admin. prefix, because the
    // maintenance-mode middleware in server.ts exempts /trpc/admin. so the
    // switches stay reachable while the installation is paused. One mounted
    // elsewhere is refused during maintenance, which locks the operator away
    // from the control that releases it.
    const misplaced = [...declaredAdminPermissions().keys()].filter(
      (path) => !path.startsWith('admin.'),
    )
    assert.deepEqual(
      misplaced,
      [],
      'these operator routes are not under the admin. prefix, so maintenance mode refuses them',
    )
  })

  it('every route has a sample input, so none drops out of the matrix', () => {
    // Operator routes are excluded for the same reason and by the same test as
    // above: they are driven by their own matrix against the platform catalog,
    // and they take an operator session this org-scoped harness does not have.
    // Excluded by DECLARATION, so an operator route that declares nothing is
    // still missing here and still fails.
    const inputs = inputsFor(org)
    const operatorRoutes = declaredAdminPermissions()
    const missing = listProcedures()
      .map(({ path }) => path)
      .filter((path) => !(path in inputs) && !operatorRoutes.has(path))
    assert.deepEqual(missing, [], `no sample input for: ${missing.join(', ')}`)
  })

  /**
   * The inverse of the assertion above, and the one that was missing.
   *
   * "Every route declares a permission" catches an endpoint added without a
   * guard. It says nothing at all about a permission that guards nothing, and
   * six of them did: environments.create, network.approve, agents.run,
   * load.run, billing.manage and runtimes.manage. Every one of them was
   * described in PERMISSION_DESCRIPTIONS, granted to roles, and rendered in the
   * documentation table a customer's security team reads, and none of them
   * could be exercised by anybody. A permission with no route is a feature the
   * product claims and does not have, and it is invisible from every direction
   * except this one.
   *
   * There is no exception list on purpose. A permission that is genuinely not
   * ready to be routed should not be in the catalog yet, because the catalog is
   * what the documentation is generated from.
   */
  it('every permission in the catalog guards at least one route', () => {
    const used = new Set(declaredPermissions().values())
    const unrouted = PERMISSIONS.filter((p) => !used.has(p))
    assert.deepEqual(
      unrouted,
      [],
      `these permissions guard no route, so nobody can exercise them:\n  ${unrouted.join('\n  ')}\n` +
        'Either build the route, or take the permission out of the catalog it documents.',
    )
  })

  it('the catalog and the router agree about which permissions exist', () => {
    const used = new Set(declaredPermissions().values())
    const granted = new Set(Object.values(ROLE_PERMISSIONS).flat())
    for (const permission of used) {
      assert.ok(
        granted.has(permission),
        `${permission} guards a route but no role has it, so the route is unreachable`,
      )
    }
  })

  /** Operator routes, excluded from the TENANT matrix by declaration rather
   *  than by path, so one that declares nothing still fails above. */
  const operatorPaths = declaredAdminPermissions()

  // The matrix itself. One test per role per route, named so that a failure
  // says which cell broke.
  for (const role of ROLES) {
    describe(`as ${role}`, () => {
      const adminPaths = declaredAdminPermissions()
      for (const { path, type } of listProcedures()) {
        if (adminPaths.has(path)) continue
        if (PUBLIC_ROUTES.has(path)) continue
        // Operator routes take an operator session, which this org-scoped
        // harness cannot mint. admin-routes.test.ts drives them with one.
        if (operatorPaths.has(path)) continue

        it(`${type} ${path}`, async () => {
          const permission = declaredPermissions().get(path) as Permission
          assert.ok(permission, `${path} declares no permission`)

          const session = sessions.get(role)!
          const { status, body } = await callProcedure(h, session, path, type, inputsFor(org)[path])
          const code = errorCode(body)
          const shouldAllow = roleHas(role, permission)

          if (shouldAllow) {
            assert.notEqual(
              code,
              'FORBIDDEN',
              `${role} holds ${permission} but ${path} refused it`,
            )
            // NOT_FOUND, BAD_REQUEST and PRECONDITION_FAILED are fine: the
            // sample input names rows that may not exist, and a route may need
            // state this fixture does not set up -- members.sync wants a GitHub
            // App installation. What this matrix asserts is that the permission
            // gate let the call through to the handler, not what the handler
            // then made of an organization seeded for a different purpose.
            assert.ok(
              status === 200 ||
                code === 'NOT_FOUND' ||
                code === 'BAD_REQUEST' ||
                code === 'PRECONDITION_FAILED',
              `${role} calling ${path} got ${status} ${code}: ${JSON.stringify(body).slice(0, 200)}`,
            )
          } else {
            assert.equal(
              code,
              'FORBIDDEN',
              `${role} does not hold ${permission} but ${path} answered ${status} ${code}`,
            )
          }
        })
      }
    })
  }

  it('no session at all is unauthorized, not forbidden and not allowed', async () => {
    for (const { path, type } of listProcedures()) {
      if (PUBLIC_ROUTES.has(path)) continue
      if (operatorPaths.has(path)) continue
      const { body } = await callProcedure(h, null, path, type, inputsFor(org)[path])
      assert.equal(
        errorCode(body),
        'UNAUTHORIZED',
        `${path} answered something other than unauthorized with no session`,
      )
    }
  })

  it('a member removed from the organization loses access on the next request', async () => {
    const session = await signInAs(h, org, 'admin', 'departing')
    const before = await callProcedure(h, session, 'environments.list', 'query', { limit: 5 })
    assert.equal(before.status, 200, 'the fixture never had access to begin with')

    await h.admin`DELETE FROM members WHERE user_id = ${session.userId}`

    // The role is read on every request rather than carried in the session, so
    // this takes effect now and not at the end of whatever session they hold.
    const after = await callProcedure(h, session, 'environments.list', 'query', { limit: 5 })
    assert.equal(
      errorCode(after.body),
      'UNAUTHORIZED',
      'a removed member kept access through their existing session',
    )
  })

  it('a role change takes effect on the next request', async () => {
    const session = await signInAs(h, org, 'admin', 'demoted')
    const before = await callProcedure(h, session, 'members.setRole', 'mutation', {
      githubLogin: 'nobody-here',
      role: 'member',
    })
    assert.notEqual(errorCode(before.body), 'FORBIDDEN', 'an admin could not manage members')

    await h.admin`UPDATE members SET role = 'viewer' WHERE user_id = ${session.userId}`

    const after = await callProcedure(h, session, 'members.setRole', 'mutation', {
      githubLogin: 'nobody-here',
      role: 'member',
    })
    assert.equal(errorCode(after.body), 'FORBIDDEN', 'a demoted member kept the old role')
  })
})
