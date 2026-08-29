// The seam between single sign-on and SCIM.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// EVERY OTHER SUITE IN THIS PACKAGE TESTS ONE SUBSYSTEM AT A TIME, and that is
// exactly how the defect below survived. The SCIM suite provisions and
// deprovisions the same identity in every ordering it can think of. The single
// sign-on suite signs people in and out in every ordering it can think of.
// Both were green. The fault needed the two of them ALTERNATING, and neither
// file could express that because neither registered the other extension.
//
// The defect, measured against a real Microsoft Entra tenant before it was
// fixed: SCIM provisions somebody, SCIM deprovisions them (which DELETES the
// membership, deliberately), they then sign in through SAML before SCIM has
// re-provisioned them, and single sign-on writes a SECOND users row because its
// lookup joins THROUGH members and finds nothing. From then on the directory
// manages one row and sign-in manages the other, so the next deprovision
// revokes a row nobody uses. Entra reported action=Disable status=Success while
// the person kept a membership and a live session.
//
// Migration 0014 carries the fix and the reason it has to be SECURITY DEFINER.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { createHash, randomBytes, randomUUID } from 'node:crypto'
import { acsPost, available, cookieFrom, dropOrg, seedOrg, start, type Harness, type Org } from './harness.ts'
import { buildResponse, cleanupIdps, sign } from './idp.ts'

const hasDatabase = await available()
after(() => cleanupIdps())

/**
 * Asserts a status and reads the body ONLY when it is wrong.
 *
 * `assert.equal(res.status, 201, await res.text())` evaluates its message
 * eagerly, so it consumes the body on the happy path and the next `.json()`
 * throws "Body is unusable". Same trap this package already hit once.
 */
async function expect(response: Response, status: number): Promise<Response> {
  if (response.status !== status) {
    assert.fail(`expected ${status}, got ${response.status}: ${await response.text()}`)
  }
  return response
}

describe('the seam between single sign-on and SCIM', { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let h: Harness
  let org: Org
  let token: string

  before(async () => {
    h = await start({ scim: true })
    org = await seedOrg(h, 'seam')
    // Assembled at run time. There is no token in the repository.
    token = `afs_${randomBytes(24).toString('base64url')}`
    await h.admin`
      INSERT INTO scim_tokens (org_id, name, token_hash, prefix)
      VALUES (${org.orgId}, 'seam', ${createHash('sha256').update(token, 'utf8').digest()},
              ${token.slice(0, 10)})`
  })
  after(async () => {
    await dropOrg(h, org.orgId)
    await h.close()
  })

  const scim = (path: string, init: RequestInit = {}) => {
    const headers = new Headers(init.headers)
    headers.set('authorization', `Bearer ${token}`)
    if (init.body && !headers.has('content-type')) headers.set('content-type', 'application/scim+json')
    headers.set('x-forwarded-for', '198.51.100.9')
    return h.request(path, { ...init, headers })
  }

  const deactivate = (id: string) => setActive(id, false)
  const setActive = (id: string, active: boolean) =>
    scim(`/scim/v2/Users/${id}`, {
      method: 'PATCH',
      body: JSON.stringify({
        schemas: ['urn:ietf:params:scim:api:messages:2.0:PatchOp'],
        Operations: [{ op: 'replace', path: 'active', value: active }],
      }),
    })

  /** A signed assertion for this organisation's connection. */
  function assertionFor(email: string): string {
    return sign(
      buildResponse({
        issuer: 'https://idp.test/metadata',
        audience: `https://antifailure.test/sso/saml/${org.handle}/metadata`,
        destination: `https://antifailure.test/sso/saml/${org.handle}/acs`,
        inResponseTo: null,
        nameId: email,
        issueInstant: h.clock.now(),
      }),
      h.idp,
    )
  }

  async function rows(email: string) {
    const users = await h.admin<{ id: string; identity_source: string }[]>`
      SELECT id, identity_source FROM users WHERE lower(email) = ${email}`
    const members = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM members WHERE org_id = ${org.orgId}
        AND user_id IN (SELECT id FROM users WHERE lower(email) = ${email})`
    const live = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM sessions WHERE revoked_at IS NULL
        AND user_id IN (SELECT id FROM users WHERE lower(email) = ${email})`
    return { users, members: Number(members[0]!.n), live: Number(live[0]!.n) }
  }

  it('refuses a sign-in while the directory says the person is inactive', async () => {
    // The authority question. A SAML assertion says who is holding the
    // password; SCIM says who still works here. When they disagree the
    // directory wins, or an offboarding lasts only until the person's next
    // sign-in.
    const email = `seam-one@${org.domain}`
    const created = await expect(await scim('/scim/v2/Users', {
      method: 'POST',
      body: JSON.stringify({
        schemas: ['urn:ietf:params:scim:schemas:core:2.0:User'],
        userName: email,
        emails: [{ value: email, primary: true }],
        active: true,
      }),
    }), 201)
    const resourceId = ((await created.json()) as { id: string }).id
    const first = await rows(email)
    assert.equal(first.users.length, 1)

    await expect(await deactivate(resourceId), 200)
    assert.equal((await rows(email)).members, 0, 'the membership should be gone')

    const refused = await h.request(`/sso/saml/${org.handle}/acs`, acsPost(assertionFor(email)))
    assert.equal(refused.status, 403, await refused.text())

    const after = await rows(email)
    assert.equal(after.users.length, 1, 'the refused sign-in still forked the account')
    assert.equal(after.members, 0, 'a refused sign-in granted a membership')
    assert.equal(after.live, 0, 'a refused sign-in issued a session')
  })

  it('reuses the same account when the directory brings them back', async () => {
    // The ordering that used to fork the row: deprovision, then a sign-in.
    // Legitimate this time, because the directory has reactivated them.
    const email = `seam-two@${org.domain}`
    const created = await expect(await scim('/scim/v2/Users', {
      method: 'POST',
      body: JSON.stringify({
        schemas: ['urn:ietf:params:scim:schemas:core:2.0:User'],
        userName: email,
        emails: [{ value: email, primary: true }],
        active: true,
      }),
    }), 201)
    const resourceId = ((await created.json()) as { id: string }).id
    const original = (await rows(email)).users[0]!.id

    await expect(await deactivate(resourceId), 200)
    await expect(await setActive(resourceId, true), 200)

    const acs = await h.request(`/sso/saml/${org.handle}/acs`, acsPost(assertionFor(email)))
    await expect(acs, 302)
    assert.ok(cookieFrom(acs), 'no session was issued')

    const after = await rows(email)
    assert.equal(
      after.users.length,
      1,
      `signing in forked the account into ${after.users.length} rows: ${JSON.stringify(after.users)}`,
    )
    assert.equal(after.users[0]!.id, original, 'a different account was used')
    assert.equal(after.live, 1)

    // And the directory can still take it away, which is the property the
    // whole defect was about.
    await expect(await deactivate(resourceId), 200)
    const revoked = await rows(email)
    assert.equal(revoked.members, 0, 'the membership survived a deprovision')
    assert.equal(revoked.live, 0, 'the person still holds a live session after being deprovisioned')
  })

  it('does not adopt an account that belongs to another organisation', async () => {
    // The safety boundary of adoptable_directory_user. A user with a membership
    // anywhere is not an orphan, and adopting one on the strength of an emailed
    // claim would turn a verified domain into a way to take over accounts.
    const email = `shared-${randomUUID().slice(0, 8)}@${org.domain}`
    const userId = randomUUID()
    const [elsewhere] = await h.admin<{ id: string }[]>`
      INSERT INTO organizations (slug, name)
      VALUES (${`elsewhere-${randomUUID().slice(0, 8)}`}, 'Elsewhere') RETURNING id`
    try {
      await h.admin`
        INSERT INTO users (id, email, name, identity_source)
        VALUES (${userId}, ${email}, 'Shared', 'sso')`
      await h.admin`
        INSERT INTO members (org_id, user_id, role, source)
        VALUES (${elsewhere!.id}, ${userId}, 'member', 'sso')`

      const acs = await h.request(`/sso/saml/${org.handle}/acs`, acsPost(assertionFor(email)))
      await expect(acs, 302)

      const all = await h.admin<{ id: string }[]>`
        SELECT id FROM users WHERE lower(email) = ${email}`
      assert.equal(all.length, 2, "the other organisation's account was adopted")
      const mine = await h.admin<{ n: string }[]>`
        SELECT count(*) AS n FROM members WHERE org_id = ${elsewhere!.id} AND user_id = ${userId}`
      assert.equal(Number(mine[0]!.n), 1, "the other organisation's membership was disturbed")
    } finally {
      await h.admin`DELETE FROM audit_entries WHERE org_id = ${elsewhere!.id}`
      await h.admin`DELETE FROM organizations WHERE id = ${elsewhere!.id}`
    }
  })
})
