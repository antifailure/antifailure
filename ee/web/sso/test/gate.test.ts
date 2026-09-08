// The licence gate, observed refusing.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// WHY THIS SUITE EXISTS AS ITS OWN FILE, and it is the reason the whole wave
// was dispatched. On 2026-09-08 two people counted the enforcement sites in
// this product independently and agreed on the number, and both of them READ
// it out of the source. Neither turned an entitlement off and watched anything
// refuse. A site that looks like a gate and a site that refuses are different
// claims, and the first keeps being mistaken for the second, so nothing here
// asserts that a check exists: every test makes the same request twice with one
// row changed between, and asserts the answer changed.
//
// The pair is deliberate in both directions. A suite that only ever saw the
// refusal would be satisfied by a route that refuses everybody, which is a
// broken feature rather than an enforced one, and this package's own history
// says that is not a hypothetical: fourteen of its tests went red the moment
// the gate was added, because every one of them had been seeding an
// organization on the free plan.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { available, dropOrg, seedOrg, start, type Harness, type Org } from './harness.ts'
import { cleanupIdps } from './idp.ts'
import { enforce, isEnforced, signInPolicy } from '../src/enforce.ts'
import { connectionByHandle, routeForDomain } from '../src/store.ts'
import { Unlicensed } from '@antifailure-ee/features'

const hasDatabase = await available()

after(() => cleanupIdps())

describe('the single sign-on licence gate', { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let h: Harness
  /** On a plan that does NOT carry single sign-on, which is where every
   *  organization starts. */
  let free: Org
  /** On a plan that does. */
  let entitled: Org

  before(async () => {
    h = await start()
    free = await seedOrg(h, 'unentitled', { plan: 'free', entityId: 'https://idp.test/unentitled' })
    entitled = await seedOrg(h, 'entitled', { entityId: 'https://idp.test/entitled' })
  })

  after(async () => {
    await dropOrg(h, free.orgId)
    await dropOrg(h, entitled.orgId)
    await h.close()
  })

  /** Grants one feature to one organization, and hands back the undo. The
   *  overrides table is the mechanism a sales exception uses, so a grant made
   *  this way is the same grant an operator would make on the admin screen. */
  async function grant(orgId: string, feature: string): Promise<() => Promise<void>> {
    const [row] = await h.admin<{ id: string }[]>`
      INSERT INTO entitlement_overrides
        (scope, scope_id, org_id, feature, value, reason, created_by_label)
      VALUES ('organization', ${orgId}, ${orgId}, ${feature}, ${'true'}::jsonb,
              'A design partner using a capability their plan does not carry.',
              'operator@example.test')
      RETURNING id`
    return async () => {
      await h.admin`
        UPDATE entitlement_overrides
           SET revoked_at = now(), revoked_by_label = 'test@example.test',
               revoked_reason = 'The test that granted it is finished.'
         WHERE id = ${row!.id}`
    }
  }

  const login = (org: Org) =>
    h.request(`/sso/saml/${org.handle}/login`, { headers: { 'x-forwarded-for': '203.0.113.31' } })

  it('refuses the provider login for an organization that is not entitled', async () => {
    const entitledResponse = await login(entitled)
    assert.equal(entitledResponse.status, 302, 'an entitled organization cannot even start')

    const refused = await login(free)
    const body = await refused.text()
    assert.equal(refused.status, 403, body)
    // Named, not a bare "forbidden". An administrator reading this has to be
    // able to tell it apart from a broken certificate, and the two produce
    // identical symptoms at the identity provider.
    assert.match(body, /single sign-on/)
    assert.match(body, /change the plan/)
  })

  it('refuses the assertion consumer, which is the route that would create a member', async () => {
    // The login route above is a redirect and creates nothing. This is the one
    // that turns a signed assertion into a session and a membership row, so a
    // gate that covered the first and not the second would refuse the polite
    // path and leave the load bearing one open.
    const refused = await h.request(`/sso/saml/${free.handle}/acs`, {
      method: 'POST',
      headers: {
        'content-type': 'application/x-www-form-urlencoded',
        'x-forwarded-for': '203.0.113.32',
      },
      body: 'SAMLResponse=irrelevant',
    })
    assert.equal(refused.status, 403, await refused.text())
  })

  it('says nothing at all on discovery, rather than refusing by name', async () => {
    // The opposite decision to the two above, on purpose. This endpoint is
    // unauthenticated and takes an email address, and it already answers the
    // same 404 for a domain it has never heard of and for one that is
    // registered and unverified, so that it cannot be used to ask which
    // companies have configured single sign-on here. Answering 403 for
    // "registered and not entitled" would be that question with one more bit
    // in it.
    const found = await h.request(`/sso/start?email=${encodeURIComponent(`someone@${entitled.domain}`)}`, {
      headers: { 'x-forwarded-for': '203.0.113.33' },
    })
    assert.equal(found.status, 302, await found.text())

    const hidden = await h.request(`/sso/start?email=${encodeURIComponent(`someone@${free.domain}`)}`, {
      headers: { 'x-forwarded-for': '203.0.113.34' },
    })
    const body = await hidden.text()
    assert.equal(hidden.status, 404, body)
    assert.doesNotMatch(
      body, /entitle|plan/i,
      'the discovery endpoint named the entitlement, which tells a stranger this domain is ours',
    )
  })

  it('an override turns it on for an organization whose plan does not carry it', async () => {
    // The plan is not the only authority, and this is what makes the gate an
    // entitlement rather than a hardcoded plan comparison. The customer on
    // team who was sold single sign-on is ordinary commercial reality, and
    // moving their plan to make it work would charge them the wrong amount.
    const before = await login(free)
    assert.equal(before.status, 403, await before.text())

    const revoke = await grant(free.orgId, 'sso')
    try {
      const after = await login(free)
      assert.equal(after.status, 302, await after.text())
    } finally {
      await revoke()
    }

    const again = await login(free)
    assert.equal(again.status, 403, 'revoking the override did not close the door again')
  })

  it('relaxes enforcement instead of locking the organization out', async () => {
    // THE ORDERING THAT WOULD HAVE BEEN A LOCKOUT. Single sign-on is two
    // halves: the routes that let a provider in, and the policy that turns
    // GitHub away. Gating the first and leaving the second would produce an
    // organization with no way in at all, which is worse than either the
    // feature working or the feature being absent.
    //
    // So the same entitlement decides both, and this asserts the pair on one
    // organization: enforcement is on in the database and off in effect.
    await enforce({
      pool: h.pool,
      orgId: free.orgId,
      connectionId: free.connectionId,
      actorUserId: free.ownerUserId,
      actorLabel: `owner@${free.domain}`,
      now: h.clock.now(),
    })

    // Still recorded. The licence documentation promises that every enterprise
    // setting is preserved and that renewing restores it exactly, so the flag
    // must stay on the row rather than being cleared by a lapse.
    const [row] = await h.admin<{ enforced: boolean }[]>`
      SELECT enforced FROM sso_connections WHERE id = ${free.connectionId}`
    assert.equal(row!.enforced, true, 'the enforced flag was cleared rather than relaxed')

    assert.equal(
      await isEnforced(h.pool, free.orgId, h.clock.now()), false,
      'an organization that cannot use single sign-on is still being told it must',
    )
    const policy = signInPolicy(h.pool, () => h.clock.now())
    const decision = await policy({ orgId: free.orgId, userId: free.ownerUserId, method: 'github' })
    assert.equal(
      decision.orgId, free.orgId,
      'GitHub sign-in was refused for an organization with no other way in',
    )

    // And the restoration, which is the half that proves the relaxation is the
    // entitlement rather than something else about this organization.
    const revoke = await grant(free.orgId, 'sso')
    try {
      assert.equal(await isEnforced(h.pool, free.orgId, h.clock.now()), true)
      const restored = await policy({ orgId: free.orgId, userId: free.ownerUserId, method: 'github' })
      assert.equal(restored.orgId, null)
      assert.equal(restored.note, 'sso_required')
    } finally {
      await revoke()
    }
  })

  it('refuses at the store, so a handler added later cannot skip it', async () => {
    // The gate is inside the lookup every route uses rather than in each of the
    // five handlers, which is the difference between a check somebody has to
    // remember and one they inherit. Asserted directly, because the routes
    // above cannot tell a gate in the store from five gates in the handlers.
    await assert.rejects(
      () => connectionByHandle(h.pool, free.handle, h.clock.now()),
      (err: unknown) => err instanceof Unlicensed && err.feature === 'sso',
      'the connection lookup handed a caller a connection it may not use',
    )
    assert.ok(await connectionByHandle(h.pool, entitled.handle, h.clock.now()))

    assert.equal(await routeForDomain(h.pool, free.domain, h.clock.now()), null)
    assert.ok(await routeForDomain(h.pool, entitled.domain, h.clock.now()))
  })

  it('a grant to one organization does not open the door for another', async () => {
    // An override is per organization. A gate that read the global answer would
    // turn the first design partner exception into single sign-on for everybody
    // on the free plan, and nothing would look wrong from any screen.
    const suffix = randomUUID().slice(0, 4)
    const other = await seedOrg(h, `neighbour-${suffix}`, {
      plan: 'free',
      entityId: `https://idp.test/neighbour-${suffix}`,
    })
    const revoke = await grant(free.orgId, 'sso')
    try {
      assert.equal((await login(free)).status, 302)
      assert.equal((await login(other)).status, 403)
    } finally {
      await revoke()
      await dropOrg(h, other.orgId)
    }
  })
})
