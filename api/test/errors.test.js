"use strict";

// Every code this function app can return, against the one it publishes.
//
// The codes used to be written out at each throw site. Nothing listed them,
// nothing checked them, and `GET /api` did not mention that they existed, so a
// caller receiving one had no way to find out what it meant from the host that
// sent it. The engine and control plane have had that property enforced for a
// long time by tools/errcheck; this side had nothing.
//
// The scan below is the part that matters. Asserting that the codes in the
// catalog are in the catalog proves nothing. Reading the source for code
// literals and failing on one that is not in the catalog is what stops the next
// emitter from going back to writing its own, and it is what makes the claim
// "every code this app returns is published" true rather than aspirational.
//
// The second test is the one that just did some work. Removing the waitlist
// left three catalog entries nothing could return, and it named all three
// rather than letting them stay published at GET /api as descriptions of
// situations this app cannot be in.

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

const { CATALOG, failure, published } = require("../shared/errors");
const { INDEX } = require("../index/index.js");

const ROOT = path.join(__dirname, "..");

/** Every .js file this function app ships, tests and dependencies excluded. */
function sources(dir) {
  const found = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    if (entry.name === "node_modules" || entry.name === "test") continue;
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) found.push(...sources(full));
    else if (entry.name.endsWith(".js")) found.push(full);
  }
  return found;
}

test("every error code in the source has a catalog entry", () => {
  // Matched on the object member rather than on any string, because a bare
  // string search finds the catalog's own keys, the comments about them, and
  // the word in prose, and would report every code as fine. The shape being
  // looked for is the one that puts a code on the wire: `code: "..."`, or
  // `failure("...")`.
  const emitted = new Map();
  for (const file of sources(ROOT)) {
    const body = fs.readFileSync(file, "utf8");
    const relative = path.relative(ROOT, file);
    if (relative === path.join("shared", "errors.js")) continue;
    for (const match of body.matchAll(/\bcode:\s*"([a-z_]+)"/g)) {
      emitted.set(match[1], relative);
    }
    for (const match of body.matchAll(/\bfailure\(\s*"([a-z_]+)"/g)) {
      emitted.set(match[1], relative);
    }
  }

  // Standard 24. An empty scan and a clean scan look identical from here, and
  // this app does emit codes, so zero means the pattern stopped matching.
  assert.ok(emitted.size > 0, "the scan found no error code at all in api/, which cannot be right");

  const undefined_ = [...emitted].filter(([code]) => !(code in CATALOG));
  assert.deepEqual(
    undefined_,
    [],
    `these codes are returned to a caller and are not in shared/errors.js: ${undefined_
      .map(([code, where]) => `${code} (${where})`)
      .join(", ")}`,
  );
});

test("every catalog entry is one the source can actually return", () => {
  // The other direction. An entry nothing emits is published at GET /api, and
  // somebody searching for it after a failure finds a description of a
  // situation this app cannot be in.
  const emitted = new Set();
  for (const file of sources(ROOT)) {
    if (path.relative(ROOT, file) === path.join("shared", "errors.js")) continue;
    const body = fs.readFileSync(file, "utf8");
    for (const match of body.matchAll(/\bfailure\(\s*"([a-z_]+)"/g)) emitted.add(match[1]);
  }
  const dead = Object.keys(CATALOG).filter((code) => !emitted.has(code));
  assert.deepEqual(dead, [], `no code path returns: ${dead.join(", ")}`);
});

test("GET /api publishes the catalog, so a caller can resolve a code it received", () => {
  assert.deepEqual(INDEX.errors, published());
  for (const entry of INDEX.errors) {
    assert.ok(entry.code && entry.message && entry.resolution, JSON.stringify(entry));
    assert.ok(entry.status >= 400 && entry.status <= 599, `${entry.code} publishes status ${entry.status}`);
  }
});

test("an unknown code throws rather than answering with an empty message", () => {
  // A wrong code is a programming mistake, and a visitor reading a refusal with
  // no message because of it is worse off than with the platform's own 500.
  assert.throws(() => failure("no_such_code"), /no catalog entry for the error code no_such_code/);
});

test("a body carries the code, the message and the resolution the catalog holds", () => {
  const answer = failure("endpoint_not_found");
  assert.equal(answer.status, 404);
  assert.deepEqual(answer.body, {
    ok: false,
    code: "endpoint_not_found",
    message: CATALOG.endpoint_not_found.message,
    resolution: CATALOG.endpoint_not_found.resolution,
  });
});
