"use strict";

/**
 * The waitlist, separated from the Azure Functions entry point.
 *
 * The entry point owns the two things that exist only in production: the
 * connection string and the platform's request object. Everything here takes
 * what it needs as an argument, which is what makes the branches that matter
 * reachable from a test. The rate limiter tripping on the sixth attempt in a
 * minute, a storage lookup that fails for a reason other than "no such row",
 * and a write that fails outright are each a real thing a visitor can hit, and
 * none of them can be reached through a real clock or a real table.
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
 * - Nothing here reads the list back. There is no GET. A marketing page with
 *   an anonymous endpoint that can enumerate its own signups is how a waitlist
 *   becomes a leaked mailing list.
 * - Rate limiting is per address and per remote IP, in memory. That is honest
 *   about what it is: a speed bump against a loop, not protection against a
 *   distributed flood, which is the platform's job.
 */

const crypto = require("crypto");
const { failure } = require("./errors");

const PARTITION = "waitlist";
const MAX_EMAIL_LENGTH = 254; // RFC 5321 upper bound on a path
const MAX_SOURCE_LENGTH = 32;
const RATE_LIMIT_WINDOW_MS = 60_000;
const RATE_LIMIT_MAX = 5;

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

/**
 * A fixed window counter, per process.
 *
 * A class rather than a module level map because the entry point needs one
 * that survives between invocations on a warm instance, and a test needs one
 * that does not survive between cases. Sharing a map across tests makes the
 * order of the file decide whether a case passes.
 */
class RateLimiter {
  constructor({ windowMs = RATE_LIMIT_WINDOW_MS, max = RATE_LIMIT_MAX } = {}) {
    this.windowMs = windowMs;
    this.max = max;
    this.hits = new Map();
  }

  /** True when this identifier has already spent its allowance in the window. */
  limited(identifier, now) {
    this.sweep(now);
    const window = this.hits.get(identifier);
    if (!window || now - window.start > this.windowMs) {
      this.hits.set(identifier, { start: now, count: 1 });
      return false;
    }
    window.count += 1;
    return window.count > this.max;
  }

  // The map only grows if we let it. Sweep anything outside the window
  // whenever it gets large, which on this traffic is approximately never.
  sweep(now) {
    if (this.hits.size < 2048) return;
    for (const [key, window] of this.hits) {
      if (now - window.start > this.windowMs) this.hits.delete(key);
    }
  }
}

/** The body every storage failure answers with, so a visitor sees one message. */
function unavailable() {
  return failure("waitlist_unavailable");
}

/**
 * One signup. Returns the status and the body to send; it never touches the
 * platform's response object, which is the entry point's job.
 *
 * `table` is anything with the two methods used here, which in production is a
 * TableClient and in a test is an object that counts calls.
 */
async function join({ table, limiter, now, body, ip, log }) {
  // The address limit costs a hash, so the address has to be valid first. The
  // IP limit costs nothing and comes before validation on purpose: a loop
  // posting junk is the cheapest way to make this endpoint do work, and there
  // is no reason to parse a body for a caller that is already over its
  // allowance.
  if (limiter.limited(`ip:${ip}`, now)) {
    return failure("rate_limited");
  }

  const email = normaliseEmail(body && body.email);
  if (!email) {
    return failure("invalid_email");
  }

  const rowKey = keyFor(email);
  if (limiter.limited(`email:${rowKey}`, now)) {
    return failure("rate_limited");
  }

  const source =
    typeof body.source === "string" ? body.source.slice(0, MAX_SOURCE_LENGTH) : "unknown";

  let alreadyJoined = false;
  try {
    await table.getEntity(PARTITION, rowKey);
    alreadyJoined = true;
  } catch (error) {
    if (error.statusCode !== 404) {
      log("waitlist lookup failed", error);
      return unavailable();
    }
  }

  const seen = new Date(now).toISOString();
  try {
    await table.upsertEntity(
      {
        partitionKey: PARTITION,
        rowKey,
        email,
        source,
        // Keep the first time we saw somebody as well as the latest, so a
        // second signup does not erase when they actually found us. A Merge
        // upsert drops an undefined property rather than writing a null over
        // the value already there.
        firstSeen: alreadyJoined ? undefined : seen,
        lastSeen: seen,
      },
      "Merge",
    );
  } catch (error) {
    log("waitlist write failed", error);
    return unavailable();
  }

  return { status: 200, body: { ok: true, alreadyJoined } };
}

module.exports = {
  PARTITION,
  MAX_EMAIL_LENGTH,
  MAX_SOURCE_LENGTH,
  RATE_LIMIT_WINDOW_MS,
  RATE_LIMIT_MAX,
  RateLimiter,
  join,
  keyFor,
  normaliseEmail,
};
