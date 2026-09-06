import { readFileSync } from "node:fs";
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  HOSTED,
  INSTALL,
  OPTIONS,
  PATHS,
  destinationFor,
  guideFor,
  loginCommand,
  mcpEndpoint,
  otherPaths,
  readChoice,
  storageKey,
} from "./start.ts";

// Every command and permission below is checked against the place it came
// from, named beside it, so that a change there is a red test here rather than
// a page that quietly starts lying.

test("a stored path reads back as itself and the dismissal as skipped", () => {
  assert.deepEqual(
    [readChoice("ci"), readChoice("terminal"), readChoice("hosted"), readChoice("skipped")],
    ["ci", "terminal", "hosted", "skipped"],
  );
});

test("a word this console does not know reads as no answer, never as a path", () => {
  // The value a removed path would become, an older console's word, and the
  // two shapes of nothing.
  assert.deepEqual(
    [readChoice("kubernetes"), readChoice("CI"), readChoice(null), readChoice(undefined)],
    [null, null, null, null],
  );
});

test("no answer goes to the question and any answer goes where the root always went", () => {
  assert.equal(destinationFor(null), "/start");
  assert.deepEqual(
    [destinationFor("ci"), destinationFor("terminal"), destinationFor("hosted"), destinationFor("skipped")],
    ["/environments", "/environments", "/environments", "/environments"],
  );
});

test("the key names the organization, so one person in two of them is asked twice", () => {
  assert.notEqual(storageKey("org-a"), storageKey("org-b"));
  assert.ok(storageKey("org-a").includes("org-a"));
});

test("there are exactly three answers and a button for each", () => {
  assert.deepEqual([...PATHS], ["ci", "terminal", "hosted"]);
  assert.deepEqual(
    OPTIONS.map((option) => option.path),
    ["ci", "terminal", "hosted"],
  );
});

test("the other two paths are the ones not chosen, in their usual order", () => {
  assert.deepEqual(otherPaths("ci"), ["terminal", "hosted"]);
  assert.deepEqual(otherPaths("terminal"), ["ci", "hosted"]);
  assert.deepEqual(otherPaths("hosted"), ["ci", "terminal"]);
});

test("the sign-in command is plain on the hosted instance and addressed everywhere else", () => {
  // engine/internal/cli/login.go: a plain af login signs in to
  // controlplane.DefaultBaseURL, so any other origin has to be named.
  assert.equal(loginCommand(HOSTED), "af login");
  assert.equal(
    loginCommand("https://cp.example.com"),
    "af login --control-plane https://cp.example.com",
  );
});

test("the hosted endpoint is the origin and /mcp, with no doubled slash", () => {
  // web/apps/api/src/auth/mcp.ts hostedMcpEndpoint: trailing slashes are
  // stripped before /mcp is appended.
  assert.equal(mcpEndpoint("https://cp.example.com"), "https://cp.example.com/mcp");
  assert.equal(mcpEndpoint("https://cp.example.com/"), "https://cp.example.com/mcp");
});

test("the CI path is the App install when the plane has one", () => {
  const guide = guideFor("ci", HOSTED, "https://github.com/apps/antifailure/installations/new");
  assert.equal(guide.step?.href, "https://github.com/apps/antifailure/installations/new");
  assert.equal(guide.copy.length, 0);
  // web/apps/api/src/github/setup.ts: SETUP_TITLE and WORKFLOW_PATH.
  assert.ok(guide.step?.note?.includes("Check every pull request with Antifailure"));
  assert.ok(guide.step?.note?.includes(".github/workflows/antifailure.yml"));
});

test("the CI path falls back to af init when the plane has no install address", () => {
  // docs/src/content/docs/getting-started/pull-requests.md, "Run af init":
  // with a github.com remote it writes the same workflow file.
  const guide = guideFor("ci", HOSTED, undefined);
  assert.equal(guide.step, null);
  assert.deepEqual(
    guide.copy.map((item) => item.value),
    ["af init"],
  );
});

test("the CI permissions name what the App and the workflow actually declare", () => {
  // github/setup.ts: contents: write on the installation.
  // examples/github-workflow.yml: contents: read, pull-requests: write,
  // id-token: write.
  const { permissions } = guideFor("ci", HOSTED, null);
  for (const needed of ["Contents: write", "contents: read", "pull-requests: write", "id-token: write"]) {
    assert.ok(permissions.includes(needed), `permissions line names ${needed}`);
  }
});

test("the terminal path is install, sign in, up, on the plane this console is served from", () => {
  const guide = guideFor("terminal", "https://cp.example.com", null);
  assert.deepEqual(
    guide.copy.map((item) => item.value),
    [INSTALL, "af login --control-plane https://cp.example.com", "af up"],
  );
  assert.equal(guide.next.href, "/cli#first-run");
});

test("the terminal permissions say what the default token can do and how long it lasts", () => {
  // login.go Long: read environments and runs, write events, nothing else
  // unless --scope; the credential is good for ninety days.
  const { permissions } = guideFor("terminal", HOSTED, null);
  assert.ok(permissions.includes("--scope"));
  assert.ok(permissions.includes("ninety days"));
});

test("the hosted path is the endpoint address and the two scopes", () => {
  // docs/reference/mcp.md, "Connecting to the control plane", and
  // lib/mcp-consent.ts mcpScopeLabels.
  const guide = guideFor("hosted", "https://cp.example.com", null);
  assert.deepEqual(
    guide.copy.map((item) => item.value),
    ["https://cp.example.com/mcp"],
  );
  assert.ok(guide.permissions.includes("mcp:read"));
  assert.ok(guide.permissions.includes("mcp:write"));
  assert.ok(guide.permissions.includes("ninety days"));
});

test("every guide ends somewhere inside the console and points at real documentation", () => {
  for (const path of PATHS) {
    const guide = guideFor(path, HOSTED, null);
    assert.ok(guide.next.href.startsWith("/"), `${path} continues inside the console`);
    assert.ok(guide.docs.href.startsWith("https://antifailure.dev/docs/"), `${path} cites the docs`);
  }
});

test("the root decides from the remembered answer, not from a fixed page", () => {
  // The browser check proves the redirect. This keeps the root routed through
  // destinationFor when the page is edited later, so a hard coded
  // /environments cannot quietly come back.
  const source = readFileSync(new URL("../app/(app)/page.tsx", import.meta.url), "utf8");
  assert.deepEqual(
    [source.includes("destinationFor(choice)"), source.includes("useStartChoice(")],
    [true, true],
  );
});
