// The limit on every public endpoint, in one list.
//
// The list is the point. Rate limiting that is applied where somebody
// remembered to apply it is rate limiting that is missing from whichever
// endpoint was added last, and the endpoint added last is the one nobody has
// load tested. So the limits live here, the middleware reads them here, and a
// test walks the server's own route table and fails on anything not named.
//
// Three keys, because they answer different questions. A per-token limit stops
// one engine from filling the queue. A per-organization limit stops one
// customer from consuming the instance. A per-address limit stops somebody with
// no account at all, which is the only key available on the sign-in path,
// precisely because that is where an attacker without an account operates.

import { extensionRoutes } from './extensions.ts'

export type LimitKey = 'ip' | 'token' | 'org'

export interface EndpointLimit {
  /** Requests per second, sustained. */
  rate: number
  /** How many may arrive at once before the sustained rate applies. */
  burst: number
  key: LimitKey
  /** Why this number and not another. Read by whoever raises it later. */
  reason: string
}

/**
 * Every endpoint the server exposes, and what bounds it.
 *
 * Keyed by "METHOD /path" for the plain HTTP routes and by the procedure path
 * for tRPC. Wildcards are not allowed: a pattern would let a new endpoint
 * inherit a limit chosen for something else, which is how an expensive route
 * ends up behind a limit sized for a cheap one.
 */
export const ENDPOINT_LIMITS: Record<string, EndpointLimit> = {
  'GET /health': {
    rate: 50, burst: 200, key: 'ip',
    reason: 'A liveness probe from a load balancer, plus whatever else asks. Cheap, and refusing it looks like an outage.',
  },
  'GET /metrics': {
    rate: 2, burst: 10, key: 'ip',
    reason: 'A Prometheus scrape every fifteen seconds is one request per fifteen seconds per replica. Two per second leaves room for several scrapers and a person with curl, and refusing this looks like the control plane is down.',
  },
  'GET /readyz': {
    rate: 50, burst: 200, key: 'ip',
    reason: 'The deploy gate polls it every two seconds for a few minutes, and a load balancer may too. It runs one trivial query, so the cost is a round trip to Postgres.',
  },
  // The device authorization grant. See src/auth/device.ts: the short code is
  // eight characters from a 28 character alphabet, and the arithmetic that makes
  // guessing it hopeless assumes these limits exist. They are not a nicety here,
  // they are the third of the three things holding that code up.
  'POST /auth/device/code': {
    rate: 1, burst: 10, key: 'ip',
    reason: 'Starting a terminal login is a human action. Ten at once covers somebody retrying in three shells; a sustained one per second does not.',
  },
  'POST /auth/device/token': {
    rate: 2, burst: 30, key: 'ip',
    reason: 'The terminal polls every five seconds and the server tells a fast client to slow down. This bounds a client that ignores that, and is above the honest rate so a normal login is never refused.',
  },
  'GET /auth/device/pending': {
    rate: 1, burst: 10, key: 'ip',
    reason: 'Reading the pending request for a typed code. Deliberately as tight as approve: this endpoint would otherwise be the oracle that tells an attacker which guessed codes are real.',
  },
  'POST /auth/device/approve': {
    rate: 1, burst: 10, key: 'ip',
    reason: 'A person clicking Approve once. This is the limit that makes guessing an eight character code hopeless rather than merely unlikely.',
  },
  'POST /auth/device/deny': {
    rate: 1, burst: 10, key: 'ip',
    reason: 'The same action with the opposite answer, and the same reason for the same number.',
  },
  // Invitations and the export of a deleted organization.
  //
  // All three carry a token in the URL or the body and nothing else, so the
  // limit is part of what makes the token hard to find rather than a nicety.
  // The token is 32 random bytes, which no rate makes guessable, but these are
  // also the three endpoints an unauthenticated caller can reach that touch the
  // database, so they are bounded like a sign-in rather than like a read.
  'GET /auth/invitation': {
    rate: 1, burst: 10, key: 'ip',
    reason: 'Opening an invitation link is a human action, once, from an email. Ten at once covers a person refreshing a page that looked slow.',
  },
  'POST /auth/invitation/accept': {
    rate: 1, burst: 10, key: 'ip',
    reason: 'Pressing accept, once, and possibly twice because the first press looked like it did nothing. Same shape as approving a terminal login and the same number.',
  },
  'GET /exports/deletion': {
    rate: 1, burst: 5, key: 'ip',
    reason: 'Downloading a copy of a deleted organization. The response is the whole export, so this is the most expensive unauthenticated response the server has; five at once covers a browser retrying a large download and nothing about a person needs more.',
  },

  'GET /v1/whoami': {
    rate: 5, burst: 50, key: 'token',
    reason: 'af whoami, and the first call of most CLI sessions. Keyed by token because the caller always has one.',
  },
  'POST /v1/logout': {
    rate: 2, burst: 20, key: 'token',
    reason: 'af logout. Must never be refused in practice: a sign-out that fails leaves a live credential on a machine somebody is trying to clean.',
  },

  // `af provider`. Keyed by token because every caller has one, and tighter
  // than the reads around it because each of these is a deliberate human
  // action on somebody else's money: nobody rotates a key in a loop.
  'GET /v1/providers': {
    rate: 5, burst: 50, key: 'token',
    reason: 'af provider list, and the read a script runs before deciding whether to set anything. Two small queries, so the cost is a round trip.',
  },
  'PUT /v1/providers/:provider': {
    rate: 1, burst: 10, key: 'token',
    reason: 'Storing or rotating a key. A person does this once and occasionally twice; ten at once covers a retry and a fumbled paste, and a sustained one per second is not a person.',
  },
  'DELETE /v1/providers/:provider': {
    rate: 1, burst: 10, key: 'token',
    reason: 'Removing a key, often during an incident. Same shape as storing one, and deliberately not tighter: this is the call somebody makes in a hurry when a key has leaked.',
  },
  'PUT /v1/providers/:provider/budget': {
    rate: 1, burst: 10, key: 'token',
    reason: 'Setting a monthly cap. The same human cadence as storing a key, and it writes an audit entry each time.',
  },

  // `af token`. The mint is the tightest write on this server: it is the one
  // call that hands back a usable credential, so a loop against it is either a
  // mistake or somebody filling the table.
  'POST /v1/tokens': {
    rate: 1, burst: 5, key: 'token',
    reason: 'Minting an engine token. A person does this once when they set CI up and again when they rotate it; five at once covers a fumbled retry and nothing about a person needs more.',
  },
  'GET /v1/tokens': {
    rate: 5, burst: 50, key: 'token',
    reason: 'af token list. One query, and the read somebody runs before deciding which token to revoke, so it is as generous as the other reads.',
  },
  'DELETE /v1/tokens/:token': {
    rate: 1, burst: 10, key: 'token',
    reason: 'Revoking a token, usually because it leaked. Same shape as removing a provider key and deliberately not tighter, for the same reason: this is the call somebody makes in a hurry.',
  },

  // ---------------------------------------------------------------------------
  // The console.
  //
  // The console is a static export now, so its URL space is a set of files
  // rather than a set of endpoints, and it is declared as two classes instead
  // of one entry per page. The pages are limited generously because a person
  // clicking through a console produces a handful of requests a second and
  // refusing one of them shows a rate limit error to somebody who is reading.
  // ---------------------------------------------------------------------------
  'GET /console/app/*': {
    rate: 20, burst: 120, key: 'ip',
    reason:
      'One page of the console, which is a file on disk. The work is a stat and a read, ' +
      'and a cold load fetches exactly one of them.',
  },
  'GET /console/asset/*': {
    rate: 40, burst: 400, key: 'ip',
    reason:
      'A hashed, immutable asset under /_next/static, cached for a year by anything that ' +
      'has fetched it once. A cold load fetches a dozen and then never again.',
  },
  'GET /console/api/providers': {
    rate: 10, burst: 60, key: 'ip',
    reason: "Two queries for the keys and this month's budgets. Never reads a ciphertext.",
  },
  'PUT /console/api/providers/:provider': {
    rate: 1, burst: 10, key: 'ip',
    reason:
      'Storing or rotating a key. A person does this a few times a year, and a script ' +
      'doing it faster is not a person.',
  },
  'DELETE /console/api/providers/:provider': {
    rate: 1, burst: 10, key: 'ip',
    reason: 'Removing a key. The same human cadence as storing one, and it is audited.',
  },
  'PUT /console/api/providers/:provider/budget': {
    rate: 1, burst: 10, key: 'ip',
    reason: 'Setting a monthly cap. Writes an audit entry each time.',
  },

  'GET /openapi.json': {
    rate: 2, burst: 10, key: 'ip',
    reason: 'A static document that is fetched once by a person and cached by everything else.',
  },

  // Sign-in. The tightest limits on the server, because this is where somebody
  // with no account operates and where a stolen cookie or a guessed state value
  // would be tried.
  'GET /auth/github': {
    rate: 1, burst: 20, key: 'ip',
    reason: 'Starting a sign-in is a human action. Twenty at once covers an office behind one address; a sustained one per second does not.',
  },
  'GET /auth/github/callback': {
    rate: 1, burst: 20, key: 'ip',
    reason: 'The same flow returning. A higher rate here would let somebody grind state values.',
  },
  'POST /auth/email': {
    rate: 1, burst: 10, key: 'ip',
    reason:
      'Asking for a sign-in link is a human action and it causes mail to be sent to somebody ' +
      'else. Ten at once covers a person mistyping their address; a sustained one per second ' +
      'does not, and the thing being bounded is using this endpoint to post somebody mail.',
  },
  'GET /auth/email/callback': {
    rate: 1, burst: 20, key: 'ip',
    reason: 'The link coming back. A higher rate here would let somebody grind token values.',
  },
  'POST /auth/signout': {
    rate: 2, burst: 20, key: 'ip',
    reason: 'Signing out must never be refused in practice; this only bounds a script.',
  },
  'GET /auth/session': {
    rate: 10, burst: 60, key: 'ip',
    reason: 'The page asks on load and after a focus change. Generous, because refusing it signs somebody out visually.',
  },

  // Engines. High volume from few callers, and the only endpoints where a
  // refusal is expected in normal operation.
  // Model calls against a budget. Keyed by token, because the thing worth
  // bounding is one organization's spend rate rather than one machine's.
  //
  // Deliberately not tight. A workflow with twenty steps makes twenty calls in
  // a burst, and refusing one turns a run into a flake. The real bound on this
  // endpoint is the budget, which is checked before the key is decrypted; the
  // limit here only stops a loop from turning into a bill faster than anybody
  // can notice.
  'POST /byok/anthropic/v1/messages': {
    rate: 5, burst: 60, key: 'token',
    reason: 'One model call per planner step, in bursts of a workflow. The budget is what caps spend; this caps the rate at which a runaway loop can reach it.',
  },
  'POST /byok/openai/v1/chat/completions': {
    rate: 5, burst: 60, key: 'token',
    reason: 'The same shape of caller against the other provider, and the same reasoning.',
  },

  // GitHub's own deliveries, keyed by address because a delivery carries no
  // token. Generous: an organization adding twenty repositories at once sends a
  // burst, and a refused delivery is one GitHub retries with backoff for a
  // while and then abandons, which loses an installation nothing else will
  // tell us about.
  'POST /webhooks/github': {
    rate: 20, burst: 200, key: 'ip',
    reason: 'GitHub sends bursts when an installation changes, and a delivery lost to a rate limit is an installation this control plane never learns about. The signature check runs before any work, so an unsigned flood is cheap to refuse.',
  },
  'POST /webhooks/stripe': {
    rate: 20, burst: 200, key: 'ip',
    reason: 'Stripe sends a burst when a billing period rolls over and every subscription renews at once, and a delivery lost to a rate limit is a payment this control plane never learns about. The signature check runs before any work, so an unsigned flood is cheap to refuse.',
  },
  // What a job in the customer's CI reports about one commit. Keyed by address
  // because the first call has no token yet: it is the exchange that gets one.
  //
  // Generous, and deliberately so. A busy repository can have twenty pull
  // requests running at once, each one job, each making exactly two calls. The
  // thing worth bounding here is a loop, and the real bound is the credential:
  // it is issued for one generation, spent on one report, and expires within
  // the hour, so a refused report is a check that says nothing about a commit
  // somebody is waiting on.
  'POST /v1/pr/callback-token': {
    rate: 2, burst: 60, key: 'ip',
    reason: 'One exchange per job, and a workflow identity token is verified against a cached key set before anything is written. The burst covers twenty pull requests starting at once behind one NAT.',
  },
  'POST /v1/pr/report': {
    rate: 2, burst: 60, key: 'ip',
    reason: 'One report per job. Refusing this loses the result of a run somebody has already paid for, so it is as generous as the exchange that precedes it.',
  },
  'POST /v1/events': {
    rate: 200, burst: 2000, key: 'token',
    reason: 'An engine that was offline sends its backlog at once. The burst absorbs a re-connect; the rate is what one busy CI account sustains.',
  },
  'GET /v1/environments/:envId': {
    rate: 10, burst: 60, key: 'token',
    reason: 'Polled by af env pull and by a CI step, not by a loop.',
  },

  // The application API. One limit for the whole surface rather than per
  // procedure: these are reads and small writes made by a browser with a
  // session, and the thing worth bounding is a runaway page rather than any
  // individual route.
  'POST /trpc/*': {
    rate: 20, burst: 200, key: 'org',
    reason: 'A person clicking. The burst covers a page that fires several queries on load.',
  },
  'GET /trpc/*': {
    rate: 20, burst: 200, key: 'org',
    reason: 'Reads made by a browser with a session. Same shape as the writes: the thing worth bounding is a page in a loop, not any one route.',
  },
}

/** The identifier used for one request's bucket, given its key kind. */
export interface LimitSubject {
  ip: string
  token?: string | null
  org?: string | null
}

export function bucketFor(limit: EndpointLimit, subject: LimitSubject): string {
  switch (limit.key) {
    case 'token':
      // Falls back to the address when there is no token, so an unauthenticated
      // caller hammering an engine endpoint is still bounded rather than
      // sharing one global bucket with everybody else in the same position.
      return `token:${subject.token ?? `anon:${subject.ip}`}`
    case 'org':
      return `org:${subject.org ?? `anon:${subject.ip}`}`
    case 'ip':
      return `ip:${subject.ip}`
  }
}

/**
 * Looks up the limit for a concrete request path.
 *
 * The catalog holds route patterns and a request carries a concrete path, so
 * this matches by segment: `:name` matches exactly one segment and nothing
 * else. An earlier version compared the strings directly and so found no limit
 * for `/v1/environments/af-1`, which meant the endpoint answered 500 rather
 * than serving. That failure was loud, which is the only reason it was a
 * five-minute bug instead of an unbounded endpoint.
 *
 * `/trpc/*` is the one real wildcard and it is deliberate: those are reads and
 * small writes from a browser with a session, and the thing worth bounding is a
 * page in a loop rather than any individual procedure. Nothing else may use one,
 * because a pattern lets a new endpoint inherit a number chosen for something
 * cheaper.
 */
export function limitFor(method: string, path: string): EndpointLimit | undefined {
  const exact = ENDPOINT_LIMITS[`${method} ${path}`]
  if (exact) return exact
  if (path.startsWith('/trpc/')) return ENDPOINT_LIMITS[`${method} /trpc/*`]

  const segments = path.split('/')
  for (const [endpoint, limit] of Object.entries(ENDPOINT_LIMITS)) {
    const space = endpoint.indexOf(' ')
    if (endpoint.slice(0, space) !== method) continue
    if (matches(endpoint.slice(space + 1), segments)) return limit
  }

  // Routes another edition registered. They are consulted after the catalog
  // rather than merged into it, so an extension cannot change the limit on a
  // path this server already declares: registerExtension refuses a reserved
  // prefix outright, and this ordering means even a mistake there could not
  // loosen a core endpoint.
  //
  // Every extension route carries its own limit and the field is not optional,
  // so this loop cannot fail to find one for a route that is actually mounted.
  for (const route of extensionRoutes()) {
    if (route.method !== method) continue
    if (matches(route.path, segments)) return route.limit
  }

  // Last, after every declared route and every extension route, so that a
  // route which declares its own limit always wins. It went before the
  // extension loop first, and a GET route registered by another edition
  // silently inherited the console's numbers instead of the ones it declared.
  const asConsole = consoleClass(method, path)
  if (asConsole) return ENDPOINT_LIMITS[asConsole]

  return undefined
}

/**
 * Where the API ends and the console's files begin.
 *
 * The console is a static export served by this process, so anything the API
 * did not claim is a file: a page, an asset, or the 404 page. That cannot be
 * enumerated -- a mistyped URL is a real request that has to answer 404 rather
 * than 500 -- so it is one class, in the same spirit as `/trpc/*`.
 *
 * The loud refusal for an undeclared endpoint is what this must not weaken, so
 * it is kept exactly where it earns its keep:
 *
 *   - every method that can change something still falls through to the
 *     refusal, because a static file server answers GET and HEAD and nothing
 *     else;
 *   - and every GET under a prefix the API owns still falls through too, so a
 *     new endpoint under /v1, /auth, /trpc, /webhooks, /byok or /console/api
 *     added without a limit is refused as loudly as it ever was.
 *
 * What is left is the space a browser asks for pages in, and a page there is a
 * stat and a read.
 */
const API_PREFIXES = ['/v1/', '/auth/', '/trpc/', '/webhooks/', '/byok/', '/console/api/']

export function consoleClass(method: string, path: string): string | null {
  if (method !== 'GET' && method !== 'HEAD') return null
  if (API_PREFIXES.some((prefix) => path === prefix.slice(0, -1) || path.startsWith(prefix))) {
    return null
  }
  if (path.startsWith('/_next/')) return 'GET /console/asset/*'
  return 'GET /console/app/*'
}

/** One route pattern against one concrete path, by segment. `:name` matches
 *  exactly one segment and nothing else. */
function matches(pattern: string, segments: string[]): boolean {
  const parts = pattern.split('/')
  if (parts.length !== segments.length) return false
  return parts.every((part, i) => part.startsWith(':') || part === segments[i])
}

// ---------------------------------------------------------------------------
// Quotas
// ---------------------------------------------------------------------------

/**
 * What an organization may hold at once.
 *
 * Separate from rate limits because they answer a different question: a rate
 * limit is about how fast, a quota is about how much, and a customer can sit
 * inside every rate limit while holding ten thousand environments.
 *
 * A quota that is reached refuses the next creation and never removes anything
 * that exists. Tearing down somebody's running environments because a plan
 * changed is not a behaviour any product should have.
 */
export interface Quota {
  environments: number
  goldens: number
  artifactGigabytes: number
}

export const PLAN_QUOTAS: Record<string, Quota> = {
  free: { environments: 3, goldens: 2, artifactGigabytes: 1 },
  team: { environments: 25, goldens: 10, artifactGigabytes: 50 },
  enterprise: { environments: 500, goldens: 100, artifactGigabytes: 1000 },
}

/** The plan applied when an organization has none recorded. */
export const DEFAULT_PLAN = 'free'

export interface QuotaVerdict {
  allowed: boolean
  /** What is currently held. */
  current: number
  limit: number
  /** One sentence naming what to do, empty when allowed. */
  reason: string
}

export function checkQuota(
  plan: string,
  resource: keyof Quota,
  current: number,
): QuotaVerdict {
  const quota = PLAN_QUOTAS[plan] ?? PLAN_QUOTAS[DEFAULT_PLAN]!
  const limit = quota[resource]
  if (current < limit) {
    return { allowed: true, current, limit, reason: '' }
  }
  return {
    allowed: false,
    current,
    limit,
    reason:
      `This organization is holding ${current} of ${limit} ${resource} on the ${plan} plan. ` +
      `Tear one down, or change the plan. Nothing that already exists was removed.`,
  }
}
