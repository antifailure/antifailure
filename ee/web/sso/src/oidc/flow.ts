// The authorization code flow, with PKCE, and the discovery that configures it.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// PKCE is required here even though this is a confidential client that holds a
// secret, and the specification only requires it for public ones. The reason is
// what PKCE actually defends: an authorization code that leaks, from a referrer
// header, a proxy log, a browser extension, or a redirect that went somewhere
// it should not. A confidential client's secret stops somebody else redeeming
// the code from their own server; it does nothing about somebody feeding a
// stolen code into OUR callback, which is the injection attack. The verifier
// does, because the code is bound to a secret that never left this process.
//
// state and nonce are separate values doing separate jobs, and using one for
// both is a common shortcut that gives up one of the two properties. state is
// round-tripped through the browser and stops cross-site request forgery on the
// callback; it is checked against a stored row and consumed. nonce goes to the
// provider and comes back inside the signed id_token, and is what binds the
// token to this login rather than to some other login at the same provider.

import { createHash, randomBytes } from 'node:crypto'
import { TokenRefused, verifyIdToken, type Jwk, type JwtClaims } from './jwt.ts'

export class DiscoveryRefused extends Error {}

export interface ProviderEndpoints {
  issuer: string
  authorizationEndpoint: string
  tokenEndpoint: string
  jwksUri: string
  userinfoEndpoint: string | null
}

/** What a login has to remember between the redirect and the callback. */
export interface LoginSecrets {
  state: string
  nonce: string
  codeVerifier: string
}

export function beginLogin(): LoginSecrets {
  return {
    state: randomBytes(32).toString('base64url'),
    nonce: randomBytes(32).toString('base64url'),
    // 43 characters is the specification's minimum and 32 bytes base64url is
    // exactly that. The verifier never leaves this process; only its hash goes
    // to the provider.
    codeVerifier: randomBytes(32).toString('base64url'),
  }
}

export function codeChallenge(verifier: string): string {
  return createHash('sha256').update(verifier, 'ascii').digest('base64url')
}

export interface AuthorizationUrlInput {
  endpoints: Pick<ProviderEndpoints, 'authorizationEndpoint'>
  clientId: string
  redirectUri: string
  secrets: LoginSecrets
  /** Extra scopes beyond openid, email and profile. */
  scopes?: readonly string[]
  /** Sent when the sign-in page already knows who is arriving, so the provider
   *  can skip its own account picker. */
  loginHint?: string | null
  prompt?: 'login' | 'select_account' | null
}

export function authorizationUrl(input: AuthorizationUrlInput): string {
  const url = new URL(input.endpoints.authorizationEndpoint)
  const scopes = new Set(['openid', 'email', 'profile', ...(input.scopes ?? [])])

  url.searchParams.set('response_type', 'code')
  url.searchParams.set('client_id', input.clientId)
  url.searchParams.set('redirect_uri', input.redirectUri)
  url.searchParams.set('scope', [...scopes].join(' '))
  url.searchParams.set('state', input.secrets.state)
  url.searchParams.set('nonce', input.secrets.nonce)
  url.searchParams.set('code_challenge', codeChallenge(input.secrets.codeVerifier))
  // S256 and never `plain`. `plain` sends the verifier itself, which defends
  // against nothing at all: whoever intercepted the code intercepted the
  // challenge beside it.
  url.searchParams.set('code_challenge_method', 'S256')
  if (input.loginHint) url.searchParams.set('login_hint', input.loginHint)
  if (input.prompt) url.searchParams.set('prompt', input.prompt)
  return url.toString()
}

export interface TokenResponse {
  idToken: string
  accessToken: string | null
  refreshToken: string | null
  expiresIn: number | null
}

export type Fetch = typeof globalThis.fetch

export interface ExchangeInput {
  endpoints: Pick<ProviderEndpoints, 'tokenEndpoint'>
  clientId: string
  clientSecret: string
  redirectUri: string
  code: string
  codeVerifier: string
  fetch?: Fetch
  timeoutMs?: number
}

/** Redeems an authorization code. */
export async function exchangeCode(input: ExchangeInput): Promise<TokenResponse> {
  const body = new URLSearchParams({
    grant_type: 'authorization_code',
    code: input.code,
    redirect_uri: input.redirectUri,
    client_id: input.clientId,
    client_secret: input.clientSecret,
    code_verifier: input.codeVerifier,
  })

  const response = await withTimeout(
    (signal) =>
      (input.fetch ?? fetch)(input.endpoints.tokenEndpoint, {
        method: 'POST',
        headers: {
          'content-type': 'application/x-www-form-urlencoded',
          accept: 'application/json',
        },
        body: body.toString(),
        signal,
      }),
    input.timeoutMs ?? 10_000,
    'the token endpoint',
  )

  const text = await response.text()
  let parsed: Record<string, unknown>
  try {
    parsed = JSON.parse(text) as Record<string, unknown>
  } catch {
    throw new TokenRefused(
      'token_endpoint',
      `The token endpoint answered ${response.status} with something that is not JSON.`,
    )
  }

  if (!response.ok) {
    // The provider's own error, quoted. This is the message somebody debugging
    // a misconfigured client actually needs, and it names no secret: the
    // request carried the secret, the response does not.
    const code = typeof parsed.error === 'string' ? parsed.error : String(response.status)
    const detail =
      typeof parsed.error_description === 'string' ? `: ${parsed.error_description}` : ''
    throw new TokenRefused('token_endpoint', `The identity provider refused the code (${code}${detail}).`)
  }

  const idToken = parsed.id_token
  if (typeof idToken !== 'string' || idToken === '') {
    throw new TokenRefused(
      'no_id_token',
      'The token response carries no id_token. The connection is probably missing the openid scope.',
    )
  }

  return {
    idToken,
    accessToken: typeof parsed.access_token === 'string' ? parsed.access_token : null,
    refreshToken: typeof parsed.refresh_token === 'string' ? parsed.refresh_token : null,
    expiresIn: typeof parsed.expires_in === 'number' ? parsed.expires_in : null,
  }
}

export interface CompleteInput extends ExchangeInput {
  issuer: string
  jwksUri: string
  nonce: string
  clockSkewSeconds: number
  now: Date
}

/** Redeems the code and verifies the token that comes back. */
export async function completeLogin(input: CompleteInput): Promise<JwtClaims> {
  const tokens = await exchangeCode(input)
  const keys = await fetchJwks(input.jwksUri, input.fetch, input.timeoutMs)
  return verifyIdToken(tokens.idToken, {
    keys,
    issuer: input.issuer,
    clientId: input.clientId,
    nonce: input.nonce,
    clockSkewSeconds: input.clockSkewSeconds,
    now: input.now,
    accessToken: tokens.accessToken,
  })
}

export async function fetchJwks(
  uri: string,
  doFetch?: Fetch,
  timeoutMs = 10_000,
): Promise<Jwk[]> {
  const response = await withTimeout(
    (signal) => (doFetch ?? fetch)(uri, { headers: { accept: 'application/json' }, signal }),
    timeoutMs,
    'the key set',
  )
  if (!response.ok) {
    throw new TokenRefused('jwks', `The provider's key set answered ${response.status}.`)
  }
  const parsed = (await response.json()) as { keys?: unknown }
  if (!Array.isArray(parsed.keys)) {
    throw new TokenRefused('jwks', "The provider's key set has no keys array.")
  }
  return parsed.keys as Jwk[]
}

/**
 * Reads a provider's discovery document.
 *
 * Run when a connection is configured, not on every login. Discovery is a
 * network call to somebody else's service, and putting it on the critical path
 * of every sign-in means their brief outage is our sign-in outage.
 */
export async function discover(
  issuer: string,
  doFetch?: Fetch,
  timeoutMs = 10_000,
): Promise<ProviderEndpoints> {
  const base = issuer.endsWith('/') ? issuer.slice(0, -1) : issuer
  const url = `${base}/.well-known/openid-configuration`

  const response = await withTimeout(
    (signal) => (doFetch ?? fetch)(url, { headers: { accept: 'application/json' }, signal }),
    timeoutMs,
    'the discovery document',
  )
  if (!response.ok) {
    throw new DiscoveryRefused(`${url} answered ${response.status}.`)
  }

  const document = (await response.json()) as Record<string, unknown>
  const stated = document.issuer

  // The document has to agree about who it belongs to. A discovery document
  // fetched from one host that names another is either a misconfiguration or
  // somebody pointing a connection at a provider they control, and either way
  // the issuer is what every id_token is checked against, so taking it from an
  // unverified document would make that check circular.
  if (typeof stated !== 'string' || normalise(stated) !== normalise(issuer)) {
    throw new DiscoveryRefused(
      `The discovery document at ${url} says its issuer is ${String(stated)}, not ${issuer}. ` +
        `The issuer is what every token is checked against, so a document that disagrees about ` +
        `its own identity is not usable.`,
    )
  }

  const endpoints: ProviderEndpoints = {
    issuer: stated,
    authorizationEndpoint: requireUrl(document, 'authorization_endpoint', url),
    tokenEndpoint: requireUrl(document, 'token_endpoint', url),
    jwksUri: requireUrl(document, 'jwks_uri', url),
    userinfoEndpoint:
      typeof document.userinfo_endpoint === 'string' ? document.userinfo_endpoint : null,
  }

  // Every endpoint must be https. A token endpoint over plain HTTP would carry
  // the client secret in clear on every login.
  for (const [name, value] of Object.entries(endpoints)) {
    if (typeof value === 'string' && /^http:\/\//i.test(value)) {
      throw new DiscoveryRefused(
        `The provider's ${name} is ${value}. It must be https: the token exchange carries the ` +
          `client secret and the authorization code.`,
      )
    }
  }
  return endpoints
}

function requireUrl(document: Record<string, unknown>, field: string, where: string): string {
  const value = document[field]
  if (typeof value !== 'string' || value === '') {
    throw new DiscoveryRefused(`The discovery document at ${where} states no ${field}.`)
  }
  return value
}

const normalise = (value: string) => (value.endsWith('/') ? value.slice(0, -1) : value)

/**
 * A request that cannot hang.
 *
 * Every call here is to a service somebody else operates, on the critical path
 * of a person trying to log in. Without a deadline, a provider that accepts the
 * connection and never answers holds the request open until something else
 * gives up, and the something else is usually the browser.
 */
async function withTimeout(
  run: (signal: AbortSignal) => Promise<Response>,
  ms: number,
  what: string,
): Promise<Response> {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), ms)
  try {
    return await run(controller.signal)
  } catch (err) {
    if (controller.signal.aborted) {
      throw new TokenRefused('timeout', `${what} did not answer within ${ms / 1000} seconds.`)
    }
    throw err
  } finally {
    clearTimeout(timer)
  }
}
