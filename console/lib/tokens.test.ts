// What the credentials list folds away, and the one thing it must never fold.
//
// The page these back was seven hundred rows of dead machine credentials with
// the reader's own terminal near the bottom. Grouping is the fix, and grouping
// is also the thing that could quietly hide a live credential from the one
// screen in the product that would have shown it to somebody. So the first test
// here is that assertion and the rest are about the reading being honest.

import test from "node:test";
import assert from "node:assert/strict";
import { groupHistory, kindLabel, stateOf, type TokenRow } from "./tokens.ts";

const NOW = Date.parse("2026-09-05T12:00:00.000Z");

function row(over: Partial<TokenRow> & { id: string }): TokenRow {
  return {
    name: "a credential",
    prefix: "aft_000000",
    kind: "oidc",
    created_at: "2026-09-05T10:00:00.000Z",
    last_used_at: null,
    revoked_at: null,
    expires_at: "2026-09-05T10:15:00.000Z",
    ...over,
  };
}

/** One continuous integration run, which is eight credentials with one name. */
function run(id: number, over: Partial<TokenRow> = {}): TokenRow[] {
  return Array.from({ length: 8 }, (_, i) =>
    row({ id: `r${id}-${i}`, name: `antifailure/antifailure run ${id}`, ...over }),
  );
}

test("a live credential is never in the history, whatever kind it is", () => {
  // THE ONE THAT MATTERS. Everything this function returns is rendered behind a
  // closed disclosure, so anything it returns is a credential the reader has to
  // ask to see. A live one must never be in here.
  const rows = [
    row({ id: "live-oidc", expires_at: "2026-09-05T12:00:01.000Z" }),
    row({ id: "live-cli", kind: "cli", expires_at: "2026-12-01T00:00:00.000Z" }),
    row({ id: "live-engine", kind: "engine", expires_at: null }),
    row({ id: "live-mcp", kind: "mcp", expires_at: "2026-10-01T00:00:00.000Z" }),
  ];
  assert.deepEqual(groupHistory(rows, NOW), []);
});

test("an expiry exactly now is expired, the way every route reads it", () => {
  // The boundary, and it is the direction that matters: calling this instant
  // live would show a credential as usable at the moment it stopped being.
  const at = new Date(NOW).toISOString();
  assert.equal(stateOf(row({ id: "a", expires_at: at }), NOW), "expired");
  assert.equal(stateOf(row({ id: "b", expires_at: at }), NOW - 1), "live");
});

test("a revoked credential reads as revoked even when it has also expired", () => {
  // Revocation is somebody's decision and expiry is a timer. The reader opening
  // this page during an incident is looking for the decision.
  const t = row({ id: "a", revoked_at: "2026-09-05T11:00:00.000Z" });
  assert.equal(stateOf(t, NOW), "revoked");
  assert.equal(groupHistory([t], NOW)[0]!.state, "revoked");
});

test("one run collapses to one line carrying its count", () => {
  const lines = groupHistory(run(101), NOW);
  assert.equal(lines.length, 1);
  assert.equal(lines[0]!.count, 8);
  assert.equal(lines[0]!.name, "antifailure/antifailure run 101");
});

test("two runs do not collapse into each other", () => {
  const lines = groupHistory([...run(101), ...run(102)], NOW);
  assert.equal(lines.length, 2);
  assert.deepEqual(
    lines.map((l) => l.count),
    [8, 8],
  );
});

test("a run that was revoked is a separate line from one that expired", () => {
  // Same name, two states. A single line could only carry one badge, and it
  // would be the wrong one for half the credentials behind it.
  const lines = groupHistory(
    [...run(101), ...run(101, { revoked_at: "2026-09-05T11:00:00.000Z" })],
    NOW,
  );
  assert.equal(lines.length, 2);
  assert.deepEqual(
    lines.map((l) => l.state).sort(),
    ["expired", "revoked"],
  );
});

test("a grouped line shows no prefix, because it stands for more than one", () => {
  // Showing the first prefix of eight would name a credential the reader would
  // then go and look for, and it is not the one the line is about.
  assert.equal(groupHistory(run(101), NOW)[0]!.prefix, null);
  assert.equal(groupHistory(run(101).slice(0, 1), NOW)[0]!.prefix, "aft_000000");
});

test("nothing but a workflow identity is ever grouped, even two with one name", () => {
  // Two engine tokens can share a name, because that is what a rotation is.
  // Collapsing them would hide one credential behind another that is not it.
  const rows = [
    row({ id: "a", kind: "engine", name: "ci", expires_at: null, revoked_at: "2026-09-05T11:00:00.000Z" }),
    row({ id: "b", kind: "engine", name: "ci", expires_at: null, revoked_at: "2026-09-05T11:30:00.000Z" }),
  ];
  const lines = groupHistory(rows, NOW);
  assert.equal(lines.length, 2);
  assert.deepEqual(
    lines.map((l) => l.count),
    [1, 1],
  );
});

test("the group counts how many of its credentials were ever used", () => {
  // The number that shows the leak on the page itself: eight are minted per run
  // and about three of them ever send anything.
  const rows = run(101).map((t, i) =>
    i < 3 ? { ...t, last_used_at: `2026-09-05T10:0${i + 1}:00.000Z` } : t,
  );
  const line = groupHistory(rows, NOW)[0]!;
  assert.equal(line.used, 3);
  assert.equal(line.count, 8);
  assert.equal(line.lastUsed, "2026-09-05T10:03:00.000Z", "the newest use is the one shown");
});

test("a group nobody used says so rather than borrowing a time", () => {
  assert.equal(groupHistory(run(101), NOW)[0]!.lastUsed, null);
  assert.equal(groupHistory(run(101), NOW)[0]!.used, 0);
});

test("the newest line is first, and a group is dated by its newest credential", () => {
  const older = run(100, { created_at: "2026-09-04T10:00:00.000Z" });
  const newer = run(101, { created_at: "2026-09-05T09:00:00.000Z" });
  // One straggler in the older run, newer than anything in the newer run. The
  // group has to move with it or a run that is still minting sinks down the
  // page as its earlier credentials age.
  // LAST in the array, not first. It was first, and the test passed with the
  // running maximum deleted: the group takes its date from whichever credential
  // it sees first, so a straggler at the head of the list set it by accident and
  // the line that keeps it current was never exercised.
  older[older.length - 1] = { ...older.at(-1)!, created_at: "2026-09-05T11:59:00.000Z" };
  const lines = groupHistory([...newer, ...older], NOW);
  assert.deepEqual(
    lines.map((l) => l.name),
    ["antifailure/antifailure run 100", "antifailure/antifailure run 101"],
  );
});

test("every kind has a name a person can read", () => {
  assert.equal(kindLabel("cli"), "terminal");
  assert.equal(kindLabel("mcp"), "MCP client");
  assert.equal(kindLabel("oidc"), "workflow identity");
  assert.equal(kindLabel("engine"), "engine token");
  // Anything the control plane adds later reads as an engine token rather than
  // as the raw value, which is the safe direction for a label on this page.
  assert.equal(kindLabel("something-new"), "engine token");
});
