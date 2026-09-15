/**
 * The tenant runs page, which is the page this product is demonstrated on.
 *
 * WHY THESE TESTS ARE HERE AND NOT BESIDE THE COMPONENT. There is no component
 * or page test anywhere in this console and no Playwright config in the
 * repository, so a test of the rendered page would have been the first of its
 * kind and the infrastructure, not the defect, would have been the work. The
 * cheaper move that fits this codebase is the one lib/ already demonstrates
 * eleven times over: the decision lives in a pure function, the function is
 * tested, and the page becomes a place that renders an answer rather than a
 * place that computes one.
 *
 * WHAT THAT HONESTLY DOES NOT COVER, said plainly rather than left for somebody
 * to discover: these tests prove the ANSWERS are right. They cannot see the
 * banner deleted from the JSX. The last test in this file is the pair for that,
 * and it is a source-level wiring guard rather than a render: it asserts the
 * page still CALLS each of these, which is the difference between a capability
 * defined and a capability wired. A render test would subsume it and nothing
 * here pretends otherwise.
 *
 * It does not search for `role="alert"`, which was the first version of that
 * guard and could not have said no: the Start card's error handler has carried
 * one since long before any of this, so the assertion passed with the banner
 * deleted. That is the exact shape of defect this repository keeps finding in
 * its own instruments, so it is written down here rather than quietly fixed.
 */

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { toneFor } from "./tone.ts";
import {
  noVerdictsReason,
  nothingWasVerified,
  nothingWasVerifiedNotice,
  recentRunSummary,
  reproductionText,
  runIsInFlight,
  runStateOf,
  tallyVerdicts,
} from "./runshapes.ts";

/* -------------------------------------------------------------------------
 * The defect: a run that proved nothing looked like a clean run.
 * ---------------------------------------------------------------------- */

test("a run whose verdicts are all unverified verified nothing", () => {
  // The case the lead named, and the one a real `af test` run produced: the
  // persona could not be created, so every workflow finished having evaluated
  // nothing. Five full rows in the table and no judgement in any of them.
  assert.equal(nothingWasVerified(["unverified"]), true);
  assert.equal(nothingWasVerified(["unverified", "unverified", "unverified"]), true);
  assert.notEqual(nothingWasVerifiedNotice(["unverified", "unverified", "unverified"]), null);
});

test("a run whose verdicts are all blocked verified nothing", () => {
  assert.equal(nothingWasVerified(["blocked", "blocked"]), true);
  assert.equal(nothingWasVerified(["blocked", "unverified"]), true);
});

test("one real judgement anywhere in the run silences the banner", () => {
  // The other direction, which matters as much: a banner on a run that DID
  // find something is a banner people learn to scroll past.
  assert.equal(nothingWasVerified(["pass"]), false);
  assert.equal(nothingWasVerified(["unverified", "unverified", "pass"]), false);
  assert.equal(nothingWasVerified(["unverified", "fail"]), false);
  // Flaky is a finding. The same check answered differently on repeat is
  // something we looked at and something we found, so it is proof that the run
  // did its job, and it is still not a pass.
  assert.equal(nothingWasVerified(["unverified", "flaky"]), false);
  assert.equal(nothingWasVerifiedNotice(["unverified", "flaky"]), null);
});

test("no verdicts at all is the empty state's business and not the banner's", () => {
  // A banner here would fire on every run in its first seconds, which is how a
  // warning becomes furniture.
  assert.equal(nothingWasVerified([]), false);
  assert.equal(nothingWasVerifiedNotice([]), null);
});

test("a verdict word this console cannot read is not counted as proof", () => {
  // A sixth word reaching the console must not be silently drawn as a pass.
  // "I could not look" is a distinct answer from "it was fine".
  assert.equal(nothingWasVerified(["surprising"]), true);
  assert.equal(nothingWasVerified([null]), true);
  const notice = nothingWasVerifiedNotice(["surprising"]);
  assert.notEqual(notice, null);
  assert.match(notice!, /does not recognise/);
});

test("the banner counts what happened rather than only that nothing did", () => {
  const notice = nothingWasVerifiedNotice(["blocked", "unverified", "unverified"]);
  assert.notEqual(notice, null);
  assert.match(notice!, /3 verdicts/);
  assert.match(notice!, /One never reached the application\./);
  assert.match(notice!, /2 ran and proved nothing\./);
  // The load view's sentence, on purpose, so two screens describing one outcome
  // do not teach a reader two vocabularies.
  assert.match(notice!, /not the same as nothing being wrong/);
});

test("the tally separates proof from absence of proof", () => {
  const t = tallyVerdicts(["pass", "fail", "flaky", "blocked", "unverified", "nonsense"]);
  assert.equal(t.total, 6);
  assert.equal(t.proved, 3);
  assert.equal(t.blocked, 1);
  assert.equal(t.unverified, 1);
  assert.equal(t.unknown, 1);
});

/* -------------------------------------------------------------------------
 * The colour. The shared one, because the shared one was wrong.
 * ---------------------------------------------------------------------- */

test("unverified and flaky are not drawn as unremarkable", () => {
  // Both are real values of `verdict_value` and both fell through toneFor's
  // three lists to "neutral", which is the tone this console uses for a thing
  // it has no opinion about. Grey on "unverified" is an opinion and it is the
  // wrong one.
  assert.equal(toneFor("unverified"), "warn");
  assert.equal(toneFor("flaky"), "warn");
  // Case, because the badge renders whatever the column holds.
  assert.equal(toneFor("UNVERIFIED"), "warn");
});

test("toneFor's existing answers are unchanged by that", () => {
  // The whole console badges through this function, so the regression to watch
  // for is the fix moving something else. One assertion per family.
  assert.equal(toneFor("pass"), "pass");
  assert.equal(toneFor("verified"), "pass");
  assert.equal(toneFor("ready"), "pass");
  assert.equal(toneFor("failed"), "fail");
  assert.equal(toneFor("revoked"), "fail");
  assert.equal(toneFor("running"), "warn");
  assert.equal(toneFor("provisioning"), "warn");
  assert.equal(toneFor("torn_down"), "neutral");
  assert.equal(toneFor("complete"), "neutral");
  assert.equal(toneFor(null), "neutral");
  assert.equal(toneFor(""), "neutral");
});

/* -------------------------------------------------------------------------
 * The poll, and the two things it must not do.
 * ---------------------------------------------------------------------- */

test("a queued or running run is asked about again", () => {
  // `queued` is the half that would have been missed by reusing the load
  // feature's `isRunning`, whose vocabulary is a different Postgres enum and
  // does not contain the word. A run dispatched and not yet picked up is the
  // state a reader watches for longest.
  assert.equal(runIsInFlight("queued"), true);
  assert.equal(runIsInFlight("running"), true);
});

test("a finished run is never polled again", () => {
  // Poll forever and a tab left open is a request every six seconds until the
  // laptop closes.
  assert.equal(runIsInFlight("complete"), false);
  assert.equal(runIsInFlight("failed"), false);
  assert.equal(runIsInFlight("cancelled"), false);
  // And a state this console does not know stops rather than spins.
  assert.equal(runIsInFlight("something_new"), false);
  assert.equal(runIsInFlight(null), false);
  assert.equal(runIsInFlight(undefined), false);
});

test("the run state vocabulary is the one the migration declares", () => {
  // The instrument that can say no. `run_state` is declared once, in SQL, and
  // this reads it from there rather than from a list somebody typed twice. A
  // sixth word added to the enum fails here, which is the only way the console
  // finds out at all.
  const sql = readFileSync(
    new URL("../../web/packages/db/migrations/0001_init.sql", import.meta.url),
    "utf8",
  );
  const m = /CREATE TYPE run_state AS ENUM \(([^)]*)\)/.exec(sql);
  assert.notEqual(m, null, "run_state is no longer declared where this test looks");
  const declared = [...m![1].matchAll(/'([a-z_]+)'/g)].map((x) => x[1]!);
  assert.equal(declared.length, 5);
  for (const word of declared) {
    assert.notEqual(runStateOf(word), null, `${word} is in the enum and not in runshapes`);
  }
  // And nothing extra: a word this file accepts that the column cannot hold is
  // dead code pretending to be tolerance.
  for (const invented of ["succeeded", "requested", "accepted", "timed_out", "abandoned"]) {
    assert.equal(
      runStateOf(invented),
      null,
      `${invented} belongs to workload_run_state, not run_state`,
    );
  }
});

/* -------------------------------------------------------------------------
 * What the empty state is allowed to claim.
 * ---------------------------------------------------------------------- */

test("the empty state stops telling a finished run it is probably still going", () => {
  // The wording this replaces covered both cases and hedged: "usually means it
  // is still going or it failed before the first workflow". A run whose
  // personas all failed to provision sat on that sentence forever.
  const going = noVerdictsReason("running");
  assert.match(going, /still going/);
  assert.match(going, /refreshes on its own/);

  const done = noVerdictsReason("complete");
  assert.doesNotMatch(done, /still going/);
  assert.match(done, /nothing about your application was checked/i);
  assert.match(done, /persona could not be created/);

  const unknown = noVerdictsReason("something_new");
  assert.doesNotMatch(unknown, /still going/);
  assert.match(unknown, /not one this console recognises/);
});

/* -------------------------------------------------------------------------
 * The reproduction, which was fetched and dropped.
 * ---------------------------------------------------------------------- */

test("the reproduction is printed as the runner recorded it", () => {
  assert.equal(
    reproductionText({ command: "af test --workflow checkout" }),
    '{\n  "command": "af test --workflow checkout"\n}',
  );
  // jsonb, so a bare string is a shape it can arrive in.
  assert.equal(reproductionText("af test --workflow checkout"), "af test --workflow checkout");
});

test("a verdict with no reproduction says so rather than printing null", () => {
  assert.equal(reproductionText(null), null);
  assert.equal(reproductionText(undefined), null);
  assert.equal(reproductionText("   "), null);
});

/* -------------------------------------------------------------------------
 * Defined is not wired. This is the pair for the four above.
 * ---------------------------------------------------------------------- */

test("the runs page actually calls each of these", () => {
  // A function with no call site is a dead, shippable gap that looks like a
  // working feature, and every test above this line passes just as happily
  // with the page rendering none of it. This is a source read and it says so:
  // it proves the wiring exists, not that the pixels are right. A render test
  // would replace it.
  const page = readFileSync(new URL("../app/(app)/runs/page.tsx", import.meta.url), "utf8");
  for (const wired of [
    "nothingWasVerifiedNotice",
    "noVerdictsReason",
    "reproductionText",
    "runIsInFlight",
    "useInterval",
  ]) {
    assert.ok(page.includes(wired), `runs/page.tsx no longer uses ${wired}`);
  }
  // The OPENING TAG, not the name. Counting the bare name would have passed
  // with the render deleted: `function Reproduction` and the column's own
  // `<Td label="Reproduction">` are two occurrences on their own, and a guard
  // satisfied by the thing it is guarding cannot say no.
  for (const rendered of ["<NothingVerified", "<Reproduction"]) {
    assert.ok(page.includes(rendered), `${rendered} is defined in runs/page.tsx and never rendered`);
  }
  // The poll must be conditional, at EVERY call site. Written as the absence of
  // an unconditional one rather than the presence of a conditional one: with two
  // polls on this page, "at least one is gated" is satisfied while the other
  // hammers a finished run forever.
  assert.ok(!/useInterval\(\s*true/.test(page), "the runs page polls unconditionally");
  assert.ok(
    (page.match(/runIsInFlight/g) ?? []).length >= 3,
    "the list and the detail must each gate their own poll",
  );
});

/* -------------------------------------------------------------------------
 * The LIST column, which reaches the page as server counts rather than rows.
 *
 * `nothingWasVerifiedNotice` above guards the run DETAIL, which holds verdict
 * rows. The list holds only the aggregate counts `runs.recent` sends, so it has
 * its own helper, and the two must agree about one run: the list said "N
 * passing" whenever nothing was failing, so a run whose verdicts were all
 * blocked or unverified drew green on the surface a customer scans first while
 * the detail banner said nothing was verified. Each test below is one cell of
 * `recentRunSummary`, and each was mutation checked: the production line broken,
 * the exact test confirmed red, restored, confirmed green.
 * ---------------------------------------------------------------------- */

test("the list reads five passes as five passing, in the pass tone", () => {
  assert.deepEqual(recentRunSummary({ total: 5, passing: 5, failing: 0, proved: 5 }), {
    tone: "pass",
    text: "5 passing",
  });
});

test("the list names a failure first, in the fail tone", () => {
  assert.deepEqual(recentRunSummary({ total: 5, passing: 3, failing: 2, proved: 5 }), {
    tone: "fail",
    text: "2 of 5 failing",
  });
});

test("the list calls a run of five unverified verdicts nothing verified, never passing", () => {
  // THE DEMO LIE. This tally used to render green "5 passing" on the most
  // scanned surface while the detail banner said nothing was verified.
  const out = recentRunSummary({ total: 5, passing: 0, failing: 0, proved: 0 });
  assert.deepEqual(out, { tone: "warn", text: "nothing verified" });
  assert.notEqual(out?.tone, "pass");
});

test("the list calls five flaky zero of five passed, not nothing verified", () => {
  // Flaky is conclusive, so the run PROVED something and is not "nothing
  // verified"; it is not a pass either, so it is not green. This is the line
  // that separates proved-nothing from proved-but-not-passing, consistent with
  // the detail banner declining to fire on a run that has findings.
  assert.deepEqual(recentRunSummary({ total: 5, passing: 0, failing: 0, proved: 5 }), {
    tone: "warn",
    text: "0 of 5 passed",
  });
});

test("the list calls three passes and two unverified three of five passed", () => {
  assert.deepEqual(recentRunSummary({ total: 5, passing: 3, failing: 0, proved: 3 }), {
    tone: "warn",
    text: "3 of 5 passed",
  });
});

test("the list calls five blocked nothing verified", () => {
  // Blocked is not conclusive, exactly like unverified: the work never reached
  // the application, so a run made only of blocked verdicts proved nothing.
  assert.deepEqual(recentRunSummary({ total: 5, passing: 0, failing: 0, proved: 0 }), {
    tone: "warn",
    text: "nothing verified",
  });
});

test("the list says nothing about a run with no verdicts, so the cell is a dash", () => {
  assert.equal(recentRunSummary({ total: 0, passing: 0, failing: 0, proved: 0 }), null);
});
