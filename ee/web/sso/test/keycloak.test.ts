// Against a real identity provider.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// Every other suite in this package builds its own assertions and its own
// tokens. That proves the verifier refuses what it should and it does NOT prove
// interoperability, because a fixture built by the same person who wrote the
// parser agrees with the parser by construction. The things that break against
// a real provider are the things nobody thought to put in a fixture: the
// namespace prefix it happens to use, where it puts the signature, whether it
// sends the email as a NameID or a claim, what its metadata document looks
// like, whether it deflates the request the way the binding says.
//
// So this drives Keycloak. It configures a realm through the admin API, logs a
// real person in by filling in the real login form, and hands what comes back
// to the real routes. Nothing is stubbed except the browser.
//
// Keycloak rather than Entra ID or Okta because it runs in a container with no
// account, so it runs on every machine and in CI. It is not a substitute for
// the two hosted providers: those have their own quirks and their own suite,
// and the rows in STATUS.md stay honest about which has been run.
//
// THE PROVIDER MUST BE HTTPS, and the invocation documented here used to say
// otherwise, which meant this suite could never have passed. The product
// refuses a plain HTTP provider in two places on purpose: an http single
// sign-on URL in parseIdentityProviderMetadata, and an http token endpoint in
// discover(). Both refusals are right, because a token exchange over plain HTTP
// carries a client secret in clear text. So the container gets a certificate,
// generated at run time outside the repository, and the script below is what
// arranges it:
//
//   eval "$(ee/web/sso/test/keycloak-up.sh)"
//   node --test ee/web/sso/test/keycloak.test.ts
//   ee/web/sso/test/keycloak-up.sh --down
//
// The eval is not decoration. NODE_EXTRA_CA_CERTS has to name a certificate
// that did not exist until the script ran.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { available, cookieFrom, dropOrg, membersOf, start, type Harness } from './harness.ts'
import { cleanupIdps } from './idp.ts'
import { parseIdentityProviderMetadata, samlUrls } from '../src/saml/request.ts'
import { discover } from '../src/oidc/flow.ts'

const KEYCLOAK = process.env.AF_KEYCLOAK_URL ?? ''
const ADMIN_USER = process.env.AF_KEYCLOAK_USER ?? 'admin'
// Not a secret and not pretending to be one: it is the bootstrap password of a
// throwaway container this file creates. It is overridable so that nothing here
// hardcodes a credential anybody might reuse.
const ADMIN_PASSWORD = process.env.AF_KEYCLOAK_PASSWORD ?? 'admin'

const hasKeycloak = KEYCLOAK !== ''
const hasDatabase = hasKeycloak ? await available() : false
const skip = !hasKeycloak
  ? 'set AF_KEYCLOAK_URL to run against a real identity provider'
  : !hasDatabase
    ? 'no Postgres at AF_TEST_DATABASE_URL'
    : false

after(() => cleanupIdps())

// The person who signs in. Both values are created by this file, in a realm
// this file creates and deletes.
const PERSON = { username: 'ada', password: `pw-${randomUUID()}`, email: 'ada@keycloak.test' }

describe('a real identity provider', { skip }, () => {
  let h: Harness
  let realm: string
  let orgId: string
  let handle: string
  let connectionId: string
  let adminToken: string
  let clientSecret: string

  const kc = (path: string) => `${KEYCLOAK.replace(/\/$/, '')}${path}`
  const realmUrl = () => kc(`/realms/${realm}`)

  // Every admin call is scoped to the realm this file created.
  //
  // This used to build `/admin/realms${path}`, with no realm in it at all, so
  // `admin('/clients')` asked for `/admin/realms/clients` and Keycloak read
  // "clients" as the name of a realm it did not have. The whole suite died in
  // before() on {"error":"Realm not found."}, which is a thing no amount of
  // reading found and one run surfaced immediately.
  async function admin(path: string, init: RequestInit = {}): Promise<Response> {
    return fetch(kc(`/admin/realms/${realm}${path}`), {
      ...init,
      headers: {
        authorization: `Bearer ${adminToken}`,
        'content-type': 'application/json',
        ...(init.headers ?? {}),
      },
    })
  }

  before(async () => {
    // The real network, not the map. Every other suite in this package answers
    // the OIDC endpoints from a fixture, and a fixture cannot exchange a code
    // with Keycloak. Running this file against the default was the second
    // reason it could never have passed.
    h = await start({ network: 'real' })
    realm = `af-lane8-${randomUUID().slice(0, 8)}`

    // 1. An administrator token, through the password grant on admin-cli.
    const tokenResponse = await fetch(kc('/realms/master/protocol/openid-connect/token'), {
      method: 'POST',
      headers: { 'content-type': 'application/x-www-form-urlencoded' },
      body: new URLSearchParams({
        grant_type: 'password',
        client_id: 'admin-cli',
        username: ADMIN_USER,
        password: ADMIN_PASSWORD,
      }).toString(),
    })
    if (!tokenResponse.ok) {
      throw new Error(`Keycloak refused the admin login: ${await tokenResponse.text()}`)
    }
    adminToken = ((await tokenResponse.json()) as { access_token: string }).access_token

    // 2. A realm of our own, so nothing here touches anything else.
    const created = await fetch(kc('/admin/realms'), {
      method: 'POST',
      headers: { authorization: `Bearer ${adminToken}`, 'content-type': 'application/json' },
      body: JSON.stringify({ realm, enabled: true }),
    })
    assert.ok(created.ok || created.status === 409, `creating the realm failed: ${await created.text()}`)

    // 3. The organization and a SAML connection, filled in from the provider's
    //    own metadata rather than from anything written here.
    const org = await h.admin<{ id: string }[]>`
      INSERT INTO organizations (slug, name) VALUES (${realm}, 'Keycloak') RETURNING id`
    orgId = org[0]!.id
    handle = Buffer.from(randomUUID() + randomUUID()).toString('base64url').slice(0, 43)

    const urls = samlUrls('https://antifailure.test', handle)

    // 4. A SAML client, registered with exactly the URLs the metadata endpoint
    //    publishes. If those two disagree, the audience check refuses the
    //    assertion, which is the single most common way a SAML setup fails.
    const samlClient = await admin('/clients', {
      method: 'POST',
      body: JSON.stringify({
        clientId: urls.entityId,
        protocol: 'saml',
        enabled: true,
        redirectUris: [urls.acsUrl],
        adminUrl: urls.acsUrl,
        attributes: {
          'saml.assertion.signature': 'true',
          'saml.server.signature': 'false',
          'saml.client.signature': 'false',
          'saml_name_id_format': 'email',
          'saml.signature.algorithm': 'RSA_SHA256',
          'saml_assertion_consumer_url_post': urls.acsUrl,
        },
      }),
    })
    assert.ok(samlClient.ok, `creating the SAML client failed: ${await samlClient.text()}`)

    // 5. An OIDC client, confidential, with a secret Keycloak generates.
    const oidcClient = await admin('/clients', {
      method: 'POST',
      body: JSON.stringify({
        clientId: 'antifailure-oidc',
        protocol: 'openid-connect',
        enabled: true,
        publicClient: false,
        standardFlowEnabled: true,
        // handle + 'o', because that is the handle the OIDC CONNECTION is
        // stored under a few lines below, and therefore the one the callback
        // route builds its redirect_uri from. Registering the bare handle here
        // made Keycloak answer invalid_redirect_uri and return a 400 error
        // page, which then failed as "no login form" three frames away from
        // the cause.
        redirectUris: [`https://antifailure.test/sso/oidc/${handle}o/callback`],
        attributes: { 'post.logout.redirect.uris': '+' },
      }),
    })
    assert.ok(oidcClient.ok, `creating the OIDC client failed: ${await oidcClient.text()}`)

    const clients = (await (await admin('/clients?clientId=antifailure-oidc')).json()) as {
      id: string
    }[]
    const secret = (await (await admin(`/clients/${clients[0]!.id}/client-secret`)).json()) as {
      value: string
    }
    clientSecret = secret.value

    // 6. The person.
    const user = await admin('/users', {
      method: 'POST',
      body: JSON.stringify({
        username: PERSON.username,
        email: PERSON.email,
        emailVerified: true,
        enabled: true,
        firstName: 'Ada',
        lastName: 'Lovelace',
        credentials: [{ type: 'password', value: PERSON.password, temporary: false }],
      }),
    })
    assert.ok(user.ok, `creating the user failed: ${await user.text()}`)

    // 7. The connection, from the provider's published metadata. This is the
    //    first real test in the file: parseIdentityProviderMetadata is reading
    //    a document Keycloak wrote, not one this repository wrote.
    const descriptor = await (await fetch(`${realmUrl()}/protocol/saml/descriptor`)).text()
    const metadata = parseIdentityProviderMetadata(descriptor)
    assert.equal(metadata.entityId, realmUrl())
    assert.ok(metadata.certificates.length >= 1, 'no signing certificate in the metadata')

    const endpoints = await discover(realmUrl())

    const connection = await h.admin<{ id: string }[]>`
      INSERT INTO sso_connections (
        org_id, handle, kind, display_name, enabled, default_role,
        idp_entity_id, idp_sso_url, idp_certificates)
      VALUES (${orgId}, ${handle}, 'saml', 'Keycloak', true, 'member',
              ${metadata.entityId}, ${metadata.ssoUrl}, ${h.admin.array(metadata.certificates)})
      RETURNING id`
    connectionId = connection[0]!.id

    await h.admin`
      INSERT INTO sso_domains (org_id, connection_id, domain, verification_token, verified_at)
      VALUES (${orgId}, ${connectionId}, 'keycloak.test', 'token', now())`

    // Kept for the OIDC case, which needs the same organization.
    await h.admin`
      INSERT INTO sso_connections (
        org_id, handle, kind, display_name, enabled, default_role,
        oidc_issuer, oidc_client_id, oidc_authorization_endpoint, oidc_token_endpoint, oidc_jwks_uri)
      VALUES (${orgId}, ${handle + 'o'}, 'oidc', 'Keycloak OIDC', true, 'member',
              ${endpoints.issuer}, 'antifailure-oidc', ${endpoints.authorizationEndpoint},
              ${endpoints.tokenEndpoint}, ${endpoints.jwksUri})`
  })

  after(async () => {
    if (orgId) await dropOrg(h, orgId)
    if (realm && adminToken) {
      await fetch(kc(`/admin/realms/${realm}`), {
        method: 'DELETE',
        headers: { authorization: `Bearer ${adminToken}` },
      }).catch(() => {})
    }
    await h?.close()
  })

  /**
   * Signs in at Keycloak's own login form and returns whatever it hands back.
   *
   * This is the browser, and nothing more. It follows redirects by hand so that
   * the cookies Keycloak sets are carried, because its login form will not
   * accept a post without them.
   */
  async function signInAt(startUrl: string): Promise<{ finalUrl: string; body: string }> {
    const jar = new Map<string, string>()
    const cookieHeader = () => [...jar].map(([k, v]) => `${k}=${v}`).join('; ')
    const remember = (response: Response) => {
      for (const raw of response.headers.getSetCookie?.() ?? []) {
        const [pair] = raw.split(';')
        const eq = pair!.indexOf('=')
        if (eq > 0) jar.set(pair!.slice(0, eq), pair!.slice(eq + 1))
      }
    }

    let url = startUrl
    let response = await fetch(url, { redirect: 'manual', headers: { cookie: cookieHeader() } })
    remember(response)
    // Up to five hops. Keycloak's login flow is two or three; a cap stops a
    // misconfiguration turning into an infinite loop in a test.
    for (let hop = 0; hop < 5 && response.status >= 300 && response.status < 400; hop += 1) {
      url = new URL(response.headers.get('location')!, url).toString()
      response = await fetch(url, { redirect: 'manual', headers: { cookie: cookieHeader() } })
      remember(response)
    }

    const page = await response.text()
    // Single or double quoted, because a theme is free to choose and this
    // assertion is not the place to be strict about somebody else's HTML.
    const action = /<form[^>]*\baction=["']([^"']+)["']/i.exec(page)?.[1]
    // The slice used to be 400 characters, which on a Keycloak login page is
    // exactly the <head> and none of the form, so a failure here printed a page
    // that looked right and told you nothing. Print the status, the URL, and
    // enough of the body to see what actually came back.
    assert.ok(
      action,
      `no login form at ${url} (status ${response.status}):\n${page.slice(0, 4000)}`,
    )
    const formUrl = decodeHtml(action)

    const submitted = await fetch(formUrl, {
      method: 'POST',
      redirect: 'manual',
      headers: { 'content-type': 'application/x-www-form-urlencoded', cookie: cookieHeader() },
      body: new URLSearchParams({
        username: PERSON.username,
        password: PERSON.password,
      }).toString(),
    })
    remember(submitted)

    let out = submitted
    let outUrl = formUrl
    for (let hop = 0; hop < 5 && out.status >= 300 && out.status < 400; hop += 1) {
      outUrl = new URL(out.headers.get('location')!, outUrl).toString()
      // The last hop is the redirect back to us, which this process serves
      // rather than the network. Stop and hand it back.
      if (outUrl.startsWith('https://antifailure.test')) return { finalUrl: outUrl, body: '' }
      out = await fetch(outUrl, { redirect: 'manual', headers: { cookie: cookieHeader() } })
      remember(out)
    }
    return { finalUrl: outUrl, body: await out.text() }
  }

  it('signs a real person in through SAML, end to end', async () => {
    const begin = await h.request(
      `/sso/start?email=${encodeURIComponent(PERSON.email)}`,
      { headers: { 'x-forwarded-for': '203.0.113.90' } },
    )
    assert.equal(begin.status, 302, await begin.text())
    const authUrl = begin.headers.get('location')!
    assert.ok(authUrl.startsWith(`${realmUrl()}/protocol/saml`), `unexpected redirect: ${authUrl}`)

    // Keycloak parsed our AuthnRequest. If the deflate, the base64 or the
    // binding were wrong, this is where it says so.
    const { body } = await signInAt(authUrl)
    const samlResponse = /name="SAMLResponse"[^>]*value="([^"]*)"/.exec(body)?.[1]
    assert.ok(samlResponse, `no SAMLResponse in the reply: ${body.slice(0, 600)}`)
    const relayState = /name="RelayState"[^>]*value="([^"]*)"/.exec(body)?.[1]

    const form = new URLSearchParams({ SAMLResponse: decodeHtml(samlResponse) })
    if (relayState) form.set('RelayState', decodeHtml(relayState))

    const acs = await h.request(`/sso/saml/${handle}/acs`, {
      method: 'POST',
      headers: {
        'content-type': 'application/x-www-form-urlencoded',
        'x-forwarded-for': '203.0.113.91',
      },
      body: form.toString(),
    })

    assert.equal(acs.status, 302, await acs.text())
    assert.ok(cookieFrom(acs), 'no session cookie was issued')

    const members = await membersOf(h, orgId)
    assert.ok(
      members.some((m) => m.email === PERSON.email),
      `the person Keycloak authenticated is not a member: ${JSON.stringify(members)}`,
    )
  })

  it('signs the same person in through OIDC, end to end', async () => {
    // The client secret Keycloak generated, sealed the way the product seals
    // it, so the callback exercises the real decrypt path.
    const { seal } = await import('../src/secrets.ts')
    const { ENCRYPTION_KEY } = await import('./harness.ts')
    const oidcConnection = await h.admin<{ id: string }[]>`
      SELECT id FROM sso_connections WHERE org_id = ${orgId} AND kind = 'oidc'`
    await h.admin`
      INSERT INTO sso_connection_secrets (connection_id, org_id, oidc_client_secret)
      VALUES (${oidcConnection[0]!.id}, ${orgId}, ${seal(clientSecret, ENCRYPTION_KEY, orgId)})
      ON CONFLICT (connection_id) DO UPDATE SET oidc_client_secret = EXCLUDED.oidc_client_secret`

    const begin = await h.request(`/sso/oidc/${handle}o/login`, {
      headers: { 'x-forwarded-for': '203.0.113.92' },
    })
    assert.equal(begin.status, 302, await begin.text())

    const { finalUrl } = await signInAt(begin.headers.get('location')!)
    const back = new URL(finalUrl)
    const code = back.searchParams.get('code')
    const state = back.searchParams.get('state')
    assert.ok(code && state, `Keycloak did not return a code: ${finalUrl}`)

    const callback = await h.request(
      `/sso/oidc/${handle}o/callback?code=${encodeURIComponent(code)}&state=${encodeURIComponent(state)}`,
      { headers: { 'x-forwarded-for': '203.0.113.93' } },
    )
    assert.equal(callback.status, 302, await callback.text())
    assert.ok(cookieFrom(callback), 'no session cookie was issued')
  })
})

/** The handful of entities that appear in a form value in an HTML page. */
function decodeHtml(value: string): string {
  return value
    .replace(/&amp;/g, '&')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&#x27;|&apos;/g, "'")
    .replace(/&#x2F;/g, '/')
}
