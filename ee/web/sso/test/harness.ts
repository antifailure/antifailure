// A real control plane, a real database, and a real identity provider key.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// The unit suites in this package prove the pure functions: a tampered
// signature is refused, an expired assertion is refused, a nonce that does not
// match is refused. None of that proves the feature EXISTS, because none of it
// goes through a route. This repository has shipped a block button with no
// caller, a connection pool nothing constructed, and a store nothing could
// write to, and every one of them had code that would have passed a unit test.
//
// So this harness stands up the actual server, with the extension registered
// the way the enterprise entry point registers it, against a real Postgres with
// the real policies applied, and the suite drives it with HTTP requests. If a
// route is not mounted, or the limiter has no limit for it, or a policy refuses
// a statement, these tests fail. That is the point.

import postgres from 'postgres'
import { randomBytes, randomUUID } from 'node:crypto'
import { createPool, migrate, sql, type Pool } from '@antifailure/db'
import { createServer, clearExtensions, registerExtension, setSignInPolicy, FakeClock } from '@antifailure/api'
import { ssoExtension } from '../src/index.ts'
import { signInPolicy } from '../src/enforce.ts'
import { seal } from '../src/secrets.ts'
import { makeIdp, type Idp } from './idp.ts'

export const adminUrl =
  process.env.AF_TEST_DATABASE_URL ?? 'postgres://postgres:test@127.0.0.1:55432/antifailure'

export function appUrl(): string {
  const u = new URL(adminUrl)
  u.username = 'antifailure_app'
  u.password = 'app-test-password'
  return u.toString()
}

export async function available(): Promise<boolean> {
  // Retried, and fatal when a database was named explicitly.
  //
  // The three-second connect timeout the other harnesses use is fine on an idle
  // machine and wrong on a busy one: under load the probe fails, every suite in
  // the file skips, and the run exits 0 having proved nothing. That is the
  // failure the skip was invented to avoid, arriving through the skip itself.
  //
  // So: three attempts with a longer deadline, and if AF_TEST_DATABASE_URL was
  // set deliberately, an unreachable database THROWS instead of skipping.
  // Setting the variable is a statement that a database is supposed to be
  // there, and quietly proving nothing is not an acceptable answer to it.
  let last: unknown = null
  for (let attempt = 0; attempt < 3; attempt += 1) {
    try {
      const probe = postgres(adminUrl, { max: 1, connect_timeout: 15, onnotice: () => {} })
      await probe`SELECT 1`
      await probe.end({ timeout: 5 })
      return true
    } catch (err) {
      last = err
    }
  }
  if (process.env.AF_TEST_DATABASE_URL) {
    throw new Error(
      `AF_TEST_DATABASE_URL is set to ${adminUrl} and nothing answered after three attempts. ` +
        `Refusing to skip: a run that names a database and then proves nothing is worse than a ` +
        `red one. Underlying error: ${last instanceof Error ? last.message : String(last)}`,
    )
  }
  return false
}

export const BASE_URL = 'https://antifailure.test'
export const APP_URL = 'https://app.antifailure.test/'

/** The key the suite encrypts stored secrets under. Assembled at run time, so
 *  there is no key material in the repository, in a clone of it, or in an
 *  image built from it. */
export const ENCRYPTION_KEY = randomBytes(32)

export interface Harness {
  admin: postgres.Sql
  pool: Pool
  clock: FakeClock
  app: ReturnType<typeof createServer>['app']
  idp: Idp
  fetchMock: { responses: Map<string, unknown>; calls: string[] }
  request(path: string, init?: RequestInit): Promise<Response>
  close(): Promise<void>
}

export interface Org {
  orgId: string
  slug: string
  ownerUserId: string
  connectionId: string
  handle: string
  domain: string
}

export async function start(options: { seats?: number | null } = {}): Promise<Harness> {
  const admin = postgres(adminUrl, { max: 2, connect_timeout: 30, onnotice: () => {} })
  await migrate(admin)
  await admin.unsafe(`ALTER ROLE antifailure_app LOGIN PASSWORD 'app-test-password'`)

  const pool = createPool({ url: appUrl(), max: 3, connectTimeoutSeconds: 30, statementTimeoutSeconds: 60 })
  const clock = new FakeClock()
  const idp = makeIdp()

  // A fetch that answers from a map, so the OIDC path can be driven without a
  // network. Every URL asked for is recorded, which is what lets a test assert
  // that the token endpoint was reached rather than assuming it.
  const fetchMock = { responses: new Map<string, unknown>(), calls: [] as string[] }
  const fakeFetch: typeof fetch = (async (input: string | URL | Request) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url
    fetchMock.calls.push(url)
    const body = fetchMock.responses.get(url)
    if (body === undefined) {
      return new Response(JSON.stringify({ error: 'not_configured' }), { status: 404 })
    }
    return new Response(JSON.stringify(body), {
      status: 200,
      headers: { 'content-type': 'application/json' },
    })
  }) as never

  // Registered exactly as install() does, minus reading the key from the
  // environment. Both extension points, because registering one and not the
  // other is a feature that looks finished and enforces nothing.
  clearExtensions()
  setSignInPolicy(null)
  registerExtension(
    ssoExtension({
      pool,
      clock,
      baseUrl: BASE_URL,
      appBaseUrl: APP_URL,
      secureCookies: false,
      encryptionKey: ENCRYPTION_KEY,
      seats: options.seats === undefined ? undefined : async () => options.seats ?? null,
      fetch: fakeFetch,
    }),
  )
  setSignInPolicy(signInPolicy(pool))

  const { app } = createServer({
    pool,
    // Nothing in these suites reaches GitHub. A stub that throws is better than
    // a working fake: if a test ever does reach it, it fails loudly instead of
    // quietly depending on a fixture.
    github: {} as never,
    clock,
    secureCookies: false,
    appBaseUrl: APP_URL,
  })

  return {
    admin,
    pool,
    clock,
    app,
    idp,
    fetchMock,
    request: async (path, init) => app.request(`${BASE_URL}${path}`, init),
    async close() {
      clearExtensions()
      setSignInPolicy(null)
      await pool.close()
      await admin.end({ timeout: 5 })
    },
  }
}

/** One organization with an owner, a SAML connection, and a verified domain. */
export async function seedOrg(
  h: Harness,
  label: string,
  overrides: { kind?: 'saml' | 'oidc'; enabled?: boolean; verified?: boolean } = {},
): Promise<Org> {
  const slug = `${label}-${randomUUID().slice(0, 8)}`
  const domain = `${slug}.test`
  const kind = overrides.kind ?? 'saml'

  const [org] = await h.admin<{ id: string }[]>`
    INSERT INTO organizations (slug, name) VALUES (${slug}, ${label}) RETURNING id`
  const orgId = org!.id

  const [owner] = await h.admin<{ id: string }[]>`
    INSERT INTO users (github_id, github_login, email, name)
    VALUES (${Math.floor(Math.random() * 1e12)}, ${slug}, ${`owner@${domain}`}, ${'Owner'})
    RETURNING id`
  const ownerUserId = owner!.id
  await h.admin`INSERT INTO members (org_id, user_id, role) VALUES (${orgId}, ${ownerUserId}, 'owner')`

  const handle = randomBytes(32).toString('base64url')
  const [connection] = await h.admin<{ id: string }[]>`
    INSERT INTO sso_connections (
      org_id, handle, kind, display_name, enabled, default_role,
      idp_entity_id, idp_sso_url, idp_certificates,
      oidc_issuer, oidc_client_id, oidc_authorization_endpoint, oidc_token_endpoint, oidc_jwks_uri)
    VALUES (
      ${orgId}, ${handle}, ${kind}, ${`${label} directory`}, ${overrides.enabled ?? true}, 'member',
      ${kind === 'saml' ? 'https://idp.test/metadata' : null},
      ${kind === 'saml' ? 'https://idp.test/sso' : null},
      ${kind === 'saml' ? h.admin.array([h.idp.certificate]) : h.admin.array([] as string[])},
      ${kind === 'oidc' ? 'https://oidc.test' : null},
      ${kind === 'oidc' ? 'antifailure-test' : null},
      ${kind === 'oidc' ? 'https://oidc.test/authorize' : null},
      ${kind === 'oidc' ? 'https://oidc.test/token' : null},
      ${kind === 'oidc' ? 'https://oidc.test/keys' : null})
    RETURNING id`
  const connectionId = connection!.id

  await h.admin`
    INSERT INTO sso_connection_secrets (connection_id, org_id, oidc_client_secret)
    VALUES (${connectionId}, ${orgId}, ${seal('the-client-secret', ENCRYPTION_KEY, orgId)})`

  await h.admin`
    INSERT INTO sso_domains (org_id, connection_id, domain, verification_token, verified_at)
    VALUES (${orgId}, ${connectionId}, ${domain}, ${`token-${slug}`},
            ${overrides.verified === false ? null : h.clock.now().toISOString()})`

  return { orgId, slug, ownerUserId, connectionId, handle, domain }
}

export async function dropOrg(h: Harness, orgId: string): Promise<void> {
  await h.admin`DELETE FROM audit_entries WHERE org_id = ${orgId}`
  await h.admin`DELETE FROM organizations WHERE id = ${orgId}`
}

/** The members of an organization, read with the owner connection. */
export async function membersOf(
  h: Harness,
  orgId: string,
): Promise<{ email: string; role: string; source: string }[]> {
  const rows = await h.admin<{ email: string; role: string; source: string }[]>`
    SELECT u.email, m.role::text AS role, m.source
    FROM members m JOIN users u ON u.id = m.user_id
    WHERE m.org_id = ${orgId} ORDER BY u.email`
  return rows
}

/** Posts an assertion the way an identity provider does. */
export function acsPost(xml: string, relayState?: string | null): RequestInit {
  const body = new URLSearchParams({ SAMLResponse: Buffer.from(xml, 'utf8').toString('base64') })
  if (relayState) body.set('RelayState', relayState)
  return {
    method: 'POST',
    headers: {
      'content-type': 'application/x-www-form-urlencoded',
      'x-forwarded-for': `198.51.100.${Math.floor(Math.random() * 200) + 1}`,
    },
    body: body.toString(),
  }
}

/** The session cookie out of a Set-Cookie header. */
export function cookieFrom(response: Response): string | null {
  const header = response.headers.get('set-cookie')
  if (!header) return null
  const match = /af_session=([^;]+)/.exec(header)
  return match ? match[1]! : null
}

export { sql }
