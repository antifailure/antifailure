// Storing, using, rotating and budgeting a customer's provider key.
//
// One rule runs through the whole file and is worth stating before the code:
// THE PLAINTEXT LEAVES THIS MODULE IN EXACTLY ONE FUNCTION, `borrowKey`, and
// that function returns it to a caller that is about to put it in an
// Authorization header. Nothing else returns it, nothing here logs it, and the
// row is fetched with an explicit column list every time so that a `SELECT *`
// somewhere cannot start carrying it into a response by accident.
//
// The three columns beside the ciphertext -- provider, last four, fingerprint --
// are everything a screen needs, which is what makes that rule keepable rather
// than aspirational. A console page has no reason to ask for the secret.

import { sql } from 'drizzle-orm'
import type { Db, Pool } from '@antifailure/db'
import { appendAudit } from '@antifailure/db'
import type { Clock } from '../clock.ts'
import {
  checkKeyShape,
  fingerprintOf,
  KEY_VERSION,
  open,
  seal,
  type Provider,
} from './seal.ts'

export class ProviderKeyError extends Error {}

export interface StoredKey {
  id: string
  provider: Provider
  last4: string
  fingerprint: string
  createdAt: Date
  rotatedAt: Date | null
  createdBy: string | null
}

export interface Budget {
  provider: Provider
  period: string
  capUsd: number
  spentUsd: number
  remainingUsd: number
}

/** The month a budget row is keyed by. Stored rather than computed at read
 *  time, so a month rolling over is a row that does not match rather than an
 *  expression three places have to agree about. */
export function periodOf(now: Date): string {
  return `${now.getUTCFullYear()}-${String(now.getUTCMonth() + 1).padStart(2, '0')}-01`
}

// ---------------------------------------------------------------------------
// Reading
// ---------------------------------------------------------------------------

/** What the console shows. Never selects the ciphertext. */
export async function listKeys(pool: Pool, orgId: string): Promise<StoredKey[]> {
  return pool.withTenant({ orgId }, async (db) => {
    const rows = await db.execute<{
      id: string
      provider: Provider
      last4: string
      fingerprint: string
      created_at: Date | string
      rotated_at: Date | string | null
      created_by: string | null
    }>(sql`
      SELECT id, provider, last4, fingerprint, created_at, rotated_at, created_by
      FROM provider_keys WHERE revoked_at IS NULL ORDER BY provider`)
    return rows.map((r) => ({
      id: r.id,
      provider: r.provider,
      last4: r.last4,
      fingerprint: r.fingerprint,
      createdAt: asDate(r.created_at),
      rotatedAt: r.rotated_at ? asDate(r.rotated_at) : null,
      createdBy: r.created_by,
    }))
  })
}

// ---------------------------------------------------------------------------
// Writing
// ---------------------------------------------------------------------------

export interface SaveInput {
  orgId: string
  provider: Provider
  key: string
  actorUserId: string | null
  actorLabel: string
}

/**
 * Stores a key, replacing whatever was there.
 *
 * Replacing rather than adding, because `provider_keys_one_live` allows one
 * live key per provider per organization: two live keys is ambiguity about
 * which one a run charged, and that ambiguity is a billing dispute with
 * somebody else's money.
 *
 * The previous key is REVOKED rather than deleted, so the audit trail still
 * shows which fingerprint was in use when. Revoked rows carry ciphertext that
 * nothing will ever open again; they are kept for the record and swept
 * elsewhere.
 */
export async function saveKey(
  pool: Pool,
  clock: Clock,
  sealingKey: Buffer,
  input: SaveInput,
): Promise<{ stored: StoredKey; replaced: boolean; sameAsBefore: boolean }> {
  const key = input.key.trim()
  const complaint = checkKeyShape(input.provider, key)
  if (complaint) throw new ProviderKeyError(complaint)

  const sealed = seal(sealingKey, key, { orgId: input.orgId, provider: input.provider })

  return pool.withTenant({ orgId: input.orgId, userId: input.actorUserId ?? undefined }, async (db) => {
    const existing = await db.execute<{ id: string; fingerprint: string }>(sql`
      SELECT id, fingerprint FROM provider_keys
      WHERE provider = ${input.provider} AND revoked_at IS NULL`)
    const previous = existing[0] ?? null

    // Told plainly rather than silently accepted. During a rotation, pasting
    // the key that is already there is the mistake somebody makes at the exact
    // moment they believe they have replaced it.
    const sameAsBefore = previous?.fingerprint === sealed.fingerprint

    if (previous) {
      await db.execute(sql`
        UPDATE provider_keys SET revoked_at = ${clock.now().toISOString()}
        WHERE id = ${previous.id}::uuid`)
    }

    const inserted = await db.execute<{ id: string; created_at: Date | string }>(sql`
      INSERT INTO provider_keys
        (org_id, provider, ciphertext, nonce, key_version, fingerprint, last4, created_by,
         created_at, rotated_at)
      VALUES (${input.orgId}::uuid, ${input.provider}, ${sealed.ciphertext}, ${sealed.nonce},
              ${KEY_VERSION}, ${sealed.fingerprint}, ${sealed.last4},
              ${input.actorUserId}::uuid, ${clock.now().toISOString()},
              ${previous ? clock.now().toISOString() : null})
      RETURNING id, created_at`)

    await appendAudit(db, {
      orgId: input.orgId,
      actorLabel: input.actorLabel,
      action: previous ? 'provider_key.rotated' : 'provider_key.stored',
      targetType: 'provider_key',
      targetId: input.provider,
      origin: 'web',
      // The fingerprint and the last four, never the key. This record exists to
      // prove WHICH key was in use, and it is written to a table an operator
      // reads, so it must be safe to read over somebody's shoulder.
      detail: {
        provider: input.provider,
        last4: sealed.last4,
        fingerprint: sealed.fingerprint,
        replacedFingerprint: previous?.fingerprint ?? null,
      },
      occurredAt: clock.now(),
    })

    return {
      stored: {
        id: inserted[0]!.id,
        provider: input.provider,
        last4: sealed.last4,
        fingerprint: sealed.fingerprint,
        createdAt: asDate(inserted[0]!.created_at),
        rotatedAt: previous ? clock.now() : null,
        createdBy: input.actorUserId,
      },
      replaced: previous !== null,
      sameAsBefore,
    }
  })
}

/** Removes a key. The old one stops working here immediately. */
export async function revokeKey(
  pool: Pool,
  clock: Clock,
  input: { orgId: string; provider: Provider; actorLabel: string; actorUserId: string | null },
): Promise<{ revoked: boolean }> {
  return pool.withTenant({ orgId: input.orgId, userId: input.actorUserId ?? undefined }, async (db) => {
    const rows = await db.execute<{ fingerprint: string; last4: string }>(sql`
      UPDATE provider_keys SET revoked_at = ${clock.now().toISOString()}
      WHERE provider = ${input.provider} AND revoked_at IS NULL
      RETURNING fingerprint, last4`)
    if (rows.length === 0) return { revoked: false }
    await appendAudit(db, {
      orgId: input.orgId,
      actorLabel: input.actorLabel,
      action: 'provider_key.revoked',
      targetType: 'provider_key',
      targetId: input.provider,
      origin: 'web',
      detail: { provider: input.provider, last4: rows[0]!.last4, fingerprint: rows[0]!.fingerprint },
      occurredAt: clock.now(),
    })
    return { revoked: true }
  })
}

// ---------------------------------------------------------------------------
// Using
// ---------------------------------------------------------------------------

export interface Borrowed {
  key: string
  fingerprint: string
  budget: Budget
}

/**
 * The one function that returns a plaintext key.
 *
 * It checks the budget FIRST and refuses before decrypting, so a run that has
 * no allowance never causes the key to exist in this process's memory at all.
 * That ordering is the difference between a spend cap and a spend suggestion.
 *
 * A provider with no budget row cannot spend. Not "unlimited": a key that
 * belongs to somebody else, with no cap anybody set, is the shape of an
 * unbounded bill, and the safe reading of a missing row is zero rather than
 * infinity.
 */
export async function borrowKey(
  pool: Pool,
  clock: Clock,
  sealingKey: Buffer,
  input: { orgId: string; provider: Provider; estimatedUsd?: number },
): Promise<Borrowed> {
  const period = periodOf(clock.now())
  const estimate = input.estimatedUsd ?? 0

  return pool.withTenant({ orgId: input.orgId }, async (db) => {
    const budget = await readBudget(db, input.orgId, input.provider, period)
    if (!budget) {
      throw new ProviderKeyError(
        `No budget is set for ${input.provider}, so nothing may be spent on it. ` +
          `Set a monthly cap in the console under Provider keys.`,
      )
    }
    if (budget.spentUsd >= budget.capUsd) {
      throw new ProviderKeyError(
        `The ${input.provider} budget for ${period.slice(0, 7)} is spent: ` +
          `${budget.spentUsd.toFixed(2)} of ${budget.capUsd.toFixed(2)} USD. ` +
          `Raise the cap or wait for the next month.`,
      )
    }
    if (estimate > 0 && budget.spentUsd + estimate > budget.capUsd) {
      throw new ProviderKeyError(
        `This would take ${input.provider} past its cap: ` +
          `${budget.spentUsd.toFixed(2)} spent plus ${estimate.toFixed(2)} estimated ` +
          `against a cap of ${budget.capUsd.toFixed(2)} USD.`,
      )
    }

    const rows = await db.execute<{
      ciphertext: Buffer
      nonce: Buffer
      fingerprint: string
    }>(sql`
      SELECT ciphertext, nonce, fingerprint FROM provider_keys
      WHERE provider = ${input.provider} AND revoked_at IS NULL`)
    const row = rows[0]
    if (!row) {
      throw new ProviderKeyError(
        `No ${input.provider} key is stored for this organization. Add one in the console, ` +
          `or with: af provider set ${input.provider}`,
      )
    }

    const key = open(sealingKey, { ciphertext: row.ciphertext, nonce: row.nonce }, {
      orgId: input.orgId,
      provider: input.provider,
    })
    return { key, fingerprint: row.fingerprint, budget }
  })
}

/** Adds to what has been spent. Called after a request, with the real cost. */
export async function recordSpend(
  pool: Pool,
  clock: Clock,
  input: { orgId: string; provider: Provider; usd: number },
): Promise<Budget | null> {
  if (!(input.usd > 0)) return null
  const period = periodOf(clock.now())
  return pool.withTenant({ orgId: input.orgId }, async (db) => {
    // Added in SQL rather than read-modify-written, so two runs finishing at
    // once cannot lose one another's spend.
    await db.execute(sql`
      UPDATE provider_budgets
      SET spent_usd = spent_usd + ${input.usd}, updated_at = ${clock.now().toISOString()}
      WHERE provider = ${input.provider} AND period = ${period}::date`)
    return readBudget(db, input.orgId, input.provider, period)
  })
}

// ---------------------------------------------------------------------------
// Budgets
// ---------------------------------------------------------------------------

export async function setBudget(
  pool: Pool,
  clock: Clock,
  input: {
    orgId: string
    provider: Provider
    capUsd: number
    actorLabel: string
    actorUserId: string | null
  },
): Promise<Budget> {
  if (!(input.capUsd >= 0)) throw new ProviderKeyError('A cap cannot be negative.')
  const period = periodOf(clock.now())

  return pool.withTenant({ orgId: input.orgId, userId: input.actorUserId ?? undefined }, async (db) => {
    await db.execute(sql`
      INSERT INTO provider_budgets (org_id, provider, period, cap_usd, updated_at)
      VALUES (${input.orgId}::uuid, ${input.provider}, ${period}::date, ${input.capUsd},
              ${clock.now().toISOString()})
      ON CONFLICT (org_id, provider, period)
      DO UPDATE SET cap_usd = ${input.capUsd}, updated_at = ${clock.now().toISOString()}`)

    await appendAudit(db, {
      orgId: input.orgId,
      actorLabel: input.actorLabel,
      action: 'provider_budget.set',
      targetType: 'provider_budget',
      targetId: `${input.provider}:${period}`,
      origin: 'web',
      detail: { provider: input.provider, capUsd: input.capUsd, period },
      occurredAt: clock.now(),
    })

    const budget = await readBudget(db, input.orgId, input.provider, period)
    return budget!
  })
}

export async function listBudgets(pool: Pool, clock: Clock, orgId: string): Promise<Budget[]> {
  const period = periodOf(clock.now())
  return pool.withTenant({ orgId }, async (db) => {
    const rows = await db.execute<{
      provider: Provider
      period: Date | string
      cap_usd: string
      spent_usd: string
    }>(sql`
      SELECT provider, period, cap_usd, spent_usd FROM provider_budgets
      WHERE period = ${period}::date ORDER BY provider`)
    return rows.map(toBudget)
  })
}

async function readBudget(
  db: Db,
  _orgId: string,
  provider: Provider,
  period: string,
): Promise<Budget | null> {
  const rows = await db.execute<{
    provider: Provider
    period: Date | string
    cap_usd: string
    spent_usd: string
  }>(sql`
    SELECT provider, period, cap_usd, spent_usd FROM provider_budgets
    WHERE provider = ${provider} AND period = ${period}::date`)
  const row = rows[0]
  return row ? toBudget(row) : null
}

function toBudget(row: {
  provider: Provider
  period: Date | string
  cap_usd: string
  spent_usd: string
}): Budget {
  const cap = Number(row.cap_usd)
  const spent = Number(row.spent_usd)
  return {
    provider: row.provider,
    period: typeof row.period === 'string' ? row.period.slice(0, 10) : row.period.toISOString().slice(0, 10),
    capUsd: cap,
    spentUsd: spent,
    // Never below zero. A negative remaining reads as a refund on a screen and
    // is really just an overshoot from the last request before the cap bit.
    remainingUsd: Math.max(0, cap - spent),
  }
}

function asDate(v: Date | string): Date {
  return v instanceof Date ? v : new Date(v)
}

export { fingerprintOf }
