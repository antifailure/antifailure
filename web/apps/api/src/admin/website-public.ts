import type { Env, Hono } from 'hono'
import { sql, type Pool } from '@antifailure/db'
import { projectWebsiteDocument, type WebsiteDocument } from '@antifailure/website'
import { matchSiteOrigin } from '../siteorigin.ts'

export function mountWebsitePublished<E extends Env>(app: Hono<E>, deps: { pool: Pool; siteOrigins: readonly string[] | null }): void {
  app.on(['GET', 'HEAD', 'OPTIONS'], '/v1/website/published', async (c) => {
    const origin = matchSiteOrigin(c.req.header('origin'), deps.siteOrigins ?? [])
    c.header('Vary', 'Origin')
    if (origin) {
      c.header('Access-Control-Allow-Origin', origin)
      c.header('Access-Control-Expose-Headers', 'ETag')
      c.header('Access-Control-Allow-Methods', 'GET, HEAD, OPTIONS')
      c.header('Access-Control-Allow-Headers', 'If-None-Match')
    }
    if (c.req.method === 'OPTIONS') return origin ? c.body(null, 204) : c.json({ error: 'This origin is not allowed.' }, 403)
    const path = c.req.query('path')
    if (path !== undefined && (path.length > 180 || !/^\/(?:[a-z0-9_-]+(?:\/[a-z0-9_-]+)*)?$/i.test(path))) return c.json({ error: 'Invalid website path.' }, 400)
    // Conditional requests are the common path. Read the tiny revision first;
    // avoid deserializing the full document from Postgres for every 304 poll.
    const rows = await deps.pool.withoutTenant((db) => db.execute<{
      revision: string; content_hash: string;
    }>(sql`SELECT revision, content_hash FROM website_published WHERE id = 'homepage'`))
    const row = rows[0]
    if (!row) return c.json({ error: 'Published website content is not available.' }, 503)
    const etag = `"website-${row.revision}-${row.content_hash}"`
    c.header('ETag', etag)
    c.header('Cache-Control', 'public, max-age=0, must-revalidate')
    c.header('X-Content-Type-Options', 'nosniff')
    if (c.req.header('if-none-match')?.split(',').some((v) => v.trim() === etag || v.trim() === `W/${etag}` || v.trim() === '*')) return c.body(null, 304)
    if (c.req.method === 'HEAD') return c.body(null, 200)
    const full = await deps.pool.withoutTenant((db) => db.execute<{
      revision: string; document: WebsiteDocument; content_hash: string;
    }>(sql`SELECT revision, document, content_hash FROM website_published WHERE id = 'homepage'`))
    const current = full[0]
    if (!current) return c.json({ error: 'Published website content is not available.' }, 503)
    // Publication can race the two reads. The body and validator must name the
    // same row, even when the ETag changed during this request.
    c.header('ETag', `"website-${current.revision}-${current.content_hash}"`)
    return c.json({ revision: Number(current.revision), contentHash: current.content_hash,
      document: path === undefined ? current.document : projectWebsiteDocument(current.document, path) })
  })
}
