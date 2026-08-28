// The console's routes.
//
// Mounted on the same Hono app as the API, deliberately, so that the session
// cookie the browser already has is the session these pages read. A separate
// origin would need CORS, a second cookie policy, and a place to put a token in
// a client, and every one of those is somewhere a session can be mishandled.
//
// THE CONTENT SECURITY POLICY IS DIFFERENT HERE, and it has to be. The API sets
// `default-src 'none'` on every response because it returns JSON and renders
// nothing, which is exactly right for the API and would stop the console
// loading its own stylesheet. So these routes set their own, still strict: no
// inline script, no eval, no third-party anything, and frame-ancestors none.
//
// There is no JavaScript on any of these pages. Not as an aesthetic: every
// interaction here is a form submission, which means the console works with
// scripting disabled, cannot get into a state where the page and the server
// disagree, and has no bundle to keep in step with the API it reads.

import type { Context, Hono } from 'hono'
import type { Pool } from '@antifailure/db'
import type { Clock } from '../clock.ts'
import {
  CSRF_HEADER,
  SESSION_COOKIE,
  csrfMatches,
  readCookie,
  resolveSession,
  revokeSession,
  clearedCookie,
} from '../auth/session.ts'
import { approveDeviceCode, denyDeviceCode, describePending, DeviceError, normaliseUserCode } from '../auth/device.ts'
import { CONSOLE_CSS, CONSOLE_ICON, html, page, type Viewer } from './layout.ts'
import {
  auditPage,
  devicePage,
  environmentPage,
  environmentsPage,
  maskingPage,
  membersPage,
  networkPage,
  noOrganizationPage,
  runPage,
  runsPage,
  signInPage,
} from './pages.ts'

export interface ConsoleOptions {
  pool: Pool
  clock: Clock
  secureCookies: boolean
}

// Strict, and it names every source rather than relying on a default. The
// stylesheet is a same-origin file so it needs no 'unsafe-inline'; adding that
// to save one request would undo most of the value of having a policy.
const CONSOLE_CSP = [
  "default-src 'none'",
  "style-src 'self'",
  "img-src 'self' data:",
  "form-action 'self'",
  "base-uri 'none'",
  "frame-ancestors 'none'",
].join('; ')

export function mountConsole(app: Hono, options: ConsoleOptions): void {
  const { pool, clock } = options

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
      csrfToken: session.csrfToken,
    }
  }

  /** Same-origin form posts get the same CSRF treatment as the API's mutations. */
  function csrfOk(c: Context, submitted: string | undefined): boolean {
    const token = readCookie(c.req.header('cookie'), SESSION_COOKIE)
    if (!token) return false
    return csrfMatches(token, submitted ?? c.req.header(CSRF_HEADER))
  }

  function send(c: Context, body: { toString(): string }, status = 200) {
    c.header('content-type', 'text/html; charset=utf-8')
    c.header('content-security-policy', CONSOLE_CSP)
    c.header('x-frame-options', 'DENY')
    c.header('referrer-policy', 'strict-origin-when-cross-origin')
    // A page rendered for one session must never be served to another.
    c.header('cache-control', 'no-store')
    return c.body(String(body), status as 200)
  }

  // ---- static -------------------------------------------------------------

  app.get('/console/console.css', (c) => {
    c.header('content-type', 'text/css; charset=utf-8')
    c.header('cache-control', 'public, max-age=300')
    return c.body(CONSOLE_CSS)
  })

  app.get('/console/icon.svg', (c) => {
    c.header('content-type', 'image/svg+xml')
    c.header('cache-control', 'public, max-age=86400')
    return c.body(CONSOLE_ICON)
  })

  // ---- the pages that need a tenant ---------------------------------------

  /**
   * Every page below needs a signed-in viewer with an organization.
   *
   * Signed in with no organization is a REAL state, not an error: somebody on
   * the allowlist who has not been invited anywhere. It gets its own page,
   * because showing them an empty environments table would make a correct
   * system look broken.
   */
  function guarded(
    render: (c: Context, viewer: Viewer, orgId: string) => Promise<{ toString(): string } | null>,
  ) {
    return async (c: Context) => {
      const viewer = await viewerFor(c)
      if (!viewer) return send(c, signInPage(), 401)
      if (!viewer.organization) return send(c, noOrganizationPage(viewer), 200)
      const body = await render(c, viewer, viewer.organization)
      if (body === null) return send(c, notFound(viewer), 404)
      return send(c, body)
    }
  }

  app.get('/', async (c) => {
    const viewer = await viewerFor(c)
    if (!viewer) return send(c, signInPage())
    return c.redirect('/environments', 302)
  })

  app.get('/environments', guarded((_c, v, org) => environmentsPage(pool, v, org)))
  app.get('/environments/:envId', guarded((c, v, org) => environmentPage(pool, v, org, c.req.param('envId') ?? '')))
  app.get('/runs', guarded((_c, v, org) => runsPage(pool, v, org)))
  app.get('/runs/:runId', guarded(async (c, v, org) => {
    const id = c.req.param('runId') ?? ''
    // A run id is a uuid. Anything else is a 404 rather than a database error,
    // because ::uuid on a bad string raises and would surface as a 500.
    if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(id)) return null
    return runPage(pool, v, org, id)
  }))
  app.get('/masking', guarded((_c, v, org) => maskingPage(pool, v, org)))
  app.get('/network', guarded((_c, v, org) => networkPage(pool, v, org)))
  app.get('/audit', guarded((_c, v, org) => auditPage(pool, v, org)))
  app.get('/settings/members', guarded((_c, v, org) => membersPage(pool, v, org)))

  // ---- approving a terminal ----------------------------------------------

  app.get('/device', async (c) => {
    const viewer = await viewerFor(c)
    if (!viewer) {
      // Straight into sign-in, and back here afterwards with the code intact,
      // so somebody who clicked the link from their terminal does not have to
      // find their way back and retype it.
      const asked = c.req.query('code') ?? ''
      const back = asked ? `/device?code=${encodeURIComponent(asked)}` : '/device'
      return c.redirect(`/auth/github?redirect_to=${encodeURIComponent(back)}`, 302)
    }
    const code = normaliseUserCode(c.req.query('code') ?? '')
    const pending = code ? await describePending(pool, clock, code) : null
    return send(
      c,
      devicePage({
        viewer,
        code: code || (c.req.query('code') ?? ''),
        pending,
        error: code && !pending ? 'It has expired, been used, or was never issued.' : undefined,
      }),
    )
  })

  app.post('/console/device', async (c) => {
    const viewer = await viewerFor(c)
    if (!viewer) return c.redirect('/device', 302)

    const form = await c.req.formData()
    const submittedCsrf = String(form.get('csrf') ?? '')
    if (!csrfOk(c, submittedCsrf)) {
      return send(c, devicePage({ viewer, code: '', pending: null, error: 'Start again from your terminal.' }), 403)
    }

    const rawCode = String(form.get('user_code') ?? '')
    const code = normaliseUserCode(rawCode)
    const decision = String(form.get('decision') ?? 'lookup')

    if (!code) {
      return send(c, devicePage({ viewer, code: rawCode, pending: null, error: 'A code is eight characters.' }), 400)
    }

    if (decision === 'lookup') {
      const pending = await describePending(pool, clock, code)
      return send(
        c,
        devicePage({
          viewer,
          code,
          pending,
          error: pending ? undefined : 'It has expired, been used, or was never issued.',
        }),
        pending ? 200 : 404,
      )
    }

    if (decision === 'deny') {
      await denyDeviceCode(pool, clock, code)
      return send(c, devicePage({ viewer, code, pending: null, denied: true }))
    }

    if (!viewer.organization) {
      return send(c, noOrganizationPage(viewer), 403)
    }

    try {
      await approveDeviceCode(pool, clock, {
        userCode: code,
        userId: viewer.userId,
        orgId: viewer.organization,
        actorLabel: viewer.label,
      })
    } catch (err) {
      if (err instanceof DeviceError) {
        return send(c, devicePage({ viewer, code, pending: null, error: err.message }), 400)
      }
      throw err
    }
    return send(c, devicePage({ viewer, code, pending: null, approved: true }))
  })

  // ---- signing out --------------------------------------------------------

  app.post('/console/signout', async (c) => {
    const form = await c.req.formData()
    if (!csrfOk(c, String(form.get('csrf') ?? ''))) return c.redirect('/', 302)
    const token = readCookie(c.req.header('cookie'), SESSION_COOKIE)
    if (token) await revokeSession(pool, token)
    c.header('set-cookie', clearedCookie(options.secureCookies))
    return c.redirect('/', 302)
  })
}

/**
 * A 404 that keeps the rail, so somebody who followed a stale link is one click
 * from somewhere real rather than at a dead end.
 */
function notFound(viewer: Viewer) {
  return page(
    { title: 'Not found', viewer },
    html`
      <div class="page">
        <div class="page-head">
          <div class="eyebrow">404</div>
          <h1>That is not here</h1>
          <p>
            It may have been torn down, or it may belong to another organization. Those two
            look the same from here on purpose: telling them apart would be a way to ask
            whether somebody else has a thing by that name.
          </p>
        </div>
        <a class="btn" href="/environments">Back to environments</a>
      </div>
    `,
  )
}
