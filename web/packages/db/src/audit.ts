// The audit log, and the chain that makes an alteration visible.
//
// Two mechanisms, guarding two different attackers.
//
// The database grants stop the application. It has INSERT and SELECT on this
// table and nothing else, so a bug, an injection, or a compromised request
// handler cannot rewrite history no matter what statement it manages to run.
// That covers the realistic case.
//
// The hash chain stops somebody with a privileged connection. Each entry
// carries the hash of the entry before it, so changing an old entry changes its
// hash, and every entry after it now points at a hash that no longer exists.
// Deleting an entry leaves the same break. It does not make tampering
// impossible, because an attacker who can write the table can recompute the
// whole chain; it makes tampering something that has to be done to every entry
// since, which is both far harder to do quietly and exactly what a periodic
// signed export of the head hash detects.
//
// The verification function ships in the community edition on purpose. A
// tamper-evidence scheme that only the vendor can check is not evidence.

import { createHash } from 'node:crypto'
import { sql } from 'drizzle-orm'
import type { Db } from './client.ts'

export interface AuditInput {
  orgId: string
  /** The user who did it, when a user did it. */
  actorUserId?: string | null
  /** How to name the actor a year from now, when the user row may be gone. */
  actorLabel: string
  /** Verb and object, as `environment.torn_down`. */
  action: string
  targetType: string
  targetId?: string | null
  /** Where the action came from: `web`, `api`, `github`, `engine`, `system`. */
  origin: string
  detail?: Record<string, unknown>
  occurredAt?: Date
}

/**
 * The canonical form an entry is hashed in.
 *
 * Field order is fixed here and never taken from object iteration order, and
 * every field is length-prefixed. Without the prefixes an actor named `a` doing
 * `b.c` would hash identically to an actor named `ab` doing `.c`, and an
 * attacker who chooses one field can then choose the hash.
 */
export function auditEntryHash(entry: {
  seq: number
  orgId: string
  actorUserId?: string | null
  actorLabel: string
  action: string
  targetType: string
  targetId?: string | null
  origin: string
  detail: unknown
  occurredAt: Date
  prevHash: string | null
}): string {
  const parts = [
    String(entry.seq),
    entry.orgId,
    entry.actorUserId ?? '',
    entry.actorLabel,
    entry.action,
    entry.targetType,
    entry.targetId ?? '',
    entry.origin,
    canonicalJson(entry.detail),
    entry.occurredAt.toISOString(),
    entry.prevHash ?? '',
  ]
  const h = createHash('sha256')
  for (const part of parts) {
    h.update(String(Buffer.byteLength(part, 'utf8')))
    h.update(':')
    h.update(part, 'utf8')
  }
  return h.digest('hex')
}

/**
 * JSON with object keys sorted, so that two encoders that disagree about key
 * order do not disagree about the hash.
 */
export function canonicalJson(value: unknown): string {
  if (value === null || value === undefined) return 'null'
  if (Array.isArray(value)) return `[${value.map(canonicalJson).join(',')}]`
  if (typeof value === 'object') {
    const entries = Object.entries(value as Record<string, unknown>)
      .filter(([, v]) => v !== undefined)
      .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
    return `{${entries.map(([k, v]) => `${JSON.stringify(k)}:${canonicalJson(v)}`).join(',')}}`
  }
  return JSON.stringify(value)
}

/**
 * Appends one entry and links it to the one before it.
 *
 * The sequence number is assigned by the database rather than by the caller,
 * because two replicas appending at the same instant must not both believe they
 * are the next entry. Taking the previous head inside the same transaction, and
 * locking the tail row while doing it, is what makes the chain a chain rather
 * than a set of entries that each guessed at a predecessor.
 */
export async function appendAudit(db: Db, input: AuditInput): Promise<{ seq: number; entryHash: string }> {
  // Appends within one organization have to be serialised, or two
  // transactions read the same head, both write an entry claiming it as their
  // predecessor, and the chain is forked forever after.
  //
  // The obvious way to do that is SELECT ... FOR UPDATE on the current head,
  // and it is wrong twice. It needs the UPDATE privilege, which is the exact
  // privilege withheld to make this table append-only. And locking the result
  // of a query that returns no rows locks nothing, so the first two appends to
  // a new organization would race past each other with no head to contend for.
  //
  // An advisory lock has neither problem: it needs no privilege on the table,
  // it exists whether or not any row does, and the transaction-scoped form is
  // released on commit or rollback without anything having to remember to.
  // Keyed per organization so that a busy tenant does not serialise a quiet one.
  await db.execute(sql`SELECT pg_advisory_xact_lock(hashtext(${'audit:' + input.orgId}))`)

  const head = await db.execute<{ seq: number; entry_hash: string }>(sql`
    SELECT seq, entry_hash FROM audit_entries
    WHERE org_id = ${input.orgId}
    ORDER BY seq DESC LIMIT 1`)

  const prevHash = head[0]?.entry_hash ?? null
  const occurredAt = input.occurredAt ?? new Date()
  const detail = input.detail ?? {}

  // The sequence is taken from the table's own sequence so the value hashed is
  // the value stored. Reading nextval here rather than letting the default fire
  // is what lets the hash include seq without a second round trip to read it
  // back.
  const seqRow = await db.execute<{ nextval: string }>(sql`SELECT nextval('audit_entries_seq_seq')`)
  const seq = Number(seqRow[0]!.nextval)

  const entryHash = auditEntryHash({
    seq,
    orgId: input.orgId,
    actorUserId: input.actorUserId ?? null,
    actorLabel: input.actorLabel,
    action: input.action,
    targetType: input.targetType,
    targetId: input.targetId ?? null,
    origin: input.origin,
    detail,
    occurredAt,
    prevHash,
  })

  await db.execute(sql`
    INSERT INTO audit_entries
      (seq, org_id, actor_user_id, actor_label, action, target_type, target_id,
       origin, detail, occurred_at, prev_hash, entry_hash)
    VALUES
      (${seq}, ${input.orgId}, ${input.actorUserId ?? null}, ${input.actorLabel},
       ${input.action}, ${input.targetType}, ${input.targetId ?? null},
       ${input.origin}, ${JSON.stringify(detail)}::jsonb, ${occurredAt.toISOString()},
       ${prevHash}, ${entryHash})`)

  return { seq, entryHash }
}

export interface ChainReport {
  ok: boolean
  entries: number
  /** The head hash, for a signed export that lets a later check detect a
   *  rewrite of everything before it. */
  head: string | null
  problems: ChainProblem[]
}

export interface ChainProblem {
  seq: number
  kind: 'altered' | 'broken_link' | 'missing'
  detail: string
}

/**
 * Walks an organization's chain and reports every break.
 *
 * It reports all of them rather than stopping at the first, because an
 * investigation wants the extent of the tampering and the first break tells you
 * only where it started.
 */
export async function verifyAuditChain(db: Db, orgId: string): Promise<ChainReport> {
  const rows = await db.execute<{
    seq: string
    org_id: string
    actor_user_id: string | null
    actor_label: string
    action: string
    target_type: string
    target_id: string | null
    origin: string
    detail: unknown
    occurred_at: Date | string
    prev_hash: string | null
    entry_hash: string
  }>(sql`
    SELECT seq, org_id, actor_user_id, actor_label, action, target_type,
           target_id, origin, detail, occurred_at, prev_hash, entry_hash
    FROM audit_entries WHERE org_id = ${orgId} ORDER BY seq ASC`)

  const problems: ChainProblem[] = []
  let expectedPrev: string | null = null
  let head: string | null = null

  for (const row of rows) {
    const seq = Number(row.seq)
    const occurredAt = row.occurred_at instanceof Date ? row.occurred_at : new Date(row.occurred_at)

    const recomputed = auditEntryHash({
      seq,
      orgId: row.org_id,
      actorUserId: row.actor_user_id,
      actorLabel: row.actor_label,
      action: row.action,
      targetType: row.target_type,
      targetId: row.target_id,
      origin: row.origin,
      detail: row.detail,
      occurredAt,
      prevHash: row.prev_hash,
    })

    if (recomputed !== row.entry_hash) {
      problems.push({
        seq,
        kind: 'altered',
        detail: `entry ${seq} does not hash to its recorded hash, so one of its fields was changed after it was written`,
      })
    }
    if (row.prev_hash !== expectedPrev) {
      problems.push({
        seq,
        kind: 'broken_link',
        detail:
          expectedPrev === null
            ? `entry ${seq} is the first entry but points at a predecessor, so earlier entries were removed`
            : `entry ${seq} points at ${row.prev_hash ?? 'nothing'} but the entry before it hashes to ${expectedPrev}, so an entry between them was removed or rewritten`,
      })
    }

    expectedPrev = row.entry_hash
    head = row.entry_hash
  }

  return { ok: problems.length === 0, entries: rows.length, head, problems }
}
