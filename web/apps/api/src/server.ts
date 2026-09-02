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

import { randomUUID } from 'node:crypto'
import { Hono } from 'hono'
import type { Context } from 'hono'
import type { ApiEnv } from './env.ts'
import {
  mintEngineToken,
  listEngineTokens,
  revokeEngineToken,
  MAY_MANAGE_TOKENS,
  TokenError,
} from './tokens.ts'
import { sql as rawSql } from 'drizzle-orm'
import { trpcServer } from '@hono/trpc-server'
import type { Pool, AdminPool } from '@antifailure/db'
import {
  ADMIN_CSRF_HEADER,
  ADMIN_SESSION_COOKIE,
  AdminSignInError,
  adminCsrfMatches,
  adminCsrfTokenFor,
  adminSessionCookie,
  adminSignIn,
  adminSignOut,
  clearedAdminCookie,
  looksSameOrigin,
  readAdminSessionCookie,
  resolveAdminSession,
} from './admin/session.ts'
import { actorOf } from './admin/trpc.ts'
// Lifted out of this file when a second one needed it. `x-forwarded-for` is a
// LIST with optional ports, and Postgres refuses every one of those shapes on
// an inet column. See clientaddress.ts.
import { clientAddress, clientIP } from './clientaddress.ts'
import { endImpersonation, registerImpersonationRoutes } from './admin/customers.ts'
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
import { claimRun, heartbeat, resolveOverdueRuns } from './workloads/store.ts'
import {
  acknowledgeCommand, claimCommands, expireOverdueCommands,
} from './workloads/commands.ts'
import {
  beginSignIn,
  completeSignIn,
  SignInError,
  safeRedirect,
  type SignInAllowlist,
} from './auth/signin.ts'
import { problem, type Problem, type ProblemAction } from './errorpage.ts'
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
import {
  acceptInvitation,
  lookupInvitation,
  InvitationError,
} from './enterprise/invitations.ts'
import { readHeldExport } from './enterprise/deletion.ts'
import { mountConsole } from './console/index.ts'
import type { ConsoleBuild } from './console/static.ts'
import { PROVIDERS, type Provider } from './providers/seal.ts'
import { verifySignature } from './github/app.ts'
import { forward, ProxyError } from './providers/proxy.ts'
import { PricingError, type Price } from './providers/pricing.ts'
import { handleDelivery } from './github/webhook.ts'
import {
  accountLoginFrom,
  claimDelivery,
  closeDelivery,
  releaseDelivery,
} from './github/deliveries.ts'
import {
  CALLBACK_TTL_MS,
  handleLifecycleDelivery,
  hashCallback,
  issueCallback,
  issueWorkflowEngineToken,
  recordReport,
  WORKFLOW_ENGINE_TTL_MS,
  type LifecycleDeps,
} from './github/lifecycle.ts'
import type { RepositoryApi } from './github/api.ts'
import {
  ActionsKeys,
  CALLBACK_AUDIENCE,
  kidOf,
  TokenRefused,
  verifyWorkflowIdentity,
} from './github/oidc.ts'
import { registerWorkflowIdentityRoutes, repositoryLimiter } from './github/exchange.ts'
import {
  handleStripeDelivery,
  parseStripeEvent,
  verifyStripeSignature,
  type Billing,
} from './billing/index.ts'
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
import { createAnalytics, type Analytics } from './analytics/record.ts'
import { beaconCors, siteBeacon } from './analytics/beacon.ts'
import { decideSignIn, extensionRoutes } from './extensions.ts'
import {
  limitFor, bucketFor, servedRoute, ENDPOINT_LIMITS, type EndpointLimit,
} from './limits.ts'
import { createMetrics, routeLabel, statusClass, type ControlPlaneMetrics } from './metrics.ts'
import { apiNotFound } from './notfound.ts'
import { engagedReason } from './admin/controls.ts'
import {
  HOSTED_ACCESS_MESSAGE,
  hasHostedAccess,
  type HostedRequiredPlan,
} from './hosted.ts'

export interface ServerOptions {
  pool: Pool
  /**
   * The operator database credential, for the admin portal.
   *
   * A SEPARATE pool with a separate role, never `pool` handed in twice: the
   * cross tenant read has to be a credential the application cannot acquire.
   * Absent means this installation has no operator portal, which is the correct
   * default for somebody running this for one team, and the admin procedures
   * say so by name rather than rendering an empty portal.
   */
  adminPool?: AdminPool | null
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

  /** Who may sign in at all. Null is open, which is the self-hosted default.
   *  See parseAllowlist: an empty list is closed to everyone, not open. */
  signInAllowlist?: SignInAllowlist
  /** Whether somebody who signs in with no organization is given one, on the
   *  free plan, owned by them. Default false; see auth/provision.ts for why
   *  that direction and not the other. */
  selfServeSignup?: boolean
  /** Set false to serve the API alone, without the console. */
  console?: boolean
  /** The exported console, located at start-up. Absent means the API is served
   *  alone, which is a legitimate way to run this and is logged as such rather
   *  than answering blank 404s that read like a routing bug. */
  consoleBuild?: ConsoleBuild
  /** The secret that seals provider keys. Null means keys cannot be stored,
   *  which the console says out loud rather than failing on submit. */
  sealingKey?: Buffer | null
  /** The GitHub App's webhook secret. Null means no App is configured, and the
   *  webhook endpoint refuses every delivery rather than accepting unsigned
   *  ones. */
  githubWebhookSecret?: string | null
  /**
   * Acting on a repository as the installation: check runs, the pull request
   * comment, cancelling a run.
   *
   * Null means no App is configured, and the pull request lifecycle records
   * what it learns and publishes nothing, which is a real way to run this: the
   * deliveries still populate installations and repositories, and a deployment
   * with no App key is not a deployment that should refuse to start.
   */
  githubApi?: RepositoryApi | null
  /**
   * GitHub's signing keys, for the workflow identity tokens a job exchanges for
   * a callback credential. Supplied by a test so nothing reaches github.com;
   * unset means the real key set.
   */
  actionsKeys?: ActionsKeys
  /** Drops a cached installation token, so the webhook can invalidate one the
   *  moment GitHub says the installation changed. Absent when no App is
   *  configured, which is when there is no cache to drop from. */
  forgetInstallationToken?: (installationId: number) => void
  /** Stripe, when this installation takes money. Null is the self-hosted
   *  default: the billing routes answer PRECONDITION_FAILED naming the
   *  variables, and the webhook endpoint refuses every delivery rather than
   *  accepting unsigned ones. */
  stripe?: Billing | null
  /** Null for self-hosting. The hosted service requires this plan everywhere
   *  except authentication, billing, health, and sign-out. */
  hostedRequiredPlan?: HostedRequiredPlan | null
  /** Whether the plan may be written by hand through `billing.set`. False by
   *  default, deliberately: the safe answer is the one an operator who has not
   *  thought about billing gets without doing anything. See hosted.ts. */
  operatorSetsPlan?: boolean
  /** Public GitHub App installation address shown to a signed-in user who has
   *  no organization yet. */
  githubAppInstallUrl?: string
  /** Where somebody this installation will not admit is sent instead, which on
   *  the hosted planes is the marketing site's request page. Undefined is the
   *  self-hosted default and means the refusal page offers no link, because
   *  pointing an operator's users at the vendor's waitlist would be wrong. */
  signupUrl?: string
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
  /**
   * The key organization surrogates are computed under.
   *
   * Null turns analytics off: nothing is recorded, the site beacon answers that
   * it is off, and the dashboard says so rather than showing an empty chart
   * that looks like nobody came. There is deliberately no fallback to a
   * constant, because a constant key is a surrogate anybody can recompute,
   * which is an org_id with extra steps.
   */
  analyticsSecret?: Buffer | null
  /**
   * The organization whose members may read the analytics dashboard.
   *
   * The dashboard shows the whole installation: every organization's funnel, the
   * plan mix, the acquisition channels. On a hosted control plane that is the
   * operator's business and nobody else's, so a permission alone is not enough
   * to gate it, because every organization has an owner and every owner holds
   * every permission in their own organization.
   *
   * Null means nobody can read it, which is the safe default and is what a
   * self-hosted installation gets until its operator names their own
   * organization here. The route says which variable to set rather than
   * answering an empty page.
   */
  analyticsOperatorOrgSlug?: string | null
  /**
   * Where the marketing site is served from, for the one endpoint the browser
   * calls cross-origin. Null refuses every beacon rather than reflecting
   * whatever Origin arrives, which is what a permissive default would do.
   */
  siteOrigin?: string | null
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
  // Minting an engine token is the one capability a self-hoster cannot do
  // without, and it is not in CLI_SCOPES for the same reason the provider
  // scopes are not: it produces a credential, so it has to be asked for by name
  // and approved on a screen that shows what was asked for.
  'tokens.manage',
]



/**
 * The reason maintenance is engaged, or null.
 *
 * Named so that the catalog entry in admin/controls.ts can point at a real
 * function and a test can assert the pointer is not stale. It reads on a
 * connection with no tenant, which the read policy on platform_controls
 * deliberately admits: every request has to be able to learn that the
 * installation is paused, including requests that have no organization yet.
 */

/**
 * The paths maintenance mode does not refuse.
 *
 * Exported, and read by a test that walks the routes the server actually
 * serves, because the failure this list guards against is not a wrong entry.
 * It is a route somebody adds later, outside every prefix here, that turns out
 * to be the one an operator needs in order to turn maintenance off. That
 * failure is invisible in code review and total in production.
 */
export const maintenanceExemptions = [
  // Signing in. Refusing this means the operator who engaged maintenance
  // cannot authenticate to release it, and the only way back is a deploy or a
  // psql session. That is the canonical way a maintenance mode becomes the
  // incident it was meant to contain.
  '/auth/',
  // Operator sign-in, wherever the admin portal ends up putting it. See the
  // comment at the middleware for why this prefix is not optional.
  //
  // BOTH spellings, and the second is not hypothetical: the portal landed
  // sign-in at POST /v1/admin/signin, outside `/admin/`, and the route-table
  // test caught it by name. That is the lockout this list exists to prevent,
  // arriving from the exact direction that was predicted and still missed by a
  // list written from memory. Either prefix is cheap to exempt and either one
  // is an outage to omit.
  '/admin/',
  '/v1/admin/',
  // Engines keep reporting. Refusing ingestion does not pause anything, it
  // loses the record of work that ran anyway.
  '/v1/events',
  // The credential a running job needs in ORDER to report. `/v1/events` being
  // open and this being closed is incoherent: a workflow exchanges its GitHub
  // identity token here for the callback credential it then sends events with,
  // so refusing this while accepting those leaves the job holding nothing it
  // can use. The route-table test found it, under `/v1/auth/` rather than the
  // `/auth/` this list already carried, which is the second time a prefix that
  // LOOKS like it covers a path did not.
  '/v1/auth/',
  // The surface that owns the switch. admin-portal has committed to this
  // prefix as a hard interface for exactly this reason.
  '/trpc/admin.',
] as const

export async function refuseDuringMaintenance(pool: Pool): Promise<string | null> {
  return pool.withoutTenant((db) => engagedReason(db, 'maintenance'))
}
/**
 * The code the unexpected path returns.
 *
 * A literal from `engine/internal/errors/catalog.yaml`, and written out rather
 * than imported because the catalog is Go. `tools/errcheck` reads this file for
 * quoted codes, so an entry that stops being returned here, or a code returned
 * here with no entry, is a build failure in both directions.
 *
 * The value matters to a caller: it is the key into
 * https://antifailure.dev/errors.v1.json, which carries the same message,
 * resolution and retryability an engine-side failure would.
 *
 * Named for what it is rather than for the code it holds, and the obvious name
 * was the code with its hyphens turned into underscores.
 * `test/config-docs.test.ts` refused that: it reads this directory for anything
 * shaped like an AF prefixed screaming-case identifier and requires it on the
 * configuration reference page, because an environment variable the process
 * reads and nobody documents is an operator's afternoon. An identifier that
 * reads like a variable and is not one puts a setting on that page that does
 * nothing. Writing the rejected name into this comment fails the same test, for
 * the same reason, which is worth knowing before trying it.
 */
const CONTROL_PLANE_FAILURE = 'AF-CP-003'

export function createServer(options: ServerOptions) {
  const clock = options.clock ?? systemClock
  const secure = options.secureCookies ?? true
  const metrics = options.metrics ?? createMetrics(options.version ?? 'dev')
  const hostedRequiredPlan = options.hostedRequiredPlan ?? null
  const operatorSetsPlan = options.operatorSetsPlan ?? false
  // Read once. It is the bounded set of label values, and reading it per
  // request would be the metrics endpoint doing work proportional to traffic.
  const declaredRoutes = Object.keys(ENDPOINT_LIMITS)
  const app = new Hono<ApiEnv>()

  // One identifier per request, on the response and on the log line.
  //
  // Without it the 500 below told a caller to go and find their request in the
  // logs and gave them nothing to find it with: no id in the body, no id on the
  // header, and deliberately no query, no parameters and no payload in the log,
  // so there was nothing on either side to match. That is a resolution step
  // nobody can carry out, which is worse than no resolution step, because it
  // reads like one.
  //
  // A caller-supplied x-request-id is honoured so a trace crossing a proxy
  // stays one identifier, and it is bounded and filtered first: it is echoed
  // into a response header and written to a log, and an unbounded caller string
  // in either is how a header injection or a forged log line happens.
  app.use('*', async (c, next) => {
    const supplied = c.req.header('x-request-id')
    const id =
      supplied && /^[A-Za-z0-9._-]{1,64}$/.test(supplied) ? supplied : randomUUID()
    c.set('requestId', id)
    await next()
    c.header('x-request-id', id)
  })

  // Hono's default unhandled-error response is plain text. Every expected
  // refusal below is JSON already, and the unexpected path has to keep that
  // contract too: an agent should not need an HTML or text parser only when
  // the service is least healthy.
  //
  // What reaches the log is the class of the error, the driver's own code, the
  // method and the route, and the id. NOT the error object and NOT its message:
  // Drizzle writes a query failure as "Failed query: <the whole statement>"
  // with the parameters after it, so its message can carry event payloads and
  // anything else a caller sent. This is the same reason the tRPC formatter
  // withholds the stack, applied to the member beside it.
  app.onError((err, c) => {
    const cause = (err as { cause?: { code?: unknown } }).cause
    const requestId = c.get('requestId') ?? 'unassigned'
    console.error('unhandled request error', {
      requestId,
      method: c.req.method,
      route: c.req.routePath,
      type: err instanceof Error ? err.name : typeof err,
      providerCode: typeof cause?.code === 'string' ? cause.code : undefined,
    })
    c.header('x-request-id', requestId)
    return c.json(
      {
        error: {
          code: CONTROL_PLANE_FAILURE,
          message: 'The control plane could not complete this request.',
          resolution:
            'Retry once. If it fails again, quote the requestId below: it is the only thing ' +
            'that ties this answer to a log line.',
        },
        requestId,
      },
      500,
    )
  })

  // Two limiters with different shapes. Ingestion is high volume from few
  // callers, so the burst is large. Authentication is low volume from many
  // callers and is the one worth throttling hard, because it is where a
  // stolen-cookie or code-guessing attempt shows up.
  const ingestLimiter = new RateLimiter(clock, options.ingestLimit ?? { rate: 200, burst: 2000 })
  const authLimiter = new RateLimiter(clock, options.authLimit ?? { rate: 1, burst: 20 })

  // Analytics. Off when no surrogate secret is configured, and off means every
  // method still works and records nothing, so no producer has to check first.
  // A producer wrapped in `if (analytics.enabled)` is a producer that stops
  // being exercised by tests the moment somebody forgets the secret.
  const analytics = createAnalytics({
    secret: options.analyticsSecret ?? null,
    clock,
    counters: { events: metrics.analyticsEvents, rejections: metrics.analyticsRejections },
  })

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
    const path = new URL(c.req.url).pathname
    const limit = limitFor(c.req.method, path)
    if (!limit) {
      // Two different things arrive here, and until this line they were
      // answered identically.
      //
      // A path the router has no route for is a typo in somebody's URL. It was
      // answered 500, which told every monitor and load balancer watching the
      // deployed control plane that the server was broken because a client
      // asked for something that never existed. It is a 404, and Hono's own
      // not-found handler already knows how to say so.
      //
      // A route that EXISTS with no declared limit is the hole this gate was
      // built to catch, and it stays a refusal. Which of the two this is comes
      // from the router itself rather than from a second list of paths, so
      // there is nothing to keep in step. See servedRoute.
      const route = servedRoute(app, c.req.method, path)
      if (!route) return c.notFound()

      // Deliberately loud. The alternative is a quiet default, and a quiet
      // default means nobody ever notices the endpoint is unbounded.
      //
      // Loud in the LOG, though, not in the body. The body used to carry
      // "Add it to ENDPOINT_LIMITS with the reason for the number", which is an
      // instruction to a maintainer of this codebase served to anybody who can
      // reach the port. It described this server's own gate design to a
      // stranger and told the one person who could act on it nothing, because
      // maintainers read logs and not other people's 500 bodies. So the
      // sentence moves to the log line, where it names the catalog key to add,
      // and the caller gets the same answer every other control plane failure
      // gives: a code, a resolution, and the id that ties the two together.
      //
      // The route pattern is logged rather than the path: it comes from this
      // server's route table, so it cannot carry a caller's string into a log,
      // and it is already the key ENDPOINT_LIMITS would hold it under.
      const requestId = c.get('requestId') ?? 'unassigned'
      console.error('endpoint has no declared rate limit, refused', {
        requestId,
        route,
        fix: 'Add it to ENDPOINT_LIMITS in web/apps/api/src/limits.ts, with the reason for the number.',
      })
      return c.json(
        {
          error: {
            code: CONTROL_PLANE_FAILURE,
            message: 'The control plane could not complete this request.',
            resolution:
              'Retry once. If it fails again, quote the requestId below: it is the only thing ' +
              'that ties this answer to a log line.',
          },
          requestId,
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

  // -------------------------------------------------------------------------
  // Maintenance mode.
  // -------------------------------------------------------------------------
  //
  // Refuses everything that CHANGES something, for every organization, while
  // the installation's maintenance switch is engaged. Four things are
  // deliberately still allowed, and each one is the difference between a pause
  // and an outage this product caused:
  //
  //   Reads. An operator and a customer can both still see what the state is,
  //   which is what people do first when something is paused.
  //
  //   /auth. If signing in were refused, the operator who engaged maintenance
  //   could not authenticate to release it, and the only way back would be a
  //   deploy or a psql session. That is the canonical way a maintenance mode
  //   becomes the incident.
  //
  //   /v1/events. Engines keep reporting. Refusing ingestion does not pause
  //   anything, it loses the record of work that ran anyway.
  //
  //   The admin portal's own procedures. The switch has to be reachable from
  //   the surface that owns it, and the matching prefix is asserted by a test
  //   rather than trusted, because a rename here is a lockout.
  //
  // Enforced before the routes rather than inside each one, because the failure
  // this guards against is a route somebody adds later and does not think
  // about, which is the same reason ENDPOINT_LIMITS is a list rather than a
  // decorator.
  //
  //   Anything under /admin. Operator sign-in runs BEFORE an operator session
  //   exists, so it cannot be a procedure behind the admin session guard, and
  //   if it lands at /admin/auth/... rather than under the tRPC prefix then
  //   exempting only /trpc/admin. locks the operator away from the switch that
  //   releases maintenance. This prefix is the one that costs nothing to add
  //   and is a lockout to omit. `maintenanceExemptions` is exported so a test
  //   can walk the server's REAL route table against it rather than against
  //   somebody's recollection of it.
  const MAINTENANCE_EXEMPT_PREFIXES = maintenanceExemptions
  app.use('*', async (c, next) => {
    const method = c.req.method.toUpperCase()
    if (method === 'GET' || method === 'HEAD' || method === 'OPTIONS') return next()
    const path = new URL(c.req.url).pathname
    if (MAINTENANCE_EXEMPT_PREFIXES.some((p) => path.startsWith(p))) return next()
    const reason = await refuseDuringMaintenance(options.pool)
    if (reason === null) return next()
    return c.json(
      {
        error:
          `This installation is paused for maintenance: ${reason}. Nothing has been lost, ` +
          `everything can still be read, and work that was already running is untouched.`,
      },
      503,
    )
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

  /**
   * Records a completed sign-in.
   *
   * Detached from whatever transaction the sign-in used, and that is the one
   * place in this subsystem where the "same transaction as the thing it
   * describes" rule is deliberately broken. The sign-in exchange spans several
   * transactions across two modules, so there is no single one to join; and if
   * there were, this is the path where a failed analytics insert would keep
   * somebody out of their own account. Losing the event is the cheaper failure
   * and it is counted either way.
   *
   * first_time is read from the age of the user row rather than from a session
   * count, because sessions are swept when they expire, so a member returning
   * after a month would read as new and the number would be quietly inflated.
   * A user row is created during the exchange that issues the first session, so
   * a row younger than five minutes is a first sign-in; five minutes is far
   * above any exchange and far below any return visit.
   */
  async function recordSignIn(
    method: 'github' | 'email_link' | 'device' | 'sso',
    userId: string,
    orgId: string | null,
  ): Promise<void> {
    const created = await options.pool
      .withoutTenant(
        (db) =>
          db.execute<{ created_at: Date | string }>(
            rawSql`SELECT created_at FROM users WHERE id = ${userId}::uuid`,
          ),
        { signinUserId: userId },
      )
      .catch(() => [])
    const at = created[0]?.created_at
    const ageMs = at ? clock.now().getTime() - new Date(at).getTime() : Number.POSITIVE_INFINITY

    await analytics.recordDetached(options.pool, {
      name: 'identity.signed_in',
      occurredAt: clock.now(),
      orgId,
      payload: { method, first_time: ageMs < 5 * 60 * 1000 },
    })
  }

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

  /**
   * Where a refused person is offered to go, and nothing when there is
   * nowhere.
   *
   * One place, so the sentence and the link cannot disagree between the two
   * routes that refuse.
   */
  function wayOut(): { actions: ProblemAction[]; sentence: string } {
    if (options.signupUrl) {
      return {
        actions: [{ href: options.signupUrl, label: 'Join the waitlist' }],
        sentence:
          'Leave an address on the waitlist and we will tell you when it opens. The engine ' +
          'itself is open source, runs on your own machine, and needs no account at all.',
      }
    }
    return {
      actions: [],
      sentence:
        'Ask an owner of this installation to add your GitHub account to its sign-in ' +
        'allowlist.',
    }
  }

  /** The page somebody sees when this installation will not let them in. */
  function notInvited(refusal: SignInError): Problem {
    const out = wayOut()
    return {
      status: 403,
      // The sentence the refusal itself carries, not a second copy of it. Every
      // existing client reads this field and a literal here is one rewording
      // away from the API and the page saying different things.
      error: refusal.message,
      title: 'You have not been invited yet',
      body: [
        'This control plane is invitation only, and the GitHub account you just signed in ' +
          'with is not on the list of accounts it admits.',
        refusal.authorizationRevoked
          ? 'Nothing was created here, and the authorization you granted on GitHub a moment ' +
            'ago has already been withdrawn, so nothing of yours is left connected to it.'
          : 'Nothing was created here. You can withdraw the access you granted under ' +
            'Applications in your GitHub settings.',
        out.sentence,
      ],
      actions: out.actions,
    }
  }

  /** The same, for a link that arrived by email. The way back is different:
   *  the form that sends one is on the sign-in screen, not at /auth/github. */
  function staleEmailLink(): Problem {
    return {
      status: 400,
      error: 'This sign-in link is no longer valid. Ask for another one.',
      title: 'This sign-in link is no longer valid',
      body: [
        'A link signs you in once, and it expires. This one has been used already or it has ' +
          'run out of time.',
        'Nothing is wrong with your account. Ask for another link and it will work.',
      ],
      actions: [{ href: options.appBaseUrl ?? '/', label: 'Ask for another link' }],
    }
  }

  /** The one message for a state that was never issued, already used, or has
   *  expired. They are deliberately indistinguishable: telling them apart tells
   *  somebody probing state values which of their guesses was once real. */
  function staleSignIn(): Problem {
    return {
      status: 400,
      error: 'This sign-in link is no longer valid. Start again.',
      title: 'This sign-in link is no longer valid',
      body: [
        'A sign-in can only be completed once, and it has a few minutes to finish. This one ' +
          'has been used already or it has run out of time.',
        'Nothing is wrong with your account. Start again and the next one will work.',
      ],
      actions: [{ href: '/auth/github', label: 'Start again' }],
    }
  }

  app.get('/auth/github', async (c) => {
    const limited = authLimiter.take(clientKey(c.req.header('x-forwarded-for'), c.req.header('user-agent')))
    if (!limited.allowed) return tooMany(c, limited.retryAfterSeconds)

    // Refused here, before the redirect, when the answer cannot depend on who
    // is asking.
    //
    // An allowlist that names nobody closes this installation to every GitHub
    // account there is, which is a property of the deployment and not of the
    // visitor. Sending them to GitHub, having them authorise an application,
    // and refusing them on the way back would be asking for something in order
    // to give an answer already known before they left.
    //
    // A list that names SOMEBODY cannot be resolved this early and this says
    // so rather than pretending. The list is keyed on the GitHub login, the
    // login arrives with the code exchange, and there is nothing in the
    // request that carries it: no cookie, no session, and no identity in the
    // OAuth protocol before the callback. So a non-empty list is still decided
    // at the callback, and what is done about the grant by then is in
    // completeSignIn.
    if (options.signInAllowlist?.size === 0) {
      const out = wayOut()
      return problem(c, {
        status: 403,
        error:
          'This installation is not open for sign-ups. Ask an owner to add your GitHub ' +
          'account to the allowlist.',
        title: 'This control plane is not open for sign-ups',
        body: [
          'Sign-in is closed on this installation. No GitHub account can sign in while it ' +
            'stays that way, so you have not been sent to GitHub and nothing has been asked ' +
            'of your account.',
          out.sentence,
        ],
        actions: out.actions,
      })
    }

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
    if (!code || !state) return problem(c, staleSignIn())

    try {
      const result = await completeSignIn(
        options.pool,
        clock,
        options.github,
        { code, state },
        {
          allowlist: options.signInAllowlist ?? null,
          selfServeSignup: options.selfServeSignup === true,
          log: (message, error) => console.error(message, error),
        },
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
        ip: clientAddress(c.req.header('x-forwarded-for')),
        userAgent: c.req.header('user-agent') ?? undefined,
        replacing: existing ?? undefined,
      })

      c.header('set-cookie', sessionCookie(issued.token, issued.expiresAt, secure))
      await recordSignIn('github', result.userId, decision.orgId)
      const base = options.appBaseUrl ?? '/'
      const target = safeRedirect(result.redirectTo) ?? '/'
      const landing = new URL(target, base.endsWith('/') ? base : `${base}/`)
      // Why they landed with no organization, when a policy said so. The note
      // is checked against a strict pattern before it reaches here, so it
      // cannot carry anything but a short identifier.
      if (decision.note) landing.searchParams.set('note', decision.note)
      return c.redirect(landing.toString(), 302)
    } catch (err) {
      if (err instanceof SignInError) {
        if (err.refusal === 'not-invited') return problem(c, notInvited(err))
        if (err.refusal === 'link-expired') return problem(c, staleSignIn())
        // Something else refused, and it is not a link that ran out. The
        // sentence it carries is the only thing that knows what, so the page
        // shows that rather than a guess: syncMembership refusing to apply an
        // empty member list reaches here, and telling that person their link
        // expired would send them to press the same button forever.
        return problem(c, {
          status: 400,
          error: err.message,
          title: 'The sign-in could not be completed',
          body: [err.message, 'Nothing was created here.'],
          actions: [{ href: '/auth/github', label: 'Try again' }],
        })
      }
      return problem(c, {
        status: 400,
        error: 'GitHub refused the sign in. Try again.',
        title: 'GitHub did not finish the sign-in',
        body: [
          'GitHub would not complete the exchange. That usually means the link was already ' +
            'used, or that too long passed between pressing the button and coming back.',
          'Nothing was created here. Starting again is the whole of the fix.',
        ],
        actions: [{ href: '/auth/github', label: 'Start again' }],
      })
    }
  })

  // -------------------------------------------------------------------------
  // The operator portal's own sign-in
  //
  // SEPARATE ROUTES FROM /auth/*, and that is the whole design rather than
  // tidiness. The product's sessions table is populated by GitHub OAuth, so if
  // operator power were a flag on a product session then compromising somebody's
  // GitHub account would be compromising the platform. Two credentials that fail
  // independently is the point, and these routes never touch `sessions`.
  //
  // These exist because without them the operator portal cannot be signed into
  // AT ALL: adminSignIn, adminSessionCookie and their siblings had no caller
  // outside tests, so ctx.admin was null on every request and all twelve
  // procedures in the admin router answered UNAUTHORIZED. The functions were
  // correct and unreachable, which is the failure mode that looks most like
  // working software.
  //
  // Rate limited on the same bucket as the product's sign-in, because this is a
  // password endpoint and the operator credential is the one that reads every
  // customer's data.
  // -------------------------------------------------------------------------

  app.post('/v1/admin/signin', async (c) => {
    const limited = authLimiter.take(
      clientKey(c.req.header('x-forwarded-for'), c.req.header('user-agent')),
    )
    if (!limited.allowed) return tooMany(c, limited.retryAfterSeconds)

    const body = await c.req.json().catch(() => null)
    const email = typeof body?.email === 'string' ? body.email : ''
    const password = typeof body?.password === 'string' ? body.password : ''

    try {
      const result = await adminSignIn(
        options.pool,
        { email, password, ip: c.req.header('x-forwarded-for'), userAgent: c.req.header('user-agent') },
        clock.now(),
      )
      c.header('set-cookie', adminSessionCookie(result.token, result.expiresAt, secure))
      return c.json({ signedIn: true })
    } catch (err) {
      // ONE MESSAGE FOR EVERY REFUSAL, and the same one adminSignIn composes:
      // a wrong password, an unknown address, an operator with no password set
      // and a suspended operator must be indistinguishable from outside, or
      // this endpoint answers "which of your guesses was closer".
      //
      // 401 rather than 400: the credential was refused, not malformed.
      if (err instanceof AdminSignInError) return c.json({ error: err.message }, 401)
      throw err
    }
  })

  /**
   * The operator session, and the CSRF token that goes with it.
   *
   * Mirrors GET /auth/session, and exists for the same reason: the token is
   * derived from the session cookie, the cookie is HttpOnly, so a browser
   * cannot compute it and a page reload would otherwise lose the one the
   * sign-in response carried. Without this endpoint the operator portal can
   * mutate exactly once per sign-in, which is a guard that looks like it works
   * and fails on the first refresh.
   *
   * It returns the token to whoever already holds the cookie, which is what a
   * CSRF token is: not a secret, but a value an attacker on another site cannot
   * read because they cannot make this request and see its response.
   */
  app.get('/v1/admin/session', async (c) => {
    const token = readAdminSessionCookie(c.req.header('cookie'))
    if (!token) return c.json({ signedIn: false }, 200)
    const session = await resolveAdminSession(options.pool, token, clock.now())
    if (!session) return c.json({ signedIn: false }, 200)
    return c.json({
      signedIn: true,
      csrfToken: adminCsrfTokenFor(token),
      label: session.label,
      email: session.email,
      role: session.role,
      impersonating: session.impersonating,
    })
  })

  app.post('/v1/admin/signout', async (c) => {
    const token = readAdminSessionCookie(c.req.header('cookie'))
    if (token) {
      // AN IMPERSONATION IS ENDED BEFORE THE OPERATOR SESSION IS, and that
      // order is the whole of this block.
      //
      // The portal's refusal screen has always offered "End this session" and
      // it has always called this route, which cleared the OPERATOR cookie and
      // nothing else. The moment impersonation could actually be started, that
      // became a hole: the customer cookie stayed live in the browser for the
      // rest of its lifetime, and the row that would have explained it had just
      // had its operator marker removed. A way out that leaves the door open is
      // not a way out.
      //
      // Read first, because adminSignOut revokes the row this needs to read.
      const operator = await resolveAdminSession(options.pool, token, clock.now())
      if (operator && options.adminPool) {
        await endImpersonation(options.adminPool, operator, clock.now(), {
          ip: clientAddress(c.req.header('x-forwarded-for')) ?? null,
          how: 'signed out',
        })
      }
      await adminSignOut(options.pool, token, clock.now())
    }
    // Cleared whether or not there was a session, so a stale or unparseable
    // cookie can still be got rid of by pressing the button.
    //
    // BOTH cookies, for the reason above. Clearing the customer one on an
    // ordinary operator sign-out costs an operator who also has a product
    // session in the same browser one sign-in, and buys the guarantee that
    // signing out of the portal never leaves a borrowed identity behind. That
    // trade is not close.
    c.header('set-cookie', clearedAdminCookie(secure))
    // append, not set. Hono's header() REPLACES by default, so the second call
    // would silently discard the first and this route would clear exactly one
    // cookie while reading as though it cleared two. Set-Cookie is the one
    // header where a response legitimately carries several.
    c.header('set-cookie', clearedCookie(secure), { append: true })
    return c.json({ signedOut: true })
  })

  // The two impersonation routes, registered from the lane that owns them so
  // that this file gains an import and a call rather than eighty lines. They
  // are here rather than in the tRPC tree because both end in a Set-Cookie for
  // the CUSTOMER's session, and because the operator gate refuses every
  // procedure while impersonating, which would make a tRPC `end` unreachable
  // exactly when it is the only thing left to press. See admin/customers.ts.
  registerImpersonationRoutes(app, {
    pool: options.pool,
    adminPool: options.adminPool ?? null,
    clock,
    secure,
    appBaseUrl: options.appBaseUrl ?? '',
  })

  app.post('/auth/signout', async (c) => {
    const token = readCookie(c.req.header('cookie'), SESSION_COOKIE)
    if (token) await revokeSession(options.pool, token)
    c.header('set-cookie', clearedCookie(secure))
    return c.json({ signedOut: true })
  })

  // Which ways in this deployment actually offers. The console is a static
  // export built once and served by every installation, so it cannot know at
  // build time whether mail is configured here. Asking means a sign-in method
  // that cannot work is not on the page at all, rather than being a button
  // that fails on press.
  const signInMethods = ['github', ...(options.emailSignIn ? ['email'] : [])]

  app.get('/auth/session', async (c) => {
    const token = readCookie(c.req.header('cookie'), SESSION_COOKIE)
    const publicSignIn = {
      methods: signInMethods,
      signupsOpen: options.signInAllowlist == null,
      // Whether signing in ENDS somewhere, which is a different question from
      // whether it is allowed to start. The console renders one of two empty
      // states from this: with self serve on, arriving in no organization is a
      // failure worth reporting; with it off, it is the ordinary state of
      // somebody waiting for an installation or an invitation, and telling
      // them to fix it would be telling them to do something they cannot.
      selfServeSignup: options.selfServeSignup === true,
      githubAppInstallUrl: options.githubAppInstallUrl,
    }
    if (!token) return c.json({ signedIn: false, ...publicSignIn }, 200)
    const session = await resolveSession(options.pool, clock, token)
    if (!session) return c.json({ signedIn: false, ...publicSignIn }, 200)
    return c.json({
      signedIn: true,
      label: session.label,
      orgId: session.orgId,
      orgSlug: session.orgSlug,
      role: session.role,
      plan: session.plan,
      hostedRequiredPlan,
      hostedAccess: hasHostedAccess(session.plan, hostedRequiredPlan),
      githubAppInstallUrl: options.githubAppInstallUrl,
      signupsOpen: options.signInAllowlist == null,
      selfServeSignup: options.selfServeSignup === true,
      // Handed to the page so it can send it back on mutations. Safe to expose:
      // it is derived from the session secret and reveals nothing about it.
      csrfToken: session.csrfToken,
      // Null on every ordinary sign-in. Set when this session is an operator
      // acting as this account, and it is told to the PERSON BEING ACTED AS
      // rather than only recorded about them. The audit entry is the durable
      // record and the customer already gets a copy of it; this is what makes
      // it visible while it is happening, which is the only time they can do
      // anything about it. It carries no secret: the operator's address, the
      // reason they typed, and the entry number, all three of which are already
      // in that organization's own audit log.
      impersonation: session.impersonation,
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
      if (!token) return problem(c, staleEmailLink())

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
        await recordSignIn('email_link', result.userId, result.orgId)
        const base = options.appBaseUrl ?? '/'
        const target = result.redirectTo ?? '/'
        return c.redirect(new URL(target, base.endsWith('/') ? base : `${base}/`).toString(), 302)
      } catch (err) {
        if (err instanceof EmailSignInError) {
          return problem(c, { ...staleEmailLink(), error: err.message })
        }
        throw err
      }
    })
  }


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
      const outcome = await denyDeviceCode(options.pool, clock, String(body.user_code ?? ''))
      if (!outcome.denied) {
        // 409 rather than 200 or 404. Nothing here says whether the code ever
        // existed, which is the same reason approve gives one message for
        // three cases: this endpoint must not become the oracle that tells
        // somebody which guessed codes are real. What it does say is that the
        // login was not declined, because the caller may have just approved it
        // in another tab and a terminal somewhere is holding the token.
        return c.json(
          {
            error:
              'That login was not declined: it was already approved, already answered, or is no longer live. ' +
              'If a terminal has a token it should not have, revoke it under Keys.',
          },
          409,
        )
      }
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
  // -------------------------------------------------------------------------
  // Invitations, and the export a deleted organization is owed
  //
  // Outside tRPC, and both for the same reason: neither caller has a tenant.
  //
  // Somebody accepting an invitation is signed in and belongs to no
  // organization, so `createContext` builds no actor for them and every
  // procedure would answer UNAUTHORIZED. Somebody downloading the export of a
  // deleted organization has no session at all, because the organization the
  // session belonged to no longer exists. In both cases the token in the link
  // is what identifies the row, and the policies in migrations/0022 confine the
  // caller to exactly that one.
  // -------------------------------------------------------------------------

  /** What the link says, before anybody signs in. */
  app.get('/auth/invitation', async (c) => {
    const limited = authLimiter.take(
      clientKey(c.req.header('x-forwarded-for'), c.req.header('user-agent')),
    )
    if (!limited.allowed) return tooMany(c, limited.retryAfterSeconds)

    const token = c.req.query('token') ?? ''
    if (!token) return c.json({ error: 'That link is missing its token.' }, 400)
    const found = await lookupInvitation(options.pool, clock, token)
    // One answer for "no such invitation" whatever the reason, and it is not
    // 404 by accident: this endpoint is reachable without signing in, and an
    // answer that distinguished a wrong token from a revoked one would let
    // somebody test guessed tokens against it.
    if (!found) return c.json({ error: 'That invitation link is not valid.' }, 404)
    return c.json(found)
  })

  /** Taking it up. Needs a session, and deliberately does not need a tenant. */
  app.post('/auth/invitation/accept', async (c) => {
    const session = await sessionFrom(c.req.header('cookie'))
    if (!session) return c.json({ error: 'Sign in first.' }, 401)
    if (!csrfMatches(readCookie(c.req.header('cookie'), SESSION_COOKIE)!, c.req.header(CSRF_HEADER))) {
      return c.json({ error: `This request needs the ${CSRF_HEADER} header from GET /auth/session.` }, 403)
    }
    let body: { token?: unknown } = {}
    try {
      body = (await c.req.json()) as typeof body
    } catch {
      return c.json({ error: 'The body is not JSON.' }, 400)
    }
    const token = String(body.token ?? '')
    if (!token) return c.json({ error: 'That link is missing its token.' }, 400)

    try {
      const accepted = await acceptInvitation(options.pool, clock, {
        token,
        userId: session.userId,
      })
      return c.json(accepted)
    } catch (err) {
      if (err instanceof InvitationError) return c.json({ error: err.message }, 400)
      throw err
    }
  })

  /**
   * The export of an organization that has been deleted.
   *
   * The token is the whole authorisation, so it is rate limited like a sign-in
   * rather than like an API read: it is the one endpoint here somebody could
   * usefully guess at.
   */
  app.get('/exports/deletion', async (c) => {
    const limited = authLimiter.take(
      clientKey(c.req.header('x-forwarded-for'), c.req.header('user-agent')),
    )
    if (!limited.allowed) return tooMany(c, limited.retryAfterSeconds)

    const token = c.req.query('token') ?? ''
    if (!token) {
      return problem(c, {
        status: 400,
        error: 'That link is missing its token.',
        title: 'This export link is incomplete',
        body: [
          'The address has no token on it, so there is nothing to look up. It was probably ' +
            'cut short somewhere between the message it arrived in and the address bar.',
          'Open the original link again, in full.',
        ],
      })
    }

    // `describe` answers the page that the link opens, which has to say whether
    // the export is still there BEFORE it offers a download. Without it the
    // page shows a button and a person with a dead link finds out by pressing
    // it and getting nothing, which is indistinguishable from a broken browser.
    const describe = c.req.query('describe') === '1'
    const held = await readHeldExport(
      options.pool,
      clock,
      token,
      describe ? 'describe' : 'download',
    )
    if (!held.found) {
      // 404 for a link that names nothing, 409 for one that names an export
      // which is not ready yet. The second is a real link and the caller should
      // come back rather than go looking for another one.
      //
      // This link is handed to a person and opened in a browser, so a refusal
      // that answered only JSON put the sentence explaining what happened into
      // an unstyled body. The `state` field stays in the JSON, because the
      // console's export page reads it.
      const notReady = held.state === 'not_ready'
      return problem(c, {
        status: notReady ? 409 : 404,
        error: held.reason,
        json: { state: held.state },
        title: notReady ? 'This export is not ready yet' : 'This export link is not valid',
        body: [
          held.reason,
          notReady
            ? 'The link itself is good. Come back to this same address rather than looking ' +
              'for another one.'
            : 'An export is held for a limited time after an organization is deleted, and ' +
              'then it is destroyed. There is nothing to recover from this address.',
        ],
      })
    }
    if (describe) {
      return c.json({
        organization: held.value.organization,
        slug: held.value.slug,
        generatedAt: held.value.generatedAt,
        expiresAt: held.value.expiresAt,
        sizeBytes: held.value.sizeBytes,
      })
    }

    // A file rather than a page. The console fetches this and saves it, and an
    // operator with the link and curl gets the same bytes.
    const name = `antifailure-${held.value.slug}-${held.value.generatedAt?.slice(0, 10) ?? 'export'}.json`
    return new Response(JSON.stringify(held.value.document, null, 2), {
      status: 200,
      headers: {
        'content-type': 'application/json; charset=utf-8',
        'content-disposition': `attachment; filename="${name}"`,
        // Never cached anywhere but the browser that asked, because the URL
        // carries the only credential there is.
        'cache-control': 'no-store',
      },
    })
  })

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
      plan: who.plan,
      hostedRequiredPlan,
      hostedAccess: hasHostedAccess(who.plan, hostedRequiredPlan),
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
  /** The roles a scope needs on top of the token carrying it. A scope proves
   *  the terminal was approved for something; the role proves the person still
   *  holds it, which is re-read per request so that a demotion takes effect
   *  without waiting for a ninety day token to expire. */
  const SCOPE_NEEDS_ROLE: Record<string, ReadonlySet<string>> = {
    'providers.write': MAY_MANAGE_KEYS,
    'tokens.manage': MAY_MANAGE_TOKENS,
  }

  async function cliCaller(
    c: Context,
    need: 'providers.view' | 'providers.write' | 'tokens.manage',
  ): Promise<CliCaller | Response> {
    const header = c.req.header('authorization') ?? ''
    const token = header.startsWith('Bearer ') ? header.slice(7).trim() : ''
    const who = await identify(options.pool, clock, token)
    if (!who) return c.json({ error: 'This token is not valid.' }, 401)

    if (!hasHostedAccess(who.plan, hostedRequiredPlan)) {
      return c.json({ error: HOSTED_ACCESS_MESSAGE }, 402)
    }

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
    const roles = SCOPE_NEEDS_ROLE[need]
    if (roles && !roles.has(who.role)) {
      return c.json({ error: `${need} needs owner or admin. You are ${who.role}.` }, 403)
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
    const caller = await cliCaller(c, 'providers.view')
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
    const caller = await cliCaller(c, 'providers.write')
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
        analytics,
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
    const caller = await cliCaller(c, 'providers.write')
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
    const caller = await cliCaller(c, 'providers.write')
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
        analytics,
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
  // Engine tokens
  // -------------------------------------------------------------------------
  //
  // What a CI job or a self-hosted engine puts in AF_CONTROL_PLANE_TOKEN. The
  // documentation told people to create one here long before anything could,
  // and `tokens.list` and `tokens.revoke` in the tRPC router managed a resource
  // that had no producer outside the test fixtures.
  //
  // On this surface rather than in tRPC, because tRPC needs a browser session
  // and a self-hoster is at a terminal. The console keeps its own list through
  // the tRPC router and both end up reading the same rows.

  app.post('/v1/tokens', async (c) => {
    const caller = await cliCaller(c, 'tokens.manage')
    if (isResponse(caller)) return caller
    let body: { name?: unknown } = {}
    try {
      body = (await c.req.json()) as typeof body
    } catch {
      return c.json({ error: 'The body is not JSON.' }, 400)
    }
    const name = typeof body.name === 'string' ? body.name : ''
    try {
      const made = await mintEngineToken(options.pool, clock, {
        analytics,
        orgId: caller.orgId,
        name,
        actorUserId: caller.userId,
        actorLabel: caller.label,
        origin: 'cli',
      })
      // The only response in the product that carries a usable credential, and
      // the only time this one is ever readable. 201 rather than 200 so a
      // client can tell a mint from a retry that found an existing row, which
      // this route never does.
      return c.json(
        { id: made.id, name: made.name, prefix: made.prefix, token: made.token },
        201,
      )
    } catch (err) {
      if (err instanceof TokenError) return c.json({ error: err.message }, 400)
      throw err
    }
  })

  app.get('/v1/tokens', async (c) => {
    const caller = await cliCaller(c, 'tokens.manage')
    if (isResponse(caller)) return caller
    const rows = await listEngineTokens(options.pool, caller.orgId)
    return c.json({
      tokens: rows.map((t) => ({
        id: t.id,
        name: t.name,
        prefix: t.prefix,
        createdAt: t.createdAt.toISOString(),
        lastUsedAt: t.lastUsedAt ? t.lastUsedAt.toISOString() : null,
        revokedAt: t.revokedAt ? t.revokedAt.toISOString() : null,
      })),
    })
  })

  app.delete('/v1/tokens/:token', async (c) => {
    const caller = await cliCaller(c, 'tokens.manage')
    if (isResponse(caller)) return caller
    try {
      const result = await revokeEngineToken(options.pool, clock, {
        orgId: caller.orgId,
        idOrPrefix: c.req.param('token'),
        actorUserId: caller.userId,
        actorLabel: caller.label,
        origin: 'cli',
      })
      if (!result.found) {
        // The same answer whether it belongs to another organization or does
        // not exist, which is what every other lookup on this server does.
        return c.json({ error: 'No engine token here has that id or prefix.' }, 404)
      }
      return c.json({
        revoked: true,
        name: result.name,
        alreadyRevoked: result.alreadyRevoked,
      })
    } catch (err) {
      if (err instanceof TokenError) return c.json({ error: err.message }, 400)
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
    if (engine) {
      if (!hasHostedAccess(engine.plan, hostedRequiredPlan)) {
        return c.json({ error: { message: HOSTED_ACCESS_MESSAGE } }, 402)
      }
      return engine.orgId
    }
    const who = await identify(options.pool, clock, token)
    if (who) {
      if (!hasHostedAccess(who.plan, hostedRequiredPlan)) {
        return c.json({ error: { message: HOSTED_ACCESS_MESSAGE } }, 402)
      }
      return who.orgId
    }

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
  // The pull request lifecycle's dependencies
  //
  // Built once, here, rather than per request. Null when there is no App: the
  // deliveries still land and still record installations, and the parts that
  // would publish a check say so rather than the process refusing to start
  // over a feature a self-hosted operator may not want.
  // -------------------------------------------------------------------------

  const actionsKeys = options.actionsKeys ?? new ActionsKeys(clock)
  const consoleBase = options.appBaseUrl ? options.appBaseUrl.replace(/\/+$/, '') : null
  const githubApi = options.githubApi ?? null

  // The other way to get an engine token, and the one a customer in CI should
  // use: no token, no environment variable, no repository secret. A job posts
  // the workflow identity GitHub minted for it and gets back a credential that
  // expires within the quarter hour. Registered as one call because the
  // exchange and the claims that authorize it must not be added apart: an
  // exchange with no way to claim a repository refuses every request, and
  // claims with no exchange are rows nothing reads. See src/github/exchange.ts
  // for why the `repository` claim is an identity and never a permission.
  registerWorkflowIdentityRoutes(app, {
    pool: options.pool,
    clock,
    actionsKeys,
    limiter: repositoryLimiter(clock),
    cliCaller,
  })

  function lifecycleDeps(): LifecycleDeps | null {
    if (!githubApi) return null
    return { pool: options.pool, clock, api: githubApi, consoleBase }
  }

  function bearer(header: string | undefined): string {
    const auth = header ?? ''
    return auth.startsWith('Bearer ') ? auth.slice(7).trim() : ''
  }

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
    const deliveryId = c.req.header('x-github-delivery') ?? ''
    const login = accountLoginFrom(payload)

    // A delivery with no identifier is not one GitHub sent, and the header has
    // been on every delivery since webhooks existed. Refusing rather than
    // handling it unfenced, because the whole point of the ledger below is
    // that nothing here runs twice, and a delivery that cannot be recorded
    // cannot be fenced.
    if (!deliveryId) {
      return c.json({ error: 'That delivery carries no x-github-delivery header.' }, 400)
    }

    // THE REPLAY FENCE. The HMAC says a delivery is genuine and says nothing
    // about it being new, so a captured delivery verifies exactly as well the
    // thousandth time. Everything below this writes something.
    const claim = await claimDelivery(options.pool, clock, {
      deliveryId,
      event,
      action: typeof payload.action === 'string' ? payload.action : null,
      login,
    })
    if (claim.status === 'replay') {
      // 200, because from GitHub's side this delivery succeeded, and it did:
      // it was handled the first time it arrived.
      return c.json(
        { event, handled: true, replay: true, detail: claim.outcome ?? 'already handled' },
        200,
      )
    }
    if (claim.status === 'in_flight') {
      // 503 rather than 200. Another attempt at this exact delivery is running
      // right now; answering 200 would be reporting success for work that may
      // still fail, and a delivery lost that way is silent on both sides.
      c.header('retry-after', String(claim.retryAfterSeconds))
      return c.json({ event, error: 'That delivery is being handled right now.' }, 503)
    }

    try {
      const lifecycle = lifecycleDeps()
      // The account has to be known before the lifecycle runs, because every
      // statement it makes is on a connection scoped to that account and an
      // empty one is refused rather than silently matching nothing. A payload
      // that names no account at all is not one the lifecycle can act on, so it
      // falls through to the installation handler, which answers rather than
      // throwing. Without this a malformed payload would be a 500, and a 500 is
      // a delivery GitHub retries into the same 500 forever.
      const outcome =
        lifecycle && login
          ? await handleLifecycleDelivery(lifecycle, login, event, payload)
          : {
              handled: false,
              detail: lifecycle ? 'the payload names no account' : 'no GitHub App is configured',
              orgId: null,
            }

      // The installation handler still sees everything the lifecycle did not
      // act on, so installations and repositories are recorded exactly as
      // before. Two handlers rather than one because they answer different
      // questions: which accounts exist, and what is happening on a commit.
      // options.github is load bearing and easy to lose in a merge, because it
      // is OPTIONAL: dropping it type checks, and `adoptInstaller` inside
      // handleDelivery then returns null immediately, which silently reinstates
      // the sign in before install ordering that fix exists to close.
      const installation = outcome.handled
        ? null
        : await handleDelivery(options.pool, clock, event, payload, {
            // Named fields rather than positions, for the reason the comment
            // above gives: three optional collaborators on one call is where a
            // value in the wrong slot still type checks.
            github: options.github,
            analytics,
            forgetTokens: options.forgetInstallationToken,
          })

      const detail = outcome.handled ? outcome.detail : (installation?.detail ?? outcome.detail)
      await closeDelivery(
        options.pool,
        clock,
        { deliveryId, login },
        { orgId: outcome.orgId, outcome: detail },
      )
      return c.json(
        {
          event,
          action: typeof payload.action === 'string' ? payload.action : null,
          handled: outcome.handled || (installation?.handled ?? false),
          detail,
        },
        200,
      )
    } catch (err) {
      // A real failure on our side. 500 is right here and the retry is wanted:
      // a database that was briefly unreachable should not lose an
      // installation event, because nothing else will ever tell us about it.
      //
      // The claim goes back first. A claim that survived a failure would be
      // read as "handled" by the retry, so one transient error would turn into
      // a delivery refused forever while looking successful.
      await releaseDelivery(options.pool, { deliveryId, login }).catch(() => {})
      const message = err instanceof Error ? err.message : String(err)
      return c.json({ event, error: message }, 500)
    }
  })

  // -------------------------------------------------------------------------
  // What a job in the customer's CI reports back
  //
  // Two endpoints, and the first one is why there is no repository secret to
  // paste. A job asks GitHub Actions for an identity token, GitHub signs it,
  // and this exchanges it for a credential scoped to ONE generation: one
  // commit, one run, expiring within the hour. GitHub does not grant that
  // identity to a pull request job running on a fork, so the fork case is
  // closed by GitHub's own rules rather than by this remembering to check, and
  // it is closed a second time here because a fork's commit gets no credential
  // until a maintainer has approved that exact commit.
  // -------------------------------------------------------------------------

  app.post('/v1/pr/callback-token', async (c) => {
    const lifecycle = lifecycleDeps()
    if (!lifecycle) {
      return c.json({ error: 'This control plane has no GitHub App configured.' }, 503)
    }
    const presented = bearer(c.req.header('authorization'))
    if (!presented) {
      return c.json(
        {
          error:
            'Present the workflow identity token as a bearer token. In a workflow with ' +
            `id-token: write, ask for it with the audience ${CALLBACK_AUDIENCE}.`,
        },
        401,
      )
    }

    let body: { head_sha?: unknown }
    try {
      body = (await c.req.json()) as { head_sha?: unknown }
    } catch {
      return c.json({ error: 'The body is not JSON.' }, 400)
    }
    const headSha = typeof body.head_sha === 'string' ? body.head_sha : ''
    if (!/^[0-9a-f]{40}$/.test(headSha)) {
      return c.json(
        {
          error:
            'head_sha has to be the full forty character commit this job is checking. A result ' +
            'belongs to one commit, so a credential is issued for one commit.',
        },
        400,
      )
    }

    let identity
    try {
      const keys = await actionsKeys.current(kidOf(presented))
      identity = verifyWorkflowIdentity(presented, { keys, clock })
    } catch (err) {
      if (err instanceof TokenRefused) {
        // The reason is safe to return: every one of them is something the
        // workflow author can act on, and none of them narrows a search for a
        // valid token, because the token is signed by GitHub rather than
        // guessed.
        return c.json({ error: err.message, reason: err.reason }, 401)
      }
      throw err
    }

    const owner = identity.repository.split('/')[0] ?? ''
    const issued = await issueCallback(lifecycle, owner, {
      repository: identity.repository,
      headSha,
      workflowRunId: identity.runId,
      // Which workflow, and which attempt of it, out of the verified token.
      reportedBy: `${identity.jobWorkflowRef} attempt ${identity.runAttempt}`,
    })
    if ('refused' in issued) {
      return c.json({ error: issued.refused }, 409)
    }
    return c.json({
      token: issued.token,
      expires_in: Math.floor(CALLBACK_TTL_MS / 1000),
      repository: identity.repository,
      head_sha: headSha,
    })
  })

  // The engine's own exchange.
  //
  // Same proof as the callback exchange above and deliberately the same code
  // path for it: one JWKS cache, one verifier, one audience. What differs is
  // what comes back. The callback credential publishes a result for one commit;
  // this one lets the engine report its event stream while the run is still
  // happening, which is not about a commit and cannot be bound to one.
  //
  // This route is why a workflow needs no AF_CONTROL_PLANE_TOKEN. Before it
  // existed the engine's control plane sink read that variable, found nothing,
  // and attached no sink at all, so a CI run reported its events nowhere.
  app.post('/v1/engine/token', async (c) => {
    const lifecycle = lifecycleDeps()
    if (!lifecycle) {
      return c.json({ error: 'This control plane has no GitHub App configured.' }, 503)
    }
    const presented = bearer(c.req.header('authorization'))
    if (!presented) {
      return c.json(
        {
          error:
            'Present the workflow identity token as a bearer token. In a workflow with ' +
            `id-token: write, ask for it with the audience ${CALLBACK_AUDIENCE}.`,
        },
        401,
      )
    }

    let identity
    try {
      const keys = await actionsKeys.current(kidOf(presented))
      identity = verifyWorkflowIdentity(presented, { keys, clock })
    } catch (err) {
      if (err instanceof TokenRefused) {
        return c.json({ error: err.message, reason: err.reason }, 401)
      }
      throw err
    }

    const owner = identity.repository.split('/')[0] ?? ''
    const issued = await issueWorkflowEngineToken(lifecycle, owner, {
      repository: identity.repository,
      workflowRunId: identity.runId,
    })
    if ('refused' in issued) {
      return c.json({ error: issued.refused }, 409)
    }
    return c.json({
      token: issued.token,
      expires_in: Math.floor(WORKFLOW_ENGINE_TTL_MS / 1000),
      repository: identity.repository,
    })
  })

  app.post('/v1/pr/report', async (c) => {
    const lifecycle = lifecycleDeps()
    if (!lifecycle) {
      return c.json({ error: 'This control plane has no GitHub App configured.' }, 503)
    }
    const presented = bearer(c.req.header('authorization'))
    if (!presented) {
      return c.json({ error: 'Present the callback credential as a bearer token.' }, 401)
    }

    let body: { head_sha?: unknown; markdown?: unknown; report?: unknown }
    try {
      body = (await c.req.json()) as { head_sha?: unknown; markdown?: unknown; report?: unknown }
    } catch {
      return c.json({ error: 'The body is not JSON.' }, 400)
    }
    const headSha = typeof body.head_sha === 'string' ? body.head_sha : ''
    if (!/^[0-9a-f]{40}$/.test(headSha)) {
      return c.json({ error: 'head_sha has to be the full forty character commit.' }, 400)
    }

    const outcome = await recordReport(lifecycle, hashCallback(presented), {
      headSha,
      markdown: typeof body.markdown === 'string' ? body.markdown : null,
      report: body.report,
    })
    if (outcome.status === 'refused') {
      return c.json({ error: outcome.detail }, 409)
    }
    return c.json({ recorded: true, state: outcome.state, detail: outcome.detail })
  })

  // -------------------------------------------------------------------------
  // Stripe webhook deliveries
  // -------------------------------------------------------------------------
  //
  // The same order as the GitHub endpoint above, and for a sharper reason: this
  // is the endpoint that decides who has paid.
  //
  //   1. read the RAW body,
  //   2. verify the HMAC over those exact bytes and the timestamp,
  //   3. only then parse it.
  //
  // The timestamp is inside the signature, so a captured delivery is useless
  // minutes later. Without that a signature anybody ever saw could be replayed
  // forever, and the delivery worth replaying is invoice.paid.

  app.post('/webhooks/stripe', async (c) => {
    const billing = options.stripe ?? null
    if (!billing) {
      // 503, not 401. Nothing is wrong with the request; this installation
      // takes no money, and a delivery arriving here is a misconfiguration
      // worth seeing in Stripe's delivery log rather than a rejection.
      return c.json({ error: 'This control plane is not configured to take payments.' }, 503)
    }

    const raw = await c.req.text()
    const failure = verifyStripeSignature(
      billing.config.webhookSecret,
      raw,
      c.req.header('stripe-signature'),
      clock.now(),
    )
    if (failure) {
      // The reason is logged and not returned. A body that says which check
      // failed is a body that helps somebody iterate towards a valid signature,
      // and the difference between "stale" and "wrong" tells them which half to
      // work on.
      console.warn(`stripe webhook refused: ${failure}`)
      return c.json({ error: 'That delivery could not be verified.' }, 401)
    }

    const event = parseStripeEvent(raw)
    if (!event) {
      // 400, so Stripe stops. A 500 would make it retry a body that will never
      // parse, forever.
      return c.json({ error: 'The body is not a Stripe event.' }, 400)
    }

    try {
      const outcome = await handleStripeDelivery(options.pool, clock, billing.config, event, analytics)
      return c.json(outcome, 200)
    } catch (err) {
      // A real failure on our side, and the retry is wanted: a database that
      // was briefly unreachable must not lose the event that says somebody paid.
      const message = err instanceof Error ? err.message : String(err)
      return c.json({ event: event.id, type: event.type, error: message }, 500)
    }
  })

  // -------------------------------------------------------------------------
  // Ingestion
  // -------------------------------------------------------------------------

  /** The body as an object, or null when it is not JSON or is not one. Every
 *  engine endpoint reads its body the same way, so a caller sending an array or
 *  a bare string gets the same answer everywhere rather than a type error from
 *  whichever property was touched first. */
  async function readJson(c: Context): Promise<Record<string, unknown> | null> {
    try {
      const parsed: unknown = await c.req.json()
      return parsed !== null && typeof parsed === 'object' && !Array.isArray(parsed)
        ? (parsed as Record<string, unknown>)
        : null
    } catch {
      return null
    }
  }

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
    if (!hasHostedAccess(engine.plan, hostedRequiredPlan)) {
      return c.json({ error: HOSTED_ACCESS_MESSAGE }, 402)
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
      const result = await ingest(options.pool, clock, ingestLimiter, engine, events, analytics)
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
    if (!hasHostedAccess(engine.plan, hostedRequiredPlan)) {
      return c.json({ error: HOSTED_ACCESS_MESSAGE }, 402)
    }
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
  // The site beacon
  //
  // The only unauthenticated write on this server, and the only route a browser
  // on another origin may call. See analytics/beacon.ts for why that is safe:
  // the application role cannot read the table it writes to, and the events it
  // accepts are declared as carrying no organization at all.
  // -------------------------------------------------------------------------

  app.options('/v1/site/events', (c) => {
    // The preflight. Answered here rather than by a wildcard handler, because a
    // wildcard OPTIONS handler answers for every route on the server and that
    // is how an endpoint nobody meant to expose becomes reachable from a page.
    if (!beaconCors(c, options.siteOrigin ?? null)) return c.body(null, 403)
    return c.body(null, 204)
  })

  app.post('/v1/site/events', async (c) => {
    // The origin check comes first and refuses rather than answering without
    // the header. A browser would refuse the response anyway; a non-browser
    // caller would not, and this is the line that bounds it to the site.
    if (!beaconCors(c, options.siteOrigin ?? null)) {
      return c.json({ error: 'This endpoint serves the marketing site only.' }, 403)
    }
    let body: unknown
    try {
      body = await c.req.json()
    } catch {
      return c.json({ error: 'The body is not JSON.' }, 400)
    }
    const result = await siteBeacon(body, { pool: options.pool, analytics, clock })
    return c.json(result.body, result.status as 202)
  })

  // Studio, for an engine
  //
  // An engine on a CI runner has no browser, no cookie and no way to obtain
  // one, so everything it needs is here behind its bearer token rather than
  // behind the session the console uses.
  //
  // WHY THE ENGINE PULLS RATHER THAN BEING TOLD. A workload run is dispatched
  // as a workflow run in the customer's own repository, and a workflow_dispatch
  // carries only the inputs the workflow declares. Passing the run identifier
  // through one would mean an input the engine's CLI has no flag for, which is
  // the dead socket routers/dispatch.ts exists to refuse. So the dispatch says
  // what to run and this says which recorded request it belongs to: the engine
  // asks what is waiting for the environment it is working on, and takes it.
  //
  // That also makes the correlation robust to the dispatch failing. A run whose
  // dispatch was refused is still claimable by an engine somebody starts by
  // hand, and a run nobody ever claims ends as `abandoned` at its deadline
  // rather than as a row that reads like it is still going.
  // -------------------------------------------------------------------------

  app.post('/v1/workloads/claim', async (c) => {
    const engine = await engineFrom(c.req.header('authorization'))
    if (!engine) return c.json({ error: 'This token is not valid.' }, 401)
    const suspended = await suspensionReason(options.pool, engine.orgId)
    if (suspended !== null) {
      // The same answer /v1/events gives, and for the same reason: a suspension
      // stops new work, and handing out a run to start is new work.
      return c.json({ error: `This organization is suspended: ${suspended}` }, 403)
    }

    const body = await readJson(c)
    if (body === null) return c.json({ error: 'The body is not JSON.' }, 400)
    const envId = typeof body.envId === 'string' ? body.envId.trim() : ''
    if (!envId) return c.json({ error: 'The body needs an envId.' }, 400)

    const claimed = await options.pool.withTenant({ orgId: engine.orgId }, async (db) => {
      // Overdue runs are resolved first, so a stuck run of the same workload
      // does not hold the live-run index against the one being claimed now.
      await resolveOverdueRuns(db, clock.now())
      const environments = await db.execute<{ id: string }>(rawSql`
        SELECT id FROM environments WHERE env_id = ${envId}`)
      const environmentId = environments[0]?.id
      if (!environmentId) return { unknown: true as const }

      const run = await claimRun(db, {
        environmentId,
        holder: engine.tokenId,
        now: clock.now(),
      })
      if (!run) return { run: null }

      const rows = await db.execute<Record<string, unknown>>(rawSql`
        SELECT wr.id AS "runId", w.slug AS workload, w.kind::text AS kind,
               v.version, v.body, wr.attempt,
               to_char(wr.deadline_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS "deadlineAt",
               to_char(wr.lease_expires_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS "leaseExpiresAt"
        FROM workload_runs wr
        JOIN workloads w ON w.id = wr.workload_id
        JOIN workload_versions v ON v.id = wr.workload_version_id
        WHERE wr.id = ${run.runId}`)
      return { run: rows[0] ?? null }
    })

    if ('unknown' in claimed) {
      // The same answer whether the environment belongs to another organization
      // or does not exist, for the reason /v1/environments/:envId gives.
      return c.json({ error: `No environment named ${envId} in this organization.` }, 404)
    }
    // 200 with a null run rather than 204, because a poller that reads a status
    // code and not a body is a poller that cannot tell "nothing waiting" from
    // "something went wrong with the shape".
    return c.json({ run: claimed.run })
  })

  app.post('/v1/workloads/runs/:runId/heartbeat', async (c) => {
    const engine = await engineFrom(c.req.header('authorization'))
    if (!engine) return c.json({ error: 'This token is not valid.' }, 401)
    const runId = c.req.param('runId')
    const beat = await options.pool.withTenant({ orgId: engine.orgId }, (db) =>
      heartbeat(db, { runId, holder: engine.tokenId, now: clock.now() }),
    )
    if (!beat.held) {
      // Named rather than answered with a bare 404. An engine that has lost its
      // lease is about to have its run claimed by somebody else, and it needs
      // to know that rather than to keep working and report at the end.
      return c.json(
        {
          error:
            `Run ${runId} is not held by this token. It may have finished, been cancelled, ` +
            `or had its lease taken after it expired. Stop and claim again.`,
        },
        409,
      )
    }
    // cancelRequested rides the beat, so an engine learns about a cancel on a
    // request it was making anyway rather than by polling for a command.
    return c.json({ held: true, cancelRequested: beat.cancelRequested })
  })

  app.post('/v1/commands/claim', async (c) => {
    const engine = await engineFrom(c.req.header('authorization'))
    if (!engine) return c.json({ error: 'This token is not valid.' }, 401)
    // Deliberately reachable while suspended. A suspension stops new work; a
    // teardown is the opposite of new work, and refusing it would leave a
    // suspended organization unable to stop paying for what is running.

    const body = await readJson(c)
    if (body === null) return c.json({ error: 'The body is not JSON.' }, 400)
    const envId = typeof body.envId === 'string' && body.envId.trim() !== '' ? body.envId.trim() : null
    const limit = Number.isInteger(body.limit) ? Math.min(Math.max(Number(body.limit), 1), 50) : 10

    const commands = await options.pool.withTenant({ orgId: engine.orgId }, async (db) => {
      await expireOverdueCommands(db, { now: clock.now() })
      return claimCommands(db, {
        orgId: engine.orgId,
        envId,
        holder: engine.tokenId,
        limit,
        now: clock.now(),
      })
    })
    return c.json({ commands })
  })

  app.post('/v1/commands/:id/ack', async (c) => {
    const engine = await engineFrom(c.req.header('authorization'))
    if (!engine) return c.json({ error: 'This token is not valid.' }, 401)
    const body = await readJson(c)
    if (body === null) return c.json({ error: 'The body is not JSON.' }, 400)
    const outcome = body.outcome === 'failed' ? 'failed' : body.outcome === 'done' ? 'done' : null
    if (outcome === null) {
      return c.json({ error: 'The body needs an outcome of "done" or "failed".' }, 400)
    }
    const detail = typeof body.detail === 'string' ? body.detail.slice(0, 2000) : null

    const acknowledged = await options.pool.withTenant({ orgId: engine.orgId }, (db) =>
      acknowledgeCommand(db, {
        commandId: c.req.param('id'),
        holder: engine.tokenId,
        outcome,
        detail,
        now: clock.now(),
      }),
    )
    if (!acknowledged) {
      // A 409 and a sentence rather than a 200. An acknowledgement that matched
      // no row is exactly the silent nothing the durable command exists to end,
      // and answering it with a 200 would put the old defect back one level up.
      return c.json(
        {
          error:
            'That command is not claimed by this token. It may have expired, been superseded, ' +
            'or had its lease taken after it ran out.',
        },
        409,
      )
    }
    return c.json({ acknowledged: true })
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

      // The same check for the OPERATOR cookie, and it is not redundant with
      // the one above.
      //
      // The two cookies are independent credentials in independent tables, and
      // a request can legitimately carry both. A mutation authenticated by the
      // operator cookie is checked against the operator token; the product's
      // token says nothing about it, and an operator with no product session
      // would otherwise pass this middleware having presented nothing at all.
      //
      // Why it is needed when the operator cookie is already SameSite=Strict:
      // SameSite is SITE-scoped rather than origin-scoped, so a subdomain an
      // attacker controls is inside it, which is the same sentence the block
      // above is written for. It matters more here, because the mutations
      // behind this cookie move money.
      //
      // The origin check runs FIRST and fails OPEN by design, per its own
      // comment: a request that DECLARES a cross-site origin is refused before
      // its token is looked at, and one that declares nothing is left to the
      // token. Something between a browser and this process may strip those
      // headers, and refusing every such request would break the portal for a
      // reason nobody could diagnose.
      //
      // readAdminSessionCookie, NOT readCookie with the bare name, and this
      // line was the bare name until somebody drove a browser at it.
      // adminSessionCookie writes `__Host-af_admin_session` whenever the cookie
      // is Secure, which is every deployment that matters and none of the tests
      // in this repository, because the test server speaks plain HTTP. So this
      // read returned null in production, the operator was resolved a hundred
      // lines below by readAdminSessionCookie regardless, and the entire
      // operator CSRF check was skipped on exactly the deployments it exists
      // for while admincsrf.test.ts went on passing. The suite below now
      // presents the prefixed name as well, which is the assertion that can say
      // no to this.
      const adminToken = readAdminSessionCookie(c.req.header('cookie'))
      if (adminToken) {
        const operator = await resolveAdminSession(options.pool, adminToken, clock.now())
        if (operator) {
          if (
            !looksSameOrigin(
              {
                origin: c.req.header('origin') ?? null,
                secFetchSite: c.req.header('sec-fetch-site') ?? null,
              },
              options.appBaseUrl ?? '',
            )
          ) {
            return c.json({ error: 'This operator request came from another site.' }, 403)
          }
          if (!adminCsrfMatches(adminToken, c.req.header(ADMIN_CSRF_HEADER))) {
            return c.json(
              {
                error:
                  `This operator request needs the ${ADMIN_CSRF_HEADER} header from the ` +
                  'operator session endpoint.',
              },
              403,
            )
          }
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
      // The other half of withholding the message.
      //
      // The formatter replaces an INTERNAL_SERVER_ERROR's message with a fixed
      // sentence, because whatever threw wrote it and drizzle writes the whole
      // failing statement. Redacting it without writing it down anywhere would
      // trade a leak for a blindness: the operator would have a console card
      // saying something went wrong and no way to find out what. This is where
      // it goes instead, so `af logs web` still has the diagnosis and the
      // browser does not.
      onError({ error, path, type }) {
        if (error.code !== 'INTERNAL_SERVER_ERROR') return
        console.error(`trpc ${type} ${path ?? 'unknown'}:`, error.cause ?? error)
      },
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
              sessionId: session.sessionId,
              plan: session.plan ?? 'free',
            }
          }
        }
        // The operator cookie, resolved beside the product one and never
        // instead of it. A request can legitimately carry both: an operator
        // signed in to the product as themselves is still an operator, and the
        // two sessions are independent credentials in independent tables.
        // readAdminSessionCookie, not readCookie: the cookie is written under
        // the __Host- prefix when Secure, so the bare name resolves in local
        // development and never in production. See its comment.
        const adminToken = readAdminSessionCookie(c.req.header('cookie'))
        const adminSession = adminToken
          ? await resolveAdminSession(options.pool, adminToken, clock.now())
          : null

        const context: TrpcContext = {
          pool: options.pool,
          admin: adminSession ? actorOf(adminSession) : null,
          adminPool: options.adminPool ?? null,
          clock,
          github: options.github,
          stripe: options.stripe ?? null,
          appBaseUrl: options.appBaseUrl ?? '/',
          // The sign-in mailer, deliberately. There is one way to send a
          // message from this process and one variable that configures it, so
          // an installation either can send or cannot, and a second mailer
          // would be a second thing to configure and a second thing to be
          // misconfigured.
          mailer: options.emailSignIn?.mailer ?? null,
          productName: options.emailSignIn?.productName ?? 'Antifailure',
          analytics,
          analyticsOperatorOrgSlug: options.analyticsOperatorOrgSlug ?? null,
          hostedRequiredPlan,
          operatorSetsPlan,
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

  // What answers a path with no route, when the console is not mounted to
  // answer it with a page. Registered before mountConsole rather than after,
  // because Hono holds ONE not-found handler and the last call wins: the
  // console's handler has to be able to replace this, and it answers exactly
  // this for the paths the API owns.
  //
  // Without it, an API-only deployment answered Hono's default, which is plain
  // text, for the one response a caller finding its way around is most likely
  // to get.
  app.notFound(apiNotFound)

  // The console, last, so that every API route above wins a path collision.
  // Mounted on this app rather than a second one: the session cookie the
  // browser holds is the session these pages read, and a separate origin would
  // need CORS, a second cookie policy and a place to put a token in a client.
  if (options.console !== false) {
    // Mounted whether or not the build is there. The endpoints the browser
    // needs are part of the API and do not depend on any file being present;
    // only the pages do, and a missing build is reported by the handler rather
    // than by these routes quietly not existing. They did quietly not exist
    // once, for exactly as long as it took a test to ask for one.
    mountConsole(app, {
      pool: options.pool,
      clock,
      analytics,
      secureCookies: secure,
      sealingKey: options.sealingKey ?? null,
      build: options.consoleBuild ?? {
        dir: '',
        present: false,
        summary: 'no console build was located',
      },
    })
  }

  return { app, ingestLimiter, authLimiter, metrics, analytics }
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
  result: { accepted: number; duplicates: number; rejected: number; unprojected: number },
  events: IncomingEvent[],
) {
  metrics.ingestBatches.inc({ outcome: result.rejected > 0 ? 'partial' : 'accepted' })
  metrics.ingestEvents.inc({ outcome: 'accepted' }, result.accepted)
  metrics.ingestEvents.inc({ outcome: 'duplicate' }, result.duplicates)
  metrics.ingestEvents.inc({ outcome: 'rejected' }, result.rejected)
  // A fourth label value rather than a fourth metric, so that the sum over
  // outcomes stays the number of events the batch carried. These are a subset
  // of the accepted ones: stored, and applied to no environment row because
  // the sender did not say which repository the environment belongs to. The
  // loss objective is measured on rejected and is deliberately not affected.
  metrics.ingestEvents.inc({ outcome: 'unprojected' }, result.unprojected)

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

/**
 * The rate limiter's answer, on the routes a person opens directly.
 *
 * Negotiated for the same reason the refusals above are: every caller of this
 * helper is a GET a browser navigates to, so a bare JSON body here is a line
 * of quoting on a white page at the moment somebody is already stuck.
 */
function tooMany(c: Parameters<typeof problem>[0], seconds: number) {
  c.header('retry-after', String(seconds))
  return problem(c, {
    status: 429,
    error: 'Too many attempts. Try again shortly.',
    json: { retryAfterSeconds: seconds },
    title: 'Too many attempts',
    body: [
      `This address has been asked for too many times at once. Wait about ${seconds} ` +
        'seconds and try again.',
      'Nothing is wrong with your account, and nothing has been locked.',
    ],
  })
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
