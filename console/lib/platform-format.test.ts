// The four decisions on the Developer Platform lane that the obvious version
// gets wrong.
//
// Each one is a case where the shorter implementation is not merely uglier, it
// is a different and false claim on a screen somebody makes a security decision
// from. They are written down here because a helper nobody exercises is a
// helper that gets simplified back to the wrong answer by the next reader.

import { test, describe } from "node:test";
import assert from "node:assert/strict";
import {
  approvalWording,
  deliveryStanding,
  shortSha,
  standingTone,
} from "./platform-format.ts";

describe("what colour a credential's standing wears", () => {
  test("a revoked credential is not a failure", () => {
    // The reflex is red, and red is wrong. Somebody revoked it deliberately,
    // and the person reading the list is usually that somebody checking that it
    // worked. A column of red beside every credential that was correctly killed
    // trains the reader to ignore the colour.
    assert.equal(standingTone("revoked"), "neutral");
  });

  test("an expired credential is the one that deserves attention", () => {
    // Nothing decided this. It stopped working on a clock, and whoever depended
    // on it has a pipeline that fails for a reason nobody was told about.
    assert.equal(standingTone("expired"), "warn");
  });

  test("a live credential reads as live", () => {
    assert.equal(standingTone("live"), "pass");
  });
});

describe("what to say about a fork approval", () => {
  const fork = { fromFork: true, approvedSha: null, approvalCoversHead: false };

  test("an approval that no longer covers the head is its own answer", () => {
    // THE CASE THIS FUNCTION EXISTS FOR. A boolean would call this "approved",
    // and it is the exact state 0021 stores approved_sha rather than a boolean
    // to make visible: a maintainer approved one commit and somebody pushed
    // another after it. Reading that as approved is reading unreviewed code as
    // reviewed.
    const stale = approvalWording({ ...fork, approvedSha: "abc123", approvalCoversHead: false });
    assert.equal(stale.label, "stale approval");
    assert.equal(stale.tone, "warn");
    assert.ok(stale.hint, "the stale case says nothing about why it is stale");
  });

  test("an approval that covers the head is an approval", () => {
    const good = approvalWording({ ...fork, approvedSha: "abc123", approvalCoversHead: true });
    assert.equal(good.label, "approved");
    assert.equal(good.tone, "pass");
  });

  test("a fork nobody approved is not the same as a branch that needs no approval", () => {
    // Two absences that a boolean collapses into one. The first is a pull
    // request waiting on a person; the second is an ordinary branch where the
    // gate does not apply, and showing "not approved" beside it would send
    // somebody looking for an approver who was never needed.
    assert.equal(approvalWording(fork).label, "not approved");
    assert.equal(approvalWording(fork).tone, "warn");
    const internal = approvalWording({ ...fork, fromFork: false });
    assert.equal(internal.label, "not required");
    assert.equal(internal.tone, "neutral");
  });
});

describe("a commit, shortened", () => {
  test("seven characters, the way git shortens one", () => {
    assert.equal(shortSha("0123456789abcdef0123456789abcdef01234567"), "0123456");
  });

  test("a head that was never reported stays null rather than becoming an empty string", () => {
    // An empty string renders as a cell with nothing in it, which reads as a
    // value that failed to load. Null reaches the component that says the
    // record does not have this field.
    assert.equal(shortSha(null), null);
  });
});

describe("whether a delivery was handled", () => {
  test("Stripe's unresolved is not the same as never looked at", () => {
    // The two ledgers say it differently and this is the one place that
    // reconciles them. An unresolved billing event HAS a processed_at in some
    // paths: the webhook looked at it and could not decide what it meant, which
    // is a different thing to report than silence.
    const unresolved = deliveryStanding({ handledAt: new Date().toISOString(), outcome: "unresolved" });
    assert.equal(unresolved.label, "unresolved");
    assert.equal(unresolved.tone, "warn");
  });

  test("a GitHub delivery with no handled time was not handled", () => {
    const silent = deliveryStanding({ handledAt: null, outcome: null });
    assert.equal(silent.label, "not handled");
    assert.equal(silent.tone, "warn");
  });

  test("a stale event was handled and still says so", () => {
    // It was decided, and the decision was to ignore it because a newer event
    // had already been applied. Reporting it as a failure would send somebody
    // investigating ordinary webhook reordering.
    const stale = deliveryStanding({ handledAt: new Date().toISOString(), outcome: "stale" });
    assert.equal(stale.label, "stale");
    assert.equal(stale.tone, "warn");
  });

  test("an outcome the ledger recorded is shown in the ledger's own words", () => {
    const applied = deliveryStanding({ handledAt: new Date().toISOString(), outcome: "applied" });
    assert.equal(applied.label, "applied");
    assert.equal(applied.tone, "pass");
    // GitHub records no outcome word for a delivery it handled cleanly, so the
    // fallback has to be a word rather than a blank.
    const handled = deliveryStanding({ handledAt: new Date().toISOString(), outcome: null });
    assert.equal(handled.label, "handled");
    assert.equal(handled.tone, "pass");
  });
});
