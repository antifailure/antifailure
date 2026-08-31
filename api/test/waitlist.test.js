"use strict";

/**
 * The waitlist endpoint's behaviour.
 *
 * The file this covers used to end with a comment saying its exports were
 * there "for the unit tests", and there were no unit tests: the whole api
 * directory had none, and neither CI nor the justfile ran anything in it. So
 * these start from the branches a visitor can actually reach rather than from
 * the ones that were easy to write.
 *
 * No real clock and no real table. `now` is a number the caller chooses, which
 * is the only way to prove a sixty second window without sleeping for one, and
 * the table is an object that records what it was asked to do.
 */

const test = require("node:test");
const assert = require("node:assert/strict");

const {
  MAX_EMAIL_LENGTH,
  MAX_SOURCE_LENGTH,
  PARTITION,
  RATE_LIMIT_MAX,
  RATE_LIMIT_WINDOW_MS,
  RateLimiter,
  join,
  keyFor,
  normaliseEmail,
} = require("../shared/waitlist");

/** A table that answers "no such row" and remembers every write. */
function fakeTable({ existing = new Set(), getFails = null, upsertFails = null } = {}) {
  const notFound = Object.assign(new Error("not found"), { statusCode: 404 });
  return {
    writes: [],
    gets: [],
    async getEntity(partitionKey, rowKey) {
      this.gets.push({ partitionKey, rowKey });
      if (getFails) throw getFails;
      if (existing.has(rowKey)) return { partitionKey, rowKey };
      throw notFound;
    },
    async upsertEntity(entity, mode) {
      this.writes.push({ entity, mode });
      if (upsertFails) throw upsertFails;
    },
  };
}

function call(overrides = {}) {
  return join({
    table: overrides.table || fakeTable(),
    limiter: overrides.limiter || new RateLimiter(),
    now: overrides.now === undefined ? 1_700_000_000_000 : overrides.now,
    body: overrides.body,
    ip: overrides.ip === undefined ? "203.0.113.7" : overrides.ip,
    log: overrides.log || (() => {}),
  });
}

test("normaliseEmail accepts an address and lowercases it", () => {
  assert.equal(normaliseEmail("  Person@Example.COM "), "person@example.com");
});

test("normaliseEmail rejects what is certainly not an address", () => {
  for (const value of [
    undefined,
    null,
    42,
    {},
    "",
    "a@b.c", // under the six character floor
    "no-at-sign.example.com",
    "two@@example.com",
    "trailing@example.",
    "spaced address@example.com",
    "double..dot@example.com",
    `${"a".repeat(MAX_EMAIL_LENGTH)}@example.com`, // over the RFC 5321 length bound
  ]) {
    assert.equal(normaliseEmail(value), null, `expected ${JSON.stringify(value)} to be rejected`);
  }
});

test("normaliseEmail accepts an address of exactly the length bound", () => {
  // The rejection list above only reaches the length bound from far above it,
  // so `>=` in place of `>` would pass every test in this file while turning
  // away the longest address RFC 5321 allows. The boundary is the only place
  // an off-by-one lives, so it is the place worth asserting: 254 is kept, 255
  // is not.
  const domain = "@example.com";
  const longest = "a".repeat(MAX_EMAIL_LENGTH - domain.length) + domain;
  assert.equal(longest.length, MAX_EMAIL_LENGTH);
  assert.equal(normaliseEmail(longest), longest);
  assert.equal(normaliseEmail("a" + longest), null);
});

test("normaliseEmail keeps the shapes a strict regex would turn away", () => {
  // Each of these is a real deliverable address. A validator that rejects any
  // of them costs a signup, which is far more expensive than one junk row.
  for (const value of [
    "person+tag@example.com",
    "first.last@sub.domain.example.co.uk",
    "p@example.museum",
    "person_1@example-host.com",
  ]) {
    assert.equal(normaliseEmail(value), value);
  }
});

test("keyFor hashes the address rather than carrying it", () => {
  const key = keyFor("person@example.com");
  assert.match(key, /^[0-9a-f]{64}$/);
  assert.ok(!key.includes("person"));
  assert.equal(key, keyFor("person@example.com"));
  assert.notEqual(key, keyFor("other@example.com"));
});

test("the rate limiter allows the maximum and refuses the next one", () => {
  // The shipped numbers rather than convenient ones, so that loosening the
  // allowance on a public write endpoint has to be a deliberate edit here too.
  const limiter = new RateLimiter();
  const now = 1_000;
  assert.equal(limiter.max, RATE_LIMIT_MAX);
  assert.equal(limiter.windowMs, RATE_LIMIT_WINDOW_MS);
  for (let i = 0; i < RATE_LIMIT_MAX; i += 1) {
    assert.equal(limiter.limited("ip:one", now), false, `attempt ${i + 1} should be allowed`);
  }
  assert.equal(limiter.limited("ip:one", now), true);
});

test("the rate limiter forgets an identifier once its window has passed", () => {
  const limiter = new RateLimiter({ windowMs: 60_000, max: 1 });
  assert.equal(limiter.limited("ip:one", 1_000), false);
  assert.equal(limiter.limited("ip:one", 1_000), true);
  assert.equal(limiter.limited("ip:one", 61_001), false, "a new window starts clean");
});

test("the rate limiter counts identifiers separately", () => {
  const limiter = new RateLimiter({ windowMs: 60_000, max: 1 });
  assert.equal(limiter.limited("ip:one", 1_000), false);
  assert.equal(limiter.limited("ip:two", 1_000), false);
});

test("a first signup writes one row keyed on the hash, not the address", async () => {
  const table = fakeTable();
  const answer = await call({ table, body: { email: "Person@Example.com", source: "footer" } });

  assert.equal(answer.status, 200);
  assert.deepEqual(answer.body, { ok: true, alreadyJoined: false });
  assert.equal(table.writes.length, 1);

  const { entity, mode } = table.writes[0];
  assert.equal(mode, "Merge");
  assert.equal(entity.partitionKey, PARTITION);
  assert.equal(entity.rowKey, keyFor("person@example.com"));
  assert.equal(entity.email, "person@example.com");
  assert.equal(entity.source, "footer");
  assert.equal(entity.firstSeen, entity.lastSeen);
});

test("signing up again is not an error and does not rewrite firstSeen", async () => {
  const rowKey = keyFor("person@example.com");
  const table = fakeTable({ existing: new Set([rowKey]) });
  const answer = await call({ table, body: { email: "person@example.com", source: "modal" } });

  assert.equal(answer.status, 200);
  assert.deepEqual(answer.body, { ok: true, alreadyJoined: true });
  assert.equal(table.writes.length, 1);
  assert.equal(table.writes[0].entity.firstSeen, undefined);
  assert.ok(table.writes[0].entity.lastSeen);
});

test("an absent or unusable source is recorded as unknown and a long one is cut", async () => {
  const table = fakeTable();
  await call({ table, body: { email: "person@example.com" } });
  assert.equal(table.writes[0].entity.source, "unknown");

  const second = fakeTable();
  await call({ table: second, body: { email: "other@example.com", source: { evil: true } } });
  assert.equal(second.writes[0].entity.source, "unknown");

  const third = fakeTable();
  await call({ table: third, body: { email: "third@example.com", source: "x".repeat(200) } });
  assert.equal(third.writes[0].entity.source.length, MAX_SOURCE_LENGTH);
});

test("junk is refused with 400 and never reaches storage", async () => {
  for (const body of [undefined, null, {}, { email: 12 }, { email: "nope" }, "a string"]) {
    const table = fakeTable();
    const answer = await call({ table, body });
    assert.equal(answer.status, 400, `expected 400 for ${JSON.stringify(body)}`);
    assert.equal(answer.body.ok, false);
    assert.equal(table.writes.length, 0);
    assert.equal(table.gets.length, 0);
  }
});

test("the sixth signup from one address in a minute is refused", async () => {
  const limiter = new RateLimiter();
  const table = fakeTable();
  const body = { email: "person@example.com", source: "signup" };

  for (let i = 0; i < RATE_LIMIT_MAX; i += 1) {
    // A different IP each time, so this proves the per address limit rather
    // than the per IP one.
    const answer = await call({ table, limiter, body, ip: `198.51.100.${i}`, now: 1_000 });
    assert.equal(answer.status, 200);
  }

  const answer = await call({ table, limiter, body, ip: "198.51.100.99", now: 1_000 });
  assert.equal(answer.status, 429);
  assert.equal(table.writes.length, RATE_LIMIT_MAX, "the refused attempt writes nothing");
});

test("the sixth request from one IP in a minute is refused before the body is read", async () => {
  const limiter = new RateLimiter();
  const table = fakeTable();

  for (let i = 0; i < RATE_LIMIT_MAX; i += 1) {
    const answer = await call({ table, limiter, body: { email: `p${i}@example.com` }, now: 1_000 });
    assert.equal(answer.status, 200);
  }

  // Junk, so a 429 rather than a 400 is the proof that the IP limit runs
  // first and a loop cannot make the endpoint work for free.
  const answer = await call({ table, limiter, body: { email: "nope" }, now: 1_000 });
  assert.equal(answer.status, 429);
});

test("a lookup that fails for a reason other than a missing row does not write", async () => {
  const table = fakeTable({ getFails: Object.assign(new Error("throttled"), { statusCode: 503 }) });
  const logged = [];
  const answer = await call({
    table,
    body: { email: "person@example.com" },
    log: (message) => logged.push(message),
  });

  assert.equal(answer.status, 503);
  assert.equal(answer.body.ok, false);
  assert.equal(table.writes.length, 0);
  assert.deepEqual(logged, ["waitlist lookup failed"]);
});

test("a failed write is reported as unavailable rather than as success", async () => {
  const table = fakeTable({ upsertFails: new Error("no route to host") });
  const logged = [];
  const answer = await call({
    table,
    body: { email: "person@example.com" },
    log: (message) => logged.push(message),
  });

  assert.equal(answer.status, 503);
  assert.equal(answer.body.ok, false);
  assert.deepEqual(logged, ["waitlist write failed"]);
});

test("no answer this endpoint gives ever carries an address back", async () => {
  // The one thing that must never leak: the endpoint has no read path, so a
  // body that repeats what was sent is the only way an address could come out
  // of it. Checked over every status it can return.
  const email = "person@example.com";
  const answers = [
    await call({ body: { email } }),
    await call({ body: { email } }),
    await call({ body: { email: "nope" } }),
    await call({ table: fakeTable({ upsertFails: new Error("down") }), body: { email } }),
  ];
  for (const answer of answers) {
    const serialised = JSON.stringify(answer.body);
    assert.ok(!serialised.includes(email), `an address escaped in ${serialised}`);
    assert.ok(!serialised.includes(keyFor(email)), `a row key escaped in ${serialised}`);
  }
});
