import type { Hono, Context, Env } from 'hono'
import { bodyLimit } from 'hono/body-limit'
import type { Pool } from '@antifailure/db'
import type { Clock } from '../clock.ts'
import { applicationInput, recordApplication } from './applications.ts'

export function mountApplicationRoutes<E extends Env>(app: Hono<E>, options: { pool: Pool; clock: Clock; siteOrigin?: string }) {
  function origin(c: Context<E>) {
    c.header('cache-control', 'no-store')
    c.header('vary', 'origin')
    if (!options.siteOrigin || c.req.header('origin') !== options.siteOrigin) return false
    c.header('access-control-allow-origin', options.siteOrigin)
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
