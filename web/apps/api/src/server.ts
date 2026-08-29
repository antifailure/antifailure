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
import type { Context } from 'hono'
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
import {
  beginSignIn,
  completeSignIn,
  SignInError,
  safeRedirect,
  type SignInAllowlist,
} from './auth/signin.ts'
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
import {
  approveDeviceCode,
  denyDeviceCode,
  describePending,
  DeviceError,
  identify,
  redeemDeviceCode,
  requestDeviceCode,
  revokeCliToken,
} from './auth/device.ts'
import { mountConsole } from './console/index.ts'
import { PROVIDERS, type Provider } from './providers/seal.ts'
import { verifySignature } from './github/app.ts'
import { forward, ProxyError } from './providers/proxy.ts'
import { PricingError, type Price } from './providers/pricing.ts'
import { handleDelivery } from './github/webhook.ts'
import {
  listBudgets,
  listKeys,
  MAY_MANAGE_KEYS,
  ProviderKeyError,
  revokeKey,
  saveKey,
  setBudget,
} from './providers/store.ts'
import { openApiDocument } from './openapi.ts'
import { decideSignIn, extensionRoutes } from './extensions.ts'
import { limitFor, bucketFor, ENDPOINT_LIMITS, type EndpointLimit } from './limits.ts'
import { createMetrics, routeLabel, statusClass, type ControlPlaneMetrics } from './metrics.ts'

export interface ServerOptions {
  pool: Pool
  github: GitHubClient
  clock?: Clock
  /** Set false only for local HTTP development. The cookie is Secure otherwise. */
  secureCookies?: boolean
  /** Where the browser lands after signing in. */
  appBaseUrl?: string
  /** Who may sign in at all. Null is open, which is the self-hosted default.
   *  See parseAllowlist: an empty list is closed to everyone, not open. */
  signInAllowlist?: SignInAllowlist
  /** Set false to serve the API alone, without the console's pages. */
  console?: boolean
  /** The secret that seals provider keys. Null means keys cannot be stored,
   *  which the console says out loud rather than failing on submit. */
  sealingKey?: Buffer | null
  /** The GitHub App's webhook secret. Null means no App is configured, and the
   *  webhook endpoint refuses every delivery rather than accepting unsigned
   *  ones. */
  githubWebhookSecret?: string | null
  /** What each model costs, for charging a budget. */
  modelPrices?: Record<string, Price>
  /** Where the providers live. Overridden in tests so nothing reaches a real
   *  one; unset means the real addresses. */
  providerBases?: Record<string, string>
  ingestLimit?: { rate: number; burst: number }
  authLimit?: { rate: number; burst: number }
  /** The build, reported as a label on af_control_plane_info so a graph can
   *  say which version produced a number. */
  version?: string
  /** Supplied by a test that wants to read the counters. Otherwise each server
   *  gets its own, deliberately not module state: two servers in one process
   *  sharing counters means one test passes because of another. */
  metrics?: ControlPlaneMetrics
}

/**
 * What a token minted by `af login` may ever hold.
 *
 * A closed list rather than a free string. A terminal asks for scopes and the
 * request is intersected with this, so a client cannot invent a capability by
 * naming one, and adding a capability is a change to this line rather than a
 * change to whatever a caller happened to send.
 */
export const CLI_SCOPES: readonly string[] = ['environments.view', 'runs.view', 'events.write']

/**
 * Everything a CLI token may hold if somebody asks for it and approves it.
 *
 * Wider than the default on purpose, and the gap between the two lists is the
 * design. `af login` with no arguments gets CLI_SCOPES: read environments and
 * runs, write events, and nothing that can cost money or change a secret. A
 * terminal that needs to manage provider keys has to ask, and the person
 * approving sees exactly what was asked for on the screen where they approve.
 *
 * Note what is NOT here and never will be: a scope that reads a provider key
 * back. The CLI can store one, replace one, remove one and cap its spend, and
 * it cannot retrieve one. Storing a secret and reading a secret are different
 * capabilities, and a terminal has no reason for the second.
 */
export const GRANTABLE_SCOPES: readonly string[] = [
  ...CLI_SCOPES,
  'providers.view',
  'providers.write',
]


export function createServer(options: ServerOptions) {
  const clock = options.clock ?? systemClock
  const secure = options.secureCookies ?? true
  const metrics = options.metrics ?? createMetrics(options.version ?? 'dev')
  // Read once. It is the bounded set of label values, and reading it per
  // request would be the metrics endpoint doing work proportional to traffic.
  const declaredRoutes = Object.keys(ENDPOINT_LIMITS)
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

  // Counting comes before the rate limiter, so a request refused with 429 is
  // still counted. A metric that only sees the requests that got through
  // cannot tell an outage from a quiet afternoon, and it is exactly the
  // refusals an operator needs when a limit is set too low.
  app.use('*', async (c, next) => {
    const started = process.hrtime.bigint()
    // The label is the declared route key that matched, never the path. See
    // routeLabel: a path that matches GET /v1/environments/:envId is bounded
    // only if it is reported as that pattern, and the first version of this
    // reported the path, so every environment identifier anybody fetched became
    // its own series.
    const route = routeLabel(c.req.method, new URL(c.req.url).pathname, declaredRoutes)
    try {
      await next()
    } finally {
      const seconds = Number(process.hrtime.bigint() - started) / 1e9
      const status = c.res?.status ?? 500
      metrics.httpRequests.inc({ route, status_class: statusClass(status) })
      metrics.httpDuration.observe(seconds, { route })
      if (status === 429) metrics.rateLimited.inc({ route })
    }
  })

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
    c.header('x-frame-options', 'DENY')

    // The API's own policy, applied only where a route has not set its own.
    //
    // This used to be unconditional, with a comment explaining that the API
    // returns JSON and never renders anything. That stopped being true the day
    // the console was added, and because this middleware runs AFTER the route,
    // it overwrote the console's policy with one that has no style-src. The
    // effect on a real browser: `default-src 'none'` blocked the stylesheet,
    // and every console page rendered as unstyled text. It answered 200 with
    // the right HTML and the right headers by its own account, which is why
    // nothing caught it.
    //
    // Set rather than replaced, so a route that has thought about its own
    // policy keeps it, and one that has not still gets the strict default.
    if (!c.res.headers.get('content-security-policy')) {
      c.header('content-security-policy', "default-src 'none'; frame-ancestors 'none'")
    }
  })

  // Liveness. Deliberately a static literal that touches nothing: it answers
  // "is this process running", and it is not allowed to imply more.
  app.get('/health', (c) => c.json({ ok: true }))

  // Readiness, which is a different question, and the difference is not
  // academic.
  //
  // The first deploy of this application to Azure answered /health with 200 for
  // thirteen minutes while every endpoint that touched a table returned 500.
  // The schema had never applied, because the managed Postgres refused
  // CREATE EXTENSION pgcrypto, and nothing that was being monitored could tell.
  // A health check that cannot fail for the most common cause of an unusable
  // deploy is decoration.
  //
  // So this one takes a connection out of the pool the application actually
  // serves with and asks the database a question. It reports the build as well,
  // because the second thing a deploy gate needs to know, after "is it well",
  // is "is it the build I just deployed" -- otherwise a rollout that silently
  // did not happen passes every check.
  app.get('/readyz', async (c) => {
    const build = {
      version: process.env.AF_VERSION ?? 'dev',
      commit: process.env.AF_COMMIT ?? 'unknown',
    }
    try {
      await options.pool.withoutTenant(async (db) => {
        await db.execute(rawSql`SELECT 1`)
      })
    } catch (err) {
      // 503, not 500. This is "not ready to receive traffic", which is what a
      // load balancer and a deploy gate both act on, and it is what makes an
      // automatic rollback fire rather than a page at three in the morning.
      return c.json(
        {
          ready: false,
          ...build,
          // The message and not the stack. A readiness endpoint is unauthenticated.
          reason: err instanceof Error ? err.message : 'the database did not answer',
        },
        503,
      )
    }
    return c.json({ ready: true, ...build })
  })

  app.get('/openapi.json', (c) => c.json(openApiDocument()))

  // Prometheus scrapes this. It reads only counters this process kept itself
  // and touches no table, which is not laziness: tenancy here is row level
  // security, so an aggregate across every organization would need a role that
  // can read every organization's rows. Creating one in order to draw a graph
  // would put the strongest read in the system on the least important path and
  // leave it there being scraped every fifteen seconds forever.
  app.get('/metrics', (c) => {
    c.header('content-type', 'text/plain; version=0.0.4; charset=utf-8')
    return c.body(metrics.registry.render())
  })

  // Resolving the browser's session, for the routes that need one outside tRPC.
  async function sessionFrom(cookie: string | undefined) {
    const token = readCookie(cookie, SESSION_COOKIE)
    if (!token) return null
    return resolveSession(options.pool, clock, token)
  }

  // -------------------------------------------------------------------------
  // Routes another edition registered
  //
  // Mounted here, after the rate limiter and the headers and before anything
  // that authenticates, because an extension route is by definition one that
  // has its own idea of who the caller is: a single sign-on assertion arrives
  // with no cookie, and a provisioning request arrives with a bearer token this
  // server knows nothing about.
  //
  // What they do NOT get is a way around the two middlewares above. The limiter
  // has already run and found their declared limit through limitFor, and the
  // security headers are applied on the way out to every response including
  // these. An extension that wanted to serve HTML would find the content
  // security policy in its way, which is correct: nothing here renders.
  for (const route of extensionRoutes()) {
    app.on(route.method, route.path, (c) => route.handler(c))
  }

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
      const result = await completeSignIn(
        options.pool,
        clock,
        options.github,
        { code, state },
        options.signInAllowlist ?? null,
      )

      // Another edition may have an opinion about this sign-in: an
      // organization that requires single sign-on is one where arriving by
      // GitHub must not land in that tenant. The policy returns which
      // organization the session may be scoped to, and null means signed in
      // with no tenant, which is a state this server already handles.
      const decision = await decideSignIn({
        userId: result.userId,
        orgId: result.orgId,
        method: 'github',
      })

      // Rotation. Any session the browser already holds is destroyed, so a
      // cookie planted before sign-in cannot ride the login that follows it.
      const existing = readCookie(c.req.header('cookie'), SESSION_COOKIE)
      const issued = await issueSession(options.pool, clock, {
        userId: result.userId,
        orgId: decision.orgId,
        ip: c.req.header('x-forwarded-for') ?? undefined,
        userAgent: c.req.header('user-agent') ?? undefined,
        replacing: existing ?? undefined,
      })

      c.header('set-cookie', sessionCookie(issued.token, issued.expiresAt, secure))
      const base = options.appBaseUrl ?? '/'
      const target = safeRedirect(result.redirectTo) ?? '/'
      const landing = new URL(target, base.endsWith('/') ? base : `${base}/`)
      // Why they landed with no organization, when a policy said so. The note
      // is checked against a strict pattern before it reaches here, so it
      // cannot carry anything but a short identifier.
      if (decision.note) landing.searchParams.set('note', decision.note)
      return c.redirect(landing.toString(), 302)
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
  // Signing in a terminal
  //
  // Four endpoints, and which of them needs a session is the whole design. The
  // two the terminal calls have no session and cannot have one; the two the
  // browser calls require one, and the organization the terminal ends up in
  // comes from that session rather than from anything the terminal asked for.
  // -------------------------------------------------------------------------

  /** The terminal asks. No authentication: this is where a login begins. */
  app.post('/auth/device/code', async (c) => {
    let body: { clientLabel?: unknown; scopes?: unknown } = {}
    try {
      body = (await c.req.json()) as typeof body
    } catch {
      // A body is optional. A terminal that sends none gets the defaults.
    }
    const label =
      typeof body.clientLabel === 'string' && body.clientLabel.trim()
        ? body.clientLabel.trim()
        : 'a terminal'
    // Scopes are taken from the request and recorded, but never trusted to
    // widen anything: they are intersected with what a CLI token may ever hold,
    // so a terminal cannot ask for a capability that does not exist.
    //
    // Asking for nothing gets the default set, which is the read-mostly one. A
    // terminal that wants more names it, and the name reaches the approval
    // screen, so nobody grants provider-key management without seeing the words.
    const asked = Array.isArray(body.scopes) ? body.scopes.filter((s) => typeof s === 'string') : []
    const scopes = asked.length
      ? asked.filter((s) => GRANTABLE_SCOPES.includes(s))
      : [...CLI_SCOPES]
    if (asked.length && scopes.length === 0) {
      // Every scope asked for was refused. Issuing a code for a token that can
      // do nothing would produce a login that appears to work and then fails at
      // the first command, which is the worst place to learn about it.
      return c.json(
        {
          error: 'invalid_scope',
          error_description: `None of those scopes exist. Available: ${GRANTABLE_SCOPES.join(', ')}.`,
        },
        400,
      )
    }

    const origin = options.appBaseUrl ?? new URL(c.req.url).origin
    const issued = await requestDeviceCode(options.pool, clock, {
      clientLabel: label,
      scopes,
      baseUrl: origin,
    })
    return c.json({
      device_code: issued.deviceCode,
      user_code: issued.userCode,
      verification_uri: issued.verificationUri,
      verification_uri_complete: issued.verificationUriComplete,
      expires_in: issued.expiresIn,
      interval: issued.interval,
    })
  })

  /** The terminal collects. Still no session: it holds the device code. */
  app.post('/auth/device/token', async (c) => {
    let body: { device_code?: unknown } = {}
    try {
      body = (await c.req.json()) as typeof body
    } catch {
      return c.json({ error: 'invalid_request', error_description: 'The body is not JSON.' }, 400)
    }
    const deviceCode = typeof body.device_code === 'string' ? body.device_code : ''
    if (!deviceCode) {
      return c.json({ error: 'invalid_request', error_description: 'device_code is required.' }, 400)
    }

    try {
      const token = await redeemDeviceCode(options.pool, clock, deviceCode)
      return c.json({
        access_token: token.accessToken,
        token_type: token.tokenType,
        expires_in: token.expiresIn,
        scope: token.scopes.join(' '),
      })
    } catch (err) {
      if (err instanceof DeviceError) {
        // 400 with an error code, as RFC 8628 specifies. The CLI switches on
        // the code: authorization_pending and slow_down mean keep going, and
        // anything else means stop. A single generic failure would make a
        // client either poll forever or quit on the first tick.
        return c.json({ error: err.code, error_description: err.message }, 400)
      }
      throw err
    }
  })

  /** What the browser shows before somebody approves. Needs a session. */
  app.get('/auth/device/pending', async (c) => {
    const session = await sessionFrom(c.req.header('cookie'))
    if (!session) return c.json({ error: 'Sign in first.' }, 401)
    const pending = await describePending(options.pool, clock, c.req.query('code') ?? '')
    if (!pending) {
      return c.json({ error: 'That code is not valid any more. Run af login again.' }, 404)
    }
    return c.json({
      userCode: pending.userCode,
      clientLabel: pending.clientLabel,
      scopes: pending.scopes,
      expiresAt: pending.expiresAt.toISOString(),
      // Shown so the person approving knows which tenant they are handing over.
      organization: session.orgId,
    })
  })

  /** Approval. The tenant comes from the session, never from the request. */
  app.post('/auth/device/approve', async (c) => {
    const session = await sessionFrom(c.req.header('cookie'))
    if (!session) return c.json({ error: 'Sign in first.' }, 401)
    if (!session.orgId) {
      // Signed in with no organization. Approving would have to invent a
      // tenant, and inventing one is how a terminal ends up in the wrong
      // company's data.
      return c.json(
        { error: 'You are not a member of an organization yet, so there is nothing to grant.' },
        403,
      )
    }
    if (!csrfMatches(readCookie(c.req.header('cookie'), SESSION_COOKIE)!, c.req.header(CSRF_HEADER))) {
      return c.json({ error: `This request needs the ${CSRF_HEADER} header from GET /auth/session.` }, 403)
    }

    let body: { user_code?: unknown } = {}
    try {
      body = (await c.req.json()) as typeof body
    } catch {
      return c.json({ error: 'The body is not JSON.' }, 400)
    }
    try {
      await approveDeviceCode(options.pool, clock, {
        userCode: String(body.user_code ?? ''),
        userId: session.userId,
        orgId: session.orgId,
        actorLabel: session.label,
      })
      return c.json({ approved: true })
    } catch (err) {
      if (err instanceof DeviceError) return c.json({ error: err.message }, 400)
      throw err
    }
  })

  app.post('/auth/device/deny', async (c) => {
    const session = await sessionFrom(c.req.header('cookie'))
    if (!session) return c.json({ error: 'Sign in first.' }, 401)
    if (!csrfMatches(readCookie(c.req.header('cookie'), SESSION_COOKIE)!, c.req.header(CSRF_HEADER))) {
      return c.json({ error: `This request needs the ${CSRF_HEADER} header from GET /auth/session.` }, 403)
    }
    let body: { user_code?: unknown } = {}
    try {
      body = (await c.req.json()) as typeof body
    } catch {
      return c.json({ error: 'The body is not JSON.' }, 400)
    }
    try {
      await denyDeviceCode(options.pool, clock, String(body.user_code ?? ''))
      return c.json({ denied: true })
    } catch (err) {
      if (err instanceof DeviceError) return c.json({ error: err.message }, 400)
      throw err
    }
  })

  // -------------------------------------------------------------------------
  // What a CLI token is
  // -------------------------------------------------------------------------

  /** `af whoami`. Answers for a CLI token and for nothing else. */
  app.get('/v1/whoami', async (c) => {
    const auth = c.req.header('authorization') ?? ''
    const token = auth.startsWith('Bearer ') ? auth.slice(7).trim() : ''
    const who = await identify(options.pool, clock, token)
    if (!who) {
      // The same answer for a revoked token, an expired one, a made-up one, and
      // an engine token. An engine token is deliberately not an identity: a
      // machine is not a person, and answering with one would put a machine's
      // actions in somebody's name.
      return c.json({ error: 'This token is not valid.' }, 401)
    }
    return c.json({
      login: who.login,
      name: who.name,
      organization: who.orgSlug,
      role: who.role,
      scopes: who.scopes,
      tokenPrefix: who.tokenPrefix,
      expiresAt: who.expiresAt ? who.expiresAt.toISOString() : null,
    })
  })

  /** `af logout`, server side. The token stops working everywhere, not just here. */
  app.post('/v1/logout', async (c) => {
    const auth = c.req.header('authorization') ?? ''
    const token = auth.startsWith('Bearer ') ? auth.slice(7).trim() : ''
    if (!token) return c.json({ error: 'This token is not valid.' }, 401)
    const { revoked } = await revokeCliToken(options.pool, clock, token)
    // 200 either way. Signing out has to be idempotent: a client retrying after
    // a timeout must not be told its second attempt failed, and a token that
    // was already revoked is in exactly the state the caller asked for.
    return c.json({ revoked })
  })

  // -------------------------------------------------------------------------
  // Provider keys, from a terminal
  // -------------------------------------------------------------------------
  //
  // The same capability the console has, reachable by `af provider`. It exists
  // because the console is not always where the person is: a key gets rotated
  // from a laptop at the end of an incident, and telling somebody to open a
  // browser to do it is how a rotation gets postponed.
  //
  // Three gates, in this order, and each one refuses for a different reason:
  //
  //   1. The token is a CLI token that is live and belongs to a member.
  //   2. It carries the scope. A token minted by a plain `af login` does not,
  //      so the capability is not silently attached to every terminal that
  //      ever signed in -- somebody had to ask for it and approve it.
  //   3. The person is an owner or an admin. Scope says what the TOKEN may do;
  //      role says what the PERSON may do, and a member who cannot change a
  //      key in the console must not be able to change one from a shell.
  //
  // The plaintext key travels in one direction only. There is no route here
  // that returns a key, and there is no scope that would grant one.

  interface CliCaller {
    orgId: string
    userId: string
    label: string
  }

  /** Applies the three gates and answers with the reason it refused. */
  async function providerCaller(
    c: Context,
    need: 'providers.view' | 'providers.write',
  ): Promise<CliCaller | Response> {
    const header = c.req.header('authorization') ?? ''
    const token = header.startsWith('Bearer ') ? header.slice(7).trim() : ''
    const who = await identify(options.pool, clock, token)
    if (!who) return c.json({ error: 'This token is not valid.' }, 401)

    if (!who.scopes.includes(need)) {
      // Named in the message, because the fix is a specific command and a
      // caller who is told only "forbidden" will go looking for a role problem.
      return c.json(
        {
          error:
            `This token does not carry ${need}. Run: af login --scope ${need}` +
            ` -- and approve it in the browser.`,
          scopes: who.scopes,
        },
        403,
      )
    }
    if (need === 'providers.write' && !MAY_MANAGE_KEYS.has(who.role)) {
      return c.json(
        { error: `Changing a provider key needs owner or admin. You are ${who.role}.` },
        403,
      )
    }
    return { orgId: who.orgId, userId: who.userId, label: who.login }
  }

  function isResponse(v: CliCaller | Response): v is Response {
    return v instanceof Response
  }

  /** The provider from the path, or null. Never trusted into a query unchecked. */
  function providerParam(raw: string): Provider | null {
    return (PROVIDERS as string[]).includes(raw) ? (raw as Provider) : null
  }

  app.get('/v1/providers', async (c) => {
    const caller = await providerCaller(c, 'providers.view')
    if (isResponse(caller)) return caller
    const [keys, budgets] = await Promise.all([
      listKeys(options.pool, caller.orgId),
      listBudgets(options.pool, clock, caller.orgId),
    ])
    return c.json({
      // Whether a key CAN be stored at all. Reported rather than discovered on
      // a failed write, so `af provider list` on an installation with no
      // sealing secret says so instead of looking merely empty.
      sealing: Boolean(options.sealingKey),
      keys: keys.map((k) => ({
        provider: k.provider,
        last4: k.last4,
        fingerprint: k.fingerprint,
        createdAt: k.createdAt.toISOString(),
        rotatedAt: k.rotatedAt ? k.rotatedAt.toISOString() : null,
      })),
      budgets: budgets.map((b) => ({
        provider: b.provider,
        period: b.period,
        capUsd: b.capUsd,
        spentUsd: b.spentUsd,
        remainingUsd: b.remainingUsd,
      })),
    })
  })

  app.put('/v1/providers/:provider', async (c) => {
    const caller = await providerCaller(c, 'providers.write')
    if (isResponse(caller)) return caller
    const provider = providerParam(c.req.param('provider'))
    if (!provider) {
      return c.json({ error: `Unknown provider. Known: ${PROVIDERS.join(', ')}.` }, 400)
    }
    if (!options.sealingKey) {
      return c.json(
        {
          error:
            'This control plane has no sealing secret, so a key cannot be stored. ' +
            'Set AF_PROVIDER_KEY_SECRET and restart it.',
        },
        503,
      )
    }
    let body: { key?: unknown } = {}
    try {
      body = (await c.req.json()) as typeof body
    } catch {
      return c.json({ error: 'The body is not JSON.' }, 400)
    }
    const key = typeof body.key === 'string' ? body.key : ''
    if (!key.trim()) return c.json({ error: 'The body needs a key.' }, 400)

    try {
      const result = await saveKey(options.pool, clock, options.sealingKey, {
        orgId: caller.orgId,
        provider,
        key,
        actorUserId: caller.userId,
        actorLabel: caller.label,
        origin: 'cli',
      })
      return c.json({
        provider,
        last4: result.stored.last4,
        fingerprint: result.stored.fingerprint,
        replaced: result.replaced,
        // Said back rather than swallowed. Pasting the key that is already
        // there is the mistake somebody makes at the moment they believe they
        // have just rotated it.
        sameAsBefore: result.sameAsBefore,
      })
    } catch (err) {
      // The message is the complaint about the SHAPE of the key -- that it
      // starts with the other provider's prefix, that a whole export line was
      // pasted. It never contains the key.
      if (err instanceof ProviderKeyError) return c.json({ error: err.message }, 400)
      throw err
    }
  })

  app.delete('/v1/providers/:provider', async (c) => {
    const caller = await providerCaller(c, 'providers.write')
    if (isResponse(caller)) return caller
    const provider = providerParam(c.req.param('provider'))
    if (!provider) {
      return c.json({ error: `Unknown provider. Known: ${PROVIDERS.join(', ')}.` }, 400)
    }
    const { revoked } = await revokeKey(options.pool, clock, {
      orgId: caller.orgId,
      provider,
      actorLabel: caller.label,
      actorUserId: caller.userId,
      origin: 'cli',
    })
    // 200 whether or not there was one. Removing a key has to be idempotent:
    // a retry after a timeout must not report failure for reaching the state
    // the caller asked for.
    return c.json({ provider, revoked })
  })

  app.put('/v1/providers/:provider/budget', async (c) => {
    const caller = await providerCaller(c, 'providers.write')
    if (isResponse(caller)) return caller
    const provider = providerParam(c.req.param('provider'))
    if (!provider) {
      return c.json({ error: `Unknown provider. Known: ${PROVIDERS.join(', ')}.` }, 400)
    }
    let body: { capUsd?: unknown } = {}
    try {
      body = (await c.req.json()) as typeof body
    } catch {
      return c.json({ error: 'The body is not JSON.' }, 400)
    }
    // Strict, and deliberately not `Number(body.capUsd)`.
    //
    // Number(null) is 0 and Number('') is 0, so a coercing read turns a field
    // a caller forgot to fill -- an unset shell variable interpolated into a
    // JSON body -- into a cap of zero dollars. Zero is a legitimate cap, which
    // is what makes this dangerous: nothing looks wrong until every run refuses
    // with "the budget is spent". A cap of zero has to be asked for, not
    // inferred from an absence.
    //
    // A numeric string is accepted because a person using curl writes one, and
    // it is unambiguous. Nothing else is.
    const raw = body.capUsd
    const cap =
      typeof raw === 'number'
        ? raw
        : typeof raw === 'string' && raw.trim() !== ''
          ? Number(raw)
          : NaN
    if (!Number.isFinite(cap) || cap < 0) {
      return c.json({ error: 'capUsd has to be a number of US dollars, zero or more.' }, 400)
    }
    try {
      const budget = await setBudget(options.pool, clock, {
        orgId: caller.orgId,
        provider,
        capUsd: cap,
        actorLabel: caller.label,
        actorUserId: caller.userId,
        origin: 'cli',
      })
      return c.json({
        provider: budget.provider,
        period: budget.period,
        capUsd: budget.capUsd,
        spentUsd: budget.spentUsd,
        remainingUsd: budget.remainingUsd,
      })
    } catch (err) {
      if (err instanceof ProviderKeyError) return c.json({ error: err.message }, 400)
      throw err
    }
  })

  // -------------------------------------------------------------------------
  // Model calls against a budget
  // -------------------------------------------------------------------------
  //
  // These paths are the providers' own, on purpose. Both callers in this
  // repository build their URL by concatenating a base onto the provider's
  // path and take that base from an environment variable, so answering here
  // means neither of them changes: point ANTHROPIC_BASE_URL at
  // `<origin>/byok/anthropic`, put an Antifailure token where the provider key
  // used to go, and the same code now spends against a cap.
  //
  // The token arrives in whichever header that provider's client sends it in,
  // for the same reason. Anthropic's client sends x-api-key; OpenAI's sends an
  // Authorization bearer. Insisting on one shape would mean editing both
  // callers to satisfy a preference of ours.

  async function byokCaller(c: Context, provider: string): Promise<string | Response> {
    if (!(PROVIDERS as string[]).includes(provider)) {
      return c.json({ error: { message: `Unknown provider ${provider}.` } }, 404)
    }
    if (!options.sealingKey) {
      return c.json(
        { error: { message: 'This control plane cannot hold provider keys: AF_PROVIDER_KEY_SECRET is not set.' } },
        503,
      )
    }
    const bearer = c.req.header('authorization') ?? ''
    const token = bearer.startsWith('Bearer ')
      ? bearer.slice(7).trim()
      : (c.req.header('x-api-key') ?? '').trim()
    if (!token) {
      return c.json({ error: { message: 'No Antifailure token was sent.' } }, 401)
    }

    // Either kind of token. An engine on a build machine has an engine token
    // and no person attached; a terminal has a personal one. Both are asking
    // the same organization to spend its own money, so both are accepted, and
    // the organization comes from the token rather than from the request.
    const engine = await authenticateEngine(options.pool, clock, token)
    if (engine) return engine.orgId
    const who = await identify(options.pool, clock, token)
    if (who) return who.orgId

    return c.json({ error: { message: 'That token is not valid.' } }, 401)
  }

  async function byok(c: Context, provider: string): Promise<Response> {
    const caller = await byokCaller(c, provider)
    if (caller instanceof Response) return caller

    const body = await c.req.text()
    try {
      const result = await forward(
        {
          pool: options.pool,
          clock,
          sealingKey: options.sealingKey!,
          prices: options.modelPrices ?? {},
          ...(options.providerBases ? { bases: options.providerBases } : {}),
        },
        provider as Provider,
        caller,
        body,
      )
      // The provider's own status and body, unchanged. A caller that knows how
      // to read an Anthropic error should keep being able to.
      c.header('content-type', 'application/json')
      // Said out loud so a run can show what it spent without asking again.
      if (result.costUsd !== null) c.header('x-antifailure-cost-usd', result.costUsd.toFixed(6))
      return c.body(result.body, result.status as 200)
    } catch (err) {
      // The provider's error shape, so a client library parses our refusal the
      // same way it parses theirs rather than throwing on an unexpected body.
      if (err instanceof ProxyError) {
        return c.json({ error: { message: err.message, type: 'antifailure_refused' } }, err.status as 400)
      }
      if (err instanceof ProviderKeyError) {
        // 402: the request is fine and there is no allowance. Distinct from a
        // 401, because retrying with a different token will not help and a
        // client that treats it as auth will loop.
        return c.json({ error: { message: err.message, type: 'antifailure_budget' } }, 402)
      }
      if (err instanceof PricingError) {
        return c.json({ error: { message: err.message, type: 'antifailure_unpriced' } }, 400)
      }
      throw err
    }
  }

  app.post('/byok/anthropic/v1/messages', (c) => byok(c, 'anthropic'))
  app.post('/byok/openai/v1/chat/completions', (c) => byok(c, 'openai'))

  // -------------------------------------------------------------------------
  // GitHub webhook deliveries
  // -------------------------------------------------------------------------
  //
  // The only unauthenticated endpoint here that writes anything, so the order
  // of what follows is the security property:
  //
  //   1. read the RAW body,
  //   2. verify the HMAC over those exact bytes,
  //   3. only then parse it.
  //
  // Parsing first and verifying after would mean running a JSON parser over
  // whatever anybody on the internet sent, and deciding afterwards whether to
  // have trusted it.
  //
  // It answers 2xx for anything it has decided about, including deliveries it
  // does not act on. GitHub retries a 5xx, so answering 500 to an event this
  // control plane will never handle produces a retry storm against an endpoint
  // that will refuse it identically every time.

  app.post('/webhooks/github', async (c) => {
    const secret = options.githubWebhookSecret ?? null
    if (!secret) {
      // 503, not 401. Nothing is wrong with the request; this installation has
      // no App configured, and a delivery arriving here is a misconfiguration
      // worth seeing in GitHub's delivery log rather than a rejection.
      return c.json({ error: 'This control plane has no GitHub App configured.' }, 503)
    }

    const raw = await c.req.text()
    if (!verifySignature(secret, raw, c.req.header('x-hub-signature-256'))) {
      // No detail. A body that says which part failed is a body that helps
      // somebody iterate towards a valid signature.
      return c.json({ error: 'That delivery could not be verified.' }, 401)
    }

    let payload: Record<string, unknown>
    try {
      payload = JSON.parse(raw) as Record<string, unknown>
    } catch {
      return c.json({ error: 'The body is not JSON.' }, 400)
    }

    const event = c.req.header('x-github-event') ?? 'unknown'
    try {
      const outcome = await handleDelivery(options.pool, clock, event, payload)
      return c.json(outcome, 200)
    } catch (err) {
      // A real failure on our side. 500 is right here and the retry is wanted:
      // a database that was briefly unreachable should not lose an
      // installation event, because nothing else will ever tell us about it.
      const message = err instanceof Error ? err.message : String(err)
      return c.json({ event, error: message }, 500)
    }
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
      countIngestion(metrics, result, events)
      // 207 when some were rejected, so a caller that only checks the status
      // still learns that the batch was not wholly accepted.
      return c.json(result, result.rejected > 0 ? 207 : 202)
    } catch (err) {
      if (err instanceof IngestRefused) {
        metrics.ingestBatches.inc({ outcome: 'refused' })
        if (err.retryAfterSeconds) c.header('retry-after', String(err.retryAfterSeconds))
        return c.json({ error: err.message, retryAfterSeconds: err.retryAfterSeconds }, err.status as 429)
      }
      metrics.ingestBatches.inc({ outcome: 'error' })
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

  // The console, last, so that every API route above wins a path collision.
  // Mounted on this app rather than a second one: the session cookie the
  // browser holds is the session these pages read, and a separate origin would
  // need CORS, a second cookie policy and a place to put a token in a client.
  if (options.console !== false) {
    mountConsole(app, {
      pool: options.pool,
      clock,
      secureCookies: secure,
      sealingKey: options.sealingKey ?? null,
    })
  }

  return { app, ingestLimiter, authLimiter, metrics }
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
 * Turns one ingested batch into the numbers the objectives are measured on.
 *
 * The engine reports; the control plane counts. That is the only division that
 * works, because the engine runs on machines nothing scrapes and the events it
 * sends are already the record of what happened. Counting here means the
 * service level objectives are computed from the same stream the dashboard
 * shows, rather than from a second pipeline that can disagree with it.
 */
function countIngestion(
  metrics: ControlPlaneMetrics,
  result: { accepted: number; duplicates: number; rejected: number },
  events: IncomingEvent[],
) {
  metrics.ingestBatches.inc({ outcome: result.rejected > 0 ? 'partial' : 'accepted' })
  metrics.ingestEvents.inc({ outcome: 'accepted' }, result.accepted)
  metrics.ingestEvents.inc({ outcome: 'duplicate' }, result.duplicates)
  metrics.ingestEvents.inc({ outcome: 'rejected' }, result.rejected)

  for (const event of events) {
    switch (event.type) {
      case 'environment.ready':
        metrics.environmentOutcomes.inc({ outcome: 'ready' })
        // The engine measures this, because only the engine knows when the
        // work started. A control plane timing it from its own clock would be
        // measuring the network as well, and would report nothing at all for
        // an environment created while it was unreachable.
        if (typeof event.payload?.seconds === 'number') {
          metrics.environmentReadySeconds.observe(event.payload.seconds)
        }
        break
      case 'environment.failed':
        metrics.environmentOutcomes.inc({
          outcome: 'failed',
          // The code, not the message. A dashboard can group by AF-DB-001; it
          // cannot group by a sentence written for a terminal.
          code: typeof event.payload?.code === 'string' ? event.payload.code : 'unknown',
        })
        break
      case 'verdict.recorded':
        metrics.runVerdicts.inc({
          verdict: typeof event.payload?.value === 'string' ? event.payload.value : 'unknown',
        })
        break
      default:
        break
    }
    if (event.type.startsWith('environment.')) {
      metrics.environmentTransitions.inc({ to_state: event.type.slice('environment.'.length) })
    }
  }
}

function tooMany(c: { header: (k: string, v: string) => void; json: (b: unknown, s: 429) => Response }, seconds: number) {
  c.header('retry-after', String(seconds))
  return c.json({ error: 'Too many attempts. Try again shortly.', retryAfterSeconds: seconds }, 429)
}

// Re-exported so that an edition built on top of this can import the permission
// model from one place. The types are the contract between the two, and a
// second copy of them is a second thing that drifts.
// Re-exported for the same reason the permission model below is: an edition
// built on top of this imports the contract from one place, and a second copy
// of any of it is a second thing that drifts. Single sign-on needs all three
// groups: the extension point to mount its routes, the limit types to declare
// what bounds them, and the session helpers to issue a cookie once an assertion
// has been verified. Issuing that cookie through issueSession rather than by
// writing the row itself is what keeps rotation, expiry and the CSRF derivation
// identical between signing in with GitHub and signing in through a provider.
export {
  registerExtension,
  registeredExtensions,
  extensionRoutes,
  clearExtensions,
  setSignInPolicy,
  hasSignInPolicy,
  decideSignIn,
  ExtensionRefused,
  type SignInAttempt,
  type SignInDecision,
  type SignInPolicy,
  type Extension,
  type ExtensionRoute,
  type ExtensionMethod,
} from './extensions.ts'

export { type EndpointLimit, type LimitKey } from './limits.ts'

export {
  SESSION_COOKIE,
  CSRF_HEADER,
  IDLE_TIMEOUT_MS,
  ABSOLUTE_LIFETIME_MS,
  hashToken,
  issueSession,
  resolveSession,
  revokeSession,
  sessionCookie,
  clearedCookie,
  readCookie,
  csrfTokenFor,
  csrfMatches,
  type IssuedSession,
  type ResolvedSession,
} from './auth/session.ts'

export { safeRedirect } from './auth/signin.ts'

export { type Clock, systemClock, FakeClock } from './clock.ts'

// The router's request context, re-exported for the same reason the database
// exports drizzle's sql tag: a package that imports hono itself gets a second
// copy, and an extension handler typed against that copy does not satisfy the
// ExtensionRoute type declared against this one.
export type { Context } from 'hono'

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
