"use strict";

/**
 * POST /api/waitlist
 *
 * Stores one email address in the `waitlist` table so that the sentence on the
 * sign-up screen is true. Before this existed, the address was written to the
 * visitor's own localStorage under a heading that promised we would email
 * them, which nothing could have done.
 *
 * The decisions live next to the logic in ../shared/waitlist.js. This file is
 * the Azure Functions adapter and nothing else: it builds a table client from
 * the environment, hands the request over, and turns the answer into a
 * response.
 *
 * The whole thing is wrapped, including the parts that cannot obviously throw.
 * An unhandled rejection here is not a log line, it is a 500 with the host's
 * own body on a public path, and a public endpoint that answers a surprise
 * with a 500 both looks broken and says more about its insides than it should.
 */

const { TableClient } = require("@azure/data-tables");
const { PARTITION, RateLimiter, join } = require("../shared/waitlist");

// A single client and a single limiter per cold start. Creating a client per
// request would open a new connection pool on every signup, and a limiter per
// request would count to one forever.
let cachedClient = null;
const limiter = new RateLimiter();

function tableClient() {
  if (cachedClient) return cachedClient;
  const connectionString = process.env.WAITLIST_TABLE_CONNECTION;
  if (!connectionString) {
    throw new Error("WAITLIST_TABLE_CONNECTION is not configured");
  }
  cachedClient = TableClient.fromConnectionString(connectionString, PARTITION, {
    allowInsecureConnection: false,
  });
  return cachedClient;
}

function clientIp(req) {
  const forwarded = req.headers && req.headers["x-forwarded-for"];
  if (typeof forwarded === "string" && forwarded.length > 0) {
    // Static Web Apps appends the caller, so the first entry is the client.
    return forwarded.split(",")[0].trim();
  }
  return "unknown";
}

function respond(context, status, body) {
  context.res = {
    status,
    headers: {
      "content-type": "application/json; charset=utf-8",
      "cache-control": "no-store",
    },
    body: JSON.stringify(body),
  };
}

module.exports = async function (context, req) {
  let table;
  try {
    table = tableClient();
  } catch (error) {
    // A misconfigured deployment is our problem, and the visitor should be
    // told the truth about whose fault it is rather than that their address
    // was wrong.
    context.log.error("waitlist storage is not configured", error);
    respond(context, 503, {
      ok: false,
      message: "The waitlist is temporarily unavailable. Please try again later.",
    });
    return;
  }

  try {
    const answer = await join({
      table,
      limiter,
      now: Date.now(),
      body: req.body,
      ip: clientIp(req),
      log: (message, error) => context.log.error(message, error),
    });
    respond(context, answer.status, answer.body);
  } catch (error) {
    context.log.error("waitlist request failed", error);
    respond(context, 503, {
      ok: false,
      message: "The waitlist is temporarily unavailable. Please try again later.",
    });
  }
};
