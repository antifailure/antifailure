import { createHash } from 'node:crypto'
import { sql, type Db } from '@antifailure/db'
import { TRPCError } from '@trpc/server'
import {
  assertWebsiteDocument, normalizeWebsiteDocument, referencedAssets, stableStringify, type ValidationIssue, type WebsiteDocument,
} from '@antifailure/website'
import { adminAudit, type AdminContext } from './trpc.ts'

export interface WebsiteRefreshState {
  revision: number
  status: 'queued' | 'dispatching' | 'waiting' | 'deployed' | 'failed' | 'superseded'
  attempts: number
  nextAttemptAt: string
  lastError: string | null
  deployedAt: string | null
}
export interface WebsiteState {
  document: WebsiteDocument
  draftRevision: number
  publishedRevision: number
  updatedAt: string
  updatedBy: string | null
  warnings: ValidationIssue[]
  refresh: WebsiteRefreshState | null
}

export const websiteDigest = (value: unknown): string =>
  createHash('sha256').update(stableStringify(value)).digest('hex')
const iso = (value: Date | string): string => new Date(value).toISOString()

/** All writes to the one homepage serialize here, including asset deletion.
 * Expected revisions still reject edits from stale tabs; serialization alone
 * would merely make the last writer silently win. */
export async function lockWebsite(db: Db): Promise<void> {
  await db.execute(sql`SELECT pg_advisory_xact_lock(87321050)`)
}

export async function readWebsiteState(db: Db): Promise<WebsiteState> {
  const rows = await db.execute<{
    document: WebsiteDocument; draft_revision: string; published_revision: string;
    updated_at: Date | string; updated_by: string | null; status: WebsiteRefreshState['status'] | null;
    attempts: number | null; next_attempt_at: Date | string | null;
    last_error: string | null; deployed_at: Date | string | null;
  }>(sql`SELECT d.document, d.revision AS draft_revision, d.updated_at, d.updated_by,
    p.revision AS published_revision, j.status, j.attempts, j.next_attempt_at, j.last_error, j.deployed_at
    FROM website_draft d JOIN website_published p ON p.id = d.id
    LEFT JOIN website_refresh_jobs j ON j.revision = p.revision WHERE d.id = 'homepage'`)
  const row = rows[0]
  if (!row) throw new TRPCError({ code: 'PRECONDITION_FAILED', message: 'Website storage is not ready. Apply the current database migrations.' })
  const normalized = normalizeWebsiteDocument(row.document)
  return {
    document: normalized.document, warnings: normalized.warnings,
    draftRevision: Number(row.draft_revision), publishedRevision: Number(row.published_revision),
    updatedAt: iso(row.updated_at), updatedBy: row.updated_by,
    refresh: row.status && row.next_attempt_at ? {
      revision: Number(row.published_revision), status: row.status,
      attempts: Number(row.attempts), nextAttemptAt: iso(row.next_attempt_at),
      lastError: row.last_error, deployedAt: row.deployed_at ? iso(row.deployed_at) : null,
    } : null,
  }
}

export function validatedWebsiteDocument(value: unknown): WebsiteDocument {
  try { return assertWebsiteDocument(value) }
  catch { throw new TRPCError({ code: 'BAD_REQUEST', message: 'Some website edits are not valid. Review the highlighted content and try again.' }) }
}

/** Returns the ORIGINAL receipt before comparing revisions. A client that lost
 * its response can safely retry after another tab has saved or published. */
async function replayMutation(db: Db, ctx: AdminContext, requestId: string, operation: string, digest: string): Promise<WebsiteState | null> {
  const rows = await db.execute<{ actor_id: string; operation: string; digest: string; response: WebsiteState }>(sql`
    SELECT actor_id, operation, digest, response FROM website_mutations WHERE request_id = ${requestId}::uuid`)
  const old = rows[0]
  if (!old) return null
  if (old.actor_id !== ctx.admin.adminUserId || old.operation !== operation || old.digest !== digest) {
    throw new TRPCError({ code: 'CONFLICT', message: 'This save identifier belongs to a different change. Refresh the editor before trying again.' })
  }
  return old.response
}

async function rememberMutation(db: Db, ctx: AdminContext, requestId: string, operation: string, digest: string): Promise<WebsiteState> {
  const state = await readWebsiteState(db)
  await db.execute(sql`INSERT INTO website_mutations(request_id, actor_id, operation, digest, response, created_at)
    VALUES (${requestId}::uuid, ${ctx.admin.adminUserId}::uuid, ${operation}, ${digest}, ${JSON.stringify(state)}::jsonb, ${ctx.clock.now().toISOString()}::timestamptz)`)
  return state
}

function checkRevision(state: WebsiteState, expected: number): void {
  if (state.draftRevision !== expected) throw new TRPCError({
    code: 'CONFLICT', message: 'This website was edited in another tab. Reload the latest draft before saving or publishing.',
  })
}

export async function assertWebsiteAssetsExist(db: Db, document: WebsiteDocument): Promise<void> {
  const ids = referencedAssets(document)
  if (!ids.length) return
  const rows = await db.execute<{ id: string; kind: string }>(sql`SELECT id, kind FROM website_assets WHERE id IN (${sql.join(ids.map((id) => sql`${id}::uuid`), sql`, `)}) FOR UPDATE`)
  if (rows.length !== ids.length) throw new TRPCError({ code: 'BAD_REQUEST', message: 'An image, video or font in this draft is missing. Choose an available file before saving.' })
  const kinds = new Map(rows.map((row) => [row.id, row.kind]))
  const media = [...Object.values(document.fields), ...Object.values(document.collections).flatMap((collection) => collection.custom.flatMap((item) => Object.values(item.fields)))]
  for (const value of media) if (value && typeof value === 'object' && value.type === 'media' && value.source === 'asset') {
    const actualKind = kinds.get(value.assetId)
    if (actualKind === 'font') throw new TRPCError({ code: 'BAD_REQUEST', message: 'A font cannot be used as an image or video. Choose a media file for that block.' })
    if (value.kind && value.kind !== actualKind) throw new TRPCError({ code: 'BAD_REQUEST', message: 'This file has a different media type. Choose it again from the media library.' })
  }
  for (const responsive of Object.values(document.styles)) for (const style of Object.values(responsive)) {
    const family = style?.fontFamily
    if (family?.startsWith('asset:') && kinds.get(family.slice(6)) !== 'font') throw new TRPCError({ code: 'BAD_REQUEST', message: 'Choose an uploaded WOFF2 font for this typeface.' })
  }
}

export async function saveWebsiteDraft(ctx: AdminContext, input: { document: unknown; expectedRevision: number; requestId: string }): Promise<WebsiteState> {
  const document = validatedWebsiteDocument(input.document)
  const digest = websiteDigest({ document, expectedRevision: input.expectedRevision })
  return ctx.adminDb(async (db) => {
    await lockWebsite(db)
    const replay = await replayMutation(db, ctx, input.requestId, 'save', digest)
    if (replay) return replay
    const state = await readWebsiteState(db)
    checkRevision(state, input.expectedRevision)
    await assertWebsiteAssetsExist(db, document)
    await db.execute(sql`UPDATE website_draft SET document = ${JSON.stringify(document)}::jsonb,
      revision = revision + 1, updated_at = ${ctx.clock.now().toISOString()}::timestamptz,
      updated_by = ${ctx.admin.email} WHERE id = 'homepage'`)
    await adminAudit(db, ctx, { action: 'website.draft.saved', targetType: 'website', targetId: 'homepage',
      severity: 'notice', detail: { revision: state.draftRevision + 1, contentHash: websiteDigest(document) }, tenantCopy: false })
    return rememberMutation(db, ctx, input.requestId, 'save', digest)
  })
}

export async function publishWebsite(ctx: AdminContext, input: { expectedRevision: number; requestId: string; revision?: number }): Promise<WebsiteState> {
  const operation = input.revision === undefined ? 'publish' : 'restore'
  const digest = websiteDigest({ expectedRevision: input.expectedRevision, revision: input.revision ?? null })
  return ctx.adminDb(async (db) => {
    await lockWebsite(db)
    const replay = await replayMutation(db, ctx, input.requestId, operation, digest)
    if (replay) return replay
    const state = await readWebsiteState(db)
    checkRevision(state, input.expectedRevision)
    // Reads tolerate individual damaged fields so the editor can recover. A
    // publication validates the stored original instead: silently dropping a
    // malformed field here would publish a change the operator never approved.
    const stored = await db.execute<{ document: WebsiteDocument }>(sql`SELECT document FROM website_draft WHERE id = 'homepage'`)
    let document = stored[0]!.document
    let draftRevision = state.draftRevision
    if (input.revision !== undefined) {
      const history = await db.execute<{ document: WebsiteDocument }>(sql`SELECT document FROM website_history WHERE revision = ${input.revision}`)
      if (!history[0]) throw new TRPCError({ code: 'NOT_FOUND', message: 'That website version is no longer available.' })
      document = history[0].document
      draftRevision += 1
      await db.execute(sql`UPDATE website_draft SET document = ${JSON.stringify(document)}::jsonb,
        revision = ${draftRevision}, updated_at = ${ctx.clock.now().toISOString()}::timestamptz,
        updated_by = ${ctx.admin.email} WHERE id = 'homepage'`)
    }
    document = validatedWebsiteDocument(document)
    await assertWebsiteAssetsExist(db, document)
    const revision = state.publishedRevision + 1
    const contentHash = websiteDigest(document)
    const ids = referencedAssets(document)
    if (ids.length) await db.execute(sql`UPDATE website_assets SET is_public = true WHERE id IN (${sql.join(ids.map((id) => sql`${id}::uuid`), sql`, `)})`)
    await db.execute(sql`INSERT INTO website_history(revision, draft_revision, document, content_hash, source_version, restored_from, created_at, created_by)
      VALUES (${revision}, ${draftRevision}, ${JSON.stringify(document)}::jsonb, ${contentHash}, ${document.sourceVersion ?? null}, ${input.revision ?? null}, ${ctx.clock.now().toISOString()}::timestamptz, ${ctx.admin.email})`)
    await db.execute(sql`UPDATE website_published SET revision = ${revision}, document = ${JSON.stringify(document)}::jsonb,
      content_hash = ${contentHash} WHERE id = 'homepage'`)
    await db.execute(sql`INSERT INTO website_refresh_jobs(revision, next_attempt_at) VALUES (${revision}, ${ctx.clock.now().toISOString()}::timestamptz)`)
    await adminAudit(db, ctx, { action: operation === 'restore' ? 'website.restored' : 'website.published', targetType: 'website', targetId: 'homepage',
      severity: 'high', detail: { revision, draftRevision, contentHash, restoredFrom: input.revision ?? null }, tenantCopy: false })
    return rememberMutation(db, ctx, input.requestId, operation, digest)
  })
}

/** Public media stays available to immutable history. Private uploads can be
 * deleted only after the current draft no longer references them. */
export async function deleteWebsiteAsset(ctx: AdminContext, id: string): Promise<{ deleted: true }> {
  id = id.toLowerCase()
  return ctx.adminDb(async (db) => {
    await lockWebsite(db)
    const rows = await db.execute<{ is_public: boolean }>(sql`SELECT is_public FROM website_assets WHERE id = ${id}::uuid FOR UPDATE`)
    if (!rows[0]) throw new TRPCError({ code: 'NOT_FOUND', message: 'That file has already been removed.' })
    if (rows[0].is_public) throw new TRPCError({ code: 'CONFLICT', message: 'This file belongs to a published version. Archive it to hide it from the library while keeping past versions available.' })
    const draft = await readWebsiteState(db)
    if (referencedAssets(draft.document).includes(id)) throw new TRPCError({ code: 'CONFLICT', message: 'This file is used in your draft. Replace it there before deleting it.' })
    await db.execute(sql`DELETE FROM website_assets WHERE id = ${id}::uuid`)
    await adminAudit(db, ctx, { action: 'website.asset.deleted', targetType: 'website_asset', targetId: id, severity: 'notice', tenantCopy: false })
    return { deleted: true }
  })
}
