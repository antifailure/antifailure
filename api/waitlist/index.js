"use strict";

/**
 * POST /api/waitlist
 *
 * Stores one email address in the `waitlist` table so that the sentence on the
 * sign-up screen is true. Before this existed, the address was written to the
 * visitor's own localStorage under a heading that promised we would email
 * them, which nothing could have done.
 *
 * Design notes worth keeping, because each one is a decision rather than a
 * default:
 *
 * - The row key is a hash of the address, not the address. Table Storage keys
 *   appear in request URLs and in diagnostics, and an address is personal
 *   data. The plaintext lives in a property, which is what we actually need to
 *   read back when it is time to email people.
 * - Signing up twice is not an error and is not a second row. The upsert is
 *   keyed on that hash, so the endpoint is idempotent and the caller is told
 *   which of the two happened.
 * - The endpoint never reads the list back. There is no GET. A marketing page
 *   with an anonymous endpoint that can enumerate its own signups is how a
 *   waitlist becomes a leaked mailing list.
 * - Rate limiting is per address and per remote IP, in memory. That is honest
 *   about what it is: a speed bump against a loop, not protection against a
 *   distributed flood, which is the platform's job.
 */

const crypto = require("crypto");
const { TableClient, odata } = require("@azure/data-tables");

const TABLE = "waitlist";
const MAX_EMAIL_LENGTH = 254; // RFC 5321 upper bound on a path
const RATE_LIMIT_WINDOW_MS = 60_000;
const RATE_LIMIT_MAX = 5;

// A single client per cold start. Creating one per request would open a new
// connection pool on every signup.
let cachedClient = null;

function tableClient() {
  if (cachedClient) return cachedClient;
  const connectionString = process.env.WAITLIST_TABLE_CONNECTION;
  if (!connectionString) {
    throw new Error("WAITLIST_TABLE_CONNECTION is not configured");
  }
  cachedClient = TableClient.fromConnectionString(connectionString, TABLE, {
    allowInsecureConnection: false,
  });
  return cachedClient;
}

/**
 * Deliberately permissive. The job here is to reject what is certainly not an
 * address and to bound the length, not to adjudicate RFC 5322: a regex strict
 * enough to be "correct" rejects real addresses, and the cost of storing one
 * junk row is far lower than the cost of turning away a real signup.
 */
function normaliseEmail(raw) {
  if (typeof raw !== "string") return null;
  const email = raw.trim().toLowerCase();
  if (email.length < 6 || email.length > MAX_EMAIL_LENGTH) return null;
  if (!/^[^\s@]+@[^\s@]+\.[^\s@]{2,}$/.test(email)) return null;
  if (email.includes("..")) return null;
  return email;
}

function keyFor(email) {
  return crypto.createHash("sha256").update(email).digest("hex");
}

const hits = new Map();

function rateLimited(identifier, now) {
  const window = hits.get(identifier);
  if (!window || now - window.start > RATE_LIMIT_WINDOW_MS) {
    hits.set(identifier, { start: now, count: 1 });
    return false;
  }
  window.count += 1;
  return window.count > RATE_LIMIT_MAX;
}

// The map only grows if we let it. Sweep anything outside the window whenever
// it gets large, which on this traffic is approximately never.
function sweep(now) {
  if (hits.size < 2048) return;
  for (const [key, window] of hits) {
    if (now - window.start > RATE_LIMIT_WINDOW_MS) hits.delete(key);
  }
}

function clientIp(req) {
  const forwarded = req.headers["x-forwarded-for"];
  if (typeof forwarded === "string" && forwarded.length > 0) {
    // Static Web Apps appends the caller, so the first entry is the client.
    return forwarded.split(",")[0].trim();
  }
  return "unknown";
}

function json(context, status, body) {
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
  const now = Date.now();
  sweep(now);

  const email = normaliseEmail(req.body && req.body.email);
  if (!email) {
    json(context, 400, {
      ok: false,
      message: "That does not look like an email address.",
    });
    return;
  }

  const rowKey = keyFor(email);
  const ip = clientIp(req);
  if (rateLimited(`ip:${ip}`, now) || rateLimited(`email:${rowKey}`, now)) {
    json(context, 429, {
      ok: false,
      message: "Too many attempts. Try again in a minute.",
    });
    return;
  }

  const source = typeof req.body.source === "string" ? req.body.source.slice(0, 32) : "unknown";

  let client;
  try {
    client = tableClient();
  } catch (error) {
    // A misconfigured deployment is our problem, and the visitor should be
    // told the truth about whose fault it is rather than that their address
    // was wrong.
    context.log.error("waitlist storage is not configured", error);
    json(context, 503, {
      ok: false,
      message: "The waitlist is temporarily unavailable. Please try again later.",
    });
    return;
  }

  let alreadyJoined = false;
  try {
    await client.getEntity("waitlist", rowKey);
    alreadyJoined = true;
  } catch (error) {
    if (error.statusCode !== 404) {
      context.log.error("waitlist lookup failed", error);
      json(context, 503, {
        ok: false,
        message: "The waitlist is temporarily unavailable. Please try again later.",
      });
      return;
    }
  }

  try {
    await client.upsertEntity(
      {
        partitionKey: "waitlist",
        rowKey,
        email,
        source,
        // Keep the first time we saw somebody as well as the latest, so a
        // second signup does not erase when they actually found us.
        firstSeen: alreadyJoined ? undefined : new Date(now).toISOString(),
        lastSeen: new Date(now).toISOString(),
      },
      "Merge",
    );
  } catch (error) {
    context.log.error("waitlist write failed", error);
    json(context, 503, {
      ok: false,
      message: "The waitlist is temporarily unavailable. Please try again later.",
    });
    return;
  }

  json(context, 200, { ok: true, alreadyJoined });
};

// Exported for the unit tests, which cover the parts that are easy to get
// wrong and impossible to see: what counts as an address, and when the rate
// limiter trips.
module.exports.normaliseEmail = normaliseEmail;
module.exports.keyFor = keyFor;
module.exports.rateLimited = rateLimited;
module.exports.RATE_LIMIT_MAX = RATE_LIMIT_MAX;
module.exports.RATE_LIMIT_WINDOW_MS = RATE_LIMIT_WINDOW_MS;
module.exports.odata = odata;
