// The enterprise leads lane of the operator portal.
//
// WHAT THIS FIXES, and it is a gap rather than a bug. Somebody who fills in the
// "talk to us" form lands as a row in enterprise_leads, which migration 0035
// arranged so the anonymous public form can only INSERT and can never read a
// row back. That was the whole point: a role that could also SELECT there is
// one query bug away from publishing every prospect's contact details. But the
// only reader that boundary left was af-control-plane-backup leads, a CLI that
// needs the migration role or the cluster superuser, so in practice a lead
// landed and nobody with the portal open could see it. The list was reachable
// only by whoever holds a privileged connection string and remembers to look,
// which is the same failure the waitlist had that this table was built to end.
//
// So this router is the reader the portal was missing. It reads through
// ctx.adminDb, the operator pool, whose antifailure_admin role already holds
// SELECT on this table via 0023's default privileges and now holds UPDATE too,
// granted by migration 0047 for exactly the mark-handled write below. The
// serving role is untouched and stays INSERT-only, so the leak boundary is
// intact and the queue is finally readable by an operator without a psql
// session.
//
// It mirrors recruitment.ts deliberately: a private queue of people who are not
// customers, oldest first, with a mark-handled write and no un-handle. There is
// no remove here, because a lead is a request to buy and destroying it is not a
// routine act the way retracting an application is.

import { z } from 'zod'
import { sql } from 'drizzle-orm'
import { TRPCError } from '@trpc/server'
import { router } from '../trpc.ts'
import { adminAudit, adminProcedure, type AdminContext } from './trpc.ts'

export const leadsRouter = router({
  list: adminProcedure('admin.leads.read')
    .input(z.object({ handled: z.boolean().default(false), cursor: z.object({ id: z.string().uuid(), createdAt: z.string().datetime() }).optional() }).default({ handled: false }))
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      return c.adminDb(async (db) => {
        // ip and user_agent are deliberately not selected. 0035 keeps them for
        // one purpose, abuse, and a per-row contact page is not where anybody
        // answers a flood; putting them on the operator's screen would spread a
        // prospect's network trail across a dashboard for no use.
        const rows = await db.execute<{
          id: string; email: string; name: string; company: string; seats: number | string | null;
          message: string; source: string; created_at: Date | string;
          handled_at: Date | string | null; handled_note: string | null;
        }>(sql`
          SELECT id, email, name, company, seats, message, source, created_at, handled_at, handled_note
          FROM enterprise_leads
          WHERE (handled_at IS NOT NULL) = ${input.handled}
            AND (${input.cursor?.id ?? null}::uuid IS NULL OR (created_at, id) >
              (${input.cursor?.createdAt ?? null}::timestamptz, ${input.cursor?.id ?? null}::uuid))
          ORDER BY created_at, id LIMIT 51`)
        const visible = rows.slice(0, 50)
        return {
          rows: visible.map((r) => ({
            id: r.id, email: r.email, name: r.name, company: r.company,
            // Postgres hands an integer column back as a number here, but a
            // string would coerce silently and a NaN would not; Number(null) is
            // 0, so the null is kept as null rather than run through it.
            seats: r.seats === null ? null : Number(r.seats),
            message: r.message, source: r.source,
            createdAt: new Date(r.created_at).toISOString(),
            handledAt: r.handled_at === null ? null : new Date(r.handled_at).toISOString(),
            handledNote: r.handled_note,
          })),
          nextCursor: rows.length > 50 ? { id: visible[49]!.id, createdAt: new Date(visible[49]!.created_at).toISOString() } : null,
        }
      })
    }),
  handle: adminProcedure('admin.leads.write')
    .input(z.object({ id: z.string().uuid(), note: z.string().max(2000).optional() }))
    .mutation(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      return c.adminDb(async (db) => {
        // handled_at and handled_by are set together on purpose: the table's
        // CHECK in 0035 pairs them, so a time without an operator or an operator
        // without a time is refused by the database, not just by taste. The
        // operator is the admin_users row this session belongs to, which is the
        // foreign key the column names.
        const rows = await db.execute(sql`
          UPDATE enterprise_leads
          SET handled_at = ${c.clock.now().toISOString()}::timestamptz,
              handled_by = ${c.admin.adminUserId}::uuid,
              handled_note = ${input.note ?? null}
          WHERE id = ${input.id}::uuid AND handled_at IS NULL RETURNING id`)
        if (!rows.length) throw new TRPCError({ code: 'CONFLICT', message: 'This lead was already marked handled. Refresh the queue to see who answered it and when.' })
        await adminAudit(db, c, { action: 'leads.handled', targetType: 'enterprise_lead', targetId: input.id, severity: 'notice', detail: {} })
        return { handled: true }
      })
    }),
})
