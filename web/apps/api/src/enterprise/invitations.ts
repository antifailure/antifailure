// Inviting somebody who does not have an account yet.
//
// Membership has until now followed a GitHub App installation: whoever GitHub
// says is in the organization is in this one, and `members.sync` reconciles
// them. That is right for a company whose engineers are all in one GitHub
// organization and useless for the two cases enterprises actually have: a
// finance person who needs the billing page and no repository access, and a
// contractor who is not in the GitHub organization at all.
//
// So an invitation is a token in a link, and the link is the proof. Three
// things follow from that and each is a decision:
//
// THE TOKEN IS HASHED. What is stored is sha256 of a 32 byte random value, the
// same as a session. A leaked backup is a list of hashes, not a list of ways
// into somebody's organization.
//
// THE ACCEPTANCE TRANSACTION HAS NO TENANT. The person accepting belongs to no
// organization yet, so `withTenant` cannot be used: the organization is what
// the acceptance establishes. The caller declares the token hash and the
// policies in migrations/0021 confine it to that one invitation and to
// inserting one membership in that invitation's organization.
//
// THE LINK IS RETURNED TO THE INVITER, ALWAYS. Mail is optional in this product:
// a self-hosted control plane with no AF_MAIL_FROM has no mailer at all. An
// invitation that could only arrive by email would be a feature that silently
// does nothing on those installations, so the route returns the link and the
// console shows it. When a mailer is configured the message is sent as well,
// and a send that fails does not fail the invitation: the row is written, the
// link is on the screen, and the inviter can paste it into chat.

import { createHash, randomBytes } from 'node:crypto'
import { sql } from 'drizzle-orm'
import type { Db, Pool } from '@antifailure/db'
import type { Clock } from '../clock.ts'
import type { Role } from '../permissions.ts'

/** How long an invitation link is good for. */
export const INVITATION_TTL_MS = 14 * 24 * 60 * 60 * 1000

export function hashInvitationToken(token: string): Buffer {
  return createHash('sha256').update(token, 'utf8').digest()
}

/**
 * Whether a string could be an address at all.
 *
 * Deliberately not a full grammar. RFC 5322 permits things no mail provider
 * accepts, and a regular expression that tries to implement it is famously
 * wrong in both directions. What this refuses is the input that is obviously
 * not an address, so the person typing gets an answer immediately rather than
 * an invitation nobody can receive; whether the address exists is settled by
 * the message arriving.
 */
export function looksLikeEmail(value: string): boolean {
  if (value.length < 3 || value.length > 320) return false
  if (/\s/.test(value)) return false
  const at = value.indexOf('@')
  if (at <= 0 || at !== value.lastIndexOf('@')) return false
  const domain = value.slice(at + 1)
  return domain.includes('.') && !domain.startsWith('.') && !domain.endsWith('.')
}

/** Addresses are compared lower-cased, here and nowhere else, so that one
 *  caller forgetting cannot create a second invitation for the same person. */
export function normaliseEmail(value: string): string {
  return value.trim().toLowerCase()
}

export interface CreatedInvitation {
  id: string
  email: string
  role: Role
  /** The value that goes in the link. Returned once and never stored. */
  token: string
  expiresAt: Date
}

/**
 * Writes one invitation.
 *
 * Runs inside the caller's transaction so that the row and the audit entry that
 * records it commit together, which is why this takes a `Db` rather than a
 * `Pool`.
 *
 * A duplicate is refused by the partial unique index rather than by a read
 * first. Reading and then writing is a race two administrators can lose at the
 * same moment; the index cannot be raced.
 */
export async function createInvitation(
  db: Db,
  clock: Clock,
  input: { orgId: string; email: string; role: Role; invitedBy: string; invitedByLabel: string },
): Promise<CreatedInvitation> {
  const token = randomBytes(32).toString('base64url')
  const email = normaliseEmail(input.email)
  const expiresAt = new Date(clock.now().getTime() + INVITATION_TTL_MS)

  const rows = await db.execute<{ id: string }>(sql`
    INSERT INTO invitations
      (org_id, email, role, token_hash, invited_by, invited_by_label, created_at, expires_at)
    VALUES (${input.orgId}::uuid, ${email}, ${input.role}, ${hashInvitationToken(token)},
            ${input.invitedBy}::uuid, ${input.invitedByLabel},
            ${clock.now().toISOString()}, ${expiresAt.toISOString()})
    RETURNING id`)

  return { id: rows[0]!.id, email, role: input.role, token, expiresAt }
}

/** Why an invitation cannot be taken up, or null when it can. */
export type InvitationState = 'open' | 'accepted' | 'revoked' | 'expired'

export interface InvitationView {
  organization: string
  role: Role
  email: string
  invitedBy: string
  expiresAt: string
  state: InvitationState
}

/**
 * What the link says, for the page somebody lands on before they sign in.
 *
 * Returns the organization's name and the role, which is information the token
 * holder is entitled to: they were sent it. It deliberately does not say
 * whether an account already exists for the address, because that would make
 * this an oracle for whether a person works somewhere.
 */
export async function lookupInvitation(
  pool: Pool,
  clock: Clock,
  token: string,
): Promise<InvitationView | null> {
  const tokenHash = hashInvitationToken(token)
  return pool.withoutTenant(
    async (db) => {
      const rows = await db.execute<{
        email: string
        role: Role
        invited_by_label: string
        expires_at: Date | string
        accepted_at: Date | string | null
        revoked_at: Date | string | null
        org_name: string
      }>(sql`
        SELECT i.email, i.role, i.invited_by_label, i.expires_at, i.accepted_at, i.revoked_at,
               o.name AS org_name
        FROM invitations i JOIN organizations o ON o.id = i.org_id
        WHERE i.token_hash = ${tokenHash}`)
      const row = rows[0]
      if (!row) return null
      return {
        organization: row.org_name,
        role: row.role,
        email: row.email,
        invitedBy: row.invited_by_label,
        expiresAt: asDate(row.expires_at).toISOString(),
        state: stateOf(row, clock),
      }
    },
    { invitationTokenHash: tokenHash },
  )
}

export class InvitationError extends Error {
  readonly state: InvitationState | 'unknown' | 'wrong_address'
  constructor(message: string, state: InvitationError['state']) {
    super(message)
    this.state = state
  }
}

export interface AcceptedInvitation {
  orgId: string
  organization: string
  role: Role
  /** True when this account was already a member, which is what a second click
   *  on the same link looks like and is not an error. */
  alreadyMember: boolean
}

/**
 * Takes up an invitation.
 *
 * ORDERING. The membership is inserted and the invitation is marked accepted in
 * one transaction, so there is no window in which somebody holds a used link
 * and no membership, or a membership nobody can account for. The insert comes
 * first because the policy that permits it reads the invitation, and marking it
 * first would be a second thing to reason about for no benefit.
 *
 * THE INVITER MAY BE GONE. Nothing here reads the inviter's membership, their
 * role, or their account. An invitation sent by somebody who has since left is
 * as good as any other: it was authorised when it was sent, and the alternative
 * is a link that stops working for a reason the person clicking it cannot see
 * or fix. `invited_by_label` is a copy taken at send time so the record still
 * says who sent it after the account is gone.
 *
 * THE ADDRESS IS NOT CHECKED AGAINST THE ACCOUNT. It is recorded, and the
 * console shows both, but a person whose GitHub account carries a personal
 * address and who was invited at their work address is the ordinary case rather
 * than an attack. The token is the proof; the address is a label.
 */
export async function acceptInvitation(
  pool: Pool,
  clock: Clock,
  input: { token: string; userId: string },
): Promise<AcceptedInvitation> {
  const tokenHash = hashInvitationToken(input.token)

  return pool.withoutTenant(
    async (db) => {
      const rows = await db.execute<{
        id: string
        org_id: string
        role: Role
        expires_at: Date | string
        accepted_at: Date | string | null
        accepted_user_id: string | null
        revoked_at: Date | string | null
        org_name: string
      }>(sql`
        SELECT i.id, i.org_id, i.role, i.expires_at, i.accepted_at, i.accepted_user_id,
               i.revoked_at, o.name AS org_name
        FROM invitations i JOIN organizations o ON o.id = i.org_id
        WHERE i.token_hash = ${tokenHash}`)
      const row = rows[0]
      if (!row) {
        throw new InvitationError(
          'That invitation link is not valid. Ask whoever invited you to send a new one.',
          'unknown',
        )
      }

      // A second click by the same person is success, not a failure. Anything
      // else about an already-accepted invitation is a refusal.
      if (row.accepted_at) {
        if (row.accepted_user_id === input.userId) {
          return {
            orgId: row.org_id,
            organization: row.org_name,
            role: row.role,
            alreadyMember: true,
          }
        }
        throw new InvitationError(
          'That invitation has already been used by somebody else. Ask for a new one.',
          'accepted',
        )
      }
      if (row.revoked_at) {
        throw new InvitationError(
          'That invitation was withdrawn. Ask whoever invited you to send a new one.',
          'revoked',
        )
      }
      if (asDate(row.expires_at).getTime() <= clock.now().getTime()) {
        throw new InvitationError(
          'That invitation has expired. Ask whoever invited you to send a new one.',
          'expired',
        )
      }

      // ON CONFLICT DO NOTHING rather than a read first. Somebody can be
      // invited to an organization they are already in, by an administrator who
      // could not see them because they were added by a GitHub sync a minute
      // ago. Refusing that would be correct and useless: what they want is to
      // be in the organization, and they are.
      const joined = await db.execute<{ user_id: string }>(sql`
        INSERT INTO members (org_id, user_id, role, source, created_at, updated_at)
        VALUES (${row.org_id}::uuid, ${input.userId}::uuid, ${row.role}, 'invitation',
                ${clock.now().toISOString()}, ${clock.now().toISOString()})
        ON CONFLICT (org_id, user_id) DO NOTHING
        RETURNING user_id`)

      await db.execute(sql`
        UPDATE invitations
        SET accepted_at = ${clock.now().toISOString()}, accepted_user_id = ${input.userId}::uuid
        WHERE token_hash = ${tokenHash}`)

      return {
        orgId: row.org_id,
        organization: row.org_name,
        role: row.role,
        alreadyMember: joined.length === 0,
      }
    },
    { invitationTokenHash: tokenHash },
  )
}

/** The message an invitation is sent in. Plain text and HTML say the same
 *  thing: a client that renders neither still gets the link. */
export function invitationMessage(input: {
  product: string
  organization: string
  role: Role
  invitedBy: string
  link: string
}): { subject: string; text: string; html: string } {
  const subject = `${input.invitedBy} invited you to ${input.organization} on ${input.product}`
  const text = [
    `${input.invitedBy} invited you to join ${input.organization} on ${input.product} as ${input.role}.`,
    '',
    'Open this link to accept:',
    input.link,
    '',
    'The link expires in 14 days. If you were not expecting this, ignore it: nothing',
    'happens until somebody opens the link.',
  ].join('\n')
  const html = [
    `<p>${escapeHtml(input.invitedBy)} invited you to join <strong>${escapeHtml(input.organization)}</strong>`,
    ` on ${escapeHtml(input.product)} as ${escapeHtml(input.role)}.</p>`,
    `<p><a href="${escapeHtml(input.link)}">Accept the invitation</a></p>`,
    `<p>The link expires in 14 days. If you were not expecting this, ignore it: nothing happens`,
    ` until somebody opens the link.</p>`,
  ].join('')
  return { subject, text, html }
}

function escapeHtml(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

function stateOf(
  row: { accepted_at: Date | string | null; revoked_at: Date | string | null; expires_at: Date | string },
  clock: Clock,
): InvitationState {
  if (row.accepted_at) return 'accepted'
  if (row.revoked_at) return 'revoked'
  if (asDate(row.expires_at).getTime() <= clock.now().getTime()) return 'expired'
  return 'open'
}

function asDate(v: Date | string): Date {
  return v instanceof Date ? v : new Date(v)
}
