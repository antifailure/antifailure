"use strict";

/**
 * Every refusal this function app can return, in one place, published.
 *
 * The engine and the control plane share one namespace, `AF-<AREA>-<NNN>`, and
 * every code in it has a message, a resolution, a documentation page and a row
 * in https://antifailure.dev/errors.v1.json, enforced in both directions by
 * `tools/errcheck`. This function app is not in that namespace and should not
 * be: it is the marketing site's form backend, its codes are already live in
 * `www/lib/waitlist.ts`, and renaming them would break a shipped client to buy
 * an agent nothing, because none of these situations is one the product can
 * recover from by looking up a product error.
 *
 * What was actually wrong is that the codes were written out at each throw
 * site, so nothing listed them, nothing checked them, and a caller receiving
 * `waitlist_unavailable` had no way to find out what it meant. Two things fix
 * that and both are here: the bodies are built from this table, so a code
 * cannot be emitted without an entry, and `GET /api` publishes the table, so a
 * caller can resolve any code it receives from the same host that sent it.
 *
 * `api/test/errors.test.js` reads this directory's source for code literals and
 * fails on one that is not here, which is what stops a future emitter from
 * quietly going back to writing its own.
 */

const CATALOG = {
  endpoint_not_found: {
    status: 404,
    message: "No such endpoint.",
    resolution: "GET /api to list this host's public endpoints and product API.",
  },
  invalid_email: {
    status: 400,
    message: "That does not look like an email address.",
    resolution: "Enter one complete email address and submit it again.",
  },
  rate_limited: {
    status: 429,
    message: "Too many attempts.",
    resolution: "Wait one minute before trying again.",
  },
  waitlist_unavailable: {
    status: 503,
    message: "The waitlist is temporarily unavailable. Please try again later.",
    resolution: "Wait a minute and submit the same address again.",
  },
};

/**
 * The body and status for one code.
 *
 * Throws on an unknown code rather than inventing a body. A wrong code here is
 * a programming mistake, and answering a visitor with an empty message because
 * of it is strictly worse than the 500 the platform would produce.
 */
function failure(code, extra) {
  const entry = CATALOG[code];
  if (!entry) throw new Error(`no catalog entry for the error code ${code}`);
  return {
    status: entry.status,
    body: {
      ok: false,
      code,
      message: entry.message,
      resolution: entry.resolution,
      ...(extra || {}),
    },
  };
}

/** The catalog as an array, for publishing. */
function published() {
  return Object.entries(CATALOG).map(([code, entry]) => ({
    code,
    status: entry.status,
    message: entry.message,
    resolution: entry.resolution,
  }));
}

module.exports = { CATALOG, failure, published };
