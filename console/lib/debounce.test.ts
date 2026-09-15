import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { debounce } from "./debounce.ts";

describe("a filter that waits for the typing to stop", () => {
  it("sends one call for a word typed letter by letter", (t) => {
    t.mock.timers.enable({ apis: ["setTimeout"] });
    const calls: string[] = [];
    const send = debounce((value: string) => calls.push(value), 250);
    for (const prefix of ["b", "bi", "bil", "bill", "billi", "billin", "billing"]) send(prefix);
    assert.deepEqual(calls, [], "nothing goes out while the reader is still typing");
    t.mock.timers.tick(250);
    assert.deepEqual(calls, ["billing"], "one call, carrying what was typed last");
  });

  it("waits again when a keystroke lands inside the window", (t) => {
    t.mock.timers.enable({ apis: ["setTimeout"] });
    const calls: string[] = [];
    const send = debounce((value: string) => calls.push(value), 250);
    send("bil");
    t.mock.timers.tick(200);
    send("billing");
    t.mock.timers.tick(200);
    assert.deepEqual(calls, [], "the second keystroke restarted the wait");
    t.mock.timers.tick(50);
    assert.deepEqual(calls, ["billing"]);
  });

  it("sends a later call after an earlier one has gone out", (t) => {
    t.mock.timers.enable({ apis: ["setTimeout"] });
    const calls: string[] = [];
    const send = debounce((value: string) => calls.push(value), 250);
    send("billing");
    t.mock.timers.tick(250);
    send("");
    t.mock.timers.tick(250);
    assert.deepEqual(calls, ["billing", ""], "clearing the filter is a call of its own");
  });

  it("cancels a pending call, so a page that has gone away sends nothing", (t) => {
    t.mock.timers.enable({ apis: ["setTimeout"] });
    const calls: string[] = [];
    const send = debounce((value: string) => calls.push(value), 250);
    send("billing");
    send.cancel();
    t.mock.timers.tick(1000);
    assert.deepEqual(calls, []);
  });
});
