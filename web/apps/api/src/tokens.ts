// Engine tokens: the credential a CI job or a self-hosted engine presents.
//
// WHY THIS FILE EXISTS. The product documented "create an engine token in the
// control plane" in three places: the self-hosting page, the next step on
// AF-CPL-001, and the next step on AF-CP-002. There was no such action. The
// console has no tokens page, the tRPC router can list and revoke them, and the
// only INSERT into engine_tokens anywhere outside the test fixtures is the
// device flow, which mints a per-user `cli` token that expires in ninety days
// and is never shown to the operator. So an engine could authenticate against a
// row nobody could create: `tokens.list` and `tokens.revoke` managed a resource
// with no producer, and every self-hoster who followed the documentation
// reached a control plane their engine could never talk to.
//
// TWO KINDS, ONE TABLE, AND THE DIFFERENCE MATTERS. A `cli` token is a person:
// it carries a user id, a scope list, and an expiry, and `/v1/whoami` will
// answer for it. An `engine` token is a machine: it belongs to the organization
// rather than to whoever created it, so it keeps working when that person
// leaves, and it deliberately carries no scopes, because the engine endpoints
// authenticate on the token existing rather than on what it may do. Minting one
// therefore has to be gated on the person doing the minting, which is what the
// scope and the role check on the route are for.
//
// SHOWN ONCE. Only the hash is stored, so there is no route that can return a
// token later and no support process that can recover one. Losing it means
// making another and revoking the old one, which is the trade the hashing buys:
// a database that leaks leaks nothing usable.

import { appendAudit, sql, type Pool } from '@antifailure/db'
import type { Clock } from './clock.ts'
import { randomBytes, createHash } from 'node:crypto'

/** Who may mint or revoke an engine token. The same two roles the
 *  `tokens.manage` permission names, so the terminal and the console cannot
 *  disagree about who is allowed to do this. */
export const MAY_MANAGE_TOKENS: ReadonlySet<string> = new Set(['owner', 'admin'])

/** The longest name worth storing. It is a label a person reads in a list, not
 *  a description, and an unbounded one is a row somebody can grow forever. */
const MAX_NAME = 60

export class TokenError extends Error {}

export interface EngineTokenRow {
  id: string
  name: string
  prefix: string
  createdAt: Date
  lastUsedAt: Date | null
  revokedAt: Date | null
}

export interface CreatedEngineToken {
  id: string
  name: string
  prefix: string
  /** The whole token, returned exactly once and stored nowhere. */
  token: string
}

export interface MintInput {
  orgId: string
  name: string
  actorUserId: string
  actorLabel: string
  origin: 'cli' | 'web'
}

/**
 * Mints an engine token and returns it once.
 *
 * The prefix is `aft_`, which is what the documentation has always shown and
 * what the staging fixtures already generate, so an operator comparing what
 * they got against the docs sees the same shape.
 *
 * Deliberately not idempotent on the name. Two tokens called "ci" are a normal
 * thing to want while rotating one, and collapsing them onto a name would mean
 * a rotation silently handed back the credential it was replacing.
 */
export async function mintEngineToken(
  pool: Pool,
  clock: Clock,
  input: MintInput,
): Promise<CreatedEngineToken> {
  const name = input.name.trim()
  if (!name) throw new TokenError('A token needs a name. It is how you tell two of them apart.')
  if (name.length > MAX_NAME) {
    throw new TokenError(`That name is ${name.length} characters; ${MAX_NAME} is the most.`)
  }

  // 32 bytes, the same width the device flow uses for a CLI token. base64url so
  // the whole thing survives a shell, an environment variable and a CI secret
  // field without quoting.
  const token = `aft_${randomBytes(32).toString('base64url')}`
  const tokenHash = createHash('sha256').update(token).digest()
  const prefix = token.slice(0, 12)
  const now = clock.now()

  return pool.withTenant({ orgId: input.orgId, userId: input.actorUserId }, async (db) => {
    const rows = await db.execute<{ id: string }>(sql`
      INSERT INTO engine_tokens (org_id, name, token_hash, prefix, created_by, kind, created_at)
      VALUES (${input.orgId}::uuid, ${name}, ${tokenHash}, ${prefix},
              ${input.actorUserId}::uuid, 'engine', ${now.toISOString()})
      RETURNING id`)
    const id = rows[0]!.id

    // The prefix is the target, never the token. An audit log that recorded the
    // credential would undo the hashing three lines above it.
    await appendAudit(db, {
      orgId: input.orgId,
      actorUserId: input.actorUserId,
      actorLabel: input.actorLabel,
      action: 'token.created',
      targetType: 'engine_token',
      targetId: prefix,
      origin: input.origin,
      detail: { name },
      occurredAt: now,
    })

    return { id, name, prefix, token }
  })
}

/** Every engine token in the organization, newest first, revoked ones included.
 *  A revoked token is shown rather than hidden: somebody reading this list is
 *  usually checking that the one they revoked is the one that stopped working. */
export async function listEngineTokens(pool: Pool, orgId: string): Promise<EngineTokenRow[]> {
  return pool.withTenant({ orgId }, async (db) => {
    const rows = await db.execute<{
      id: string
      name: string
      prefix: string
      created_at: Date
      last_used_at: Date | null
      revoked_at: Date | null
    }>(sql`
      SELECT id, name, prefix, created_at, last_used_at, revoked_at
      FROM engine_tokens WHERE kind = 'engine' ORDER BY created_at DESC`)
    return rows.map((r) => ({
      id: r.id,
      name: r.name,
      prefix: r.prefix,
      createdAt: new Date(r.created_at),
      lastUsedAt: r.last_used_at ? new Date(r.last_used_at) : null,
      revokedAt: r.revoked_at ? new Date(r.revoked_at) : null,
    }))
  })
}

export interface RevokeInput {
  orgId: string
  /** The token's id or its prefix. A prefix is what a person can see, in
   *  `af token list` and in the line the mint printed, so accepting one is the
   *  difference between revoking a leaked credential now and going to find its
   *  uuid first. */
  idOrPrefix: string
  actorUserId: string
  actorLabel: string
  origin: 'cli' | 'web'
}

/** Revokes one engine token. Idempotent: revoking an already revoked token
 *  reports that rather than failing, because during an incident the same
 *  command gets run twice and the second run must not read as a new problem. */
export async function revokeEngineToken(
  pool: Pool,
  clock: Clock,
  input: RevokeInput,
): Promise<{ found: boolean; name: string | null; alreadyRevoked: boolean }> {
  const needle = input.idOrPrefix.trim()
  if (!needle) throw new TokenError('Name the token to revoke, by its id or its prefix.')
  const now = clock.now()

  return pool.withTenant({ orgId: input.orgId, userId: input.actorUserId }, async (db) => {
    // The id is a uuid and the prefix is not, so the comparison is done on the
    // text form of the id. Casting the input to uuid instead would raise on
    // every prefix, which is the argument a person is most likely to pass.
    const found = await db.execute<{ id: string; name: string; revoked_at: Date | null }>(sql`
      SELECT id, name, revoked_at FROM engine_tokens
      WHERE kind = 'engine' AND (id::text = ${needle} OR prefix = ${needle})`)
    const row = found[0]
    if (!row) return { found: false, name: null, alreadyRevoked: false }
    if (row.revoked_at) return { found: true, name: row.name, alreadyRevoked: true }

    await db.execute(sql`
      UPDATE engine_tokens SET revoked_at = ${now.toISOString()} WHERE id = ${row.id}`)
    await appendAudit(db, {
      orgId: input.orgId,
      actorUserId: input.actorUserId,
      actorLabel: input.actorLabel,
      action: 'token.revoked',
      targetType: 'engine_token',
      targetId: row.id,
      origin: input.origin,
      detail: { name: row.name },
      occurredAt: now,
    })
    return { found: true, name: row.name, alreadyRevoked: false }
  })
}
