"use strict";

/**
 * Every refusal this function app can return, in one place, published.
 *
 * The engine and the control plane share one namespace, `AF-<AREA>-<NNN>`, and
 * every code in it has a message, a resolution, a documentation page and a row
 * in https://antifailure.dev/errors.v1.json, enforced in both directions by
 * `tools/errcheck`. This function app is not in that namespace and should not
 * be: it answers for the marketing host, not for the product, and none of its
 * situations is one the product can recover from by looking up a product error.
 *
 * What was wrong before this table existed is that the codes were written out
 * at each throw site, so nothing listed them and a caller receiving one had no
 * way to find out what it meant. Two things fix that and both are here: the
 * bodies are built from this table, so a code cannot be emitted without an
 * entry, and `GET /api` publishes the table, so a caller can resolve any code
 * it receives from the same host that sent it.
 *
 * The table is down to one entry, which is the right size for what this app
 * does now. Three others went with the waitlist, kept honest by the test below
 * that fails on a catalog entry no code path can return: an entry nothing emits
 * is a description, published at GET /api, of a situation this app cannot be
 * in.
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
