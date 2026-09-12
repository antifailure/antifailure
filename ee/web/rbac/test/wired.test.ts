// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

// Custom roles, defined through the routes, applied by the resolver, observed at
// a real tRPC procedure.
//
// THE CLAIM THIS FILE EXISTS TO MAKE TRUE is the one three buyer facing files
// made while it was false: that an enterprise organization can define a role,
// grant it a set of permissions, assign it to a member, and have a request by
// that member allowed or refused accordingly. Every piece of that existed and
// none of it was joined, and every unit test passed, so the assertions here are
// about the joined path only. A policy file goes in over HTTP, a viewer's
// session calls environments.readiness through the real permission middleware,
// and the answer is read off the response.
//
// Why environments.readiness. It needs environments.create, which a viewer
// does not hold, it takes a repository, so a scoped grant can be told apart
// from an organization wide one, and it writes nothing, so a test that is
// allowed through changes no state that the next case would see.
//
// Every case is red then green, or green then red, on the SAME session. A role
// change that only took effect for a new session would pass a test that made a
// new session for each half, and it would be the revocation that never lands.

import { after, before, beforeEach, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import {
  clearExtensions,
  issueSession,
  registerExtension,
  setPermissionResolver,
  type Permission,
} from '@antifailure/api'
import { createPool, sql } from '@antifailure/db'
import {
  appUrl,
  available,
  callProcedure,
  dropOrg,
  errorCode,
  seedOrg,
  signInAs,
  startApi,
  type ApiHarness,
  type Org,
  type SignedIn,
} from '../../../../web/apps/api/test/harness.ts'
import {
  customRoleResolver,
  lockModel,
  rbacExtension,
  readModel,
  toYAML,
  writeModel,
  type CustomRole,
  type Grant,
  type PolicyFile,
  type RepositoryGroup,
} from '../src/index.ts'

const hasDatabase = await available()

// The licence key half of the gate, flipped by the case that withdraws it. The
// organization's own entitlement is the other half and is changed in the
// database, the way an operator changes it.
let installationPermits = true
const logged: string[] = []

const deployer: CustomRole = {
  id: 'deployer',
  name: 'Deployer',
  description: 'Brings environments up, and nothing else.',
  permissions: ['environments.view', 'environments.create'],
}

function policy(
  roles: CustomRole[],
  grants: Grant[] = [],
  groups: RepositoryGroup[] = [],
  approvals: PolicyFile['approvals'] = [],
): string {
  return toYAML({ version: 1, roles, grants, groups, approvals })
}

function onRepository(userId: string, repository: string, roleId = 'deployer'): Grant {
  return { userId, roleId, scope: { kind: 'repository', name: repository } }
}

describe('custom roles, end to end', { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let h: ApiHarness
  let org: Org
  let owner: SignedIn
  let viewer: SignedIn
  let other: string
  const orgs: string[] = []

  before(async () => {
    clearExtensions()
    // Registered before the server is built, because the server mounts the
    // extension routes it can see at construction. That is the order the
    // enterprise entry point uses too.
    registerExtension(rbacExtension({ pool: lazyPool(), clock: lazyClock(), log: (l) => logged.push(l) }))
    h = await startApi()
    boundPool = h.pool
    boundClock = h.clock
    setPermissionResolver(
      customRoleResolver({
        pool: h.pool,
        clock: h.clock,
        installationPermits: () => installationPermits,
        log: (l) => logged.push(l),
      }),
    )
  })

  after(async () => {
    setPermissionResolver(null)
    clearExtensions()
    for (const id of orgs) await dropOrg(h.admin, id)
    await h.close()
  })

  beforeEach(async () => {
    installationPermits = true
    org = await seedOrg(h.admin, 'rbac')
    orgs.push(org.orgId)
    await h.admin`UPDATE organizations SET plan = 'enterprise' WHERE id = ${org.orgId}`
    other = `${org.slug}/other`
    owner = await signInAs(h, org, 'owner')
    viewer = await signInAs(h, org, 'viewer')
  })

  // -------------------------------------------------------------------------
  // The helpers speak HTTP, the way the console and a CI job would.
  // -------------------------------------------------------------------------

  async function put(session: SignedIn, body: string) {
    // The write limit refills one token every two seconds, and the harness
    // clock only moves when somebody moves it.
    h.clock.advance(2_500)
    const res = await h.fetch('/roles/policy', {
      method: 'PUT',
      headers: {
        cookie: session.cookie,
        'x-antifailure-csrf': session.csrfToken,
        'content-type': 'application/yaml',
      },
      body,
    })
    return { status: res.status, body: (await res.json()) as Record<string, unknown> }
  }

  async function get(session: SignedIn | null, path: string) {
    h.clock.advance(600)
    const res = await h.fetch(path, { headers: session ? { cookie: session.cookie } : {} })
    const text = await res.text()
    let body: unknown = text
    try {
      body = JSON.parse(text)
    } catch {
      // YAML, or a refusal; the assertion says which.
    }
    return { status: res.status, body, text }
  }

  async function readiness(session: SignedIn, repository: string) {
    return callProcedure(h, session, 'environments.readiness', 'query', {
      repository,
      workflow: 'antifailure.yml',
    })
  }

  async function assertAllowed(session: SignedIn, repository: string, why: string) {
    const r = await readiness(session, repository)
    assert.equal(r.status, 200, `${why}: expected the request through, got ${r.status} ${JSON.stringify(r.body)}`)
  }

  async function assertRefused(session: SignedIn, repository: string, why: string) {
    const r = await readiness(session, repository)
    assert.equal(r.status, 403, `${why}: expected a refusal, got ${r.status} ${JSON.stringify(r.body)}`)
    assert.equal(errorCode(r.body), 'FORBIDDEN')
    // The refusal is observable and names the permission, not the role.
    assert.match(JSON.stringify(r.body), /environments\.create/)
  }

  // -------------------------------------------------------------------------
  // Defined, then wired, then effective
  // -------------------------------------------------------------------------

  it('a role created and then assigned lets the member do it there, and nowhere else', async () => {
    await assertRefused(viewer, org.repository, 'a viewer before any model exists')

    // The role alone grants nobody anything.
    const defined = await put(owner, policy([deployer]))
    assert.equal(defined.status, 200, JSON.stringify(defined.body))
    await assertRefused(viewer, org.repository, 'the role exists and is assigned to nobody')

    const assigned = await put(owner, policy([deployer], [onRepository(viewer.userId, org.repository)]))
    assert.equal(assigned.status, 200, JSON.stringify(assigned.body))
    await assertAllowed(viewer, org.repository, 'assigned at this repository')
    await assertRefused(viewer, other, 'assigned at a different repository')
  })

  it('a grant at a repository group covers the group and nothing else, and an organization grant covers all', async () => {
    const grouped = await put(
      owner,
      policy(
        [deployer],
        [{ userId: viewer.userId, roleId: 'deployer', scope: { kind: 'group', name: 'payments' } }],
        [{ name: 'payments', repositories: [org.repository] }],
      ),
    )
    assert.equal(grouped.status, 200, JSON.stringify(grouped.body))
    await assertAllowed(viewer, org.repository, 'in the group')
    await assertRefused(viewer, other, 'outside the group')

    const wide = await put(
      owner,
      policy([deployer], [{ userId: viewer.userId, roleId: 'deployer', scope: { kind: 'organization' } }]),
    )
    assert.equal(wide.status, 200, JSON.stringify(wide.body))
    await assertAllowed(viewer, other, 'organization wide')
  })

  // -------------------------------------------------------------------------
  // The orderings
  // -------------------------------------------------------------------------

  it('a member assigned before the role exists is refused at the write, then allowed once it does', async () => {
    const early = await put(owner, policy([], [onRepository(viewer.userId, org.repository)]))
    assert.equal(early.status, 400, JSON.stringify(early.body))
    assert.match(String(early.body.detail), /names role deployer, which does not exist/)
    await assertRefused(viewer, org.repository, 'the refused file changed nothing')

    const later = await put(owner, policy([deployer], [onRepository(viewer.userId, org.repository)]))
    assert.equal(later.status, 200)
    await assertAllowed(viewer, org.repository, 'role and grant arrived together')
  })

  it('a grant for somebody not yet a member is refused, and applies once they join', async () => {
    const login = `joiner-${randomUUID().slice(0, 6)}`
    const [user] = await h.admin<{ id: string }[]>`
      INSERT INTO users (github_id, github_login, email, name)
      VALUES (${Math.floor(Math.random() * 1e12)}, ${login}, ${`${login}@example.test`}, 'Joiner')
      RETURNING id`
    const early = await put(owner, policy([deployer], [onRepository(user!.id, org.repository)]))
    assert.equal(early.status, 400, JSON.stringify(early.body))
    assert.match(String(early.body.detail), /not a member of this organization/)

    await h.admin`
      INSERT INTO members (org_id, user_id, role, source) VALUES (${org.orgId}, ${user!.id}, 'viewer', 'manual')`
    const issued = await issueSession(h.pool, h.clock, { userId: user!.id, orgId: org.orgId })
    const joiner: SignedIn = {
      userId: user!.id, token: issued.token, csrfToken: issued.csrfToken, cookie: `af_session=${issued.token}`,
    }
    await assertRefused(joiner, org.repository, 'joined, and the earlier file was refused whole')
    assert.equal((await put(owner, policy([deployer], [onRepository(user!.id, org.repository)]))).status, 200)
    await assertAllowed(joiner, org.repository, 'joined and then granted')
  })

  it('a role deleted while a member holds it stops granting on their next request', async () => {
    await put(owner, policy([deployer], [onRepository(viewer.userId, org.repository)]))
    await assertAllowed(viewer, org.repository, 'before the deletion')

    // Deleting the role and leaving the grant is refused, whole, rather than
    // leaving a grant that names nothing.
    const dangling = await put(owner, policy([], [onRepository(viewer.userId, org.repository)]))
    assert.equal(dangling.status, 400)
    await assertAllowed(viewer, org.repository, 'the refused file changed nothing')

    assert.equal((await put(owner, policy([]))).status, 200)
    await assertRefused(viewer, org.repository, 'the same session, after the role was deleted')
  })

  it('a role removed underneath the routes, by another writer, stops granting with it', async () => {
    // Not through the routes: an operator's statement, a restore, a second
    // replica. The grant must not outlive its role by any path.
    await put(owner, policy([deployer], [onRepository(viewer.userId, org.repository)]))
    await assertAllowed(viewer, org.repository, 'before')
    await h.admin`DELETE FROM custom_roles WHERE org_id = ${org.orgId} AND role_key = 'deployer'`
    const [left] = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM custom_role_grants WHERE org_id = ${org.orgId}`
    assert.equal(Number(left!.n), 0, 'the grant outlived its role')
    await assertRefused(viewer, org.repository, 'the role row is gone')
  })

  it('a permission revoked mid session is refused on the next request of that session', async () => {
    await put(owner, policy([deployer], [onRepository(viewer.userId, org.repository)]))
    await assertAllowed(viewer, org.repository, 'before the revocation')

    const narrowed: CustomRole = { ...deployer, permissions: ['environments.view'] }
    assert.equal((await put(owner, policy([narrowed], [onRepository(viewer.userId, org.repository)]))).status, 200)
    await assertRefused(viewer, org.repository, 'same session, permission revoked from the role')
  })

  it('a member removed and re-added does not get their custom grant back', async () => {
    await put(owner, policy([deployer], [onRepository(viewer.userId, org.repository)]))
    await assertAllowed(viewer, org.repository, 'before removal')

    const [row] = await h.admin<{ github_login: string }[]>`
      SELECT github_login FROM users WHERE id = ${viewer.userId}`
    const removed = await callProcedure(h, owner, 'members.remove', 'mutation', { githubLogin: row!.github_login })
    assert.equal(removed.status, 200, JSON.stringify(removed.body))

    await h.admin`
      INSERT INTO members (org_id, user_id, role, source) VALUES (${org.orgId}, ${viewer.userId}, 'viewer', 'manual')`
    const issued = await issueSession(h.pool, h.clock, { userId: viewer.userId, orgId: org.orgId })
    const back: SignedIn = { ...viewer, token: issued.token, csrfToken: issued.csrfToken, cookie: `af_session=${issued.token}` }
    await assertRefused(back, org.repository, 're-added after removal')
  })

  it('withdrawing the entitlement stops custom grants widening anything, and keeps the model', async () => {
    await put(owner, policy([deployer], [onRepository(viewer.userId, org.repository)]))
    await assertAllowed(viewer, org.repository, 'entitled')

    await h.admin`UPDATE organizations SET plan = 'team' WHERE id = ${org.orgId}`
    await assertRefused(viewer, org.repository, 'same session, entitlement withdrawn')

    const refusedWrite = await put(owner, policy([]))
    assert.equal(refusedWrite.status, 403)
    assert.equal(refusedWrite.body.error, 'not_entitled')
    assert.match(String(refusedWrite.body.detail), /not entitled to custom roles/)

    // Nothing was removed, so restoring the plan restores the grant.
    await h.admin`UPDATE organizations SET plan = 'enterprise' WHERE id = ${org.orgId}`
    await assertAllowed(viewer, org.repository, 'entitlement restored')
  })

  it('a licence that stops permitting rbac stops custom grants on the next request', async () => {
    await put(owner, policy([deployer], [onRepository(viewer.userId, org.repository)]))
    await assertAllowed(viewer, org.repository, 'licensed')
    installationPermits = false
    await assertRefused(viewer, org.repository, 'the licence no longer permits rbac')
    installationPermits = true
    await assertAllowed(viewer, org.repository, 'licence restored')
  })

  it('two files applied at once end as one file, never a mixture', async () => {
    // Deterministic rather than a race hoped for. The first writer holds its
    // transaction open after writing; the second starts only then. With the
    // lock, the second waits, reads the first writer's committed model as its
    // starting point, and replaces it. Without it, the second sees nothing
    // committed, writes alongside, and both commit: the roles of both files.
    const first: CustomRole = { ...deployer, id: 'first' }
    const second: CustomRole = { ...deployer, id: 'second' }
    let wrote!: () => void
    const firstWrote = new Promise<void>((resolve) => (wrote = resolve))

    const one = h.pool.withTenant({ orgId: org.orgId }, async (db) => {
      await lockModel(db, org.orgId)
      await writeModel(db, org.orgId, { roles: [first], grants: [], groups: [] })
      wrote()
      await new Promise((resolve) => setTimeout(resolve, 400))
    })
    await firstWrote
    const two = h.pool.withTenant({ orgId: org.orgId }, async (db) => {
      await lockModel(db, org.orgId)
      const seen = await readModel(db, org.orgId)
      await writeModel(db, org.orgId, { roles: [second], grants: [], groups: [] })
      return seen.roles.map((r) => r.id)
    })
    const [, seenBySecond] = await Promise.all([one, two])

    assert.deepEqual(seenBySecond, ['first'], 'the second writer judged its file against a model that was not the one it replaced')
    const final = await h.pool.withTenant({ orgId: org.orgId }, (db) => readModel(db, org.orgId))
    assert.deepEqual(final.roles.map((r) => r.id), ['second'])
  })

  // -------------------------------------------------------------------------
  // Tenancy
  // -------------------------------------------------------------------------

  it('a custom role in one organization grants nothing in another, for the same person and the same repository name', async () => {
    const b = await seedOrg(h.admin, 'rbac-b')
    orgs.push(b.orgId)
    await h.admin`UPDATE organizations SET plan = 'enterprise' WHERE id = ${b.orgId}`
    const bOwner = await signInAs(h, b, 'owner')
    await h.admin`
      INSERT INTO members (org_id, user_id, role, source) VALUES (${b.orgId}, ${viewer.userId}, 'viewer', 'manual')`
    const issued = await issueSession(h.pool, h.clock, { userId: viewer.userId, orgId: b.orgId })
    const viewerInB: SignedIn = { ...viewer, token: issued.token, csrfToken: issued.csrfToken, cookie: `af_session=${issued.token}` }

    // Granted in B, on a repository named exactly like A's.
    assert.equal((await put(bOwner, policy([deployer], [onRepository(viewer.userId, org.repository)]))).status, 200)
    await assertAllowed(viewerInB, org.repository, 'in the organization that granted it')
    await assertRefused(viewer, org.repository, 'in the organization that did not')

    // And B's owner reading the model sees B's and nothing of A's.
    await put(owner, policy([{ ...deployer, id: 'only-in-a' }]))
    const read = await get(bOwner, '/roles/policy')
    assert.equal(read.status, 200)
    assert.doesNotMatch(read.text, /only-in-a/)
    assert.match(read.text, /deployer/)
  })

  it('a tenant cannot point a grant at another tenant\'s role, even holding its id', async () => {
    // The attack the composite references exist for. A foreign key is checked
    // as the table owner and ignores row level security, so a single column
    // reference would accept a grant in B naming A's role, with B's org_id on
    // the row and every policy satisfied.
    const b = await seedOrg(h.admin, 'rbac-fk')
    orgs.push(b.orgId)
    const bMember = await signInAs(h, b, 'owner')
    await put(owner, policy([deployer]))
    const [aRole] = await h.admin<{ id: string }[]>`
      SELECT id FROM custom_roles WHERE org_id = ${org.orgId} AND role_key = 'deployer'`
    const err = await h.pool
      .withTenant({ orgId: b.orgId }, async (db) => {
        await db.execute(sql`
          INSERT INTO custom_role_grants (org_id, role_id, user_id, scope_kind)
          VALUES (${b.orgId}, ${aRole!.id}, ${bMember.userId}, 'organization')`)
      })
      .then(() => null, (e: unknown) => e as { code?: string; cause?: { code?: string } })
    assert.ok(err, 'a grant in one organization was allowed to name another organization\'s role')
    assert.equal(err.code ?? err.cause?.code, '23503')
  })

  // -------------------------------------------------------------------------
  // Who may write the model
  // -------------------------------------------------------------------------

  it('an admin cannot write a role holding a permission an admin does not have', async () => {
    const admin = await signInAs(h, org, 'admin')
    const treasurer: CustomRole = {
      id: 'treasurer', name: 'Treasurer', description: 'Changes the plan.', permissions: ['billing.manage'],
    }
    const refused = await put(admin, policy([treasurer], [onRepository(admin.userId, org.repository, 'treasurer')]))
    assert.equal(refused.status, 409, JSON.stringify(refused.body))
    assert.match(JSON.stringify(refused.body.refusals), /billing\.manage/)

    // An owner may. And the admin may then change something else in the same
    // file without being refused for the role the owner wrote.
    assert.equal((await put(owner, policy([treasurer]))).status, 200)
    assert.equal((await put(admin, policy([treasurer, deployer]))).status, 200)
    // But may not grant that role to anybody.
    const grant = await put(admin, policy([treasurer, deployer], [onRepository(viewer.userId, org.repository, 'treasurer')]))
    assert.equal(grant.status, 409)
  })

  it('a custom role holding members.manage does not open the model to its holder', async () => {
    const steward: CustomRole = {
      id: 'steward', name: 'Steward', description: 'Manages members.', permissions: ['members.manage'],
    }
    assert.equal(
      (await put(owner, policy([steward], [{ userId: viewer.userId, roleId: 'steward', scope: { kind: 'organization' } }]))).status,
      200,
    )
    const read = await get(viewer, '/roles/policy')
    assert.equal(read.status, 403)
    const write = await put(viewer, policy([]))
    assert.equal(write.status, 403)
    assert.equal(write.body.error, 'forbidden')
  })

  it('a file carrying approval policies is refused whole, because nothing enforces them', async () => {
    const refused = await put(
      owner,
      policy([deployer], [onRepository(viewer.userId, org.repository)], [], [
        { kind: 'masking.rules', approvals: 1, requires: 'masking.approve' as Permission, reason: 'Two sets of eyes.' },
      ] as unknown as PolicyFile['approvals']),
    )
    assert.equal(refused.status, 409, JSON.stringify(refused.body))
    assert.match(JSON.stringify(refused.body.refusals), /approval/)
    await assertRefused(viewer, org.repository, 'nothing from the refused file was applied')
  })

  it('a dry run writes nothing, and the apply is audited', async () => {
    h.clock.advance(2_500)
    const preview = await h.fetch('/roles/policy/dry-run', {
      method: 'POST',
      headers: { cookie: owner.cookie, 'x-antifailure-csrf': owner.csrfToken },
      body: policy([deployer], [onRepository(viewer.userId, org.repository)]),
    })
    assert.equal(preview.status, 200)
    const diff = (await preview.json()) as { changes: unknown[]; summary: string }
    assert.equal(diff.changes.length, 2)
    assert.match(diff.summary, /added/)
    await assertRefused(viewer, org.repository, 'after a dry run only')

    await put(owner, policy([deployer], [onRepository(viewer.userId, org.repository)]))
    const [entry] = await h.admin<{ action: string; actor_user_id: string }[]>`
      SELECT action, actor_user_id FROM audit_entries
      WHERE org_id = ${org.orgId} AND action = 'roles.policy_applied'`
    assert.equal(entry?.actor_user_id, owner.userId)
  })

  it('refuses without a session, without the CSRF header, and without members.manage', async () => {
    assert.equal((await get(null, '/roles/policy')).status, 401)
    assert.equal((await get(viewer, '/roles/policy')).status, 403)
    h.clock.advance(2_500)
    const noCsrf = await h.fetch('/roles/policy', { method: 'PUT', headers: { cookie: owner.cookie }, body: policy([]) })
    assert.equal(noCsrf.status, 403)
    assert.equal(((await noCsrf.json()) as { error: string }).error, 'csrf')
  })

  it('answers what one person can do and where it came from, to them and to a member manager', async () => {
    await put(owner, policy([deployer], [onRepository(viewer.userId, org.repository)]))
    const own = await get(viewer, `/roles/members/${viewer.userId}/permissions`)
    assert.equal(own.status, 200, JSON.stringify(own.body))
    const body = own.body as { role: string; permissions: { source: string; scope: { kind: string; name?: string } }[] }
    assert.equal(body.role, 'viewer')
    assert.ok(body.permissions.some((p) => p.source === 'Deployer' && p.scope.name === org.repository))
    assert.equal((await get(owner, `/roles/members/${viewer.userId}/permissions`)).status, 200)
    assert.equal((await get(viewer, `/roles/members/${owner.userId}/permissions`)).status, 403)
  })

  // -------------------------------------------------------------------------
  // The four built-in roles
  // -------------------------------------------------------------------------

  it('the built-in roles keep exactly what they had with a model installed', async () => {
    const admin = await signInAs(h, org, 'admin')
    const member = await signInAs(h, org, 'member')
    await put(owner, policy([deployer], [onRepository(viewer.userId, org.repository)]))
    for (const [session, label] of [[owner, 'owner'], [admin, 'admin'], [member, 'member']] as const) {
      await assertAllowed(session, other, `${label} on a repository no custom grant names`)
    }
    // A viewer's own built-in grants are untouched by a model that names them.
    const list = await callProcedure(h, viewer, 'environments.list', 'query', {})
    assert.equal(list.status, 200, JSON.stringify(list.body))
  })

  it('a resolver that cannot read the model leaves every built-in answer as it was', async () => {
    const closed = createPool({ url: appUrl(), max: 1, connectTimeoutSeconds: 5 })
    await closed.close()
    setPermissionResolver(
      customRoleResolver({
        pool: closed,
        clock: h.clock,
        installationPermits: () => true,
        log: (l) => logged.push(l),
      }),
    )
    try {
      await put(owner, policy([deployer], [onRepository(viewer.userId, org.repository)]))
      await assertRefused(viewer, org.repository, 'the model could not be read')
      await assertAllowed(owner, org.repository, 'owner, unaffected by the resolver failing')
      assert.ok(logged.some((l) => l.startsWith('custom roles: the model for')), 'the failure was silent')
    } finally {
      setPermissionResolver(
        customRoleResolver({
          pool: h.pool,
          clock: h.clock,
          installationPermits: () => installationPermits,
          log: (l) => logged.push(l),
        }),
      )
    }
  })
})

// The extension is built before the server, and the server is what owns the
// pool and the clock. These defer to them once they exist, so the routes use
// the same pool and the same clock as every other route in the harness.
let boundPool: ApiHarness['pool'] | null = null
let boundClock: ApiHarness['clock'] | null = null
function lazyPool(): ApiHarness['pool'] {
  return new Proxy({} as ApiHarness['pool'], {
    get: (_t, prop) => {
      const target = boundPool as unknown as Record<string | symbol, unknown>
      const value = target[prop]
      return typeof value === 'function' ? (value as (...a: unknown[]) => unknown).bind(boundPool) : value
    },
  })
}
function lazyClock(): ApiHarness['clock'] {
  return new Proxy({} as ApiHarness['clock'], {
    get: (_t, prop) => {
      const target = boundClock as unknown as Record<string | symbol, unknown>
      const value = target[prop]
      return typeof value === 'function' ? (value as (...a: unknown[]) => unknown).bind(boundClock) : value
    },
  })
}
