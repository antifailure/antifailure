// The HTTP surface.
//
// Three kinds of route, and the difference between them is who authenticates.
//
// /trpc/* is the application's own API, authenticated by a session cookie and
// guarded by the permission on each procedure.
//
// /v1/events is the ingestion path, authenticated by a per-engine bearer token.
// It is separate because the callers are different: engines on machines the
// control plane cannot reach, sending in bursts, retrying blindly. Putting it
// behind the same session middleware would mean an engine needs a cookie.
//
// /auth/* is the sign-in exchange, which by definition has no session yet.

import { Hono } from 'hono'
import { sql as rawSql } from 'drizzle-orm'
import { trpcServer } from '@hono/trpc-server'
import type { Pool } from '@antifailure/db'
import { appRouter } from './routers/index.ts'
import type { Context as TrpcContext, Actor } from './trpc.ts'
import type { Clock } from './clock.ts'
import { systemClock } from './clock.ts'
import { RateLimiter } from './ratelimit.ts'
import {
  authenticateEngine,
  ingest,
  IngestRefused,
  type IncomingEvent,
} from './ingest.ts'
import type { GitHubClient } from './auth/github.ts'
import { beginSignIn, completeSignIn, SignInError, safeRedirect } from './auth/signin.ts'
import {
  CSRF_HEADER,
  SESSION_COOKIE,
  clearedCookie,
  csrfMatches,
  issueSession,
  readCookie,
  resolveSession,
  revokeSession,
  sessionCookie,
} from './auth/session.ts'
import { openApiDocument } from './openapi.ts'

export interface ServerOptions {
  pool: Pool
  github: GitHubClient
  clock?: Clock
  /** Set false only for local HTTP development. The cookie is Secure otherwise. */
  secureCookies?: boolean
  /** Where the browser lands after signing in. */
  appBaseUrl?: string
  ingestLimit?: { rate: number; burst: number }
  authLimit?: { rate: number; burst: number }
}

export function createServer(options: ServerOptions) {
  const clock = options.clock ?? systemClock
  const secure = options.secureCookies ?? true
  const app = new Hono()

  // Two limiters with different shapes. Ingestion is high volume from few
  // callers, so the burst is large. Authentication is low volume from many
  // callers and is the one worth throttling hard, because it is where a
  // stolen-cookie or code-guessing attempt shows up.
  const ingestLimiter = new RateLimiter(clock, options.ingestLimit ?? { rate: 200, burst: 2000 })
  const authLimiter = new RateLimiter(clock, options.authLimit ?? { rate: 1, burst: 20 })

  // -------------------------------------------------------------------------
  // Headers every response carries.
  // -------------------------------------------------------------------------
  app.use('*', async (c, next) => {
    await next()
    c.header('x-content-type-options', 'nosniff')
    c.header('referrer-policy', 'strict-origin-when-cross-origin')
    // The API returns JSON and never renders anything, so the strictest
    // possible policy is also the correct one.
    c.header('content-security-policy', "default-src 'none'; frame-ancestors 'none'")
    c.header('x-frame-options', 'DENY')
  })

  app.get('/health', (c) => c.json({ ok: true }))
  app.get('/openapi.json', (c) => c.json(openApiDocument()))

  // -------------------------------------------------------------------------
  // Sign in
  // -------------------------------------------------------------------------

  app.get('/auth/github', async (c) => {
    const limited = authLimiter.take(clientKey(c.req.header('x-forwarded-for'), c.req.header('user-agent')))
    if (!limited.allowed) return tooMany(c, limited.retryAfterSeconds)

    const { url } = await beginSignIn(
      options.pool,
      clock,
      options.github,
      c.req.query('redirect_to'),
    )
    return c.redirect(url, 302)
  })

  app.get('/auth/github/callback', async (c) => {
    const limited = authLimiter.take(clientKey(c.req.header('x-forwarded-for'), c.req.header('user-agent')))
    if (!limited.allowed) return tooMany(c, limited.retryAfterSeconds)

    const code = c.req.query('code')
    const state = c.req.query('state')
    if (!code || !state) {
      return c.json({ error: 'This sign-in link is no longer valid. Start again.' }, 400)
    }

    try {
      const result = await completeSignIn(options.pool, clock, options.github, { code, state })
      // Rotation. Any session the browser already holds is destroyed, so a
      // cookie planted before sign-in cannot ride the login that follows it.
      const existing = readCookie(c.req.header('cookie'), SESSION_COOKIE)
      const issued = await issueSession(options.pool, clock, {
        userId: result.userId,
        orgId: result.orgId,
        ip: c.req.header('x-forwarded-for') ?? undefined,
        userAgent: c.req.header('user-agent') ?? undefined,
        replacing: existing ?? undefined,
      })

      c.header('set-cookie', sessionCookie(issued.token, issued.expiresAt, secure))
      const base = options.appBaseUrl ?? '/'
      const target = safeRedirect(result.redirectTo) ?? '/'
      return c.redirect(new URL(target, base.endsWith('/') ? base : `${base}/`).toString(), 302)
    } catch (err) {
      if (err instanceof SignInError) return c.json({ error: err.message }, 400)
      return c.json({ error: 'GitHub refused the sign in. Try again.' }, 400)
    }
  })

  app.post('/auth/signout', async (c) => {
    const token = readCookie(c.req.header('cookie'), SESSION_COOKIE)
    if (token) await revokeSession(options.pool, token)
    c.header('set-cookie', clearedCookie(secure))
    return c.json({ signedOut: true })
  })

  app.get('/auth/session', async (c) => {
    const token = readCookie(c.req.header('cookie'), SESSION_COOKIE)
    if (!token) return c.json({ signedIn: false }, 200)
    const session = await resolveSession(options.pool, clock, token)
    if (!session) return c.json({ signedIn: false }, 200)
    return c.json({
      signedIn: true,
      label: session.label,
      orgId: session.orgId,
      role: session.role,
      // Handed to the page so it can send it back on mutations. Safe to expose:
      // it is derived from the session secret and reveals nothing about it.
      csrfToken: session.csrfToken,
    })
  })

  // -------------------------------------------------------------------------
  // Ingestion
  // -------------------------------------------------------------------------

  async function engineFrom(header: string | undefined) {
    const auth = header ?? ''
    const token = auth.startsWith('Bearer ') ? auth.slice(7).trim() : ''
    return authenticateEngine(options.pool, clock, token)
  }

  app.post('/v1/events', async (c) => {
    const engine = await engineFrom(c.req.header('authorization'))
    if (!engine) {
      // No detail about which part failed. An engine with a revoked token and
      // an engine with a made-up token get the same answer.
      return c.json({ error: 'This token is not valid.' }, 401)
    }

    let body: unknown
    try {
      body = await c.req.json()
    } catch {
      return c.json({ error: 'The body is not JSON.' }, 400)
    }
    const events = Array.isArray((body as { events?: unknown })?.events)
      ? ((body as { events: IncomingEvent[] }).events)
      : null
    if (!events) return c.json({ error: 'The body needs an events array.' }, 400)

    try {
      const result = await ingest(options.pool, clock, ingestLimiter, engine, events)
      // 207 when some were rejected, so a caller that only checks the status
      // still learns that the batch was not wholly accepted.
      return c.json(result, result.rejected > 0 ? 207 : 202)
    } catch (err) {
      if (err instanceof IngestRefused) {
        if (err.retryAfterSeconds) c.header('retry-after', String(err.retryAfterSeconds))
        return c.json({ error: err.message, retryAfterSeconds: err.retryAfterSeconds }, err.status as 429)
      }
      throw err
    }
  })

  // An engine holding a token can read back what the control plane recorded for
  // one of its environments. This exists because the application API is
  // authenticated by a session cookie, and an engine on a CI runner has no
  // browser, no cookie, and no way to obtain one. Without it a token could
  // write and never read, which is exactly the shape of a feature that looks
  // finished and does nothing.
  //
  // Scoped to the token's organization by the same tenant transaction every
  // other read uses, so a token cannot be pointed at somebody else's
  // environment by changing the path.
  app.get('/v1/environments/:envId', async (c) => {
    const engine = await engineFrom(c.req.header('authorization'))
    if (!engine) return c.json({ error: 'This token is not valid.' }, 401)

    const envId = c.req.param('envId')
    const rows = await options.pool.withTenant({ orgId: engine.orgId }, async (db) =>
      db.execute<Record<string, unknown>>(rawSql`
        SELECT e.env_id, r.full_name AS repository, e.branch, e.pull_request,
               e.state::text AS state, e.preview_url, e.runtime, e.golden_version,
               -- Formatted in SQL rather than left to the driver. A raw query
               -- returns whatever text Postgres emits, which is
               -- "2026-08-26 07:31:48.683911+00": valid, and not RFC 3339, so
               -- every strict parser on the other side rejects it. The wire
               -- format is part of the contract, so it is stated here.
               to_char(e.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS created_at,
               to_char(e.updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS updated_at
        FROM environments e JOIN repositories r ON r.id = e.repository_id
        WHERE e.env_id = ${envId}`),
    )
    if (rows.length === 0) {
      // The same answer whether it belongs to another organization or does not
      // exist. Telling them apart turns this into a way to ask whether another
      // organization has an environment by that name.
      return c.json({ error: `No environment named ${envId} in this organization.` }, 404)
    }
    return c.json(rows[0])
  })

  // -------------------------------------------------------------------------
  // The application API
  // -------------------------------------------------------------------------

  app.use('/trpc/*', async (c, next) => {
    // Cross-site request forgery. The cookie is SameSite=Lax, which stops the
    // usual case, and this stops the rest: a top-level form POST, and a
    // subdomain that an attacker controls, which is inside SameSite.
    //
    // Only mutations are checked. A query cannot change anything, and requiring
    // a token on reads would mean the page cannot render before it has one.
    if (c.req.method !== 'GET' && c.req.method !== 'HEAD') {
      const token = readCookie(c.req.header('cookie'), SESSION_COOKIE)
      if (token) {
        const session = await resolveSession(options.pool, clock, token)
        if (session && !csrfMatches(token, c.req.header(CSRF_HEADER))) {
          return c.json(
            { error: `This request needs the ${CSRF_HEADER} header from GET /auth/session.` },
            403,
          )
        }
      }
    }
    return next()
  })

  app.use(
    '/trpc/*',
    trpcServer({
      router: appRouter,
      endpoint: '/trpc',
      createContext: async (_opts, c) => {
        const token = readCookie(c.req.header('cookie'), SESSION_COOKIE)
        let actor: Actor | null = null
        if (token) {
          const session = await resolveSession(options.pool, clock, token)
          // A session with no organization, or one whose membership has been
          // removed, is signed in and has no tenant. It is not an actor: every
          // procedure needs an organization to scope its transaction to, and
          // guessing one is how somebody ends up in the wrong tenant.
          if (session?.orgId && session.role) {
            actor = {
              userId: session.userId,
              label: session.label,
              orgId: session.orgId,
              role: session.role,
            }
          }
        }
        const context: TrpcContext = {
          pool: options.pool,
          clock,
          actor,
          origin: 'web',
          ip: c.req.header('x-forwarded-for') ?? undefined,
          userAgent: c.req.header('user-agent') ?? undefined,
        }
        // tRPC's fetch adapter types the context as a plain record, and the
        // context here is an interface. The cast is confined to this one line
        // so that every procedure still sees the precise type.
        return context as unknown as Record<string, unknown>
      },
    }),
  )

  return { app, ingestLimiter, authLimiter }
}

function clientKey(forwardedFor: string | undefined, userAgent: string | undefined): string {
  // The first entry in X-Forwarded-For is the client as the closest trusted
  // proxy saw it. Later entries are attacker-controlled and must not be used.
  const ip = (forwardedFor ?? '').split(',')[0]?.trim() || 'unknown'
  return `${ip}|${(userAgent ?? '').slice(0, 64)}`
}

function tooMany(c: { header: (k: string, v: string) => void; json: (b: unknown, s: 429) => Response }, seconds: number) {
  c.header('retry-after', String(seconds))
  return c.json({ error: 'Too many attempts. Try again shortly.', retryAfterSeconds: seconds }, 429)
}
