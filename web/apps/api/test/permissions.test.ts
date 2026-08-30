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
import { declaredPermissions } from '../src/trpc.ts'
import { ROLES, ROLE_PERMISSIONS, roleHas, type Permission, type Role } from '../src/permissions.ts'
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
    'runs.list': { envId: org.envId },
    'runs.recent': { limit: 10 },
    'runs.get': { runId: '00000000-0000-0000-0000-000000000000' },
    'runs.verdicts': { runId: '00000000-0000-0000-0000-000000000000' },
    'runs.artifacts': { runId: '00000000-0000-0000-0000-000000000000' },
    'network.effective': { repository: org.repository },
    'network.explain': { host: 'api.stripe.com', tls: true, path: '/v1/charges' },
    'network.decisions': { limit: 10 },
    'network.propose': { repository: org.repository, host: 'api.example.test', mode: 'block' },
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
  }
}

/** Routes deliberately reachable with no session, each with a reason. */
const PUBLIC_ROUTES = new Map<string, string>([
  ['health', 'a liveness probe cannot hold a session'],
  ['permissions', 'describes the product, not any tenant; the docs table is built from it'],
])

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
    const declared = declaredPermissions()
    const undeclared = listProcedures()
      .map(({ path }) => path)
      .filter((path) => !declared.has(path) && !PUBLIC_ROUTES.has(path))

    assert.deepEqual(
      undeclared,
      [],
      `these routes are reachable with no permission check:\n  ${undeclared.join('\n  ')}\n` +
        'Build them with orgProcedure(permission), or add them to PUBLIC_ROUTES with the reason.',
    )
  })

  it('every route has a sample input, so none drops out of the matrix', () => {
    const inputs = inputsFor(org)
    const missing = listProcedures()
      .map(({ path }) => path)
      .filter((path) => !(path in inputs))
    assert.deepEqual(missing, [], `no sample input for: ${missing.join(', ')}`)
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

  // The matrix itself. One test per role per route, named so that a failure
  // says which cell broke.
  for (const role of ROLES) {
    describe(`as ${role}`, () => {
      for (const { path, type } of listProcedures()) {
        if (PUBLIC_ROUTES.has(path)) continue

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
