// The reducer is the whole correctness surface of the live view, so the tests
// below pin the behaviours a watcher actually depends on: events fold in first
// seen order, a frame that arrives late and out of order never replaces a newer
// one, one malformed event does not blank the collection, and the transport and
// tone choices are the ones the panes render from.

import { test, describe } from "node:test";
import assert from "node:assert/strict";
import {
  parseLiveLine,
  reduce,
  reduceAll,
  initialState,
  transportFor,
  statusChipTone,
  statusChipLabel,
  STEP_CAP,
  type LiveEvent,
} from "./live.ts";

const at = "2026-09-15T12:00:00.000Z";

describe("parseLiveLine", () => {
  test("reads a well formed event", () => {
    const ev = parseLiveLine('{"t":"hello","run":"r","at":"x","protocol":1}');
    assert.equal(ev?.t, "hello");
  });
  test("returns undefined for blank and malformed lines", () => {
    assert.equal(parseLiveLine(""), undefined);
    assert.equal(parseLiveLine("   "), undefined);
    assert.equal(parseLiveLine("{not json"), undefined);
  });
  test("refuses an object with no string t", () => {
    assert.equal(parseLiveLine('{"x":1}'), undefined);
  });
});

describe("reduce", () => {
  test("hello sets run and protocol", () => {
    const s = reduce(initialState, { t: "hello", run: "run-1", at, protocol: 1 });
    assert.equal(s.run, "run-1");
    assert.equal(s.protocol, 1);
  });

  test("agents keep first seen order", () => {
    const s = reduceAll([
      { t: "agent", at, agent: "b", surface: "web", state: "pending" },
      { t: "agent", at, agent: "a", surface: "web", state: "pending" },
      { t: "agent", at, agent: "c", surface: "terminal", state: "pending" },
    ]);
    assert.deepEqual(s.agents.map((x) => x.id), ["b", "a", "c"]);
  });

  test("an agent event updates state and verdict without reordering", () => {
    const s = reduceAll([
      { t: "agent", at, agent: "a", surface: "web", state: "pending" },
      { t: "agent", at, agent: "b", surface: "web", state: "pending" },
      { t: "agent", at, agent: "a", surface: "web", state: "ended", verdict: "fail" },
    ]);
    assert.deepEqual(s.agents.map((x) => x.id), ["a", "b"]);
    assert.equal(s.agents[0]!.state, "ended");
    assert.equal(s.agents[0]!.verdict, "fail");
  });

  test("a step lands on its agent and counts", () => {
    const s = reduceAll([
      { t: "agent", at, agent: "a", surface: "web", state: "live" },
      { t: "step", at, agent: "a", seq: 1, text: "Open /", action: "goto" },
      { t: "step", at, agent: "a", seq: 2, text: "Press Continue", action: "click" },
    ]);
    assert.equal(s.agents[0]!.stepCount, 2);
    assert.equal(s.agents[0]!.steps.at(-1)!.text, "Press Continue");
  });

  test("a step before its agent event creates the agent", () => {
    const s = reduce(initialState, { t: "step", at, agent: "z", seq: 1, text: "early" });
    assert.equal(s.agents.length, 1);
    assert.equal(s.agents[0]!.id, "z");
    assert.equal(s.agents[0]!.stepCount, 1);
  });

  test("the latest frame is kept and shown", () => {
    const s = reduceAll([
      { t: "agent", at, agent: "a", surface: "web", state: "live" },
      { t: "frame", at, agent: "a", seq: 2, mime: "image/jpeg", w: 4, h: 3, b64: "AAAA" },
      { t: "frame", at, agent: "a", seq: 5, mime: "image/jpeg", w: 4, h: 3, b64: "BBBB" },
    ]);
    assert.equal(s.agents[0]!.lastFrame!.b64, "BBBB");
    assert.equal(s.agents[0]!.lastFrame!.seq, 5);
  });

  test("a late lower sequence frame does NOT replace a newer one", () => {
    const s = reduceAll([
      { t: "agent", at, agent: "a", surface: "web", state: "live" },
      { t: "frame", at, agent: "a", seq: 5, mime: "image/jpeg", w: 4, h: 3, b64: "NEW" },
      // arrives late, out of order, from a slower path
      { t: "frame", at, agent: "a", seq: 3, mime: "image/jpeg", w: 4, h: 3, b64: "OLD" },
    ]);
    // The pane must still show the seq 5 frame; the stale one is dropped.
    assert.equal(s.agents[0]!.lastFrame!.b64, "NEW");
    assert.equal(s.agents[0]!.lastFrame!.seq, 5);
  });

  test("a frame with no bytes is refused", () => {
    const s = reduceAll([
      { t: "agent", at, agent: "a", surface: "web", state: "live" },
      { t: "frame", at, agent: "a", seq: 1, mime: "image/jpeg", w: 4, h: 3, b64: "" },
    ]);
    assert.equal(s.agents[0]!.lastFrame, undefined);
  });

  test("one malformed event does not blank the collection", () => {
    // An agent event missing its id, folded between two good ones. The bad one
    // is skipped and both real agents survive.
    const events: LiveEvent[] = [
      { t: "agent", at, agent: "a", surface: "web", state: "live" },
      { t: "agent", at, surface: "web", state: "live" } as unknown as LiveEvent,
      { t: "agent", at, agent: "b", surface: "web", state: "live" },
    ];
    const s = reduceAll(events);
    assert.deepEqual(s.agents.map((x) => x.id), ["a", "b"]);
  });

  test("done sets the counts", () => {
    const s = reduce(initialState, {
      t: "done", run: "r", at, passed: 2, failed: 1, flaky: 0, blocked: 0, unverified: 3,
    });
    assert.equal(s.counts!.passed, 2);
    assert.equal(s.counts!.unverified, 3);
  });

  test("steps are capped so a long run does not grow without bound", () => {
    const events: LiveEvent[] = [{ t: "agent", at, agent: "a", surface: "web", state: "live" }];
    for (let i = 1; i <= STEP_CAP + 25; i++) {
      events.push({ t: "step", at, agent: "a", seq: i, text: `step ${i}` });
    }
    const s = reduceAll(events);
    assert.equal(s.agents[0]!.steps.length, STEP_CAP);
    assert.equal(s.agents[0]!.stepCount, STEP_CAP + 25);
    // The most recent step is kept; the oldest is dropped.
    assert.equal(s.agents[0]!.steps.at(-1)!.text, `step ${STEP_CAP + 25}`);
  });
});

describe("transportFor", () => {
  test("a live web agent with a frame shows frames", () => {
    assert.equal(transportFor("web", "live", true), "frames");
  });
  test("a live web agent with no frame yet is connecting", () => {
    assert.equal(transportFor("web", "live", false), "connecting");
  });
  test("a terminal agent renders its cast, never pixels", () => {
    assert.equal(transportFor("terminal", "live", true), "cast");
  });
  test("an ended agent shows the ended view", () => {
    assert.equal(transportFor("web", "ended", true), "ended");
    assert.equal(transportFor("terminal", "ended", false), "ended");
  });
  test("an error is an ended view", () => {
    assert.equal(transportFor("web", "error", false), "ended");
  });
  test("a pending or connecting agent is connecting", () => {
    assert.equal(transportFor("web", "pending", false), "connecting");
    assert.equal(transportFor("web", "connecting", false), "connecting");
  });
});

describe("statusChipTone and label", () => {
  test("live is a pass tone", () => {
    assert.equal(statusChipTone("live"), "pass");
  });
  test("error is a fail tone", () => {
    assert.equal(statusChipTone("error"), "fail");
  });
  test("ended passes, fails and warns by verdict", () => {
    assert.equal(statusChipTone("ended", "pass"), "pass");
    assert.equal(statusChipTone("ended", "fail"), "fail");
    assert.equal(statusChipTone("ended", "flaky"), "warn");
    assert.equal(statusChipTone("ended", "unverified"), "warn");
    assert.equal(statusChipTone("ended", "blocked"), "warn");
  });
  test("pending is neutral", () => {
    assert.equal(statusChipTone("pending"), "neutral");
  });
  test("the label is the verdict once ended, else the state", () => {
    assert.equal(statusChipLabel("ended", "pass"), "pass");
    assert.equal(statusChipLabel("live"), "live");
  });
});
