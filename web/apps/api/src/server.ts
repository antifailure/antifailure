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
  suspensionReason,
  IngestRefused,
  type IncomingEvent,
} from './ingest.ts'
import type { GitHubClient } from './auth/github.ts'
import { beginSignIn, completeSignIn, SignInError, safeRedirect } from './auth/signin.ts'
import {
  beginEmailSignIn,
  redeemEmailSignIn,
  EmailSignInError,
  type EmailSignInConfig,
} from './auth/email.ts'
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
import { limitFor, bucketFor, type EndpointLimit } from './limits.ts'

export interface ServerOptions {
  pool: Pool
  github: GitHubClient
  clock?: Clock
  /** Set false only for local HTTP development. The cookie is Secure otherwise. */
  secureCookies?: boolean
  /** Where the browser lands after signing in. */
  appBaseUrl?: string
  /** Signing in with a link, for the deployments GitHub cannot reach. Absent
   *  turns the two routes off entirely rather than leaving them answering with
   *  an error, so an installation that has not configured mail does not expose
   *  a sign-in path that cannot work. */
  emailSignIn?: EmailSignInConfig
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
  // Rate limiting, before anything else does work.
  //
  // Applied from the declared list rather than per route, so that an endpoint
  // added without a limit is refused outright instead of being unbounded. That
  // is the safe direction: an endpoint nobody remembered to limit is exactly
  // the one that has never been load tested, and answering 500 to it is a bug
  // report while leaving it open is an outage.
  // -------------------------------------------------------------------------
  const buckets = new Map<string, RateLimiter>()
  function limiterFor(limit: EndpointLimit): RateLimiter {
    const signature = `${limit.key}:${limit.rate}:${limit.burst}`
    let limiter = buckets.get(signature)
    if (!limiter) {
      limiter = new RateLimiter(clock, { rate: limit.rate, burst: limit.burst })
      buckets.set(signature, limiter)
    }
    return limiter
  }

  app.use('*', async (c, next) => {
    const limit = limitFor(c.req.method, new URL(c.req.url).pathname)
    if (!limit) {
      // Deliberately loud. The alternative is a quiet default, and a quiet
      // default means nobody ever notices the endpoint is unbounded.
      return c.json(
        {
          error:
            'This endpoint has no declared rate limit, so the server refuses to serve it. ' +
            'Add it to ENDPOINT_LIMITS with the reason for the number.',
        },
        500,
      )
    }

    const auth = c.req.header('authorization') ?? ''
    const verdict = limiterFor(limit).take(
      bucketFor(limit, {
        ip: clientIP(c.req.header('x-forwarded-for')),
        token: auth.startsWith('Bearer ') ? auth.slice(7, 39) : null,
        // The organization is not known before the session resolves, so the
        // org-keyed limits fall back to the address for an unauthenticated
        // request. That is the conservative direction: it bounds somebody with
        // no session rather than letting them share one anonymous bucket.
        org: null,
      }),
    )
    if (!verdict.allowed) {
      c.header('retry-after', String(verdict.retryAfterSeconds))
      return c.json(
        { error: 'Too many requests.', retryAfterSeconds: verdict.retryAfterSeconds },
        429,
      )
    }
    return next()
  })

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
        ip: clientAddress(c.req.header('x-forwarded-for')),
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
      orgSlug: session.orgSlug,
      role: session.role,
      // Handed to the page so it can send it back on mutations. Safe to expose:
      // it is derived from the session secret and reveals nothing about it.
      csrfToken: session.csrfToken,
    })
  })

  // -------------------------------------------------------------------------
  // Signing in with a link
  //
  // Registered only when mail is configured. A route that exists and always
  // answers "not available" is a sign-in method users try and support has to
  // explain; a route that is not there is a form the page does not render.
  // -------------------------------------------------------------------------

  if (options.emailSignIn) {
    const emailSignIn = options.emailSignIn

    app.post('/auth/email', async (c) => {
      const limited = authLimiter.take(
        clientKey(c.req.header('x-forwarded-for'), c.req.header('user-agent')),
      )
      if (!limited.allowed) return tooMany(c, limited.retryAfterSeconds)

      const body = await readEmailForm(c.req.raw)
      // One answer for every input: an address with an account, an address
      // without one, and something that is not an address. Anything else turns
      // this form into a way to ask whether somebody works here.
      const answer = { sent: true } as const
      if (!body.email) return c.json(answer, 200)

      let send: (() => Promise<void>) | null = null
      try {
        ;({ send } = await beginEmailSignIn(options.pool, clock, emailSignIn, {
          email: body.email,
          redirectTo: body.redirectTo,
          ip: clientAddress(c.req.header('x-forwarded-for')) ?? null,
          userAgent: c.req.header('user-agent') ?? null,
        }))
      } catch (err) {
        // A database failure here is ours, and it is the one case worth
        // answering differently, because telling somebody their link is on the
        // way when it is not is worse than telling them to try again.
        console.error('email sign-in', err)
        return c.json({ error: 'Could not send a link just now. Try again.' }, 503)
      }

      // Started, not awaited. The response must not take longer for an address
      // that has an account than for one that does not, and awaiting a mail
      // provider is exactly that difference. A failure is logged; the caller
      // was already told what to do next and retrying is the same action.
      if (send) {
        void send().catch((err: unknown) => {
          console.error('email sign-in: the link could not be sent', err)
        })
      }
      return c.json(answer, 200)
    })

    app.get('/auth/email/callback', async (c) => {
      const limited = authLimiter.take(
        clientKey(c.req.header('x-forwarded-for'), c.req.header('user-agent')),
      )
      if (!limited.allowed) return tooMany(c, limited.retryAfterSeconds)

      const token = c.req.query('token')
      if (!token) {
        return c.json({ error: 'This sign-in link is no longer valid. Ask for another one.' }, 400)
      }

      try {
        const result = await redeemEmailSignIn(options.pool, clock, token)
        // Rotation, for the same reason the OAuth callback rotates: a cookie
        // planted before sign-in must not ride the login that follows it.
        const existing = readCookie(c.req.header('cookie'), SESSION_COOKIE)
        const issued = await issueSession(options.pool, clock, {
          userId: result.userId,
          orgId: result.orgId,
          ip: clientAddress(c.req.header('x-forwarded-for')),
          userAgent: c.req.header('user-agent') ?? undefined,
          replacing: existing ?? undefined,
        })
        c.header('set-cookie', sessionCookie(issued.token, issued.expiresAt, secure))
        const base = options.appBaseUrl ?? '/'
        const target = result.redirectTo ?? '/'
        return c.redirect(new URL(target, base.endsWith('/') ? base : `${base}/`).toString(), 302)
      } catch (err) {
        if (err instanceof EmailSignInError) return c.json({ error: err.message }, 400)
        throw err
      }
    })
  }

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
    const suspended = await suspensionReason(options.pool, engine.orgId)
    if (suspended !== null) {
      // A different answer from an invalid token, and deliberately so. The
      // token is fine and the organization is stopped, and somebody debugging
      // at two in the morning needs to know which.
      //
      // Events are refused rather than silently dropped, so the engine keeps
      // them buffered and sends them when the suspension lifts. Accepting and
      // discarding them would lose exactly the record of what happened during
      // the incident the suspension was for.
      return c.json(
        {
          error: `This organization is suspended: ${suspended}`,
          retryAfterSeconds: 300,
        },
        403,
      )
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
    // Reading is deliberately still permitted while suspended. A suspension
    // stops new work; taking away the ability to see what is already running is
    // the opposite of what an incident needs.

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

/**
 * The address and return path out of a sign-in request.
 *
 * Both shapes are accepted because both are real. The application posts JSON;
 * a page with no JavaScript posts a form, and a sign-in that stops working
 * when a script fails to load is a sign-in that locks people out on exactly
 * the days they most need to get in.
 */
async function readEmailForm(
  req: Request,
): Promise<{ email: string | null; redirectTo: string | null }> {
  const type = req.headers.get('content-type') ?? ''
  try {
    if (type.includes('application/json')) {
      const body = (await req.json()) as { email?: unknown; redirect_to?: unknown }
      return {
        email: typeof body.email === 'string' ? body.email : null,
        redirectTo: typeof body.redirect_to === 'string' ? body.redirect_to : null,
      }
    }
    const form = await req.formData()
    const email = form.get('email')
    const redirect = form.get('redirect_to')
    return {
      email: typeof email === 'string' ? email : null,
      redirectTo: typeof redirect === 'string' ? redirect : null,
    }
  } catch {
    // A body that does not parse is the same as no address: answered the same
    // way as everything else, because a parse error is also a probe.
    return { email: null, redirectTo: null }
  }
}

function clientKey(forwardedFor: string | undefined, userAgent: string | undefined): string {
  return `${clientIP(forwardedFor)}|${(userAgent ?? '').slice(0, 64)}`
}

// The first entry in X-Forwarded-For is the client as the closest trusted proxy
// saw it. Later entries were supplied by the caller and must never be used: a
// limiter keyed on an attacker-chosen value is a limiter with unlimited
// buckets.
function clientIP(forwardedFor: string | undefined): string {
  return (forwardedFor ?? '').split(',')[0]?.trim() || 'unknown'
}

/**
 * The caller's address, in a form the database will accept, or null.
 *
 * Not the same function as clientIP, and the difference is the whole point.
 * clientIP produces a rate-limit bucket key, where "unknown" is a perfectly
 * good key and every request without a header sharing one bucket is the
 * intended behaviour. This produces a value for an `inet` column, where
 * "unknown" is a type error that fails the whole statement.
 *
 * Two ways that bites, both found by putting a real Postgres behind this:
 * a direct request has no `x-forwarded-for` at all, and a request through two
 * proxies has "1.2.3.4, 5.6.7.8", which is a list rather than an address. The
 * first entry is the client; the rest are the proxies that forwarded it. A
 * value that is neither is recorded as nothing, because a sign-in that fails
 * because the audit field would not parse is the wrong trade in every
 * direction.
 */
function clientAddress(forwardedFor: string | undefined): string | undefined {
  const first = (forwardedFor ?? '').split(',')[0]?.trim()
  if (!first) return undefined
  // A bracketed IPv6 literal with a port, which is what some proxies send.
  const unbracketed = /^\[(.+)\](?::\d+)?$/.exec(first)?.[1] ?? first
  // An IPv4 address with a port, likewise. IPv6 is left alone here: it is
  // full of colons, so stripping at the last one would truncate an address.
  const withoutPort = /^(\d{1,3}(?:\.\d{1,3}){3}):\d+$/.exec(unbracketed)?.[1] ?? unbracketed
  return looksLikeAddress(withoutPort) ? withoutPort : undefined
}

function looksLikeAddress(value: string): boolean {
  if (/^\d{1,3}(\.\d{1,3}){3}$/.test(value)) {
    return value.split('.').every((octet) => Number(octet) <= 255)
  }
  // Deliberately shape rather than grammar: Postgres does the real parsing,
  // and this only has to keep a header value that is plainly not an address
  // out of the statement.
  return /^[0-9a-fA-F:]+$/.test(value) && value.includes(':')
}

function tooMany(c: { header: (k: string, v: string) => void; json: (b: unknown, s: 429) => Response }, seconds: number) {
  c.header('retry-after', String(seconds))
  return c.json({ error: 'Too many attempts. Try again shortly.', retryAfterSeconds: seconds }, 429)
}

// Re-exported so that an edition built on top of this can import the permission
// model from one place. The types are the contract between the two, and a
// second copy of them is a second thing that drifts.
export {
  PERMISSIONS,
  ROLES,
  ROLE_PERMISSIONS,
  PERMISSION_DESCRIPTIONS,
  roleHas,
  rolesWith,
  permits,
  setPermissionResolver,
  hasPermissionResolver,
  type Permission,
  type Role,
  type PermissionRequest,
  type PermissionResolver,
} from './permissions.ts'
