// The operator's audit chain.
//
// WHY THIS IS NOT appendAudit. `audit_entries.org_id` is NOT NULL, its index is
// `(org_id, seq DESC)`, and appendAudit takes an advisory lock keyed on the
// organization and reads the chain head with `WHERE org_id = ...`. A platform
// level action has no organization to key any of that to: an operator signing
// in, an operator being granted a role, an operator searching across every
// tenant. Making the column nullable does not fix it, it forks the chain, since
// NULL never equals current_org() and the per organization head lookup would
// find nothing to chain onto.
//
// So this is a second table with its own chain, and it reuses auditEntryHash
// and canonicalJson from audit.ts UNCHANGED. Two hash functions that are meant
// to agree and are written twice are two hash functions that eventually
// disagree, and the one that would be wrong is whichever a verification tool
// was not written against.
//
// WHAT IS DIFFERENT FROM THE TENANT CHAIN, and why:
//
//   subject_org_id is NULLABLE. An installation wide action writes ONE entry
//   naming no tenant, rather than a fabricated row per affected organization.
//   The alternative was rejected because an event that is not per tenant
//   becomes unreadable when split across tenants, and the count of rows would
//   imply a blast radius that the action did not have.
//
//   severity is a COLUMN, not a key in detail. It is the field an incident
//   review filters on first, and a value buried in jsonb is one that cannot be
//   indexed and, more to the point, one that a caller can silently omit.
//
//   The chain is GLOBAL rather than per tenant. There is exactly one operator
//   history, so there is exactly one order, and one advisory lock.
//
// WHAT IS THE SAME, deliberately: the entry is appended inside the caller's
// transaction, never in one of its own. An audit entry written separately is an
// entry that survives a rolled back action, and a log that records things that
// did not happen is as useless as one that misses things that did. That is why
// this takes the transaction rather than the pool, and it is the property that
// makes "write the record BEFORE the thing it describes" enforceable: put this
// call first in the transaction and the action cannot commit without it.

import { sql } from 'drizzle-orm'
import type { Db } from './client.ts'
import { appendAudit, auditEntryHash } from './audit.ts'

/**
 * How bad it is, chosen by the caller rather than inferred from the action.
 *
 * Inferring it from a string prefix was the obvious alternative and it is
 * wrong: the same verb is routine in one context and an incident in another,
 * and a table of prefixes is a second place to keep in step with the actions.
 */
export type AdminAuditSeverity = 'info' | 'notice' | 'high' | 'critical'

export interface AdminAuditInput {
  /** The operator, when there is one. Null for an attempt that failed before
   *  anybody was identified, which is the line most worth having. */
  adminUserId?: string | null
  /** How to name the actor a year from now, when the row may be gone. */
  actorLabel: string
  /** Verb and object, as `organization.suspended`. */
  action: string
  targetType: string
  targetId?: string | null
  /** The tenant this concerned, when it concerned one. Null for a platform
   *  level action, which is the whole reason this table exists. */
  subjectOrgId?: string | null
  /** The tenant's slug, kept as text for the same reason as actorLabel. */
  subjectOrgLabel?: string | null
  /** Where it came from: `admin`, `system`. */
  origin: string
  ip?: string | null
  severity: AdminAuditSeverity
  detail?: Record<string, unknown>
  occurredAt?: Date
  /**
   * Whether the tenant also gets a copy. Defaults to TRUE whenever there is a
   * subject organization, and the default is the point.
   *
   * THE DOUBLE WRITE RULE. An operator action against a customer's account is
   * recorded twice: once in the operator chain, which the customer cannot see,
   * and once in that customer's own audit log, which they can. A record only
   * the vendor can read is not accountability, it is a vendor's private note.
   * The customer's log is the half that matters to the person whose data was
   * touched, and it is the half a caller would forget, so it is not a second
   * function to remember to call. It happens here, and switching it off is an
   * argument you have to write down at the call site.
   *
   * Set false only when the tenant copy would be noise rather than news: a
   * per request read entry that names no specific row is the case this was
   * added for, and it is the only one that currently passes it.
   */
  tenantCopy?: boolean
}

/**
 * Appends one entry and links it to the one before it.
 *
 * The sequence is taken from the table's own sequence so the value hashed is
 * the value stored, and the head is read under an advisory lock so two replicas
 * appending at the same instant cannot both believe they are the next entry.
 * Both are the reasoning appendAudit gives, and both apply unchanged; what
 * differs is that the lock is global rather than per organization, because
 * there is one operator history rather than one per tenant.
 *
 * The advisory lock is transaction scoped, so it is released on commit or
 * rollback without anything having to remember to. It needs no privilege on the
 * table, which matters here for the same reason it mattered there: UPDATE is
 * the privilege deliberately withheld to make this table append only, so
 * SELECT ... FOR UPDATE on the head is not available.
 */
export async function appendAdminAudit(
  db: Db,
  input: AdminAuditInput,
): Promise<{ seq: number; entryHash: string }> {
  await db.execute(sql`SELECT pg_advisory_xact_lock(hashtext('admin_audit'))`)

  const head = await db.execute<{ seq: number; entry_hash: string }>(sql`
    SELECT seq, entry_hash FROM admin_audit_entries ORDER BY seq DESC LIMIT 1`)

  const prevHash = head[0]?.entry_hash ?? null
  const occurredAt = input.occurredAt ?? new Date()
  const detail = input.detail ?? {}

  const seqRow = await db.execute<{ nextval: string }>(
    sql`SELECT nextval('admin_audit_entries_seq_seq')`,
  )
  const seq = Number(seqRow[0]!.nextval)

  // The hash covers severity and the subject organization by folding them into
  // the fields auditEntryHash already length-prefixes, rather than by writing a
  // second hash function. The point of reusing it is that a verifier written
  // against either chain verifies both.
  //
  // THE SUBJECT IS HASHED BY LABEL, NEVER BY ID, and that is a fix rather than
  // a preference. subject_org_id carries ON DELETE SET NULL, so deleting an
  // organization rewrites that column on every operator entry about it. An
  // earlier version hashed the id, so a deletion silently changed a hashed
  // field and the verifier then reported every one of those entries as
  // `altered`, which is its word for tampered.
  //
  // That is worse than an ordinary bug. The chain's whole job is to tell
  // "somebody edited history" from "nothing happened", and it cried wolf on the
  // single most important record it holds: an operator deleting a customer is
  // exactly the entry a reader comes looking for, and it sat in a wall of false
  // alterations. Found by admin-money, reproduced, then fixed.
  //
  // subject_org_label is text with no foreign key, carried beside the id for
  // precisely this reason, the same way actor_label outlives its user. Never
  // hash a column a foreign key can rewrite.
  const entryHash = auditEntryHash({
    seq,
    orgId: input.subjectOrgLabel ?? '',
    actorUserId: input.adminUserId ?? null,
    actorLabel: input.actorLabel,
    action: input.action,
    targetType: `${input.severity}|${input.targetType}`,
    targetId: input.targetId ?? null,
    origin: input.origin,
    detail,
    occurredAt,
    prevHash,
  })

  await db.execute(sql`
    INSERT INTO admin_audit_entries
      (seq, admin_user_id, actor_label, action, target_type, target_id,
       subject_org_id, subject_org_label, origin, ip, severity, detail,
       occurred_at, prev_hash, entry_hash)
    VALUES
      (${seq}, ${input.adminUserId ?? null}, ${input.actorLabel}, ${input.action},
       ${input.targetType}, ${input.targetId ?? null}, ${input.subjectOrgId ?? null},
       ${input.subjectOrgLabel ?? null}, ${input.origin}, ${input.ip ?? null}::inet,
       ${input.severity}, ${JSON.stringify(detail)}::jsonb, ${occurredAt.toISOString()},
       ${prevHash}, ${entryHash})`)

  // The customer's copy, in the same transaction as the operator's.
  //
  // Same transaction rather than a follow up write, for the reason this whole
  // function takes the transaction: two logs that can disagree about whether
  // something happened are worse than one, and a tenant copy written
  // afterwards is exactly a copy that can be missing while the operator entry
  // says the action succeeded.
  //
  // actorUserId is NULL and not the operator's id. It is not a defensive
  // choice, it is a foreign key: audit_entries.actor_user_id REFERENCES
  // users(id), and an admin_users id is a different id space, so passing it
  // raises. The operator is named in actorLabel, which is text and is the
  // field that survives the row being deleted anyway.
  const wantsTenantCopy = input.tenantCopy ?? true
  if (input.subjectOrgId && wantsTenantCopy) {
    await appendAudit(db, {
      orgId: input.subjectOrgId,
      actorUserId: null,
      actorLabel: input.actorLabel,
      action: input.action,
      targetType: input.targetType,
      targetId: input.targetId ?? null,
      // A distinct origin, so a customer reading their own log can tell an
      // action taken by the vendor from one taken by somebody in their own
      // organization. Without this the two are indistinguishable, which is the
      // single most misleading thing this log could do.
      origin: 'admin',
      detail: { ...detail, operator: input.actorLabel, severity: input.severity },
      occurredAt,
    })
  }

  return { seq, entryHash }
}

export interface AdminChainReport {
  ok: boolean
  entries: number
  head: string | null
  /** The first and last sequence numbers actually walked, or null when the
   *  range held nothing. Reported because a verification of a RANGE is only
   *  meaningful alongside the range it covered, and a report that says `ok`
   *  without saying over what is a claim nobody can check. */
  firstSeq: number | null
  lastSeq: number | null
  problems: { seq: number; kind: 'altered' | 'broken_link'; detail: string }[]
}

/**
 * A window of the chain to verify, inclusive at both ends.
 *
 * WHY A RANGE EXISTS AT ALL. The whole chain is the honest default and stays
 * the default. But an export hands somebody a slice of the history, and a slice
 * shipped with a verification of the WHOLE chain is a verification of something
 * other than the file in their hands. So the same walk covers both, and there
 * is exactly one hash comparison in this package rather than a second one
 * written for exports that would eventually disagree with this one.
 *
 * The subtlety a second implementation would get wrong: the first entry of a
 * range points at a predecessor OUTSIDE the range, so a naive range walk starts
 * with `expectedPrev` of null and reports the range's own first entry as a
 * broken link every single time. This reads that predecessor's hash first, so a
 * range that is intact reports intact.
 */
export interface AdminChainRange {
  fromSeq?: number | null
  toSeq?: number | null
}

/**
 * Walks the operator chain and reports every break.
 *
 * Every break rather than the first, because an investigation wants the extent
 * of the tampering and the first break tells you only where it started. The
 * same reasoning as verifyAuditChain, and the same reason it ships rather than
 * being a vendor tool: tamper evidence only the vendor can check is not
 * evidence.
 */
export async function verifyAdminAuditChain(
  db: Db,
  range: AdminChainRange = {},
): Promise<AdminChainReport> {
  const fromSeq = range.fromSeq ?? null
  const toSeq = range.toSeq ?? null

  // The hash the range's first entry must point at, read from the entry BEFORE
  // it. Null when the range starts at the beginning of the chain, which is the
  // whole-chain case and the one the original walk handled.
  let expectedPrev: string | null = null
  if (fromSeq !== null) {
    const before = await db.execute<{ entry_hash: string }>(sql`
      SELECT entry_hash FROM admin_audit_entries
      WHERE seq < ${fromSeq} ORDER BY seq DESC LIMIT 1`)
    expectedPrev = before[0]?.entry_hash ?? null
  }

  const rows = await db.execute<{
    seq: string
    admin_user_id: string | null
    actor_label: string
    action: string
    target_type: string
    target_id: string | null
    subject_org_id: string | null
    subject_org_label: string | null
    origin: string
    severity: AdminAuditSeverity
    detail: unknown
    occurred_at: Date | string
    prev_hash: string | null
    entry_hash: string
  }>(sql`
    SELECT seq, admin_user_id, actor_label, action, target_type, target_id,
           subject_org_id, subject_org_label, origin, severity, detail,
           occurred_at, prev_hash, entry_hash
    FROM admin_audit_entries
    WHERE (${fromSeq}::bigint IS NULL OR seq >= ${fromSeq})
      AND (${toSeq}::bigint IS NULL OR seq <= ${toSeq})
    ORDER BY seq ASC`)

  const problems: AdminChainReport['problems'] = []
  let head: string | null = null
  let firstSeq: number | null = null
  let lastSeq: number | null = null

  for (const row of rows) {
    const seq = Number(row.seq)
    const occurredAt =
      row.occurred_at instanceof Date ? row.occurred_at : new Date(row.occurred_at)

    const recomputed = auditEntryHash({
      seq,
      // The label, matching what was hashed at write time. Reading
      // subject_org_id here would reintroduce the defect on the verify side
      // only, which is the version that is hardest to notice.
      orgId: row.subject_org_label ?? '',
      actorUserId: row.admin_user_id,
      actorLabel: row.actor_label,
      action: row.action,
      targetType: `${row.severity}|${row.target_type}`,
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
    if (firstSeq === null) firstSeq = seq
    lastSeq = seq
  }

  return { ok: problems.length === 0, entries: rows.length, head, firstSeq, lastSeq, problems }
}
