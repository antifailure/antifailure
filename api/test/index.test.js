"use strict";

/**
 * What /api answers, and what it answers for a path that is not there.
 *
 * The case worth having is the last one. The catch-all route this function is
 * bound to must never be able to take a request the waitlist should have had,
 * because that would turn a signup into a 404 silently and the form would go
 * on saying something went wrong. The router decides precedence, which a unit
 * test cannot reach, so the guarantee is made a second way that a test can:
 * the binding accepts GET and HEAD only, and the waitlist is POST only.
 */

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

const handler = require("../index");
const { INDEX } = handler;

function invoke(params) {
  const context = { res: null, log: { error: () => {} } };
  return handler(context, { params }).then(() => ({
    status: context.res.status,
    body: JSON.parse(context.res.body),
    headers: context.res.headers,
  }));
}

function binding(name) {
  const file = path.join(__dirname, "..", name, "function.json");
  return JSON.parse(fs.readFileSync(file, "utf8")).bindings[0];
}

test("GET /api answers with the one endpoint this host serves", async () => {
  const answer = await invoke({ path: "" });
  assert.equal(answer.status, 200);
  assert.equal(answer.body.service, "antifailure.dev");
  assert.deepEqual(
    answer.body.endpoints.map((e) => `${e.method} ${e.path}`),
    ["POST /api/waitlist"],
  );
});

test("GET /api says where the product's API actually is", async () => {
  const answer = await invoke({ path: "" });
  assert.equal(answer.body.productApi.openapi, "https://app.antifailure.dev/openapi.json");
  assert.equal(
    answer.body.productApi.documentation,
    "https://antifailure.dev/docs/reference/api",
  );
});

test("the documented address points at a page that exists", () => {
  // A pointer into our own documentation is the kind of link that rots
  // quietly, because nothing renders this JSON and nobody reads it until they
  // are already lost. lychee checks the built site's links and cannot see a
  // string in a function, so it is checked here instead.
  const url = new URL(INDEX.productApi.documentation);
  const page = path.join(
    __dirname,
    "..",
    "..",
    "docs",
    "src",
    "content",
    url.pathname.replace(/^\/docs\//, "docs/") + ".md",
  );
  assert.ok(fs.existsSync(page), `no page at ${page} for ${INDEX.productApi.documentation}`);
});

test("an unknown path is a 404 that says so, not a 500 and not an empty body", async () => {
  for (const requested of ["nope", "v1/users", "waitlist/all", "../../etc/passwd"]) {
    const answer = await invoke({ path: requested });
    assert.equal(answer.status, 404);
    assert.equal(answer.body.ok, false);
    assert.match(answer.body.message, /No such endpoint/);
    assert.ok(answer.body.productApi.documentation);
  }
});

test("a 404 does not reflect the path it was given", async () => {
  const requested = "<script>alert(1)</script>";
  const answer = await invoke({ path: requested });
  const serialised = JSON.stringify(answer.body);
  assert.ok(!serialised.includes(requested), serialised);
  assert.ok(!serialised.includes("alert(1)"), serialised);
});

test("a missing or malformed route parameter is treated as the bare path", async () => {
  for (const params of [undefined, {}, { path: 7 }, { path: "/" }, { path: "//" }]) {
    const answer = await invoke(params);
    assert.equal(answer.status, 200, `expected the index for ${JSON.stringify(params)}`);
  }
});

test("the catch-all cannot take a signup away from the waitlist", () => {
  const catchAll = binding("index");
  const waitlist = binding("waitlist");

  assert.equal(catchAll.route, "{*path}");
  assert.deepEqual(catchAll.methods, ["get", "head"]);
  assert.deepEqual(waitlist.methods, ["post"]);
  assert.equal(
    catchAll.methods.includes("post"),
    false,
    "a catch-all that accepts POST could swallow a signup if route precedence ever changed",
  );
});

test("both functions are anonymous, because the platform is what fronts them", () => {
  for (const name of ["index", "waitlist"]) {
    assert.equal(binding(name).authLevel, "anonymous");
  }
});
