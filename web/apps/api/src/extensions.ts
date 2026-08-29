// Routes that another edition adds, and the rules that make adding one safe.
//
// ee/README promises "route and page registration" as one of the extension
// points the community edition declares, and until now the only one that
// existed was setPermissionResolver. This is the second, and it is deliberately
// the same shape: an interface here with a no-op default, an implementation
// somewhere the community build cannot see, and no reference in either
// direction that a grep would find.
//
// Three rules, and each one closes a failure this server already has a defence
// against, which an extension could otherwise walk around.
//
// A route carries its own rate limit, and the field is not optional. The
// middleware in server.ts refuses to serve any path limitFor cannot answer
// for, with a 500 that says so, because an endpoint nobody remembered to limit
// is the one nobody load tested. An extension route with no limit would
// therefore be a mounted endpoint that can never serve: dead on arrival, and
// dead in the way that looks finished. Requiring the limit at the type level
// means it cannot be forgotten, and requiring the reason string means the
// number has to be defended to whoever raises it later.
//
// A route may not claim a path the server already owns. Without this an
// extension could register GET /auth/session and receive every cookie the
// browser holds, or shadow POST /v1/events and take engine tokens. Hono matches
// in registration order, so "the core routes are added first" is not a defence
// on its own: a prefix registered before them would still win. The check is
// explicit and it is a refusal, not a warning.
//
// Registration is a function call rather than a config file, so the enterprise
// entry point imports this and calls it, and the community entry point does
// not import anything. That is what keeps the boundary checkable by grep, which
// is how CI checks it.

import type { Context } from 'hono'
import type { EndpointLimit } from './limits.ts'

export type ExtensionMethod = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'

export interface ExtensionRoute {
  method: ExtensionMethod
  /**
   * The path pattern, in the router's own syntax: `:name` matches one segment.
   *
   * No wildcards. A pattern that matches a subtree lets a route added later
   * inherit a limit chosen for something else, which is the same reason
   * ENDPOINT_LIMITS forbids them.
   */
  path: string
  /** What bounds it. Required; see the note above about why. */
  limit: EndpointLimit
  handler: (c: Context) => Response | Promise<Response>
}

export interface Extension {
  /** Named so that a refusal can say which extension caused it. */
  name: string
  routes: readonly ExtensionRoute[]
}

/**
 * Path prefixes the server owns and an extension may not take.
 *
 * Listed as prefixes rather than exact paths so that a route added to the core
 * server later is protected without anybody having to remember to add it here.
 */
const RESERVED_PREFIXES = ['/auth', '/v1', '/trpc', '/health', '/openapi'] as const

const extensions: Extension[] = []

export class ExtensionRefused extends Error {}

/**
 * Installs an extension's routes.
 *
 * Everything is checked before anything is added, so a rejected extension
 * leaves nothing half-registered. Half of an authentication feature mounted is
 * worse than none of it: the half that is there looks like it works.
 */
export function registerExtension(extension: Extension): void {
  if (!extension.name) throw new ExtensionRefused('An extension needs a name.')
  if (extensions.some((e) => e.name === extension.name)) {
    throw new ExtensionRefused(
      `An extension named ${extension.name} is already registered. ` +
        `Registering twice would mount every route twice and the first would win silently.`,
    )
  }

  const taken = new Set(extensionRoutes().map((r) => `${r.method} ${r.path}`))

  for (const route of extension.routes) {
    const where = `${extension.name}: ${route.method} ${route.path}`

    if (!route.path.startsWith('/')) {
      throw new ExtensionRefused(`${where} is not an absolute path.`)
    }
    if (route.path.includes('*')) {
      throw new ExtensionRefused(
        `${where} uses a wildcard. Name each path, so that a route added later ` +
          `cannot inherit a rate limit chosen for something else.`,
      )
    }
    for (const prefix of RESERVED_PREFIXES) {
      if (route.path === prefix || route.path.startsWith(`${prefix}/`)) {
        throw new ExtensionRefused(
          `${where} is under ${prefix}, which the server owns. An extension that ` +
            `served a path here could shadow sign-in or ingestion and receive their credentials.`,
        )
      }
    }
    if (!route.limit || !(route.limit.rate > 0) || !(route.limit.burst > 0)) {
      throw new ExtensionRefused(
        `${where} has no usable rate limit. Every endpoint declares one, because the ` +
          `server refuses to serve a path it has no limit for.`,
      )
    }
    if (!route.limit.reason) {
      throw new ExtensionRefused(
        `${where} declares a limit with no reason. The reason is read by whoever raises it.`,
      )
    }

    const signature = `${route.method} ${route.path}`
    if (taken.has(signature)) {
      throw new ExtensionRefused(`${where} is already served by another extension.`)
    }
    taken.add(signature)
  }

  extensions.push(extension)
}

export function registeredExtensions(): readonly Extension[] {
  return extensions
}

export function extensionRoutes(): readonly ExtensionRoute[] {
  return extensions.flatMap((e) => e.routes)
}

/** Removes everything registered. For tests, which need each case to start
 *  from an empty server rather than from whatever ran before it. */
export function clearExtensions(): void {
  extensions.length = 0
}

// ---------------------------------------------------------------------------
// The sign-in policy
//
// The other half of what ee/README calls the authentication extension point.
// Routes let another edition add a way IN; this lets it have an opinion about
// the way in that already exists.
//
// The case it is here for: an organization that has turned on single sign-on
// and required it. GitHub sign-in must then stop being a way into that
// organization, or requiring single sign-on means nothing at all.
//
// The shape is deliberately not "allow or refuse". A policy returns the
// organization a session may be scoped to, and returning null is not a
// rejection: it is "signed in, no tenant", a state this server already models
// and handles, where the person is authenticated and every procedure that needs
// an organization declines. That matters because refusing the sign-in outright
// would leave somebody locked out with no session at all, and therefore no way
// to present a recovery code, which is the situation the recovery code exists
// for. Signed in with no tenant is the state a break-glass flow can start from.
//
// One policy rather than a list, for the same reason as the permission
// resolver: combining two would need a rule, and every such rule either lets
// one widen access by accident or makes adding one break the others.
// ---------------------------------------------------------------------------

export interface SignInAttempt {
  userId: string
  /** The organization the sign-in would land in, when there is exactly one. */
  orgId: string | null
  /** How they authenticated. Today there is one value; naming it means a
   *  policy written now does not have to be revisited to stay correct. */
  method: 'github'
}

export interface SignInDecision {
  /** The organization the session may be scoped to. Null means signed in with
   *  no tenant. */
  orgId: string | null
  /** A short machine-readable note appended to the return URL, so the page can
   *  say why the person landed with no organization. Must be safe in a query
   *  string: letters, digits, dash and underscore only. */
  note?: string | null
}

export type SignInPolicy = (attempt: SignInAttempt) => Promise<SignInDecision>

let signInPolicy: SignInPolicy | null = null

export function setSignInPolicy(next: SignInPolicy | null): void {
  signInPolicy = next
}

export function hasSignInPolicy(): boolean {
  return signInPolicy !== null
}

/**
 * The decision for one sign-in.
 *
 * A policy that throws leaves the sign-in exactly as it would have been without
 * one. That direction is deliberate and it is the opposite of the permission
 * resolver's: there, failing open would grant access, so failure falls back to
 * the built-in table. Here, failing closed would lock every member of an
 * organization out because a policy had a bug, and the thing being decided is
 * which tenant a session lands in rather than what it may do. Row-level
 * security still applies to every statement that session makes.
 */
export async function decideSignIn(attempt: SignInAttempt): Promise<SignInDecision> {
  if (!signInPolicy) return { orgId: attempt.orgId }
  try {
    const decision = await signInPolicy(attempt)
    return {
      orgId: decision.orgId,
      note: decision.note && /^[A-Za-z0-9_-]{1,64}$/.test(decision.note) ? decision.note : null,
    }
  } catch {
    return { orgId: attempt.orgId }
  }
}
