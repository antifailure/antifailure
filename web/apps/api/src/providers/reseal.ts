// Re-sealing every stored credential under a new sealing key.
//
// WHAT THIS IS FOR. Replacing AF_PROVIDER_KEY_SECRET used to be a one way door:
// every stored provider key stopped opening, permanently and silently, because a
// value that will not decrypt looks exactly like a tampered one. Rows carried a
// key version and nothing read it. The rotation the column anticipated could not
// be performed, so the honest answer to "how do you rotate the key that seals
// our credentials" was that you do not.
//
// A rotation is three steps and this file is the middle one:
//
//   1. Add the new key alongside the old. Both are open at once, old rows still
//      open under the old one, new rows are sealed under the new one.
//   2. Run this. Every row still filed under an older version is opened with the
//      key it was sealed under and written back under the new one.
//   3. Remove the old key. This is also the proof that step 2 finished, because
//      a row that still needed it now says so by name.
//
// FIVE PROPERTIES, and each one is a decision rather than an accident.
//
// IT NEVER HOLDS THE TABLE IN MEMORY. Rows come in batches ordered by id, with a
// cursor, so the memory this uses does not depend on how many customers there
// are. The cursor is not redundant with the "not already at the target version"
// predicate: a row that cannot be opened stays matching that predicate forever,
// and without a cursor the same batch would come back and the tool would never
// finish.
//
// IT IS IDEMPOTENT. Rows already at the target version are not selected, so a
// second run does nothing and says so. That is also what makes it RESUMABLE:
// interrupt it at any point and run it again, and it continues from wherever it
// got to, because the work remaining is a property of the table rather than of a
// checkpoint file this would have to keep.
//
// IT IS SAFE WHILE THE APPLICATION IS SERVING. Each row is its own transaction,
// and the UPDATE carries the ciphertext and version it expects to be replacing.
// So two of these running at once, or one of them racing a customer who saves a
// new key from the console in the same second, ends with one write landing and
// the other reporting that the row changed underneath it. Nothing is lost and
// nothing is written twice.
//
// A FAILURE PART WAY THROUGH LEAVES A ROW UNCHANGED RATHER THAN CORRUPTED. The
// new value is opened again, under the new key and the new associated data,
// before the UPDATE is composed: see `resealValue` in seal.ts. A row is either
// replaced by a value already proven to open or not touched at all.
//
// IT PRINTS NO KEY MATERIAL. Not a sealing key, not a plaintext credential, not
// a ciphertext. What it prints is row ids, table names, version labels and
// counts. A plaintext exists inside one function call per row and is never
// returned, logged or carried into a report.

import { createPool, sql, type Db, type Pool } from '@antifailure/db'
import {
  CannotOpenError,
  fingerprintOf,
  type Keyring,
  MissingSealingKeyError,
  open,
  resealValue,
  SealError,
} from './seal.ts'

/** An argument or a configuration an operator can fix, as opposed to a failure
 *  of the work. The CLI exits 2 on these and 1 on anything else. */
export class ResealRefused extends Error {}

/**
 * A table carrying values sealed by seal.ts, and the columns the associated data
 * is built from.
 *
 * A list rather than one hard coded table, because the sealing module is not
 * specific to provider keys and the second caller is already being written: an
 * enterprise audit sink stores a customer's own credential the same way. A
 * rotation that re-sealed one table and silently left the other is the failure
 * this shape exists to prevent, and adding a row here is the whole of what a new
 * sealed column costs.
 *
 * Presence is CHECKED rather than assumed, so a descriptor naming a table a
 * database has not migrated yet is reported as absent instead of failing the
 * run. A tool that refuses to start because one of several tables is missing
 * cannot be the thing an operator reaches for mid-rotation.
 */
export interface SealedTable {
  table: string
  /** The organization column. First component of the associated data. */
  orgColumn: string
  /** The column holding the second component of the associated data. For
   *  provider keys that is the provider name. */
  boundColumn: string
  /** What one row is, for a report line an operator reads. */
  what: string
}

export const SEALED_TABLES: readonly SealedTable[] = [
  {
    table: 'provider_keys',
    orgColumn: 'org_id',
    boundColumn: 'provider',
    what: "a customer's provider key",
  },
]

/** Why one row was not re-sealed. Three reasons, and they need different
 *  responses from whoever reads the report. */
export type ProblemKind =
  /** The row names a sealing key version this process does not hold. Add the
   *  key. Nothing is wrong with the row. */
  | 'missing key'
  /** The row will not authenticate under the key held for its version. Either
   *  the key filed under that version is the wrong bytes, or the row has been
   *  altered, or it was moved between organizations. */
  | 'cannot open'
  /** It opened, and what came out is not what the fingerprint beside it says.
   *  Never expected, and refused rather than written. */
  | 'invariant'

export interface RowProblem {
  table: string
  id: string
  keyVersion: string
  kind: ProblemKind
  /** The message, which carries no key material. */
  detail: string
}

export interface TableReport {
  table: string
  /** False when the database has no such table, which is a fact rather than a
   *  failure: it is reported and counted as nothing. */
  present: boolean
  /** How many rows sit at each version, counted before anything was written.
   *  This is the number the runbook checks to decide a rotation is complete. */
  versionsBefore: Record<string, number>
  /** Rows read and opened. */
  scanned: number
  /** Rows written, or that would have been written on a dry run. */
  resealed: number
  /** Rows another writer changed between the read and the write. Not an error:
   *  the other writer's value is newer and it is already sealed under whatever
   *  version that writer was using. */
  changedUnderUs: number
  problems: RowProblem[]
}

export type ResealMode =
  /** Open and rewrite every row not already at the target version. */
  | 'apply'
  /** The same scan and the same opening, writing nothing. */
  | 'dry run'
  /**
   * Open EVERY row, whatever version it is at, and write nothing.
   *
   * A dry run says what is left to do. This says whether what has already been
   * done actually works, which is a different question and the one that has to
   * be answered before an old key is thrown away. Without it, "0 rows to
   * re-seal" is indistinguishable from "every row is at the new version and
   * none of them open".
   */
  | 'check'

export interface ResealOptions {
  /** A connection string row level security does not apply to. The application
   *  role cannot read across tenants, and a tool that read one tenant's rows
   *  and reported success would be the worst possible outcome here. */
  adminUrl: string
  /** Every key this process holds. The old ones open rows; the target seals
   *  them. Read from the environment by the CLI, never from an argument. */
  keyring: Keyring
  /** The version to re-seal to. Defaults to the keyring's current version. */
  to?: string
  mode?: ResealMode
  /** Rows per batch. Bounded memory is the point; this only tunes round trips. */
  batchSize?: number
  /**
   * Whether revoked rows are re-sealed too. True by default, and the default is
   * the interesting half.
   *
   * A revoked row carries ciphertext nothing will ever open again, so leaving it
   * behind breaks nothing today. What it breaks is the verification: after the
   * old key is gone, `check` reports every one of those rows as naming a key
   * nobody holds, and an operator cannot tell that finding apart from a live row
   * they missed. A rotation that ends with a clean report is worth re-sealing
   * rows nobody reads.
   */
  includeRevoked?: boolean
  log?: (line: string) => void
  /**
   * Called after the new value is computed and before it is written.
   *
   * A TEST SEAM, and it is here rather than absent because the guarded UPDATE
   * below is the whole of this tool's safety beside a serving application, and
   * an ordering that cannot be forced cannot be proven. Every other caller
   * leaves it unset. What it makes provable is the one cell of the ordering
   * table that timing alone will not produce on demand: another writer changing
   * the row between this tool's read and its write.
   */
  onBeforeWrite?: (row: { table: string; id: string }) => Promise<void>
}

export interface ResealReport {
  mode: ResealMode
  to: string
  /** True only when rows were actually written. */
  applied: boolean
  tables: TableReport[]
  scanned: number
  resealed: number
  changedUnderUs: number
  problems: RowProblem[]
  /** Rows still not at the target version once this finished. Zero is what
   *  makes it safe to remove the old key. */
  remaining: number
  seconds: number
}

interface SealedRow extends Record<string, unknown> {
  id: string
  org: string
  bound: string
  ciphertext: Buffer
  nonce: Buffer
  key_version: string
  fingerprint: string
}

/**
 * Re-seals, or reports what re-sealing would do, or checks that every row opens.
 *
 * Returns a report rather than throwing on the first row it cannot open. An
 * operator mid-rotation needs the whole list: one unopenable row and four
 * hundred is the difference between a bad row and a missing key, and a tool that
 * stops at the first one cannot tell them which they have. The CLI turns a
 * non-empty problem list into a non-zero exit.
 */
export async function reseal(options: ResealOptions): Promise<ResealReport> {
  const mode = options.mode ?? 'apply'
  const to = options.to ?? options.keyring.current
  if (!options.keyring.has(to)) {
    throw new ResealRefused(
      `there is no sealing key for version ${JSON.stringify(to)} in this process, so nothing ` +
        `can be re-sealed to it. Configured versions: ${options.keyring.versions().join(', ')}.`,
    )
  }
  const batchSize = options.batchSize ?? 100
  if (!(Number.isInteger(batchSize) && batchSize > 0)) {
    throw new ResealRefused(`the batch size must be a positive whole number, not ${batchSize}`)
  }
  const includeRevoked = options.includeRevoked ?? true
  const log = options.log ?? (() => {})
  const started = Date.now()

  // max: 1, so this tool takes one connection out of the ceiling the app's own
  // pool was sized against. app.tf counts it among the tools that do.
  const pool = createPool({ url: options.adminUrl, max: 1, rowSecurity: false })
  const tables: TableReport[] = []
  try {
    for (const spec of SEALED_TABLES) {
      tables.push(await oneTable(pool, spec, {
        mode, to, keyring: options.keyring, batchSize, includeRevoked, log,
        ...(options.onBeforeWrite ? { onBeforeWrite: options.onBeforeWrite } : {}),
      }))
    }
  } finally {
    await pool.close()
  }

  const problems = tables.flatMap((t) => t.problems)
  const remaining = tables.reduce((n, t) => {
    const at = t.versionsBefore[to] ?? 0
    const total = Object.values(t.versionsBefore).reduce((a, b) => a + b, 0)
    // On an apply, everything scanned and written has moved; what is left is
    // what was not at the target and did not get there.
    return n + (mode === 'apply' ? total - at - t.resealed - t.changedUnderUs : total - at)
  }, 0)

  return {
    mode,
    to,
    applied: mode === 'apply',
    tables,
    scanned: tables.reduce((n, t) => n + t.scanned, 0),
    resealed: tables.reduce((n, t) => n + t.resealed, 0),
    changedUnderUs: tables.reduce((n, t) => n + t.changedUnderUs, 0),
    problems,
    remaining,
    seconds: (Date.now() - started) / 1000,
  }
}

interface Pass {
  mode: ResealMode
  to: string
  keyring: Keyring
  batchSize: number
  includeRevoked: boolean
  log: (line: string) => void
  onBeforeWrite?: (row: { table: string; id: string }) => Promise<void>
}

async function oneTable(pool: Pool, spec: SealedTable, pass: Pass): Promise<TableReport> {
  const report: TableReport = {
    table: spec.table,
    present: false,
    versionsBefore: {},
    scanned: 0,
    resealed: 0,
    changedUnderUs: 0,
    problems: [],
  }

  // to_regclass rather than a catalogue query, and rather than trying and
  // catching: a descriptor for a table this database has not migrated yet is a
  // fact to report, and a caught 42P01 inside a transaction would have aborted
  // it anyway.
  const exists = await pool.withoutTenant((db) =>
    db.execute<{ present: boolean }>(sql`SELECT to_regclass(${spec.table}) IS NOT NULL AS present`),
  )
  report.present = exists[0]?.present === true
  if (!report.present) {
    pass.log(`${spec.table}  not in this database, so nothing to re-seal`)
    return report
  }

  report.versionsBefore = await versionCounts(pool, spec)
  const before = Object.entries(report.versionsBefore)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([v, n]) => `${v}=${n}`)
    .join(' ')
  pass.log(`${spec.table}  ${before === '' ? 'no rows' : before}`)

  let cursor = '00000000-0000-0000-0000-000000000000'
  for (;;) {
    const rows = await readBatch(pool, spec, pass, cursor)
    if (rows.length === 0) break
    cursor = rows[rows.length - 1]!.id
    for (const row of rows) {
      report.scanned += 1
      await oneRow(pool, spec, pass, row, report)
    }
  }

  return report
}

/** Rows per version, which is the number an operator checks to decide a
 *  rotation is complete. Counted in the database rather than by walking rows,
 *  so it is one round trip whatever the table holds. */
async function versionCounts(pool: Pool, spec: SealedTable): Promise<Record<string, number>> {
  const rows = await pool.withoutTenant((db) =>
    db.execute<{ key_version: string; n: string }>(
      sql`SELECT key_version, count(*)::text AS n FROM ${sql.identifier(spec.table)} GROUP BY key_version`,
    ),
  )
  const out: Record<string, number> = {}
  for (const r of rows) out[r.key_version] = Number(r.n)
  return out
}

async function readBatch(pool: Pool, spec: SealedTable, pass: Pass, cursor: string): Promise<SealedRow[]> {
  // A bounded batch, ordered by the primary key, with a cursor. The cursor is
  // what guarantees progress: in apply mode a row that cannot be opened keeps
  // matching the version predicate, so without it the tool would read the same
  // rows forever having written none of them.
  const notCurrent = pass.mode === 'check' ? sql`TRUE` : sql`key_version <> ${pass.to}`
  const live = pass.includeRevoked ? sql`TRUE` : sql`revoked_at IS NULL`
  return pool.withoutTenant((db) =>
    db.execute<SealedRow>(sql`
      SELECT id::text AS id,
             ${sql.identifier(spec.orgColumn)}::text AS org,
             ${sql.identifier(spec.boundColumn)}::text AS bound,
             ciphertext, nonce, key_version, fingerprint
      FROM ${sql.identifier(spec.table)}
      WHERE ${notCurrent} AND ${live} AND id > ${cursor}::uuid
      ORDER BY id
      LIMIT ${pass.batchSize}`),
  )
}

async function oneRow(
  pool: Pool,
  spec: SealedTable,
  pass: Pass,
  row: SealedRow,
  report: TableReport,
): Promise<void> {
  const bound = { orgId: row.org, provider: row.bound }
  const sealed = {
    ciphertext: row.ciphertext,
    nonce: row.nonce,
    keyVersion: row.key_version,
    fingerprint: row.fingerprint,
  }

  if (pass.mode === 'check') {
    // Opened and thrown away. The plaintext exists for the length of this
    // expression and is not returned, logged or compared to anything but its
    // own fingerprint.
    try {
      const plaintext = open(pass.keyring, sealed, bound)
      if (fingerprintOf(plaintext) !== row.fingerprint) {
        report.problems.push(problem(spec, row, 'invariant', 'it opened and does not match the fingerprint stored beside it'))
      }
    } catch (err) {
      report.problems.push(fromError(spec, row, err))
    }
    return
  }

  let next
  try {
    next = resealValue(pass.keyring, sealed, bound, pass.to)
  } catch (err) {
    report.problems.push(fromError(spec, row, err))
    return
  }

  if (pass.mode === 'dry run') {
    report.resealed += 1
    return
  }

  if (pass.onBeforeWrite) await pass.onBeforeWrite({ table: spec.table, id: row.id })

  // The guard is the whole of the concurrency story. Another re-sealing run, or
  // a customer saving a new key from the console in the same second, changes the
  // row; this UPDATE then matches nothing and says so rather than overwriting a
  // value that is newer than the one it read.
  const updated = await pool.withoutTenant((db: Db) =>
    db.execute<{ id: string }>(sql`
      UPDATE ${sql.identifier(spec.table)}
      SET ciphertext = ${next.ciphertext}, nonce = ${next.nonce}, key_version = ${next.keyVersion}
      WHERE id = ${row.id}::uuid
        AND key_version = ${row.key_version}
        AND ciphertext = ${row.ciphertext}
      RETURNING id::text AS id`),
  )
  if (updated.length === 0) {
    report.changedUnderUs += 1
    pass.log(`${spec.table}  ${row.id}  changed under us, left alone`)
    return
  }
  report.resealed += 1
}

function problem(spec: SealedTable, row: SealedRow, kind: ProblemKind, detail: string): RowProblem {
  return { table: spec.table, id: row.id, keyVersion: row.key_version, kind, detail }
}

/** Which of the three reasons an error is, decided by CLASS rather than by
 *  reading the message. The whole point of the two error types in seal.ts is
 *  that a missing key and a failed authentication are different facts, and a
 *  report that matched on words would collapse them again the first time a
 *  sentence was reworded. */
function fromError(spec: SealedTable, row: SealedRow, err: unknown): RowProblem {
  if (err instanceof MissingSealingKeyError) {
    return problem(spec, row, 'missing key', err.message)
  }
  if (err instanceof CannotOpenError) {
    return problem(spec, row, 'cannot open', err.message)
  }
  if (err instanceof SealError) {
    return problem(spec, row, 'invariant', err.message)
  }
  throw err
}

/**
 * The report as lines, for the CLI and for a runbook that quotes it.
 *
 * Here rather than in the CLI because the check the runbook tells an operator to
 * make is "read these lines", and a second renderer somewhere would eventually
 * describe a different report from the one the tool produced.
 */
export function describe(report: ResealReport): string[] {
  const lines: string[] = []
  for (const t of report.tables) {
    if (!t.present) {
      lines.push(`${t.table.padEnd(20)} absent from this database`)
      continue
    }
    const at = Object.entries(t.versionsBefore)
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([v, n]) => `${v}=${n}`)
      .join(' ')
    lines.push(`${t.table.padEnd(20)} ${at === '' ? 'no rows' : at}`)
    lines.push(
      `${''.padEnd(20)} scanned ${t.scanned}, ` +
        `${report.mode === 'apply' ? 're-sealed' : report.mode === 'dry run' ? 'would re-seal' : 'opened'} ` +
        `${report.mode === 'check' ? t.scanned - t.problems.length : t.resealed}` +
        (t.changedUnderUs > 0 ? `, ${t.changedUnderUs} changed under us` : '') +
        (t.problems.length > 0 ? `, ${t.problems.length} could not be opened` : ''),
    )
  }
  if (report.problems.length > 0) {
    lines.push('')
    // Grouped by REASON, because the three reasons need three different next
    // steps and a flat list of four hundred rows hides which one this is.
    for (const kind of ['missing key', 'cannot open', 'invariant'] as ProblemKind[]) {
      const these = report.problems.filter((p) => p.kind === kind)
      if (these.length === 0) continue
      lines.push(`${these.length} row${these.length === 1 ? '' : 's'}: ${kind}`)
      const versions = [...new Set(these.map((p) => p.keyVersion))].sort()
      lines.push(`  under version${versions.length === 1 ? '' : 's'} ${versions.join(', ')}`)
      for (const p of these.slice(0, 10)) lines.push(`  ${p.table} ${p.id}`)
      if (these.length > 10) lines.push(`  and ${these.length - 10} more`)
      lines.push(`  ${these[0]!.detail}`)
    }
  }
  return lines
}
