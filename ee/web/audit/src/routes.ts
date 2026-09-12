// Where an organization chooses its own collector.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// THE ENTRY POINT THAT DID NOT EXIST. destinations.ts can store a customer's
// destination and the forwarder can deliver to it, and a table with a writer
// that no request can reach is the shape this repository keeps finding in its
// own tree: a complete looking schema with zero writers. These four routes are
// that writer, and the integration suite drives them over HTTP rather than
// calling destinations.ts directly, so a route that stopped reaching the store
// would be a red test and not a quiet one.
//
// Two gates, and neither is the other. `gated` in ee/web/server wraps every
// route here in the INSTALLATION licence, per request. `permitted`, supplied by
// the host, is the ORGANIZATION'S entitlement from the control plane's own
// catalogue, which an operator grants or withdraws with no deploy. The forwarder
// asks the same two questions on every pass, so an organization that can
// configure a destination is exactly an organization whose entries would reach
// it.
//
// WHO MAY DO WHAT. Any member may READ, because the answer carries a URL, a last
// four and a fingerprint and no credential, and a member who can see that the
// stream is failing can tell why without asking somebody. Only an owner or an
// admin may change it, the same two roles web/apps/api/src/providers/store.ts
// allows to manage a provider key, and for a sharper reason here: changing an
// organization's audit destination is how somebody who has taken over an account
// would stop the organization's security team seeing what they do next.
//
// THE CREDENTIAL IS REQUIRED ON EVERY SAVE, INCLUDING A URL CHANGE, and that is a
// security decision rather than an inconvenience. If the URL could be changed
// while the stored credential was kept, a stolen admin session could point the
// destination at a host it controls and receive the organization's Splunk token
// in the Authorization header of the very next delivery. Requiring the
// credential alongside the URL means changing where the credential goes requires
// already having it. Switching the stream on or off touches neither, and is a
// separate route for that reason.

import {
  CSRF_HEADER,
  SESSION_COOKIE,
  csrfMatches,
  readCookie,
  resolveSession,
  type Clock,
  type Context,
  type EndpointLimit,
  type Extension,
  type ResolvedSession,
} from '@antifailure/api'
import type { Pool } from '@antifailure/db'
import {
  DestinationRefused,
  delivery,
  isKind,
  KINDS,
  read,
  remove,
  save,
  setEnabled,
  type Delivery,
  type Destination,
} from './destinations.ts'

/** The roles that may change an organization's destination. */
export const MAY_CONFIGURE: ReadonlySet<string> = new Set(['owner', 'admin'])

/** The one path. A single resource per organization, which is what the table
 *  is: one destination, keyed on the organization the session is in. There is
 *  no organization in the path, because a path that named one would be a path
 *  somebody could put another organization's id in. */
export const PATH = '/enterprise/audit-stream'

export interface AuditStreamRoutesOptions {
  pool: Pool
  clock: Clock
  /** The key credentials are sealed under, or null when AF_PROVIDER_KEY_SECRET
   *  is unset. Null mounts the routes anyway: reading answers, and saving
   *  refuses with 503 naming the variable, because a 404 would be
   *  indistinguishable from a build that never had the feature. */
  sealingKey: Buffer | null
  /** Whether one organization is entitled to audit streaming right now. */
  permitted: (orgId: string, now: Date) => Promise<boolean>
  log?: (line: string) => void
}

const READ_LIMIT: EndpointLimit = {
  rate: 2,
  burst: 10,
  key: 'org',
  reason: 'A settings page an administrator opens, and refreshes while waiting for a delivery.',
}

const WRITE_LIMIT: EndpointLimit = {
  rate: 1,
  burst: 5,
  key: 'org',
  reason:
    'Configuring a collector is a handful of attempts while somebody gets a URL and a token ' +
    'right. Anything faster is somebody trying destinations, which is worth slowing down.',
}

export function auditStreamExtension(options: AuditStreamRoutesOptions): Extension {
  return {
    name: 'audit-stream',
    routes: [
      { method: 'GET', path: PATH, limit: READ_LIMIT, handler: (c) => handle(c, options, false, show) },
      { method: 'PUT', path: PATH, limit: WRITE_LIMIT, handler: (c) => handle(c, options, true, put) },
      { method: 'PATCH', path: PATH, limit: WRITE_LIMIT, handler: (c) => handle(c, options, true, patch) },
      { method: 'DELETE', path: PATH, limit: WRITE_LIMIT, handler: (c) => handle(c, options, true, del) },
    ],
  }
}

type Handler = (
  c: Context,
  options: AuditStreamRoutesOptions,
  session: ResolvedSession & { orgId: string },
) => Promise<Response>

/**
 * Authentication, the organization, the entitlement and the role, in that order,
 * for every route. One function rather than four copies, because a check written
 * four times is a check one route will eventually be missing.
 *
 * An unexpected failure is logged and answered with a 500 that carries no detail.
 * Nothing in this package ever puts a credential in an error message, and the
 * response still says nothing, because the rule that keeps a secret out of a
 * response has to hold for the failure nobody predicted too.
 */
async function handle(
  c: Context,
  options: AuditStreamRoutesOptions,
  mutating: boolean,
  next: Handler,
): Promise<Response> {
  try {
    const token = readCookie(c.req.header('cookie'), SESSION_COOKIE)
    const session = token ? await resolveSession(options.pool, options.clock, token) : null
    if (!token || !session) return c.json({ error: 'Sign in first.' }, 401)
    if (!session.orgId) {
      return c.json({ error: 'Choose an organization first. A destination belongs to one.' }, 403)
    }
    if (mutating && !csrfMatches(token, c.req.header(CSRF_HEADER))) {
      // The community server applies its cross site check to /trpc only, so a
      // route mounted beside it does its own. Without this, a page on another
      // origin could repoint a signed in administrator's audit stream.
      return c.json({ error: `This request needs the ${CSRF_HEADER} header from GET /auth/session.` }, 403)
    }
    if (!(await options.permitted(session.orgId, options.clock.now()))) {
      return c.json(
        {
          error: 'not_entitled',
          feature: 'audit_stream',
          detail:
            'This organization is not entitled to audit streaming. Ask an owner to change the ' +
            'plan, or an operator to grant it. Nothing was changed and nothing was removed.',
        },
        402,
      )
    }
    if (mutating && !MAY_CONFIGURE.has(session.role ?? '')) {
      return c.json(
        { error: 'Only an owner or an admin can change where this organization\'s audit log goes.' },
        403,
      )
    }
    return await next(c, options, session as ResolvedSession & { orgId: string })
  } catch (err) {
    if (err instanceof DestinationRefused) return c.json({ error: err.message }, 400)
    ;(options.log ?? ((line: string) => console.error(line)))(
      `audit stream: ${c.req.method} ${PATH} failed: ${err instanceof Error ? err.name : 'unknown'}`,
    )
    return c.json({ error: 'The audit stream settings could not be changed. Nothing was saved.' }, 500)
  }
}

/** A destination as the response carries it. Built field by field from a type
 *  that has no ciphertext in it, so this cannot become the place one leaks. */
function view(destination: Destination | null, state: Delivery | null, sealing: boolean) {
  return {
    destination: destination && {
      kind: destination.kind,
      url: destination.url,
      indexName: destination.indexName,
      sourcetype: destination.sourcetype,
      enabled: destination.enabled,
      fromSeq: destination.fromSeq,
      credential: { last4: destination.last4, fingerprint: destination.fingerprint },
      createdAt: destination.createdAt.toISOString(),
      updatedAt: destination.updatedAt.toISOString(),
    },
    delivery: state && {
      deliveredSeq: state.deliveredSeq,
      lastAttemptAt: state.lastAttemptAt?.toISOString() ?? null,
      lastDeliveredAt: state.lastDeliveredAt?.toISOString() ?? null,
      lastError: state.lastError,
      consecutiveFailures: state.consecutiveFailures,
    },
    // Whether a destination can be saved on this control plane at all, so a
    // screen can say why before somebody types a token into it.
    canStoreCredentials: sealing,
  }
}

const show: Handler = async (c, options, session) => {
  const [destination, state] = await Promise.all([
    read(options.pool, session.orgId),
    delivery(options.pool, session.orgId),
  ])
  return c.json(view(destination, state, options.sealingKey !== null))
}

async function body(c: Context): Promise<Record<string, unknown> | null> {
  try {
    const parsed: unknown = await c.req.json()
    return parsed !== null && typeof parsed === 'object' && !Array.isArray(parsed)
      ? (parsed as Record<string, unknown>)
      : null
  } catch {
    return null
  }
}

function optionalText(value: unknown): string | null {
  return typeof value === 'string' && value.trim() !== '' ? value.trim() : null
}

const put: Handler = async (c, options, session) => {
  if (!options.sealingKey) {
    return c.json(
      {
        error:
          'This control plane cannot store a collector credential, because AF_PROVIDER_KEY_SECRET ' +
          'is not set. An operator has to set it before any organization can choose a destination.',
      },
      503,
    )
  }
  const input = await body(c)
  if (!input) return c.json({ error: 'The body is not a JSON object.' }, 400)
  const kind = typeof input.kind === 'string' ? input.kind : ''
  if (!isKind(kind)) {
    return c.json({ error: `kind has to be one of ${KINDS.join(', ')}.` }, 400)
  }
  const url = typeof input.url === 'string' ? input.url.trim() : ''
  const credential = typeof input.credential === 'string' ? input.credential : ''
  if (!url || !credential) {
    return c.json(
      {
        error:
          'Send the url and the credential together. The credential is required on every save, ' +
          'including a change of URL, so that where it is sent cannot change without it.',
      },
      400,
    )
  }
  if (input.enabled !== undefined && typeof input.enabled !== 'boolean') {
    return c.json({ error: 'enabled has to be true or false.' }, 400)
  }

  const saved = await save(
    options.pool,
    options.sealingKey,
    {
      orgId: session.orgId,
      kind,
      url,
      credential,
      indexName: optionalText(input.indexName),
      sourcetype: optionalText(input.sourcetype),
      enabled: input.enabled as boolean | undefined,
      actorUserId: session.userId,
      actorLabel: session.label,
      origin: 'web',
    },
    options.clock.now(),
  )
  const state = await delivery(options.pool, session.orgId)
  return c.json(view(saved, state, true))
}

const patch: Handler = async (c, options, session) => {
  const input = await body(c)
  if (!input || typeof input.enabled !== 'boolean') {
    return c.json({ error: 'Send {"enabled": true} or {"enabled": false}.' }, 400)
  }
  const saved = await setEnabled(
    options.pool,
    {
      orgId: session.orgId,
      enabled: input.enabled,
      actorUserId: session.userId,
      actorLabel: session.label,
      origin: 'web',
    },
    options.clock.now(),
  )
  if (!saved) return c.json({ error: 'This organization has no destination to switch.' }, 404)
  const state = await delivery(options.pool, session.orgId)
  return c.json(view(saved, state, options.sealingKey !== null))
}

const del: Handler = async (c, options, session) => {
  const removed = await remove(options.pool, {
    orgId: session.orgId,
    actorUserId: session.userId,
    actorLabel: session.label,
    origin: 'web',
  })
  if (!removed) return c.json({ error: 'This organization has no destination to remove.' }, 404)
  return c.json({ removed: true })
}
