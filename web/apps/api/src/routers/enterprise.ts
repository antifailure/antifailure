// Running the organization: settings, people, sessions, a copy of everything,
// and the way out.
//
// Every procedure here is built with orgProcedure(permission), which is the
// only way to build one, so none of these can exist without declaring what it
// needs. The permission matrix then exercises each of them as all four roles
// without anybody adding a case, which is what makes "the server decides"
// something this repository can prove rather than assert.
//
// The role split, stated once so the routes below do not each have to argue it:
//
//   members.manage         admin and owner. Invitations and roles.
//   sessions.manage        admin and owner. Who is signed in, and signing them
//                          out.
//   organization.settings  admin and owner. The display name.
//   data.export            admin and owner. A copy of everything.
//   billing.manage         owner alone. The billing contact, because it decides
//                          where a bill goes.
//   organization.delete    owner alone. The action that does not come back.
//   account.close          every role including viewer, because it is about the
//                          holder rather than about the organization.

import { z } from 'zod'
import { TRPCError } from '@trpc/server'
import { sql } from 'drizzle-orm'
import { router, orgProcedure, audit, type OrgContext } from '../trpc.ts'
import { ROLES } from '../permissions.ts'
import { MailError } from '../auth/mail.ts'
import { StripeError } from '../billing/stripe.ts'
import {
  createInvitation,
  hashInvitationToken,
  invitationMessage,
  looksLikeEmail,
  normaliseEmail,
  INVITATION_TTL_MS,
} from '../enterprise/invitations.ts'
import { buildExport } from '../enterprise/export.ts'
import {
  advanceDeletion,
  cancelDeletion,
  destroyHeldExport,
  readDeletion,
  requestDeletion,
  DeletionError,
  EXPORT_RETENTION_MS,
  type DeletionDeps,
} from '../enterprise/deletion.ts'
import { randomBytes } from 'node:crypto'

const uuid = z.string().uuid()

function notFound(what: string, which: string): TRPCError {
  return new TRPCError({ code: 'NOT_FOUND', message: `No ${what} called ${which}.` })
}

/** The deletion machine's dependencies, out of a request's context. */
function deletionDeps(c: OrgContext): DeletionDeps {
  return {
    pool: c.pool,
    clock: c.clock,
    github: c.github,
    stripe: c.stripe,
    log: (line, err) => console.error(line, err),
  }
}

/** Turns a DeletionError into a refusal a person can act on. */
function asRefusal(err: unknown): never {
  if (err instanceof DeletionError) {
    throw new TRPCError({ code: 'BAD_REQUEST', message: err.message })
  }
  throw err
}

/** Where a link this control plane hands out points. */
function linkTo(c: OrgContext, path: string): string {
  return new URL(path, c.appBaseUrl.endsWith('/') ? c.appBaseUrl : `${c.appBaseUrl}/`).toString()
}

// ---------------------------------------------------------------------------
// Settings, and the billing contact
// ---------------------------------------------------------------------------

/**
 * Spliced into the existing `org` router rather than given one of its own.
 *
 * There is already an `org` router holding the kill switch and the quota
 * summary, and two routers named after the same noun is how a console ends up
 * calling `org.status` on one screen and `organization.get` on the next for
 * facts about the same row.
 */
export const organizationSettings = {
  /**
   * Everything the settings page needs in one read.
   *
   * Under environments.view rather than a management permission, because a
   * viewer opening Settings and getting a refusal for the organization's own
   * name would be a page that exists to tell people they may not look at it.
   * What a viewer cannot do is change any of it, and that is enforced on each
   * write below rather than by hiding the screen.
   *
   * The billing contact is NOT here. It is an address that decides where money
   * goes, it is read by its own route under billing.manage, and putting it in
   * the read every role makes would have handed it to a viewer.
   */
  settings: orgProcedure('environments.view').query(async ({ ctx }) => {
    const c = ctx as OrgContext
    return c.pool.withTenant(c.tenant, async (db) => {
      const rows = await db.execute<{
        slug: string
        name: string
        github_login: string | null
        plan: string
        created_at: Date | string
        suspended_at: Date | string | null
        suspended_reason: string | null
        members: string
        repositories: string
        environments: string
        open_invitations: string
      }>(sql`
        SELECT o.slug, o.name, o.github_login, o.plan, o.created_at,
               o.suspended_at, o.suspended_reason,
               (SELECT count(*) FROM members m WHERE m.org_id = o.id) AS members,
               (SELECT count(*) FROM repositories r WHERE r.org_id = o.id) AS repositories,
               (SELECT count(*) FROM environments e
                 WHERE e.org_id = o.id AND e.state <> 'torn_down') AS environments,
               (SELECT count(*) FROM invitations i
                 WHERE i.org_id = o.id AND i.accepted_at IS NULL AND i.revoked_at IS NULL
                   AND i.expires_at > ${c.clock.now().toISOString()}) AS open_invitations
        FROM organizations o WHERE o.id = ${c.actor.orgId}::uuid`)
      const row = rows[0]
      if (!row) throw notFound('organization', c.actor.orgId)

      return {
        slug: row.slug,
        name: row.name,
        githubLogin: row.github_login,
        plan: row.plan,
        createdAt: asIso(row.created_at),
        suspended: row.suspended_at !== null,
        suspendedReason: row.suspended_reason,
        counts: {
          members: Number(row.members),
          repositories: Number(row.repositories),
          environments: Number(row.environments),
          openInvitations: Number(row.open_invitations),
        },
        deletion: await readDeletion(db, c.clock, c.actor.orgId),
        /** How long a copy of the export is kept after the organization is
         *  gone, so the screen can say the number rather than inventing one. */
        exportRetentionDays: Math.round(EXPORT_RETENTION_MS / (24 * 60 * 60 * 1000)),
      }
    })
  }),

  rename: orgProcedure('organization.settings')
    .input(z.object({ name: z.string().trim().min(1).max(100) }))
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) => {
        // The slug is deliberately not changed with it. A slug appears in
        // links people have already sent each other and in the confirmation a
        // deletion asks for, and renaming it would silently break both for a
        // cosmetic change.
        const rows = await db.execute<{ name: string }>(sql`
          UPDATE organizations SET name = ${input.name},
                                   updated_at = ${c.clock.now().toISOString()}
          WHERE id = ${c.actor.orgId}::uuid RETURNING name`)
        if (rows.length === 0) throw notFound('organization', c.actor.orgId)
        await audit(db, c, {
          action: 'organization.renamed',
          targetType: 'organization',
          targetId: c.actor.orgId,
          detail: { name: input.name },
        })
        return { name: rows[0]!.name }
      })
    }),

  /** Where the invoices go, and what Stripe currently believes. */
  billingContact: orgProcedure('billing.manage').query(async ({ ctx }) => {
    const c = ctx as OrgContext
    return c.pool.withTenant(c.tenant, async (db) => {
      const contact = await db.execute<{
        email: string
        name: string | null
        updated_by_label: string
        updated_at: Date | string
      }>(sql`
        SELECT email, name, updated_by_label, updated_at
        FROM billing_contacts WHERE org_id = ${c.actor.orgId}::uuid`)
      const customer = await db.execute<{ email: string | null }>(sql`
        SELECT email FROM billing_customers WHERE org_id = ${c.actor.orgId}::uuid`)
      return {
        contact: contact[0]
          ? {
              email: contact[0].email,
              name: contact[0].name,
              updatedBy: contact[0].updated_by_label,
              updatedAt: asIso(contact[0].updated_at),
            }
          : null,
        /** What is on the Stripe customer, so a page can show the two
         *  disagreeing rather than showing one and being wrong. */
        onFileWithStripe: customer[0]?.email ?? null,
        hasCustomer: customer.length > 0,
      }
    })
  }),

  /**
   * Sets where the invoices go.
   *
   * Three writes, and the order matters. The local row is the source of truth
   * and is written first. The customer row is written in the same transaction,
   * so the two cannot disagree. Stripe is told last, outside the transaction,
   * because it is a network call and holding a database transaction open across
   * one is how a slow provider becomes a lock nobody can explain.
   *
   * A Stripe refusal does not fail the change and does not roll it back. It is
   * reported: `pushedToStripe` false with the reason, and the screen says the
   * address is saved here and not yet on the invoices. Rolling back would lose
   * a change the person made because a third party was slow, and pretending it
   * succeeded would put invoices somewhere nobody is reading.
   */
  setBillingContact: orgProcedure('billing.manage')
    .input(
      z.object({
        email: z.string().trim().min(3).max(320),
        name: z.string().trim().max(200).optional(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      const email = normaliseEmail(input.email)
      if (!looksLikeEmail(email)) {
        throw new TRPCError({
          code: 'BAD_REQUEST',
          message: 'That does not look like an email address. Invoices have to reach somebody.',
        })
      }

      const customerId = await c.pool.withTenant(c.tenant, async (db) => {
        const now = c.clock.now().toISOString()
        await db.execute(sql`
          INSERT INTO billing_contacts (org_id, email, name, updated_by_label, created_at, updated_at)
          VALUES (${c.actor.orgId}::uuid, ${email}, ${input.name ?? null}, ${c.actor.label},
                  ${now}, ${now})
          ON CONFLICT (org_id) DO UPDATE
            SET email = EXCLUDED.email, name = EXCLUDED.name,
                updated_by_label = EXCLUDED.updated_by_label, updated_at = EXCLUDED.updated_at`)

        const customer = await db.execute<{ stripe_customer_id: string }>(sql`
          UPDATE billing_customers SET email = ${email}, updated_at = ${now}
          WHERE org_id = ${c.actor.orgId}::uuid
          RETURNING stripe_customer_id`)

        await audit(db, c, {
          action: 'billing.contact_changed',
          targetType: 'organization',
          targetId: c.actor.orgId,
          // The address is the change, so it is the detail. It is a corporate
          // billing address that already appears on every invoice, not a
          // secret.
          detail: { email, name: input.name ?? null },
        })
        return customer[0]?.stripe_customer_id ?? null
      })

      if (!customerId || !c.stripe) {
        return {
          email,
          pushedToStripe: false,
          note: customerId
            ? 'This control plane is not configured to talk to Stripe, so nothing was sent there.'
            : 'This organization has no Stripe customer yet, so there is nothing to update there. ' +
              'The address will be used the first time one is created.',
        }
      }

      try {
        await c.stripe.client.updateCustomerEmail(customerId, email)
        return { email, pushedToStripe: true, note: null }
      } catch (err) {
        if (err instanceof StripeError) {
          return {
            email,
            pushedToStripe: false,
            note:
              'Saved here, but Stripe would not accept the change, so invoices still go to the ' +
              'old address. Try again in a moment.',
          }
        }
        throw err
      }
    }),
}

// ---------------------------------------------------------------------------
// Invitations
// ---------------------------------------------------------------------------

export const invitationsRouter = router({
  list: orgProcedure('members.manage').query(async ({ ctx }) => {
    const c = ctx as OrgContext
    return c.pool.withTenant(c.tenant, async (db) => {
      const rows = await db.execute<{
        id: string
        email: string
        role: string
        invited_by_label: string
        created_at: Date | string
        expires_at: Date | string
        accepted_at: Date | string | null
        revoked_at: Date | string | null
      }>(sql`
        SELECT id, email, role::text AS role, invited_by_label, created_at, expires_at,
               accepted_at, revoked_at
        FROM invitations ORDER BY created_at DESC LIMIT 200`)
      const now = c.clock.now().getTime()
      return rows.map((r) => ({
        id: r.id,
        email: r.email,
        role: r.role,
        invitedBy: r.invited_by_label,
        sentAt: asIso(r.created_at),
        expiresAt: asIso(r.expires_at),
        state: r.accepted_at
          ? 'accepted'
          : r.revoked_at
            ? 'revoked'
            : new Date(asIso(r.expires_at)).getTime() <= now
              ? 'expired'
              : 'open',
      }))
    })
  }),

  /**
   * Sends one.
   *
   * The link comes back whether or not a message was sent, and that is the
   * decision this route turns on. Mail is optional in this product: a
   * self-hosted control plane with no AF_MAIL_FROM cannot send anything, and an
   * invitation that only existed as an email would be a feature that silently
   * does nothing there. So the link is the artifact and the message is the
   * convenience.
   *
   * A refused send does not fail the invitation either. The row is written, the
   * link is on the screen, and the inviter can paste it into chat. Failing the
   * whole thing because a mail provider was slow would throw away a row that is
   * already perfectly usable.
   */
  create: orgProcedure('members.manage')
    .input(
      z.object({
        email: z.string().trim().min(3).max(320),
        role: z.enum(ROLES),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      const email = normaliseEmail(input.email)
      if (!looksLikeEmail(email)) {
        throw new TRPCError({
          code: 'BAD_REQUEST',
          message: 'That does not look like an email address, so the invitation could not arrive.',
        })
      }

      const created = await c.pool.withTenant(c.tenant, async (db) => {
        // A person who is already here does not need inviting, and an
        // invitation that could never be accepted for a reason the sender
        // cannot see is worse than a refusal that names it.
        const already = await db.execute<{ github_login: string | null }>(sql`
          SELECT u.github_login FROM members m JOIN users u ON u.id = m.user_id
          WHERE lower(u.email) = ${email}`)
        if (already.length > 0) {
          throw new TRPCError({
            code: 'BAD_REQUEST',
            message: `${email} is already a member of this organization.`,
          })
        }

        const open = await db.execute<{ id: string }>(sql`
          SELECT id FROM invitations
          WHERE email = ${email} AND accepted_at IS NULL AND revoked_at IS NULL`)
        if (open.length > 0) {
          throw new TRPCError({
            code: 'BAD_REQUEST',
            message:
              `${email} already has an invitation that has not been used. Withdraw it, or send ` +
              'it again to get a fresh link.',
          })
        }

        const invitation = await createInvitation(db, c.clock, {
          orgId: c.actor.orgId,
          email,
          role: input.role,
          invitedBy: c.actor.userId,
          invitedByLabel: c.actor.label,
        })
        await audit(db, c, {
          action: 'member.invited',
          targetType: 'invitation',
          targetId: email,
          detail: { role: input.role },
        })
        return invitation
      })

      const link = linkTo(c, `invite?token=${encodeURIComponent(created.token)}`)
      const sent = await trySend(c, {
        to: email,
        organization: await organizationName(c),
        role: input.role,
        link,
      })

      return {
        id: created.id,
        email,
        role: input.role,
        link,
        expiresAt: created.expiresAt.toISOString(),
        emailed: sent.emailed,
        note: sent.note,
      }
    }),

  /**
   * Sends it again, with a new link.
   *
   * A new token rather than the old one, because the old one is not held
   * anywhere: what is stored is its hash, which is the point of storing a hash.
   * That is also the better behaviour: an invitation forwarded to the wrong
   * person is invalidated by asking for a fresh link.
   */
  resend: orgProcedure('members.manage')
    .input(z.object({ id: uuid }))
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      const token = randomBytes(32).toString('base64url')
      const expiresAt = new Date(c.clock.now().getTime() + INVITATION_TTL_MS)

      const invitation = await c.pool.withTenant(c.tenant, async (db) => {
        const rows = await db.execute<{ email: string; role: string }>(sql`
          UPDATE invitations
          SET token_hash = ${hashInvitationToken(token)}, expires_at = ${expiresAt.toISOString()}
          WHERE id = ${input.id}::uuid AND accepted_at IS NULL AND revoked_at IS NULL
          RETURNING email, role::text AS role`)
        if (rows.length === 0) {
          throw new TRPCError({
            code: 'NOT_FOUND',
            message: 'That invitation has already been used or withdrawn. Send a new one.',
          })
        }
        await audit(db, c, {
          action: 'member.invitation_resent',
          targetType: 'invitation',
          targetId: rows[0]!.email,
        })
        return rows[0]!
      })

      const link = linkTo(c, `invite?token=${encodeURIComponent(token)}`)
      const sent = await trySend(c, {
        to: invitation.email,
        organization: await organizationName(c),
        role: invitation.role,
        link,
      })
      return {
        email: invitation.email,
        role: invitation.role,
        link,
        expiresAt: expiresAt.toISOString(),
        emailed: sent.emailed,
        note: sent.note,
      }
    }),

  revoke: orgProcedure('members.manage')
    .input(z.object({ id: uuid }))
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) => {
        const rows = await db.execute<{ email: string }>(sql`
          UPDATE invitations
          SET revoked_at = ${c.clock.now().toISOString()}, revoked_by_label = ${c.actor.label}
          WHERE id = ${input.id}::uuid AND accepted_at IS NULL AND revoked_at IS NULL
          RETURNING email`)
        if (rows.length === 0) {
          throw new TRPCError({
            code: 'NOT_FOUND',
            message: 'That invitation has already been used or withdrawn.',
          })
        }
        await audit(db, c, {
          action: 'member.invitation_revoked',
          targetType: 'invitation',
          targetId: rows[0]!.email,
        })
        return { revoked: true }
      })
    }),
})

async function organizationName(c: OrgContext): Promise<string> {
  return c.pool.withTenant(c.tenant, async (db) => {
    const rows = await db.execute<{ name: string }>(sql`
      SELECT name FROM organizations WHERE id = ${c.actor.orgId}::uuid`)
    return rows[0]?.name ?? 'your organization'
  })
}

async function trySend(
  c: OrgContext,
  input: { to: string; organization: string; role: string; link: string },
): Promise<{ emailed: boolean; note: string | null }> {
  if (!c.mailer) {
    return {
      emailed: false,
      note: 'This control plane cannot send email, so copy the link and send it yourself.',
    }
  }
  const message = invitationMessage({
    product: c.productName,
    organization: input.organization,
    role: input.role as (typeof ROLES)[number],
    invitedBy: c.actor.label,
    link: input.link,
  })
  try {
    await c.mailer.send({ to: input.to, ...message })
    return { emailed: true, note: null }
  } catch (err) {
    if (err instanceof MailError) {
      return {
        emailed: false,
        note: 'The invitation is ready but the message could not be sent. Copy the link instead.',
      }
    }
    throw err
  }
}

// ---------------------------------------------------------------------------
// Sessions
// ---------------------------------------------------------------------------

export const sessionsRouter = router({
  /**
   * Who is signed in.
   *
   * Never the token and never its hash. What is here is what an administrator
   * needs to recognise a session they want gone: whose it is, where from, and
   * when it was last used. `isYou` is the one that matters most, because
   * signing yourself out of the machine you are sitting at is a reasonable
   * thing to do deliberately and a miserable thing to do by accident.
   */
  list: orgProcedure('sessions.manage')
    .input(z.object({ includeRevoked: z.boolean().default(false) }))
    .query(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) => {
        const rows = await db.execute<{
          id: string
          user_id: string
          github_login: string | null
          name: string | null
          ip: string | null
          user_agent: string | null
          created_at: Date | string
          last_seen_at: Date | string
          expires_at: Date | string
          revoked_at: Date | string | null
        }>(sql`
          SELECT s.id, s.user_id, u.github_login, u.name, host(s.ip) AS ip, s.user_agent,
                 s.created_at, s.last_seen_at, s.expires_at, s.revoked_at
          FROM sessions s JOIN users u ON u.id = s.user_id
          WHERE s.org_id = ${c.actor.orgId}::uuid
            AND (${input.includeRevoked} OR s.revoked_at IS NULL)
          ORDER BY s.last_seen_at DESC LIMIT 200`)
        const now = c.clock.now().getTime()
        return rows.map((r) => ({
          id: r.id,
          person: r.github_login ?? r.name ?? 'a closed account',
          name: r.name,
          ip: r.ip,
          userAgent: r.user_agent,
          startedAt: asIso(r.created_at),
          lastSeenAt: asIso(r.last_seen_at),
          expiresAt: asIso(r.expires_at),
          revokedAt: r.revoked_at ? asIso(r.revoked_at) : null,
          expired: new Date(asIso(r.expires_at)).getTime() <= now,
          isYou: r.id === c.actor.sessionId,
        }))
      })
    }),

  revoke: orgProcedure('sessions.manage')
    .input(z.object({ id: uuid }))
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) => {
        const rows = await db.execute<{ user_id: string }>(sql`
          UPDATE sessions SET revoked_at = ${c.clock.now().toISOString()}
          WHERE id = ${input.id}::uuid AND org_id = ${c.actor.orgId}::uuid AND revoked_at IS NULL
          RETURNING user_id`)
        if (rows.length === 0) throw notFound('live session', input.id)
        await audit(db, c, {
          action: 'session.revoked',
          targetType: 'session',
          targetId: input.id,
          detail: { wasYours: input.id === c.actor.sessionId },
        })
        return { revoked: true, wasYours: input.id === c.actor.sessionId }
      })
    }),

  /**
   * Signs one person out everywhere.
   *
   * The single-session control is for a laptop somebody left on a train. This
   * one is for somebody who has left, and it is what an administrator reaches
   * for when they are about to change or remove a role.
   */
  revokeForPerson: orgProcedure('sessions.manage')
    .input(z.object({ githubLogin: z.string().min(1).max(120) }))
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) => {
        const rows = await db.execute<{ id: string }>(sql`
          UPDATE sessions s SET revoked_at = ${c.clock.now().toISOString()}
          FROM users u
          WHERE u.id = s.user_id AND u.github_login = ${input.githubLogin}
            AND s.org_id = ${c.actor.orgId}::uuid AND s.revoked_at IS NULL
          RETURNING s.id`)
        await audit(db, c, {
          action: 'session.revoked_for_person',
          targetType: 'member',
          targetId: input.githubLogin,
          detail: { sessions: rows.length },
        })
        // Zero is a real answer rather than a failure: somebody with no live
        // session is exactly who an administrator wants after pressing this.
        return { revoked: rows.length }
      })
    }),
})

// ---------------------------------------------------------------------------
// The export
// ---------------------------------------------------------------------------

export const exportsRouter = router({
  /**
   * A copy of everything, now.
   *
   * A mutation rather than a query, and not because it changes anything the
   * caller reads back. It writes an audit entry, and an export of who did what
   * that leaves the system without being recorded is the one export nobody
   * should be able to take quietly. Being a mutation also means it carries the
   * CSRF token, which a link somebody was tricked into clicking does not.
   */
  organization: orgProcedure('data.export').mutation(async ({ ctx }) => {
    const c = ctx as OrgContext
    return c.pool.withTenant(c.tenant, async (db) => {
      const document = await buildExport(db, c.clock, {
        orgId: c.actor.orgId,
        generatedBy: c.actor.label,
      })
      await audit(db, c, {
        action: 'organization.exported',
        targetType: 'organization',
        targetId: c.actor.orgId,
        detail: { counts: document.counts, truncated: document.truncated },
      })
      return document
    })
  }),
})

// ---------------------------------------------------------------------------
// Deletion
// ---------------------------------------------------------------------------

export const deletionRouter = router({
  /**
   * Where the deletion has got to.
   *
   * Under environments.view, so every role sees the banner. An organization
   * being deleted underneath somebody is not a thing to keep from them: a
   * member whose environments stopped working needs to know why, and telling
   * only the owner means everybody else files a support ticket.
   */
  status: orgProcedure('environments.view').query(async ({ ctx }) => {
    const c = ctx as OrgContext
    return c.pool.withTenant(c.tenant, async (db) => ({
      deletion: await readDeletion(db, c.clock, c.actor.orgId),
      retentionDays: Math.round(EXPORT_RETENTION_MS / (24 * 60 * 60 * 1000)),
    }))
  }),

  /**
   * Asks for the organization to be deleted.
   *
   * `confirm` has to be the organization's slug, checked inside the same
   * transaction that writes the record so that the check and the write are one
   * act rather than two a caller could separate.
   *
   * The download link comes back once, here, and is stored nowhere: after the
   * purge there is no membership left to authorise a download, so it has to be
   * in the requester's hands before the organization stops existing.
   */
  request: orgProcedure('organization.delete')
    .input(
      z.object({
        confirm: z.string().min(1).max(200),
        reason: z.string().max(1000).optional(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      try {
        const { view, exportToken } = await requestDeletion(
          deletionDeps(c),
          { orgId: c.actor.orgId, userId: c.actor.userId, label: c.actor.label },
          { confirm: input.confirm, reason: input.reason ?? null },
        )
        return {
          deletion: view,
          exportUrl: linkTo(c, `export?token=${encodeURIComponent(exportToken)}`),
          retentionDays: Math.round(EXPORT_RETENTION_MS / (24 * 60 * 60 * 1000)),
        }
      } catch (err) {
        asRefusal(err)
      }
    }),

  /** Does the next step now, and says what happened. */
  advance: orgProcedure('organization.delete').mutation(async ({ ctx }) => {
    const c = ctx as OrgContext
    try {
      const { moved, view } = await advanceDeletion(deletionDeps(c), c.actor.orgId)
      if (!view) {
        throw new TRPCError({
          code: 'NOT_FOUND',
          message: 'There is no deletion in progress.',
        })
      }
      return { moved, deletion: view }
    } catch (err) {
      asRefusal(err)
    }
  }),

  cancel: orgProcedure('organization.delete').mutation(async ({ ctx }) => {
    const c = ctx as OrgContext
    try {
      return await cancelDeletion(deletionDeps(c), {
        orgId: c.actor.orgId,
        userId: c.actor.userId,
        label: c.actor.label,
      })
    } catch (err) {
      asRefusal(err)
    }
  }),

  /**
   * Destroys the held copy early.
   *
   * Somebody who asked for their data to be deleted may reasonably object to us
   * keeping a complete copy of it for a week, and the honest answer to that is
   * a control rather than a policy.
   */
  destroyExport: orgProcedure('organization.delete').mutation(async ({ ctx }) => {
    const c = ctx as OrgContext
    const result = await destroyHeldExport(deletionDeps(c), {
      orgId: c.actor.orgId,
      userId: c.actor.userId,
      label: c.actor.label,
    })
    if (!result.destroyed) {
      throw new TRPCError({
        code: 'NOT_FOUND',
        message: 'There is no held export to destroy.',
      })
    }
    return result
  }),
})

// ---------------------------------------------------------------------------
// Closing your own account
// ---------------------------------------------------------------------------

export const accountRouter = router({
  /**
   * Closes the account of whoever is asking.
   *
   * NOT a row delete, and the name says so. `audit_entries.actor_user_id`
   * references `users` with NO ACTION, so Postgres refuses to remove anybody
   * who has ever done anything, and that is deliberate: an audit log whose
   * subject can erase themselves from it is not an audit log. Nulling the
   * column instead is not available either, because UPDATE on that table is
   * revoked to make it append-only and because the column is inside the hash
   * chain.
   *
   * So this erases the personal data on the row, removes the memberships and
   * revokes the sessions, and leaves a row with nothing personal in it that the
   * audit entries can still point at. The response says exactly that, because a
   * control called Delete that anonymises is a claim this product does not get
   * to make.
   *
   * Signing in again afterwards makes a NEW account. The GitHub identity is
   * cleared, so nothing joins the old row to the new one.
   */
  close: orgProcedure('account.close')
    .input(z.object({ confirm: z.string().min(1).max(200) }))
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) => {
        // The label, which is what the console shows in the corner and what the
        // person can therefore read back. An earlier version asked for the
        // GitHub login, which is the same string only for somebody who has no
        // display name: `resolveSession` computes the label as name or login,
        // so anybody with a name typed what they could see and was refused for
        // a reason nothing on the screen explained.
        const expected = c.actor.label
        if (input.confirm.trim() !== expected) {
          throw new TRPCError({
            code: 'BAD_REQUEST',
            message: `Type ${expected} to confirm.`,
          })
        }

        // The last owner is refused, and it is the only refusal here. An
        // organization with no owner cannot grant anybody the permission to
        // become one, so it is unrecoverable without a database console, and
        // the person leaving is exactly the one who can still fix it.
        const owners = await db.execute<{ n: string; is_me: boolean }>(sql`
          SELECT count(*) AS n, bool_or(m.user_id = ${c.actor.userId}::uuid) AS is_me
          FROM members m WHERE m.role = 'owner'`)
        const row = owners[0]
        if (row && Number(row.n) === 1 && row.is_me) {
          throw new TRPCError({
            code: 'BAD_REQUEST',
            message:
              'You are the only owner of this organization. Make somebody else an owner first, ' +
              'or delete the organization, and then close your account.',
          })
        }

        const now = c.clock.now().toISOString()
        // The audit entry goes first, while the membership that authorises
        // writing it still exists. Written before the change rather than after
        // it, in the same transaction, so the two still commit together.
        await audit(db, c, {
          action: 'account.closed',
          targetType: 'user',
          targetId: expected,
        })

        await db.execute(sql`
          DELETE FROM members WHERE user_id = ${c.actor.userId}::uuid`)
        const sessions = await db.execute<{ id: string }>(sql`
          UPDATE sessions SET revoked_at = ${now}
          WHERE user_id = ${c.actor.userId}::uuid AND revoked_at IS NULL RETURNING id`)
        await db.execute(sql`
          UPDATE users
          SET github_id = NULL, github_login = NULL, name = NULL, avatar_url = NULL,
              email = ${`closed-account+${c.actor.userId}@invalid`},
              closed_at = ${now}, updated_at = ${now}
          WHERE id = ${c.actor.userId}::uuid`)

        return {
          closed: true,
          sessionsRevoked: sessions.length,
          removed: [
            'your name, your email address, your GitHub identity and your avatar',
            'your membership of this organization',
            'every session you had signed in with',
          ],
          kept: [
            'the audit log entries recording what you did, under the name you had at the time, ' +
              'because the log is a hash chain and changing an entry breaks it',
          ],
        }
      })
    }),
})

function asIso(v: Date | string): string {
  return (v instanceof Date ? v : new Date(v)).toISOString()
}
