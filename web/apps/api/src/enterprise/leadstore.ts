// Reading the leads, on a connection the serving process does not have.
//
// This is the half the waitlist never had, and its absence was the whole
// defect. That form wrote an address into a store nothing in the repository
// could read, on the reasoning that an anonymous endpoint able to enumerate its
// own signups is how a list becomes a leaked mailing list. The reasoning was
// right and the conclusion was half of one: what it needed was a reader that is
// not the anonymous endpoint, and instead it had none at all. So a row landed,
// nothing was mailed, and the only way to see it was somebody with the Azure
// subscription deciding to look.
//
// So: the write path is the application role, which holds INSERT and nothing
// else, and cannot read a row back even holding its id. The read path is this
// file, on the same privileged connection break-glass and create-org take,
// which is a credential the process serving public traffic does not have and
// cannot acquire. Migration 0035 is where that boundary is drawn.

import { createPool, sql, type Pool } from '@antifailure/db'

export class LeadsRefused extends Error {}

/** How many rows a bare `leads` prints. Enough that a quiet week is one screen
 *  and a flood is obviously a flood, and low enough that a mistake does not
 *  scroll a terminal for a minute. */
const DEFAULT_LIMIT = 50
const MAX_LIMIT = 500

export interface Lead {
  id: string
  email: string
  name: string
  company: string
  seats: number | null
  message: string
  source: string
  createdAt: Date
  handledAt: Date | null
  handledNote: string | null
}

export interface ListLeadsInput {
  /** A connection string row-level security does not apply to. */
  adminUrl: string
  /** Include the ones already dealt with. Off by default, because the question
   *  somebody runs this to answer is "who is waiting". */
  includeHandled?: boolean
  limit?: number
}

/**
 * The leads, oldest first.
 *
 * OLDEST FIRST, which is the opposite of almost every other listing in this
 * repository and is deliberate. A queue read newest first answers the wrong
 * question: the person who has been waiting longest is the one who is about to
 * give up, and putting them at the bottom of a list is how they stay there.
 */
export async function listLeads(input: ListLeadsInput): Promise<Lead[]> {
  const limit = boundedLimit(input.limit)
  const pool = createPool({ url: input.adminUrl, max: 1, rowSecurity: false })
  try {
    await assertUnrestricted(pool)
    return await pool.withoutTenant(async (db) => {
      const rows = await db.execute<{
        id: string
        email: string
        name: string
        company: string
        seats: number | null
        message: string
        source: string
        created_at: Date | string
        handled_at: Date | string | null
        handled_note: string | null
      }>(sql`
        SELECT id, email, name, company, seats, message, source, created_at,
               handled_at, handled_note
        FROM enterprise_leads
        WHERE ${input.includeHandled ? sql`true` : sql`handled_at IS NULL`}
        ORDER BY created_at ASC
        LIMIT ${limit}`)
      return rows.map((row) => ({
        id: row.id,
        email: row.email,
        name: row.name,
        company: row.company,
        seats: row.seats === null ? null : Number(row.seats),
        message: row.message,
        source: row.source,
        createdAt: asDate(row.created_at),
        handledAt: row.handled_at === null ? null : asDate(row.handled_at),
        handledNote: row.handled_note,
      }))
    })
  } finally {
    await pool.close()
  }
}

export interface HandleLeadInput {
  adminUrl: string
  id: string
  /** The operator claiming it, by the address their operator account carries.
   *  Required, and resolved against `admin_users`; see below. */
  as: string
  note?: string | undefined
  /** The shell account the command ran under, for the note. Not an identity. */
  operator?: string | undefined
}

/**
 * Marks one lead answered.
 *
 * WHY IT MAKES YOU NAME YOURSELF. `handled_by` references `admin_users(id)` and
 * the table's constraint pairs it with `handled_at`, so recording that a lead
 * was answered means recording which operator answered it. Whoever runs this
 * command is holding a database connection string, which is not an identity:
 * there may be no operator account for them at all.
 *
 * Three ways to resolve that and two of them are wrong. Attributing it to the
 * root operator because there is always exactly one would put somebody else's
 * name on every lead anybody handled. Guessing from the shell username would be
 * a claim about who was at the keyboard that a shared machine makes false. So
 * `--as` is required, it is resolved against `admin_users`, and an address with
 * no operator account is refused rather than recorded as nobody. The record
 * stays true at the cost of one flag.
 */
export async function handleLead(input: HandleLeadInput): Promise<Lead> {
  const pool = createPool({ url: input.adminUrl, max: 1, rowSecurity: false })
  try {
    await assertUnrestricted(pool)
    return await pool.withoutTenant(async (db) => {
      const found = await db.execute<{ id: string; handled_at: Date | string | null }>(sql`
        SELECT id, handled_at FROM enterprise_leads WHERE id::text = ${input.id}`)
      if (!found[0]) {
        throw new LeadsRefused(
          `No lead has the id ${input.id}. Run \`leads --all\` to see the ids, which are the ` +
            'first column beside the time.',
        )
      }
      if (found[0].handled_at) {
        throw new LeadsRefused(
          `That lead was already marked handled on ${asDate(found[0].handled_at).toISOString()}. ` +
            'Marking it again would overwrite who dealt with it and when, which is the record ' +
            'somebody would come here to read.',
        )
      }

      // The operator the record will name. Resolved against admin_users rather
      // than trusted from the shell, because the column is a foreign key and a
      // record naming an account that does not exist is a record of nothing.
      const claimed = input.as.trim().toLowerCase()
      const operators = await db.execute<{ id: string; email: string }>(sql`
        SELECT id, email FROM admin_users WHERE email = ${claimed}`)
      const operator = operators[0]
      if (!operator) {
        throw new LeadsRefused(
          `No operator has the address ${claimed}, so there is nobody to record as having ` +
            'handled this. If this control plane has no operators at all, create the root one ' +
            'with `bootstrap-operator`, which is also what makes the operator portal reachable.',
        )
      }

      const note = input.note?.trim()
        ? input.note.trim().slice(0, 2000)
        : `handled from the command line${input.operator ? ` by ${input.operator}` : ''}`

      const rows = await db.execute<{
        id: string
        email: string
        name: string
        company: string
        seats: number | null
        message: string
        source: string
        created_at: Date | string
        handled_at: Date | string
        handled_note: string | null
      }>(sql`
        UPDATE enterprise_leads
        SET handled_at = now(), handled_by = ${operator.id}::uuid, handled_note = ${note}
        WHERE id::text = ${input.id} AND handled_at IS NULL
        RETURNING id, email, name, company, seats, message, source, created_at,
                  handled_at, handled_note`)
      const row = rows[0]
      if (!row) {
        // Two commands racing on one lead. The precondition is in the statement
        // rather than above it, so exactly one of them wins and the other is
        // told rather than silently overwriting.
        throw new LeadsRefused('That lead was marked handled by somebody else a moment ago.')
      }
      return {
        id: row.id,
        email: row.email,
        name: row.name,
        company: row.company,
        seats: row.seats === null ? null : Number(row.seats),
        message: row.message,
        source: row.source,
        createdAt: asDate(row.created_at),
        handledAt: asDate(row.handled_at),
        handledNote: row.handled_note,
      }
    })
  } finally {
    await pool.close()
  }
}

function boundedLimit(value: number | undefined): number {
  if (value === undefined) return DEFAULT_LIMIT
  if (!Number.isFinite(value) || value < 1) {
    throw new LeadsRefused('--limit has to be a positive whole number.')
  }
  return Math.min(Math.floor(value), MAX_LIMIT)
}

/**
 * Proves the connection can actually read the table before anything depends on
 * it.
 *
 * The same probe break-glass and create-org make, and it earns its keep harder
 * here than anywhere: `enterprise_leads` grants the application role INSERT and
 * no SELECT, so running this on AF_DATABASE_URL does not return zero rows, it
 * raises. Without this the failure would be a permission error from a driver in
 * the middle of a listing, rather than a sentence naming the credential.
 */
async function assertUnrestricted(pool: Pool): Promise<void> {
  try {
    await pool.withoutTenant((db) => db.execute(sql`SELECT 1 FROM enterprise_leads LIMIT 1`))
  } catch (error) {
    throw new LeadsRefused(
      `This connection cannot read enterprise_leads: ${reasonFor(error)}\n\n` +
        'That table is written by an anonymous endpoint and is deliberately unreadable by the ' +
        'role that serves requests: it holds INSERT and no SELECT, so a query bug on a public ' +
        'route cannot publish somebody else\'s contact details. Use a connection row-level ' +
        'security does not apply to: the cluster superuser, or the migration role. That is the ' +
        'same connection string the bootstrap job uses, not the one in AF_DATABASE_URL.',
    )
  }
}

function reasonFor(error: unknown): string {
  const cause = (error as { cause?: unknown }).cause
  if (cause instanceof Error && cause.message) return cause.message
  return error instanceof Error ? error.message : String(error)
}

function asDate(v: Date | string): Date {
  return v instanceof Date ? v : new Date(v)
}
