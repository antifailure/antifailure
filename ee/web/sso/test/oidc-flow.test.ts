// The OIDC routes, end to end, against a real database.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// The unit suite proves the token verifier refuses everything it should. It
// says nothing about whether the OIDC ROUTES exist, whether the redirect
// carries a challenge, whether the state survives the round trip, whether the
// stored client secret can be decrypted, or whether a member ends up in the
// database. Those are six separate places this could be wired up wrong while
// every unit test stays green, and each of them is checked here by making the
// requests a browser would make.
//
// The identity provider is a map of URL to response, so there is no network.
// What is NOT faked is the database, the policies, the encryption of the client
// secret, or the routing.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { createSign, generateKeyPairSync } from 'node:crypto'
import { available, cookieFrom, dropOrg, membersOf, seedOrg, start, type Harness, type Org } from './harness.ts'
import { cleanupIdps } from './idp.ts'
import { hashToken } from '@antifailure/api'

const hasDatabase = await available()
after(() => cleanupIdps())

const ISSUER = 'https://oidc.test'
const CLIENT_ID = 'antifailure-test'

const { publicKey, privateKey } = generateKeyPairSync('rsa', { modulusLength: 2048 })
const jwk = { ...(publicKey.export({ format: 'jwk' }) as object), kid: 'k1', use: 'sig', alg: 'RS256' }

function idToken(claims: Record<string, unknown>): string {
  const head = Buffer.from(JSON.stringify({ alg: 'RS256', kid: 'k1', typ: 'JWT' })).toString('base64url')
  const body = Buffer.from(JSON.stringify(claims)).toString('base64url')
  const signer = createSign('RSA-SHA256')
  signer.update(`${head}.${body}`)
  return `${head}.${body}.${signer.sign(privateKey).toString('base64url')}`
}

describe('OIDC end to end', { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let h: Harness
  let org: Org

  before(async () => {
    h = await start()
    org = await seedOrg(h, 'oidc', { kind: 'oidc' })
    h.fetchMock.responses.set('https://oidc.test/keys', { keys: [jwk] })
  })
  after(async () => {
    await dropOrg(h, org.orgId)
    await h.close()
  })

  /** Starts a login and returns the state and nonce the server stored. */
  async function begin(email: string) {
    const response = await h.request(`/sso/start?email=${encodeURIComponent(email)}`, {
      headers: { 'x-forwarded-for': '203.0.113.60' },
    })
    if (response.status !== 302) assert.fail(`begin failed: ${await response.text()}`)

    const location = new URL(response.headers.get('location')!)
    assert.equal(location.origin + location.pathname, 'https://oidc.test/authorize')
    assert.equal(location.searchParams.get('code_challenge_method'), 'S256')
    assert.equal(location.searchParams.get('client_id'), CLIENT_ID)
    assert.equal(
      location.searchParams.get('redirect_uri'),
      `https://antifailure.test/sso/oidc/${org.handle}/callback`,
    )
    assert.equal(location.searchParams.get('login_hint'), email)

    const state = location.searchParams.get('state')!
    const rows = await h.admin<{ nonce: string; code_verifier: string }[]>`
      SELECT nonce, code_verifier FROM sso_login_states WHERE state = ${state}`
    assert.ok(rows[0], 'the login state was not stored')

    // The verifier stayed here. If it had gone to the provider, PKCE would be
    // protecting nothing at all.
    assert.ok(
      !location.search.includes(rows[0]!.code_verifier),
      'the code verifier was sent to the identity provider',
    )
    return { state, nonce: rows[0]!.nonce, challenge: location.searchParams.get('code_challenge')! }
  }

  function claims(nonce: string, over: Record<string, unknown> = {}) {
    const now = Math.floor(h.clock.now().getTime() / 1000)
    return {
      iss: ISSUER,
      sub: 'oidc-subject-1',
      aud: CLIENT_ID,
      exp: now + 600,
      iat: now - 5,
      nonce,
      email: `Ada@${org.domain}`,
      name: 'Ada Lovelace',
      ...over,
    }
  }

  it('signs a new person in and makes them a member', async () => {
    const { state, nonce } = await begin(`ada@${org.domain}`)
    h.fetchMock.responses.set('https://oidc.test/token', { id_token: idToken(claims(nonce)) })

    const response = await h.request(
      `/sso/oidc/${org.handle}/callback?code=the-code&state=${encodeURIComponent(state)}`,
      { headers: { 'x-forwarded-for': '203.0.113.61' } },
    )
    assert.equal(response.status, 302, await response.text())
    const cookie = cookieFrom(response)
    assert.ok(cookie, 'no session cookie was issued')

    // The token endpoint was actually reached, rather than the flow taking some
    // path that skipped it.
    assert.ok(
      h.fetchMock.calls.includes('https://oidc.test/token'),
      `the token endpoint was never called: ${h.fetchMock.calls.join(', ')}`,
    )

    // Lowercased. The provider sent Ada@..., and a second account for the same
    // person is exactly what case sensitivity produces.
    const members = await membersOf(h, org.orgId)
    assert.ok(
      members.some((m) => m.email === `ada@${org.domain}`),
      `ada is not a member: ${JSON.stringify(members)}`,
    )

    const rows = await h.admin<{ org_id: string }[]>`
      SELECT org_id FROM sessions WHERE token_hash = ${hashToken(cookie)}`
    assert.equal(rows[0]?.org_id, org.orgId)
  })

  it('refuses a token carrying a nonce from a different login', async () => {
    const first = await begin(`grace@${org.domain}`)
    const second = await begin(`grace@${org.domain}`)

    // The token is valid, signed, in date, and for this client. It belongs to
    // the other login. Without the nonce check, a token the holder obtained
    // legitimately elsewhere can be injected into somebody else's flow.
    h.fetchMock.responses.set('https://oidc.test/token', {
      id_token: idToken(claims(first.nonce)),
    })
    const response = await h.request(
      `/sso/oidc/${org.handle}/callback?code=c&state=${encodeURIComponent(second.state)}`,
      { headers: { 'x-forwarded-for': '203.0.113.62' } },
    )
    assert.equal(response.status, 400)
    assert.match((await response.json()).error, /nonce/)
    assert.equal(cookieFrom(response), null)
  })

  it('refuses a state that has already been spent', async () => {
    const { state, nonce } = await begin(`hopper@${org.domain}`)
    h.fetchMock.responses.set('https://oidc.test/token', { id_token: idToken(claims(nonce)) })

    const first = await h.request(
      `/sso/oidc/${org.handle}/callback?code=c&state=${encodeURIComponent(state)}`,
      { headers: { 'x-forwarded-for': '203.0.113.63' } },
    )
    assert.equal(first.status, 302, await first.text())

    const again = await h.request(
      `/sso/oidc/${org.handle}/callback?code=c&state=${encodeURIComponent(state)}`,
      { headers: { 'x-forwarded-for': '203.0.113.64' } },
    )
    assert.equal(again.status, 400)
    assert.match((await again.json()).error, /no longer valid/)
  })

  it('refuses a state this connection did not issue', async () => {
    const other = await seedOrg(h, 'other-oidc', { kind: 'oidc' })
    try {
      const theirs = await h.request(
        `/sso/start?email=${encodeURIComponent(`someone@${other.domain}`)}`,
        { headers: { 'x-forwarded-for': '203.0.113.65' } },
      )
      const state = new URL(theirs.headers.get('location')!).searchParams.get('state')!

      // Their state, spent at our endpoint. A state from one organization must
      // not be redeemable at another's callback.
      const response = await h.request(
        `/sso/oidc/${org.handle}/callback?code=c&state=${encodeURIComponent(state)}`,
        { headers: { 'x-forwarded-for': '203.0.113.66' } },
      )
      assert.equal(response.status, 400)
      assert.equal(cookieFrom(response), null)
    } finally {
      await dropOrg(h, other.orgId)
    }
  })

  it("passes the provider's own refusal through, which is what a misconfiguration needs", async () => {
    const response = await h.request(
      `/sso/oidc/${org.handle}/callback?error=access_denied&error_description=User+cancelled`,
      { headers: { 'x-forwarded-for': '203.0.113.67' } },
    )
    assert.equal(response.status, 400)
    assert.match((await response.json()).error, /User cancelled/)
  })

  it('refuses a token with no email rather than provisioning a nameless account', async () => {
    const { state, nonce } = await begin(`nameless@${org.domain}`)
    const { email: _dropped, ...rest } = claims(nonce)
    h.fetchMock.responses.set('https://oidc.test/token', { id_token: idToken(rest) })

    const response = await h.request(
      `/sso/oidc/${org.handle}/callback?code=c&state=${encodeURIComponent(state)}`,
      { headers: { 'x-forwarded-for': '203.0.113.68' } },
    )
    assert.equal(response.status, 400)
    assert.match((await response.json()).error, /email/)
  })

  it('answers nothing for a handle that does not exist', async () => {
    const response = await h.request(
      '/sso/oidc/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/callback?code=c&state=s',
      { headers: { 'x-forwarded-for': '203.0.113.69' } },
    )
    assert.equal(response.status, 404)
  })
})
