import type { Env, Hono } from 'hono'
import { sql, type Pool } from '@antifailure/db'
import type { WebsiteDocument } from '@antifailure/website'
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
    const rows = await deps.pool.withoutTenant((db) => db.execute<{
      revision: string; document: WebsiteDocument; content_hash: string;
    }>(sql`SELECT revision, document, content_hash FROM website_published WHERE id = 'homepage'`))
    const row = rows[0]
    if (!row) return c.json({ error: 'Published website content is not available.' }, 503)
    const etag = `"website-${row.revision}-${row.content_hash}"`
    c.header('ETag', etag)
    c.header('Cache-Control', 'public, max-age=0, must-revalidate')
    c.header('X-Content-Type-Options', 'nosniff')
    if (c.req.header('if-none-match')?.split(',').some((v) => v.trim() === etag || v.trim() === `W/${etag}` || v.trim() === '*')) return c.body(null, 304)
    if (c.req.method === 'HEAD') return c.body(null, 200)
    return c.json({ revision: Number(row.revision), contentHash: row.content_hash, document: row.document })
  })
}
