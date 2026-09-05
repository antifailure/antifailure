/**
 * Every control plane route this site calls, in one place, as a value.
 *
 * THE FAILURE THIS EXISTS FOR. The careers form posted to
 * `POST /v1/applications`. The form was correct, the route was correct, and the
 * route was in `web/apps/api/src/server.ts` on main. A person filling the form
 * in on antifailure.dev was told "Could not reach the server", because the
 * control plane answering that origin was still serving v1.1.1 and v1.1.1 has
 * no such route. The site had shipped 22 commits ahead of the API it posts to.
 *
 * That is not a bug in either half. It is the deployment split working as
 * designed: this site publishes on every merge to main, and the control plane
 * only moves when a `v*` tag is promoted. deploy.yml already writes that split
 * down, and already decided it was tolerable, on the grounds that "the paths
 * are additive and a caller reading an operation that is not there yet gets a
 * 404". That reasoning is sound for `openapi.json`, which is a document a
 * reader consults. It is wrong here, because this site is not a reader of the
 * control plane. It is a CALLER of it, and a caller that gets a 404 is a form
 * that does not work.
 *
 * WHY A LIST AND NOT A GREP. The URLs were built at the call sites, as
 * `` fetch(`${CONTROL_PLANE_URL}/v1/applications`) ``. Nothing can enumerate
 * those reliably: a template literal can hold an expression, a path can be
 * assembled from a variable, and an extractor that reads four of five call
 * sites and says nothing about the fifth is the shape of instrument this
 * repository keeps having to throw away. So the inventory is a value that the
 * call sites read, rather than a pattern something tries to recognise in them,
 * and `tools/routecheck` refuses any file in www that builds a control plane
 * URL outside this module. There is no call site it can fail to see, because a
 * call site it cannot see does not compile past the gate.
 *
 * WHY THE PROBE IS DESCRIBED HERE. Proving that the deployed control plane
 * serves a route means asking it, and asking it means sending it a request.
 * A request that reached a handler would be a real job application in a review
 * queue read by a person, which is not a thing a gate may create. So each entry
 * carries the request that proves the route is there and the reason that
 * request cannot reach the handler behind it. The reason is checked by reading
 * the API's source, not assumed, and it is written next to the route it is
 * about so that a route added here without one is visibly unfinished.
 *
 * @see tools/routecheck for the gate that reads this.
 */

import { CONTROL_PLANE_URL } from "./site";

/**
 * What a probe of this route does to the deployment it is aimed at.
 *
 * A closed set, so the question "which of these writes something" is answered
 * by counting rather than by reading five sentences and forming an impression.
 */
export type ProbeEffect =
  /**
   * The request cannot reach the route's handler. Middleware in front of it
   * refuses first, or the handler's own validation refuses before it touches
   * anything. Nothing is written and nothing is sent.
   */
  | "inert"
  /**
   * The request reaches the handler and the handler writes. Naming it here is
   * the point: `tools/routecheck` refuses to send one of these unless it is
   * told to in as many words, so a probe with a cost is never sent by a run
   * that did not know it was paying it.
   */
  | "writes";

/** One route this site calls, and how to find out whether it is really there. */
export interface ControlPlaneRoute {
  /** The method the site itself uses. A route registered for POST answers 404
   *  to a GET, so a probe with the wrong method cannot tell "the route is not
   *  there" from "I asked the wrong way". */
  readonly method: "GET" | "POST";
  /** The path, as a literal. Never assembled. */
  readonly path: string;
  /** Where in this site it is reached from, so a reader can go and look. */
  readonly calledFrom: string;
  /** What breaks for a person when the deployed control plane lacks it. */
  readonly whenMissing: string;
  /** What a probe costs. See ProbeEffect. */
  readonly probeEffect: ProbeEffect;
  /** Why the probe is what it says it is, checked against the API's source.
   *  For "inert", this names the code that refuses before any write. For
   *  "writes", it names what gets written. */
  readonly probeReason: string;
}

/**
 * The inventory.
 *
 * Adding a route here is how a new call site is declared. Adding a call site
 * without one fails `tools/routecheck` in the `www` gate, naming the file and
 * the line, before the pull request that added it can merge.
 */
export const CONTROL_PLANE_ROUTES = {
  "applications.create": {
    method: "POST",
    path: "/v1/applications",
    calledFrom: "components/pages/company/ApplicationForm.tsx",
    whenMissing:
      "The careers form says 'Could not reach the server' and a job application is lost.",
    probeEffect: "inert",
    probeReason:
      "recruitment/routes.ts registers an `app.use` in front of the route that answers 403 unless the Origin header is exactly the configured site origin. A probe sends no Origin, so it is refused before the body is read and recordApplication is never called.",
  },
  "leads.create": {
    method: "POST",
    path: "/v1/leads",
    calledFrom: "components/pages/company/EnterpriseForm.tsx",
    whenMissing:
      "The enterprise contact form says 'Could not reach the server' and a sales enquiry is lost.",
    probeEffect: "inert",
    probeReason:
      "server.ts validates the body with validateLead, which is pure, and returns 400 on an empty name before recordLead is reached. A probe sends {} and is refused on the first check. It does spend one token of the shared auth rate limit bucket, which is why a 429 from this route counts as the route being present.",
  },
  "site.events": {
    method: "POST",
    path: "/v1/site/events",
    calledFrom: "lib/beacon.ts",
    whenMissing:
      "The site beacon posts into nothing. No reader sees an error, which is why this one needs a gate more than the forms do.",
    probeEffect: "inert",
    probeReason:
      "server.ts refuses the route with 403 'This endpoint serves the marketing site only.' unless the Origin header matches the configured site origin. A probe sends no Origin and never reaches the ingest.",
  },
  "auth.github": {
    method: "GET",
    path: "/auth/github",
    calledFrom: "components/AuthScreen.tsx",
    whenMissing:
      "'Continue with GitHub' lands on a 404 page. It is the primary conversion path off this site.",
    probeEffect: "writes",
    probeReason:
      "auth/signin.ts beginSignIn inserts one row into oauth_states unconditionally, and nothing in front of the handler refuses a request that carries no credential. A probe therefore creates one expiring state row, exactly as a person who clicks the button and then closes the tab does. There is no request that proves this route is present without doing that, so it is declared rather than hidden.",
  },
} as const satisfies Record<string, ControlPlaneRoute>;

/** The name of a route in the inventory. */
export type ControlPlaneRouteName = keyof typeof CONTROL_PLANE_ROUTES;

/**
 * The absolute URL of a route, for a call site to fetch or link to.
 *
 * The one function that turns an entry above into a URL. CONTROL_PLANE_URL has
 * any trailing slash stripped by lib/site.ts, so this is a plain join.
 */
export function controlPlaneUrl(name: ControlPlaneRouteName): string {
  return CONTROL_PLANE_URL + CONTROL_PLANE_ROUTES[name].path;
}
