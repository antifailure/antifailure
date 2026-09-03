// The Product lane's pure logic.
//
// Three functions, and each one is here because getting it wrong produces a
// screen that is confidently wrong rather than obviously broken, which is the
// failure this console keeps paying for.
//
//   expiryPhrase   says a direction. "3d left" and "3d over" are opposite
//                  claims about an environment somebody is paying for, and the
//                  same mistake in `ago` printed "-30d ago" on the line that
//                  told a customer when they would next be charged.
//   metricsFor     decides which numbers a load result HAS. The four workload
//                  kinds measure different things and the database has a CHECK
//                  refusing a row that pretends otherwise; a console can
//                  reintroduce exactly that confusion on the far side of a
//                  correct table by rendering a latency beside a browser run.
//   toneForStanding is the colour, and the case worth pinning is `unknown`: a
//                  run that reported nothing must not be coloured like one that
//                  passed.
//
// These live in productshapes.ts rather than beside the hooks precisely so this
// file can import them. `npm test` in console is `node --test lib/*.test.ts`,
// so a helper that sits next to a React import is a helper nobody checks.

import { test, describe } from "node:test";
import assert from "node:assert/strict";
import { expiryPhrase, metricsFor, toneForStanding } from "./productshapes.ts";

describe("how long a twin has left", () => {
  const now = new Date("2026-09-02T12:00:00.000Z");
  const at = (ms: number) => new Date(now.getTime() + ms).toISOString();

  test("a future expiry reads as time remaining", () => {
    assert.equal(expiryPhrase(at(45_000), now), "45s left");
    assert.equal(expiryPhrase(at(90 * 60_000), now), "2h left");
    assert.equal(expiryPhrase(at(5 * 86_400_000), now), "5d left");
  });

  test("a past expiry reads as time overrun, never as a negative", () => {
    // The whole point of the column. A minus sign in front of a duration is
    // read backwards by somebody who is halfway through an incident.
    assert.equal(expiryPhrase(at(-3 * 3_600_000), now), "3h over");
    assert.ok(!expiryPhrase(at(-3 * 3_600_000), now).includes("-"));
  });

  test("a long overrun reads in days rather than in thousands of minutes", () => {
    // The reason this does not reuse `duration` from loadshapes.ts, which is
    // built for a measured run time and stops at the minute. A twin five days
    // past its expiry reading "7200m over" is a number nobody converts.
    assert.equal(expiryPhrase(at(-5 * 86_400_000), now), "5d over");
  });

  test("no expiry is said in words rather than shown as forever", () => {
    assert.equal(expiryPhrase(null, now), "No expiry set");
  });

  test("an unparseable date does not render as NaN", () => {
    // A row whose timestamp did not survive the round trip must not put the
    // string NaN into a table cell.
    assert.equal(expiryPhrase("not a date", now), "No expiry set");
  });

  test("the boundary belongs to the future, not the past", () => {
    // Exactly at the expiry is not yet over. An off by one here flips a whole
    // column of environments into the overdue filter for a millisecond.
    assert.ok(expiryPhrase(at(0), now).endsWith("left"));
  });
});

describe("which numbers a load result has", () => {
  test("an observed load has requests and latency and no workflows", () => {
    const metrics = metricsFor({
      kind: "observed_load",
      requests: 1200,
      failures: 4,
      error_rate: 0.0033,
      p50_ms: 41,
      p95_ms: 190,
    });
    const labels = metrics.map((m) => m.label);
    assert.ok(labels.includes("Requests"));
    assert.ok(labels.includes("p95"));
    assert.ok(!labels.includes("Workflows"));
    assert.ok(!labels.includes("Findings"));
  });

  test("a browser workflow has five outcome counts and no latency", () => {
    // Five and not two. A run that returned nought passed, nought failed and
    // one unverified did nothing, and passed-plus-failed alone renders it as a
    // run with no failures.
    const metrics = metricsFor({
      kind: "browser_workflow",
      workflows: 1,
      workflows_passed: 0,
      workflows_failed: 0,
      workflows_flaky: 0,
      workflows_blocked: 0,
      workflows_unverified: 1,
    });
    const labels = metrics.map((m) => m.label);
    for (const outcome of ["Passed", "Failed", "Flaky", "Blocked", "Unverified"]) {
      assert.ok(labels.includes(outcome), `browser results lost ${outcome}`);
    }
    // The assertion that matters. A percentile beside a browser run is a chart
    // over a number that is not a latency.
    for (const latency of ["p50", "p95", "p99", "Slowest", "Requests"]) {
      assert.ok(!labels.includes(latency), `browser results grew ${latency}`);
    }
  });

  test("an exploration counts goals reached rather than answering yes or no", () => {
    const labels = metricsFor({
      kind: "exploration",
      findings: 2,
      goals: 50,
      goals_reached: 47,
    }).map((m) => m.label);
    assert.deepEqual(labels, ["Findings", "Goals", "Goals reached"]);
  });

  test("a zero is a measurement and a missing field is not", () => {
    const metrics = metricsFor({ kind: "observed_load", requests: 0 });
    const requests = metrics.find((m) => m.label === "Requests");
    const failures = metrics.find((m) => m.label === "Failures");
    // Zero requests is an answer: the run sent nothing. A null is the absence
    // of an answer, and Metric renders the two differently on purpose.
    assert.equal(requests?.value, 0);
    assert.equal(failures?.value, null);
  });

  test("no result at all is an empty row rather than a row of zeroes", () => {
    assert.deepEqual(metricsFor(null), []);
  });

  test("a kind this build does not know returns nothing rather than guessing", () => {
    // Guessing by field name is how a future workload kind gets rendered with
    // the wrong units for a release and a half.
    assert.deepEqual(metricsFor({ kind: "something_new", requests: 5 }), []);
  });
});

describe("the colour a standing gets", () => {
  test("a run that reported nothing is not coloured like one that passed", () => {
    assert.notEqual(toneForStanding("unknown"), toneForStanding("passed"));
    assert.equal(toneForStanding("unknown"), "neutral");
  });

  test("running is not a failure and cancelled is not either", () => {
    assert.equal(toneForStanding("running"), "warn");
    assert.equal(toneForStanding("cancelled"), "neutral");
    assert.equal(toneForStanding("failed"), "fail");
    assert.equal(toneForStanding("passed"), "pass");
  });
});
