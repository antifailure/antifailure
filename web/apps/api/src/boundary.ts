// Which routes are the published API and which are transport, said out loud.
//
// The 1.0.0 notes carve the control plane's HTTP API out of the stability
// promise: it is how the console and the engine speak to each other, not a
// published integration surface. That is the right promise and it had nothing
// holding it. `www/public/openapi.json` is the published half, generated from
// the router, and a route that was absent from it was absent for one of two
// completely different reasons, spelled identically:
//
//   - Nobody could call it from the document, so publishing it would document
//     an API its readers cannot use.
//   - Somebody forgot.
//
// Four routes under /v1/oidc/bindings were the second kind. They take the same
// bearer token the document already declares, they are gated on tokens.manage,
// and guides/github.md tells a customer to curl them by hand because there is
// no `af` command in front of them. They sat outside the document for as long
// as they existed and nothing noticed, because silence meant both things.
//
// So every route the router serves is classified here, with a reason, and
// route-boundary.test.ts walks the server's own route table and fails on one
// that is not. A route classified `contract` must be in the published document.
// A route classified `excluded` must not be. Both directions, because a check
// that only looked one way would let a route be quietly published as easily as
// quietly hidden.
//
// WHY THE GROUNDS ARE A CLOSED SET. A free text reason is a reason nobody
// audits: thirty of them read as thirty different judgements and the pattern in
// them is invisible. Naming one of seven grounds makes the register readable at
// a glance -- how many routes are excluded, and on what basis -- and makes a new
// kind of excuse an edit to this file's type rather than a sentence somebody
// slipped in. The reason is still required, because the ground says the class
// and the sentence says the case.
//
// WHAT THIS IS NOT. It does not say whether a route is documented in prose;
// route-docs.test.ts already fails on a route family reference/api.md does not
// name, in both directions. This is about the machine readable contract
// specifically: what a generated client can call.

/** Every reason a route may be kept out of the published document, in one
 *  place so the set can be counted and the count can be checked. */
export const GROUNDS = [
  /** The caller needs a credential the document does not describe: a webhook
   *  HMAC over the raw body, a token that arrived in a mailed link, an
   *  operator session from a different table. A reader holding a session
   *  cookie or a bearer token cannot reach it at all. */
  'different-credential',
  /** The console's own wire. The console is a static export served by this
   *  same process, from this same commit, so there is no second client and no
   *  version skew for a contract to protect against. */
  'console-transport',
  /** The wire underneath an `af` command. The command, its flags and its exit
   *  codes are the stable surface and are promised in the release notes; the
   *  shape of the request it makes is not, and publishing it would promise
   *  something the notes say is free to change. */
  'cli-transport',
  /** The wire between the engine and the control plane. The engine ships from
   *  this repository and is versioned with it, so both ends move together. */
  'engine-transport',
  /** The request and response shapes are another vendor's API rather than
   *  ours. Describing them here would publish a copy of somebody else's
   *  contract, which is wrong the moment they change it. */
  'foreign-shape',
  /** Served for whoever runs the deployment rather than for a caller
   *  integrating with the product: a scrape, or the document itself. */
  'operator-facing',
  /** Not an operation a client calls. A mount that other routes hang off, or
   *  the static file fallback. */
  'not-an-endpoint',
] as const

/**
 * Why a route is not in the published document.
 *
 * Each of these is a reason a reader of `openapi.json` could not, or should
 * not, call the route. None of them is "we have not got round to it": that
 * case belongs in the register in route-boundary.test.ts, which empties itself
 * when the route is documented.
 *
 * Derived from the array rather than written twice. Two lists of the same
 * seven strings is two lists that drift, and the one the test walks would be
 * the one that stopped matching.
 */
export type Grounds = (typeof GROUNDS)[number]

export interface RouteBoundary {
  /**
   * `contract` means the published document has to carry it, so a generated
   * client can call it. `excluded` means the document must not.
   */
  audience: 'contract' | 'excluded'
  /** Required on an excluded route and meaningless on a contract one. */
  grounds?: Grounds
  /** The case, in one sentence. Read by whoever wants to move the route across
   *  the line later. */
  reason: string
}

/**
 * Every route the server registers, keyed "METHOD /path" in the router's own
 * syntax.
 *
 * Wildcards appear only where the server registers one. A pattern that stood
 * for a family would let a route added later inherit a classification chosen
 * for something else, which is the same reason ENDPOINT_LIMITS forbids them.
 */
export const ROUTE_BOUNDARY: Record<string, RouteBoundary> = {
  // ---------------------------------------------------------------------
  // The published contract.
  // ---------------------------------------------------------------------
  'GET /health': {
    audience: 'contract',
    reason: 'Liveness. Unauthenticated on purpose, and the first thing anybody points at a new deployment.',
  },
  'GET /readyz': {
    audience: 'contract',
    reason: 'Readiness, plus the version and commit actually serving. A deploy gate reads it and so does a person checking a promotion landed.',
  },
  'POST /v1/events': {
    audience: 'contract',
    reason: 'Where an engine sends what it did. Published because a self-hosted engine, or one somebody else builds, is a real caller with a real bearer token.',
  },
  'POST /v1/auth/github-oidc': {
    audience: 'contract',
    reason: 'A workflow author writes this call into their own workflow by hand, so the request and its refusals are theirs to read rather than the engine\'s.',
  },

  // The Studio endpoints. An engine token reaches all four, and the engine
  // that reaches them is the one running in the customer's own CI rather than
  // one this repository controls, so the shape of these is a contract with a
  // process on somebody else's machine.
  'POST /v1/workloads/claim': {
    audience: 'contract',
    reason: 'Takes the oldest requested run for an environment and returns it with a lease. The engine pulls rather than being told, so the poller is the caller and the shape is its contract.',
  },
  'POST /v1/workloads/runs/:runId/heartbeat': {
    audience: 'contract',
    reason: 'Extends the lease on a claimed run. A run whose deadline passes with nothing said about it ends as abandoned, so a caller that gets this wrong loses work.',
  },
  'POST /v1/commands/claim': {
    audience: 'contract',
    reason: 'Takes the teardowns and cancellations waiting for an organization, with a lease. Reachable while the organization is suspended, which is a promise worth publishing rather than discovering.',
  },
  'POST /v1/commands/:id/ack': {
    audience: 'contract',
    reason: 'Says what happened to a claimed command. Only the lease holder may acknowledge and a mismatch answers 409, which is exactly the kind of refusal a generated client has to be told about.',
  },

  // ---------------------------------------------------------------------
  // The console's wire.
  // ---------------------------------------------------------------------
  'ALL /trpc/*': {
    audience: 'excluded',
    grounds: 'not-an-endpoint',
    reason: 'The mount the tRPC procedures hang off. The procedures themselves are classified one by one, and every one of them is in the document.',
  },
  'ALL /*': {
    audience: 'excluded',
    grounds: 'not-an-endpoint',
    reason: 'The console static export and the 404 for everything else. A mistyped URL has to answer as a missing page, so this set cannot be enumerated.',
  },
  'GET /auth/github': {
    audience: 'excluded',
    grounds: 'console-transport',
    reason: 'Starts the browser sign-in redirect. There is nothing to generate a client for: the caller is a link the console renders.',
  },
  'GET /auth/github/callback': {
    audience: 'excluded',
    grounds: 'console-transport',
    reason: 'Where GitHub sends the browser back. Called by a redirect, never by a client.',
  },
  'POST /auth/email': {
    audience: 'excluded',
    grounds: 'console-transport',
    reason: 'The sign-in link for deployments GitHub cannot reach. A form on the console posts it.',
  },
  'GET /auth/email/callback': {
    audience: 'excluded',
    grounds: 'console-transport',
    reason: 'Where that link lands. A browser follows it; nothing calls it.',
  },
  'POST /auth/signout': {
    audience: 'excluded',
    grounds: 'console-transport',
    reason: 'Clears the session cookie the console holds. Meaningless to a caller that does not hold one.',
  },
  'GET /auth/session': {
    audience: 'excluded',
    grounds: 'console-transport',
    reason: 'What the console asks on load to find out who it is talking to.',
  },
  'GET /auth/invitation': {
    audience: 'excluded',
    grounds: 'console-transport',
    reason: 'Renders what an invitation link is offering, before the person accepts it.',
  },
  'POST /auth/invitation/accept': {
    audience: 'excluded',
    grounds: 'console-transport',
    reason: 'The accept button on that page. The credential is the token in the link and the caller is the browser holding it.',
  },
  'GET /auth/device/pending': {
    audience: 'excluded',
    grounds: 'console-transport',
    reason: 'The approval page reads the pending request for a typed code. Deliberately as tightly limited as approve, because it would otherwise say which guessed codes are real.',
  },
  'POST /auth/device/approve': {
    audience: 'excluded',
    grounds: 'console-transport',
    reason: 'A person pressing Approve on the console\'s device page.',
  },
  'POST /auth/device/deny': {
    audience: 'excluded',
    grounds: 'console-transport',
    reason: 'The same page and the opposite answer, bounded as tightly as approve for the same reason.',
  },
  'GET /console/api/providers': {
    audience: 'excluded',
    grounds: 'console-transport',
    reason: 'Which provider keys and budgets an organization holds, for the console. Separate from /v1/providers because teaching one endpoint both a cookie and a bearer token is how it ends up accepting the weaker one.',
  },
  'PUT /console/api/providers/:provider': {
    audience: 'excluded',
    grounds: 'console-transport',
    reason: 'Seals and stores one key from the console. Needs the CSRF header as well as the cookie, which no generated client would send.',
  },
  'DELETE /console/api/providers/:provider': {
    audience: 'excluded',
    grounds: 'console-transport',
    reason: 'Revokes one from the console, under the same cookie and CSRF pair.',
  },
  'PUT /console/api/providers/:provider/budget': {
    audience: 'excluded',
    grounds: 'console-transport',
    reason: 'Sets the spend cap the model proxy enforces, from the console.',
  },

  // ---------------------------------------------------------------------
  // The command line's wire. `af` is stable; this is not.
  // ---------------------------------------------------------------------
  'POST /auth/device/code': {
    audience: 'excluded',
    grounds: 'cli-transport',
    reason: 'Starts the device flow. The surface is `af login`, and the exchange it runs is free to change under it.',
  },
  'POST /auth/device/token': {
    audience: 'excluded',
    grounds: 'cli-transport',
    reason: 'What `af login` polls while a person approves the code in a browser.',
  },
  'GET /v1/whoami': {
    audience: 'excluded',
    grounds: 'cli-transport',
    reason: 'The wire under `af whoami`. The command and its --output json fields are the promise; this shape is not.',
  },
  'POST /v1/logout': {
    audience: 'excluded',
    grounds: 'cli-transport',
    reason: 'The wire under `af logout`. It must never be refused in practice: a sign-out that fails leaves a live credential on a machine somebody is cleaning.',
  },
  'GET /v1/providers': {
    audience: 'excluded',
    grounds: 'cli-transport',
    reason: 'The wire under `af provider list`, and the read a script runs before deciding whether to set anything.',
  },
  'PUT /v1/providers/:provider': {
    audience: 'excluded',
    grounds: 'cli-transport',
    reason: 'The wire under `af provider set`. The plaintext key travels in one direction only and no route returns one.',
  },
  'DELETE /v1/providers/:provider': {
    audience: 'excluded',
    grounds: 'cli-transport',
    reason: 'The wire under `af provider rm`, which is the call somebody makes in a hurry when a key has leaked.',
  },
  'PUT /v1/providers/:provider/budget': {
    audience: 'excluded',
    grounds: 'cli-transport',
    reason: 'The wire under `af provider budget`, which sets the monthly cap the model proxy enforces.',
  },
  'POST /v1/tokens': {
    audience: 'excluded',
    grounds: 'cli-transport',
    reason: 'The wire under `af token create`. The plaintext is shown once by the command and never returned again.',
  },
  'GET /v1/tokens': {
    audience: 'excluded',
    grounds: 'cli-transport',
    reason: 'The wire under `af token list`, which prints the labels and never the tokens.',
  },
  'DELETE /v1/tokens/:token': {
    audience: 'excluded',
    grounds: 'cli-transport',
    reason: 'The wire under `af token rm`, the command that revokes a credential somebody has lost.',
  },

  // ---------------------------------------------------------------------
  // The OIDC claim routes, which have no command in front of them.
  // ---------------------------------------------------------------------
  //
  // These are the four that made this file necessary. guides/github.md hands a
  // customer a curl line for them, they are gated on the tokens.manage scope,
  // and they take the same bearer token the document already declares. There
  // is no `af` command, so the HTTP call IS the surface and there is nothing
  // else it could be. They are contract, and the register in the test names
  // them as a gap that is being closed rather than as an exemption.
  'POST /v1/oidc/bindings': {
    audience: 'contract',
    reason: 'Claims a repository for an organization by hand, for a repository the GitHub App is not installed on. Documented as a curl line, so the request is what a customer writes.',
  },
  'GET /v1/oidc/bindings': {
    audience: 'contract',
    reason: 'Lists the repositories an organization has claimed. The only way to read them back.',
  },
  'DELETE /v1/oidc/bindings/:binding': {
    audience: 'contract',
    reason: 'Revokes a claim by its identifier, which is the form the list above hands back.',
  },
  'DELETE /v1/oidc/bindings/:owner/:name': {
    audience: 'contract',
    reason: 'Revokes a claim by the repository it is on, which is the form guides/github.md shows because it is the form a person has to hand.',
  },

  // ---------------------------------------------------------------------
  // The engine's wire.
  // ---------------------------------------------------------------------
  'POST /v1/engine/token': {
    audience: 'excluded',
    grounds: 'engine-transport',
    reason: 'The engine exchanges the workflow identity for its own token here. Unlike /v1/auth/github-oidc, nobody writes this call: the binary makes it, and both ends ship from this repository.',
  },
  'POST /v1/pr/callback-token': {
    audience: 'excluded',
    grounds: 'engine-transport',
    reason: 'The engine exchanges the workflow identity for a credential scoped to one commit. Made by the binary, not by a workflow author.',
  },
  'POST /v1/pr/report': {
    audience: 'excluded',
    grounds: 'engine-transport',
    reason: 'What the engine says about the commit it checked, using that credential.',
  },
  'GET /v1/environments/:envId': {
    audience: 'excluded',
    grounds: 'engine-transport',
    reason: 'The engine reads back an environment it reported. Scoped to the token\'s organization by the same tenant transaction every other read uses.',
  },

  // ---------------------------------------------------------------------
  // Credentials the document does not describe.
  // ---------------------------------------------------------------------
  // The operator sign in. #93 argues the case at length in openapi.ts and the
  // argument is right: an operator route is not something a customer can call.
  // It takes an operator session from a different table, issued by a different
  // sign in, carried in a differently named `__Host-` cookie with its own CSRF
  // token, so describing one correctly would still describe an API no reader of
  // this document is able to use. What that argument could not do on its own is
  // say so out loud, because a route absent from the document looked the same
  // whether it was withheld on purpose or forgotten. These three say it.
  'POST /v1/admin/signin': {
    audience: 'excluded',
    grounds: 'different-credential',
    reason: 'Issues an operator session from the operators table, which no customer credential in this document can obtain. Publishing it would map the operator surface for somebody enumerating it.',
  },
  'POST /v1/admin/signout': {
    audience: 'excluded',
    grounds: 'different-credential',
    reason: 'Clears that operator session. Meaningless to a caller who could never have been issued one.',
  },
  'GET /v1/admin/session': {
    audience: 'excluded',
    grounds: 'different-credential',
    reason: 'Reports which operator is signed in, for the admin console. It reads the operator cookie and nothing else, so a session cookie or a bearer token reaches it as an anonymous caller.',
  },
  // Stepping into a customer's account, and stepping back out. Plain routes
  // rather than procedures because both end in a Set-Cookie for the CUSTOMER's
  // session, and because the operator gate refuses every admin procedure while
  // a session is impersonating, which would make a tRPC end unreachable exactly
  // when it is the only thing left to press. See src/admin/customers.ts.
  'POST /v1/admin/impersonation/start': {
    audience: 'excluded',
    grounds: 'different-credential',
    reason: 'Takes an operator session from the operators table and issues a customer session against it. No credential this document describes can obtain the first, and publishing the shape would map the operator surface for somebody enumerating it.',
  },
  'POST /v1/admin/impersonation/end': {
    audience: 'excluded',
    grounds: 'different-credential',
    reason: 'Ends that impersonation and revokes the session it issued. Meaningless to a caller who could never have started one.',
  },

  'POST /webhooks/github': {
    audience: 'excluded',
    grounds: 'different-credential',
    reason: 'The credential is an HMAC over the raw body, verified before the body is parsed. GitHub is the only caller and it was never going to read this document.',
  },
  'POST /webhooks/stripe': {
    audience: 'excluded',
    grounds: 'different-credential',
    reason: 'The same arrangement for billing deliveries from Stripe, verified the same way and handled once.',
  },
  'POST /v1/leads': {
    audience: 'excluded',
    grounds: 'console-transport',
    reason:
      'The enterprise contact form on the marketing site posts it. It is the one route here a cross origin browser may call, it takes no credential and returns an id, and nothing integrates with it.',
  },
  'OPTIONS /v1/leads': {
    audience: 'excluded',
    grounds: 'not-an-endpoint',
    reason: 'The preflight for the route above. A browser sends it; nothing calls it.',
  },
  'GET /exports/deletion': {
    audience: 'excluded',
    grounds: 'different-credential',
    reason: 'The credential is the token in the link mailed at closure. A deleted organization has no members left to authenticate, so no session or token in this document reaches it.',
  },

  // ---------------------------------------------------------------------
  // Somebody else's shape, and the deployment's own.
  // ---------------------------------------------------------------------
  'POST /byok/anthropic/v1/messages': {
    audience: 'excluded',
    grounds: 'foreign-shape',
    reason: 'The budgeted model proxy speaks Anthropic\'s Messages API. Publishing it would publish a copy of their contract that goes wrong the moment they change it, and their own documentation is the correct one to read.',
  },
  'POST /byok/openai/v1/chat/completions': {
    audience: 'excluded',
    grounds: 'foreign-shape',
    reason: 'The same, for OpenAI-shaped requests, for the same reason.',
  },
  'GET /metrics': {
    audience: 'excluded',
    grounds: 'operator-facing',
    reason: 'Prometheus text format for whoever runs the deployment. Not JSON, not versioned with the API, and read by a scraper rather than by a client.',
  },
  'GET /openapi.json': {
    audience: 'excluded',
    grounds: 'operator-facing',
    reason: 'Serves the document itself. A document describing the route that serves it is circular, and the address is in reference/api.md where a person looking for it will be.',
  },

  // ---------------------------------------------------------------------
  // The marketing site's beacon.
  // ---------------------------------------------------------------------
  // Classified `console-transport` because that is the nearest of the seven
  // grounds and none of them was written with this route in mind. The ground's
  // own definition names the console; the property it is really about is that
  // the caller is a page this repository ships, so there is no second client
  // to generate one for. That holds here: the caller is www/lib/analytics.ts,
  // the origin check bounds the route to the site's own origin, and both ends
  // move in this repository. If a reader disagrees, the honest fix is an
  // eighth ground rather than a looser reading of this one.
  'OPTIONS /v1/site/events': {
    audience: 'excluded',
    grounds: 'console-transport',
    reason: 'The CORS preflight for the beacon below. A browser sends it; nothing calls it, and there is no operation to describe.',
  },
  'POST /v1/site/events': {
    audience: 'excluded',
    grounds: 'console-transport',
    reason: 'Where the marketing site posts page views. The caller is JavaScript this repository ships, the origin check refuses anything else from a browser, and the wire shape is free to change with the page that produces it.',
  },
}

/**
 * The classification for a route, or undefined when nothing classifies it.
 *
 * Exact match only. limitFor resolves a concrete request path against the
 * catalogue's patterns because it runs per request; this compares two patterns
 * that both came from the router, so anything cleverer than equality would only
 * be a way to match something that was never registered.
 */
export function boundaryFor(method: string, path: string): RouteBoundary | undefined {
  return ROUTE_BOUNDARY[`${method} ${path}`]
}

/**
 * The route pattern as the published document spells it.
 *
 * Hono writes a parameter `:envId` and OpenAPI writes it `{envId}`. Comparing
 * the strings finds nothing, which would make every parameterised route look
 * undocumented and the gate look like noise.
 */
export function documentPath(path: string): string {
  return path.replace(/:([A-Za-z0-9_]+)/g, '{$1}')
}
