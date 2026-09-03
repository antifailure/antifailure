// The Operations lane's pure functions.
//
// Small, and each one guards a defect that renders rather than throws, which is
// the kind this console has shipped before.
//
// A LOOKUP MAP THAT LOSES A KEY RENDERS `undefined`. Not a crash, not a type
// error at the call site, just the word undefined in a table cell, and only if
// somebody happens to be looking at a row in that state. The two maps below are
// keyed by union types, so TypeScript catches a key that is removed from the
// map; it does NOT catch a member added to the union in another file, which is
// exactly what happens when the server grows a fourth teardown standing. So the
// keys are asserted against the list, here, where a mismatch fails the build.
//
// A TONE THAT IS NEVER RED HAS NOT BEEN SHOWN TO SAY NO. `toneForStanding` is
// the reason the standing exists at all: `nothing-to-reach` and `abandoned`
// both read as "pending" in the raw state column, and both mean the environment
// is still up and nothing further will happen on its own. If either of them
// came back warn, the page would file the two situations that need a human
// alongside the two that resolve themselves.

import test from "node:test";
import assert from "node:assert/strict";
import {
  FINDING_LABEL,
  STANDING_LABEL,
  WINDOWS,
  toneForStanding,
  toneForVerdict,
  type FindingKind,
  type TeardownStanding,
  type Verdict,
} from "./operations-shapes.ts";

const STANDINGS: TeardownStanding[] = [
  "nothing-to-reach",
  "waiting-to-dispatch",
  "dispatched-unconfirmed",
  "confirmed",
  "abandoned",
];

const KINDS: FindingKind[] = ["sandbox-without-credential", "never-approved", "allow"];

test("every teardown standing has a label a person can read", () => {
  for (const standing of STANDINGS) {
    const label = STANDING_LABEL[standing];
    assert.ok(label, `no label for ${standing}, so the cell would render undefined`);
    assert.ok(
      !label.includes("-"),
      `${standing} was labelled ${label}, which is still the wire identifier`,
    );
  }
  assert.equal(Object.keys(STANDING_LABEL).length, STANDINGS.length);
});

test("every firewall finding kind has a label", () => {
  for (const kind of KINDS) {
    assert.ok(FINDING_LABEL[kind], `no label for ${kind}`);
  }
  assert.equal(Object.keys(FINDING_LABEL).length, KINDS.length);
});

test("a teardown that can never reach anything is a failure, not a wait", () => {
  // The whole reason the standing is derived rather than read off the state
  // column. Both of these look like "pending" there.
  assert.equal(toneForStanding("nothing-to-reach"), "fail");
  assert.equal(toneForStanding("abandoned"), "fail");

  // These two genuinely are waits, and colouring them red would make a healthy
  // queue look like an outage.
  assert.equal(toneForStanding("waiting-to-dispatch"), "warn");
  assert.equal(toneForStanding("dispatched-unconfirmed"), "warn");

  assert.equal(toneForStanding("confirmed"), "pass");
});

test("a health verdict maps to three distinct tones", () => {
  const verdicts: Verdict[] = ["ok", "degraded", "failing"];
  const tones = verdicts.map(toneForVerdict);
  assert.deepEqual(tones, ["pass", "warn", "fail"]);
  // Degraded is not red. A page where everything worth looking at is red is a
  // page where nothing is.
  assert.notEqual(toneForVerdict("degraded"), toneForVerdict("failing"));
});

test("every window offered is one the routes accept", () => {
  // The server takes a fixed set, not a free number, because these queries scan
  // by time. A window in this list that the route refuses is a select that
  // produces a validation error instead of a page.
  const accepted = new Set(["1", "6", "24", "72", "168"]);
  for (const w of WINDOWS) {
    assert.ok(accepted.has(w.value), `${w.value} hours is not a window the route accepts`);
    assert.ok(w.label.length > 0);
  }
});
