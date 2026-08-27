// The endpoints, and the order the work happens in.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// Everything above this file is a pure function over a document or a row.
// This is where it becomes reachable, and a feature that is not reachable is
// not a feature: this repository has shipped a block button with no caller, a
// connection pool nothing constructed, and a store nothing could write to, all
// of which compiled and looked finished. So every function in this package has
// its call site here, and the integration suite drives these routes rather than
// calling the pieces directly.
//
// The order inside the assertion consumer is load bearing and it is not the
// obvious one:
//
//   1. Find the connection. Unscoped, by the handle in the URL.
//   2. VERIFY THE SIGNATURE. Before the login state is touched, and before
//      anything else is read. If the state were consumed first, anybody could
//      burn a person's in-flight login by posting rubbish at this URL, which is
//      a denial of service with no authentication required.
//   3. Consume the login state, which is what makes a login single use.
//   4. Validate the assertion against what this connection expects.
//   5. Remember the assertion id, which is what makes it single use. Before
//      provisioning, so a replay is refused whatever provisioning would do.
//   6. Provision, then issue the session.
//
// Nothing here renders HTML. The server answers
// `content-security-policy: default-src 'none'` to everything, which is right,
// and every step is a redirect or JSON, so there is nothing to weaken.

import { randomBytes } from 'node:crypto'
import type { Pool } from '@antifailure/db'
import {
  CSRF_HEADER,
  type Context,
  SESSION_COOKIE,
  csrfMatches,
  issueSession,
  readCookie,
  resolveSession,
  safeRedirect,
  sessionCookie,
  type Clock,
  type Extension,
  type ExtensionRoute,
} from '@antifailure/api'
import { verifyResponse, SignatureRefused } from './saml/verify.ts'
import { AssertionRefused, readAssertion, statusMessage } from './saml/response.ts'
import { buildAuthnRequest, samlUrls, serviceProviderMetadata } from './saml/request.ts'
import { decodeBase64, parseXml, MalformedXml, one, text } from './saml/xml.ts'
import { authorizationUrl, beginLogin, completeLogin } from './oidc/flow.ts'
import { TokenRefused } from './oidc/jwt.ts'
import {
  connectionByHandle,
  consumeLoginState,
  connectionById,
  connectionSecrets,
  rememberAssertion,
  routeForDomain,
  saveLoginState,
  sweepOrg,
  type Connection,
} from './store.ts'
import { provision, ProvisioningRefused } from './provision.ts'
import { spendRecoveryCode, BreakGlassRefused } from './enforce.ts'
import { keyFromEnv, open as openSecret, SecretUnavailable } from './secrets.ts'

/** How long a browser has to come back from the identity provider. */
export const LOGIN_TTL_MS = 10 * 60 * 1000

export interface SsoOptions {
  pool: Pool
  clock: Clock
  /** Where this control plane is reachable, for the URLs a provider is given. */
  baseUrl: string
  /** Where the browser lands after signing in. */
  appBaseUrl: string
  /** Set false only for local HTTP development. */
  secureCookies?: boolean
  /** The key the stored client secret is encrypted under. Read once, at
   *  registration, so a missing one is a startup failure and not a failure on
   *  the first login somebody attempts. */
  encryptionKey?: Buffer
  /** How many members the license covers, per organization. */
  seats?: (orgId: string) => Promise<number | null>
  /** For tests, and for a host that wants its own client. */
  fetch?: typeof globalThis.fetch
  /** Where an unexpected failure is reported. Defaults to stderr: a 500 with
   *  no cause on the sign-in path is an outage nobody can diagnose. */
  log?: (line: string) => void
}

// Every limit is keyed by address, because everything here is reached by
// somebody with no session: that is the whole point of a sign-in path, and the
// address is the only key available.
const LOGIN_LIMIT = {
  rate: 1,
  burst: 20,
  key: 'ip' as const,
  reason:
    'Starting a sign-in is a human action. Twenty at once covers an office behind one address, ' +
    'and a sustained one per second does not. The same numbers as the GitHub sign-in path.',
}

const CALLBACK_LIMIT = {
  rate: 1,
  burst: 20,
  key: 'ip' as const,
  reason:
    'The same flow returning. Higher would let somebody grind state values or replay assertions ' +
    'quickly enough to matter.',
}

const METADATA_LIMIT = {
  rate: 2,
  burst: 10,
  key: 'ip' as const,
  reason: 'A static document an administrator fetches once while configuring their provider.',
}

const BREAK_GLASS_LIMIT = {
  rate: 1,
  burst: 5,
  key: 'ip' as const,
  reason:
    'A hundred-bit code, so guessing is hopeless anyway; this is tight because a burst of ' +
    'attempts against recovery codes is the shape of an attack and never of a person.',
}

/**
 * Wraps a handler so an unexpected failure is reported rather than escaping.
 *
 * Without this, a throw becomes the router's own "Internal Server Error" with
 * no body and nothing logged: the operator sees a 500 with no cause and the
 * person signing in sees a blank page. Every expected refusal in this file
 * already returns a message; this is only for the ones nobody predicted, which
 * are exactly the ones worth seeing.
 *
 * The message goes to the log and never to the response. The response is
 * reached by anybody who can POST to the assertion consumer, and a stack trace
 * or a query is not something to hand them.
 */
function reported(
  options: SsoOptions,
  name: string,
  handler: (c: Context) => Promise<Response>,
): (c: Context) => Promise<Response> {
  return async (c) => {
    try {
      return await handler(c)
    } catch (err) {
      options.log?.(`sso: ${name} failed: ${describe(err)}`)
      return c.json({ error: 'The sign-in could not be completed. Try again.' }, 500)
    }
  }
}

/** The failure underneath whatever wrapped it. Drizzle reports a database error
 *  as "Failed query: <sql>" and hangs the driver's error off cause, so the
 *  outer message alone says nothing. */
function describe(err: unknown): string {
  const parts: string[] = []
  let cur: unknown = err
  for (let depth = 0; depth < 8 && cur; depth += 1) {
    const e = cur as { code?: string; message?: string; cause?: unknown }
    if (e.message) parts.push(e.code ? `${e.code}: ${e.message}` : e.message)
    cur = e.cause
  }
  return parts.join(' <- ') || String(err)
}

/** Builds the extension. Registered by the enterprise entry point. */
export function ssoExtension(input: SsoOptions): Extension {
  const options: SsoOptions = { ...input, log: input.log ?? ((line) => console.error(line)) }
  const routes: ExtensionRoute[] = [
    { method: 'GET', path: '/sso/start', limit: LOGIN_LIMIT, handler: reported(options, 'start', (c) => start(c, options)) },
    {
      method: 'GET',
      path: '/sso/saml/:handle/metadata',
      limit: METADATA_LIMIT,
      handler: reported(options, 'metadata', (c) => metadata(c, options)),
    },
    {
      method: 'GET',
      path: '/sso/saml/:handle/login',
      limit: LOGIN_LIMIT,
      handler: reported(options, 'samlLogin', (c) => samlLogin(c, options)),
    },
    {
      method: 'POST',
      path: '/sso/saml/:handle/acs',
      limit: CALLBACK_LIMIT,
      handler: reported(options, 'samlAcs', (c) => samlAcs(c, options)),
    },
    {
      method: 'GET',
      path: '/sso/oidc/:handle/login',
      limit: LOGIN_LIMIT,
      handler: reported(options, 'oidcLogin', (c) => oidcLogin(c, options)),
    },
    {
      method: 'GET',
      path: '/sso/oidc/:handle/callback',
      limit: CALLBACK_LIMIT,
      handler: reported(options, 'oidcCallback', (c) => oidcCallback(c, options)),
    },
    {
      method: 'POST',
      path: '/sso/break-glass',
      limit: BREAK_GLASS_LIMIT,
      handler: reported(options, 'breakGlass', (c) => breakGlass(c, options)),
    },
  ]
  return { name: 'sso', routes }
}

// ---------------------------------------------------------------------------
// Discovery
// ---------------------------------------------------------------------------

async function start(c: Context, options: SsoOptions): Promise<Response> {
  const email = (c.req.query('email') ?? '').trim().toLowerCase()
  const at = email.lastIndexOf('@')
  if (at < 1 || at === email.length - 1) {
    return c.json({ error: 'Give an email address to find the right identity provider.' }, 400)
  }

  const route = await routeForDomain(options.pool, email.slice(at + 1))
  if (!route) {
    // 404 with no detail about which part failed. A different answer for
    // "unverified" and "unknown" would turn this into a way to ask which
    // domains have configured single sign-on here.
    return c.json({ error: 'No identity provider is configured for that address.' }, 404)
  }

  const connection = await connectionById(options.pool, route.orgId, route.connectionId)
  if (!connection || !connection.enabled) {
    return c.json({ error: 'No identity provider is configured for that address.' }, 404)
  }

  return beginFor(c, options, connection, {
    redirectTo: c.req.query('redirect_to'),
    loginHint: email,
  })
}

// ---------------------------------------------------------------------------
// SAML
// ---------------------------------------------------------------------------

async function metadata(c: Context, options: SsoOptions): Promise<Response> {
  const connection = await connectionByHandle(options.pool, c.req.param('handle') ?? '')
  if (!connection || connection.kind !== 'saml') {
    return c.json({ error: 'No such connection.' }, 404)
  }
  const urls = samlUrls(options.baseUrl, connection.handle)
  const secrets = await connectionSecrets(options.pool, connection.orgId, connection.id)

  return new Response(
    serviceProviderMetadata({
      entityId: urls.entityId,
      acsUrl: urls.acsUrl,
      certificate: secrets?.spCertificate ?? null,
    }),
    { status: 200, headers: { 'content-type': 'application/samlmetadata+xml; charset=utf-8' } },
  )
}

async function samlLogin(c: Context, options: SsoOptions): Promise<Response> {
  const connection = await connectionByHandle(options.pool, c.req.param('handle') ?? '')
  if (!connection || connection.kind !== 'saml' || !connection.enabled) {
    return c.json({ error: 'No such connection.' }, 404)
  }
  return beginFor(c, options, connection, { redirectTo: c.req.query('redirect_to') })
}

async function samlAcs(c: Context, options: SsoOptions): Promise<Response> {
  const connection = await connectionByHandle(options.pool, c.req.param('handle') ?? '')
  if (!connection || connection.kind !== 'saml' || !connection.enabled) {
    return c.json({ error: 'No such connection.' }, 404)
  }

  let form: FormData
  try {
    form = await c.req.formData()
  } catch {
    return c.json({ error: 'The assertion consumer expects a form post.' }, 400)
  }
  const encoded = String(form.get('SAMLResponse') ?? '')
  if (!encoded) return c.json({ error: 'The post carries no SAMLResponse.' }, 400)

  let xml: string
  try {
    xml = decodeBase64(encoded, 'SAMLResponse').toString('utf8')
  } catch {
    return c.json({ error: 'The SAMLResponse is not base64.' }, 400)
  }

  // 2. The signature, before anything else and before the login state is
  // touched. Consuming the state first would let anybody destroy a person's
  // in-flight login by posting rubbish here.
  let verified
  try {
    verified = verifyResponse(xml, { certificates: connection.idpCertificates })
  } catch (err) {
    if (err instanceof SignatureRefused || err instanceof MalformedXml) {
      // When the provider reported a failure rather than sending an assertion,
      // say what it reported. That reading is of the unsigned document and is
      // used for the message only; nothing is authorised on it.
      const reported = reportedFailure(xml)
      return c.json(
        { error: reported ? `The identity provider refused the sign-in: ${reported}` : err.message },
        400,
      )
    }
    throw err
  }

  // 3. The login state. RelayState carries it for a sign-in that started here;
  // its absence means the provider started this one.
  const relayState = String(form.get('RelayState') ?? '')
  const login = relayState ? await consumeLoginState(options.pool, relayState) : null
  if (relayState && !login) {
    // One message for never-issued, already-used and expired, so that somebody
    // probing values learns nothing about which of their guesses was once real.
    return c.json({ error: 'This sign-in link is no longer valid. Start again.' }, 400)
  }
  if (login && login.expiresAt.getTime() <= options.clock.now().getTime()) {
    return c.json({ error: 'This sign-in link is no longer valid. Start again.' }, 400)
  }
  if (login && login.connectionId !== connection.id) {
    // The state belongs to a different connection. Nothing legitimate produces
    // this, and treating it as a mismatch rather than ignoring it stops a
    // state from one organization being spent at another's endpoint.
    return c.json({ error: 'This sign-in link is no longer valid. Start again.' }, 400)
  }

  const urls = samlUrls(options.baseUrl, connection.handle)

  // 4. What the assertion says.
  let facts
  try {
    facts = readAssertion(verified.assertion, {
      audience: urls.entityId,
      recipient: urls.acsUrl,
      issuer: connection.idpEntityId ?? '',
      inResponseTo: login?.requestId ?? null,
      clockSkewSeconds: connection.clockSkewSeconds,
      now: options.clock.now(),
    })
  } catch (err) {
    if (err instanceof AssertionRefused) return c.json({ error: err.message }, 400)
    throw err
  }

  // 5. Replay. Before provisioning, so a replayed assertion is refused
  // whatever provisioning would have done with it.
  const { fresh } = await rememberAssertion(
    options.pool,
    connection.orgId,
    connection.id,
    facts.id,
    facts.notOnOrAfter,
  )
  if (!fresh) {
    return c.json(
      {
        error:
          'This assertion has already been used. An assertion is valid once; start the sign-in ' +
          'again.',
      },
      400,
    )
  }

  // Opportunistic cleanup, in the tenant we are already in. There is no
  // scheduled sweeper because there is no policy that would let one see these
  // tables, and expiry is enforced when a row is presented rather than by the
  // sweep.
  void sweepOrg(options.pool, connection.orgId, options.clock.now()).catch(() => {})

  return finish(c, options, connection, {
    email: facts.email,
    displayName: facts.displayName,
    givenName: facts.givenName,
    familyName: facts.familyName,
    groups: facts.groups,
    redirectTo: login?.redirectTo ?? null,
  })
}

/** What the provider said, when it sent a failure instead of an assertion. */
function reportedFailure(xml: string): string | null {
  try {
    return statusMessage(parseXml(xml))
  } catch {
    return null
  }
}

// ---------------------------------------------------------------------------
// OIDC
// ---------------------------------------------------------------------------

async function oidcLogin(c: Context, options: SsoOptions): Promise<Response> {
  const connection = await connectionByHandle(options.pool, c.req.param('handle') ?? '')
  if (!connection || connection.kind !== 'oidc' || !connection.enabled) {
    return c.json({ error: 'No such connection.' }, 404)
  }
  return beginFor(c, options, connection, { redirectTo: c.req.query('redirect_to') })
}

async function oidcCallback(c: Context, options: SsoOptions): Promise<Response> {
  const connection = await connectionByHandle(options.pool, c.req.param('handle') ?? '')
  if (!connection || connection.kind !== 'oidc' || !connection.enabled) {
    return c.json({ error: 'No such connection.' }, 404)
  }

  const error = c.req.query('error')
  if (error) {
    const description = c.req.query('error_description')
    return c.json(
      { error: `The identity provider refused the sign-in: ${description || error}` },
      400,
    )
  }

  const code = c.req.query('code')
  const state = c.req.query('state') ?? ''
  if (!code || !state) {
    return c.json({ error: 'This sign-in link is no longer valid. Start again.' }, 400)
  }

  const login = await consumeLoginState(options.pool, state)
  if (!login || login.connectionId !== connection.id) {
    return c.json({ error: 'This sign-in link is no longer valid. Start again.' }, 400)
  }
  if (login.expiresAt.getTime() <= options.clock.now().getTime()) {
    return c.json({ error: 'This sign-in link is no longer valid. Start again.' }, 400)
  }
  if (!login.nonce || !login.codeVerifier) {
    return c.json({ error: 'This sign-in link is no longer valid. Start again.' }, 400)
  }

  let clientSecret: string
  try {
    clientSecret = await readClientSecret(options, connection)
  } catch (err) {
    if (err instanceof SecretUnavailable) return c.json({ error: err.message }, 500)
    throw err
  }

  let claims
  try {
    claims = await completeLogin({
      endpoints: { tokenEndpoint: connection.oidcTokenEndpoint ?? '' },
      jwksUri: connection.oidcJwksUri ?? '',
      issuer: connection.oidcIssuer ?? '',
      clientId: connection.oidcClientId ?? '',
      clientSecret,
      redirectUri: oidcRedirectUri(options.baseUrl, connection.handle),
      code,
      codeVerifier: login.codeVerifier,
      nonce: login.nonce,
      clockSkewSeconds: connection.clockSkewSeconds,
      now: options.clock.now(),
      fetch: options.fetch,
    })
  } catch (err) {
    if (err instanceof TokenRefused) return c.json({ error: err.message }, 400)
    throw err
  }

  const email = typeof claims.email === 'string' ? claims.email.toLowerCase() : null
  if (!email) {
    return c.json(
      {
        error:
          'The identity provider returned no email address. Add the email claim to this ' +
          "application's scope: it is what links a person to their account here.",
      },
      400,
    )
  }

  void sweepOrg(options.pool, connection.orgId, options.clock.now()).catch(() => {})

  return finish(c, options, connection, {
    email,
    displayName: typeof claims.name === 'string' ? claims.name : null,
    givenName: typeof claims.given_name === 'string' ? claims.given_name : null,
    familyName: typeof claims.family_name === 'string' ? claims.family_name : null,
    groups: claimList(claims.groups) ?? claimList(claims.roles) ?? [],
    redirectTo: login.redirectTo,
  })
}

function claimList(value: unknown): string[] | null {
  if (Array.isArray(value)) return value.filter((v): v is string => typeof v === 'string')
  if (typeof value === 'string' && value !== '') return [value]
  return null
}

function oidcRedirectUri(baseUrl: string, handle: string): string {
  const base = baseUrl.endsWith('/') ? baseUrl.slice(0, -1) : baseUrl
  return `${base}/sso/oidc/${handle}/callback`
}

async function readClientSecret(options: SsoOptions, connection: Connection): Promise<string> {
  const secrets = await connectionSecrets(options.pool, connection.orgId, connection.id)
  if (!secrets?.oidcClientSecret) {
    throw new SecretUnavailable(
      'This connection has no client secret stored, so the authorization code cannot be redeemed.',
    )
  }
  const key = options.encryptionKey ?? keyFromEnv()
  return openSecret(secrets.oidcClientSecret, key, connection.orgId)
}

// ---------------------------------------------------------------------------
// Shared
// ---------------------------------------------------------------------------

async function beginFor(
  c: Context,
  options: SsoOptions,
  connection: Connection,
  input: { redirectTo?: string | null; loginHint?: string | null },
): Promise<Response> {
  const now = options.clock.now()
  const expiresAt = new Date(now.getTime() + LOGIN_TTL_MS)
  // Only a path on this application. An absolute URL here is an open redirect
  // on the sign-in path, which is the link people are trained to click and it
  // arrives carrying a session.
  const redirectTo = safeRedirect(input.redirectTo ?? null)

  if (connection.kind === 'saml') {
    const urls = samlUrls(options.baseUrl, connection.handle)
    const relayState = randomBytes(32).toString('base64url')
    const secrets = await connectionSecrets(options.pool, connection.orgId, connection.id)
    const privateKeyBytes = secrets?.spPrivateKey ?? null
    const signing = privateKeyBytes
      ? { privateKey: openSecret(privateKeyBytes, options.encryptionKey ?? keyFromEnv(), connection.orgId) }
      : null

    const request = buildAuthnRequest({
      destination: connection.idpSsoUrl ?? '',
      issuer: urls.entityId,
      acsUrl: urls.acsUrl,
      relayState,
      issueInstant: now,
      signing,
    })

    await saveLoginState(options.pool, {
      state: relayState,
      orgId: connection.orgId,
      connectionId: connection.id,
      requestId: request.id,
      relayState,
      redirectTo,
      expiresAt,
    })
    return c.redirect(request.redirectUrl, 302)
  }

  const secrets = beginLogin()
  await saveLoginState(options.pool, {
    state: secrets.state,
    orgId: connection.orgId,
    connectionId: connection.id,
    nonce: secrets.nonce,
    codeVerifier: secrets.codeVerifier,
    redirectTo,
    expiresAt,
  })

  return c.redirect(
    authorizationUrl({
      endpoints: { authorizationEndpoint: connection.oidcAuthorizationEndpoint ?? '' },
      clientId: connection.oidcClientId ?? '',
      redirectUri: oidcRedirectUri(options.baseUrl, connection.handle),
      secrets,
      loginHint: input.loginHint ?? null,
    }),
    302,
  )
}

/** Provisions the person and hands the browser a session. */
async function finish(
  c: Context,
  options: SsoOptions,
  connection: Connection,
  identity: {
    email: string
    displayName: string | null
    givenName: string | null
    familyName: string | null
    groups: string[]
    redirectTo: string | null
  },
): Promise<Response> {
  const now = options.clock.now()

  let result
  try {
    result = await provision({
      pool: options.pool,
      connection,
      identity,
      now,
      seats: options.seats ? await options.seats(connection.orgId) : null,
      origin: connection.kind === 'saml' ? 'saml' : 'oidc',
    })
  } catch (err) {
    if (err instanceof ProvisioningRefused) {
      // 403 rather than 400: the assertion was fine and this organization will
      // not take the person. The code is in the body so an operator can find
      // the documentation for it.
      return c.json({ error: err.message, code: err.code }, 403)
    }
    throw err
  }

  const existing = readCookie(c.req.header('cookie'), SESSION_COOKIE)
  const issued = await issueSession(options.pool, options.clock, {
    userId: result.userId,
    orgId: connection.orgId,
    ip: c.req.header('x-forwarded-for') ?? undefined,
    userAgent: c.req.header('user-agent') ?? undefined,
    // Rotation. A cookie planted before the sign-in cannot ride the login that
    // follows it.
    replacing: existing ?? undefined,
  })

  c.header('set-cookie', sessionCookie(issued.token, issued.expiresAt, options.secureCookies ?? true))
  const base = options.appBaseUrl.endsWith('/') ? options.appBaseUrl : `${options.appBaseUrl}/`
  return c.redirect(new URL(identity.redirectTo ?? '/', base).toString(), 302)
}

// ---------------------------------------------------------------------------
// Break-glass
// ---------------------------------------------------------------------------

async function breakGlass(c: Context, options: SsoOptions): Promise<Response> {
  // Authenticated, and that is what makes this safe to expose. The person has
  // completed a GitHub sign-in and is holding a session with no tenant, because
  // the policy left them there. There is no unauthenticated lookup keyed on a
  // recovery code anywhere in this feature.
  const token = readCookie(c.req.header('cookie'), SESSION_COOKIE)
  if (!token) {
    return c.json(
      { error: 'Sign in first. A recovery code is used from a signed-in session, not instead of one.' },
      401,
    )
  }
  const session = await resolveSession(options.pool, options.clock, token)
  if (!session) return c.json({ error: 'Sign in first.' }, 401)

  // The community server applies its cross-site check to /trpc only, so this
  // route does its own. Without it, a page on another origin could spend a
  // recovery code on behalf of somebody who is signed in.
  if (!csrfMatches(token, c.req.header(CSRF_HEADER))) {
    return c.json({ error: `This request needs the ${CSRF_HEADER} header from GET /auth/session.` }, 403)
  }

  let body: { orgId?: unknown; code?: unknown }
  try {
    body = (await c.req.json()) as { orgId?: unknown; code?: unknown }
  } catch {
    return c.json({ error: 'The body is not JSON.' }, 400)
  }
  const orgId = typeof body.orgId === 'string' ? body.orgId : null
  const code = typeof body.code === 'string' ? body.code : null
  if (!orgId || !code) {
    return c.json({ error: 'Send the organization and the recovery code.' }, 400)
  }

  try {
    await spendRecoveryCode({
      pool: options.pool,
      orgId,
      userId: session.userId,
      code,
      now: options.clock.now(),
      ip: c.req.header('x-forwarded-for') ?? null,
      userAgent: c.req.header('user-agent') ?? null,
    })
  } catch (err) {
    if (err instanceof BreakGlassRefused) return c.json({ error: err.message }, 403)
    throw err
  }

  // A new session, scoped to the organization the code opened. Rotated rather
  // than amended, because a session's organization is fixed when it is issued
  // and changing it in place would mean a cookie that was valid for no tenant
  // silently becoming valid for one.
  const issued = await issueSession(options.pool, options.clock, {
    userId: session.userId,
    orgId,
    ip: c.req.header('x-forwarded-for') ?? undefined,
    userAgent: c.req.header('user-agent') ?? undefined,
    replacing: token,
  })
  c.header('set-cookie', sessionCookie(issued.token, issued.expiresAt, options.secureCookies ?? true))
  return c.json({ orgId, usedRecoveryCode: true })
}

export { one, text }
