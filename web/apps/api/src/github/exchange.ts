// Turning a GitHub Actions workflow identity into an engine token.
//
// A customer running Antifailure in CI should set no token, no environment
// variable and no repository secret. The job asks GitHub Actions for an
// identity token, posts it here, and gets back a credential that works on
// POST /v1/events exactly as a static one does and stops working within the
// quarter hour. src/github/oidc.ts explains why an identity token beats a
// repository secret and how the signature is checked. This file is about the
// question that check does NOT answer.
//
// THE QUESTION THE SIGNATURE DOES NOT ANSWER.
//
// A verified identity token says, truthfully and unforgeably, "this job runs in
// repository R". It says nothing whatever about who R belongs to. Anybody with
// a GitHub account can create a repository, put `id-token: write` in a
// workflow, and mint a perfectly valid token whose `repository` claim names it.
// GitHub will sign it, this server will verify it, and every claim in it will
// be true.
//
// So a verifier that reads `repository` or `repository_owner` and looks up "the
// organization for that owner" has authenticated a stranger flawlessly and then
// authorized them anyway. That is not a subtle failure: it is one workflow file
// in a repository the attacker owns, and it would let them write events into
// whichever tenant the lookup happened to land on.
//
// The claim is therefore treated as an identity and never as a permission. The
// permission comes from a binding: an organization has to have claimed that
// exact repository in advance, the claim has to still be live, and the
// organization has to still hold a GitHub App installation on the repository's
// owner. A repository with no binding is refused. That refusal is the feature.
//
// WHAT AN ISSUED TOKEN IS WORTH. Fifteen minutes, one organization, and
// nothing else: it carries no scopes, so it cannot reach the CLI surface, and
// it is stored as a SHA-256 hash like every other credential here, so the row
// it lives in is not usable as one. Revoking the binding revokes the tokens it
// produced in the same statement, because a revocation that leaves live
// credentials behind has not revoked anything.

import { createHash, randomBytes } from 'node:crypto'
import { sql } from 'drizzle-orm'
import type { Context, Hono } from 'hono'
import { appendAudit, type Pool } from '@antifailure/db'
import type { Clock } from '../clock.ts'
import { RateLimiter } from '../ratelimit.ts'
import {
  ActionsKeys,
  CALLBACK_AUDIENCE,
  TokenRefused,
  kidOf,
  verifyWorkflowIdentity,
} from './oidc.ts'

/**
 * How long an exchanged token lives.
 *
 * Long enough for a job that takes a while to finish reporting, short enough
 * that a token scraped out of a runner's memory is worth very little by the
 * time anybody has it. The engine re-exchanges rather than caching it, which
 * costs one signed request per job.
 */
export const OIDC_TOKEN_TTL_MS = 15 * 60 * 1000

/** The audience a workflow has to ask for. Re-exported so a caller reads it
 *  from one place and the engine and the docs cannot drift from the verifier. */
export { CALLBACK_AUDIENCE }

/** A refusal with a machine-readable reason, so the engine can tell "your
 *  workflow is misconfigured" from "nobody has claimed this repository". */
export class ExchangeRefused extends Error {
  readonly reason: string
  readonly status: 401 | 403 | 429
  /** How long to wait, for the one refusal that is worth retrying. Carried
   *  rather than guessed at the route, because a Retry-After that disagrees
   *  with the limiter tells a client to come back at the wrong moment, and the
   *  client that obeys it is refused a second time for being early. */
  readonly retryAfterSeconds: number | undefined

  constructor(
    reason: string,
    status: 401 | 403 | 429,
    message: string,
    retryAfterSeconds?: number,
  ) {
    super(message)
    this.name = 'ExchangeRefused'
    this.reason = reason
    this.status = status
    this.retryAfterSeconds = retryAfterSeconds
  }
}

/** A binding could not be created or revoked, for a reason a person caused. */
export class BindingError extends Error {
  readonly reason: string

  constructor(reason: string, message: string) {
    super(message)
    this.name = 'BindingError'
    this.reason = reason
  }
}

export interface BindingRow {
  id: string
  repository: string
  createdAt: Date
  lastUsedAt: Date | null
  revokedAt: Date | null
}

export interface ExchangeDeps {
  pool: Pool
  clock: Clock
  actionsKeys: ActionsKeys
  /** Bounds the exchange per repository, which is the only subject that means
   *  anything here. See the comment on `repositoryLimiter` below. */
  limiter: RateLimiter
}

export interface ExchangedToken {
  /** Returned exactly once and stored nowhere. */
  token: string
  expiresAt: Date
  orgId: string
  repository: string
}

/**
 * The default per-repository bound on the exchange.
 *
 * The catalog limit on the route is keyed on the address, and on this endpoint
 * that is close to useless in both directions: every GitHub-hosted runner
 * egresses from a shared pool of Azure addresses, so one customer's honest
 * traffic and an attacker's arrive from the same neighbourhood, and a number
 * tight enough to matter would refuse real jobs. The address limit is therefore
 * sized to stop a flood, and this is the one that stops a repository from
 * minting credentials in a loop.
 *
 * Applied AFTER the signature check, deliberately. A limiter keyed on an
 * unverified claim is a limiter an attacker fills on somebody else's behalf.
 */
export function repositoryLimiter(clock: Clock): RateLimiter {
  // A job exchanges once. Twenty at once covers an organization starting a
  // large matrix build in one repository; a sustained two a second is not a
  // build.
  return new RateLimiter(clock, { rate: 2, burst: 20 })
}

function hashToken(token: string): Buffer {
  return createHash('sha256').update(token, 'utf8').digest()
}

/** owner/name as this table stores it, or null when it is not a repository. */
function normalizeRepository(value: string): string | null {
  const repository = value.trim().toLowerCase()
  return /^[a-z0-9._-]+\/[a-z0-9._-]+$/.test(repository) ? repository : null
}

// ---------------------------------------------------------------------------
// The exchange
// ---------------------------------------------------------------------------

/**
 * Verifies a workflow identity token and mints an engine token for it.
 *
 * The order is the security of this function and it only reads one way:
 * nothing about the request is believed until the signature has checked, the
 * repository is not resolved to an organization until it has been verified, and
 * no credential exists until a binding has said which organization it belongs
 * to.
 */
export async function exchangeWorkflowIdentity(
  deps: ExchangeDeps,
  presented: string,
): Promise<ExchangedToken> {
  // 1. GitHub signed this. Everything below runs only because this returned.
  const keys = await deps.actionsKeys.current(kidOf(presented))
  const identity = verifyWorkflowIdentity(presented, { keys, clock: deps.clock })

  const repository = normalizeRepository(identity.repository)
  if (!repository) {
    throw new ExchangeRefused(
      'no_repository',
      401,
      'The identity token names no repository this control plane can act on.',
    )
  }
  const owner = repository.split('/')[0]!

  // 2. Bounded per repository, now that the repository is something GitHub
  //    said rather than something the caller typed.
  const verdict = deps.limiter.take(`oidc:${repository}`)
  if (!verdict.allowed) {
    throw new ExchangeRefused(
      'rate_limited',
      429,
      `Too many exchanges for ${repository}. Retry in ${verdict.retryAfterSeconds} seconds. ` +
        'A job needs one credential, so this usually means a loop rather than a build.',
      verdict.retryAfterSeconds,
    )
  }

  // 3. Which organization, if any, has claimed it. On a connection scoped to
  //    the account out of the verified token, so the policy in 0023 can only
  //    reach bindings held by organizations with an installation on that
  //    account: a bug here writes nothing into somebody else's tenant.
  const rows = await deps.pool.withGitHubAccount(owner, async (db) => {
    const bindings = await db.execute<{
      id: string
      org_id: string
      revoked_at: Date | string | null
    }>(sql`
      SELECT id, org_id, revoked_at FROM oidc_repository_bindings
      WHERE repository = ${repository}`)
    const live = await db.execute<{ n: string }>(sql`
      SELECT count(*) AS n FROM github_installations
      WHERE lower(account_login) = ${owner} AND suspended_at IS NULL`)
    return { bindings, installed: Number(live[0]?.n ?? 0) > 0 }
  })

  const binding = rows.bindings.find((b) => !b.revoked_at)
  if (!binding) {
    if (rows.bindings.length > 0) {
      // A claim that existed and was withdrawn. Named separately because the
      // fix is a person re-claiming it rather than anything about the workflow.
      throw new ExchangeRefused(
        'binding_revoked',
        403,
        `The claim on ${repository} has been revoked, so it can no longer report to this ` +
          'control plane. An owner or admin of the organization can claim it again.',
      )
    }
    // Both "nobody claimed it" and "the organization that did no longer has the
    // App on this account" land here, because the second one is invisible to
    // this connection by design. The sentence covers both rather than guessing.
    throw new ExchangeRefused(
      'no_binding',
      403,
      `No organization has claimed ${repository}, or the organization that did no longer has ` +
        `the Antifailure GitHub App installed on ${owner}. An owner or admin claims a ` +
        'repository once, and until then a workflow identity for it is refused: a signed ' +
        'token proves which repository a job runs in and not who that repository belongs to.',
    )
  }
  if (!rows.installed) {
    throw new ExchangeRefused(
      'installation_suspended',
      403,
      `The Antifailure GitHub App installation on ${owner} is suspended, so ${repository} ` +
        'cannot report until it is restored.',
    )
  }

  // 4. Mint. One transaction, so a token that exists has stamped its binding
  //    and written its audit entry, and one that failed to do either does not
  //    exist.
  const token = `aft_${randomBytes(32).toString('base64url')}`
  const tokenHash = hashToken(token)
  const prefix = token.slice(0, 12)
  const now = deps.clock.now()
  const expiresAt = new Date(now.getTime() + OIDC_TOKEN_TTL_MS)
  const name = `${repository} run ${identity.runId}`

  await deps.pool.withTenant({ orgId: binding.org_id }, async (db) => {
    await db.execute(sql`
      INSERT INTO engine_tokens
        (org_id, name, token_hash, prefix, kind, expires_at, binding_id, created_at)
      VALUES (${binding.org_id}::uuid, ${name.slice(0, 200)}, ${tokenHash}, ${prefix},
              'oidc', ${expiresAt.toISOString()}, ${binding.id}::uuid, ${now.toISOString()})`)
    await db.execute(sql`
      UPDATE oidc_repository_bindings SET last_used_at = ${now.toISOString()}
      WHERE id = ${binding.id}::uuid`)
    // The prefix and the workflow, never the token. An audit entry carrying the
    // credential would undo the hashing two statements above it.
    await appendAudit(db, {
      orgId: binding.org_id,
      actorLabel: repository,
      action: 'oidc.token_issued',
      targetType: 'engine_token',
      targetId: prefix,
      origin: 'github',
      detail: {
        repository,
        runId: identity.runId,
        runAttempt: identity.runAttempt,
        // Which workflow file, out of the verified token. This is what makes a
        // credential attributable to a workflow rather than to a repository,
        // and it is the field somebody reads when a token they did not expect
        // turns up in the log.
        jobWorkflowRef: identity.jobWorkflowRef,
        expiresAt: expiresAt.toISOString(),
      },
      occurredAt: now,
    })
  })

  return { token, expiresAt, orgId: binding.org_id, repository }
}

// ---------------------------------------------------------------------------
// Claiming a repository
// ---------------------------------------------------------------------------

export interface ClaimInput {
  orgId: string
  repository: string
  actorUserId: string
  actorLabel: string
  origin: 'cli' | 'web'
}

/**
 * Claims a repository for an organization.
 *
 * Two checks, and the second is what makes the claim mean something.
 *
 * The role check lives on the route, on the same `tokens.manage` gate as
 * minting an engine token, because that is precisely the privilege this hands
 * to a workflow: after this, a job in that repository can obtain a credential
 * without anybody approving it again.
 *
 * The installation check is here. The organization must hold a live GitHub App
 * installation on the repository's owner, and those rows are written only by
 * webhook deliveries whose HMAC has been verified, so this is GitHub saying the
 * organization controls that account rather than a person typing a name into a
 * form. Without it, claiming would be a land grab: anybody could claim
 * `some-company/their-app`, and then that company's genuine CI would either be
 * locked out or, worse, resolve to the squatter's tenant.
 */
export async function claimRepository(
  pool: Pool,
  clock: Clock,
  input: ClaimInput,
): Promise<BindingRow> {
  const repository = normalizeRepository(input.repository)
  if (!repository) {
    throw new BindingError(
      'malformed',
      `${JSON.stringify(input.repository)} is not a repository. Give it as owner/name, the way ` +
        'GitHub spells it.',
    )
  }
  const owner = repository.split('/')[0]!
  const now = clock.now()

  return pool.withTenant({ orgId: input.orgId, userId: input.actorUserId }, async (db) => {
    const installed = await db.execute<{ n: string }>(sql`
      SELECT count(*) AS n FROM github_installations
      WHERE lower(account_login) = ${owner} AND suspended_at IS NULL`)
    if (Number(installed[0]?.n ?? 0) === 0) {
      throw new BindingError(
        'not_installed',
        `This organization has no live Antifailure GitHub App installation on ${owner}, so it ` +
          `cannot claim ${repository}. Install the App on ${owner} first: the installation is ` +
          'what proves to this control plane that you control that account, and a claim without ' +
          "it would let anybody take somebody else's repository.",
      )
    }

    // Insert first, and let the index arbitrate. A SELECT followed by an INSERT
    // reads correctly and races: two administrators pressing the same button,
    // or one person double clicking, both find nothing and both insert, and the
    // loser of that race would be told its repository is claimed by somebody
    // else when the claim is its own. ON CONFLICT is what makes the check and
    // the write one decision instead of two.
    //
    // The conflict target names the partial index explicitly, because that is
    // the only unique constraint on this column and a bare `(repository)` would
    // not infer it.
    //
    // A 23505 cannot reach here, which matters: inside a transaction it would
    // abort everything after it, so the recovery below could not run at all.
    const created = await db.execute<{
      id: string
      repository: string
      created_at: Date
      last_used_at: Date | null
      revoked_at: Date | null
    }>(sql`
      INSERT INTO oidc_repository_bindings (org_id, repository, created_by, created_at)
      VALUES (${input.orgId}::uuid, ${repository}, ${input.actorUserId}::uuid,
              ${now.toISOString()})
      ON CONFLICT (repository) WHERE revoked_at IS NULL DO NOTHING
      RETURNING id, repository, created_at, last_used_at, revoked_at`)

    if (!created[0]) {
      // Something already holds the live claim. Whose it is decides the answer,
      // and the tenant policy answers that for free: a row this transaction can
      // see belongs to this organization.
      const mine = await db.execute<{
        id: string
        repository: string
        created_at: Date
        last_used_at: Date | null
        revoked_at: Date | null
      }>(sql`
        SELECT id, repository, created_at, last_used_at, revoked_at
        FROM oidc_repository_bindings
        WHERE repository = ${repository} AND revoked_at IS NULL`)
      // Ours: the same command run twice, or two administrators setting the
      // same repository up. Answered with the claim rather than an error, and
      // no second audit entry, because nothing changed.
      if (mine[0]) return asRow(mine[0])

      // Not ours, and the other organization is not named because the caller
      // has not shown they may know it exists.
      throw new BindingError(
        'already_claimed',
        `${repository} is already claimed by another organization on this control plane. ` +
          'One repository can report to one organization. If that claim is yours, revoke it ' +
          'there first; if it is not, it is somebody else claiming a repository they do not ' +
          'run, which is worth reporting.',
      )
    }

    const row = asRow(created[0])
    await appendAudit(db, {
      orgId: input.orgId,
      actorUserId: input.actorUserId,
      actorLabel: input.actorLabel,
      action: 'oidc_binding.created',
      targetType: 'oidc_repository_binding',
      targetId: row.id,
      origin: input.origin,
      detail: { repository },
      occurredAt: now,
    })
    return row
  })
}

/** Every claim this organization holds, live ones and withdrawn ones, newest
 *  first. Revoked rows are shown rather than hidden for the same reason a
 *  revoked engine token is: somebody reading this list is usually checking that
 *  the one they revoked is the one that stopped working. */
export async function listBindings(pool: Pool, orgId: string): Promise<BindingRow[]> {
  return pool.withTenant({ orgId }, async (db) => {
    const rows = await db.execute<{
      id: string
      repository: string
      created_at: Date
      last_used_at: Date | null
      revoked_at: Date | null
    }>(sql`
      SELECT id, repository, created_at, last_used_at, revoked_at
      FROM oidc_repository_bindings ORDER BY created_at DESC`)
    return rows.map(asRow)
  })
}

export interface RevokeBindingInput {
  orgId: string
  /** The binding's id, or the repository it names. A person reading `af oidc
   *  list` has the repository in front of them and should not have to go and
   *  find a uuid during an incident. */
  idOrRepository: string
  actorUserId: string
  actorLabel: string
  origin: 'cli' | 'web'
}

/**
 * Withdraws a claim, and kills the credentials it produced.
 *
 * The second half is not housekeeping. Revoking a binding that leaves fifteen
 * minutes of live tokens behind is a revocation that does not revoke, and
 * fifteen minutes is exactly the window somebody revoking in a hurry cares
 * about. Both statements are in one transaction, so there is no moment where
 * the claim is gone and the tokens are not.
 */
export async function revokeBinding(
  pool: Pool,
  clock: Clock,
  input: RevokeBindingInput,
): Promise<{ found: boolean; repository: string | null; alreadyRevoked: boolean; tokensRevoked: number }> {
  const needle = input.idOrRepository.trim()
  if (!needle) {
    throw new BindingError('malformed', 'Name the claim to revoke, by its id or its repository.')
  }
  const repository = normalizeRepository(needle)
  const now = clock.now()

  return pool.withTenant({ orgId: input.orgId, userId: input.actorUserId }, async (db) => {
    // The id is a uuid and a repository is not, so the comparison is on the
    // text form of the id. Casting the input to uuid instead would raise on
    // every repository, which is the argument somebody is most likely to pass.
    const found = await db.execute<{ id: string; repository: string; revoked_at: Date | null }>(sql`
      SELECT id, repository, revoked_at FROM oidc_repository_bindings
      WHERE id::text = ${needle} OR repository = ${repository ?? ''}
      ORDER BY revoked_at NULLS FIRST LIMIT 1`)
    const row = found[0]
    if (!row) return { found: false, repository: null, alreadyRevoked: false, tokensRevoked: 0 }
    if (row.revoked_at) {
      return { found: true, repository: row.repository, alreadyRevoked: true, tokensRevoked: 0 }
    }

    await db.execute(sql`
      UPDATE oidc_repository_bindings SET revoked_at = ${now.toISOString()}
      WHERE id = ${row.id}::uuid`)
    const killed = await db.execute<{ id: string }>(sql`
      UPDATE engine_tokens SET revoked_at = ${now.toISOString()}
      WHERE binding_id = ${row.id}::uuid AND revoked_at IS NULL
      RETURNING id`)

    await appendAudit(db, {
      orgId: input.orgId,
      actorUserId: input.actorUserId,
      actorLabel: input.actorLabel,
      action: 'oidc_binding.revoked',
      targetType: 'oidc_repository_binding',
      targetId: row.id,
      origin: input.origin,
      detail: { repository: row.repository, tokensRevoked: killed.length },
      occurredAt: now,
    })
    return {
      found: true,
      repository: row.repository,
      alreadyRevoked: false,
      tokensRevoked: killed.length,
    }
  })
}

function asRow(r: {
  id: string
  repository: string
  created_at: Date | string
  last_used_at: Date | string | null
  revoked_at: Date | string | null
}): BindingRow {
  return {
    id: r.id,
    repository: r.repository,
    createdAt: new Date(r.created_at),
    lastUsedAt: r.last_used_at ? new Date(r.last_used_at) : null,
    revokedAt: r.revoked_at ? new Date(r.revoked_at) : null,
  }
}

// ---------------------------------------------------------------------------
// The routes
// ---------------------------------------------------------------------------

/** What createServer hands over. Written as an interface rather than reaching
 *  into the server's closure so that this file can be read, and tested, on its
 *  own. */
export interface WorkflowIdentityRoutes {
  pool: Pool
  clock: Clock
  actionsKeys: ActionsKeys
  limiter: RateLimiter
  /** Resolves a CLI bearer token to a caller holding `tokens.manage`, or
   *  returns the Response that says why not. Passed in rather than rebuilt
   *  here because the server owns the hosted plan gate that goes with it, and
   *  a second implementation of a permission check is where the two disagree. */
  cliCaller: (
    c: Context,
    need: 'tokens.manage',
  ) => Promise<{ orgId: string; userId: string; label: string } | Response>
}

/**
 * Mounts the exchange and the three routes that manage a claim.
 *
 * One call, so registering this in createServer is a single line and the two
 * halves cannot be added apart: an exchange with no way to create a binding
 * refuses every request, and bindings with no exchange are rows nothing reads.
 */
export function registerWorkflowIdentityRoutes(app: Hono, deps: WorkflowIdentityRoutes): void {
  app.post('/v1/auth/github-oidc', async (c) => {
    let body: { token?: unknown }
    try {
      body = (await c.req.json()) as { token?: unknown }
    } catch {
      return c.json({ error: 'The body is not JSON.', reason: 'malformed_request' }, 400)
    }
    const presented = typeof body.token === 'string' ? body.token.trim() : ''
    if (!presented) {
      return c.json(
        {
          error:
            'Post the workflow identity token as {"token": "..."}. In a workflow with ' +
            `id-token: write, ask GitHub for one with the audience ${CALLBACK_AUDIENCE}.`,
          reason: 'no_token',
        },
        400,
      )
    }

    try {
      const issued = await exchangeWorkflowIdentity(deps, presented)
      return c.json({
        token: issued.token,
        expires_at: issued.expiresAt.toISOString(),
        // Informational, and both are things the caller already told us: the
        // repository came out of its own token and the organization is the one
        // it is now writing to. Returned so a job's log says which tenant it
        // reached, which is the first question when events land nowhere.
        org_id: issued.orgId,
        repository: issued.repository,
      })
    } catch (err) {
      if (err instanceof ExchangeRefused) {
        if (err.retryAfterSeconds !== undefined) {
          c.header('retry-after', String(err.retryAfterSeconds))
        }
        return c.json(
          { error: err.message, reason: err.reason, retryAfterSeconds: err.retryAfterSeconds },
          err.status,
        )
      }
      if (err instanceof TokenRefused) {
        // Safe to return in full: every reason is something the workflow author
        // can act on, and none of them narrows a search for a valid token,
        // because the token is signed by GitHub rather than guessed.
        return c.json({ error: err.message, reason: err.reason }, 401)
      }
      throw err
    }
  })

  // The same gate as minting an engine token, because this grants a workflow
  // the standing ability to mint one.
  app.post('/v1/oidc/bindings', async (c) => {
    const caller = await deps.cliCaller(c, 'tokens.manage')
    if (caller instanceof Response) return caller
    let body: { repository?: unknown }
    try {
      body = (await c.req.json()) as { repository?: unknown }
    } catch {
      return c.json({ error: 'The body is not JSON.' }, 400)
    }
    try {
      const row = await claimRepository(deps.pool, deps.clock, {
        orgId: caller.orgId,
        repository: typeof body.repository === 'string' ? body.repository : '',
        actorUserId: caller.userId,
        actorLabel: caller.label,
        origin: 'cli',
      })
      return c.json({ binding: renderBinding(row) }, 201)
    } catch (err) {
      if (err instanceof BindingError) {
        // 409 for a repository somebody else holds, because nothing about the
        // request is malformed and retrying it unchanged will never work.
        return c.json({ error: err.message, reason: err.reason }, err.reason === 'already_claimed' ? 409 : 400)
      }
      throw err
    }
  })

  app.get('/v1/oidc/bindings', async (c) => {
    const caller = await deps.cliCaller(c, 'tokens.manage')
    if (caller instanceof Response) return caller
    const rows = await listBindings(deps.pool, caller.orgId)
    return c.json({ audience: CALLBACK_AUDIENCE, bindings: rows.map(renderBinding) })
  })

  // Two routes and not one, because a repository is `owner/name` and a slash
  // is a path separator. A single `:binding` matches one segment, so
  // `DELETE /v1/oidc/bindings/acme/app` matched no route at all and was
  // answered by the "this endpoint has no declared rate limit" refusal, which
  // is the loudest possible way to say a revocation is unreachable. Somebody
  // holding the repository name, which is what the list shows and what a person
  // has in front of them during an incident, must not have to go and find a
  // uuid first.
  const revoke = async (c: Context, idOrRepository: string): Promise<Response> => {
    const caller = await deps.cliCaller(c, 'tokens.manage')
    if (caller instanceof Response) return caller
    try {
      const result = await revokeBinding(deps.pool, deps.clock, {
        orgId: caller.orgId,
        idOrRepository,
        actorUserId: caller.userId,
        actorLabel: caller.label,
        origin: 'cli',
      })
      if (!result.found) {
        // The same answer whether it belongs to another organization or does
        // not exist, which is what every other lookup on this server does.
        return c.json({ error: 'No claim here has that id or repository.' }, 404)
      }
      return c.json({
        revoked: true,
        repository: result.repository,
        alreadyRevoked: result.alreadyRevoked,
        // Named, because "revoked" on its own does not tell somebody in an
        // incident whether the credentials already out there are dead.
        tokensRevoked: result.tokensRevoked,
      })
    } catch (err) {
      if (err instanceof BindingError) return c.json({ error: err.message, reason: err.reason }, 400)
      throw err
    }
  }

  app.delete('/v1/oidc/bindings/:binding', async (c) => revoke(c, c.req.param('binding')))
  app.delete('/v1/oidc/bindings/:owner/:name', async (c) =>
    revoke(c, `${c.req.param('owner')}/${c.req.param('name')}`),
  )
}

function renderBinding(row: BindingRow): Record<string, unknown> {
  return {
    id: row.id,
    repository: row.repository,
    createdAt: row.createdAt.toISOString(),
    lastUsedAt: row.lastUsedAt ? row.lastUsedAt.toISOString() : null,
    revokedAt: row.revokedAt ? row.revokedAt.toISOString() : null,
  }
}
