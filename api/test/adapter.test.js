"use strict";

/**
 * `api/waitlist/index.js`, which nothing else covers.
 *
 * The split into `shared/waitlist.js` made the decisions testable and left the
 * adapter behind, and the adapter is the file the platform actually invokes.
 * Its four behaviours are each a real thing a visitor can hit and none of them
 * were reachable from the existing suite, which loads `shared/waitlist` and
 * never loads this file at all:
 *
 * - a deployment with no connection string answers 503, rather than telling
 *   somebody their perfectly good address was wrong,
 * - a surprise from anywhere inside is a 503 carrying our JSON, which is the
 *   claim the file's own comment makes about not handing the host's error body
 *   to a public path,
 * - the caller's address is the FIRST entry of x-forwarded-for, because Static
 *   Web Apps appends the caller and rate limiting the wrong end of that list
 *   would limit the platform instead of the visitor,
 * - the table client is built once per cold start, not once per request.
 *
 * No network and no clock. The connection string below is well formed and
 * points nowhere; `TableClient.fromConnectionString` only parses it, and
 * `join` is stubbed through the module cache so the adapter's own branches are
 * what runs.
 */

const test = require("node:test");
const assert = require("node:assert/strict");
const path = require("node:path");

const SHARED = require.resolve("../shared/waitlist");
const ADAPTER = require.resolve("../waitlist/index.js");

// Parsed, never dialled. AccountKey has to be valid base64 or the credential
// refuses to construct, which would fail these tests for the wrong reason.
const FAKE_CONNECTION =
  "DefaultEndpointsProtocol=https;AccountName=notreal;" +
  "AccountKey=bm90LWEtcmVhbC1rZXktYXQtYWxsLXBhZGRpbmc9;" +
  "TableEndpoint=https://notreal.table.core.windows.net/;";

/**
 * Loads a fresh adapter with `join` replaced. Fresh because the adapter caches
 * its table client in a module-level variable, and a cached client from one
 * test is exactly the kind of leak that makes a later failure unreadable.
 */
function loadAdapter(join) {
  delete require.cache[ADAPTER];
  delete require.cache[SHARED];
  const real = require(SHARED);
  require.cache[SHARED] = {
    id: SHARED,
    filename: SHARED,
    loaded: true,
    paths: [],
    path: path.dirname(SHARED),
    exports: { ...real, join },
  };
  const handler = require(ADAPTER);
  delete require.cache[SHARED];
  return handler;
}

function fakeContext() {
  const logged = [];
  return { log: { error: (...args) => logged.push(args) }, logged };
}

test("a deployment with no connection string answers 503, not 400", async () => {
  const previous = process.env.WAITLIST_TABLE_CONNECTION;
  delete process.env.WAITLIST_TABLE_CONNECTION;
  try {
    // Asserting the 503 alone is not enough and the first version of this test
    // made exactly that mistake: if `tableClient` returned null rather than
    // throwing, the request would sail past the guard, fail deeper in, and be
    // caught by the outer handler, which answers with the same 503 and the
    // same one log line. Both paths look identical from the response. What
    // separates them is that the request must never get as far as `join`, and
    // that the log has to say which of the two happened.
    let reached = false;
    const handler = loadAdapter(async () => {
      reached = true;
      throw new Error("join must not be reached without a table");
    });
    const context = fakeContext();
    await handler(context, { headers: {}, body: { email: "someone@example.test" } });

    assert.equal(reached, false, "the request reached join with no table behind it");
    assert.equal(context.res.status, 503);
    assert.deepEqual(JSON.parse(context.res.body), {
      ok: false,
      message: "The waitlist is temporarily unavailable. Please try again later.",
    });
    assert.equal(context.logged.length, 1, "a misconfigured deployment is worth a log line");
    assert.match(String(context.logged[0][0]), /not configured/);
  } finally {
    if (previous !== undefined) process.env.WAITLIST_TABLE_CONNECTION = previous;
  }
});

test("a 503 still carries our own JSON headers", async () => {
  const previous = process.env.WAITLIST_TABLE_CONNECTION;
  delete process.env.WAITLIST_TABLE_CONNECTION;
  try {
    const handler = loadAdapter(async () => ({ status: 200, body: {} }));
    const context = fakeContext();
    await handler(context, { headers: {}, body: {} });
    assert.equal(context.res.headers["content-type"], "application/json; charset=utf-8");
    // An address is personal data and a shared cache must never hold an answer
    // about one, including the answer that we could not store it.
    assert.equal(context.res.headers["cache-control"], "no-store");
  } finally {
    if (previous !== undefined) process.env.WAITLIST_TABLE_CONNECTION = previous;
  }
});

test("a surprise from inside is a 503 with our body, not the host's 500", async () => {
  // The defect this guards is the one the whole outage was about, in miniature:
  // a public path answering with the platform's own error body says more about
  // the insides than it should and reads to a visitor as a broken product.
  process.env.WAITLIST_TABLE_CONNECTION = FAKE_CONNECTION;
  const handler = loadAdapter(async () => {
    throw new Error("something nobody predicted");
  });
  const context = fakeContext();
  await handler(context, { headers: {}, body: { email: "someone@example.test" } });

  assert.equal(context.res.status, 503);
  assert.equal(JSON.parse(context.res.body).ok, false);
  assert.ok(!context.res.body.includes("something nobody predicted"), "the error text must not reach the caller");
  assert.equal(context.logged.length, 1);
});

test("the adapter passes the decision through untouched", async () => {
  process.env.WAITLIST_TABLE_CONNECTION = FAKE_CONNECTION;
  const handler = loadAdapter(async () => ({ status: 429, body: { ok: false, message: "Too many attempts." } }));
  const context = fakeContext();
  await handler(context, { headers: {}, body: { email: "someone@example.test" } });

  assert.equal(context.res.status, 429);
  assert.deepEqual(JSON.parse(context.res.body), { ok: false, message: "Too many attempts." });
  assert.equal(context.logged.length, 0, "a rate limited caller is not an incident");
});

test("the caller is the first entry of x-forwarded-for, not the last", async () => {
  // Static Web Apps appends the caller as the request passes through, so the
  // last entry is the platform. Rate limiting on the last entry would count
  // every visitor into one bucket and lock the whole site out after five
  // signups, which is a denial of service we would have shipped ourselves.
  process.env.WAITLIST_TABLE_CONNECTION = FAKE_CONNECTION;
  const seen = [];
  const handler = loadAdapter(async (args) => {
    seen.push(args.ip);
    return { status: 200, body: { ok: true } };
  });

  await handler(fakeContext(), {
    headers: { "x-forwarded-for": "203.0.113.7, 70.37.0.1" },
    body: { email: "someone@example.test" },
  });
  await handler(fakeContext(), { headers: { "x-forwarded-for": "  203.0.113.8  " }, body: {} });
  await handler(fakeContext(), { headers: {}, body: {} });
  await handler(fakeContext(), {}, undefined);

  assert.deepEqual(seen, ["203.0.113.7", "203.0.113.8", "unknown", "unknown"]);
});

test("the table client is built once per cold start, not once per request", async () => {
  // A client per request opens a new connection pool on every signup. The
  // caching is a module-level variable, so nothing but identity across calls
  // can show that it is working.
  process.env.WAITLIST_TABLE_CONNECTION = FAKE_CONNECTION;
  const tables = [];
  const handler = loadAdapter(async (args) => {
    tables.push(args.table);
    return { status: 200, body: { ok: true } };
  });

  const request = { headers: {}, body: { email: "someone@example.test" } };
  await handler(fakeContext(), request);
  await handler(fakeContext(), request);

  assert.equal(tables.length, 2);
  assert.ok(tables[0], "no table was handed to join");
  assert.equal(tables[0], tables[1], "a second request built a second client");
});
