import { z } from 'zod'
import { sql } from 'drizzle-orm'
import { TRPCError } from '@trpc/server'
import { router } from '../trpc.ts'
import { adminAudit, adminProcedure, type AdminContext } from './trpc.ts'

export const recruitmentRouter = router({
  list: adminProcedure('admin.recruitment.read')
    .input(z.object({ reviewed: z.boolean().default(false), cursor: z.object({ id: z.string().uuid(), createdAt: z.string().datetime() }).optional() }).default({ reviewed: false }))
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      return c.adminDb(async (db) => {
        const rows = await db.execute<{
          id: string; name: string; email: string; role: string; project_url: string;
          why: string; created_at: Date | string; reviewed_at: Date | string | null;
        }>(sql`
          SELECT id, name, email, role, project_url, why, created_at, reviewed_at
          FROM recruitment_applications
          WHERE (reviewed_at IS NOT NULL) = ${input.reviewed}
            AND (${input.cursor?.id ?? null}::uuid IS NULL OR (created_at, id) >
              (${input.cursor?.createdAt ?? null}::timestamptz, ${input.cursor?.id ?? null}::uuid))
          ORDER BY created_at, id LIMIT 51`)
        const visible = rows.slice(0, 50)
        return {
          rows: visible.map((r) => ({ id: r.id, name: r.name, email: r.email, role: r.role,
            projectUrl: r.project_url, why: r.why, createdAt: new Date(r.created_at).toISOString(),
            reviewedAt: r.reviewed_at === null ? null : new Date(r.reviewed_at).toISOString() })),
          nextCursor: rows.length > 50 ? { id: visible[49]!.id, createdAt: new Date(visible[49]!.created_at).toISOString() } : null,
        }
      })
    }),
  review: adminProcedure('admin.recruitment.write').input(z.object({ id: z.string().uuid() }))
    .mutation(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      return c.adminDb(async (db) => {
        const rows = await db.execute(sql`
          UPDATE recruitment_applications SET reviewed_at = ${c.clock.now().toISOString()}::timestamptz,
            reviewed_by = ${c.admin.adminUserId}::uuid
          WHERE id = ${input.id}::uuid AND reviewed_at IS NULL RETURNING id`)
        if (!rows.length) throw new TRPCError({ code: 'CONFLICT', message: 'This application was already reviewed or removed. Refresh the queue.' })
        await adminAudit(db, c, { action: 'recruitment.reviewed', targetType: 'recruitment_application', targetId: input.id, severity: 'notice', detail: {} })
        return { reviewed: true }
      })
    }),
  remove: adminProcedure('admin.recruitment.write').input(z.object({ id: z.string().uuid() }))
    .mutation(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      return c.adminDb(async (db) => {
        const rows = await db.execute(sql`DELETE FROM recruitment_applications WHERE id = ${input.id}::uuid RETURNING id`)
        if (!rows.length) throw new TRPCError({ code: 'NOT_FOUND', message: 'This application was already removed.' })
        await adminAudit(db, c, { action: 'recruitment.removed', targetType: 'recruitment_application', targetId: input.id, severity: 'notice', detail: {} })
        return { removed: true }
      })
    }),
})
