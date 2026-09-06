// The marketing site's product analytics, forwarded from here, so that a
// reader's browser never opens a connection to posthog.com.
//
// THIS IS TRANSPORT AND IT IS NOT A BOUNDARY. SAY SO EVERY TIME, INCLUDING IN
// A COMMIT MESSAGE AND IN A TEST'S FAILURE TEXT.
//
// What this changes is the destination the BROWSER CONNECTS TO. It does not
// change WHO RECEIVES THE DATA. PostHog, Inc. receives every event, every
// autocaptured interaction and every session recording, in their US cloud,
// exactly as it would if the browser had called them directly. Three real
// things are bought and nothing else: a content blocker's vendor list does not
// match the request, so the measurement is not silently half missing; the
// recorder bundle, which is the largest and most blockable thing posthog-js
// fetches, arrives rather than failing while ingestion goes on looking healthy;
// and the reader's address is dropped on the way through, which is the one
// place this arrangement genuinely withholds something rather than moving where
// a request goes.
//
// WHAT IT DOES NOT BUY IS THE SENTENCE "no third party sees this". A network
// tab showing no vendor host would make that sentence look VERIFIED while it
// was false, which is worse than not proxying at all, because the arrangement
// it hides is the one a security review is asking about. PostHog is on the
// published subprocessor list with a row of its own for that reason, and
// legal-facts.test.ts fails if that row stops saying who receives the data.
//
// WHY THIS IS ON THE CONTROL PLANE AND NOT ON THE SITE. www is a Next.js static
// export (`output: "export"`, see www/next.config.ts) published to an Azure
// Static Web App. It has no server at runtime. Static Web Apps route rules
// match on PATH and can rewrite to a file or to a LINKED BACKEND; they cannot
// rewrite to an arbitrary external host, and a linked backend needs the
// Standard plan while the site runs on Free. So the site itself cannot forward
// anything, and the only origin this product already runs a server on is the
// control plane. That is where this lives.
//
// SAME SITE, NOT SAME ORIGIN, AND THE DIFFERENCE IS NOT PEDANTRY. The site is
// https://antifailure.dev and https://www.antifailure.dev. This process answers
// on https://app.antifailure.dev. Those share a registrable domain and are
// therefore SAME SITE; they are different origins. So this is first party
// infrastructure and the browser contacts nothing but our own domain, and it is
// still a cross origin request that needs CORS, which is why every route below
// answers a preflight and echoes exactly one allowed origin. Anything published
// about this that says "same origin" is false. This repository has already lost
// most of a day to three people calling something cross site when it was same
// site only; the same word being wrong in the other direction costs the same.
//
// WHAT THIS IS NOT. It is not a general forwarder. The upstream host is chosen
// from a CLOSED SET of two regions and can never be named by a caller, the set
// of paths that reach it is an allowlist declared below rather than whatever
// arrived, and a redirect from the upstream is refused rather than followed.
// Those are three separate mechanisms and none of them subsumes the others: the
// allowlist bounds where a request may go inside PostHog, the origin assertion
// bounds which host it may reach at all, and the redirect refusal stops PostHog
// itself from steering this process at somebody else's server.

/** How the browser talks to this, and therefore what the site must configure.
 *
 *  posthog-js is given `api_host: "<control plane>/ph"`. Its request router
 *  reads that host, fails to match it against the PostHog region patterns,
 *  classifies it `custom`, and from then on sends EVERY target at that one
 *  base: capture, feature flags, remote config and the lazily loaded script
 *  bundles alike. That is why this mount has to serve the asset paths as well
 *  as the ingestion ones. A proxy that forwarded only the event paths would
 *  leave posthog-js fetching its own recorder bundle from a 404. */
export const POSTHOG_MOUNT = '/ph'

/**
 * The two hosts PostHog serves a browser from, per region.
 *
 * A CLOSED SET RATHER THAN A URL IN A VARIABLE, and that is the main thing
 * stopping this from becoming an open forwarder. An operator can choose a
 * region; there is no value of any setting that makes this process send a
 * request to a host that is not written on one of these lines. A URL shaped
 * environment variable would have needed its own validator, and a validator is
 * a thing that can be wrong.
 *
 * Two hosts per region because PostHog splits them: ingestion and the flag
 * evaluation live on `<region>.i.posthog.com`, and the JavaScript bundles and
 * the per project remote config live on `<region>-assets.i.posthog.com`. Both
 * were confirmed live against the real hosts before this was written rather
 * than taken from documentation.
 */
export const POSTHOG_REGIONS = {
  us: {
    ingestion: 'https://us.i.posthog.com',
    assets: 'https://us-assets.i.posthog.com',
  },
  eu: {
    ingestion: 'https://eu.i.posthog.com',
    assets: 'https://eu-assets.i.posthog.com',
  },
} as const

export type PostHogRegion = keyof typeof POSTHOG_REGIONS
export type PostHogUpstream = keyof (typeof POSTHOG_REGIONS)['us']
export interface PostHogUpstreams {
  readonly ingestion: string
  readonly assets: string
}

/**
 * The region, from configuration, or null for a deployment that has no PostHog.
 *
 * Absent turns the routes off entirely rather than leaving them mounted and
 * answering with an error, which is the same choice `emailSignIn` makes in
 * main.ts and for the same reason: a self hoster who has not configured this
 * should not be running a path that forwards to a vendor they do not use.
 *
 * The region is NOT guessed from the project token, because a token carries no
 * region. It was determined by asking both clouds about the project this
 * deployment uses: the US cloud answered a flag payload and the EU cloud
 * answered `authentication_failed`. Whoever configures a different project has
 * to do the same, so the refusal below names the way to find out rather than
 * only the two words it will accept.
 */
export function postHogRegionFrom(value: string | undefined | null): PostHogRegion | null {
  const raw = value?.trim().toLowerCase()
  if (!raw) return null
  if (raw in POSTHOG_REGIONS) return raw as PostHogRegion
  throw new Error(
    `AF_POSTHOG_REGION is ${JSON.stringify(raw)} and the only values are "us" and "eu". ` +
      'A PostHog project API key does not carry its region, so read it off the cloud instead: ' +
      'POST the key to https://us.i.posthog.com/flags/?v=2 and to the eu host beside it, and ' +
      'the region that answers 200 rather than authentication_failed is the one to set.',
  )
}

/** What a deployment says about itself at start up. Both absences look exactly
 *  like working software, so the process says which one it is out loud. */
export function postHogSummary(region: PostHogRegion | null): string {
  if (!region) {
    return 'AF_POSTHOG_REGION is not set: the PostHog proxy is not mounted, so a browser ' +
      'configured to send analytics through this control plane would be answered 404'
  }
  const bases = POSTHOG_REGIONS[region]
  return `the PostHog proxy is mounted at ${POSTHOG_MOUNT} and forwards to ${bases.ingestion} ` +
    `and ${bases.assets}, so a browser on the marketing site contacts no posthog.com host. ` +
    'PostHog still receives the data: this moves the connection, not the recipient'
}

// ---------------------------------------------------------------------------
// The allowlist
// ---------------------------------------------------------------------------

export interface ProxiedPath {
  /** The path under the mount, in Hono's router syntax. */
  readonly route: string
  readonly method: 'GET' | 'POST'
  readonly upstream: PostHogUpstream
  /** Bytes accepted on a POST. Meaningless on a GET. */
  readonly maxBodyBytes: number
  /** Requests per second, sustained, keyed on the caller's address. */
  readonly rate: number
  /** How many may arrive at once before the sustained rate applies. */
  readonly burst: number
  /** Why this path is here, and why those numbers. Read by whoever wants to
   *  add a path or raise a limit. */
  readonly why: string
}

/**
 * Every path that reaches PostHog, and nothing else.
 *
 * These are not guesses. Each one was read out of the posthog-js bundle this
 * site loads and confirmed against the live hosts, because the alternative is
 * a list that looks complete and silently drops one kind of request.
 *
 * THE ONE THAT WOULD HAVE BEEN MISSED. posthog-js posts events to `/e/` by
 * default, and it OVERRIDES that from the remote config it fetches at start up:
 * `analytics.endpoint`. This project's remote config returns `/i/v0/e/`, so the
 * browser never posts to `/e/` at all. A proxy allowlisting the documented
 * default would have forwarded the flag call, served the script, looked
 * healthy in every check anybody ran, and 404ed every single event. Both are
 * here, because PostHog can change that value from their side at any time and
 * the failure it causes is silent by construction.
 *
 * WHAT IS DELIBERATELY ABSENT, said out loud so the silence does not have to be
 * interpreted. Surveys (`/api/surveys/`), web experiments
 * (`/api/web_experiments/`), product tours (`/api/product_tours/`) and the SDK's
 * own log and metric channels (`/i/v1/logs`, `/i/v1/metrics`) are not here. This
 * project's remote config reports surveys, product tours and console log capture
 * all off, so allowing those paths would widen the surface for a feature nobody
 * has switched on. Turning one on later means adding its line here, with a limit
 * and a boundary entry, which is the point: an unlisted path is refused by the
 * router rather than forwarded, so this list cannot rot into permissiveness.
 */
export const POSTHOG_PROXIED: readonly ProxiedPath[] = [
  {
    route: '/i/v0/e/',
    method: 'POST',
    upstream: 'ingestion',
    maxBodyBytes: 1024 * 1024,
    rate: 5,
    burst: 120,
    why: 'Event capture, and the endpoint this project actually uses: its remote config sets analytics.endpoint to this, so the default below is never reached. posthog-js batches on a three second timer, so a reader is well under one a second and the burst is what an office behind one address produces when a class opens the site at once.',
  },
  {
    route: '/e/',
    method: 'POST',
    upstream: 'ingestion',
    maxBodyBytes: 1024 * 1024,
    rate: 5,
    burst: 120,
    why: "Event capture, posthog-js's compiled in default. Kept because the remote config that currently points elsewhere is PostHog's to change, and an event posted to a path this refuses is lost with no error a reader could see. Same numbers as the endpoint above because it is the same traffic arriving under a different name.",
  },
  {
    route: '/flags/',
    method: 'POST',
    upstream: 'ingestion',
    maxBodyBytes: 64 * 1024,
    rate: 2,
    burst: 60,
    why: 'Feature flag evaluation and the remote config that tells posthog-js which capture endpoint to use. Called once per page load, so the body is one small identity document and the sustained rate is the same as the first party beacon, which is also one call per page.',
  },
  {
    route: '/s/',
    method: 'POST',
    upstream: 'ingestion',
    maxBodyBytes: 2 * 1024 * 1024,
    rate: 5,
    burst: 120,
    why: 'Session recording. posthog-js chunks a recording well below the body limit, so that number bounds a hostile body rather than a real one, and the rate covers one flush every five seconds from several tabs behind one address.',
  },
  {
    route: '/static/:file',
    method: 'GET',
    upstream: 'assets',
    maxBodyBytes: 0,
    rate: 10,
    burst: 120,
    why: 'The lazily loaded bundles, unversioned: posthog-js fetches /static/<name>.js?v=<version> for the recorder and the other extensions. Fetched once and then held by the browser cache, so the burst is a cold office and the rate is not a person.',
  },
  {
    route: '/static/:version/:file',
    method: 'GET',
    upstream: 'assets',
    maxBodyBytes: 0,
    rate: 10,
    burst: 120,
    why: 'The same bundles under strict_script_versioning, which puts the version in the path instead of the query. Same traffic and therefore the same numbers as the unversioned form above.',
  },
  {
    route: '/array/:token/:config',
    method: 'GET',
    upstream: 'assets',
    maxBodyBytes: 0,
    rate: 10,
    burst: 120,
    why: 'The per project remote config, fetched as /array/<project token>/config and /array/<project token>/config.js. The token here is the public project key the browser already carries. One call per page load, cached, so the numbers match the bundles beside it.',
  },
] as const

/** Refused before anything left this process. Carries the status the caller
 *  gets, so the reason and the answer cannot drift apart. */
export class PostHogProxyRefused extends Error {
  readonly status: number
  constructor(message: string, status = 404) {
    super(message)
    this.status = status
  }
}

/** One allowlist route against one concrete path, by segment. `:name` matches
 *  exactly one non empty segment and nothing else. Same rule as limits.ts, and
 *  deliberately not a regular expression over the whole path: a pattern with a
 *  `.*` in it is how a path allowlist stops bounding anything. */
function matchesRoute(route: string, path: string): boolean {
  const parts = route.split('/')
  const segments = path.split('/')
  if (parts.length !== segments.length) return false
  return parts.every((part, i) => {
    if (!part.startsWith(':')) return part === segments[i]
    // A named segment must actually be a segment. Without this an empty one
    // matches, and `/static//` would be forwarded as `/static/`.
    return segments[i]!.length > 0
  })
}

/** The allowlist entry a request reaches, or null. Exported because the gate
 *  and the router have to agree about the answer and one function is the only
 *  way to guarantee that. */
export function proxiedPathFor(method: string, subPath: string): ProxiedPath | null {
  for (const entry of POSTHOG_PROXIED) {
    if (entry.method !== method) continue
    if (matchesRoute(entry.route, subPath)) return entry
  }
  return null
}

/**
 * Where one request goes, or a refusal.
 *
 * THE WHOLE POINT OF THIS BEING A PURE FUNCTION is that the refusals can be
 * driven directly, without a server and without a network, so the test that
 * proves this cannot be steered somewhere else is a test of the thing that
 * decides rather than of a route that happens to call it.
 *
 * THE ESCAPE THIS IS BUILT AGAINST. `new URL(untrusted, base)` is the obvious
 * way to write this and it is wrong: a value beginning `//` is a protocol
 * relative URL, so `new URL('//evil.example/x', 'https://us.i.posthog.com')`
 * resolves to `https://evil.example/x`. The base is discarded silently and the
 * code reads as though it could not be. So the URL is built from the base and
 * the pathname is ASSIGNED, which cannot change the origin, and then the origin
 * is asserted anyway. Two mechanisms, because the assignment is a property of
 * WHATWG URL that a later edit could stop relying on without noticing.
 */
export function postHogUpstreamFor(
  bases: PostHogUpstreams,
  method: string,
  subPath: string,
  search: string,
): { url: URL; entry: ProxiedPath } {
  const entry = proxiedPathFor(method, subPath)
  if (!entry) {
    throw new PostHogProxyRefused(
      `${method} ${subPath} is not one of the paths this forwards to PostHog.`,
      404,
    )
  }
  const base = bases[entry.upstream]
  const url = new URL(base)
  // Assigned, never resolved. See above.
  url.pathname = subPath
  url.search = search

  // The assertion, which is the line the refusal test breaks to prove the test
  // can say no. It can only fire if the two lines above stop behaving the way
  // WHATWG URL says they do, which is exactly the change nobody would notice.
  const expected = new URL(base).origin
  if (url.origin !== expected) {
    throw new PostHogProxyRefused(
      `This forwards to ${expected} and nowhere else, and a request was about to leave for ${url.origin}.`,
      502,
    )
  }
  return { url, entry }
}

// ---------------------------------------------------------------------------
// The forward itself
// ---------------------------------------------------------------------------

export interface PostHogForwardOptions {
  readonly bases: PostHogUpstreams
  /** Overridden in tests, so nothing here reaches a real PostHog. The same
   *  seam providers/proxy.ts uses, for the same reason. */
  readonly fetchImpl?: typeof fetch
}

export interface PostHogForwardRequest {
  readonly method: string
  /** The path under the mount, still percent encoded, as it arrived. */
  readonly subPath: string
  /** The query string including its leading `?`, or the empty string. */
  readonly search: string
  readonly contentType?: string | undefined
  readonly body?: ArrayBuffer | undefined
}

export interface PostHogForwardResult {
  readonly status: number
  readonly body: ArrayBuffer
  /** Exactly the headers that go back to the browser, before CORS is added.
   *  A closed set: see below for why nothing is copied wholesale. */
  readonly headers: Record<string, string>
}

/**
 * The headers this sends UPSTREAM, and that is the whole list.
 *
 * Copying the browser's headers through is the shape everybody writes first and
 * it is the one that leaks. The browser's request to this origin carries the
 * control plane's own session cookie, because a cookie goes to its domain
 * whether or not the page meant it to; forwarding the request headers would
 * hand that session to PostHog. `authorization` is the same argument. So
 * nothing is copied: the request is built from scratch and `content-type` is
 * the only thing that crosses, because PostHog needs it to know whether the
 * body is JSON or a compressed blob.
 *
 * THE VISITOR'S ADDRESS IS DELIBERATELY NOT FORWARDED. PostHog reads the source
 * address of the request for geolocation, and behind this every event arrives
 * from one container, so `$geoip_*` will describe our datacenter and not the
 * reader. That is a real loss of a real feature and it is the trade this makes
 * on purpose: forwarding `x-forwarded-for` would send every visitor's IP
 * address to a third party, which is precisely the disclosure this whole lane
 * exists to avoid, and it is the one direction that cannot be undone after the
 * fact. Adding it later is a line of code; unsending addresses is not.
 */
function upstreamHeaders(contentType: string | undefined): Record<string, string> {
  const headers: Record<string, string> = {}
  if (contentType) headers['content-type'] = contentType
  return headers
}

/**
 * The headers that come BACK, and again the whole list.
 *
 * `set-cookie` is the one that matters. PostHog answers some ingestion calls
 * with cookies of its own, and passing one back would set it on
 * app.antifailure.dev, where it would then ride along on every authenticated
 * request this control plane serves. It is not in this list, so it cannot.
 */
const RETURNED_HEADERS = ['content-type', 'cache-control', 'etag', 'last-modified'] as const

/**
 * Forwards one allowlisted request and returns what PostHog said.
 *
 * A REDIRECT IS REFUSED RATHER THAN FOLLOWED, and that is not tidiness. `fetch`
 * follows redirects by default, so an upstream answering `302 Location:
 * https://somewhere-else/` would make this process issue a second request to a
 * host that is nowhere in POSTHOG_REGIONS, with the body it was given. That is
 * the open forwarder this is supposed not to be, reached without touching any
 * configuration on this side. `redirect: 'manual'` stops the client from
 * following, and the 3xx is then turned into a 502 so that the browser cannot
 * follow it either: returning the redirect unchanged would just move the same
 * request off our infrastructure one hop later.
 */
export async function forwardToPostHog(
  options: PostHogForwardOptions,
  request: PostHogForwardRequest,
): Promise<PostHogForwardResult> {
  const { url } = postHogUpstreamFor(options.bases, request.method, request.subPath, request.search)
  const doFetch = options.fetchImpl ?? fetch

  const response = await doFetch(url, {
    method: request.method,
    headers: upstreamHeaders(request.contentType),
    ...(request.body === undefined ? {} : { body: request.body }),
    redirect: 'manual',
    // Nothing of ours travels with this. There is no credential PostHog needs
    // beyond the public project token already in the body.
    credentials: 'omit',
  } as RequestInit)

  if (response.status >= 300 && response.status < 400) {
    throw new PostHogProxyRefused(
      `The upstream answered ${response.status} and tried to redirect this request. ` +
        'This forwards to one configured host and does not follow a redirect anywhere else.',
      502,
    )
  }

  const headers: Record<string, string> = {}
  for (const name of RETURNED_HEADERS) {
    const value = response.headers.get(name)
    if (value !== null) headers[name] = value
  }
  return { status: response.status, body: await response.arrayBuffer(), headers }
}

// ---------------------------------------------------------------------------
// What the rest of the server has to know about these routes
// ---------------------------------------------------------------------------
//
// GENERATED FROM THE ALLOWLIST RATHER THAN WRITTEN OUT BESIDE IT. Eleven routes
// hand copied into ENDPOINT_LIMITS and fifteen into ROUTE_BOUNDARY is three
// lists of the same paths, and the one that stops matching is whichever a
// change forgets. Both gates walk the server's real route table, so they would
// catch the drift; deriving all three from one table means there is no drift to
// catch. The numbers and the reasons still live on the entry, where somebody
// raising a limit reads them.

/** The route key ENDPOINT_LIMITS and ROUTE_BOUNDARY are both written under. */
function key(method: string, entry: ProxiedPath): string {
  return `${method} ${POSTHOG_MOUNT}${entry.route}`
}

/** Every method actually registered for one entry. A POST is preceded by a
 *  CORS preflight, because posthog-js sends `content-type: application/json`,
 *  which is not a simple request. A GET is not: the bundles are fetched by a
 *  script tag and the remote config by a plain GET with no header of its own,
 *  so neither preflights and an OPTIONS route for them would be a route
 *  nothing calls. */
export function postHogMethodsFor(entry: ProxiedPath): string[] {
  return entry.method === 'POST' ? ['POST', 'OPTIONS'] : ['GET']
}

export interface PostHogLimit {
  rate: number
  burst: number
  key: 'ip'
  reason: string
}

/**
 * The limit for every route this mounts, keyed the way limits.ts keys them.
 *
 * Keyed on the ADDRESS, like the first party beacon and for the same reason: a
 * reader of the marketing site has no token and no organization, so the address
 * is the only key there is. That also means the same caveat holds, and it is
 * worth writing down rather than discovering later: this bounds how fast
 * somebody can push traffic through, and it is the only thing that does.
 */
export function postHogLimits(): Record<string, PostHogLimit> {
  const out: Record<string, PostHogLimit> = {}
  for (const entry of POSTHOG_PROXIED) {
    for (const method of postHogMethodsFor(entry)) {
      out[key(method, entry)] = {
        rate: entry.rate,
        burst: entry.burst,
        key: 'ip',
        reason:
          method === 'OPTIONS'
            ? `The preflight for ${POSTHOG_MOUNT}${entry.route}, cached by the browser for a day. Same shape as the request it precedes, so a refused preflight and a refused post mean the same thing.`
            : entry.why,
      }
    }
  }
  return out
}

export interface PostHogBoundary {
  audience: 'excluded'
  grounds: 'foreign-shape'
  reason: string
}

/**
 * The published API boundary for every route this mounts.
 *
 * Excluded, on the ground the register already has a name for: the request and
 * response shapes here are PostHog's, not ours. Describing them in our own
 * OpenAPI document would publish a copy of somebody else's contract, which is
 * wrong the moment they change it, and would invite a caller to integrate with
 * a path that exists to carry one site's analytics.
 */
export function postHogBoundary(): Record<string, PostHogBoundary> {
  const out: Record<string, PostHogBoundary> = {}
  for (const entry of POSTHOG_PROXIED) {
    for (const method of postHogMethodsFor(entry)) {
      out[key(method, entry)] = {
        audience: 'excluded',
        grounds: 'foreign-shape',
        reason: `Forwards ${entry.method} ${POSTHOG_MOUNT}${entry.route} to PostHog so the marketing site never contacts posthog.com from a reader's browser. The shape is PostHog's own API and publishing it here would republish another vendor's contract.`,
      }
    }
  }
  return out
}
