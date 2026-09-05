import type { Hono, Context, Env } from 'hono'
import { bodyLimit } from 'hono/body-limit'
import type { Pool } from '@antifailure/db'
import type { Clock } from '../clock.ts'
import { applicationInput, recordApplication } from './applications.ts'
import { matchSiteOrigin } from '../siteorigin.ts'

export function mountApplicationRoutes<E extends Env>(
  app: Hono<E>,
  options: { pool: Pool; clock: Clock; siteOrigins?: readonly string[] },
) {
  function origin(c: Context<E>) {
    c.header('cache-control', 'no-store')
    // Sent whether or not a header follows, and it matters more now than it did
    // with one allowed origin: a shared cache that did not vary on this can
    // serve the apex's allow header to a request that arrived on www, or a
    // refusal to a request that should have been allowed.
    c.header('vary', 'origin')
    // The same comparison the beacon and the lead route make, in the same
    // function, so a route cannot drift into a rule of its own.
    const matched = matchSiteOrigin(c.req.header('origin'), options.siteOrigins ?? [])
    if (!matched) return false
    c.header('access-control-allow-origin', matched)
    return true
  }
  app.options('/v1/applications', (c) => {
    if (!origin(c)) return c.body(null, 403)
    c.header('access-control-allow-methods', 'POST, OPTIONS')
    c.header('access-control-allow-headers', 'content-type')
    return c.body(null, 204)
  })
  app.use('/v1/applications', async (c, next) => {
    if (!origin(c)) return c.json({ error: 'Use the application form on the official website.' }, 403)
    await next()
  })
  app.use('/v1/applications', bodyLimit({ maxSize: 32768, onError: (c) => c.json({ error: 'This application is too large. Shorten your answers.' }, 413) }))
  app.post('/v1/applications', async (c) => {
    let body: unknown
    try { body = await c.req.json() } catch { return c.json({ error: 'Send a JSON application.' }, 400) }
    const parsed = applicationInput.safeParse(body)
    if (!parsed.success) return c.json({ error: parsed.error.issues[0]?.message ?? 'Check your application fields.' }, 400)
    try {
      return c.json(await recordApplication(options.pool, options.clock, parsed.data), 201)
    } catch {
      // Never log an applicant's payload or a database error containing it.
      return c.json({ error: 'We could not record your application. Please try again.' }, 503)
    }
  })
}
