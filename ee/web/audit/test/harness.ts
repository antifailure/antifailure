// A real control plane database, a real audit chain, and a sink that records.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// Entries are written with the real `appendAudit` and never with an INSERT of
// this file's own. That is the point of the whole exercise: the defect being
// closed is that the rows every entry point already writes reached nothing, so
// a harness that wrote its own rows would be proving something about a shape it
// invented rather than about the shape the product produces. `appendAudit`
// takes the advisory lock, assigns the sequence from the table's own sequence
// and chains each entry to the one before it, and the manifest the sink
// receives is signed over `entry_hash`, so a fixture that skipped any of that
// would sign a chain that does not exist.

import postgres from 'postgres'
import { randomUUID } from 'node:crypto'
import { appendAudit, createPool, migrate, type Pool } from '@antifailure/db'
import type { Batch, Clock, Sink } from '../src/index.ts'
import { PermanentError } from '../src/index.ts'

export const adminUrl =
  process.env.AF_TEST_DATABASE_URL ?? 'postgres://postgres:test@127.0.0.1:55432/antifailure'

export async function available(): Promise<boolean> {
  // Retried, and fatal when a database was named explicitly, which is the rule
  // every harness in this repository follows and the reason is worth repeating
  // here: a skipped suite and a passing suite are the same green tick, and this
  // one is the only proof that an audit entry reaches a sink at all.
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
  if (process.env.AF_TEST_DATABASE_URL || process.env.AF_REQUIRE_DATABASE === '1') {
    throw new Error(
      `AF_TEST_DATABASE_URL is ${adminUrl} and nothing answered after three attempts. ` +
        `Refusing to skip: this suite is the only place an audit entry is watched to reach a ` +
        `sink, and a run that proves nothing is worse than a red one. Underlying error: ` +
        `${last instanceof Error ? last.message : String(last)}`,
    )
  }
  return false
}

/** A clock a test moves, so a licence can be made to lapse without waiting for
 *  one to. */
export class TestClock implements Clock {
  private at: Date
  constructor(at = new Date('2026-09-09T12:00:00.000Z')) {
    this.at = at
  }
  now(): Date {
    return this.at
  }
  advance(ms: number): void {
    this.at = new Date(this.at.getTime() + ms)
  }
}

/**
 * A sink that records what it was given and can be made to fail.
 *
 * `fail` is a function of the batch rather than a flag, so a test can refuse
 * one batch and accept the next without racing a mutable boolean, and so
 * "fails mid batch" is expressible: the queue delivers in order, so refusing
 * the batch that contains a chosen sequence number is exactly a failure part
 * way through.
 */
export class RecordingSink implements Sink {
  readonly batches: Batch[] = []
  fail: ((batch: Batch) => Error | null) | null = null

  name(): string {
    return 'recording'
  }

  async deliver(batch: Batch): Promise<void> {
    const err = this.fail?.(batch) ?? null
    if (err) throw err
    this.batches.push(batch)
  }

  /** Every sequence number this sink has been given, in the order it saw them.
   *  Duplicates are kept, because at least once delivery means a duplicate is
   *  the thing a test has to be able to see. */
  seqs(): number[] {
    return this.batches.flatMap((b) => b.entries.map((e) => e.seq))
  }

  entries() {
    return this.batches.flatMap((b) => b.entries)
  }
}

export { PermanentError }

export interface Harness {
  admin: postgres.Sql
  pool: Pool
  close(): Promise<void>
  /** Writes one entry through the real appendAudit, inside a tenant
   *  transaction, exactly as every production call site does. */
  write(orgId: string, action: string, extra?: { origin?: string; detail?: Record<string, unknown> }): Promise<number>
  /** Puts the cursor where a test needs it. Uses the admin connection, because
   *  the application role is granted UPDATE and no INSERT and can only move the
   *  cursor forward, which is the property under test rather than a tool. */
  setCursor(to: number): Promise<void>
  readCursor(): Promise<number>
  /** The highest sequence number in the table right now.
   *
   *  Every case starts by parking the cursor here, because the enterprise CI
   *  job runs every workspace against one database and the provisioning and
   *  sign on suites leave their own audit entries in it. Without this a case
   *  would read rows it did not write, and its counts would depend on which
   *  suites had run before it. */
  maxSeq(): Promise<number>
  /** Consumes a sequence number without writing a row, which is exactly what a
   *  transaction that rolls back after appendAudit has read nextval leaves
   *  behind. Returns the number that is now missing forever. */
  burnSequence(): Promise<number>
  /** Moves an organization onto another plan, so an entitlement can be
   *  withdrawn or granted under a forwarder that is already running. */
  setPlan(orgId: string, plan: string): Promise<void>
}

export async function start(): Promise<Harness> {
  const admin = postgres(adminUrl, { max: 3, connect_timeout: 30, onnotice: () => {} })
  await migrate(admin)
  await admin.unsafe(`ALTER ROLE antifailure_app LOGIN PASSWORD 'app-test-password'`)

  const url = new URL(adminUrl)
  url.username = 'antifailure_app'
  url.password = 'app-test-password'
  const pool = createPool({
    url: url.toString(),
    max: 4,
    connectTimeoutSeconds: 30,
    statementTimeoutSeconds: 60,
  })

  return {
    admin,
    pool,
    async write(orgId, action, extra = {}) {
      const written = await pool.withTenant({ orgId }, (db) =>
        appendAudit(db, {
          orgId,
          actorLabel: 'harness',
          action,
          targetType: 'organization',
          targetId: orgId,
          origin: extra.origin ?? 'system',
          detail: extra.detail ?? { written: 'by appendAudit' },
        }),
      )
      return written.seq
    },
    async setCursor(to) {
      await admin`UPDATE audit_stream_cursor SET delivered_seq = ${to} WHERE id`
    },
    async burnSequence() {
      const rows = await admin<{ nextval: string }[]>`SELECT nextval('audit_entries_seq_seq')`
      return Number(rows[0]!.nextval)
    },
    async setPlan(orgId, plan) {
      await admin`UPDATE organizations SET plan = ${plan} WHERE id = ${orgId}`
    },
    async maxSeq() {
      const rows = await admin<{ seq: string | null }[]>`
        SELECT coalesce(max(seq), 0) AS seq FROM audit_entries`
      return Number(rows[0]!.seq)
    },
    async readCursor() {
      const rows = await admin<{ delivered_seq: string }[]>`
        SELECT delivered_seq FROM audit_stream_cursor WHERE id`
      return Number(rows[0]!.delivered_seq)
    },
    async close() {
      await pool.close()
      await admin.end({ timeout: 5 })
    },
  }
}

export interface Tenant {
  orgId: string
  slug: string
}

/**
 * One organization on a plan.
 *
 * The plan decides the entitlement, which is the negative control this suite
 * needs both ways: `enterprise` carries `audit_stream` and `free` does not, so
 * an unlicensed organization is a seeded row rather than a mocked answer.
 */
export async function seedTenant(h: Harness, plan = 'enterprise'): Promise<Tenant> {
  const slug = `audit-${randomUUID().slice(0, 8)}`
  const [org] = await h.admin<{ id: string }[]>`
    INSERT INTO organizations (slug, name, plan) VALUES (${slug}, 'Audit', ${plan}) RETURNING id`
  return { orgId: org!.id, slug }
}

export async function dropTenant(h: Harness, orgId: string): Promise<void> {
  await h.admin`DELETE FROM audit_entries WHERE org_id = ${orgId}`
  await h.admin`DELETE FROM organizations WHERE id = ${orgId}`
}
