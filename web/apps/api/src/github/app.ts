// Acting as the GitHub App, rather than as a person.
//
// Three credentials, three lifetimes, and keeping them straight is most of what
// this file is for.
//
// The PRIVATE KEY is permanent and is the App's identity. It signs a JWT that
// proves "I am App 4756201" and nothing more: a JWT can list installations and
// mint tokens, and it cannot read a line of anybody's code.
//
// The APP JWT lives ten minutes. GitHub refuses one older than that, and it
// refuses one issued in the future, which is why `iat` is backdated by a minute
// rather than set to now. Clock skew between this container and GitHub is not
// hypothetical: it is the single most common cause of a 401 here, and the error
// GitHub returns for it says nothing about clocks.
//
// The INSTALLATION TOKEN lives an hour and is the one that can read a
// repository. It is scoped to one installation, so a token for one customer's
// installation cannot touch another's, and that is the property that makes it
// safe to hold in memory.
//
// Nothing here writes a credential anywhere. The key comes from the
// environment, the tokens live in a Map, and the process dying loses all of it,
// which is the correct amount of persistence for something that expires in an
// hour.

import { createPrivateKey, createSign, timingSafeEqual, createHmac } from 'node:crypto'
import type { Clock } from '../clock.ts'
import type { InstalledOn } from '../auth/github.ts'

export class GitHubAppError extends Error {}

export interface AppConfig {
  /** The numeric App ID from the App's settings page. */
  appId: string
  /** The PEM GitHub generated. PKCS#1 or PKCS#8; Node reads both. */
  privateKey: string
  /** The webhook secret, for verifying deliveries. */
  webhookSecret: string
  apiBase?: string
}

/**
 * Reads the App's configuration, or returns null when it is not configured.
 *
 * Null rather than throwing, because an installation without a GitHub App is a
 * supported state: the control plane serves, sign-in works, and the parts that
 * need an App say so. A self-hosted operator who has not created one yet should
 * get a working control plane and a clear message, not a crash loop.
 */
export function appConfigFrom(env: Record<string, string | undefined>): AppConfig | null {
  const appId = (env.AF_GITHUB_APP_ID ?? '').trim()
  const privateKey = normalisePem(env.AF_GITHUB_APP_PRIVATE_KEY ?? '')
  const webhookSecret = (env.AF_GITHUB_APP_WEBHOOK_SECRET ?? '').trim()
  if (!appId && !privateKey && !webhookSecret) return null
  if (!appId || !privateKey || !webhookSecret) {
    // Partial is refused rather than half-enabled. A webhook secret with no
    // private key produces an endpoint that verifies deliveries and can do
    // nothing with them, which looks like it is working.
    throw new GitHubAppError(
      'The GitHub App is half configured. AF_GITHUB_APP_ID, AF_GITHUB_APP_PRIVATE_KEY ' +
        'and AF_GITHUB_APP_WEBHOOK_SECRET are needed together, or none of them.',
    )
  }
  return { appId, privateKey, webhookSecret, apiBase: env.AF_GITHUB_API_BASE }
}

/**
 * Puts a PEM back together after an environment variable has flattened it.
 *
 * A PEM is multi-line and most ways of getting one into a container are not.
 * Docker `-e`, a Kubernetes secret edited by hand, and a shell that lost the
 * quoting all turn the newlines into the two characters backslash-n, and the
 * resulting string is a valid-looking key that Node refuses with
 * "error:1E08010C:DECODER routines::unsupported" -- a message that sends
 * whoever reads it to the wrong place entirely.
 */
export function normalisePem(raw: string): string {
  const value = raw.trim()
  if (!value) return ''
  // Base64 with no header is also accepted, because it is the one encoding that
  // survives every transport unchanged.
  if (!value.includes('-----BEGIN')) {
    try {
      const decoded = Buffer.from(value, 'base64').toString('utf8')
      if (decoded.includes('-----BEGIN')) return decoded.trim() + '\n'
    } catch {
      // Not base64. Fall through and let the caller fail on the real value.
    }
    return value
  }
  return value.replace(/\\n/g, '\n').trim() + '\n'
}

/** A JWT proving this process holds the App's private key. */
export function appJwt(config: AppConfig, clock: Clock): string {
  const now = Math.floor(clock.now().getTime() / 1000)
  const header = { alg: 'RS256', typ: 'JWT' }
  const payload = {
    // Backdated by a minute. GitHub refuses a token issued in the future, and
    // a container whose clock is a few seconds ahead is ordinary.
    iat: now - 60,
    // Nine minutes, not ten. Ten is the maximum GitHub accepts, so asking for
    // exactly ten is asking to be refused the moment either clock disagrees.
    exp: now + 540,
    iss: config.appId,
  }
  const signingInput = `${base64url(JSON.stringify(header))}.${base64url(JSON.stringify(payload))}`

  let key
  try {
    key = createPrivateKey(config.privateKey)
  } catch (err) {
    throw new GitHubAppError(
      'AF_GITHUB_APP_PRIVATE_KEY is not a private key Node can read. It should be the ' +
        'PEM GitHub generated, newlines and all, or that PEM base64 encoded. ' +
        `The underlying error was: ${(err as Error).message}`,
    )
  }
  const signature = createSign('RSA-SHA256').update(signingInput).sign(key)
  return `${signingInput}.${signature.toString('base64url')}`
}

function base64url(s: string): string {
  return Buffer.from(s, 'utf8').toString('base64url')
}

interface CachedToken {
  token: string
  expiresAt: number
}

/**
 * Mints and reuses installation tokens.
 *
 * Cached because GitHub rate limits token creation, and because a page that
 * lists ten repositories would otherwise mint ten tokens. Expired a minute
 * early on purpose: a token that is valid when this process checks it and
 * expired when GitHub checks it produces a 401 in the middle of an operation,
 * and a minute is longer than any request here takes.
 */
export class InstallationTokens {
  private readonly config: AppConfig
  private readonly clock: Clock
  private readonly fetchImpl: typeof fetch
  private readonly cache = new Map<number, CachedToken>()

  constructor(config: AppConfig, clock: Clock, fetchImpl: typeof fetch = fetch) {
    this.config = config
    this.clock = clock
    this.fetchImpl = fetchImpl
  }

  get apiBase(): string {
    return this.config.apiBase ?? 'https://api.github.com'
  }

  async for(installationId: number): Promise<string> {
    const now = this.clock.now().getTime()
    const cached = this.cache.get(installationId)
    if (cached && cached.expiresAt - 60_000 > now) return cached.token

    const res = await this.fetchImpl(
      new URL(`/app/installations/${installationId}/access_tokens`, this.apiBase),
      {
        method: 'POST',
        headers: {
          authorization: `Bearer ${appJwt(this.config, this.clock)}`,
          accept: 'application/vnd.github+json',
          'x-github-api-version': '2022-11-28',
        },
      },
    )
    if (!res.ok) {
      const body = await res.text().catch(() => '')
      // 404 here is the confusing one and is called out: GitHub answers 404 for
      // an installation this App cannot see, which is what a wrong App ID looks
      // like, not what a missing installation looks like.
      const hint =
        res.status === 404
          ? ' A 404 here usually means the App ID does not match the private key, not that the installation is gone.'
          : ''
      throw new GitHubAppError(
        `GitHub refused an installation token for ${installationId}: ${res.status}.${hint} ${body.slice(0, 200)}`,
      )
    }
    const json = (await res.json()) as { token: string; expires_at: string }
    this.cache.set(installationId, {
      token: json.token,
      expiresAt: new Date(json.expires_at).getTime(),
    })
    return json.token
  }

  /**
   * The installation covering a repository, or null when there is none.
   *
   * The App JWT, not an installation token, because only the App may ask which
   * installations exist. That is also what makes this the one honest answer to
   * "was this App granted Actions write here": the response carries the
   * permissions GitHub actually recorded for the installation, which is not
   * the same set the App declares. An App can declare `actions: write` and
   * every existing installation still hold none of it, because widening an
   * App's permissions asks each installation to accept the new grant and
   * changes nothing until somebody does.
   *
   * Not cached. It is read on a failure path and on a page load, both of them
   * rare next to the token this class exists for, and a cached permission map
   * would keep telling somebody their grant is missing for an hour after they
   * granted it, which is the moment they are most likely to be looking.
   */
  async onRepository(repository: string): Promise<InstalledOn | null> {
    const path = `/repos/${repository.split('/').map(encodeURIComponent).join('/')}/installation`
    const res = await this.fetchImpl(new URL(path, this.apiBase), {
      headers: {
        authorization: `Bearer ${appJwt(this.config, this.clock)}`,
        accept: 'application/vnd.github+json',
        'x-github-api-version': '2022-11-28',
      },
    })
    // 404 is the answer, not a failure: it is what GitHub says for a
    // repository this App holds no installation on, and for one that does not
    // exist. The caller separates those with a second question.
    if (res.status === 404) return null
    if (!res.ok) {
      const body = await res.text().catch(() => '')
      throw new GitHubAppError(
        `GitHub refused to say which installation covers ${repository}: ${res.status}. ${body.slice(0, 200)}`,
      )
    }
    const json = (await res.json()) as { id: number; permissions?: Record<string, string> }
    return { id: json.id, permissions: json.permissions ?? {} }
  }

  /** Drops a cached token, for when GitHub says one is no longer valid. */
  forget(installationId: number): void {
    this.cache.delete(installationId)
  }
}

/**
 * Is this delivery really from GitHub?
 *
 * HMAC-SHA256 over the RAW body with the webhook secret. The raw body, not a
 * re-serialised object: JSON.stringify(JSON.parse(body)) differs from body
 * whenever key order, unicode escaping or number formatting differ, and every
 * one of those happens in real payloads. A verifier that re-serialises works in
 * testing and fails on the first delivery containing an emoji.
 *
 * Compared with timingSafeEqual, because a byte-at-a-time comparison leaks the
 * expected signature to anybody willing to send a few thousand requests.
 */
export function verifySignature(secret: string, rawBody: string, header: string | undefined): boolean {
  if (!header) return false
  const expected = 'sha256=' + createHmac('sha256', secret).update(rawBody, 'utf8').digest('hex')
  const a = Buffer.from(expected, 'utf8')
  const b = Buffer.from(header, 'utf8')
  // Length is checked first because timingSafeEqual throws on a mismatch, and a
  // throw here would be a 500 for what is simply a wrong signature.
  if (a.length !== b.length) return false
  return timingSafeEqual(a, b)
}
