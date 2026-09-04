import { readFileSync } from "node:fs";
import { test } from "node:test";
import assert from "node:assert/strict";
import { isCurrentResponse, shouldRefreshSession } from "./session-freshness.ts";

const apiSource = readFileSync(new URL("api.ts", import.meta.url), "utf8");

test("window focus refreshes the shared session", () => {
  assert.equal(shouldRefreshSession("focus", "hidden"), true);
});

test("only a visible document restoration refreshes the shared session", () => {
  assert.deepEqual(
    [
      shouldRefreshSession("visibilitychange", "visible"),
      shouldRefreshSession("visibilitychange", "hidden"),
    ],
    [true, false],
  );
});

test("only the newest response from a mounted provider is current", () => {
  assert.deepEqual(
    [
      isCurrentResponse(true, 4, 4),
      isCurrentResponse(true, 3, 4),
      isCurrentResponse(false, 4, 4),
    ],
    [true, false, false],
  );
});

test("the session request bypasses the browser cache", () => {
  // The API integration suite checks the matching response header. This side
  // makes the browser revalidate even if an older deployment omitted it.
  assert.match(
    apiSource,
    /rest<Session>\("\/auth\/session", \{ cache: "no-store" \}\)/,
  );
});
