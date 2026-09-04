"use strict";

/**
 * What /api answers, and what it answers for a path that is not there.
 *
 * The case worth having is the last one. The catch-all route this function is
 * bound to must never be able to take a request an endpoint added later should
 * have had, because that would turn a submission into a silent 404 and the
 * caller would go on believing something went wrong at their end. The router
 * decides precedence, which a unit test cannot reach, so the guarantee is made
 * a second way that a test can: the binding accepts GET and HEAD only, so
 * nothing that changes anything can ever land here.
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

test("GET /api discovers every public endpoint and machine-readable resource", async () => {
  const answer = await invoke({ path: "" });
  assert.equal(answer.status, 200);
  assert.equal(answer.body.service, "antifailure.dev");
  // Empty, and asserted rather than skipped. This host accepts nothing now that
  // signing up is a GitHub exchange against the control plane, and an entry
  // appearing here without a function behind it is the shape of the failure
  // this file exists for: a documented endpoint that answers 404.
  assert.deepEqual(answer.body.endpoints, []);
  // The two documents an agent comes here for are static files the site
  // publishes, not endpoints this app serves, and they are listed as what they
  // are. An earlier version proxied the OpenAPI document through this app; see
  // web/apps/api/scripts/openapi.ts for why that was wrong and what replaced it.
  assert.deepEqual(
    answer.body.resources.map((r) => r.path),
    ["/openapi.json", "/errors.v1.json", "/lint-findings.v1.json", "/llms.txt"],
  );
});

test("GET /api says where the product's API actually is", async () => {
  const answer = await invoke({ path: "" });
  assert.equal(answer.body.productApi.openapi, "https://antifailure.dev/openapi.json");
  assert.equal(answer.body.productApi.origin, "https://app.antifailure.dev");
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
  for (const requested of ["nope", "v1/users", "signup", "../../etc/passwd"]) {
    const answer = await invoke({ path: requested });
    assert.equal(answer.status, 404);
    assert.equal(answer.body.ok, false);
    assert.equal(answer.body.code, "endpoint_not_found");
    assert.match(answer.body.message, /No such endpoint/);
    assert.match(answer.body.resolution, /GET \/api/);
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

test("the catch-all can never take a request that changes something", () => {
  // The one function left is a reader. Its binding is what keeps that true: a
  // catch-all that accepted POST would answer, with a 404 and a cheerful body,
  // for any endpoint somebody adds under /api and forgets to route, and the
  // caller would see a submission that silently did nothing.
  const catchAll = binding("index");
  assert.equal(catchAll.route, "{*path}");
  assert.deepEqual(catchAll.methods, ["get", "head"]);
  assert.equal(
    catchAll.methods.includes("post"),
    false,
    "a catch-all that accepts POST could swallow a submission if route precedence ever changed",
  );
});

test("the function is anonymous, because the platform is what fronts it", () => {
  assert.equal(binding("index").authLevel, "anonymous");
});
