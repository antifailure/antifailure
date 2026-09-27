import { z } from 'zod'
import { sql } from '@antifailure/db'
import { TRPCError } from '@trpc/server'
import { normalizeWebsiteDocument, type WebsiteDocument } from '@antifailure/website'
import { router } from '../trpc.ts'
import { adminAudit, adminProcedure, type AdminContext } from './trpc.ts'
import {
  deleteWebsiteAsset, lockWebsite, publishWebsite, readWebsiteState, saveWebsiteDraft,
} from './website-store.ts'
import { previewWebsiteAssetUrls } from './website-media.ts'

const revision = z.number().int().min(0).max(Number.MAX_SAFE_INTEGER)
const mutation = z.object({ expectedRevision: revision, requestId: z.string().uuid() })
const timestamp = (value: Date | string) => new Date(value).toISOString()

/** Database diagnostics never become operator copy. Expected conflicts retain
 * their helpful message; failures leave the entire audited transaction rolled back. */
async function websiteCall<T>(fn: () => Promise<T>): Promise<T> {
  try { return await fn() }
  catch (error) {
    if (error instanceof TRPCError) throw error
    throw new TRPCError({ code: 'INTERNAL_SERVER_ERROR', message: 'The website could not be updated. Your last saved version is safe. Please try again.' })
  }
}

export const websiteRouter = router({
  get: adminProcedure('admin.website.read').query(({ ctx }) =>
    websiteCall(() => (ctx as AdminContext).adminDb(readWebsiteState))),
  saveDraft: adminProcedure('admin.website.write')
    .input(mutation.extend({ document: z.unknown() }))
    .mutation(({ ctx, input }) => websiteCall(() => saveWebsiteDraft(ctx as AdminContext, input))),
  publish: adminProcedure('admin.website.publish').input(mutation)
    .mutation(({ ctx, input }) => websiteCall(() => publishWebsite(ctx as AdminContext, input))),
  restore: adminProcedure('admin.website.publish').input(mutation.extend({ revision: revision.min(1) }))
    .mutation(({ ctx, input }) => websiteCall(() => publishWebsite(ctx as AdminContext, input))),
  history: adminProcedure('admin.website.read')
    .input(z.object({ limit: z.number().int().min(1).max(50).default(20), cursor: revision.min(1).optional() }).default({ limit: 20 }))
    .query(({ ctx, input }) => websiteCall(() => (ctx as AdminContext).adminDb(async (db) => {
      const rows = await db.execute<{ revision: string; created_at: Date | string; created_by: string; source_version: string | null }>(sql`
        SELECT revision, created_at, created_by, source_version FROM website_history
        WHERE (${input.cursor ?? null}::bigint IS NULL OR revision < ${input.cursor ?? null})
        ORDER BY revision DESC LIMIT ${input.limit + 1}`)
      const items = rows.slice(0, input.limit).map((r) => ({
        revision: Number(r.revision), createdAt: timestamp(r.created_at), createdBy: r.created_by, sourceVersion: r.source_version,
      }))
      return { items, nextCursor: rows.length > input.limit ? items.at(-1)!.revision : null }
    }))),
  revision: adminProcedure('admin.website.read').input(z.object({ revision: revision.min(1) }))
    .query(({ ctx, input }) => websiteCall(() => (ctx as AdminContext).adminDb(async (db) => {
      const rows = await db.execute<{ document: WebsiteDocument; revision: string; draft_revision: string; content_hash: string;
        created_at: Date | string; created_by: string; source_version: string | null; restored_from: string | null }>(sql`
        SELECT document, revision, draft_revision, content_hash, created_at, created_by, source_version, restored_from
        FROM website_history WHERE revision = ${input.revision}`)
      const r = rows[0]
      if (!r) throw new TRPCError({ code: 'NOT_FOUND', message: 'That website version is no longer available.' })
      const normalized = normalizeWebsiteDocument(r.document)
      return { document: normalized.document, warnings: normalized.warnings, revision: Number(r.revision), draftRevision: Number(r.draft_revision), contentHash: r.content_hash,
        createdAt: timestamp(r.created_at), createdBy: r.created_by, sourceVersion: r.source_version, restoredFrom: r.restored_from === null ? null : Number(r.restored_from) }
    }))),
  assets: adminProcedure('admin.website.read').input(z.object({
    query: z.string().max(180).optional(), kind: z.enum(['image', 'video', 'font']).optional(),
    archived: z.boolean().default(false), limit: z.number().int().min(1).max(60).default(30),
    cursor: z.object({ id: z.string().uuid(), createdAt: z.string().datetime() }).optional(),
  }).default({ archived: false, limit: 30 })).query(({ ctx, input }) => websiteCall(() => (ctx as AdminContext).adminDb(async (db) => {
    const rows = await db.execute<{ id: string; name: string; kind: 'image' | 'video' | 'font'; mime_type: string;
      size_bytes: number; width: number | null; height: number | null; is_public: boolean; archived: boolean; created_at: Date | string }>(sql`
      SELECT id, name, kind, mime_type, size_bytes, width, height, is_public, archived, created_at FROM website_assets
      WHERE archived = ${input.archived} AND (${input.kind ?? null}::text IS NULL OR kind = ${input.kind ?? null})
        AND (${input.query ?? ''} = '' OR strpos(lower(name), lower(${input.query ?? ''})) > 0)
        AND (${input.cursor?.id ?? null}::uuid IS NULL OR (created_at, id) < (${input.cursor?.createdAt ?? null}::timestamptz, ${input.cursor?.id ?? null}::uuid))
      ORDER BY created_at DESC, id DESC LIMIT ${input.limit + 1}`)
    const items = rows.slice(0, input.limit).map((r) => ({ id: r.id, name: r.name, kind: r.kind, mimeType: r.mime_type,
      sizeBytes: r.size_bytes, width: r.width, height: r.height, isPublic: r.is_public, archived: r.archived, createdAt: timestamp(r.created_at) }))
    return { items, nextCursor: rows.length > input.limit ? { id: items.at(-1)!.id, createdAt: items.at(-1)!.createdAt } : null }
  }))),
  assetPreview: adminProcedure('admin.website.read').input(z.object({ ids: z.array(z.string().uuid()).max(60) }))
    .query(({ ctx, input }) => websiteCall(() => {
      const c = ctx as AdminContext
      return c.adminDb(async (db) => ({ urls: await previewWebsiteAssetUrls(db, input.ids, c.clock.now()) }))
    })),
  archiveAsset: adminProcedure('admin.website.write').input(z.object({ id: z.string().uuid(), archived: z.boolean() }))
    .mutation(({ ctx, input }) => websiteCall(() => {
      const c = ctx as AdminContext
      return c.adminDb(async (db) => {
        const rows = await db.execute(sql`UPDATE website_assets SET archived = ${input.archived} WHERE id = ${input.id}::uuid RETURNING id`)
        if (!rows.length) throw new TRPCError({ code: 'NOT_FOUND', message: 'That file is no longer available.' })
        await adminAudit(db, c, { action: input.archived ? 'website.asset.archived' : 'website.asset.unarchived', targetType: 'website_asset', targetId: input.id, severity: 'notice', tenantCopy: false })
        return { id: input.id, archived: input.archived }
      })
    })),
  deleteAsset: adminProcedure('admin.website.write').input(z.object({ id: z.string().uuid() }))
    .mutation(({ ctx, input }) => websiteCall(() => deleteWebsiteAsset(ctx as AdminContext, input.id))),
  retryRefresh: adminProcedure('admin.website.publish').input(z.object({ revision: revision.min(1) }))
    .mutation(({ ctx, input }) => websiteCall(() => {
      const c = ctx as AdminContext
      return c.adminDb(async (db) => {
        await lockWebsite(db)
        const state = await readWebsiteState(db)
        if (state.publishedRevision !== input.revision) throw new TRPCError({ code: 'CONFLICT', message: 'A newer version is published. Refresh the editor to check its status.' })
        const rows = await db.execute(sql`UPDATE website_refresh_jobs SET status = 'queued', attempts = 0, next_attempt_at = ${c.clock.now().toISOString()}::timestamptz,
          lease_until = NULL, lease_token = NULL, last_error = NULL
          WHERE revision = ${input.revision} AND status = 'failed' RETURNING revision`)
        if (!rows.length) throw new TRPCError({ code: 'CONFLICT', message: 'This website refresh is already running or complete.' })
        await adminAudit(db, c, { action: 'website.refresh.retried', targetType: 'website', targetId: 'homepage', severity: 'notice', detail: { revision: input.revision }, tenantCopy: false })
        return readWebsiteState(db)
      })
    })),
})
