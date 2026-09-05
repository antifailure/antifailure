import { test } from "node:test";
import assert from "node:assert/strict";
import { describeSetup, stillConnecting, type RepositorySetup } from "./setup.ts";

function row(over: Partial<RepositorySetup>): RepositorySetup {
  return {
    id: "1",
    repository: "acme/app",
    state: "queued",
    attempts: 0,
    branch: null,
    pull_request_number: null,
    pull_request_url: null,
    last_error: null,
    requested_at: "2026-09-01T00:00:00Z",
    finished_at: null,
    ...over,
  };
}

test("a queued or leased setup says a pull request is being opened", () => {
  assert.deepEqual(
    [describeSetup(row({ state: "queued" })).label, describeSetup(row({ state: "leased" })).label],
    ["Opening a pull request", "Opening a pull request"],
  );
});

test("an opened setup names the pull request and links to it", () => {
  const line = describeSetup(
    row({
      state: "opened",
      pull_request_number: 42,
      pull_request_url: "https://github.com/acme/app/pull/42",
    }),
  );
  assert.equal(line.label, "Pull request #42 opened");
  assert.equal(line.href, "https://github.com/acme/app/pull/42");
  assert.equal(line.tone, "pass");
});

test("a present workflow is a pass with nothing to do", () => {
  const line = describeSetup(row({ state: "present" }));
  assert.equal(line.label, "Workflow present");
  assert.equal(line.tone, "pass");
  assert.equal(line.href, null);
  assert.equal(line.detail, null);
});

test("a missing permission names the grant and carries the remedy", () => {
  const remedy = "Open the App's settings, Permissions and events, and Accept new permissions.";
  const line = describeSetup(row({ state: "needs_permission", last_error: remedy }));
  assert.equal(line.label, "Needs Contents: write on the App installation");
  assert.equal(line.tone, "warn");
  assert.equal(line.detail, remedy);
});

test("a failure says so and carries the last error", () => {
  const line = describeSetup(row({ state: "failed", last_error: "GitHub answered 502." }));
  assert.equal(line.label, "Could not open a pull request");
  assert.equal(line.tone, "fail");
  assert.equal(line.detail, "GitHub answered 502.");
});

test("a state this console does not know is shown as it arrived, not hidden", () => {
  const line = describeSetup(row({ state: "waiting_for_something" }));
  assert.equal(line.label, "waiting for something");
  assert.equal(line.tone, "neutral");
});

test("the card shows while any repository is not yet checked", () => {
  assert.equal(stillConnecting([]), false);
  assert.equal(stillConnecting([row({ state: "present" })]), false);
  assert.equal(stillConnecting([row({ state: "present" }), row({ state: "opened" })]), true);
  assert.equal(stillConnecting([row({ state: "needs_permission" })]), true);
});
