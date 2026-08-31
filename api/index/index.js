"use strict";

/**
 * GET /api, and anything under /api that no other function answers.
 *
 * This exists because of what antifailure.dev/api actually did. Every path
 * under it answered `500 Backend call failure` with an empty body, because the
 * deploy workflow published the site with no API at all and Static Web Apps
 * was left routing /api at a function app that had stopped existing. That is
 * two separate failures wearing one status code, and the deploy is the fix for
 * the second one. This is the fix for what a reader gets when there is simply
 * nothing at the address they asked for.
 *
 * A 404 with no body is technically correct and tells a person nothing. The
 * question somebody typing antifailure.dev/api into a browser is asking is
 * whether this product has an API, and the honest answer is that this is not
 * it: this host serves the marketing site, and the product's API is the
 * control plane's. So the answer says that, and says where to go.
 *
 * The route is `{*path}`, which has the lowest precedence in the Functions
 * router, so a literal route like `waitlist` always wins. The methods are
 * narrowed to GET and HEAD as well, which is belt and braces: the waitlist is
 * POST only, so even if precedence ever went the other way this could not
 * shadow it and swallow a signup.
 */

const INDEX = {
  service: "antifailure.dev",
  description:
    "The API behind the marketing site. It has one endpoint, which is the waitlist form.",
  endpoints: [
    {
      method: "POST",
      path: "/api/waitlist",
      description:
        "Adds one email address to the design partner waitlist. Idempotent on the address. There is no endpoint that reads the list back.",
    },
  ],
  productApi: {
    description:
      "This is not the product's API. The control plane serves that, on its own host, and it needs a session or an engine token.",
    openapi: "https://app.antifailure.dev/openapi.json",
    documentation: "https://antifailure.dev/docs/reference/api",
  },
};

function respond(context, status, body) {
  context.res = {
    status,
    headers: {
      "content-type": "application/json; charset=utf-8",
      // A short cache rather than none: this answer changes when the site
      // deploys, and it is the reply to anything that guesses at a path, so
      // it is the one an abusive loop would hammer.
      "cache-control": "public, max-age=300",
    },
    body: JSON.stringify(body, null, 2),
  };
}

module.exports = async function (context, req) {
  const path = (req.params && typeof req.params.path === "string" ? req.params.path : "").replace(
    /^\/+|\/+$/g,
    "",
  );

  if (path === "") {
    respond(context, 200, INDEX);
    return;
  }

  respond(context, 404, {
    ok: false,
    // The requested path is deliberately not echoed. Reflecting a caller's
    // string into a response body is how a JSON endpoint becomes somebody's
    // content injection demonstration, and it adds nothing here.
    message: "No such endpoint. GET /api lists what this host serves.",
    productApi: INDEX.productApi,
  });
};

// Exported so the tests can assert the pointer stays a real address rather
// than drifting into a 404 on our own documentation site.
module.exports.INDEX = INDEX;
