// Somebody asking to buy, and what happens to what they typed.
//
// THE THING THIS MUST NOT BECOME. The route it replaces stored an address in a
// table with no reader and sent nothing, on a domain that authorizes no mail
// sender. Every decision behind that was defensible on its own and the sum of
// them was a form whose entire behaviour was to consume what somebody typed.
//
// So there are three obligations here and each one is a function below:
//
//   RECORD    the row lands in the product's own database, which is backed up,
//             restored, drilled and readable by the operator CLI. Migration
//             0035 is where the write-only boundary is.
//   NOTIFY    somebody is told, when this installation has a way to tell them.
//   SAY SO    the caller is told which of those happened, in the response,
//             rather than being shown a success that means "written down
//             somewhere nobody looks".
//
// The third is the one that is easy to skip and it is the one that keeps the
// other two honest. `notified: false` in the response is what a person on the
// page reads as "we have it, and nobody has been paged", which is the truth on
// a deployment with no mailer, and it is what makes the missing configuration
// visible instead of comfortable.

import { randomUUID } from 'node:crypto'
import { sql } from 'drizzle-orm'
import type { Pool } from '@antifailure/db'
import type { Clock } from '../clock.ts'
import type { Mailer } from '../auth/mail.ts'

/** The longest each field may be. The database repeats every one of these as a
 *  CHECK; these exist so that a person who pasted too much gets a sentence they
 *  can act on rather than a constraint violation. */
export const LIMITS = {
  email: 320,
  name: 200,
  company: 200,
  message: 4000,
  source: 64,
} as const

export interface LeadInput {
  email: string
  name: string
  company: string
  /** How many people, when they know. Absent, empty and zero all mean the same
   *  thing and all become null, so "unknown" has one representation. */
  seats?: number | null
  message: string
  source: string
  ip?: string | null
  userAgent?: string | null
}

export interface ValidLead {
  email: string
  name: string
  company: string
  seats: number | null
  message: string
  source: string
  ip: string | null
  userAgent: string | null
}

/**
 * What is wrong with what somebody typed, or null.
 *
 * One message at a time, naming the field, because a list of four complaints
 * about a five field form is a wall somebody reads none of. The order is the
 * order the fields appear on the page, so the message always points at the
 * first thing they would fix.
 *
 * There is deliberately no uniqueness check and no "we already have you". The
 * same person asking twice from two companies is two leads, and an endpoint
 * that answered differently for an address it had seen before would be an
 * oracle for who has already contacted us.
 */
export function validateLead(input: LeadInput): { lead: ValidLead } | { error: string } {
  const name = (input.name ?? '').trim()
  if (!name) return { error: 'Tell us your name so a reply is addressed to somebody.' }
  if (name.length > LIMITS.name) return { error: `A name has to be under ${LIMITS.name} characters.` }

  const email = (input.email ?? '').trim().toLowerCase()
  // Deliberately permissive, the same judgement enterprise/invitations.ts makes
  // and for the same reason: a regular expression strict enough to be "correct"
  // about RFC 5322 rejects real addresses, and turning away a person who wants
  // to buy something costs far more than storing one row that bounces.
  const at = email.indexOf('@')
  const domain = email.slice(at + 1)
  const looksLikeAddress =
    at > 0 &&
    at === email.lastIndexOf('@') &&
    !/[\s,;<>"]/.test(email) &&
    domain.includes('.') &&
    !domain.startsWith('.') &&
    !domain.endsWith('.')
  if (!looksLikeAddress) return { error: 'That does not look like an email address.' }
  if (email.length > LIMITS.email) {
    return { error: `An email address has to be under ${LIMITS.email} characters.` }
  }

  const company = (input.company ?? '').trim()
  if (!company) return { error: 'Tell us who you work for.' }
  if (company.length > LIMITS.company) {
    return { error: `A company name has to be under ${LIMITS.company} characters.` }
  }

  const message = (input.message ?? '').trim()
  if (!message) return { error: 'Say what you need, even in one line.' }
  if (message.length > LIMITS.message) {
    return {
      error: `Keep it under ${LIMITS.message} characters. Anything longer is better on a call.`,
    }
  }

  // Not an error when it is missing or nonsense. Somebody who does not know how
  // many seats they need must not be stopped by a field they cannot answer, and
  // a number typed with a comma in it is not a reason to refuse a lead.
  const rawSeats = Number(input.seats)
  const seats = Number.isFinite(rawSeats) && rawSeats > 0 ? Math.floor(rawSeats) : null

  const source = (input.source ?? '').trim().slice(0, LIMITS.source) || 'unknown'

  return {
    lead: {
      email,
      name,
      company,
      seats,
      message,
      source,
      ip: input.ip ?? null,
      userAgent: input.userAgent ? input.userAgent.slice(0, 500) : null,
    },
  }
}

/**
 * Writes the lead, and returns its id.
 *
 * `withoutTenant`, declaring nothing, because there is nothing to declare: the
 * person has no organization, no session and no token, which is the entire
 * reason this route exists. What confines the transaction is not a declaration
 * but the grant, which is INSERT and nothing else. The application role cannot
 * read this table back even holding a row's id, so the only thing an anonymous
 * caller can make this connection do is add to it.
 *
 * THE ID IS GENERATED HERE, NOT BY THE DATABASE, and that is a consequence of
 * the sentence above rather than a preference. `RETURNING id` has to READ the
 * row back, and reading is exactly what this role cannot do: the grant carries
 * no SELECT and the policy denies it, so the statement fails with a permission
 * error and the route answers 500. I watched that happen before writing this
 * comment. enterprise/invitations.ts records the same trap from the other side,
 * where a RETURNING clause failed under a policy and reported the WITH CHECK
 * message, which points at the wrong half of the statement.
 *
 * A uuid from `randomUUID` is the same v4 shape `gen_random_uuid()` produces,
 * so nothing downstream can tell which end made it.
 */
export async function recordLead(
  pool: Pool,
  clock: Clock,
  lead: ValidLead,
): Promise<{ id: string }> {
  const id = randomUUID()
  await pool.withoutTenant(async (db) => {
    await db.execute(sql`
      INSERT INTO enterprise_leads
        (id, email, name, company, seats, message, source, ip, user_agent, created_at)
      VALUES (${id}::uuid, ${lead.email}, ${lead.name}, ${lead.company}, ${lead.seats},
              ${lead.message}, ${lead.source}, ${lead.ip}::inet, ${lead.userAgent},
              ${clock.now().toISOString()})`)
  })
  return { id }
}

/** Where a lead is announced, when this installation has anywhere. */
export interface LeadNotifier {
  readonly mailer: Mailer
  /** The address a lead is announced to. Ours, not the customer's. */
  readonly to: string
  /** The product name in the subject line. */
  readonly productName?: string
}

/**
 * Reads the notifier out of the environment, or explains why there is none.
 *
 * Absent is a supported state and the summary says which of the two absences it
 * is. A self-hosted control plane has no sales inbox and should not be asked to
 * invent one. Our own deployment has an inbox and, at the time of writing, no
 * mailer: antifailure.dev publishes no mail exchanger and an SPF policy that
 * authorizes no sender, so the address a lead would be announced to has no route
 * to it. Saying that at start-up is how the missing half stops being invisible.
 */
export function leadNotifierFrom(
  env: Record<string, string | undefined>,
  mailer: Mailer | undefined,
): { notifier: LeadNotifier | null; summary: string } {
  const to = env.AF_LEAD_NOTIFY_EMAIL?.trim()
  if (!to) {
    return {
      notifier: null,
      summary:
        'enterprise leads are recorded and nobody is mailed about them: AF_LEAD_NOTIFY_EMAIL is not set. ' +
        'Read them with af-control-plane-backup leads.',
    }
  }
  if (!mailer) {
    return {
      notifier: null,
      summary:
        `enterprise leads are recorded and ${to} CANNOT be told: AF_LEAD_NOTIFY_EMAIL is set and no ` +
        'mailer is configured, so set AF_RESEND_API_KEY and AF_MAIL_FROM. Read them with ' +
        'af-control-plane-backup leads.',
    }
  }
  return {
    notifier: {
      mailer,
      to,
      ...(env.AF_PRODUCT_NAME ? { productName: env.AF_PRODUCT_NAME } : {}),
    },
    summary: `enterprise leads are recorded and announced to ${to}`,
  }
}

/**
 * The message a lead is announced in.
 *
 * Everything the person typed, because the point of the announcement is that
 * whoever reads it can reply without opening anything else. Nothing is
 * summarised and nothing is truncated: a lead is a handful of fields and the
 * one that got cut would be the one that mattered.
 */
export function leadMessage(input: {
  product: string
  lead: ValidLead
  id: string
}): { subject: string; text: string; html: string } {
  const { lead } = input
  const subject = `${lead.company} asked about ${input.product}`
  const lines = [
    `${lead.name} <${lead.email}> at ${lead.company} asked about ${input.product}.`,
    '',
    lead.seats === null ? 'Seats: not stated' : `Seats: ${lead.seats}`,
    `Came from: ${lead.source}`,
    `Lead id: ${input.id}`,
    '',
    lead.message,
    '',
    'Reply to the address above. Mark it handled with:',
    `  af-control-plane-backup leads --url <admin connection string> --handle ${input.id}`,
  ]
  const html = [
    `<p><strong>${escapeHtml(lead.name)}</strong> &lt;${escapeHtml(lead.email)}&gt; at `,
    `<strong>${escapeHtml(lead.company)}</strong> asked about ${escapeHtml(input.product)}.</p>`,
    `<p>Seats: ${lead.seats === null ? 'not stated' : lead.seats}<br>`,
    `Came from: ${escapeHtml(lead.source)}<br>`,
    `Lead id: <code>${escapeHtml(input.id)}</code></p>`,
    `<blockquote>${escapeHtml(lead.message).replaceAll('\n', '<br>')}</blockquote>`,
    `<p>Reply to the address above. Mark it handled with `,
    `<code>af-control-plane-backup leads --handle ${escapeHtml(input.id)}</code>.</p>`,
  ].join('')
  return { subject, text: lines.join('\n'), html }
}

function escapeHtml(value: string): string {
  return value
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
}
