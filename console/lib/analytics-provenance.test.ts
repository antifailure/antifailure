// Which of the three things the page says about recording.
//
// The two that matter are the ones that render the same otherwise: an
// installation that never recorded and one that has stopped both show a
// dashboard that is not moving, and only the second is a fault. Saying "off"
// for both is true and useless.
//
// One test per outcome rather than three assertions in one, because an
// assertion that throws stops the ones after it, and an outcome that was never
// checked looks exactly like one that passed.

import { describe, it } from "node:test";
import assert from "node:assert/strict";

import { recordingState } from "./analytics-provenance.ts";

describe("what the analytics page says about recording", () => {
  it("says nothing special while it is recording", () => {
    assert.equal(recordingState({ recording: true, recordingStopped: false }), "recording");
  });

  it("says it never recorded when it is off and has never recorded", () => {
    // Staging and self-hosted installations live here. This has to stay the
    // quiet message or the loud one stops meaning anything.
    assert.equal(recordingState({ recording: false, recordingStopped: false }), "never-recorded");
  });

  it("says it STOPPED when it is off and recorded before", () => {
    assert.equal(recordingState({ recording: false, recordingStopped: true }), "stopped");
  });

  it("stays on recording even if the server contradicts itself", () => {
    // recording true with recordingStopped true should not happen: the server
    // derives the second from the first. If it ever does, the page says the
    // system is working rather than showing an alarm it cannot explain, and
    // the honest signal is the one on the server, which logs.
    assert.equal(recordingState({ recording: true, recordingStopped: true }), "recording");
  });
});
