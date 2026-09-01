// Proving a request came from one job of one workflow run, with no shared secret.
//
// A job in the customer's continuous integration has no session and no tenant,
// and it needs to report a result about one commit. The obvious answer is a
// repository secret holding a control plane token, and it is the wrong one for
// three reasons that all matter here.
//
// It is organization wide. Any job in any workflow in that repository can read
// it, so a credential meant to say "this run, this commit" says "somebody in
// this repository", and the whole point of the SHA fence upstream is that a
// result belongs to one commit.
//
// It has to be created by a person, per repository, before anything works, and
// a product whose first step is "paste this secret into every repository" has
// lost most of its users at step one.
//
// And it does not distinguish a fork. GitHub Actions withholds secrets from a
// pull request job running on a fork, which is the one thing keeping a fork's
// code from running with the base repository's credentials, and a customer who
// works around that (a `pull_request_target` workflow, a self-hosted runner) has
// silently handed a fork the token.
//
// So the job proves who it is instead. GitHub Actions can mint a short lived
// OpenID Connect token for a job, signed by GitHub, whose claims name the
// repository, the workflow, the run and the ref. It requires `id-token: write`
// in the workflow, and GitHub does NOT grant that to a pull request job from a
// fork, so the fork case is closed by GitHub's own rules rather than by this
// control plane remembering to check.
//
// THE TWO ATTACKS ON A JWT, AND WHY NEITHER HAS A CODE PATH HERE.
//
//   alg: none. The header says the token is unsigned, a lenient verifier finds
//   no signature to check and returns success, and every claim in it is
//   whatever the attacker typed.
//
//   Algorithm confusion. The header says HS256, the verifier reaches for "the
//   key" and finds GitHub's RSA PUBLIC key, which anybody can fetch, and uses
//   it as an HMAC secret. Anybody who can read the key set can then mint
//   tokens.
//
// The defence for both is the same and it is not a special case for each: the
// algorithm is not read from the header as an instruction. GitHub signs these
// with RS256 and nothing else, so RS256 is the only algorithm this accepts, the
// key has to be an RSA key, and there is no code path in which any HMAC is
// computed at all.

import { createPublicKey, verify as verifySignature, type KeyObject } from 'node:crypto'
import type { Clock } from '../clock.ts'

export class TokenRefused extends Error {
  readonly reason: string

  constructor(reason: string, message: string) {
    super(message)
    this.name = 'TokenRefused'
    this.reason = reason
  }
}

/** GitHub's issuer for workflow identity tokens. */
export const ACTIONS_ISSUER = 'https://token.actions.githubusercontent.com'

/**
 * The audience a workflow has to ask for.
 *
 * Not the default. GitHub's default audience is the repository owner's URL,
 * which every workflow of every repository in that organization gets by asking
 * for nothing, so a token minted for an unrelated purpose in an unrelated
 * workflow would be accepted here. Naming an audience makes the token useless
 * anywhere else, and makes a token minted for somewhere else useless here.
 */
export const CALLBACK_AUDIENCE = 'antifailure-control-plane'

export interface Jwk {
  kty?: string
  kid?: string
  use?: string
  alg?: string
  n?: string
  e?: string
}

/** What a verified workflow identity token says about the job that holds it. */
export interface WorkflowIdentity {
  /** owner/name of the repository the job is running in. */
  repository: string
  /** The GitHub numeric id of that repository's owner, which does not change
   *  when an organization is renamed. */
  repositoryOwner: string
  /** The run this job belongs to, which is what teardown cancels. */
  runId: number
  runAttempt: number
  /** refs/pull/N/merge for a pull request, refs/heads/x otherwise. */
  ref: string
  eventName: string
  /** The workflow file, as `owner/name/.github/workflows/f.yml@refs/...`. */
  jobWorkflowRef: string
  /** The commit the job is running against. For a pull request this is the
   *  MERGE commit rather than the head, which is why the head is taken from the
   *  request body and checked against the generation instead of being read
   *  from here. */
  sha: string
}

export interface VerifyOptions {
  keys: readonly Jwk[]
  clock: Clock
  /** Seconds of tolerance on the expiry and the issued-at. Small: both clocks
   *  are machines talking to GitHub. */
  clockSkewSeconds?: number
  audience?: string
  issuer?: string
}

/**
 * Verifies an Actions workflow identity token and returns what it claims.
 *
 * Everything after the signature check runs only because the signature checked.
 * That ordering is the whole file: reading a claim to decide whether to trust
 * the token has it backwards.
 */
export function verifyWorkflowIdentity(token: string, options: VerifyOptions): WorkflowIdentity {
  const parts = token.split('.')
  if (parts.length !== 3) {
    throw new TokenRefused('malformed', 'The identity token is not a JWT.')
  }
  const [headerPart, payloadPart, signaturePart] = parts as [string, string, string]

  const header = decodeJson(headerPart, 'header') as { alg?: string; kid?: string }
  // Before the key is even looked at. `none` and every HMAC are refused here,
  // so neither reaches a code path that could return success.
  if (header.alg !== 'RS256') {
    throw new TokenRefused(
      'bad_algorithm',
      `The identity token is signed with ${header.alg || 'no stated algorithm'}. GitHub signs ` +
        `these with RS256 and nothing else, so nothing else is accepted: "none" would make the ` +
        `token unsigned, and an HMAC would let anybody holding GitHub's published public key ` +
        `mint one.`,
    )
  }

  const signature = base64UrlDecode(signaturePart, 'signature')
  const signed = Buffer.from(`${headerPart}.${payloadPart}`, 'ascii')

  const candidates = options.keys.filter((key) => {
    if (key.kty && key.kty !== 'RSA') return false
    if (key.use && key.use !== 'sig') return false
    if (header.kid && key.kid) return key.kid === header.kid
    return true
  })
  if (candidates.length === 0) {
    throw new TokenRefused(
      'no_key',
      header.kid
        ? `GitHub's key set has no signing key with kid ${header.kid}. It may have rotated since ` +
            `this was last fetched.`
        : "GitHub's key set has no RSA signing key.",
    )
  }

  const verified = candidates.some((jwk) => {
    let key: KeyObject
    try {
      key = createPublicKey({ key: jwk as never, format: 'jwk' })
    } catch {
      return false
    }
    // The key's own type has to match. This is the second half of the confusion
    // defence: even inside an allow-list of one, a key that is not RSA may not
    // be used to check something claiming RS256.
    if (key.asymmetricKeyType !== 'rsa') return false
    try {
      return verifySignature('sha256', signed, key, signature)
    } catch {
      return false
    }
  })
  if (!verified) {
    throw new TokenRefused('invalid_signature', 'The signature on the identity token is not valid.')
  }

  const claims = decodeJson(payloadPart, 'payload') as Record<string, unknown>
  const now = Math.floor(options.clock.now().getTime() / 1000)
  const skew = options.clockSkewSeconds ?? 60

  const issuer = options.issuer ?? ACTIONS_ISSUER
  if (claims.iss !== issuer) {
    throw new TokenRefused(
      'wrong_issuer',
      `The identity token was issued by ${String(claims.iss ?? 'nobody')} and this endpoint ` +
        `accepts ${issuer}.`,
    )
  }

  const audience = options.audience ?? CALLBACK_AUDIENCE
  const audiences = Array.isArray(claims.aud) ? claims.aud : claims.aud ? [claims.aud] : []
  if (!audiences.includes(audience)) {
    throw new TokenRefused(
      'wrong_audience',
      `The identity token was issued for ${audiences.join(', ') || 'nobody'} and this endpoint ` +
        `is ${audience}. Ask for it with that audience: a token GitHub minted for something else ` +
        `is a valid token for something else.`,
    )
  }

  if (typeof claims.exp !== 'number') {
    throw new TokenRefused('no_expiry', 'The identity token states no expiry, so it never expires.')
  }
  if (now - skew >= claims.exp) {
    throw new TokenRefused('expired', 'The identity token has expired. Mint a new one.')
  }
  if (typeof claims.iat === 'number' && claims.iat - skew > now) {
    throw new TokenRefused(
      'not_yet_valid',
      `The identity token was issued at ${new Date(claims.iat * 1000).toISOString()}, which is in ` +
        `the future by more than the ${skew} second tolerance. Check both clocks.`,
    )
  }

  const repository = claims.repository
  if (typeof repository !== 'string' || !/^[^/]+\/[^/]+$/.test(repository)) {
    throw new TokenRefused(
      'no_repository',
      'The identity token does not name a repository, so there is nothing to scope it to.',
    )
  }

  const runId = Number(claims.run_id)
  if (!Number.isSafeInteger(runId) || runId <= 0) {
    throw new TokenRefused(
      'no_run',
      'The identity token does not name a workflow run, so the credential it asks for could not ' +
        'be tied to one.',
    )
  }

  return {
    repository,
    repositoryOwner: typeof claims.repository_owner === 'string' ? claims.repository_owner : '',
    runId,
    runAttempt: Number(claims.run_attempt) || 1,
    ref: typeof claims.ref === 'string' ? claims.ref : '',
    eventName: typeof claims.event_name === 'string' ? claims.event_name : '',
    jobWorkflowRef: typeof claims.job_workflow_ref === 'string' ? claims.job_workflow_ref : '',
    sha: typeof claims.sha === 'string' ? claims.sha : '',
  }
}

/**
 * Fetches and caches GitHub's signing keys.
 *
 * Cached because a key set fetch on every callback is a dependency on
 * github.com in the path a customer's job blocks on, and GitHub rotates these
 * rarely. Refetched on a kid this cache has never seen, because that is what a
 * rotation looks like from here, and rate limited to one refetch a minute so an
 * attacker sending tokens with random kids cannot turn this endpoint into a
 * load generator against GitHub.
 */
export class ActionsKeys {
  private keys: Jwk[] = []
  private fetchedAt = 0
  private readonly clock: Clock
  private readonly fetchImpl: typeof fetch
  private readonly url: string

  constructor(clock: Clock, options: { fetchImpl?: typeof fetch; issuer?: string } = {}) {
    this.clock = clock
    this.fetchImpl = options.fetchImpl ?? fetch
    this.url = `${options.issuer ?? ACTIONS_ISSUER}/.well-known/jwks`
  }

  /** How long a key set is used before a token with an unknown kid may cause a
   *  refetch. */
  static readonly MIN_REFETCH_MS = 60_000
  /** And how long before one is refetched anyway. */
  static readonly MAX_AGE_MS = 6 * 60 * 60 * 1000

  async current(kid: string | null): Promise<readonly Jwk[]> {
    const now = this.clock.now().getTime()
    const stale = now - this.fetchedAt > ActionsKeys.MAX_AGE_MS
    const unknown = kid !== null && !this.keys.some((k) => k.kid === kid)
    const mayRefetch = now - this.fetchedAt > ActionsKeys.MIN_REFETCH_MS

    if (this.keys.length === 0 || stale || (unknown && mayRefetch)) {
      await this.refetch()
    }
    return this.keys
  }

  private async refetch(): Promise<void> {
    const res = await this.fetchImpl(this.url, { headers: { accept: 'application/json' } })
    if (!res.ok) {
      throw new TokenRefused(
        'keys_unavailable',
        `GitHub's signing keys could not be fetched: ${res.status}.`,
      )
    }
    const body = (await res.json()) as { keys?: unknown }
    if (!Array.isArray(body.keys)) {
      throw new TokenRefused('keys_unavailable', "GitHub's key set is not a list of keys.")
    }
    // One element at a time, skipping what does not decode. A key set with one
    // malformed entry must not leave this with no keys at all, which would
    // refuse every genuine token.
    const keys: Jwk[] = []
    for (const item of body.keys) {
      const key = item as Jwk
      if (key && typeof key === 'object' && typeof key.n === 'string' && typeof key.e === 'string') {
        keys.push(key)
      }
    }
    if (keys.length === 0) {
      throw new TokenRefused('keys_unavailable', "GitHub's key set carries no usable RSA key.")
    }
    this.keys = keys
    this.fetchedAt = this.clock.now().getTime()
  }
}

/** The `kid` of a token, read without verifying anything, so the key set can be
 *  refetched for a key it has never seen. Nothing is trusted from this. */
export function kidOf(token: string): string | null {
  const headerPart = token.split('.')[0]
  if (!headerPart) return null
  try {
    const header = JSON.parse(base64UrlDecode(headerPart, 'header').toString('utf8')) as {
      kid?: unknown
    }
    return typeof header.kid === 'string' ? header.kid : null
  } catch {
    return null
  }
}

function decodeJson(part: string, what: string): unknown {
  let parsed: unknown
  try {
    parsed = JSON.parse(base64UrlDecode(part, what).toString('utf8'))
  } catch {
    throw new TokenRefused('malformed', `The identity token ${what} is not JSON.`)
  }
  if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw new TokenRefused('malformed', `The identity token ${what} is not an object.`)
  }
  return parsed
}

function base64UrlDecode(value: string, what: string): Buffer {
  if (!/^[A-Za-z0-9_-]*$/.test(value)) {
    throw new TokenRefused('malformed', `The identity token ${what} is not base64url.`)
  }
  return Buffer.from(value, 'base64url')
}
