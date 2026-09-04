// The console's routes.
//
// Two things live here, and nothing else.
//
// 1. The console's build, served from this process so that the SameSite=Lax
//    session cookie the browser already holds is the session the application
//    reads. See ./static.ts for why that constraint decides the architecture.
//
// 2. The handful of JSON endpoints the console needs that are not tRPC.
//    Provider keys are the whole list: /v1/providers authenticates a Bearer
//    token because it exists for `af provider`, and a browser has a cookie
//    rather than a token. Rather than teach one endpoint two authentication
//    schemes -- which is how an endpoint ends up accepting the weaker one --
//    the browser gets its own, with the same role gate and the same audit
//    origin.
//
// What used to live here was 1,400 lines of hand-written HTML. It rendered,
// it was tested, and in a browser it was unstyled text for a week because the
// API's global middleware overwrote the Content-Security-Policy these routes
// set and the test asserted a substring both policies contained. The
// replacement is a real application in console/, and the escaping that file
// did by hand is now done by React by construction.

import type { Context, Hono } from 'hono'
import type { ApiEnv } from '../env.ts'
import type { Pool } from '@antifailure/db'
import type { Analytics } from '../analytics/record.ts'
import type { Clock } from '../clock.ts'
import {
  CSRF_HEADER,
  SESSION_COOKIE,
  csrfMatches,
  readCookie,
  resolveSession,
} from '../auth/session.ts'
import { PROVIDERS, type Provider } from '../providers/seal.ts'
import {
  listBudgets,
  listKeys,
  MAY_MANAGE_KEYS,
  ProviderKeyError,
  revokeKey,
  saveKey,
  setBudget,
} from '../providers/store.ts'
import { readAsset, type ConsoleBuild } from './static.ts'
import { consoleClass } from '../limits.ts'
import { apiNotFound } from '../notfound.ts'

export interface ConsoleOptions {
  pool: Pool
  clock: Clock
  secureCookies: boolean
  /** The secret that seals provider keys, or null when none is configured.
   *  Null does not hide the page: it shows why a key cannot be stored, which
   *  is more useful than a form that fails on submit. */
  sealingKey?: Buffer | null
  /** The exported console. A build that is absent is reported, never faked. */
  build: ConsoleBuild
  /** Where the analytics event goes when a key is stored from these pages. */
  analytics: Analytics
}

/**
 * The console's Content-Security-Policy, and the one concession in it.
 *
 * 'unsafe-inline' on script-src is real and is stated rather than buried: a
 * Next.js static export bootstraps itself from an inline <script> whose
 * contents change with every build, so there is no stable hash to allow-list
 * and no server to mint a nonce. The alternatives were a server-rendered
 * console (which this replaces) or shipping a policy that blocks the
 * application from starting.
 *
 * Everything else stays shut. No 'unsafe-eval', no third-party origin of any
 * kind, connect-src is this origin only, and frame-ancestors is none, so the
 * console cannot be framed and cannot talk to anywhere else.
 */
export const CONSOLE_CSP = [
  "default-src 'none'",
  "script-src 'self' 'unsafe-inline'",
  "style-src 'self' 'unsafe-inline'",
  "img-src 'self' data: https://avatars.githubusercontent.com",
  "font-src 'self'",
  "connect-src 'self'",
  "form-action 'self'",
  "base-uri 'none'",
  "frame-ancestors 'none'",
].join('; ')

interface Viewer {
  userId: string
  label: string
  organization: string | null
  role: string | null
}

export function mountConsole(app: Hono<ApiEnv>, options: ConsoleOptions): void {
  const { pool, clock, build } = options

  async function viewerFor(c: Context): Promise<Viewer | null> {
    const token = readCookie(c.req.header('cookie'), SESSION_COOKIE)
    if (!token) return null
    const session = await resolveSession(pool, clock, token)
    if (!session) return null
    return {
      userId: session.userId,
      label: session.label,
      organization: session.orgId,
      role: session.role ?? null,
    }
  }

  function csrfOk(c: Context): boolean {
    const token = readCookie(c.req.header('cookie'), SESSION_COOKIE)
    if (!token) return false
    return csrfMatches(token, c.req.header(CSRF_HEADER))
  }

  /**
   * Session, tenant, CSRF and role, in that order, for every write below.
   *
   * The role check is here rather than only in the interface. A control the
   * page does not render is not a permission: the endpoint takes a request
   * from anything that can send one, and a member who has seen this page in
   * another role knows the shape of the body.
   */
  async function writer(c: Context): Promise<Viewer | Response> {
    const viewer = await viewerFor(c)
    if (!viewer) return c.json({ error: 'Sign in first.' }, 401)
    if (!viewer.organization) {
      return c.json({ error: 'You are not a member of an organization yet.' }, 403)
    }
    if (!csrfOk(c)) {
      return c.json({ error: `This request needs the ${CSRF_HEADER} header from GET /auth/session.` }, 403)
    }
    if (!MAY_MANAGE_KEYS.has(viewer.role ?? '')) {
      return c.json(
        {
          error:
            `Changing a provider key needs owner or admin. You are ${viewer.role ?? 'not a member'}.`,
        },
        403,
      )
    }
    return viewer
  }

  function isResponse(v: unknown): v is Response {
    return v instanceof Response
  }

  function providerParam(raw: string | undefined): Provider | null {
    return PROVIDERS.includes(raw as Provider) ? (raw as Provider) : null
  }

  // ---- provider keys, for the browser -------------------------------------

  app.get('/console/api/providers', async (c) => {
    const viewer = await viewerFor(c)
    if (!viewer) return c.json({ error: 'Sign in first.' }, 401)
    if (!viewer.organization) {
      return c.json({ error: 'You are not a member of an organization yet.' }, 403)
    }
    const [keys, budgets] = await Promise.all([
      listKeys(pool, viewer.organization),
      listBudgets(pool, clock, viewer.organization),
    ])
    return c.json({
      // Whether a key CAN be stored at all, reported rather than discovered on
      // a failed write.
      sealing: Boolean(options.sealingKey),
      mayManage: MAY_MANAGE_KEYS.has(viewer.role ?? ''),
      role: viewer.role,
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

  app.put('/console/api/providers/:provider', async (c) => {
    const viewer = await writer(c)
    if (isResponse(viewer)) return viewer
    const provider = providerParam(c.req.param('provider'))
    if (!provider) {
      return c.json({ error: `Unknown provider. Known: ${PROVIDERS.join(', ')}.` }, 400)
    }
    if (!options.sealingKey) {
      // Refused rather than stored in the clear. An installation with no
      // sealing secret has nowhere safe to put this.
      return c.json(
        {
          error:
            'This control plane has no sealing secret, so a key cannot be stored. ' +
            'Set AF_PROVIDER_KEY_SECRET.',
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
    if (!key.trim()) return c.json({ error: 'No key was given.' }, 400)

    try {
      const result = await saveKey(pool, clock, options.sealingKey, {
        analytics: options.analytics,
        orgId: viewer.organization!,
        provider,
        key,
        actorUserId: viewer.userId,
        actorLabel: viewer.label,
        origin: 'web',
      })
      return c.json({
        provider,
        last4: result.stored.last4,
        replaced: result.replaced,
        sameAsBefore: result.sameAsBefore,
      })
    } catch (err) {
      // The message says what is wrong with the key WITHOUT quoting it. A
      // validation error that echoes the value is how a secret reaches a log.
      if (err instanceof ProviderKeyError) return c.json({ error: err.message }, 400)
      throw err
    }
  })

  app.delete('/console/api/providers/:provider', async (c) => {
    const viewer = await writer(c)
    if (isResponse(viewer)) return viewer
    const provider = providerParam(c.req.param('provider'))
    if (!provider) {
      return c.json({ error: `Unknown provider. Known: ${PROVIDERS.join(', ')}.` }, 400)
    }
    const { revoked } = await revokeKey(pool, clock, {
      orgId: viewer.organization!,
      provider,
      actorLabel: viewer.label,
      actorUserId: viewer.userId,
      origin: 'web',
    })
    // 200 whether or not there was one: removing a key has to be idempotent.
    return c.json({ provider, revoked })
  })

  app.put('/console/api/providers/:provider/budget', async (c) => {
    const viewer = await writer(c)
    if (isResponse(viewer)) return viewer
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
    // Not Number(body.capUsd): Number(null) and Number('') are both 0, so a
    // missing field would set a cap of zero dollars rather than complain.
    // Zero is a legitimate cap, which is exactly what makes it dangerous to
    // infer -- nothing looks wrong until every run refuses as overspent.
    const raw = body.capUsd
    const cap =
      typeof raw === 'number' ? raw : typeof raw === 'string' && raw.trim() !== '' ? Number(raw) : NaN
    if (!Number.isFinite(cap) || cap < 0) {
      return c.json({ error: 'Give a number of US dollars, zero or more.' }, 400)
    }
    const budget = await setBudget(pool, clock, {
      analytics: options.analytics,
      orgId: viewer.organization!,
      provider,
      capUsd: cap,
      actorLabel: viewer.label,
      actorUserId: viewer.userId,
      origin: 'web',
    })
    return c.json({
      provider,
      period: budget.period,
      capUsd: budget.capUsd,
      spentUsd: budget.spentUsd,
      remainingUsd: budget.remainingUsd,
    })
  })

  // ---- the application ----------------------------------------------------

  /**
   * Anything the API did not claim.
   *
   * Registered as Hono's not-found handler rather than as a wildcard route, so
   * that ordering cannot go wrong: every route declared anywhere on this app,
   * before or after this call, wins. A wildcard would silently swallow a route
   * added below it later.
   */
  app.notFound(async (c) => {
    // Whether this is the API's space or the console's, asked with the one
    // predicate that already answers it: consoleClass returns a class for the
    // paths a browser asks for pages in, and null for everything the API owns,
    // which is every method that changes something plus every path under a
    // prefix in API_PREFIXES.
    //
    // Written as one question rather than as a method check, because a GET to
    // a path under /v1 that does not exist is an API request and has to answer
    // like one. It used to reach the rate limit gate and be answered 500,
    // which is the defect this replaces; sending it here instead and then
    // handing it the console's HTML would only trade one wrong answer for
    // another, quieter one.
    if (consoleClass(c.req.method, c.req.path) === null) return apiNotFound(c)
    if (!build.present) {
      // Said plainly, in the response as well as the log. A blank 404 here
      // looks exactly like a routing bug and is not one.
      c.header('content-type', 'text/plain; charset=utf-8')
      c.header('cache-control', 'no-store')
      return c.body(
        'This control plane was started without the console build. The API is\n' +
          'serving normally. Build console/ and set AF_CONSOLE_DIR, or run the\n' +
          'published image, which carries it.\n',
        503,
      )
    }

    const asset = await readAsset(build, c.req.path)
    if (!asset) {
      return c.json(
        {
          error:
            'No page at this path, and this build carries no 404 page to render ' +
            'instead. Start at / for the console.',
        },
        404,
      )
    }

    c.header('content-type', asset.contentType)
    c.header('cache-control', asset.cacheControl)
    c.header('etag', asset.etag)
    c.header('content-security-policy', CONSOLE_CSP)
    c.header('x-frame-options', 'DENY')
    c.header('x-content-type-options', 'nosniff')
    c.header('referrer-policy', 'strict-origin-when-cross-origin')

    // A conditional request that already has this exact body gets nothing back.
    if (c.req.header('if-none-match') === asset.etag) return c.body(null, 304)
    if (c.req.method === 'HEAD') return c.body(null, asset.status)
    return c.body(asset.body as unknown as ArrayBuffer, asset.status)
  })
}
