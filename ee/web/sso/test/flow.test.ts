// Single sign-on end to end, through the routes, against a real database.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// Everything here is an HTTP request to the server that would run in
// production, with the row-level security policies applied by a real Postgres.
// The unit suites prove the pure functions; this proves the feature exists.
//
// The first test is the whole point: a person who is not a member of anything
// signs in through their identity provider and comes back with a session cookie
// and a membership row. If any route is unmounted, any limit is missing, any
// policy refuses a statement, or any function has no caller, it fails here.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import {
  acsPost,
  available,
  cookieFrom,
  dropOrg,
  membersOf,
  seedOrg,
  start,
  BASE_URL,
  type Harness,
  type Org,
} from './harness.ts'
import { buildResponse, cleanupIdps, sign } from './idp.ts'
import { enforce, spendRecoveryCode, BreakGlassRefused, isEnforced } from '../src/enforce.ts'
import { hashToken, csrfTokenFor, issueSession, CSRF_HEADER } from '@antifailure/api'

const hasDatabase = await available()

after(() => cleanupIdps())

describe('single sign-on', { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let h: Harness
  let org: Org

  before(async () => {
    h = await start()
    org = await seedOrg(h, 'acme')
  })

  after(async () => {
    await dropOrg(h, org.orgId)
    await h.close()
  })

  /** Starts a login and returns the relay state the server stored. */
  async function beginLogin(email: string): Promise<{ relayState: string; requestId: string }> {
    const response = await h.request(`/sso/start?email=${encodeURIComponent(email)}`, {
      headers: { 'x-forwarded-for': '203.0.113.10' },
    })
    assert.equal(response.status, 302, await response.text())
    const location = new URL(response.headers.get('location')!)
    assert.equal(location.origin + location.pathname, 'https://idp.test/sso')
    assert.ok(location.searchParams.get('SAMLRequest'), 'the redirect carries no SAMLRequest')

    const relayState = location.searchParams.get('RelayState')!
    const rows = await h.admin<{ request_id: string }[]>`
      SELECT request_id FROM sso_login_states WHERE state = ${relayState}`
    assert.ok(rows[0], 'the login state was not stored')
    return { relayState, requestId: rows[0]!.request_id }
  }

  /** A signed assertion for this organization's connection. */
  function assertionFor(
    email: string,
    requestId: string | null,
    extra: Parameters<typeof buildResponse>[0] = {},
  ): string {
    return sign(
      buildResponse({
        issuer: 'https://idp.test/metadata',
        audience: `${BASE_URL}/sso/saml/${org.handle}/metadata`,
        destination: `${BASE_URL}/sso/saml/${org.handle}/acs`,
        inResponseTo: requestId,
        nameId: email,
        issueInstant: h.clock.now(),
        assertionId: `_a-${Math.random().toString(36).slice(2)}`,
        ...extra,
      }),
      h.idp,
    )
  }

  it('signs a new person in and makes them a member', async () => {
    const { relayState, requestId } = await beginLogin(`ada@${org.domain}`)
    const xml = assertionFor(`ada@${org.domain}`, requestId)

    const response = await h.request(`/sso/saml/${org.handle}/acs`, acsPost(xml, relayState))
    assert.equal(response.status, 302, await response.text())
    assert.equal(response.headers.get('location'), 'https://app.antifailure.test/')

    const cookie = cookieFrom(response)
    assert.ok(cookie, 'no session cookie was issued')

    // The membership, which is the observable effect. A session cookie for
    // somebody who is not a member is a login that does nothing.
    const members = await membersOf(h, org.orgId)
    const ada = members.find((m) => m.email === `ada@${org.domain}`)
    assert.ok(ada, `ada is not a member: ${JSON.stringify(members)}`)
    assert.equal(ada.role, 'member')
    assert.equal(ada.source, 'sso')

    // And the session actually resolves to that organization, rather than
    // being a cookie nothing accepts.
    const rows = await h.admin<{ org_id: string }[]>`
      SELECT org_id FROM sessions WHERE token_hash = ${hashToken(cookie)}`
    assert.equal(rows[0]?.org_id, org.orgId)
  })

  it('accepts a provider-initiated assertion, and refuses the same one twice', async () => {
    // The replay cache is tested here rather than on the service-provider
    // initiated flow, and the reason is worth recording because it changed
    // what this test is.
    //
    // Replaying an assertion from a login that started HERE is already
    // impossible twice over without the cache: the relay state is single use,
    // and presenting the assertion without one makes it a response to a
    // request nobody made, which InResponseTo refuses. The first attempt at
    // this test proved exactly that, by failing with a message about
    // InResponseTo rather than about replay.
    //
    // A provider-initiated login has neither defence. There is no state and no
    // request identifier, so the assertion IS the whole credential, and the
    // cache is the only thing between one capture and unlimited logins.
    const xml = assertionFor(`grace@${org.domain}`, null)

    const first = await h.request(`/sso/saml/${org.handle}/acs`, acsPost(xml))
    assert.equal(first.status, 302, await first.text())
    assert.ok(cookieFrom(first), 'a provider-initiated login issued no session')

    const replayed = await h.request(`/sso/saml/${org.handle}/acs`, acsPost(xml))
    assert.equal(replayed.status, 400)
    assert.match((await replayed.json()).error, /already been used/)
    assert.equal(cookieFrom(replayed), null, 'a replayed assertion issued a session')
  })

  it('refuses an assertion answering a request, presented as if unsolicited', async () => {
    // The other half. Somebody who captures a response from a real
    // service-provider initiated login and posts it with no relay state must
    // not be treated as a provider-initiated login.
    const { requestId } = await beginLogin(`grace@${org.domain}`)
    const xml = assertionFor(`mallory@${org.domain}`, requestId)

    const response = await h.request(`/sso/saml/${org.handle}/acs`, acsPost(xml))
    assert.equal(response.status, 400)
    assert.match((await response.json()).error, /request nobody made/)
  })

  it('refuses a login state a second time', async () => {
    const { relayState, requestId } = await beginLogin(`hopper@${org.domain}`)

    const first = await h.request(
      `/sso/saml/${org.handle}/acs`,
      acsPost(assertionFor(`hopper@${org.domain}`, requestId), relayState),
    )
    assert.equal(first.status, 302)

    // A fresh assertion, so this is the state being single use rather than the
    // replay cache.
    const again = await h.request(
      `/sso/saml/${org.handle}/acs`,
      acsPost(assertionFor(`hopper@${org.domain}`, requestId), relayState),
    )
    assert.equal(again.status, 400)
    assert.match((await again.json()).error, /no longer valid/)
  })

  it('refuses a tampered assertion at the route, not only in the verifier', async () => {
    const { relayState, requestId } = await beginLogin(`ada@${org.domain}`)
    const xml = assertionFor(`mallory@${org.domain}`, requestId).replace(
      `mallory@${org.domain}`,
      `owner@${org.domain}`,
    )
    const response = await h.request(`/sso/saml/${org.handle}/acs`, acsPost(xml, relayState))
    assert.equal(response.status, 400)
    assert.equal(cookieFrom(response), null, 'a session was issued for a tampered assertion')
  })

  it('does not burn the login state when the signature fails', async () => {
    // Consuming the state before verifying would let anybody destroy somebody
    // else's in-flight login by posting rubbish at a URL that needs no
    // authentication.
    const { relayState, requestId } = await beginLogin(`ada@${org.domain}`)

    const rubbish = await h.request(
      `/sso/saml/${org.handle}/acs`,
      acsPost('<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol"/>', relayState),
    )
    assert.equal(rubbish.status, 400)

    const still = await h.admin<{ state: string }[]>`
      SELECT state FROM sso_login_states WHERE state = ${relayState}`
    assert.equal(still.length, 1, 'a failed signature consumed the login state')

    // And the same state still works for a real assertion.
    const good = await h.request(
      `/sso/saml/${org.handle}/acs`,
      acsPost(assertionFor(`ada@${org.domain}`, requestId), relayState),
    )
    assert.equal(good.status, 302)
  })

  it('refuses an address in a domain the organization has not verified', async () => {
    const { relayState, requestId } = await beginLogin(`ada@${org.domain}`)
    const xml = assertionFor('somebody@gmail.com', requestId)

    const response = await h.request(`/sso/saml/${org.handle}/acs`, acsPost(xml, relayState))
    assert.equal(response.status, 403)
    const body = await response.json()
    assert.equal(body.code, 'AF-EE-SSO-002')
    // Without this, an organization that controls its own identity provider
    // could assert any address on earth and be linked to whoever holds it.
    assert.match(body.error, /has not verified the domain gmail\.com/)
  })

  it('serves service provider metadata at the URL an administrator is given', async () => {
    const response = await h.request(`/sso/saml/${org.handle}/metadata`)
    assert.equal(response.status, 200)
    assert.match(response.headers.get('content-type') ?? '', /samlmetadata\+xml/)
    const xml = await response.text()
    assert.match(xml, new RegExp(`entityID="${BASE_URL}/sso/saml/${org.handle}/metadata"`))
    assert.match(xml, new RegExp(`Location="${BASE_URL}/sso/saml/${org.handle}/acs"`))
    assert.match(xml, /WantAssertionsSigned="true"/)
  })

  it('says nothing about an address it does not know', async () => {
    const response = await h.request('/sso/start?email=nobody@unknown-domain.test', {
      headers: { 'x-forwarded-for': '203.0.113.11' },
    })
    assert.equal(response.status, 404)
  })
})

describe('group claims and roles', { skip: hasDatabase ? false : 'no database' }, () => {
  let h: Harness
  let org: Org

  before(async () => {
    h = await start()
    org = await seedOrg(h, 'groups')
    await h.admin`
      UPDATE sso_connections
      SET group_role_map = ${h.admin.json({ 'Platform Admins': 'admin', Everyone: 'viewer' })}
      WHERE id = ${org.connectionId}`
  })
  after(async () => {
    await dropOrg(h, org.orgId)
    await h.close()
  })

  it('gives the most privileged role among the groups, not the first', async () => {
    const begin = await h.request(`/sso/start?email=${encodeURIComponent(`lead@${org.domain}`)}`, {
      headers: { 'x-forwarded-for': '203.0.113.20' },
    })
    const relayState = new URL(begin.headers.get('location')!).searchParams.get('RelayState')!
    const stateRows = await h.admin<{ request_id: string }[]>`
      SELECT request_id FROM sso_login_states WHERE state = ${relayState}`
    const requestId = stateRows[0]!.request_id

    const xml = sign(
      buildResponse({
        audience: `${BASE_URL}/sso/saml/${org.handle}/metadata`,
        destination: `${BASE_URL}/sso/saml/${org.handle}/acs`,
        inResponseTo: requestId,
        nameId: `lead@${org.domain}`,
        issueInstant: h.clock.now(),
        // Deliberately least-privileged first, so that "take the first match"
        // would produce viewer and fail this.
        attributes: { groups: ['Everyone', 'Platform Admins'] },
      }),
      h.idp,
    )

    const response = await h.request(`/sso/saml/${org.handle}/acs`, acsPost(xml, relayState))
    assert.equal(response.status, 302, await response.text())

    const members = await membersOf(h, org.orgId)
    assert.equal(members.find((m) => m.email === `lead@${org.domain}`)?.role, 'admin')
  })
})

describe('the seat limit', { skip: hasDatabase ? false : 'no database' }, () => {
  let h: Harness
  let org: Org

  before(async () => {
    // One seat, and the owner already holds it.
    h = await start({ seats: 1 })
    org = await seedOrg(h, 'seats')
  })
  after(async () => {
    await dropOrg(h, org.orgId)
    await h.close()
  })

  it('refuses the addition and removes nobody', async () => {
    const before = await membersOf(h, org.orgId)
    assert.equal(before.length, 1)

    const begin = await h.request(`/sso/start?email=${encodeURIComponent(`new@${org.domain}`)}`, {
      headers: { 'x-forwarded-for': '203.0.113.30' },
    })
    const relayState = new URL(begin.headers.get('location')!).searchParams.get('RelayState')!
    const stateRows = await h.admin<{ request_id: string }[]>`
      SELECT request_id FROM sso_login_states WHERE state = ${relayState}`
    const requestId = stateRows[0]!.request_id

    const xml = sign(
      buildResponse({
        audience: `${BASE_URL}/sso/saml/${org.handle}/metadata`,
        destination: `${BASE_URL}/sso/saml/${org.handle}/acs`,
        inResponseTo: requestId,
        nameId: `new@${org.domain}`,
        issueInstant: h.clock.now(),
      }),
      h.idp,
    )

    const response = await h.request(`/sso/saml/${org.handle}/acs`, acsPost(xml, relayState))
    assert.equal(response.status, 403)
    const body = await response.json()
    assert.equal(body.code, 'AF-EE-004')
    assert.match(body.error, /No existing member was removed/)

    // The sentence in the error is the one that has to be true.
    const after = await membersOf(h, org.orgId)
    assert.deepEqual(after, before, 'a member was evicted to make room')
  })

  it('lets somebody who is already a member sign in when the seats are full', async () => {
    // A member signing in again must never be refused because the plan shrank.
    // Taking a seat away from a person mid-session is the eviction the code
    // above refuses to do, in a slower form.
    const begin = await h.request(`/sso/start?email=${encodeURIComponent(`owner@${org.domain}`)}`, {
      headers: { 'x-forwarded-for': '203.0.113.31' },
    })
    const relayState = new URL(begin.headers.get('location')!).searchParams.get('RelayState')!
    const stateRows = await h.admin<{ request_id: string }[]>`
      SELECT request_id FROM sso_login_states WHERE state = ${relayState}`
    const requestId = stateRows[0]!.request_id

    const xml = sign(
      buildResponse({
        audience: `${BASE_URL}/sso/saml/${org.handle}/metadata`,
        destination: `${BASE_URL}/sso/saml/${org.handle}/acs`,
        inResponseTo: requestId,
        nameId: `owner@${org.domain}`,
        issueInstant: h.clock.now(),
      }),
      h.idp,
    )

    const response = await h.request(`/sso/saml/${org.handle}/acs`, acsPost(xml, relayState))
    assert.equal(response.status, 302, await response.text())
  })

  it('links a GitHub member arriving through the provider, and audits it', async () => {
    const rows = await h.admin<{ source: string }[]>`
      SELECT m.source FROM members m JOIN users u ON u.id = m.user_id
      WHERE m.org_id = ${org.orgId} AND u.email = ${`owner@${org.domain}`}`
    assert.equal(rows[0]?.source, 'sso', 'the GitHub membership was not relinked')

    const audit = await h.admin<{ action: string }[]>`
      SELECT action FROM audit_entries
      WHERE org_id = ${org.orgId} AND action = 'sso.identity.linked'`
    assert.equal(audit.length, 1, 'linking a GitHub account to a directory identity was not audited')
  })
})

describe('enforcement and break-glass', { skip: hasDatabase ? false : 'no database' }, () => {
  let h: Harness
  let org: Org
  let codes: string[]

  before(async () => {
    h = await start()
    org = await seedOrg(h, 'locked')
    const result = await enforce({
      pool: h.pool,
      orgId: org.orgId,
      connectionId: org.connectionId,
      actorUserId: org.ownerUserId,
      actorLabel: 'owner',
      now: h.clock.now(),
    })
    codes = result.codes
  })
  after(async () => {
    await dropOrg(h, org.orgId)
    await h.close()
  })

  it('issues recovery codes at the moment enforcement is turned on', async () => {
    // Not a separate step somebody can skip. An organization that required
    // single sign-on and has no way back in is a support incident with no
    // self-service fix.
    assert.equal(codes.length, 10)
    for (const code of codes) assert.match(code, /^[0-9A-HJKMNP-TV-Z]{5}(-[0-9A-HJKMNP-TV-Z]{5}){3}$/)
    assert.equal(new Set(codes).size, 10)
    assert.ok(await isEnforced(h.pool, org.orgId))
  })

  it('stores only hashes, so a leaked backup is not a set of keys', async () => {
    const rows = await h.admin<{ code_hash: Buffer }[]>`
      SELECT code_hash FROM sso_break_glass_codes WHERE org_id = ${org.orgId}`
    assert.equal(rows.length, 10)
    for (const row of rows) {
      const text = row.code_hash.toString('utf8')
      for (const code of codes) {
        assert.ok(!text.includes(code.slice(0, 5)), 'a recovery code is recoverable from the row')
      }
    }
  })

  it('leaves a GitHub sign-in signed in with no organization', async () => {
    // The policy the community sign-in path consults. Not a refusal: the
    // person has to be signed in as somebody to present a recovery code, and a
    // session with no tenant is a state the server already handles.
    const { decideSignIn } = await import('@antifailure/api')
    const decision = await decideSignIn({
      userId: org.ownerUserId,
      orgId: org.orgId,
      method: 'github',
    })
    assert.equal(decision.orgId, null)
    assert.equal(decision.note, 'sso_required')
  })

  it('refuses a code from somebody who is not an owner', async () => {
    const [member] = await h.admin<{ id: string }[]>`
      INSERT INTO users (github_id, github_login, email, name)
      VALUES (${Math.floor(Math.random() * 1e12)}, 'notowner', ${`member@${org.domain}`}, 'Member')
      RETURNING id`
    await h.admin`
      INSERT INTO members (org_id, user_id, role) VALUES (${org.orgId}, ${member!.id}, 'member')`

    await assert.rejects(
      () =>
        spendRecoveryCode({
          pool: h.pool,
          orgId: org.orgId,
          userId: member!.id,
          code: codes[0]!,
          now: h.clock.now(),
        }),
      (err: unknown) => err instanceof BreakGlassRefused && /Only an owner/.test(err.message),
    )

    // And the code is still unspent, so a refused attempt costs nothing.
    const unused = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM sso_break_glass_codes WHERE org_id = ${org.orgId} AND used_at IS NULL`
    assert.equal(Number(unused[0]!.n), 10)
  })

  it('spends a code once, through the route, and rotates the session into the organization', async () => {
    // The whole break-glass path as a person walks it: signed in with no
    // tenant, POST the code, come back scoped to the organization.
    const issued = await issueSession(h.pool, h.clock, { userId: org.ownerUserId, orgId: null })

    const response = await h.request('/sso/break-glass', {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        cookie: `af_session=${issued.token}`,
        [CSRF_HEADER]: csrfTokenFor(issued.token),
        'x-forwarded-for': '203.0.113.40',
      },
      body: JSON.stringify({ orgId: org.orgId, code: codes[0] }),
    })
    assert.equal(response.status, 200, await response.text())

    const cookie = cookieFrom(response)
    assert.ok(cookie, 'no new session was issued')
    const rows = await h.admin<{ org_id: string }[]>`
      SELECT org_id FROM sessions WHERE token_hash = ${hashToken(cookie)}`
    assert.equal(rows[0]?.org_id, org.orgId)

    const audit = await h.admin<{ action: string }[]>`
      SELECT action FROM audit_entries
      WHERE org_id = ${org.orgId} AND action = 'sso.break_glass.used'`
    assert.equal(audit.length, 1, 'a break-glass login was not audited')
  })

  it('refuses the same code again', async () => {
    const issued = await issueSession(h.pool, h.clock, { userId: org.ownerUserId, orgId: null })
    const response = await h.request('/sso/break-glass', {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        cookie: `af_session=${issued.token}`,
        [CSRF_HEADER]: csrfTokenFor(issued.token),
        'x-forwarded-for': '203.0.113.41',
      },
      body: JSON.stringify({ orgId: org.orgId, code: codes[0] }),
    })
    assert.equal(response.status, 403)
    assert.match((await response.json()).error, /already been used/)
  })

  it('refuses a request with no session, and one with no cross-site token', async () => {
    const noSession = await h.request('/sso/break-glass', {
      method: 'POST',
      headers: { 'content-type': 'application/json', 'x-forwarded-for': '203.0.113.42' },
      body: JSON.stringify({ orgId: org.orgId, code: codes[1] }),
    })
    assert.equal(noSession.status, 401)

    const issued = await issueSession(h.pool, h.clock, { userId: org.ownerUserId, orgId: null })
    const noCsrf = await h.request('/sso/break-glass', {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        cookie: `af_session=${issued.token}`,
        'x-forwarded-for': '203.0.113.43',
      },
      body: JSON.stringify({ orgId: org.orgId, code: codes[1] }),
    })
    assert.equal(noCsrf.status, 403)
  })
})
