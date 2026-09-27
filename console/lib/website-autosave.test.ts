import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { emptyWebsiteDocument, type WebsiteDocument } from "@antifailure/website";
import {
  createWebsiteAutosave, type WebsiteSaveInput, type WebsiteSaveResult,
  type WebsiteAutosaveSnapshot,
} from "./website-autosave.ts";

function page(title = ""): WebsiteDocument {
  const document = emptyWebsiteDocument();
  if (title) document.fields["hero.title"] = title;
  return document;
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

function failure(status: number, code?: string) {
  return Object.assign(new Error(status === 409 ? "Changed in another editor" : "Save failed"), { status, code });
}

function fakeClock() {
  let now = 0;
  let id = 0;
  const jobs = new Map<number, { at: number; run: () => void }>();
  return {
    scheduler: {
      set(run: () => void, delay: number) { jobs.set(++id, { at: now + delay, run }); return id; },
      clear(handle: unknown) { jobs.delete(handle as number); },
    },
    advance(ms: number) {
      now += ms;
      const ready = [...jobs].filter(([, job]) => job.at <= now);
      for (const [key, job] of ready) {
        jobs.delete(key);
        job.run();
      }
    },
    get count() { return jobs.size; },
  };
}

function setup(initialDocument = page(), initialRevision = 0) {
  const clock = fakeClock();
  const calls: Array<{ input: WebsiteSaveInput; result: ReturnType<typeof deferred<WebsiteSaveResult>> }> = [];
  const notifications: WebsiteAutosaveSnapshot[] = [];
  let id = 0;
  const autosave = createWebsiteAutosave({
    initialDocument, initialRevision, debounceMs: 500, scheduler: clock.scheduler,
    requestId: () => `00000000-0000-4000-8000-${String(++id).padStart(12, "0")}`,
    save(input) {
      const result = deferred<WebsiteSaveResult>();
      calls.push({ input, result });
      return result.promise;
    },
    onChange: (snapshot) => notifications.push(snapshot),
  });
  function accept(index: number, draftRevision: number, document = calls[index]!.input.document) {
    calls[index]!.result.resolve({ draftRevision, document });
  }
  return { autosave, clock, calls, notifications, accept };
}

/** Advances promise continuations only. Tests control every transport result
 * and every debounce timer rather than waiting for wall-clock time. */
async function continuations() {
  for (let index = 0; index < 12; index++) await Promise.resolve();
}

describe("website edits and saves in each reachable ordering", () => {
  it("edit then save: coalesces typing into one draft containing only overrides", async () => {
    const { autosave, clock, calls, accept } = setup();
    autosave.edit(page("A"));
    clock.advance(499);
    autosave.edit(page("Antifailure"));
    clock.advance(499);
    await continuations();
    assert.equal(calls.length, 0);
    clock.advance(1);
    await continuations();
    assert.equal(calls.length, 1);
    assert.equal(calls[0]!.input.expectedRevision, 0);
    assert.deepEqual(calls[0]!.input.document.fields, { "hero.title": "Antifailure" });
    accept(0, 1);
    await continuations();
    assert.equal(autosave.snapshot().status, "saved");
    assert.equal(autosave.snapshot().revision, 1);
  });

  it("save then edit: a clean flush is a no-op and the later edit starts a save", async () => {
    const { autosave, clock, calls, accept } = setup(page("Original"), 7);
    assert.equal((await autosave.flush()).revision, 7);
    assert.equal(calls.length, 0);
    autosave.edit(page("Later"));
    clock.advance(500);
    await continuations();
    accept(0, 8);
    await continuations();
    assert.equal(autosave.snapshot().document.fields["hero.title"], "Later");
  });

  it("edit without save: undo before the debounce expires sends nothing", async () => {
    const { autosave, clock, calls } = setup();
    autosave.edit(page("Temporary"));
    autosave.edit(page());
    clock.advance(5_000);
    await continuations();
    assert.equal(calls.length, 0);
    assert.equal(autosave.snapshot().hasUnsavedChanges, false);
    assert.equal(autosave.snapshot().status, "saved");
  });

  it("edit during save: the old response never replaces the newer local document", async () => {
    const { autosave, calls, accept } = setup();
    autosave.edit(page("First"));
    const flushed = autosave.flush();
    await continuations();
    autosave.edit(page("Second"));
    autosave.edit(page("Third"));
    assert.equal(calls.length, 1, "there is only one in-flight write");
    accept(0, 1);
    await continuations();
    assert.equal(autosave.snapshot().document.fields["hero.title"], "Third");
    assert.equal(calls.length, 2, "intermediate edits are coalesced");
    assert.equal(calls[1]!.input.expectedRevision, 1);
    assert.equal(calls[1]!.input.document.fields["hero.title"], "Third");
    accept(1, 2);
    const saved = await flushed;
    assert.equal(saved.revision, 2);
    assert.equal(saved.hasUnsavedChanges, false);
  });

  it("undo during save: writes the original value back after the first save commits", async () => {
    const { autosave, calls, accept } = setup(page("Original"), 4);
    autosave.edit(page("Changed"));
    const flushed = autosave.flush();
    await continuations();
    autosave.edit(page("Original"));
    assert.equal(autosave.snapshot().hasUnsavedChanges, true, "an unacknowledged write may still commit");
    accept(0, 5);
    await continuations();
    assert.equal(calls[1]!.input.document.fields["hero.title"], "Original");
    assert.equal(calls[1]!.input.expectedRevision, 5);
    accept(1, 6);
    assert.equal((await flushed).status, "saved");
  });

  it("redo during save: returning to the pending value needs no duplicate save", async () => {
    const { autosave, calls, accept } = setup();
    autosave.edit(page("A"));
    const flushed = autosave.flush();
    await continuations();
    autosave.edit(page("B"));
    autosave.edit(page("A"));
    accept(0, 1);
    await flushed;
    assert.equal(calls.length, 1);
  });

  it("concurrent flush calls join the same writer and both wait for later edits", async () => {
    const { autosave, calls, accept } = setup();
    autosave.edit(page("A"));
    const first = autosave.flush();
    const second = autosave.flush();
    await continuations();
    assert.equal(calls.length, 1);
    autosave.edit(page("B"));
    accept(0, 1);
    await continuations();
    assert.equal(calls.length, 2);
    accept(1, 2);
    const results = await Promise.all([first, second]);
    assert.deepEqual(results.map((result) => result.revision), [2, 2]);
    assert.equal(calls.length, 2);
  });

  it("automatic save completion schedules a later edit even without flush", async () => {
    const { autosave, clock, calls, accept } = setup();
    autosave.edit(page("A"));
    clock.advance(500);
    await continuations();
    autosave.edit(page("B"));
    clock.advance(1_000);
    assert.equal(calls.length, 1);
    accept(0, 1);
    await continuations();
    clock.advance(500);
    await continuations();
    assert.equal(calls[1]!.input.document.fields["hero.title"], "B");
    accept(1, 2);
    await continuations();
    assert.equal(autosave.snapshot().status, "saved");
  });
});

describe("uncertain delivery and explicit conflict recovery", () => {
  it("lost response then later edit: retries the same UUID and payload before the newer edit", async () => {
    const { autosave, calls, accept } = setup();
    autosave.edit(page("Possibly committed"));
    const first = autosave.flush();
    const refused = assert.rejects(first, /Save failed/);
    await continuations();
    autosave.edit(page("Still local"));
    calls[0]!.result.reject(failure(0));
    await refused;
    const failed = autosave.snapshot();
    assert.equal(failed.status, "error");
    assert.equal(failed.document.fields["hero.title"], "Still local");
    const retried = autosave.retry();
    await continuations();
    assert.strictEqual(calls[1]!.input, calls[0]!.input, "retry preserves the complete idempotent request");
    accept(1, 1);
    await continuations();
    assert.notEqual(calls[2]!.input.requestId, calls[0]!.input.requestId);
    assert.equal(calls[2]!.input.expectedRevision, 1);
    assert.equal(calls[2]!.input.document.fields["hero.title"], "Still local");
    accept(2, 2);
    assert.equal((await retried).status, "saved");
  });

  it("a server error may follow a commit, so retry reuses its original request", async () => {
    const { autosave, calls, accept } = setup();
    autosave.edit(page("A"));
    const refusal = assert.rejects(autosave.flush(), /Save failed/);
    await continuations();
    calls[0]!.result.reject(failure(500));
    await refusal;
    const retry = autosave.retry();
    await continuations();
    assert.strictEqual(calls[1]!.input, calls[0]!.input);
    accept(1, 1);
    await retry;
  });

  it("editing after a network failure does not silently discard the unresolved write", async () => {
    const { autosave, clock, calls } = setup();
    autosave.edit(page("A"));
    const refused = assert.rejects(autosave.flush());
    await continuations();
    calls[0]!.result.reject(new TypeError("Network unavailable"));
    await refused;
    autosave.edit(page("B"));
    clock.advance(5_000);
    await continuations();
    assert.equal(calls.length, 1);
    assert.equal(autosave.snapshot().status, "error");
    await assert.rejects(autosave.flush(), /Network unavailable/);
  });

  it("a definitive validation refusal allows a corrected edit with a new request", async () => {
    const { autosave, clock, calls, accept } = setup();
    autosave.edit(page("Rejected by server"));
    const refused = assert.rejects(autosave.flush());
    await continuations();
    calls[0]!.result.reject(failure(400));
    await refused;
    autosave.edit(page("Corrected"));
    clock.advance(500);
    await continuations();
    assert.notEqual(calls[1]!.input.requestId, calls[0]!.input.requestId);
    assert.equal(calls[1]!.input.expectedRevision, 0);
    accept(1, 1);
    await continuations();
    assert.equal(autosave.snapshot().status, "saved");
  });

  it("concurrent editors: a conflict stays visible through further edits and retry", async () => {
    const { autosave, clock, calls } = setup(page("Original"), 5);
    autosave.edit(page("Mine"));
    const refused = assert.rejects(autosave.flush(), /Changed in another editor/);
    await continuations();
    calls[0]!.result.reject(failure(409, "CONFLICT"));
    await refused;
    autosave.edit(page("More of mine"));
    clock.advance(5_000);
    await continuations();
    assert.equal(autosave.snapshot().status, "conflict");
    await assert.rejects(autosave.retry(), /Changed in another editor/);
    assert.equal(calls.length, 1);
    autosave.reload(page("Other editor"), 6);
    assert.equal(autosave.snapshot().document.fields["hero.title"], "Other editor");
    assert.equal(autosave.snapshot().status, "saved");
    assert.equal(autosave.snapshot().revision, 6);
  });

  it("conflict then deliberate reapply preserves local edits against the fetched revision", async () => {
    const { autosave, calls, accept } = setup(page("Original"), 5);
    autosave.edit(page("Mine"));
    const refused = assert.rejects(autosave.flush());
    await continuations();
    calls[0]!.result.reject(failure(409));
    await refused;
    autosave.reapply(page("Other editor"), 6);
    assert.equal(autosave.snapshot().document.fields["hero.title"], "Mine");
    const saved = autosave.flush();
    await continuations();
    assert.equal(calls[1]!.input.expectedRevision, 6);
    assert.notEqual(calls[1]!.input.requestId, calls[0]!.input.requestId);
    accept(1, 7);
    await saved;
  });

  it("a code-only tRPC conflict is handled even without an HTTP status", async () => {
    const { autosave, calls } = setup();
    autosave.edit(page("Mine"));
    const refused = assert.rejects(autosave.flush());
    await continuations();
    calls[0]!.result.reject(Object.assign(new Error("Conflict"), { code: "CONFLICT" }));
    await refused;
    assert.equal(autosave.snapshot().status, "conflict");
  });

  it("invalid success data remains an uncertain request rather than erasing the draft", async () => {
    const { autosave, calls, accept } = setup(page("Original"), 5);
    autosave.edit(page("Mine"));
    const refused = assert.rejects(autosave.flush(), /older website revision/);
    await continuations();
    accept(0, 4);
    await refused;
    assert.equal(autosave.snapshot().revision, 5);
    assert.equal(autosave.snapshot().document.fields["hero.title"], "Mine");
    const retry = autosave.retry();
    await continuations();
    assert.strictEqual(calls[1]!.input, calls[0]!.input);
    accept(1, 6);
    await retry;
  });
});

describe("publishing, restoring, and closing the editor", () => {
  it("suspend then flush: publishing sees the exact saved revision and edits stay frozen", async () => {
    const { autosave, clock, calls, accept } = setup();
    autosave.edit(page("Ready to publish"));
    autosave.suspend();
    assert.throws(() => autosave.edit(page("Too late")), /publishing or restoring/);
    clock.advance(5_000);
    await continuations();
    assert.equal(calls.length, 0, "suspension cancels the automatic timer");
    const flushed = autosave.flush();
    await continuations();
    accept(0, 1);
    const ready = await flushed;
    assert.equal(ready.revision, 1);
    assert.equal(ready.suspended, true);
    assert.equal(ready.hasUnsavedChanges, false);
    // The publish endpoint returns the unchanged draft and a published pointer.
    autosave.reload(ready.document, ready.revision);
    autosave.resume();
    assert.equal(autosave.snapshot().status, "saved");
  });

  it("save then suspend: an already running save completes before publishing", async () => {
    const { autosave, clock, calls, accept } = setup();
    autosave.edit(page("A"));
    clock.advance(500);
    await continuations();
    autosave.edit(page("B"));
    autosave.suspend();
    const flushed = autosave.flush();
    accept(0, 1);
    await continuations();
    assert.equal(calls[1]!.input.document.fields["hero.title"], "B");
    accept(1, 2);
    assert.equal((await flushed).revision, 2);
  });

  it("save failure while suspended prevents publication and retains editable recovery", async () => {
    const { autosave, calls } = setup();
    autosave.edit(page("A"));
    autosave.suspend();
    const refused = assert.rejects(autosave.flush());
    await continuations();
    calls[0]!.result.reject(failure(503));
    await refused;
    autosave.resume();
    assert.equal(autosave.snapshot().status, "error");
    assert.equal(autosave.snapshot().hasUnsavedChanges, true);
  });

  it("restore then edit: subsequent saves use the new restore revision", async () => {
    const { autosave, calls, accept } = setup(page("Current"), 8);
    autosave.suspend();
    await autosave.flush();
    autosave.reload(page("Restored"), 9);
    autosave.resume();
    autosave.edit(page("Edit after restore"));
    const flushed = autosave.flush();
    await continuations();
    assert.equal(calls[0]!.input.expectedRevision, 9);
    accept(0, 10);
    await flushed;
  });

  it("edit then restore: flush acknowledges the draft before loading restored content", async () => {
    const { autosave, calls, accept } = setup(page("Current"), 8);
    autosave.edit(page("Unpublished"));
    autosave.suspend();
    const flushed = autosave.flush();
    await continuations();
    accept(0, 9);
    await flushed;
    autosave.reload(page("Restored"), 10);
    autosave.resume();
    assert.equal(autosave.snapshot().revision, 10);
    assert.equal(autosave.snapshot().document.fields["hero.title"], "Restored");
    assert.equal(autosave.snapshot().status, "saved");
  });

  it("loading during a save or loading a stale version is refused", async () => {
    const { autosave, accept } = setup(page("Original"), 5);
    autosave.edit(page("Mine"));
    const flushed = autosave.flush();
    await continuations();
    assert.throws(() => autosave.reload(page("Other"), 6), /current save/);
    assert.throws(() => autosave.reapply(page("Other"), 6), /current save/);
    accept(0, 6);
    await flushed;
    assert.throws(() => autosave.reload(page("Stale"), 5), /older than the last saved/);
    assert.equal(autosave.snapshot().document.fields["hero.title"], "Mine");
  });

  it("close before save: no future request starts and the unsaved document remains recoverable", async () => {
    const { autosave, clock, calls } = setup();
    autosave.edit(page("Unsaved"));
    const closed = autosave.dispose();
    clock.advance(5_000);
    await continuations();
    assert.equal(calls.length, 0);
    assert.equal(closed.hasUnsavedChanges, true);
    assert.equal(closed.document.fields["hero.title"], "Unsaved");
    assert.throws(() => autosave.edit(page("B")), /closed/);
    await assert.rejects(autosave.flush(), /closed/);
  });

  it("close during save: its late acknowledgement updates truth without notifying unmounted UI", async () => {
    const { autosave, clock, notifications, accept } = setup();
    autosave.edit(page("Committed after close"));
    clock.advance(500);
    await continuations();
    let listenerCalls = 0;
    autosave.subscribe(() => { listenerCalls++; });
    autosave.dispose();
    const notificationCount = notifications.length;
    accept(0, 1);
    await continuations();
    assert.equal(autosave.snapshot().revision, 1);
    assert.equal(autosave.snapshot().hasUnsavedChanges, false);
    assert.equal(autosave.snapshot().status, "disposed");
    assert.equal(notifications.length, notificationCount);
    assert.equal(listenerCalls, 0);
  });

  it("edit then close during save: preserves the later unsaved draft without sending it", async () => {
    const { autosave, clock, calls, accept } = setup();
    autosave.edit(page("In flight"));
    clock.advance(500);
    await continuations();
    autosave.edit(page("Later local edit"));
    autosave.dispose();
    accept(0, 1);
    await continuations();
    clock.advance(5_000);
    await continuations();
    assert.equal(calls.length, 1);
    assert.equal(autosave.snapshot().revision, 1);
    assert.equal(autosave.snapshot().document.fields["hero.title"], "Later local edit");
    assert.equal(autosave.snapshot().hasUnsavedChanges, true);
  });

  it("close during a awaited flush rejects after preserving the acknowledged state", async () => {
    const { autosave, accept } = setup();
    autosave.edit(page("In flight"));
    const refused = assert.rejects(autosave.flush(), /closed/);
    await continuations();
    autosave.dispose();
    accept(0, 1);
    await refused;
    assert.equal(autosave.snapshot().revision, 1);
    assert.equal(autosave.snapshot().hasUnsavedChanges, false);
  });
});

describe("snapshot and payload integrity", () => {
  it("does not retain mutable references to caller documents or pending payloads", async () => {
    const initial = page("Original");
    const { autosave, calls, accept } = setup(initial);
    initial.fields["hero.title"] = "Outside mutation";
    assert.equal(autosave.snapshot().document.fields["hero.title"], "Original");
    const changed = page("Requested");
    autosave.edit(changed);
    const flushed = autosave.flush();
    changed.fields["hero.title"] = "Later outside mutation";
    await continuations();
    assert.equal(calls[0]!.input.document.fields["hero.title"], "Requested");
    assert.throws(() => { calls[0]!.input.document.fields["hero.title"] = "Bad transport mutation"; }, TypeError);
    accept(0, 1);
    await flushed;
  });

  it("adopts server normalization only when no newer edit is pending", async () => {
    const { autosave, accept } = setup();
    autosave.edit(page("Local"));
    const flushed = autosave.flush();
    await continuations();
    accept(0, 1, page("Normalized"));
    await flushed;
    assert.equal(autosave.snapshot().document.fields["hero.title"], "Normalized");
    assert.equal(autosave.snapshot().hasUnsavedChanges, false);
  });

  it("stable snapshots support subscriptions, equivalent edits, and unsubscribe", () => {
    const { autosave } = setup();
    const first = autosave.snapshot();
    assert.strictEqual(autosave.snapshot(), first);
    let calls = 0;
    const unsubscribe = autosave.subscribe(() => { calls++; });
    autosave.edit(page());
    assert.strictEqual(autosave.snapshot(), first);
    assert.equal(calls, 0);
    autosave.edit(page("Changed"));
    assert.notStrictEqual(autosave.snapshot(), first);
    assert.equal(calls, 1);
    unsubscribe();
    autosave.edit(page("Another change"));
    assert.equal(calls, 1);
  });
});
